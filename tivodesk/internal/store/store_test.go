package store

import (
	"path/filepath"
	"testing"
	"time"

	"tivodesk/internal/togo"
)

func TestJobsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tivodesk.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &Job{TSN: "846", State: Queued, TS: true, Recording: togo.Recording{ID: "1", Title: "One"}, Created: time.Now()}
	b := &Job{TSN: "846", State: Queued, Recording: togo.Recording{ID: "2", Title: "Two"}}
	for _, j := range []*Job{a, b} {
		if err := s.AddJob(j); err != nil {
			t.Fatal(err)
		}
	}
	if a.ID == 0 || b.ID <= a.ID {
		t.Fatalf("ids should rise: %d, %d", a.ID, b.ID)
	}

	a.State, a.Bytes, a.Path, a.Rate = Done, 12345, `D:\rec\One.tivo`, 99
	if err := s.UpdateJob(a); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteJob(b.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs, err := s.Jobs()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("after reopening: %v, %v", jobs, err)
	}
	got := jobs[0]
	if got.ID != a.ID || got.State != Done || got.Bytes != 12345 || got.Path != a.Path ||
		got.Recording.Title != "One" || !got.TS || !got.IsFinished() {
		t.Errorf("job came back as %+v", got)
	}
	if got.Rate != 0 {
		t.Error("the speed should not be stored")
	}
}

func TestListings(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	when := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	list := &togo.Listing{LastChange: "0x5", Recordings: []togo.Recording{{ID: "1", Title: "One"}}}
	if err := s.SaveListing("846", when, list); err != nil {
		t.Fatal(err)
	}
	list.LastChange = "0x6"
	if err := s.SaveListing("846", when, list); err != nil {
		t.Fatalf("saving again should replace: %v", err)
	}
	later := when.Add(time.Hour)
	if err := s.TouchListing("846", later); err != nil {
		t.Fatal(err)
	}

	all, err := s.Listings()
	if err != nil || len(all) != 1 {
		t.Fatalf("listings: %v, %v", all, err)
	}
	got := all["846"]
	if !got.Fetched.Equal(later) || got.List.LastChange != "0x6" || len(got.List.Recordings) != 1 {
		t.Errorf("listing came back as %+v / %+v", got, got.List)
	}
}
