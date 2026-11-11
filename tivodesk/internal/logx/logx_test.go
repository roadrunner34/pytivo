package logx

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretsNeverReachTheLog(t *testing.T) {
	const mak = "1234567890"
	var out bytes.Buffer
	l := New(10, &out)
	var seen []Entry
	l.OnEntry(func(e Entry) { seen = append(seen, e) })
	l.Hide(mak)
	l.Hide(mak) // registering twice is harmless
	log := l.Logger()

	// Every route a value can take into a log line.
	log.Info("fetching https://tivo:" + mak + "@192.168.1.5/nowplaying")
	log.Warn("auth failed", "mak", mak)
	log.With("key", mak).Error("with attr")
	log.WithGroup("togo").Info("grouped", slog.Group("auth", slog.String("pass", mak)))
	log.Error("wrapped", "err", errors.New("401 for user tivo:"+mak))

	for _, text := range []string{out.String(), joined(l.Entries()), joined(seen)} {
		if strings.Contains(text, mak) {
			t.Fatalf("the MAK leaked:\n%s", text)
		}
		if strings.Count(text, mask) != 5 {
			t.Errorf("want 5 masked values, got %d in:\n%s", strings.Count(text, mask), text)
		}
	}
	if got := l.Entries()[3].Message; got != "grouped togo.auth.pass="+mask {
		t.Errorf("group keys: %q", got)
	}
}

func joined(entries []Entry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Level + " " + e.Message + "\n")
	}
	return b.String()
}

func TestShortSecretsAreNotMasked(t *testing.T) {
	l := New(4, nil)
	l.Hide("a")
	l.Logger().Info("a plain message")
	if got := l.Entries()[0].Message; got != "a plain message" {
		t.Errorf("message was mangled: %q", got)
	}
}

func TestRingKeepsTheNewest(t *testing.T) {
	l := New(3, nil)
	log := l.Logger()
	if len(l.Entries()) != 0 {
		t.Fatal("a new log should be empty")
	}
	for _, m := range []string{"one", "two", "three", "four", "five"} {
		log.Info(m)
	}
	got := l.Entries()
	if len(got) != 3 || got[0].Message != "three" || got[2].Message != "five" {
		t.Fatalf("ring = %v", got)
	}
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Errorf("sequence numbers should keep counting: %d..%d", got[0].Seq, got[2].Seq)
	}
}

func TestLevelsAndFormatting(t *testing.T) {
	l := New(10, nil)
	log := l.Logger()
	log.Debug("hidden by default")
	l.SetLevel(slog.LevelDebug)
	log.Debug("now visible", "n", 3, "path", `C:\My Videos`, "empty", "")

	got := l.Entries()
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	want := `now visible n=3 path="C:\\My Videos" empty=""`
	if got[0].Message != want || got[0].Level != "DEBUG" || got[0].Time.IsZero() {
		t.Errorf("entry = %+v\nwant message %q", got[0], want)
	}
}
