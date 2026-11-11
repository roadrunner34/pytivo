package discovery

import (
	"net"
	"testing"

	"github.com/libp2p/zeroconf/v2"

	"tivodesk/internal/tivo"
)

func entry(instance string, text []string, ips ...string) *zeroconf.ServiceEntry {
	e := &zeroconf.ServiceEntry{Port: 443, Text: text}
	e.Instance = instance
	for _, ip := range ips {
		e.AddrIPv4 = append(e.AddrIPv4, net.ParseIP(ip))
	}
	return e
}

func TestBoxFrom(t *testing.T) {
	text := []string{"protocol=https", "path=/TiVoConnect?Command=QueryContainer&Container=%2FNowPlaying",
		"swversion=21.11.1", "platform=tcd/Keystone", "TSN=D6E0000000000AA"}

	// 169.254.x.x is link-local and not reachable in practice.
	got, ok := boxFrom(entry(`Family\ Room`, text, "169.254.10.10", "192.168.68.78"))
	want := tivo.Box{TSN: "D6E0000000000AA", Name: "Family Room", Address: "192.168.68.78",
		Port: 443, Platform: "tcd/Keystone", SWVersion: "21.11.1"}
	if !ok || got != want {
		t.Errorf("boxFrom = %+v, %v\nwant %+v", got, ok, want)
	}

	// A TiVo Mini announces a lower-case tsn and has no recordings.
	if _, ok := boxFrom(entry("Mini", []string{"tsn=A90000000000000"}, "192.168.68.80")); ok {
		t.Error("a box without an upper-case TSN should be ignored")
	}
	if _, ok := boxFrom(entry("NoAddress", text)); ok {
		t.Error("a box with no usable IPv4 address should be ignored")
	}
	if _, ok := boxFrom(entry("LinkLocalOnly", text, "169.254.1.1")); ok {
		t.Error("a box with only a link-local address should be ignored")
	}
}

func TestUnescape(t *testing.T) {
	for in, want := range map[string]string{
		`Family\ Room`:     "Family Room",
		`Paella\032Tivo`:   "Paella Tivo",
		`Dr\.\ Who`:        "Dr. Who",
		`Plain`:            "Plain",
		`Back\\slash`:      `Back\slash`,
		`Trailing\`:        `Trailing\`,
		`Caf\195\169`:      "Café",
		`Room\ 12\ \(up\)`: "Room 12 (up)",
	} {
		if got := unescape(in); got != want {
			t.Errorf("unescape(%q) = %q, want %q", in, got, want)
		}
	}
}
