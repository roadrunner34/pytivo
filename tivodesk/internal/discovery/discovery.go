// Package discovery finds TiVo boxes on the local network over mDNS.
//
// pyTivo browses once at startup and waits up to 30 seconds for an answer.
// This package browses continuously in the background instead, so the app
// starts at once and notices boxes that appear or leave later.
package discovery

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"tivodesk/internal/tivo"
)

// The service a TiVo that shares its recordings announces.
const (
	service = "_tivo-videos._tcp"
	domain  = "local."
)

const (
	// cycle is how long one browse runs. The mDNS library reports each box
	// once per browse and never reports one leaving, so browsing is done in
	// repeating cycles and a box's silence is how its departure shows.
	cycle = 30 * time.Second
	// goneAfter is how long a box may stay silent before it counts as
	// offline. It spans several cycles so one lost packet does not matter.
	goneAfter = 100 * time.Second
	// retryDelay is the pause after a browse fails outright, such as when
	// the network is down at startup.
	retryDelay = 5 * time.Second
)

// unescape undoes DNS-SD escaping in an instance name: "\ " is a space and
// "\032" is the character with that decimal code.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		if i+3 < len(s) {
			if n, err := strconv.Atoi(s[i+1 : i+4]); err == nil && n < 256 {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		i++
		b.WriteByte(s[i])
	}
	return b.String()
}

// boxFrom converts an mDNS announcement to a Box. ok is false when the
// announcement lacks what is needed to talk to the box.
func boxFrom(e *zeroconf.ServiceEntry) (b tivo.Box, ok bool) {
	text := map[string]string{}
	for _, kv := range e.Text {
		k, v, _ := strings.Cut(kv, "=")
		text[k] = v
	}
	// Only boxes with an upper-case TSN record serve their recordings.
	// TiVo Minis announce a lower-case "tsn" and have none to offer.
	tsn := text["TSN"]
	if tsn == "" {
		return b, false
	}
	for _, ip := range e.AddrIPv4 {
		if v4 := ip.To4(); v4 != nil && !v4.IsLinkLocalUnicast() {
			return tivo.Box{
				TSN:       tsn,
				Name:      unescape(e.Instance),
				Address:   v4.String(),
				Port:      e.Port,
				Platform:  text["platform"],
				SWVersion: text["swversion"],
			}, true
		}
	}
	return b, false
}

// usableInterfaces returns the network interfaces worth browsing: up,
// multicast-capable, not loopback, and holding an IPv4 address. Leaving out
// the rest (VPN tunnels and the like) avoids errors from interfaces that
// cannot do multicast at all.
func usableInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifc := range all {
		const want = net.FlagUp | net.FlagMulticast
		if ifc.Flags&want != want || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, ifc)
				break
			}
		}
	}
	return out
}

// Browse watches the network for TiVos and keeps reg up to date. It blocks
// until ctx is cancelled.
func Browse(ctx context.Context, reg *tivo.Registry, log *slog.Logger) {
	lastSeen := map[string]time.Time{} // by TSN

	for ctx.Err() == nil {
		entries := make(chan *zeroconf.ServiceEntry, 16)
		collected := make(chan struct{})
		go func() {
			defer close(collected)
			for e := range entries {
				b, ok := boxFrom(e)
				if !ok {
					continue
				}
				lastSeen[b.TSN] = time.Now()
				if reg.Seen(b) {
					log.Info("TiVo found", "name", b.Name, "model", b.Model(), "address", b.Address, "tsn", b.TSN)
				}
			}
		}()

		// IPv4 only: a TiVo's web server is reached over IPv4.
		opts := []zeroconf.ClientOption{zeroconf.SelectIPTraffic(zeroconf.IPv4)}
		if ifaces := usableInterfaces(); len(ifaces) > 0 {
			opts = append(opts, zeroconf.SelectIfaces(ifaces))
		}
		cycleCtx, cancel := context.WithTimeout(ctx, cycle)
		err := zeroconf.Browse(cycleCtx, service, domain, entries, opts...)
		if err == nil {
			<-cycleCtx.Done() // Browse returns once started; let the cycle run
		}
		cancel()
		<-collected // the library closes entries when the browse ends

		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn("looking for TiVos failed; trying again shortly", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(retryDelay):
			}
			continue
		}

		for tsn, seen := range lastSeen {
			if time.Since(seen) > goneAfter {
				if b, ok := reg.Get(tsn); ok && b.Online {
					log.Info("TiVo left the network", "name", b.Name)
				}
				reg.Gone(tsn)
				delete(lastSeen, tsn)
			}
		}
	}
}
