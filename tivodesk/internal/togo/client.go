// Package togo talks to a TiVo's built-in web server: it lists the
// recordings in Now Playing and, later, downloads them.
package togo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"time"
)

var (
	// ErrUnauthorized means the TiVo rejected the Media Access Key.
	ErrUnauthorized = errors.New("the TiVo rejected the Media Access Key")
	// ErrBusy means the TiVo kept answering "server busy" until we gave up.
	ErrBusy = errors.New("the TiVo is too busy to answer; try again in a minute")
)

// LegacyTLS returns the TLS settings a TiVo needs.
//
// TiVos only offer RSA key exchange, which Go no longer enables by default,
// so the cipher suites are listed explicitly; that is what makes the
// handshake work (confirmed by cmd/tlsprobe against a Roamio and an Edge).
// Certificate checks are off because every TiVo has a self-signed
// certificate; access is protected by the Media Access Key instead.
func LegacyTLS() *tls.Config {
	var suites []uint16
	for _, s := range tls.CipherSuites() {
		suites = append(suites, s.ID)
	}
	for _, s := range tls.InsecureCipherSuites() {
		suites = append(suites, s.ID)
	}
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS10,
		CipherSuites:       suites,
	}
}

// backoff is how long to wait after the nth "server busy" answer in a row.
// pyTivo waits 5 seconds and retries for ever; this doubles from one second
// up to a cap, and gives up after len(backoff) tries.
var backoff = []time.Duration{1, 2, 4, 8, 15, 15}

// Client talks to one TiVo.
type Client struct {
	base string // such as "https://192.168.1.5:443"
	http *http.Client
	// dl fetches recordings. It signs in separately, because a TiVo serves
	// recordings from a different port with its own challenge.
	dl *http.Client
	// stall is how long a download may go without data before giving up.
	stall time.Duration
	// sleep waits between retries; tests replace it.
	sleep func(context.Context, time.Duration) error
}

// NewClient returns a client for the TiVo at address:port, signing in with
// the Media Access Key.
func NewClient(address string, port int, mak string) *Client {
	if port == 0 {
		port = 443
	}
	base := "https://" + net.JoinHostPort(address, strconv.Itoa(port))

	// A TiVo expects a session cookie called sid, with any value, before it
	// will hand over a recording.
	jar, _ := cookiejar.New(nil)
	if u, err := url.Parse(base); err == nil {
		jar.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "ADEADDA7EDEBAC1E"}})
	}

	transport := &http.Transport{
		TLSClientConfig:       LegacyTLS(),
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	return &Client{
		base: base,
		http: &http.Client{
			Jar:       jar,
			Transport: &digestTransport{base: transport, user: "tivo", pass: mak},
		},
		dl: &http.Client{
			Jar:       jar,
			Transport: &digestTransport{base: transport, user: "tivo", pass: mak},
		},
		stall: 2 * time.Minute,
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}
}

// get fetches a path on the TiVo and returns the response body, retrying
// while the TiVo says it is busy.
func (c *Client) get(ctx context.Context, pathAndQuery string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+pathAndQuery, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("could not reach the TiVo: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusServiceUnavailable:
			if attempt >= len(backoff) {
				return nil, ErrBusy
			}
			if err := c.sleep(ctx, backoff[attempt]*time.Second); err != nil {
				return nil, err
			}
		case resp.StatusCode == http.StatusUnauthorized:
			return nil, ErrUnauthorized
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("the TiVo answered %s", resp.Status)
		case readErr != nil:
			return nil, fmt.Errorf("the TiVo's answer was cut short: %w", readErr)
		default:
			return body, nil
		}
	}
}
