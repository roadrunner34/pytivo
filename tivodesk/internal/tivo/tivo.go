// Package tivo keeps the list of TiVo boxes found on the network.
package tivo

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// Box is one TiVo, as it announces itself over mDNS.
type Box struct {
	TSN       string    `json:"tsn"`     // TiVo Service Number; identifies the box
	Name      string    `json:"name"`    // the name its owner gave it, such as "Family Room"
	Address   string    `json:"address"` // IPv4 address
	Port      int       `json:"port"`    // HTTPS port, normally 443
	Platform  string    `json:"platform"`
	SWVersion string    `json:"swVersion"`
	Online    bool      `json:"online"`
	LastSeen  time.Time `json:"lastSeen"`
}

// Model returns the product family, when the platform string is one we
// recognise, or the raw platform otherwise.
func (b Box) Model() string {
	switch b.Platform {
	case "tcd/Series4":
		return "Premiere"
	case "tcd/Series5":
		return "Roamio"
	case "tcd/Series6":
		return "Bolt"
	case "tcd/Keystone":
		return "Edge"
	}
	return strings.TrimPrefix(b.Platform, "tcd/")
}

// Registry is the set of known boxes, keyed by TSN. The zero value is
// ready to use.
type Registry struct {
	mu       sync.RWMutex
	boxes    map[string]Box
	onChange func()
}

// OnChange sets a function to call whenever the list changes. It is called
// without the registry locked.
func (r *Registry) OnChange(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = fn
}

// Seen records that a box announced itself. It reports whether anything a
// user would notice has changed.
func (r *Registry) Seen(b Box) bool {
	b.Online = true
	if b.LastSeen.IsZero() {
		b.LastSeen = time.Now()
	}
	r.mu.Lock()
	if r.boxes == nil {
		r.boxes = map[string]Box{}
	}
	old, known := r.boxes[b.TSN]
	r.boxes[b.TSN] = b
	fn := r.onChange
	r.mu.Unlock()

	old.LastSeen, b.LastSeen = time.Time{}, time.Time{}
	changed := !known || old != b
	if changed && fn != nil {
		fn()
	}
	return changed
}

// Gone marks a box as no longer announcing itself. It stays in the list, so
// the UI can show it as offline rather than have it vanish.
func (r *Registry) Gone(tsn string) {
	r.mu.Lock()
	b, ok := r.boxes[tsn]
	if !ok || !b.Online {
		r.mu.Unlock()
		return
	}
	b.Online = false
	r.boxes[tsn] = b
	fn := r.onChange
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Get returns the box with the given TSN.
func (r *Registry) Get(tsn string) (Box, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.boxes[tsn]
	return b, ok
}

// List returns every known box, sorted by name.
func (r *Registry) List() []Box {
	r.mu.RLock()
	out := make([]Box, 0, len(r.boxes))
	for _, b := range r.boxes {
		out = append(out, b)
	}
	r.mu.RUnlock()
	slices.SortFunc(out, func(a, b Box) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.TSN, b.TSN)
	})
	return out
}
