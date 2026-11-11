package config

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ErrBitValue reports text that is not a bit rate or size.
var ErrBitValue = errors.New("config: invalid bit value syntax")

var (
	bitValue = regexp.MustCompile(`^(\d+)(?:([yzafpnumcdhkKMGTPEZY])(i)?)?([Bb])?$`)
	exponent = map[string]float64{
		"y": -24, "z": -21, "a": -18, "f": -15, "p": -12,
		"n": -9, "u": -6, "m": -3, "c": -2, "d": -1,
		"h": 2, "k": 3, "K": 3, "M": 6, "G": 9,
		"T": 12, "P": 15, "E": 18, "Z": 21, "Y": 24,
	}
)

// Strtod parses a bit rate with SI or IEC suffixes the way ffmpeg does:
// 2K is 2000, 2Ki is 2048, 2MB is 16000000 and 2MiB is 16777216.
func Strtod(value string) (float64, error) {
	m := bitValue.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return 0, ErrBitValue
	}
	coef, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, ErrBitValue
	}
	if prefix := m[2]; prefix != "" {
		exp := exponent[prefix]
		if m[3] == "i" {
			coef *= math.Pow(2, exp/0.3) // powers of two
		} else {
			coef *= math.Pow(10, exp)
		}
	}
	if m[4] == "B" { // B is bytes, b is bits
		coef *= 8
	}
	return coef, nil
}

// trunc64 converts a rate to kilobits, rounded down to a non-zero multiple
// of 64, which is what ffmpeg's audio encoders accept.
func trunc64(rate string) (int, bool) {
	v, err := Strtod(rate)
	if err != nil {
		return 0, false
	}
	return max(int(v)/64000, 1) * 64, true
}

// kilo formats a rate as whole kilobits, such as "12582k".
func kilo(rate string) (string, bool) {
	v, err := Strtod(rate)
	if err != nil {
		return "", false
	}
	return strconv.Itoa(int(v)/1000) + "k", true
}

// A setting that does not parse is treated as not set, so the default
// applies. pyTivo raises an exception in the middle of a transfer instead;
// Validate reports the bad value up front.

// MaxAudioBR is the audio bit-rate ceiling in kilobits.
func (c *Config) MaxAudioBR(tsn string) int {
	if v, ok := trunc64(c.Get("max_audio_br", tsn)); ok {
		return v
	}
	return 448
}

// AudioBR is the audio bit rate to transcode to, such as "448k".
func (c *Config) AudioBR(tsn string) string {
	rate, ok := trunc64(c.Get("audio_br", tsn))
	if !ok {
		rate = 448
	}
	return strconv.Itoa(min(rate, c.MaxAudioBR(tsn))) + "k"
}

// MaxVideoBR is the video bit-rate ceiling.
func (c *Config) MaxVideoBR(tsn string) string {
	if v, ok := kilo(c.Get("max_video_br", tsn)); ok {
		return v
	}
	return "30000k"
}

// VideoBR is the video bit rate to transcode to.
func (c *Config) VideoBR(tsn string) string {
	if v, ok := kilo(c.Get("video_br", tsn)); ok {
		return v
	}
	switch ClassOf(tsn) {
	case Class4K:
		return c.MaxVideoBR(tsn)
	case ClassHD:
		return "16384K"
	}
	return "4096K"
}

// BufSize is the ffmpeg rate-control buffer size.
func (c *Config) BufSize(tsn string) string {
	if v, ok := kilo(c.Get("bufsize", tsn)); ok {
		return v
	}
	switch ClassOf(tsn) {
	case Class4K:
		return "8192k"
	case ClassHD:
		return "4096k"
	}
	return "1024k"
}

// bitSettings are the settings Strtod must be able to parse.
var bitSettings = []string{"audio_br", "max_audio_br", "video_br", "max_video_br", "bufsize"}

// Problem describes one setting that will not work as written.
type Problem struct {
	Section string `json:"section"` // "server", "class.HD", "tivo.<TSN>" or "share.<name>"
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Validate returns the settings that cannot be used as written.
func (c *Config) Validate() []Problem {
	var out []Problem
	check := func(section string, s Settings) {
		for _, name := range bitSettings {
			if v, ok := s[name]; ok {
				if _, err := Strtod(v); err != nil {
					out = append(out, Problem{section, name, "not a bit rate: " + strconv.Quote(v)})
				}
			}
		}
	}
	check("server", c.Server)
	for name, s := range c.Class {
		check("class."+name, s)
	}
	for tsn, s := range c.TiVo {
		check("tivo."+tsn, s)
	}
	for name, s := range c.Share {
		switch strings.ToLower(s["type"]) {
		case ShareVideo, ShareMusic, SharePhoto:
		default:
			out = append(out, Problem{"share." + name, "type",
				"must be video, music or photo, not " + strconv.Quote(s["type"])})
		}
		if s["path"] == "" {
			out = append(out, Problem{"share." + name, "path", "is empty"})
		}
	}
	return out
}
