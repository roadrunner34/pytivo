package core

import (
	"errors"
	"time"

	"tivodesk/internal/events"
	"tivodesk/internal/secrets"
	"tivodesk/internal/tivo"
	"tivodesk/internal/togo"
)

// NowPlayingStatus is where the engine is with one TiVo's list. It is also
// the payload of the nowplaying.changed event.
type NowPlayingStatus struct {
	TSN     string `json:"tsn"`
	Loading bool   `json:"loading"`
	Loaded  int    `json:"loaded"` // entries received so far, while loading
	Total   int    `json:"total"`
	// Error is why the last refresh failed, in words fit for the user, or
	// empty.
	Error string `json:"error"`
	// Fetched is when the list was last confirmed with the TiVo.
	Fetched time.Time `json:"fetched"`
	// Unchanged is true when the last refresh found the TiVo's list had
	// not changed, so the copy already held was kept.
	Unchanged bool `json:"unchanged"`
}

// NowPlaying is a TiVo's list together with its status.
type NowPlaying struct {
	NowPlayingStatus
	Recordings []togo.Recording `json:"recordings"`
}

// nowPlaying is the engine's state for one TiVo.
type nowPlaying struct {
	status  NowPlayingStatus
	listing *togo.Listing
}

var (
	// ErrNoMAK means a TiVo cannot be contacted until a key is stored.
	ErrNoMAK = errors.New("enter your Media Access Key in Settings first")
	// ErrUnknownTiVo means no TiVo with that TSN has been found.
	ErrUnknownTiVo = errors.New("that TiVo has not been found on the network")
)

// TiVos returns the TiVos found on the network, sorted by name.
func (e *Engine) TiVos() []tivo.Box { return e.tivos.List() }

// Registry returns the list of TiVos, for discovery and for tests.
func (e *Engine) Registry() *tivo.Registry { return &e.tivos }

// makFor returns the key to use with a TiVo: its own, if it has one, or
// the account's.
func (e *Engine) makFor(tsn string) (string, error) {
	if mak, err := e.secrets.Get(secrets.TiVoMAK(tsn)); err == nil {
		return mak, nil
	}
	mak, err := e.secrets.Get(secrets.MAK)
	if errors.Is(err, secrets.ErrNotFound) {
		return "", ErrNoMAK
	}
	return mak, err
}

// loadListings brings back the lists saved by the last run, so that Now
// Playing has something to show before any TiVo has answered.
func (e *Engine) loadListings() {
	saved, err := e.store.Listings()
	if err != nil {
		e.logger.Warn("could not read the saved Now Playing lists", "err", err)
		return
	}
	for tsn, l := range saved {
		n := len(l.List.Recordings)
		e.npl[tsn] = &nowPlaying{
			listing: l.List,
			status:  NowPlayingStatus{TSN: tsn, Loaded: n, Total: n, Fetched: l.Fetched},
		}
	}
}

// NowPlaying returns what the engine currently holds for a TiVo. It never
// touches the network; call RefreshNowPlaying for that.
func (e *Engine) NowPlaying(tsn string) NowPlaying {
	e.nplMu.Lock()
	defer e.nplMu.Unlock()
	out := NowPlaying{NowPlayingStatus: NowPlayingStatus{TSN: tsn}, Recordings: []togo.Recording{}}
	if st := e.npl[tsn]; st != nil {
		out.NowPlayingStatus = st.status
		if st.listing != nil {
			out.Recordings = st.listing.Recordings
		}
	}
	return out
}

// RefreshNowPlaying starts fetching a TiVo's list in the background and
// returns at once. Progress and the outcome arrive as nowplaying.changed
// events. If a refresh is already running, this does nothing.
//
// The TiVo is first asked whether its list has changed; if not, the copy
// already held is kept and the refresh costs one small request.
func (e *Engine) RefreshNowPlaying(tsn string) error {
	box, ok := e.tivos.Get(tsn)
	if !ok {
		return ErrUnknownTiVo
	}
	mak, err := e.makFor(tsn)
	if err != nil {
		return err
	}
	e.log.Hide(mak)

	e.nplMu.Lock()
	st := e.npl[tsn]
	if st == nil {
		st = &nowPlaying{status: NowPlayingStatus{TSN: tsn}}
		e.npl[tsn] = st
	}
	if st.status.Loading {
		e.nplMu.Unlock()
		return nil
	}
	client, cached := e.clientFor(box, mak), st.listing
	st.status.Loading, st.status.Error, st.status.Loaded = true, "", 0
	status := st.status
	e.nplMu.Unlock()
	e.bus.Publish(events.NowPlayingChanged, status)

	e.done.Add(1)
	go func() {
		defer e.done.Done()
		start := time.Now()

		// update applies a change to the status and announces it.
		update := func(change func(*nowPlaying)) {
			e.nplMu.Lock()
			change(st)
			status := st.status
			e.nplMu.Unlock()
			e.bus.Publish(events.NowPlayingChanged, status)
		}
		fail := func(err error) {
			e.logger.Warn("could not read Now Playing", "tivo", box.Name, "err", err)
			update(func(st *nowPlaying) {
				st.status.Loading = false
				st.status.Error = err.Error()
			})
		}

		if cached != nil {
			marker, _, err := client.Summary(e.ctx)
			if err != nil {
				fail(err)
				return
			}
			if marker == cached.LastChange {
				e.logger.Info("Now Playing is unchanged", "tivo", box.Name,
					"recordings", len(cached.Recordings), "took", time.Since(start).Round(time.Millisecond))
				update(func(st *nowPlaying) {
					st.status.Loading, st.status.Unchanged = false, true
					st.status.Fetched = time.Now()
				})
				if err := e.store.TouchListing(tsn, time.Now()); err != nil {
					e.logger.Warn("could not save the Now Playing list", "err", err)
				}
				return
			}
		}

		listing, err := client.NowPlaying(e.ctx, func(loaded, total int) {
			update(func(st *nowPlaying) { st.status.Loaded, st.status.Total = loaded, total })
		})
		if err != nil {
			fail(err)
			return
		}
		e.logger.Info("Now Playing loaded", "tivo", box.Name,
			"recordings", len(listing.Recordings), "took", time.Since(start).Round(time.Millisecond))
		update(func(st *nowPlaying) {
			st.listing = listing
			st.status.Loading, st.status.Unchanged = false, false
			st.status.Loaded, st.status.Total = len(listing.Recordings), len(listing.Recordings)
			st.status.Fetched = time.Now()
		})
		if err := e.store.SaveListing(tsn, time.Now(), listing); err != nil {
			e.logger.Warn("could not save the Now Playing list", "err", err)
		}
	}()
	return nil
}
