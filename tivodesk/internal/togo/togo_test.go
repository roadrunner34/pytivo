package togo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"tivodesk/internal/togo/faketivo"
)

const mak = "1234567890"

// client returns a Client for the fake that records its retry waits
// instead of sleeping through them.
func client(t *testing.T, fake *faketivo.Server, key string) (*Client, *[]time.Duration) {
	t.Helper()
	host, port := fake.Addr()
	c := NewClient(host, port, key)
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return c, &waits
}

func TestNowPlaying(t *testing.T) {
	fake := faketivo.New(t, mak, 684) // the size of a real library
	c, _ := client(t, fake, mak)

	lastLoaded, lastTotal, calls := 0, 0, 0
	list, err := c.NowPlaying(context.Background(), func(loaded, total int) {
		if loaded < lastLoaded {
			t.Errorf("progress went backwards: %d after %d", loaded, lastLoaded)
		}
		lastLoaded, lastTotal = loaded, total
		calls++
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Recordings) != 684 || list.LastChange != "0x1" {
		t.Fatalf("got %d recordings, marker %q", len(list.Recordings), list.LastChange)
	}
	if lastLoaded != 684 || lastTotal != 684 || calls != 15 { // one at the start, one per page
		t.Errorf("progress ended at %d of %d after %d calls", lastLoaded, lastTotal, calls)
	}

	// Entries come back in the TiVo's order, even though pages are fetched
	// in parallel.
	for i, r := range list.Recordings {
		if r.ID != fmt.Sprint(1000+i) {
			t.Fatalf("recording %d has id %s; order was not preserved", i, r.ID)
		}
	}

	// One summary request plus 14 pages, and a single challenge at the
	// start, apart from requests that raced the first answer.
	served, challenges := fake.Requests()
	if served != 15 {
		t.Errorf("served %d requests, want 15", served)
	}
	if challenges > 1 {
		t.Errorf("%d challenges: later requests should be signed up front", challenges)
	}
}

func TestRecordingFields(t *testing.T) {
	fake := faketivo.New(t, mak, 20)
	c, _ := client(t, fake, mak)
	list, err := c.NowPlaying(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := list.Recordings[3]
	want := Recording{
		ID: "1003", Title: "Show 3 & Friends", EpisodeTitle: "Episode 3",
		Description: "Things happen in episode 3.", // the copyright notice is stripped
		SeriesID:    "SH000003", ProgramID: "EP00000003", EpisodeNumber: "103",
		Captured:   time.Unix(0x60000000+3*3600, 0).UTC(),
		DurationMS: 1800000, Size: 1_000_000_003,
		Channel: "7-1", Station: "KQED", HD: true, Status: "normal", TVRating: "4",
		ContentURL: "http://" + fake.DownloadHost() + "/download/Show%203.TiVo?Container=%2FNowPlaying&id=1003",
	}
	got := r
	got.DetailsURL = ""
	if got != want {
		t.Errorf("recording 3\n got %+v\nwant %+v", got, want)
	}
	if r.DetailsURL == "" {
		t.Error("no details URL")
	}

	for i, status := range map[int]string{0: "inprogress", 9: "protected", 6: "suggestion", 19: "protected", 13: "suggestion"} {
		if got := list.Recordings[i].Status; got != status {
			t.Errorf("recording %d status = %q, want %q", i, got, status)
		}
	}
	if !list.Recordings[0].InProgress || !list.Recordings[9].CopyProtected {
		t.Error("InProgress or CopyProtected flag is wrong")
	}
}

func TestSummaryAndEmptyList(t *testing.T) {
	fake := faketivo.New(t, mak, 0)
	c, _ := client(t, fake, mak)
	list, err := c.NowPlaying(context.Background(), nil)
	if err != nil || len(list.Recordings) != 0 {
		t.Fatalf("empty list: %v, %v", list, err)
	}

	fake.SetRecordings(7)
	marker, total, err := c.Summary(context.Background())
	if err != nil || marker != "0x2" || total != 7 {
		t.Errorf("Summary = %q, %d, %v; the marker should change with the list", marker, total, err)
	}
}

// A TiVo that returns fewer entries per request than asked for must still
// yield the whole list.
func TestShortPages(t *testing.T) {
	fake := faketivo.New(t, mak, 130)
	fake.MaxPerPage = 32
	c, _ := client(t, fake, mak)
	list, err := c.NowPlaying(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Recordings) != 130 {
		t.Fatalf("got %d recordings, want 130", len(list.Recordings))
	}
	for i, r := range list.Recordings {
		if r.ID != fmt.Sprint(1000+i) {
			t.Fatalf("recording %d has id %s", i, r.ID)
		}
	}
}

func TestWrongMAK(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, _ := client(t, fake, "0000000000")
	if _, err := c.NowPlaying(context.Background(), nil); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}

func TestBusyThenRecovers(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, waits := client(t, fake, mak)
	fake.BusyFor(3)
	if _, _, err := c.Summary(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	if fmt.Sprint(*waits) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v", *waits, want)
	}
}

// pyTivo retries a busy TiVo for ever. This must give up.
func TestBusyForEverGivesUp(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, waits := client(t, fake, mak)
	fake.BusyFor(1000)
	if _, _, err := c.Summary(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if len(*waits) != len(backoff) {
		t.Errorf("waited %d times, want %d", len(*waits), len(backoff))
	}
}

func TestExpiredNonceIsRenewed(t *testing.T) {
	fake := faketivo.New(t, mak, 5)
	c, _ := client(t, fake, mak)
	if _, _, err := c.Summary(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.ExpireNonce()
	if _, _, err := c.Summary(context.Background()); err != nil {
		t.Fatalf("after the nonce expired: %v", err)
	}
}

func TestCancel(t *testing.T) {
	fake := faketivo.New(t, mak, 500)
	c, _ := client(t, fake, mak)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.NowPlaying(ctx, func(loaded, _ int) {
		if loaded > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestUnreachable(t *testing.T) {
	c := NewClient("127.0.0.1", 1, mak) // nothing listens on port 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := c.Summary(ctx); err == nil {
		t.Error("expected an error from an unreachable TiVo")
	}
}

func TestParseChallenge(t *testing.T) {
	got := parseChallenge(`Digest realm="TiVo DVR", nonce="abc, def", qop="auth", stale=false`)
	if got["realm"] != "TiVo DVR" || got["nonce"] != "abc, def" || got["qop"] != "auth" || got["stale"] != "false" {
		t.Errorf("parsed %v", got)
	}
	if len(parseChallenge(`Basic realm="x"`)) != 0 {
		t.Error("a Basic challenge should parse as nothing")
	}
}
