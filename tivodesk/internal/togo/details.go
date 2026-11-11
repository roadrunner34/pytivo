package togo

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Meta is a recording's details as "name: value" lines, in the format
// pyTivo reads from the .txt file beside a video. A name may repeat, as
// vActor does for each actor.
type Meta []MetaLine

// MetaLine is one line of a Meta.
type MetaLine struct{ Key, Value string }

// set replaces the first line with this key, or adds one. An empty value
// leaves things as they are.
func (m *Meta) set(key, value string) {
	if value == "" {
		return
	}
	for i := range *m {
		if (*m)[i].Key == key {
			(*m)[i].Value = value
			return
		}
	}
	*m = append(*m, MetaLine{key, value})
}

// Get returns the first value for a key.
func (m Meta) Get(key string) string {
	for _, line := range m {
		if line.Key == key {
			return line.Value
		}
	}
	return ""
}

// Write writes the lines as a pyTivo metadata file.
func (m Meta) Write(w io.Writer) error {
	var b bytes.Buffer
	for _, line := range m {
		fmt.Fprintf(&b, "%s: %s\n", line.Key, strings.ReplaceAll(line.Value, "\n", " "))
	}
	_, err := w.Write(b.Bytes())
	return err
}

// The words pyTivo writes for the rating numbers a TiVo sends.
var (
	tvRatings   = map[byte]string{'1': "Y7", '2': "Y", '3': "G", '4': "PG", '5': "14", '6': "MA", '7': "NR"}
	mpaaRatings = map[byte]string{'1': "G", '2': "PG", '3': "PG-13", '4': "R", '5': "X", '6': "NC-17", '8': "NR"}
	starRatings = map[byte]string{'1': "1", '2': "1.5", '3': "2", '4': "2.5", '5': "3", '6': "3.5", '7': "4"}
)

// rating turns a TiVo's rating code, such as "4" or "R4", into its word.
// A code it does not know is returned unchanged.
func rating(table map[byte]string, code string) string {
	for i := 0; i < len(code); i++ {
		if word, ok := table[code[i]]; ok {
			return word
		}
	}
	return code
}

// BasicMeta returns what the Now Playing list itself says about a
// recording. It is what gets saved when the full details cannot be fetched.
func BasicMeta(r Recording) Meta {
	var m Meta
	m.set("title", r.Title)
	m.set("episodeTitle", r.EpisodeTitle)
	m.set("episodeNumber", r.EpisodeNumber)
	m.set("description", r.Description)
	if !r.Captured.IsZero() {
		m.set("time", r.Captured.UTC().Format("2006-01-02T15:04:05Z"))
	}
	m.set("programId", r.ProgramID)
	m.set("seriesId", r.SeriesID)
	m.set("callsign", r.Station)
	major, minor, _ := strings.Cut(r.Channel, "-")
	m.set("displayMajorNumber", major)
	m.set("displayMinorNumber", minor)
	if r.TVRating != "" {
		m.set("tvRating", rating(tvRatings, r.TVRating))
	}
	if r.MPAARating != "" {
		m.set("mpaaRating", rating(mpaaRatings, r.MPAARating))
	}
	return m
}

// node is one element of an XML document.
type node struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*node
}

func parseXML(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	root := &node{}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			top.kids = append(top.kids, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top.text += string(t)
		}
	}
	return root, nil
}

// child follows a path of element names, each a direct child of the last.
func (n *node) child(path ...string) *node {
	for _, name := range path {
		var next *node
		for _, k := range n.kids {
			if k.name == name {
				next = k
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// find returns the first element with this name anywhere below n.
func (n *node) find(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
		if found := k.find(name); found != nil {
			return found
		}
	}
	return nil
}

func (n *node) textAt(path ...string) string {
	if c := n.child(path...); c != nil {
		return strings.TrimSpace(c.text)
	}
	return ""
}

// The lists of people and genres in a programme's details.
var listKeys = []string{
	"vActor", "vChoreographer", "vDirector", "vExecProducer", "vProgramGenre",
	"vGuestStar", "vHost", "vProducer", "vWriter",
}

// parseDetails adds what a TiVoVideoDetails document says to m.
func parseDetails(data []byte, m *Meta) error {
	root, err := parseXML(data)
	if err != nil {
		return fmt.Errorf("the TiVo's details were not valid XML: %w", err)
	}
	showing := root.find("showing")
	if showing == nil {
		return fmt.Errorf("the TiVo's details describe no recording")
	}
	program := showing.child("program")
	if program == nil {
		program = &node{}
	}

	clean := func(s string) string {
		return strings.TrimSuffix(descriptionNoise.Replace(s), " *")
	}
	m.set("title", program.textAt("title"))
	m.set("seriesTitle", program.textAt("series", "seriesTitle"))
	m.set("episodeTitle", program.textAt("episodeTitle"))
	m.set("episodeNumber", program.textAt("episodeNumber"))
	m.set("isEpisode", program.textAt("isEpisode"))
	m.set("description", clean(program.textAt("description")))
	m.set("time", showing.textAt("time"))
	m.set("originalAirDate", program.textAt("originalAirDate"))
	m.set("movieYear", program.textAt("movieYear"))
	m.set("programId", program.textAt("uniqueId"))
	m.set("seriesId", program.textAt("series", "uniqueId"))
	m.set("partCount", showing.textAt("partCount"))
	m.set("partIndex", showing.textAt("partIndex"))

	if n := showing.find("showingBits"); n != nil {
		m.set("showingBits", n.attrs["value"])
	}
	if n := showing.find("tvRating"); n != nil && n.attrs["value"] != "" {
		m.set("tvRating", rating(tvRatings, n.attrs["value"]))
	}
	if n := program.find("mpaaRating"); n != nil && n.attrs["value"] != "" {
		m.set("mpaaRating", rating(mpaaRatings, n.attrs["value"]))
	}
	if n := program.find("starRating"); n != nil && n.attrs["value"] != "" {
		m.set("starRating", rating(starRatings, n.attrs["value"]))
	}

	for _, key := range listKeys {
		list := program.find(key)
		if list == nil {
			continue
		}
		for _, el := range list.kids {
			if text := strings.TrimSpace(el.text); el.name == "element" && text != "" {
				*m = append(*m, MetaLine{key, text})
			}
		}
	}
	return nil
}

// Details returns everything the TiVo knows about a recording: what the
// Now Playing list says, filled out with the programme's full details
// (cast, genres, original air date and so on). If the details cannot be
// fetched, the basic information is returned along with the error.
func (c *Client) Details(ctx context.Context, r Recording) (Meta, error) {
	m := BasicMeta(r)
	if r.DetailsURL == "" {
		return m, nil
	}
	u, err := url.Parse(r.DetailsURL)
	if err != nil {
		return m, fmt.Errorf("the recording's details link is not usable: %w", err)
	}
	// Only the path is taken from the link; the request goes to the TiVo
	// this client was made for.
	body, err := c.get(ctx, u.RequestURI())
	if err != nil {
		return m, err
	}
	err = parseDetails(body, &m)
	return m, err
}
