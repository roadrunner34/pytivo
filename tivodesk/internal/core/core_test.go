package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tivodesk/internal/config"
	"tivodesk/internal/events"
	"tivodesk/internal/secrets"
)

func newEngine(t *testing.T, dir string, store secrets.Store) *Engine {
	t.Helper()
	e, err := New(Options{ConfigPath: filepath.Join(dir, "config.toml"), Secrets: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func logText(e *Engine) string {
	var b strings.Builder
	for _, entry := range e.Logs() {
		b.WriteString(entry.Message + "\n")
	}
	return b.String()
}

func TestFirstRunThenRestart(t *testing.T) {
	dir, store := t.TempDir(), secrets.NewMemory()
	e := newEngine(t, dir, store)

	guid := e.Config().Server["guid"]
	if len(guid) != 36 {
		t.Fatalf("first run should create a GUID, got %q", guid)
	}
	if e.HasMAK() {
		t.Error("a first run has no MAK")
	}

	var topics []string
	e.Events().Subscribe(func(ev events.Event) {
		if ev.Topic != events.LogEntry {
			topics = append(topics, ev.Topic)
		}
	})

	c := e.Config()
	c.Share["Movies"] = config.Settings{"type": "video", "path": `D:\Movies`}
	c.Server["audio_br"] = "loud"
	c.Server["guid"] = "tampered"
	problems, err := e.SaveConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].Name != "audio_br" {
		t.Errorf("problems = %v", problems)
	}
	if got := e.Problems(); len(got) != 1 || got[0].Name != "audio_br" {
		t.Errorf("Problems() = %v", got)
	}
	if len(topics) != 1 || topics[0] != events.ConfigChanged {
		t.Errorf("events = %v", topics)
	}

	// Changing the returned copy must not change the engine's state.
	c.Share["Movies"]["path"] = "changed after save"
	if got := e.Config().Share["Movies"]["path"]; got != `D:\Movies` {
		t.Errorf("the engine shares state with its caller: %q", got)
	}

	// A new engine on the same files sees the same server.
	again := newEngine(t, dir, store)
	c2 := again.Config()
	if c2.Server["guid"] != guid {
		t.Errorf("GUID changed across a restart or a save: %q -> %q", guid, c2.Server["guid"])
	}
	if c2.Share["Movies"]["path"] != `D:\Movies` {
		t.Errorf("share was not persisted: %v", c2.Share)
	}
	if !strings.Contains(logText(again), "will not work as written") {
		t.Error("startup should warn about the bad audio_br")
	}
}

func TestMAK(t *testing.T) {
	store := secrets.NewMemory()
	e := newEngine(t, t.TempDir(), store)

	for _, bad := range []string{"", "12345", "12345678901", "abcdefghij"} {
		if err := e.SetMAK(bad); !errors.Is(err, ErrInvalidMAK) {
			t.Errorf("SetMAK(%q) = %v, want ErrInvalidMAK", bad, err)
		}
	}
	if e.HasMAK() {
		t.Fatal("an invalid MAK was stored")
	}

	const mak = "1234567890"
	if err := e.SetMAK(mak); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(secrets.MAK); got != mak || !e.HasMAK() {
		t.Fatal("the MAK was not stored")
	}
	e.Logger().Info("using key " + mak)
	if strings.Contains(logText(e), mak) {
		t.Error("the MAK appeared in the log")
	}

	// After a restart the stored key is hidden from the very first line.
	again := newEngine(t, t.TempDir(), store)
	again.Logger().Info("still using " + mak)
	if strings.Contains(logText(again), mak) {
		t.Error("the MAK appeared in the log after a restart")
	}

	if err := e.ClearMAK(); err != nil || e.HasMAK() {
		t.Errorf("ClearMAK: %v, HasMAK=%v", err, e.HasMAK())
	}
}

func TestImport(t *testing.T) {
	dir, store := t.TempDir(), secrets.NewMemory()
	e := newEngine(t, dir, store)
	guid := e.Config().Server["guid"]
	conf := filepath.Join("..", "pytivoimport", "testdata", "full.conf")

	// A preview changes nothing.
	p, err := e.PreviewImport(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasMAK || p.TiVoMAKs != 1 || len(p.Warnings) == 0 || len(p.Config.Share) != 3 {
		t.Errorf("preview = %+v", p)
	}
	if e.HasMAK() || len(e.Config().Share) != 0 {
		t.Fatal("a preview changed the engine")
	}

	p, err = e.ApplyImport(conf)
	if err != nil {
		t.Fatal(err)
	}
	c := e.Config()
	if len(c.Share) != 3 || c.Server["port"] != "9032" || c.Server["guid"] != guid {
		t.Errorf("config after import: %+v", c)
	}
	if p.Config.Server["guid"] != guid {
		t.Error("the returned config should be the saved one")
	}
	if got, _ := store.Get(secrets.MAK); got != "1234567890" {
		t.Errorf("stored MAK = %q", got)
	}
	if got, _ := store.Get(secrets.TiVoMAK("7460001902A1B2C")); got != "0987654321" {
		t.Errorf("stored TiVo MAK = %q", got)
	}

	// Neither key may be on disk or in the log.
	raw, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	e.Logger().Info("keys 1234567890 and 0987654321")
	for _, text := range []string{string(raw), logText(e)} {
		if strings.Contains(text, "1234567890") || strings.Contains(text, "0987654321") {
			t.Errorf("a key leaked:\n%s", text)
		}
	}

	if _, err := e.ApplyImport(filepath.Join(dir, "missing.conf")); err == nil {
		t.Error("importing a missing file should fail")
	}
}

func TestImportRejectsMalformedMAK(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "pyTivo.conf")
	os.WriteFile(conf, []byte("[Server]\ntivo_mak = <Your Media Access Key>\n"), 0o644)
	e := newEngine(t, dir, secrets.NewMemory())

	p, err := e.ApplyImport(conf)
	if err != nil {
		t.Fatal(err)
	}
	if p.HasMAK || e.HasMAK() {
		t.Error("a placeholder MAK was stored")
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "not 10 digits") {
		t.Errorf("warnings = %v", p.Warnings)
	}
}

func TestCorruptConfigIsAnError(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[server\n"), 0o644)
	if _, err := New(Options{ConfigPath: filepath.Join(dir, "config.toml"), Secrets: secrets.NewMemory()}); err == nil {
		t.Error("a corrupt configuration must not be silently replaced")
	}
}
