// Package events is the engine's publish/subscribe bus. The engine
// publishes; the desktop UI and the CLI subscribe.
package events

import "sync"

// Topics published by the engine.
const (
	ConfigChanged = "config.changed" // data: nil
	MAKChanged    = "mak.changed"    // data: bool, whether a MAK is now stored
	LogEntry      = "log.entry"      // data: logx.Entry
	TiVosChanged  = "tivos.changed"  // data: nil
	// NowPlayingChanged reports progress on one TiVo's list.
	// data: core.NowPlayingStatus
	NowPlayingChanged = "nowplaying.changed"
	// TransfersChanged says the list of downloads, or the state of one,
	// has changed. data: nil
	TransfersChanged = "transfers.changed"
	// TransferProgress reports on the download under way, about twice a
	// second. data: core.TransferProgress
	TransferProgress = "transfer.progress"
)

// Event is one message on the bus.
type Event struct {
	Topic string
	Data  any
}

// Bus delivers events to every subscriber. The zero value is ready to use.
type Bus struct {
	mu   sync.RWMutex
	next int
	subs map[int]func(Event)
}

// Subscribe registers fn for every event and returns a function that
// removes it. fn is called on the publisher's goroutine, so it must return
// quickly and must not publish from inside the call.
func (b *Bus) Subscribe(fn func(Event)) (cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[int]func(Event){}
	}
	id := b.next
	b.next++
	b.subs[id] = fn
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

// Publish sends an event to the current subscribers.
func (b *Bus) Publish(topic string, data any) {
	b.mu.RLock()
	fns := make([]func(Event), 0, len(b.subs))
	for _, fn := range b.subs {
		fns = append(fns, fn)
	}
	b.mu.RUnlock()
	// Called outside the lock so a subscriber may unsubscribe itself.
	for _, fn := range fns {
		fn(Event{Topic: topic, Data: data})
	}
}
