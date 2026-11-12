package togo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tivodesk/internal/togo/faketivo"
)

// recording returns entry i of the fake's list.
func recording(t *testing.T, c *Client, i int) Recording {
	t.Helper()
	list, err := c.NowPlaying(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return list.Recordings[i]
}

func TestDownload(t *testing.T) {
	fake := faketivo.New(t, mak, 20)
	c, _ := client(t, fake, mak)
	r := recording(t, c, 3)

	for _, ts := range []bool{true, false} {
		var out bytes.Buffer
		var last int64
		res, err := c.Download(context.Background(), r.ContentURL, &out, DownloadOptions{
			TS:       ts,
			Progress: func(written int64, _ int) { last = written },
		})
		if err != nil {
			t.Fatalf("ts=%v: %v", ts, err)
		}
		want := faketivo.Recording(4000, ts)
		if !bytes.Equal(out.Bytes(), want) {
			t.Errorf("ts=%v: got %d bytes that differ from the %d sent", ts, out.Len(), len(want))
		}
		if res.Bytes != int64(len(want)) || last != res.Bytes || res.SyncLosses != 0 {
			t.Errorf("ts=%v: result %+v, last progress %d", ts, res, last)
		}
	}

	// The transport-stream request, and only that one, names the format.
	got := fake.Downloads()
	if len(got) != 2 || !strings.HasSuffix(got[0], "&Format=video/x-tivo-mpeg-ts") || strings.Contains(got[1], "Format") {
		t.Errorf("download requests: %q", got)
	}
}

func TestDownloadCountsDamagedPackets(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, _ := client(t, fake, mak)
	r := recording(t, c, 1)

	fake.CorruptDownloads(1)
	var out bytes.Buffer
	res, err := c.Download(context.Background(), r.ContentURL, &out, DownloadOptions{TS: true})
	if err != nil || res.SyncLosses != 3 {
		t.Errorf("got %+v, %v; want 3 damaged packets", res, err)
	}
}

func TestDownloadWaitsWhileBusy(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, waits := client(t, fake, mak)
	r := recording(t, c, 1)

	fake.BusyFor(2)
	if _, err := c.Download(context.Background(), r.ContentURL, &bytes.Buffer{}, DownloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 2 {
		t.Errorf("waited %v, want two waits", *waits)
	}

	fake.BusyFor(100)
	*waits = nil
	if _, err := c.Download(context.Background(), r.ContentURL, &bytes.Buffer{}, DownloadOptions{}); !errors.Is(err, ErrBusy) {
		t.Errorf("a TiVo that stays busy: got %v", err)
	}
}

func TestDownloadFallsBackToHTTPS(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, _ := client(t, fake, mak)
	r := recording(t, c, 1)

	fake.RefusePlainDownloads()
	var out bytes.Buffer
	if _, err := c.Download(context.Background(), r.ContentURL, &out, DownloadOptions{TS: true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), faketivo.Recording(4000, true)) {
		t.Error("the recording fetched over HTTPS is not what was sent")
	}
}

func TestDownloadFailures(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, _ := client(t, fake, mak)
	r := recording(t, c, 1)
	ctx := context.Background()

	fake.TruncateDownloads(1)
	_, err := c.Download(ctx, r.ContentURL, &bytes.Buffer{}, DownloadOptions{})
	if err == nil || !strings.Contains(err.Error(), "cut short") {
		t.Errorf("a broken connection: got %v", err)
	}

	wrong, _ := client(t, fake, "0000000000")
	if _, err := wrong.Download(ctx, r.ContentURL, &bytes.Buffer{}, DownloadOptions{}); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("wrong key: got %v", err)
	}

	if _, err := c.Download(ctx, "", &bytes.Buffer{}, DownloadOptions{}); err == nil {
		t.Error("an empty link should fail")
	}

	// The details page is not a recording.
	if _, err := c.Download(ctx, r.DetailsURL, &bytes.Buffer{}, DownloadOptions{}); err == nil {
		t.Error("something that is not a recording should fail")
	}

	fake.SetPackets(0)
	if _, err := c.Download(ctx, r.ContentURL, &bytes.Buffer{}, DownloadOptions{}); err == nil || !strings.Contains(err.Error(), "no video") {
		t.Errorf("a recording with only a header: got %v", err)
	}
}

func TestDownloadCancelAndStall(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	fake.SlowDownloads(200 * time.Millisecond)
	c, _ := client(t, fake, mak)
	r := recording(t, c, 1)

	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.Download(ctx, r.ContentURL, &bytes.Buffer{}, DownloadOptions{
		Progress: func(int64, int) { cancel() },
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v", err)
	}

	c.stall = 50 * time.Millisecond
	_, err = c.Download(context.Background(), r.ContentURL, &bytes.Buffer{}, DownloadOptions{})
	if !errors.Is(err, ErrStalled) {
		t.Errorf("stalled: got %v", err)
	}
}

func TestDetails(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, _ := client(t, fake, mak)
	r := recording(t, c, 3)

	m, err := c.Details(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	m.Write(&b)
	want := `title: Show & Friends
episodeTitle: The Details
episodeNumber: 204
description: The full story of recording 1003.
time: 2021-03-05T02:00:00Z
programId: EP0007770204
seriesId: SH000777
callsign: KQED
displayMajorNumber: 7
displayMinorNumber: 1
tvRating: 14
seriesTitle: Show & Friends
isEpisode: true
originalAirDate: 2021-03-04T00:00:00Z
showingBits: 4609
starRating: 3.5
vActor: Lovelace|Ada
vActor: Hopper|Grace
vDirector: Turing|Alan
vProgramGenre: Documentary
vProgramGenre: Science
vWriter: Shannon|Claude
`
	if b.String() != want {
		t.Errorf("metadata:\n%s\nwant:\n%s", b.String(), want)
	}

	// Without a details link, the list's own information is used.
	r.DetailsURL = ""
	m, err = c.Details(context.Background(), r)
	if err != nil || m.Get("episodeTitle") != "Episode 3" || m.Get("tvRating") != "PG" {
		t.Errorf("basic metadata: %v, %v", m, err)
	}
}

func TestFileName(t *testing.T) {
	utc := time.UTC
	r := Recording{
		ID: "77", Title: "Nova: What's Next?", EpisodeTitle: `The "Big" One`, Station: "KQED",
		Captured: time.Date(2021, 3, 5, 2, 0, 0, 0, time.UTC),
	}
	for _, c := range []struct {
		name         string
		r            Recording
		ts, sortable bool
		loc          *time.Location
		count        int
		want         string
	}{
		{"desktop style", r, true, false, utc, 1, "Nova - What's Next. - 'The 'Big' One' (Recorded Mar 05, 2021, KQED) (TS).tivo"},
		{"sortable", r, false, true, utc, 1, "Nova - What's Next. - 2021-03-05 - 'The 'Big' One' (KQED) (PS).tivo"},
		{"duplicate", r, true, false, utc, 3, "Nova - What's Next. - 'The 'Big' One' (Recorded Mar 05, 2021, KQED) (TS) (3).tivo"},
		// An evening recording keeps its local date.
		{"local date", r, true, false, time.FixedZone("PST", -8*3600), 1, "Nova - What's Next. - 'The 'Big' One' (Recorded Mar 04, 2021, KQED) (TS).tivo"},
		{"bare", Recording{ID: "9", Captured: r.Captured}, false, false, utc, 1, "Recording 9 (Recorded Mar 05, 2021) (PS).tivo"},
	} {
		if got := FileName(c.r, c.ts, false, c.sortable, c.loc, c.count); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}

	long := r
	long.Title = strings.Repeat("Very Long ", 60)
	if n := len(FileName(long, true, false, false, utc, 1)); n > 210 {
		t.Errorf("a long title gave a %d-character name", n)
	}
}

func TestDecryptedFileName(t *testing.T) {
	r := Recording{Title: "Nova", EpisodeTitle: "Next", Station: "KQED", Captured: time.Date(2021, 3, 5, 2, 0, 0, 0, time.UTC)}
	if got := FileName(r, true, true, false, time.UTC, 1); got != "Nova - 'Next' (Recorded Mar 05, 2021, KQED).ts" {
		t.Errorf("transport stream: %s", got)
	}
	if got := FileName(r, false, true, true, time.UTC, 2); got != "Nova - 2021-03-05 - 'Next' (KQED) (2).mpg" {
		t.Errorf("program stream: %s", got)
	}
}

func TestFreePath(t *testing.T) {
	dir := t.TempDir()
	r := Recording{Title: "Show", Captured: time.Date(2021, 3, 5, 2, 0, 0, 0, time.UTC)}
	first := FreePath(dir, r, true, false, false, time.UTC)
	if filepath.Base(first) != "Show (Recorded Mar 05, 2021) (TS).tivo" {
		t.Errorf("first path: %s", first)
	}
	// A finished file takes the first name, an unfinished one the second.
	os.WriteFile(first, nil, 0o644)
	second := FreePath(dir, r, true, false, false, time.UTC)
	os.WriteFile(second+PartSuffix, nil, 0o644)
	third := FreePath(dir, r, true, false, false, time.UTC)
	if !strings.HasSuffix(second, "(TS) (2).tivo") || !strings.HasSuffix(third, "(TS) (3).tivo") {
		t.Errorf("later paths: %s, %s", second, third)
	}
}
