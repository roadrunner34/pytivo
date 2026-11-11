package pytivoimport

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tivodesk/internal/config"
)

func hasWarning(res *Result, fragments ...string) bool {
	for _, w := range res.Warnings {
		all := true
		for _, f := range fragments {
			all = all && strings.Contains(w, f)
		}
		if all {
			return true
		}
	}
	return false
}

// The sample config that ships with pyTivo must import without complaint.
func TestImportsPyTivoConfDist(t *testing.T) {
	res, err := File(filepath.Join("..", "..", "..", "pyTivo.conf.dist"))
	if err != nil {
		t.Fatal(err)
	}
	c := res.Config
	if c.Port() != "9032" || c.Server["ffmpeg"] != "/usr/bin/ffmpeg" {
		t.Errorf("server settings: %v", c.Server)
	}
	want := map[string]config.Settings{"MyMovies": {"type": "video", "path": "/home/armooo/Videos"}}
	if !reflect.DeepEqual(c.Share, want) {
		t.Errorf("shares = %v, want %v", c.Share, want)
	}
	if res.MAK != "" || len(res.Warnings) != 0 {
		t.Errorf("unexpected MAK %q or warnings %v", res.MAK, res.Warnings)
	}
}

func TestFullConfig(t *testing.T) {
	res, err := File(filepath.Join("testdata", "full.conf"))
	if err != nil {
		t.Fatal(err)
	}
	c := res.Config

	// Keys are lower-cased; values keep "%" and inner "=" untouched.
	wantServer := config.Settings{
		"port":         "9032",
		"max_video_br": "17408k",
		"ffmpeg":       `C:\pyTivo\bin\ffmpeg.exe`,
		"ffmpeg_pram":  "-threads 2 %(video_br)s -x a=b",
		"togo_path":    `D:\TiVo Recordings`,
		"beacon":       "192.168.1.255 listen",
		"audio_br":     "320K",
	}
	if !reflect.DeepEqual(c.Server, wantServer) {
		t.Errorf("server\n got %v\nwant %v", c.Server, wantServer)
	}

	// The MAK goes to the caller, never into the configuration.
	if res.MAK != "1234567890" {
		t.Errorf("MAK = %q", res.MAK)
	}
	if !reflect.DeepEqual(res.TiVoMAKs, map[string]string{"7460001902A1B2C": "0987654321"}) {
		t.Errorf("TiVoMAKs = %v", res.TiVoMAKs)
	}
	saved := filepath.Join(t.TempDir(), "config.toml")
	if err := c.Save(saved); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(saved); strings.Contains(string(raw), "1234567890") ||
		strings.Contains(string(raw), "0987654321") || strings.Contains(string(raw), "mak") {
		t.Error("a Media Access Key reached the saved configuration")
	}

	// Class and per-TiVo sections land in the right place.
	// The unusable bufsize is kept as written (and warned about below), so
	// the user can see and fix it.
	if got := c.Class[config.ClassHD]; !reflect.DeepEqual(got, config.Settings{"video_br": "12Mi", "bufsize": "huge"}) {
		t.Errorf("class HD = %v", got)
	}
	if got := c.Class[config.ClassSD]; !reflect.DeepEqual(got, config.Settings{"aspect169": "false"}) {
		t.Errorf("class SD (written as _tivo_sd) = %v", got)
	}
	wantTiVo := config.Settings{"aspect169": "true", "shares": "Movies, Missing", "audio_lang": "fre"}
	if got := c.TiVo["7460001902A1B2C"]; !reflect.DeepEqual(got, wantTiVo) {
		t.Errorf("tivo section = %v", got)
	}

	// The lookup order works on imported data.
	if got := c.Get("video_br", "7460001902A1B2C"); got != "12Mi" {
		t.Errorf("video_br via class = %q", got)
	}
	if got := c.VideoBR("7460001902A1B2C"); got != "12582k" {
		t.Errorf("VideoBR = %q", got)
	}

	// Shares: types are normalised, multi-line values survive, and
	// pyTivo-only share types are left out.
	if got := c.Share["Movies"]; got["type"] != "video" || got["path"] != `D:\Movies` {
		t.Errorf("Movies = %v", got)
	}
	if got := c.Share["Tunes"]; got["type"] != "music" {
		t.Errorf("Tunes type = %q, want lower case", got["type"])
	}
	if got := c.Share["Movies"]["note"]; got != "first line\nsecond line" {
		t.Errorf("continuation value = %q", got)
	}
	for _, gone := range []string{"Admin", "Stray", "loggers", "logger_root"} {
		if _, ok := c.Share[gone]; ok {
			t.Errorf("section %q should not have become a share", gone)
		}
	}

	for _, want := range [][]string{
		{"tivolibre", "decrypts recordings itself"},
		{"[Admin]", "settings"},
		{"[Stray]", "no type"},
		{"logging"},
		{"share list", "Missing"},
		{"appears more than once"},
		{"set more than once", "audio_br"},
		{"class.HD", "bufsize"},
		{"before any [section]"},
		{"[Empty]", "no path"},
	} {
		if !hasWarning(res, want...) {
			t.Errorf("no warning containing %v in:\n  %s", want, strings.Join(res.Warnings, "\n  "))
		}
	}
}

// pyTivo: test_legacy_cp1252_config
func TestLegacyCP1252(t *testing.T) {
	res := Bytes([]byte("[Caf\xe9]\ntype = video\npath = C:\\Vid\xe9os\n"))
	if got := res.Config.Share["Café"]["path"]; got != `C:\Vidéos` {
		t.Errorf("cp1252 path = %q; shares: %v", got, res.Config.Share)
	}
}

func TestUTF8WithBOMAndCRLF(t *testing.T) {
	res := Bytes([]byte("\xef\xbb\xbf[Vidéos]\r\ntype=video\r\npath=C:\\Été\r\n"))
	if got := res.Config.Share["Vidéos"]["path"]; got != `C:\Été` {
		t.Errorf("UTF-8 path = %q; shares: %v", got, res.Config.Share)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings: %v", res.Warnings)
	}
}

func TestEmptyAndMissing(t *testing.T) {
	res := Bytes(nil)
	if len(res.Config.Share) != 0 || len(res.Warnings) != 0 {
		t.Errorf("empty input: %+v", res)
	}
	if _, err := File(filepath.Join(t.TempDir(), "nope.conf")); err == nil {
		t.Error("a missing file should be an error")
	}
}
