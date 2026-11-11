// Command tlsprobe checks whether this Go toolchain can still talk to a
// TiVo's HTTPS server, and with which settings.
//
//	tlsprobe 192.168.1.50 192.168.1.51
//
// For each host it tries a handshake with several TLS configurations and
// reports what was negotiated. If the TIVO_MAK environment variable is set
// it also fetches one Now Playing entry with digest auth, which is the real
// end-to-end test. The MAK is read from the environment so it stays out of
// shell history, and it is never printed.
package main

import (
	"crypto/ecdsa"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const timeout = 10 * time.Second

// allSuites is every TLS 1.0-1.2 suite Go implements, including the ones it
// marks insecure (RSA key exchange, 3DES, RC4, CBC-SHA256).
func allSuites() []uint16 {
	var ids []uint16
	for _, s := range tls.CipherSuites() {
		ids = append(ids, s.ID)
	}
	for _, s := range tls.InsecureCipherSuites() {
		ids = append(ids, s.ID)
	}
	return ids
}

type variant struct {
	name string
	cfg  *tls.Config
}

func variants() []variant {
	legacy := func(min, max uint16) *tls.Config {
		return &tls.Config{
			InsecureSkipVerify: true, // TiVos use self-signed certificates
			MinVersion:         min,
			MaxVersion:         max,
			CipherSuites:       allSuites(),
		}
	}
	return []variant{
		{"go defaults", &tls.Config{InsecureSkipVerify: true}},
		{"legacy, TLS 1.0-1.3", legacy(tls.VersionTLS10, tls.VersionTLS13)},
		{"legacy, TLS 1.2 only", legacy(tls.VersionTLS12, tls.VersionTLS12)},
		{"legacy, TLS 1.1 only", legacy(tls.VersionTLS11, tls.VersionTLS11)},
		{"legacy, TLS 1.0 only", legacy(tls.VersionTLS10, tls.VersionTLS10)},
	}
}

func certSummary(st tls.ConnectionState) string {
	if len(st.PeerCertificates) == 0 {
		return "no certificate"
	}
	c := st.PeerCertificates[0]
	key := "unknown key"
	switch k := c.PublicKey.(type) {
	case *rsa.PublicKey:
		key = fmt.Sprintf("RSA-%d", k.N.BitLen())
	case *ecdsa.PublicKey:
		key = fmt.Sprintf("ECDSA-%d", k.Curve.Params().BitSize)
	}
	return fmt.Sprintf("%s, signed %s, CN=%q, expires %s",
		key, c.SignatureAlgorithm, c.Subject.CommonName, c.NotAfter.Format("2006-01-02"))
}

// handshake tries one configuration and returns a one-line result.
func handshake(addr string, cfg *tls.Config) (ok bool, line, cert string) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", addr, cfg)
	if err != nil {
		return false, "FAIL  " + err.Error(), ""
	}
	defer conn.Close()
	st := conn.ConnectionState()
	return true, fmt.Sprintf("ok    %s, %s", tls.VersionName(st.Version),
		tls.CipherSuiteName(st.CipherSuite)), certSummary(st)
}

// digestParams parses a WWW-Authenticate: Digest challenge.
func digestParams(header string) map[string]string {
	out := map[string]string{}
	rest, found := strings.CutPrefix(header, "Digest ")
	if !found {
		return out
	}
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, ", ")
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		var val string
		if strings.HasPrefix(after, `"`) {
			end := strings.Index(after[1:], `"`)
			if end < 0 {
				break
			}
			val, rest = after[1:1+end], after[2+end:]
		} else {
			val, rest, _ = strings.Cut(after, ",")
		}
		out[strings.TrimSpace(key)] = val
	}
	return out
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// digestHeader builds an RFC 2617 Authorization header (MD5, qop=auth).
func digestHeader(p map[string]string, user, pass, method, uri string) string {
	ha1 := md5hex(user + ":" + p["realm"] + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	var nonceBytes [8]byte
	rand.Read(nonceBytes[:])
	cnonce := hex.EncodeToString(nonceBytes[:])
	const nc = "00000001"

	var response string
	h := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s"`,
		user, p["realm"], p["nonce"], uri)
	if strings.Contains(p["qop"], "auth") {
		response = md5hex(strings.Join([]string{ha1, p["nonce"], nc, cnonce, "auth", ha2}, ":"))
		h += fmt.Sprintf(`, qop=auth, nc=%s, cnonce="%s"`, nc, cnonce)
	} else {
		response = md5hex(ha1 + ":" + p["nonce"] + ":" + ha2)
	}
	h += fmt.Sprintf(`, response="%s"`, response)
	if p["opaque"] != "" {
		h += fmt.Sprintf(`, opaque="%s"`, p["opaque"])
	}
	return h
}

// nowPlaying fetches one Now Playing entry over HTTPS with digest auth.
func nowPlaying(host, mak string, cfg *tls.Config) string {
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: false},
	}
	const uri = "/TiVoConnect?Command=QueryContainer&Container=%2FNowPlaying&Recurse=Yes&ItemCount=1"
	url := "https://" + host + uri

	get := func(auth string) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return client.Do(req)
	}

	resp, err := get("")
	if err != nil {
		return "FAIL  " + err.Error()
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return fmt.Sprintf("FAIL  expected a 401 challenge, got %s", resp.Status)
	}
	params := digestParams(resp.Header.Get("WWW-Authenticate"))
	if params["nonce"] == "" {
		return fmt.Sprintf("FAIL  no digest challenge (WWW-Authenticate: %q)",
			resp.Header.Get("WWW-Authenticate"))
	}

	resp, err = get(digestHeader(params, "tivo", mak, http.MethodGet, uri))
	if err != nil {
		return "FAIL  " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "FAIL  401 after auth: the MAK was rejected"
	case resp.StatusCode != http.StatusOK:
		return "FAIL  " + resp.Status
	case !strings.Contains(string(body), "<TiVoContainer"):
		return fmt.Sprintf("FAIL  200 but not a TiVoContainer (%d bytes)", len(body))
	}
	total := "?"
	if _, after, ok := strings.Cut(string(body), "<TotalItems>"); ok {
		total, _, _ = strings.Cut(after, "<")
	}
	return fmt.Sprintf("ok    200, %d bytes, TotalItems=%s", len(body), total)
}

func main() {
	hosts := os.Args[1:]
	if len(hosts) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tlsprobe HOST [HOST...]   (set TIVO_MAK to also test Now Playing)")
		os.Exit(2)
	}
	mak := os.Getenv("TIVO_MAK")
	fmt.Printf("tlsprobe, %s, GODEBUG=%q\n", runtime.Version(), os.Getenv("GODEBUG"))

	failed := false
	for _, host := range hosts {
		fmt.Printf("\n== %s\n", host)
		addr := net.JoinHostPort(host, "443")
		var working *tls.Config
		cert := ""
		for _, v := range variants() {
			ok, line, c := handshake(addr, v.cfg)
			fmt.Printf("  %-22s %s\n", v.name, line)
			if ok && working == nil && v.name != "go defaults" {
				working = v.cfg
			}
			if c != "" {
				cert = c
			}
		}
		if cert != "" {
			fmt.Printf("  %-22s %s\n", "certificate", cert)
		}
		switch {
		case working == nil:
			failed = true
			fmt.Printf("  %-22s skipped: no legacy configuration connected\n", "now playing")
		case mak == "":
			fmt.Printf("  %-22s skipped: TIVO_MAK is not set\n", "now playing")
		default:
			line := nowPlaying(host, mak, working)
			failed = failed || strings.HasPrefix(line, "FAIL")
			fmt.Printf("  %-22s %s\n", "now playing", line)
		}
	}
	if failed {
		os.Exit(1)
	}
}
