// Package config holds TivoDesk's settings and the rules for looking them
// up.
//
// Settings are strings, as in pyTivo, and are resolved in the same order:
// the TiVo's own section, then its class (SD, HD or 4K), then the server
// section. Keeping pyTivo's semantics exactly is what lets the golden
// tests compare TivoDesk's output with pyTivo's.
//
// Secrets (the Media Access Key) are never stored here; see package secrets.
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Settings is one section of name/value pairs. Names are lower case.
type Settings map[string]string

// Share types TivoDesk can serve.
const (
	ShareVideo = "video"
	ShareMusic = "music"
	SharePhoto = "photo"
)

// Class names, used as keys of Config.Class.
const (
	ClassSD = "SD"
	ClassHD = "HD"
	Class4K = "4K"
)

// Config is the whole configuration file.
type Config struct {
	Server Settings            `toml:"server" json:"server"`
	Class  map[string]Settings `toml:"class,omitempty" json:"class"` // keyed by SD, HD, 4K
	TiVo   map[string]Settings `toml:"tivo,omitempty" json:"tivo"`   // keyed by TSN
	Share  map[string]Settings `toml:"share,omitempty" json:"share"` // keyed by share name
}

// New returns an empty configuration with every map ready to use.
func New() *Config {
	c := &Config{}
	c.normalize()
	return c
}

func (c *Config) normalize() {
	if c.Server == nil {
		c.Server = Settings{}
	}
	if c.Class == nil {
		c.Class = map[string]Settings{}
	}
	if c.TiVo == nil {
		c.TiVo = map[string]Settings{}
	}
	if c.Share == nil {
		c.Share = map[string]Settings{}
	}
}

// Clone returns a deep copy.
func (c *Config) Clone() *Config {
	cloneMap := func(m map[string]Settings) map[string]Settings {
		out := make(map[string]Settings, len(m))
		for k, v := range m {
			out[k] = cloneSettings(v)
		}
		return out
	}
	return &Config{
		Server: cloneSettings(c.Server),
		Class:  cloneMap(c.Class),
		TiVo:   cloneMap(c.TiVo),
		Share:  cloneMap(c.Share),
	}
}

func cloneSettings(s Settings) Settings {
	out := make(Settings, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// DefaultPath is where the configuration lives for the current user.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "TivoDesk", "config.toml"), nil
}

// Load reads the configuration at path. A missing file is not an error: it
// yields an empty configuration, which is what a first run looks like.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := toml.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	c.normalize()
	return c, nil
}

// Save writes the configuration to path. It writes a temporary file and
// renames it, so a crash cannot leave a half-written configuration.
func (c *Config) Save(path string) error {
	raw, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename has happened
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// GUID returns the server's identity on the network, creating one the first
// time. Unlike pyTivo, which invents a new GUID on every start, this one is
// saved with the configuration so TiVos keep recognising the server.
func (c *Config) GUID() string {
	if g := c.Server["guid"]; g != "" {
		return g
	}
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	g := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	c.Server["guid"] = g
	return g
}

// Lookup resolves a setting for a TiVo: its own section first, then its
// class, then the server section. tsn may be empty, in which case the SD
// class applies, as it does in pyTivo.
func (c *Config) Lookup(name, tsn string) (string, bool) {
	for _, section := range []Settings{c.TiVo[tsn], c.Class[ClassOf(tsn)], c.Server} {
		if v, ok := section[name]; ok {
			return v, true
		}
	}
	return "", false
}

// Get is Lookup with the empty string standing for "not set".
func (c *Config) Get(name, tsn string) string {
	v, _ := c.Lookup(name, tsn)
	return v
}

// ServerValue returns a server setting, or def if it is not set.
func (c *Config) ServerValue(name, def string) string {
	if v, ok := c.Server[name]; ok {
		return v
	}
	return def
}

// ParseBool reads a boolean the way pyTivo does. ok is false when the text
// is not a recognised boolean.
func ParseBool(s string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "yes", "true", "on":
		return true, true
	case "0", "no", "false", "off":
		return false, true
	}
	return false, false
}

// ServerBool returns a boolean server setting, or def if it is missing or
// not a boolean.
func (c *Config) ServerBool(name string, def bool) bool {
	if v, ok := ParseBool(c.Server[name]); ok {
		return v
	}
	return def
}

// Port is the HMO server's TCP port.
func (c *Config) Port() string { return c.ServerValue("port", "9032") }

// BeaconAddresses are the broadcast addresses for the discovery beacon.
func (c *Config) BeaconAddresses() string { return c.ServerValue("beacon", "255.255.255.255") }

// AllowedClients are the IP prefixes allowed to use the HMO server. An
// empty list means everyone is allowed.
func (c *Config) AllowedClients() []string { return strings.Fields(c.Server["allowedips"]) }

// IsKnownTiVo reports whether the TSN has its own section.
func (c *Config) IsKnownTiVo(tsn string) bool {
	_, ok := c.TiVo[tsn]
	return ok
}

// IsExternal reports whether a TiVo is marked as being outside the LAN.
func (c *Config) IsExternal(tsn string) bool {
	v, _ := ParseBool(c.TiVo[tsn]["external"])
	return v
}

// ZeroconfEnabled reports whether shares should be advertised over mDNS.
// In the default "auto" mode, advertising is turned off when any TiVo has a
// restricted share list, because mDNS would announce every share to
// everyone.
func (c *Config) ZeroconfEnabled() bool {
	opt := strings.ToLower(c.ServerValue("zeroconf", "auto"))
	if opt == "auto" {
		for _, section := range c.TiVo {
			if _, ok := section["shares"]; ok {
				return false
			}
		}
		for _, section := range c.Class {
			if _, ok := section["shares"]; ok {
				return false
			}
		}
		return true
	}
	if v, ok := ParseBool(opt); ok && !v {
		return false
	}
	return true
}

// TSFlag is the transport-stream policy: "auto", "on" or "off".
func (c *Config) TSFlag() string { return strings.ToLower(c.ServerValue("ts", "auto")) }

// OptRes reports whether "optimal resolution" is on for a TiVo. The first
// section that holds a valid boolean decides.
func (c *Config) OptRes(tsn string) bool {
	for _, section := range []Settings{c.TiVo[tsn], c.Class[ClassOf(tsn)], c.Server} {
		if v, ok := ParseBool(section["optres"]); ok {
			return v
		}
	}
	return false
}

// FFmpegParams is the user's ffmpeg argument template for a TiVo, if any.
func (c *Config) FFmpegParams(tsn string) string { return c.Get("ffmpeg_pram", tsn) }

// Aspect169 reports whether a TiVo should be sent 16:9 video as it is.
func (c *Config) Aspect169(tsn string) bool {
	if tsn == "" {
		return true
	}
	if v, ok := ParseBool(c.TiVo[tsn]["aspect169"]); ok {
		return v
	}
	return !Is169Blacklisted(tsn) && !Is169Letterbox(tsn)
}

// NamedShare is a share together with its name.
type NamedShare struct {
	Name     string
	Settings Settings
}

// Type is the share's kind: video, music or photo.
func (s NamedShare) Type() string { return strings.ToLower(s.Settings["type"]) }

// Path is the folder the share serves.
func (s NamedShare) Path() string { return s.Settings["path"] }

// Shares lists the shares a TiVo may see, sorted by name. A TiVo whose
// section has a "shares" setting sees only the shares named there.
func (c *Config) Shares(tsn string) []NamedShare {
	var out []NamedShare
	if list, ok := c.TiVo[tsn]["shares"]; ok {
		for _, name := range strings.Split(list, ",") {
			name = strings.TrimSpace(name)
			if s, ok := c.Share[name]; ok {
				out = append(out, NamedShare{name, s})
			}
		}
	} else {
		for name, s := range c.Share {
			out = append(out, NamedShare{name, s})
		}
	}
	slices.SortFunc(out, func(a, b NamedShare) int { return strings.Compare(a.Name, b.Name) })
	return out
}
