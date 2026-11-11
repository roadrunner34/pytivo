package tivo

import "testing"

func TestRegistry(t *testing.T) {
	var r Registry
	changes := 0
	r.OnChange(func() { changes++ })

	roamio := Box{TSN: "846000", Name: "paella", Address: "10.0.0.2", Port: 443, Platform: "tcd/Series5"}
	edge := Box{TSN: "D6E000", Name: "Family Room", Address: "10.0.0.3", Port: 443, Platform: "tcd/Keystone"}

	if !r.Seen(roamio) || !r.Seen(edge) {
		t.Fatal("a new box should count as a change")
	}
	if r.Seen(roamio) {
		t.Error("the same announcement again should not count as a change")
	}
	if changes != 2 {
		t.Errorf("got %d change callbacks, want 2", changes)
	}

	list := r.List()
	if len(list) != 2 || list[0].Name != "Family Room" || list[1].Name != "paella" {
		t.Fatalf("list should be sorted by name without regard to case: %+v", list)
	}
	if !list[0].Online || list[0].LastSeen.IsZero() {
		t.Error("a seen box should be online with a time")
	}

	// A new address is a change; the box keeps its place.
	roamio.Address = "10.0.0.9"
	if !r.Seen(roamio) {
		t.Error("a new address should count as a change")
	}

	r.Gone("846000")
	r.Gone("846000") // twice is harmless
	r.Gone("unknown")
	if b, ok := r.Get("846000"); !ok || b.Online {
		t.Errorf("a box that left should stay listed, offline: %+v", b)
	}
	if changes != 4 {
		t.Errorf("got %d change callbacks, want 4", changes)
	}
	if !r.Seen(roamio) {
		t.Error("coming back online should count as a change")
	}
}

func TestModel(t *testing.T) {
	for platform, want := range map[string]string{
		"tcd/Series5": "Roamio", "tcd/Keystone": "Edge", "tcd/Series6": "Bolt",
		"tcd/Series4": "Premiere", "tcd/Future": "Future", "": "",
	} {
		if got := (Box{Platform: platform}).Model(); got != want {
			t.Errorf("Model(%q) = %q, want %q", platform, got, want)
		}
	}
}
