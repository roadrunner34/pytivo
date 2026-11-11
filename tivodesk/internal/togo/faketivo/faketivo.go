// Package faketivo is a stand-in for a TiVo's web server, for tests. It
// speaks HTTPS with a self-signed certificate, demands Digest
// authentication with the Media Access Key, and serves a Now Playing list
// in pages, like the real thing. Recordings download from a second, plain
// HTTP port, as they do from port 80 of a real TiVo.
package faketivo

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Server is a fake TiVo. Change its exported fields only before the first
// request, or through its methods.
type Server struct {
	MAK string
	// MaxPerPage caps how many entries one request returns, to imitate a
	// TiVo that returns fewer than asked for. Zero means 50.
	MaxPerPage int

	mu         sync.Mutex
	count      int    // number of recordings
	lastChange string // the list's change marker
	busy       int    // how many more requests to answer 503
	requests   int    // authenticated requests served
	challenges int    // 401 challenges sent
	nonce      string

	packets     int           // video packets in each recording
	corrupt     int           // how many more downloads get damaged packets
	truncate    int           // how many more downloads are cut short
	plainDown   bool          // the plain port refuses downloads
	chunkDelay  time.Duration // pause between chunks of a download
	downloading bool          // a download is being sent
	downloads   []string      // request URIs of the downloads started

	srv   *httptest.Server // HTTPS: lists and details
	plain *httptest.Server // HTTP: recordings
}

// New starts a fake TiVo holding count recordings. It stops when the test
// ends.
func New(t testing.TB, mak string, count int) *Server {
	s := Start(mak, count)
	t.Cleanup(s.Close)
	return s
}

// Start starts a fake TiVo outside a test, for trying the app by hand. The
// caller must Close it.
func Start(mak string, count int) *Server {
	s := &Server{MAK: mak, count: count, lastChange: "0x1", nonce: "nonce-1", packets: 4000}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	s.plain = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// Close stops the server.
func (s *Server) Close() {
	s.srv.CloseClientConnections()
	s.plain.CloseClientConnections()
	s.srv.Close()
	s.plain.Close()
}

// DownloadHost returns the host:port that recordings download from.
func (s *Server) DownloadHost() string { return s.plain.Listener.Addr().String() }

// SetPackets sets how many 188-byte video packets each recording holds.
func (s *Server) SetPackets(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packets = n
}

// CorruptDownloads makes the next n downloads contain damaged packets.
func (s *Server) CorruptDownloads(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.corrupt = n
}

// TruncateDownloads makes the next n downloads break off half-way.
func (s *Server) TruncateDownloads(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.truncate = n
}

// RefusePlainDownloads makes the plain port answer 403, so that only the
// HTTPS port serves recordings.
func (s *Server) RefusePlainDownloads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plainDown = true
}

// SlowDownloads pauses for d between the chunks of each download.
func (s *Server) SlowDownloads(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunkDelay = d
}

// Downloads returns the request URIs of the downloads started so far.
func (s *Server) Downloads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.downloads...)
}

// Recording returns the bytes of the .TiVo file the server sends for a
// download: a 1024-byte header and then video packets. A program stream has
// no packet structure, so there the packets are filler.
func Recording(packets int, ts bool) []byte {
	const headerSize = 1024
	b := make([]byte, headerSize+packets*188)
	copy(b, "TiVo")
	binary.BigEndian.PutUint16(b[4:], 4)
	if ts {
		binary.BigEndian.PutUint16(b[6:], 0x20)
	}
	binary.BigEndian.PutUint32(b[10:], headerSize)
	for i := 0; i < packets; i++ {
		p := b[headerSize+i*188:]
		if ts {
			p[0] = 0x47
		}
		binary.BigEndian.PutUint32(p[4:], uint32(i))
	}
	return b
}

// Addr returns the server's address and port.
func (s *Server) Addr() (host string, port int) {
	host, p, _ := net.SplitHostPort(s.srv.Listener.Addr().String())
	port, _ = strconv.Atoi(p)
	return host, port
}

// SetRecordings changes the list, which also changes its change marker.
func (s *Server) SetRecordings(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count = count
	n, _ := strconv.ParseInt(s.lastChange, 0, 64)
	s.lastChange = fmt.Sprintf("0x%x", n+1)
}

// BusyFor makes the next n requests fail with 503 Server Busy.
func (s *Server) BusyFor(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = n
}

// ExpireNonce makes the server reject the nonce clients hold, as a TiVo
// does after a while. A correct client re-authenticates without fuss.
func (s *Server) ExpireNonce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonce += "x"
}

// Requests returns how many authenticated requests were served and how
// many 401 challenges were sent.
func (s *Server) Requests() (served, challenges int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests, s.challenges
}

func md5hex(v string) string {
	sum := md5.Sum([]byte(v))
	return hex.EncodeToString(sum[:])
}

// authorized checks a Digest Authorization header properly, so a client
// with a subtly wrong implementation fails here as it would on a TiVo.
func (s *Server) authorized(r *http.Request) bool {
	header, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Digest ")
	if !ok {
		return false
	}
	p := map[string]string{}
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		p[k] = strings.Trim(v, `"`)
	}
	if p["username"] != "tivo" || p["nonce"] != s.nonce || p["uri"] != r.URL.RequestURI() {
		return false
	}
	ha1 := md5hex("tivo:TiVo DVR:" + s.MAK)
	ha2 := md5hex(r.Method + ":" + p["uri"])
	want := md5hex(strings.Join([]string{ha1, p["nonce"], p["nc"], p["cnonce"], p["qop"], ha2}, ":"))
	return p["response"] == want
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if strings.HasPrefix(r.URL.Path, "/download/") {
		s.download(w, r) // unlocks
		return
	}
	defer s.mu.Unlock()

	if !s.authorized(r) {
		s.challenges++
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm="TiVo DVR", nonce="%s", qop="auth"`, s.nonce))
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if s.busy > 0 {
		s.busy--
		http.Error(w, "Server Busy", http.StatusServiceUnavailable)
		return
	}
	if c, err := r.Cookie("sid"); err != nil || c.Value == "" {
		http.Error(w, "no session cookie", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	if r.URL.Path == "/TiVoVideoDetails" {
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(Details(q.Get("id"))))
		return
	}
	if r.URL.Path != "/TiVoConnect" || q.Get("Command") != "QueryContainer" || q.Get("Container") != "/NowPlaying" {
		http.NotFound(w, r)
		return
	}
	s.requests++

	offset, _ := strconv.Atoi(q.Get("AnchorOffset"))
	want, _ := strconv.Atoi(q.Get("ItemCount"))
	limit := s.MaxPerPage
	if limit == 0 {
		limit = 50
	}
	n := max(min(want, limit, s.count-offset), 0)

	// Download links are plain HTTP on another port, as a real TiVo's are.
	content, details := s.plain.Listener.Addr().String(), s.srv.Listener.Addr().String()
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="utf-8"?>
<TiVoContainer xmlns="http://www.tivo.com/developer/calypso-protocol-1.6/">
<Details><ContentType>x-tivo-container/tivo-videos</ContentType><SourceFormat>x-tivo-container/tivo-dvr</SourceFormat>
<Title>Now Playing</Title><LastChangeDate>%s</LastChangeDate><TotalItems>%d</TotalItems><UniqueId>/NowPlaying</UniqueId></Details>
<SortOrder>Type,CaptureDate</SortOrder><GlobalSort>Yes</GlobalSort><ItemStart>%d</ItemStart><ItemCount>%d</ItemCount>
`, s.lastChange, s.count, offset, n)
	for i := offset; i < offset+n; i++ {
		b.WriteString(Item(content, details, i))
	}
	b.WriteString("</TiVoContainer>\n")
	w.Header().Set("Content-Type", "text/xml")
	w.Write([]byte(b.String()))
}

// download sends a recording. It is called with the lock held and releases
// it before sending.
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	fail := func(text string, code int) {
		s.mu.Unlock()
		http.Error(w, text, code)
	}
	if !s.authorized(r) {
		s.challenges++
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm="TiVo DVR", nonce="%s", qop="auth"`, s.nonce))
		fail("Unauthorized", http.StatusUnauthorized)
		return
	}
	if c, err := r.Cookie("sid"); err != nil || c.Value == "" {
		fail("no session cookie", http.StatusForbidden)
		return
	}
	if s.plainDown && r.TLS == nil {
		fail("Forbidden", http.StatusForbidden)
		return
	}
	// A TiVo sends one recording at a time.
	if s.busy > 0 || s.downloading {
		if s.busy > 0 {
			s.busy--
		}
		fail("Server Busy", http.StatusServiceUnavailable)
		return
	}
	ts := r.URL.Query().Get("Format") == "video/x-tivo-mpeg-ts"
	data := Recording(s.packets, ts)
	if s.corrupt > 0 {
		s.corrupt--
		// Damage three packets in the middle.
		for i := 0; i < 3; i++ {
			data[1024+(s.packets/2+i)*188] = 0xFF
		}
	}
	cut := false
	if s.truncate > 0 {
		s.truncate--
		cut = true
	}
	delay := s.chunkDelay
	s.downloading = true
	s.downloads = append(s.downloads, r.URL.RequestURI())
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.downloading = false
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "video/x-tivo-mpeg")
	flusher, _ := w.(http.Flusher)
	const chunk = 64 << 10
	for sent := 0; sent < len(data); sent += chunk {
		if cut && sent >= len(data)/2 {
			// Abort the connection without ending the response properly.
			panic(http.ErrAbortHandler)
		}
		if _, err := io.Copy(w, bytes.NewReader(data[sent:min(sent+chunk, len(data))])); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
	}
}

// Details returns the TiVoVideoDetails document for a recording id.
func Details(id string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<TvBusMarshalledStruct:TvBusEnvelope xmlns:xs="http://www.w3.org/2001/XMLSchema-instance" xmlns:TvBusMarshalledStruct="http://tivo.com/developer/xml/idl/TvBusMarshalledStruct" xs:type="TvPgdRecording:TvPgdRecording">
<recordedDuration>PT29M58S</recordedDuration><vActualShowing></vActualShowing><vBookmark></vBookmark>
<recordingQuality value="75">HIGH</recordingQuality>
<showing><showingBits value="4609"/><time>2021-03-05T02:00:00Z</time><duration>PT30M</duration>
<program><vActor><element>Lovelace|Ada</element><element>Hopper|Grace</element></vActor>
<vAdvisory></vAdvisory><vChoreographer></vChoreographer><colorCode value="4">COLOR</colorCode>
<description>The full story of recording %s. Copyright Rovi, Inc.</description>
<vDirector><element>Turing|Alan</element></vDirector><episodeNumber>204</episodeNumber>
<episodeTitle>The Details</episodeTitle><vExecProducer></vExecProducer>
<vProgramGenre><element>Documentary</element><element>Science</element></vProgramGenre>
<vGuestStar></vGuestStar><vHost></vHost><isEpisode>true</isEpisode>
<originalAirDate>2021-03-04T00:00:00Z</originalAirDate><vProducer></vProducer>
<series><isEpisodic>true</isEpisodic><vSeriesGenre><element>Science</element></vSeriesGenre>
<seriesTitle>Show &amp; Friends</seriesTitle><uniqueId>SH000777</uniqueId></series>
<showType value="5">SERIES</showType><title>Show &amp; Friends</title><uniqueId>EP0007770204</uniqueId>
<vWriter><element>Shannon|Claude</element></vWriter><starRating value="6">THREE_POINT_FIVE</starRating></program>
<channel><displayMajorNumber>7</displayMajorNumber><displayMinorNumber>1</displayMinorNumber><callsign>KQED</callsign></channel>
<tvRating value="5">_14</tvRating></showing>
<startTime>2021-03-05T01:59:58Z</startTime><stopTime>2021-03-05T02:30:00Z</stopTime>
</TvBusMarshalledStruct:TvBusEnvelope>
`, id)
}

// Item returns the XML for recording number i, with its download link on
// one host:port and its details link on another. Every tenth is copy
// protected, every seventh is a suggestion, and number 0 is in progress.
// Each belongs to one of twelve series.
func Item(contentHost, detailsHost string, i int) string {
	series := i % 12
	extra, icon := "", ""
	switch {
	case i == 0:
		extra = "<InProgress>Yes</InProgress>"
		icon = "urn:tivo:image:in-progress-recording"
	case i%10 == 9:
		extra = "<CopyProtected>Yes</CopyProtected>"
	case i%7 == 6:
		icon = "urn:tivo:image:suggestion-recording"
	}
	if icon != "" {
		icon = "<CustomIcon><Url>" + icon + "</Url><ContentType>image/*</ContentType><AcceptsParams>No</AcceptsParams></CustomIcon>"
	}
	return fmt.Sprintf(`<Item><Details><ContentType>video/x-tivo-raw-tts</ContentType><SourceFormat>video/x-tivo-raw-tts</SourceFormat>
<Title>Show %d &amp; Friends</Title><SourceSize>%d</SourceSize><Duration>1800000</Duration><CaptureDate>0x%x</CaptureDate>
<EpisodeTitle>Episode %d</EpisodeTitle><Description>Things happen in episode %d. Copyright Rovi, Inc.</Description>
<SourceChannel>7-1</SourceChannel><SourceStation>KQED</SourceStation><HighDefinition>Yes</HighDefinition>
<ProgramId>EP%08d</ProgramId><SeriesId>SH%06d</SeriesId><EpisodeNumber>%d</EpisodeNumber><TvRating>4</TvRating>%s</Details>
<Links><Content><Url>http://%s/download/Show%%20%d.TiVo?Container=%%2FNowPlaying&amp;id=%d</Url><ContentType>video/x-tivo-raw-tts</ContentType></Content>
%s<TiVoVideoDetails><Url>https://%s/TiVoVideoDetails?id=%d</Url><ContentType>text/xml</ContentType><AcceptsParams>No</AcceptsParams></TiVoVideoDetails></Links></Item>
`, series, int64(1_000_000_000)+int64(i), 0x60000000+i*3600, i, i, i, series, 100+i, extra,
		contentHost, series, 1000+i, icon, detailsHost, 1000+i)
}
