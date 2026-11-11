package events

import (
	"sync"
	"testing"
)

func TestPublishSubscribe(t *testing.T) {
	var bus Bus
	bus.Publish("nobody.listening", nil) // must not panic

	var a, b []Event
	cancelA := bus.Subscribe(func(e Event) { a = append(a, e) })
	bus.Subscribe(func(e Event) { b = append(b, e) })

	bus.Publish(ConfigChanged, nil)
	cancelA()
	cancelA() // cancelling twice is harmless
	bus.Publish(MAKChanged, true)

	if len(a) != 1 || a[0].Topic != ConfigChanged {
		t.Errorf("cancelled subscriber got %v", a)
	}
	if len(b) != 2 || b[1].Topic != MAKChanged || b[1].Data != true {
		t.Errorf("subscriber got %v", b)
	}
}

func TestSubscriberMayCancelItself(t *testing.T) {
	var bus Bus
	calls := 0
	var cancel func()
	cancel = bus.Subscribe(func(Event) {
		calls++
		cancel()
	})
	bus.Publish("x", nil)
	bus.Publish("x", nil)
	if calls != 1 {
		t.Errorf("got %d calls, want 1", calls)
	}
}

func TestConcurrentUse(t *testing.T) {
	var bus Bus
	var mu sync.Mutex
	count := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel := bus.Subscribe(func(Event) { mu.Lock(); count++; mu.Unlock() })
			for j := 0; j < 100; j++ {
				bus.Publish("x", j)
			}
			cancel()
		}()
	}
	wg.Wait()
	if count == 0 {
		t.Error("no events were delivered")
	}
}
