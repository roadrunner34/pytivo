package decrypt

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"tivodesk/internal/tivocrypt/tivofile"
)

// Stats describe a finished decryption.
type Stats struct {
	// TS is true if the recording is a transport stream, false for a
	// program stream.
	TS bool
	// Packets is the number of packets read, and Decrypted how many of
	// them were encrypted.
	Packets, Decrypted int64
	// Undecryptable counts encrypted packets dropped because no key for
	// their stream had arrived yet.
	Undecryptable int64
	// SkippedBytes counts bytes dropped to get back in step after damage.
	SkippedBytes int64
}

// Extension returns the usual file extension for the decrypted stream.
func (s Stats) Extension() string {
	if s.TS {
		return ".ts"
	}
	return ".mpg"
}

// bufferSize is used for both reading and writing.
const bufferSize = 1 << 20

// Decrypt reads a .TiVo recording from r and writes the plain MPEG stream
// to w. It works as a stream: nothing is read ahead beyond a buffer, so it
// can run while the recording is still downloading.
//
// A wrong Media Access Key is reported as ErrWrongMAK, before anything is
// written when the file lets that be checked.
func Decrypt(r io.Reader, w io.Writer, mak string) (Stats, error) {
	var stats Stats
	br := bufio.NewReaderSize(r, bufferSize)

	// The fixed header says which kind of stream follows.
	fixed, err := br.Peek(16)
	if err != nil {
		return stats, tivofile.ErrNotTiVo
	}
	stats.TS = binary.BigEndian.Uint16(fixed[6:])&0x20 != 0

	h, err := tivofile.ReadHeader(br)
	if err != nil {
		return stats, err
	}
	// The recording's key comes from the header's plain-text chunk; if
	// there are several, the reference uses the last.
	var keys *keystreams
	for _, c := range h.Chunks {
		if !c.Encrypted {
			keys = newKeystreams(mak, c.Data)
		}
	}
	if keys == nil {
		return stats, errors.New("decrypt: the recording has no key chunk")
	}
	// The details chunk is encrypted with a key that also comes from the
	// Media Access Key. If it does not decrypt to XML, the key is wrong.
	if details, err := h.Details(mak); err == nil && !bytes.HasPrefix(bytes.TrimSpace(details), []byte("<")) {
		return stats, ErrWrongMAK
	}

	bw := bufio.NewWriterSize(w, bufferSize)
	if stats.TS {
		d := &tsDecoder{keys: keys, w: bw, streams: map[uint16]*tsStream{}, stats: &stats}
		err = d.run(br)
	} else {
		d := &psDecoder{keys: keys, r: br, w: bw, stats: &stats}
		err = d.run()
	}
	if flushErr := bw.Flush(); err == nil {
		err = flushErr
	}
	if err != nil && !errors.Is(err, ErrWrongMAK) {
		err = fmt.Errorf("decrypt: %w", err)
	}
	return stats, err
}
