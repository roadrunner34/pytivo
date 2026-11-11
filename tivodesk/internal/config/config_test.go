package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func parse(t *testing.T, text string) *Config {
	t.Helper()
	c := &Config{}
	if err := toml.Unmarshal([]byte(text), c); err != nil {
		t.Fatal(err)
	}
	c.normalize()
	return c
}

// The tests below with a "pyTivo:" note are ports of tests/test_config.py.

// pyTivo: test_strtod_suffixes
func TestStrtodSuffixes(t *testing.T) {
	for in, want := range map[string]float64{
		"2K": 2000, "2Ki": 2048, "2MB": 16000000, "2MiB": 16777216, "448k": 448000, " 5 ": 5,
	} {
		got, err := Strtod(in)
		if err != nil || got != want {
			t.Errorf("Strtod(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"fast", "", "1.5M", "-3k", "2Kib2"} {
		if _, err := Strtod(bad); !errors.Is(err, ErrBitValue) {
			t.Errorf("Strtod(%q): got %v, want ErrBitValue", bad, err)
		}
	}
}

// pyTivo: test_bitrates_are_integers
func TestBitrates(t *testing.T) {
	c := parse(t, `
[server]
audio_br = "320K"
video_br = "12Mi"
`)
	checks := []struct{ name, got, want string }{
		{"AudioBR", c.AudioBR(""), "320k"},
		{"VideoBR", c.VideoBR(""), "12582k"},
		{"MaxVideoBR", c.MaxVideoBR(""), "30000k"},
		{"BufSize HD", c.BufSize("746000000000000"), "4096k"},
	}
	for _, k := range checks {
		if k.got != k.want {
			t.Errorf("%s = %q, want %q", k.name, k.got, k.want)
		}
	}
	if got := c.MaxAudioBR(""); got != 448 {
		t.Errorf("MaxAudioBR = %d, want 448", got)
	}
}

func TestBitrateDefaultsByClass(t *testing.T) {
	c := New()
	for _, k := range []struct{ tsn, video, buf string }{
		{"5400001", "4096K", "1024k"},
		{"7460001", "16384K", "4096k"},
		{"8490001", "30000k", "8192k"},
	} {
		if got := c.VideoBR(k.tsn); got != k.video {
			t.Errorf("VideoBR(%s) = %q, want %q", k.tsn, got, k.video)
		}
		if got := c.BufSize(k.tsn); got != k.buf {
			t.Errorf("BufSize(%s) = %q, want %q", k.tsn, got, k.buf)
		}
	}

	// Audio is capped by max_audio_br and rounded down to a multiple of 64.
	c.Server["audio_br"] = "500k"
	if got := c.AudioBR(""); got != "448k" {
		t.Errorf("AudioBR capped = %q, want 448k", got)
	}
	c.Server["audio_br"] = "200k"
	if got := c.AudioBR(""); got != "192k" {
		t.Errorf("AudioBR rounded = %q, want 192k", got)
	}
	c.Server["audio_br"] = "10k"
	if got := c.AudioBR(""); got != "64k" {
		t.Errorf("AudioBR floor = %q, want 64k", got)
	}
}

// pyTivo: test_get_tsn_fallback_order
func TestLookupFallbackOrder(t *testing.T) {
	c := parse(t, `
[server]
audio_lang = "eng"
ffmpeg_pram = "-threads 2 %s"

[class.HD]
audio_lang = "spa"

[tivo.7460001]
audio_lang = "fre"
`)
	for tsn, want := range map[string]string{
		"7460001": "fre", // the TiVo's own section
		"7460002": "spa", // its class
		"5400001": "eng", // the server section
	} {
		if got := c.Get("audio_lang", tsn); got != want {
			t.Errorf("audio_lang for %s = %q, want %q", tsn, got, want)
		}
	}
	if _, ok := c.Lookup("missing", "7460001"); ok {
		t.Error("Lookup found a setting that does not exist")
	}
	if got := c.FFmpegParams("7460001"); got != "-threads 2 %s" {
		t.Errorf("FFmpegParams = %q; the %% must survive", got)
	}
}

// pyTivo: test_tivo_classes
func TestTivoClasses(t *testing.T) {
	type row struct {
		tsn             string
		hd, fourK, ts   bool
		class           string
		width, height   int
		letter, blacked bool
	}
	for _, r := range []row{
		{"", false, false, false, ClassSD, 544, 480, false, false},
		{"5400001", false, false, false, ClassSD, 544, 480, false, true},
		{"6490001", false, false, false, ClassSD, 544, 480, true, false}, // SD letterbox
		{"6480001", true, false, false, ClassHD, 1920, 1080, false, false},
		{"6630001", true, false, true, ClassHD, 1920, 1080, false, false}, // TS by special case
		{"7460001", true, false, true, ClassHD, 1920, 1080, false, false},
		{"8460001", true, false, true, ClassHD, 1920, 1080, false, false}, // Roamio
		{"8490001", true, true, true, Class4K, 3840, 2160, false, false},
		{"8F90001", true, true, true, Class4K, 3840, 2160, false, false},
		{"D6E0001", true, false, true, ClassHD, 1920, 1080, false, false}, // Edge, as pyTivo sees it
		{"84", true, false, true, ClassHD, 1920, 1080, false, false},      // shorter than a prefix
	} {
		got := row{r.tsn, IsHD(r.tsn), Is4K(r.tsn), IsTSCapable(r.tsn), ClassOf(r.tsn),
			TivoWidth(r.tsn), TivoHeight(r.tsn), Is169Letterbox(r.tsn), Is169Blacklisted(r.tsn)}
		if got != r {
			t.Errorf("TSN %q:\n got %+v\nwant %+v", r.tsn, got, r)
		}
	}
	if got := NearestTivoHeight(700); got != 720 {
		t.Errorf("NearestTivoHeight(700) = %d, want 720", got)
	}
	if got := NearestTivoWidth(600); got != 544 {
		t.Errorf("NearestTivoWidth(600) = %d, want 544", got)
	}
	if got := NearestTivoWidth(712); got != 720 { // tie between 704 and 720
		t.Errorf("NearestTivoWidth(712) = %d, want the larger, 720", got)
	}
}

// pyTivo: test_shares_sorted_with_builtins. TivoDesk has no built-in
// Settings, ToGo or VRD shares, so only the user's shares are listed.
func TestSharesSortedAndRestricted(t *testing.T) {
	c := parse(t, `
[share.Zebra]
type = "video"
path = "/z"

[share.Apple]
type = "Music"
path = "/a"

[tivo.7460001]
shares = " Zebra , Nowhere"
`)
	names := func(shares []NamedShare) []string {
		var out []string
		for _, s := range shares {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names(c.Shares("")); !reflect.DeepEqual(got, []string{"Apple", "Zebra"}) {
		t.Errorf("all shares = %v", got)
	}
	if got := names(c.Shares("7460001")); !reflect.DeepEqual(got, []string{"Zebra"}) {
		t.Errorf("restricted shares = %v, want only the valid name", got)
	}
	if s := c.Shares("")[0]; s.Type() != ShareMusic || s.Path() != "/a" {
		t.Errorf("share accessors: type %q path %q", s.Type(), s.Path())
	}
	if c.ZeroconfEnabled() {
		t.Error("zeroconf should turn itself off when a TiVo has a restricted share list")
	}
}

func TestServerSettings(t *testing.T) {
	c := New()
	if c.Port() != "9032" || c.BeaconAddresses() != "255.255.255.255" || c.TSFlag() != "auto" {
		t.Error("wrong defaults")
	}
	if !c.ZeroconfEnabled() {
		t.Error("zeroconf should default to on")
	}
	c.Server["zeroconf"] = "Off"
	if c.ZeroconfEnabled() {
		t.Error("zeroconf = Off was ignored")
	}
	c.Server["allowedips"] = "192.168.1. 10.0.0.5"
	if got := c.AllowedClients(); !reflect.DeepEqual(got, []string{"192.168.1.", "10.0.0.5"}) {
		t.Errorf("AllowedClients = %v", got)
	}
	c.Server["debug"] = "maybe"
	if c.ServerBool("debug", false) || !c.ServerBool("debug", true) {
		t.Error("an unparseable boolean should give the default")
	}
}

func TestAspectAndOptRes(t *testing.T) {
	c := parse(t, `
[server]
optres = "true"
[class.HD]
optres = "nonsense"
[tivo.5400001]
aspect169 = "yes"
[tivo.7460001]
optres = "false"
external = "1"
`)
	if !c.Aspect169("") || !c.Aspect169("7460002") {
		t.Error("16:9 should be allowed with no TSN and on HD boxes")
	}
	if c.Aspect169("5400002") || c.Aspect169("6490001") {
		t.Error("16:9 should be off for blacklisted and letterbox boxes")
	}
	if !c.Aspect169("5400001") {
		t.Error("the TiVo's own aspect169 setting should win")
	}
	if c.OptRes("7460001") {
		t.Error("the TiVo's own optres = false should win")
	}
	if !c.OptRes("7460002") {
		t.Error("an invalid class value should fall through to the server's")
	}
	if !c.IsExternal("7460001") || c.IsExternal("7460002") || !c.IsKnownTiVo("7460001") {
		t.Error("IsExternal or IsKnownTiVo is wrong")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")

	// A missing file is a first run, not an error.
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Server["ffmpeg_pram"] = `-threads 2 %s "quoted"`
	c.Share["Vidéos"] = Settings{"type": "video", "path": `C:\Vidéos\TV`}
	c.TiVo["7460001"] = Settings{"audio_lang": "fre"}
	c.Class[ClassHD] = Settings{"video_br": "12Mi"}
	guid := c.GUID()
	if len(guid) != 36 || c.GUID() != guid {
		t.Fatalf("GUID should be created once and then kept: %q", guid)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}

	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, c) {
		t.Errorf("round trip changed the config\n got %+v\nwant %+v", back, c)
	}

	// Clone must not share maps with the original.
	clone := c.Clone()
	clone.Server["port"] = "1"
	clone.Share["Vidéos"]["path"] = "elsewhere"
	if c.Server["port"] != "" || c.Share["Vidéos"]["path"] != `C:\Vidéos\TV` {
		t.Error("Clone shares state with the original")
	}

	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}

	if err := os.WriteFile(path, []byte("server = [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a corrupt file should be an error, not an empty config")
	}
}

func TestValidate(t *testing.T) {
	c := parse(t, `
[server]
audio_br = "loud"
[tivo.7460001]
video_br = "12Mi"
bufsize = "big"
[share.Good]
type = "video"
path = "/v"
[share.Bad]
type = "settings"
`)
	got := map[string]bool{}
	for _, p := range c.Validate() {
		got[p.Section+"/"+p.Name] = true
	}
	want := map[string]bool{
		"server/audio_br": true, "tivo.7460001/bufsize": true,
		"share.Bad/type": true, "share.Bad/path": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Validate found %v, want %v", got, want)
	}
	// A bad value behaves as if it were not set.
	if c.AudioBR("") != "448k" || c.BufSize("7460001") != "4096k" {
		t.Error("an invalid bit rate should fall back to the default")
	}
}
