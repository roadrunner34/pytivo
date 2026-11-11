package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	"tivodesk/internal/events"
	"tivodesk/internal/secrets"
	"tivodesk/internal/tivo"
	"tivodesk/internal/togo/faketivo"
)

const testMAK = "1234567890"

// addFake registers a fake TiVo with the engine as if discovery found it.
func addFake(e *Engine, fake *faketivo.Server, tsn string) {
	host, port := fake.Addr()
	e.Registry().Seen(tivo.Box{TSN: tsn, Name: "Fake " + tsn, Address: host, Port: port, Platform: "tcd/Series5"})
}

// waitLoaded blocks until the engine is no longer loading a TiVo's list.
func waitLoaded(t *testing.T, e *Engine, tsn string) NowPlaying {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if np := e.NowPlaying(tsn); !np.Loading {
			return np
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Now Playing never finished loading")
	return NowPlaying{}
}

func TestNowPlayingRefresh(t *testing.T) {
	store := secrets.NewMemory()
	e := newEngine(t, t.TempDir(), store)
	fake := faketivo.New(t, testMAK, 120)

	var tivoEvents int
	var statuses []NowPlayingStatus
	e.Events().Subscribe(func(ev events.Event) {
		switch ev.Topic {
		case events.TiVosChanged:
			tivoEvents++
		case events.NowPlayingChanged:
			statuses = append(statuses, ev.Data.(NowPlayingStatus))
		}
	})

	if err := e.RefreshNowPlaying("846"); !errors.Is(err, ErrUnknownTiVo) {
		t.Errorf("unknown TiVo: got %v", err)
	}
	addFake(e, fake, "846")
	if tivoEvents != 1 || len(e.TiVos()) != 1 {
		t.Errorf("a found TiVo should be listed and announced: %d events, %v", tivoEvents, e.TiVos())
	}
	if err := e.RefreshNowPlaying("846"); !errors.Is(err, ErrNoMAK) {
		t.Errorf("no MAK: got %v", err)
	}
	if np := e.NowPlaying("846"); np.Recordings == nil || len(np.Recordings) != 0 || np.TSN != "846" {
		t.Errorf("before any refresh: %+v", np)
	}

	// First refresh fetches everything.
	e.SetMAK(testMAK)
	if err := e.RefreshNowPlaying("846"); err != nil {
		t.Fatal(err)
	}
	np := waitLoaded(t, e, "846")
	if np.Error != "" || len(np.Recordings) != 120 || np.Total != 120 || np.Unchanged || np.Fetched.IsZero() {
		t.Fatalf("after first refresh: %+v with %d recordings", np.NowPlayingStatus, len(np.Recordings))
	}
	if len(statuses) < 3 || !statuses[0].Loading || statuses[len(statuses)-1].Loading {
		t.Errorf("events should run from loading to done: %+v", statuses)
	}
	served, _ := fake.Requests()
	if served != 4 { // a summary and three pages
		t.Errorf("first refresh used %d requests, want 4", served)
	}

	// Second refresh finds nothing changed and costs one request.
	e.RefreshNowPlaying("846")
	np = waitLoaded(t, e, "846")
	if !np.Unchanged || len(np.Recordings) != 120 {
		t.Errorf("unchanged refresh: %+v", np.NowPlayingStatus)
	}
	if again, _ := fake.Requests(); again != served+1 {
		t.Errorf("unchanged refresh used %d requests, want 1", again-served)
	}

	// A changed list is fetched again.
	fake.SetRecordings(60)
	e.RefreshNowPlaying("846")
	np = waitLoaded(t, e, "846")
	if np.Unchanged || len(np.Recordings) != 60 {
		t.Errorf("changed list: %+v with %d recordings", np.NowPlayingStatus, len(np.Recordings))
	}

	if strings.Contains(logText(e), testMAK) {
		t.Error("the MAK appeared in the log")
	}
}

func TestNowPlayingFailureKeepsTheOldList(t *testing.T) {
	store := secrets.NewMemory()
	e := newEngine(t, t.TempDir(), store)
	fake := faketivo.New(t, testMAK, 10)
	addFake(e, fake, "846")
	e.SetMAK(testMAK)
	e.RefreshNowPlaying("846")
	if np := waitLoaded(t, e, "846"); len(np.Recordings) != 10 {
		t.Fatalf("setup: %+v", np.NowPlayingStatus)
	}

	// The key changes to a wrong one, as if the user mistyped it.
	e.SetMAK("0000000000")
	e.RefreshNowPlaying("846")
	np := waitLoaded(t, e, "846")
	if !strings.Contains(np.Error, "rejected the Media Access Key") {
		t.Errorf("error = %q", np.Error)
	}
	if len(np.Recordings) != 10 {
		t.Error("a failed refresh should leave the previous list in place")
	}

	// A TiVo's own key wins over the account's.
	store.Set(secrets.TiVoMAK("846"), testMAK)
	e.RefreshNowPlaying("846")
	if np := waitLoaded(t, e, "846"); np.Error != "" {
		t.Errorf("with the TiVo's own key: %q", np.Error)
	}
}

func TestCloseStopsARefresh(t *testing.T) {
	e, err := New(Options{ConfigPath: t.TempDir() + "/config.toml", Secrets: secrets.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	fake := faketivo.New(t, testMAK, 5)
	fake.BusyFor(1000) // the refresh would otherwise sit in retries
	addFake(e, fake, "846")
	e.SetMAK(testMAK)
	e.RefreshNowPlaying("846")

	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop a refresh that was waiting to retry")
	}
}
