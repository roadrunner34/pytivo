package togo

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	// ErrNotRecording means the TiVo answered a download request with
	// something other than a .TiVo file.
	ErrNotRecording = errors.New("the TiVo sent something that is not a recording")
	// ErrStalled means the TiVo stopped sending data part-way through.
	ErrStalled = errors.New("the TiVo stopped sending data")
)

const (
	tsPacket = 188
	// chunkSize is how much is read at a time. It is a whole number of
	// transport-stream packets, so each chunk starts on a packet boundary.
	chunkSize = tsPacket * 1500
	// tsFormat asks the TiVo for a transport stream instead of the default
	// program stream.
	tsFormat = "&Format=video/x-tivo-mpeg-ts"
)

// DownloadOptions say how to fetch a recording.
type DownloadOptions struct {
	// TS asks for a transport stream. Newer TiVos need it for H.264
	// recordings; older ones only offer program streams.
	TS bool
	// Progress, if set, is called after each chunk with the bytes written
	// so far and the number of damaged transport-stream packets seen.
	Progress func(written int64, syncLosses int)
}

// DownloadResult describes a finished download.
type DownloadResult struct {
	Bytes int64
	// SyncLosses counts transport-stream packets that did not start with
	// the sync byte. More than zero means the file is damaged in places.
	SyncLosses int
}

// open requests one URL, waiting and retrying while the TiVo is busy. A
// TiVo sends one recording at a time and answers "busy" for a while after
// the previous one ends.
func (c *Client) open(ctx context.Context, target string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.dl.Do(req)
		if err != nil {
			return nil, fmt.Errorf("could not reach the TiVo: %w", err)
		}
		switch resp.StatusCode {
		case http.StatusOK:
			return resp, nil
		case http.StatusServiceUnavailable:
			resp.Body.Close()
			if attempt >= len(backoff) {
				return nil, ErrBusy
			}
			if err := c.sleep(ctx, backoff[attempt]*time.Second); err != nil {
				return nil, err
			}
		case http.StatusUnauthorized:
			resp.Body.Close()
			return nil, ErrUnauthorized
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("the TiVo answered %s", resp.Status)
		}
	}
}

// openDownload requests a recording. The link a TiVo gives is plain HTTP on
// port 80; if that does not work, the same path is tried over HTTPS.
func (c *Client) openDownload(ctx context.Context, contentURL string, ts bool) (*http.Response, error) {
	u, err := url.Parse(contentURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the recording has no usable download link (%q)", contentURL)
	}
	if ts {
		u.RawQuery += tsFormat
	}
	targets := []string{u.String()}
	if alt := c.base + u.RequestURI(); alt != targets[0] {
		targets = append(targets, alt)
	}
	var firstErr error
	for _, target := range targets {
		resp, err := c.open(ctx, target)
		if err == nil {
			return resp, nil
		}
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrBusy) || ctx.Err() != nil {
			return nil, err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// watched resets a timer on every read, so a silent connection is noticed.
type watched struct {
	r     io.Reader
	timer *time.Timer
	every time.Duration
}

func (w *watched) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 {
		w.timer.Reset(w.every)
	}
	return n, err
}

// fill reads until buf is full or the reader ends. Unlike io.ReadFull it
// returns the reader's own error, so a clean end (io.EOF) can be told from
// a broken connection.
func fill(r io.Reader, buf []byte) (n int, err error) {
	for n < len(buf) && err == nil {
		var m int
		m, err = r.Read(buf[n:])
		n += m
	}
	return n, err
}

// Download fetches a recording, still encrypted, and writes it to w.
// contentURL is the recording's ContentURL.
func (c *Client) Download(ctx context.Context, contentURL string, w io.Writer, opts DownloadOptions) (DownloadResult, error) {
	var res DownloadResult
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	resp, err := c.openDownload(ctx, contentURL, opts.TS)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()

	timer := time.AfterFunc(c.stall, func() { cancel(ErrStalled) })
	defer timer.Stop()
	body := &watched{r: resp.Body, timer: timer, every: c.stall}

	// explain turns a read or write failure into the reason behind it.
	explain := func(err error) error {
		switch cause := context.Cause(ctx); {
		case errors.Is(cause, ErrStalled):
			return ErrStalled
		case cause != nil:
			return cause
		}
		return fmt.Errorf("the download was cut short: %w", err)
	}

	// The header first: it says where the video starts, and reading exactly
	// that far keeps the rest aligned on packet boundaries.
	head := make([]byte, 16)
	if _, err := io.ReadFull(body, head); err != nil {
		if context.Cause(ctx) != nil {
			return res, explain(err)
		}
		return res, ErrNotRecording
	}
	videoStart := int64(binary.BigEndian.Uint32(head[10:14]))
	if string(head[:4]) != "TiVo" || videoStart < 16 || videoStart > 16<<20 {
		return res, ErrNotRecording
	}
	if _, err := w.Write(head); err != nil {
		return res, err
	}
	n, err := io.CopyN(w, body, videoStart-16)
	res.Bytes = 16 + n
	if err != nil {
		return res, explain(err)
	}

	buf := make([]byte, chunkSize)
	for {
		n, readErr := fill(body, buf)
		if n > 0 {
			if opts.TS {
				for i := 0; i < n; i += tsPacket {
					if buf[i] != 0x47 {
						res.SyncLosses++
					}
				}
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return res, err
			}
			res.Bytes += int64(n)
			if opts.Progress != nil {
				opts.Progress(res.Bytes, res.SyncLosses)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return res, explain(readErr)
		}
	}
	if res.Bytes == videoStart {
		return res, errors.New("the TiVo sent a recording with no video in it")
	}
	return res, nil
}
