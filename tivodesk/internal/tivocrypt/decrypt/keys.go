// Package decrypt turns a .TiVo recording into a plain MPEG stream. It is a
// port of tivodecode-ng (Copyright 2006-2018 Jeremy Drake et al., BSD
// licence; see LICENSE-tivodecode in this folder), and follows that
// program's behaviour closely, quirks included, because the position of
// every byte in the keystream depends on it.
package decrypt

import (
	"crypto/sha1"

	"tivodesk/internal/tivocrypt/turing"
)

// frame is the keystream for one stream at one block number.
type frame struct {
	block  int
	cipher *turing.Cipher
}

// keystreams holds the keystream of every stream in a recording. Each
// stream has its own, which carries on from packet to packet until the
// stream's block number changes.
type keystreams struct {
	base    [20]byte // 16 bytes of key, then the stream ID and block number
	streams map[byte]*frame
	active  *frame
}

// newKeystreams derives the recording's key from the Media Access Key and
// the plain-text chunk of the file header.
func newKeystreams(mak string, chunk []byte) *keystreams {
	h := sha1.New()
	h.Write([]byte(mak))
	h.Write(chunk)
	k := &keystreams{streams: map[byte]*frame{}}
	copy(k.base[:], h.Sum(nil))
	return k
}

// prepare makes the keystream for a stream and block number the active
// one, starting it afresh if the block number has changed.
func (k *keystreams) prepare(streamID byte, block int) error {
	f := k.streams[streamID]
	if f == nil || f.block != block {
		k.base[16] = streamID
		k.base[17] = byte(block >> 16)
		k.base[18] = byte(block >> 8)
		k.base[19] = byte(block)
		key := sha1.Sum(k.base[:17])
		iv := sha1.Sum(k.base[:])
		c, err := turing.New(key[:], iv[:])
		if err != nil {
			return err
		}
		f = &frame{block: block, cipher: c}
		k.streams[streamID] = f
	}
	k.active = f
	return nil
}

// blockNumber unpacks the block number from the 16 bytes of private data
// that accompany encrypted video or audio. It reports false if the marker
// bits that should all be set are not, which means the bytes are not
// private data at all.
func blockNumber(b []byte) (block int, ok bool) {
	block = int(b[1]&0x3f)<<18 |
		int(b[2])<<10 |
		int(b[3]&0xc0)<<2 |
		int(b[3]&0x1f)<<3 |
		int(b[4]&0xe0)>>5
	ok = b[0]&0x80 != 0 && b[1]&0x40 != 0 && b[3]&0x20 != 0 &&
		b[4]&0x10 != 0 && b[0xd]&0x02 != 0 && b[0xf]&0x01 != 0
	return block, ok
}
