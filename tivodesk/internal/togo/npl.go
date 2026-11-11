package togo

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Recording is one entry in a TiVo's Now Playing list.
type Recording struct {
	ID            string    `json:"id"` // unique on its TiVo
	Title         string    `json:"title"`
	EpisodeTitle  string    `json:"episodeTitle"`
	Description   string    `json:"description"`
	SeriesID      string    `json:"seriesId"`
	ProgramID     string    `json:"programId"`
	EpisodeNumber string    `json:"episodeNumber"`
	Captured      time.Time `json:"captured"`
	DurationMS    int64     `json:"durationMs"`
	Size          int64     `json:"size"` // bytes, as the TiVo estimates it
	Channel       string    `json:"channel"`
	Station       string    `json:"station"`
	HD            bool      `json:"hd"`
	InProgress    bool      `json:"inProgress"`
	CopyProtected bool      `json:"copyProtected"`
	// Status is one of normal, protected, expiring, expired, kuid (keep
	// until I delete), suggestion or inprogress.
	Status     string `json:"status"`
	TVRating   string `json:"tvRating"`
	MPAARating string `json:"mpaaRating"`
	ContentURL string `json:"contentUrl"` // where to download it
	DetailsURL string `json:"detailsUrl"` // full programme details, as XML
}

// Listing is a TiVo's whole Now Playing list at one moment.
type Listing struct {
	// LastChange is the TiVo's own marker for the list's state. If it has
	// not changed, neither has the list.
	LastChange string      `json:"lastChange"`
	Recordings []Recording `json:"recordings"`
}

// pageSize is the most entries a TiVo returns for one request.
const pageSize = 50

// parallelPages is how many pages are requested at once. pyTivo asks for
// one at a time.
const parallelPages = 3

const nowPlayingPath = "/TiVoConnect?Command=QueryContainer&Container=%2FNowPlaying&Recurse=Yes"

// The XML a TiVo returns for a QueryContainer request.
type containerXML struct {
	Details struct {
		LastChangeDate string
		TotalItems     string
	}
	ItemCount string
	Items     []itemXML `xml:"Item"`
}

type itemXML struct {
	Details struct {
		Title, EpisodeTitle, Description string
		SeriesId, ProgramId              string
		EpisodeNumber                    string
		CaptureDate, Duration            string
		SourceSize                       string
		SourceChannel, SourceStation     string
		HighDefinition                   string
		InProgress, CopyProtected        string
		TvRating, MpaaRating             string
	}
	Links struct {
		Content          struct{ Url string }
		CustomIcon       struct{ Url string }
		TiVoVideoDetails struct{ Url string }
	}
}

// Text the guide data providers append to descriptions.
var descriptionNoise = strings.NewReplacer(
	" Copyright Tribune Media Services, Inc.", "",
	" Copyright Rovi, Inc.", "",
)

var statusByIcon = map[string]string{
	"urn:tivo:image:expires-soon-recording":        "expiring",
	"urn:tivo:image:expired-recording":             "expired",
	"urn:tivo:image:save-until-i-delete-recording": "kuid",
	"urn:tivo:image:suggestion-recording":          "suggestion",
	"urn:tivo:image:in-progress-recording":         "inprogress",
}

func (it itemXML) recording(base *url.URL) Recording {
	d := it.Details
	r := Recording{
		Title:         d.Title,
		EpisodeTitle:  d.EpisodeTitle,
		Description:   strings.TrimSuffix(descriptionNoise.Replace(d.Description), " *"),
		SeriesID:      d.SeriesId,
		ProgramID:     d.ProgramId,
		EpisodeNumber: d.EpisodeNumber,
		Channel:       d.SourceChannel,
		Station:       d.SourceStation,
		HD:            d.HighDefinition == "Yes",
		InProgress:    d.InProgress == "Yes",
		CopyProtected: d.CopyProtected == "Yes",
		TVRating:      d.TvRating,
		MPAARating:    d.MpaaRating,
		Status:        "normal",
	}
	// CaptureDate is seconds since 1970, written in hexadecimal ("0x5F...").
	if secs, err := strconv.ParseInt(d.CaptureDate, 0, 64); err == nil {
		r.Captured = time.Unix(secs, 0).UTC()
	}
	r.DurationMS, _ = strconv.ParseInt(d.Duration, 10, 64)
	r.Size, _ = strconv.ParseInt(d.SourceSize, 10, 64)

	if r.CopyProtected {
		r.Status = "protected"
	} else if s, ok := statusByIcon[it.Links.CustomIcon.Url]; ok {
		r.Status = s
	}

	r.DetailsURL = it.Links.TiVoVideoDetails.Url
	if ref, err := url.Parse(it.Links.Content.Url); err == nil && it.Links.Content.Url != "" {
		abs := base.ResolveReference(ref)
		r.ContentURL = abs.String()
		r.ID = abs.Query().Get("id")
	}
	if r.ID == "" {
		// No id in the link: fall back to something stable for this entry.
		r.ID = r.ProgramID + "@" + d.CaptureDate
	}
	return r
}

// page fetches up to count entries starting at offset. count 0 fetches only
// the list's summary.
func (c *Client) page(ctx context.Context, offset, count int) (*containerXML, error) {
	q := fmt.Sprintf("%s&ItemCount=%d", nowPlayingPath, count)
	if offset > 0 {
		q += fmt.Sprintf("&AnchorOffset=%d", offset)
	}
	body, err := c.get(ctx, q)
	if err != nil {
		return nil, err
	}
	var doc containerXML
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("the TiVo's answer was not valid XML: %w", err)
	}
	return &doc, nil
}

// Summary returns the list's change marker and size without fetching any
// entries. Comparing the marker with a cached one tells whether the cache
// is still good, in one small request.
func (c *Client) Summary(ctx context.Context) (lastChange string, total int, err error) {
	doc, err := c.page(ctx, 0, 0)
	if err != nil {
		return "", 0, err
	}
	total, _ = strconv.Atoi(doc.Details.TotalItems)
	return doc.Details.LastChangeDate, total, nil
}

// NowPlaying fetches the whole list. If progress is not nil it is called as
// entries arrive, with the number loaded so far and the total; calls come
// from several goroutines but never at the same time.
//
// Entries are returned in the TiVo's own order.
func (c *Client) NowPlaying(ctx context.Context, progress func(loaded, total int)) (*Listing, error) {
	lastChange, total, err := c.Summary(ctx)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(c.base)
	if err != nil {
		return nil, err
	}
	if progress != nil {
		progress(0, total)
	}

	// Split the list into fixed ranges and fetch a few at a time. Each
	// range is filled by as many requests as it takes, in case a TiVo
	// returns fewer entries than asked for.
	ranges := (total + pageSize - 1) / pageSize
	results := make([][]Recording, ranges)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		loaded   int
		firstErr error
	)
	next := make(chan int)
	for w := 0; w < min(parallelPages, ranges); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				offset, want := i*pageSize, min(pageSize, total-i*pageSize)
				for want > 0 {
					doc, err := c.page(ctx, offset, want)
					if err != nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
						cancel()
						return
					}
					if len(doc.Items) == 0 {
						break // the list shrank while we were reading it
					}
					got := min(len(doc.Items), want)
					for _, it := range doc.Items[:got] {
						results[i] = append(results[i], it.recording(base))
					}
					offset, want = offset+got, want-got

					mu.Lock()
					loaded += got
					if progress != nil {
						progress(loaded, total)
					}
					mu.Unlock()
				}
			}
		}()
	}
feed:
	for i := 0; i < ranges; i++ {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := &Listing{LastChange: lastChange, Recordings: make([]Recording, 0, total)}
	seen := make(map[string]bool, total)
	for _, page := range results {
		for _, r := range page {
			// A recording that finishes while we page can show up twice.
			if !seen[r.ID] {
				seen[r.ID] = true
				out.Recordings = append(out.Recordings, r)
			}
		}
	}
	return out, nil
}
