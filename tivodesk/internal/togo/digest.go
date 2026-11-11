package togo

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// digestTransport adds HTTP Digest authentication (RFC 2617, MD5) to
// requests. A TiVo challenges the first request with a 401; the transport
// answers it and then signs later requests up front, which halves the
// number of round trips when paging through Now Playing.
//
// It only handles requests without a body, which is all a TiVo needs.
type digestTransport struct {
	base       http.RoundTripper
	user, pass string

	mu        sync.Mutex
	challenge map[string]string // the server's last challenge, or nil
	count     int               // requests signed with that challenge
}

func (t *digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if auth := t.authorization(req); auth != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", auth)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	// Challenged: either this was the first request, or the nonce expired.
	// Answer once. A second 401 means the password is wrong.
	challenge := parseChallenge(resp.Header.Get("WWW-Authenticate"))
	if challenge["nonce"] == "" {
		return resp, nil
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	t.mu.Lock()
	t.challenge, t.count = challenge, 0
	t.mu.Unlock()

	retry := req.Clone(req.Context())
	retry.Header.Set("Authorization", t.authorization(req))
	return t.base.RoundTrip(retry)
}

// authorization builds the Authorization header for a request, or returns
// an empty string if the server has not issued a challenge yet.
func (t *digestTransport) authorization(req *http.Request) string {
	t.mu.Lock()
	c := t.challenge
	t.count++
	nc := fmt.Sprintf("%08x", t.count)
	t.mu.Unlock()
	if c == nil {
		return ""
	}

	uri := req.URL.RequestURI()
	ha1 := md5hex(t.user + ":" + c["realm"] + ":" + t.pass)
	ha2 := md5hex(req.Method + ":" + uri)

	var b strings.Builder
	fmt.Fprintf(&b, `Digest username="%s", realm="%s", nonce="%s", uri="%s"`, t.user, c["realm"], c["nonce"], uri)
	if strings.Contains(c["qop"], "auth") {
		var raw [8]byte
		rand.Read(raw[:])
		cnonce := hex.EncodeToString(raw[:])
		response := md5hex(strings.Join([]string{ha1, c["nonce"], nc, cnonce, "auth", ha2}, ":"))
		fmt.Fprintf(&b, `, qop=auth, nc=%s, cnonce="%s", response="%s"`, nc, cnonce, response)
	} else {
		fmt.Fprintf(&b, `, response="%s"`, md5hex(ha1+":"+c["nonce"]+":"+ha2))
	}
	if c["opaque"] != "" {
		fmt.Fprintf(&b, `, opaque="%s"`, c["opaque"])
	}
	return b.String()
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// parseChallenge reads the parameters of a "WWW-Authenticate: Digest ..."
// header. It returns an empty map for any other scheme.
func parseChallenge(header string) map[string]string {
	out := map[string]string{}
	rest, found := strings.CutPrefix(header, "Digest ")
	if !found {
		return out
	}
	for {
		rest = strings.TrimLeft(rest, ", ")
		key, after, ok := strings.Cut(rest, "=")
		if !ok {
			return out
		}
		var val string
		if strings.HasPrefix(after, `"`) {
			end := strings.Index(after[1:], `"`)
			if end < 0 {
				return out
			}
			val, rest = after[1:1+end], after[2+end:]
		} else {
			val, rest, _ = strings.Cut(after, ",")
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
}
