// Package tivofile reads the header of a .TiVo file and decrypts the
// metadata chunks it carries. It follows pyTivo's metadata._tdcat_py.
package tivofile

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"tivodesk/internal/tivocrypt/turing"
)

const (
	headerSize      = 16
	chunkHeaderSize = 12

	// Chunk IDs seen in a .TiVo header.
	ChunkDetails = 2 // TvBus XML describing the recording
	ChunkXMLKey  = 3 // key material for the encrypted chunks

	// maxHeader bounds how much a hostile or corrupt file can make us read.
	maxHeader = 16 << 20
)

var ErrNotTiVo = errors.New("tivofile: not a .TiVo file")

// Chunk is one metadata chunk from the file header.
type Chunk struct {
	ID        uint16
	Encrypted bool
	Data      []byte
	// Start is the chunk data's byte offset in the file. Encrypted chunks
	// use it as their position in the Turing keystream.
	Start int
}

// Header is the parsed front matter of a .TiVo file.
type Header struct {
	// MPEGOffset is where the MPEG stream begins.
	MPEGOffset int
	Chunks     []Chunk
}

// ReadHeader parses the header and all metadata chunks from r, leaving r
// positioned at the start of the MPEG stream.
func ReadHeader(r io.Reader) (*Header, error) {
	var fixed [headerSize]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return nil, fmt.Errorf("tivofile: reading header: %w", err)
	}
	if string(fixed[:4]) != "TiVo" {
		return nil, ErrNotTiVo
	}
	offset := int(binary.BigEndian.Uint32(fixed[10:]))
	count := int(binary.BigEndian.Uint16(fixed[14:]))
	if offset < headerSize || offset > maxHeader {
		return nil, fmt.Errorf("tivofile: implausible MPEG offset %d", offset)
	}

	raw := make([]byte, offset-headerSize)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, fmt.Errorf("tivofile: reading chunks: %w", err)
	}

	h := &Header{MPEGOffset: offset, Chunks: make([]Chunk, 0, count)}
	pos := 0
	for i := 0; i < count; i++ {
		if pos+chunkHeaderSize > len(raw) {
			return nil, fmt.Errorf("tivofile: chunk %d header runs past the end", i)
		}
		chunkSize := int(binary.BigEndian.Uint32(raw[pos:]))
		dataSize := int(binary.BigEndian.Uint32(raw[pos+4:]))
		id := binary.BigEndian.Uint16(raw[pos+8:])
		enc := binary.BigEndian.Uint16(raw[pos+10:])
		dataStart := pos + chunkHeaderSize
		if chunkSize < chunkHeaderSize || dataSize < 0 || dataStart+dataSize > len(raw) {
			return nil, fmt.Errorf("tivofile: chunk %d has a bad size", i)
		}
		h.Chunks = append(h.Chunks, Chunk{
			ID:        id,
			Encrypted: enc != 0,
			Data:      raw[dataStart : dataStart+dataSize],
			Start:     dataStart + headerSize,
		})
		pos += chunkSize
	}
	return h, nil
}

// Chunk returns the first chunk with the given ID.
func (h *Header) Chunk(id uint16) (Chunk, bool) {
	for _, c := range h.Chunks {
		if c.ID == id {
			return c, true
		}
	}
	return Chunk{}, false
}

// metadataCipher derives the Turing cipher that protects metadata chunks
// from the Media Access Key and the file's XML key chunk.
func metadataCipher(mak string, xmlKey []byte) (*turing.Cipher, error) {
	sum := md5.Sum([]byte("tivo:TiVo DVR:" + mak))
	hexmak := hex.EncodeToString(sum[:])

	base := sha1.Sum(append([]byte(hexmak), xmlKey...))
	var key [20]byte // 16 bytes of digest, then 4 zero bytes
	copy(key[:], base[:16])

	turKey := sha1.Sum(key[:17])
	turIV := sha1.Sum(key[:])
	return turing.New(turKey[:], turIV[:])
}

// Details returns the recording's TvBus XML, decrypting it with the Media
// Access Key if the file stores it encrypted.
//
// A wrong MAK is not detectable here: the result is simply not XML.
func (h *Header) Details(mak string) ([]byte, error) {
	details, ok := h.Chunk(ChunkDetails)
	if !ok {
		return nil, errors.New("tivofile: no details chunk")
	}
	if !details.Encrypted {
		return details.Data, nil
	}
	xmlKey, ok := h.Chunk(ChunkXMLKey)
	if !ok {
		return nil, errors.New("tivofile: details are encrypted but there is no key chunk")
	}
	c, err := metadataCipher(mak, xmlKey.Data)
	if err != nil {
		return nil, err
	}
	c.Skip(details.Start)
	out := make([]byte, len(details.Data))
	c.XORKeyStream(out, details.Data)
	return out, nil
}
