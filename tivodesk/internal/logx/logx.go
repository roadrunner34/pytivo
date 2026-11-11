// Package logx is TivoDesk's logging: a slog handler that keeps the most
// recent entries in memory for the log viewer and removes secrets from
// everything it records.
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// mask replaces a secret wherever it would have been logged.
const mask = "[hidden]"

// Entry is one log line, as shown in the log viewer.
type Entry struct {
	Seq     uint64    `json:"seq"` // increases by one per entry
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`   // DEBUG, INFO, WARN or ERROR
	Message string    `json:"message"` // message followed by key=value pairs
}

// Log collects entries. Create one with New.
type Log struct {
	mu      sync.Mutex
	secrets []string
	ring    []Entry // the last len(ring) entries, oldest at start
	start   int
	count   int
	seq     uint64
	onEntry func(Entry)
	out     io.Writer
	level   slog.LevelVar
}

// New returns a log that remembers the last capacity entries. If out is not
// nil, each entry is also written there as a line of text.
func New(capacity int, out io.Writer) *Log {
	return &Log{ring: make([]Entry, capacity), out: out}
}

// Logger returns a slog.Logger that records into the log.
func (l *Log) Logger() *slog.Logger { return slog.New(&handler{log: l}) }

// SetLevel sets the least severe level that is recorded. The default is
// Info.
func (l *Log) SetLevel(level slog.Level) { l.level.Set(level) }

// OnEntry sets a function to call with each new entry. It is called on the
// logging goroutine and must not log.
func (l *Log) OnEntry(fn func(Entry)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onEntry = fn
}

// Hide registers a secret, such as the Media Access Key, that must never
// appear in the log. It applies to entries recorded from now on. Very short
// strings are ignored, because masking them would shred ordinary text.
func (l *Log) Hide(secret string) {
	if len(secret) < 4 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.secrets {
		if s == secret {
			return
		}
	}
	l.secrets = append(l.secrets, secret)
}

// Entries returns the remembered entries, oldest first.
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, 0, l.count)
	for i := 0; i < l.count; i++ {
		out = append(out, l.ring[(l.start+i)%len(l.ring)])
	}
	return out
}

func (l *Log) record(t time.Time, level slog.Level, text string) {
	l.mu.Lock()
	for _, s := range l.secrets {
		text = strings.ReplaceAll(text, s, mask)
	}
	l.seq++
	e := Entry{Seq: l.seq, Time: t, Level: level.String(), Message: text}
	if n := len(l.ring); n > 0 {
		if l.count < n {
			l.ring[(l.start+l.count)%n] = e
			l.count++
		} else {
			l.ring[l.start] = e
			l.start = (l.start + 1) % n
		}
	}
	fn, out := l.onEntry, l.out
	l.mu.Unlock()

	if out != nil {
		fmt.Fprintf(out, "%s %-5s %s\n", t.Format("2006-01-02 15:04:05.000"), e.Level, text)
	}
	if fn != nil {
		fn(e)
	}
}

// handler adapts Log to slog. Attributes are flattened into the message as
// key=value pairs, which is what a human reading the log viewer wants.
type handler struct {
	log    *Log
	prefix string // group path for attribute keys, such as "togo."
	attrs  string // attributes added by With, already formatted
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.log.level.Level()
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	b.WriteString(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, h.prefix, a)
		return true
	})
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	h.log.record(t, r.Level, b.String())
	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	b.WriteString(h.attrs)
	for _, a := range attrs {
		writeAttr(&b, h.prefix, a)
	}
	return &handler{log: h.log, prefix: h.prefix, attrs: b.String()}
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &handler{log: h.log, prefix: h.prefix + name + ".", attrs: h.attrs}
}

func writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, ga := range v.Group() {
			writeAttr(b, p, ga)
		}
		return
	}
	if a.Key == "" {
		return
	}
	text := v.String()
	if strings.ContainsAny(text, " \t\n\"=") || text == "" {
		text = fmt.Sprintf("%q", text)
	}
	b.WriteString(" ")
	b.WriteString(prefix)
	b.WriteString(a.Key)
	b.WriteString("=")
	b.WriteString(text)
}
