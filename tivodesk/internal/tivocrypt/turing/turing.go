// Package turing implements Qualcomm's Turing stream cipher, as used by
// TiVo to encrypt .TiVo file metadata and streams.
//
// Turing was designed by Greg Rose, Copyright 2002 QUALCOMM Incorporated.
// This implementation follows pyTivo's turing.py (William McBrine, 2013),
// which is in turn based on Rose's TuringFast.c. Encryption by QUALCOMM.
package turing

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

const (
	maxKey    = 32 // bytes
	maxKeyIV  = 48 // bytes of key plus IV
	lfsrLen   = 17 // words
	blockSize = 20 // keystream bytes produced per round
)

var (
	ErrKeyLength = errors.New("turing: key must be 4 to 32 bytes, in multiples of 4")
	ErrIVLength  = errors.New("turing: IV must be a multiple of 4 bytes, and key plus IV at most 48 bytes")
)

// Cipher is a keyed Turing keystream generator. It is not safe for
// concurrent use.
type Cipher struct {
	sbox [4][256]uint32 // keyed S-boxes, one per byte position
	mkey []uint32       // mixed key words

	// lfsr is a ring: logical word i lives at lfsr[(pos+i) % lfsrLen].
	lfsr [lfsrLen]uint32
	pos  int

	block [blockSize]byte // current keystream block
	used  int             // bytes of block already consumed
}

// New returns a cipher keyed with key and loaded with iv.
func New(key, iv []byte) (*Cipher, error) {
	c := &Cipher{}
	if err := c.SetKey(key); err != nil {
		return nil, err
	}
	if err := c.LoadIV(iv); err != nil {
		return nil, err
	}
	return c, nil
}

// fixedStrans is a reversible transformation of a word through the
// unkeyed S-boxes.
func fixedStrans(w uint32) uint32 {
	b := uint32(sbox[w>>24])
	w = ((w ^ qbox[b]) & 0x00ffffff) | b<<24
	b = uint32(sbox[(w>>16)&0xff])
	w = ((w ^ bits.RotateLeft32(qbox[b], 8)) & 0xff00ffff) | b<<16
	b = uint32(sbox[(w>>8)&0xff])
	w = ((w ^ bits.RotateLeft32(qbox[b], 16)) & 0xffff00ff) | b<<8
	b = uint32(sbox[w&0xff])
	w = ((w ^ bits.RotateLeft32(qbox[b], 24)) & 0xffffff00) | b
	return w
}

// mix applies the pseudo-Hadamard transform in place: every word but the
// last gains the sum of all words, and the last becomes that sum.
func mix(w []uint32) {
	var total uint32
	for _, x := range w {
		total += x
	}
	last := len(w) - 1
	for i := 0; i < last; i++ {
		w[i] += total
	}
	w[last] = total
}

// strans pushes a word through the keyed S-boxes.
func (c *Cipher) strans(w uint32) uint32 {
	return c.sbox[0][w>>24] ^ c.sbox[1][(w>>16)&0xff] ^
		c.sbox[2][(w>>8)&0xff] ^ c.sbox[3][w&0xff]
}

// SetKey keys the cipher and builds the keyed S-box tables. LoadIV must be
// called before generating keystream.
func (c *Cipher) SetKey(key []byte) error {
	if len(key) == 0 || len(key)&3 != 0 || len(key) > maxKey {
		return ErrKeyLength
	}
	mkey := make([]uint32, len(key)/4)
	for i := range mkey {
		mkey[i] = fixedStrans(binary.BigEndian.Uint32(key[4*i:]))
	}
	mix(mkey)

	for l := 0; l < 4; l++ {
		sh1 := 8 * l
		sh2 := 24 - sh1
		mask := ^(uint32(0xff) << sh2)
		for j := 0; j < 256; j++ {
			var w uint32
			k := uint32(j)
			for i, kw := range mkey {
				k = uint32(sbox[((kw>>sh2)&0xff)^k])
				w ^= bits.RotateLeft32(qbox[k], i+sh1)
			}
			c.sbox[l][j] = (w & mask) | k<<sh2
		}
	}
	c.mkey = mkey
	return nil
}

// LoadIV fills the shift register from the IV and the key, and resets the
// keystream position to zero.
func (c *Cipher) LoadIV(iv []byte) error {
	if c.mkey == nil {
		return ErrKeyLength
	}
	klen := len(c.mkey)
	if len(iv)&3 != 0 || len(iv)+4*klen > maxKeyIV {
		return ErrIVLength
	}
	n := 0
	for i := 0; i < len(iv); i += 4 {
		c.lfsr[n] = fixedStrans(binary.BigEndian.Uint32(iv[i:]))
		n++
	}
	n += copy(c.lfsr[n:], c.mkey)
	c.lfsr[n] = uint32(klen)<<4 | uint32(len(iv))>>2 | 0x01020300
	n++
	for j := 0; n < lfsrLen; j++ {
		c.lfsr[n] = c.strans(c.lfsr[j] + c.lfsr[n-1])
		n++
	}
	mix(c.lfsr[:])
	c.pos = 0
	c.used = blockSize
	return nil
}

// at returns logical LFSR word i.
func (c *Cipher) at(i int) uint32 {
	i += c.pos
	if i >= lfsrLen {
		i -= lfsrLen
	}
	return c.lfsr[i]
}

// step advances the LFSR by one word.
func (c *Cipher) step() {
	old := c.lfsr[c.pos]
	c.lfsr[c.pos] = c.at(15) ^ c.at(4) ^ old<<8 ^ multab[old>>24]
	c.pos++
	if c.pos == lfsrLen {
		c.pos = 0
	}
}

// round produces the next 20 bytes of keystream into c.block.
func (c *Cipher) round() {
	c.step()
	t := [5]uint32{c.at(16), c.at(13), c.at(6), c.at(1), c.at(0)}
	mix(t[:])
	t[0] = c.strans(t[0])
	t[1] = c.strans(bits.RotateLeft32(t[1], 8))
	t[2] = c.strans(bits.RotateLeft32(t[2], 16))
	t[3] = c.strans(bits.RotateLeft32(t[3], 24))
	t[4] = c.strans(t[4])
	mix(t[:])
	c.step()
	c.step()
	c.step()
	t[0] += c.at(14)
	t[1] += c.at(12)
	t[2] += c.at(8)
	t[3] += c.at(1)
	t[4] += c.at(0)
	c.step()
	for i, w := range t {
		binary.BigEndian.PutUint32(c.block[4*i:], w)
	}
	c.used = 0
}

// Skip discards the next n bytes of keystream.
func (c *Cipher) Skip(n int) {
	if rest := blockSize - c.used; n <= rest {
		c.used += n
		return
	} else {
		n -= rest
		c.used = blockSize
	}
	// Whole blocks only need the register advanced: one round is 5 steps.
	for ; n >= blockSize; n -= blockSize {
		for i := 0; i < 5; i++ {
			c.step()
		}
	}
	if n > 0 {
		c.round()
		c.used = n
	}
}

// XORKeyStream XORs src with the keystream into dst, which may be the same
// slice as src. dst must be at least as long as src.
func (c *Cipher) XORKeyStream(dst, src []byte) {
	dst = dst[:len(src)]
	for len(src) > 0 {
		if c.used == blockSize {
			c.round()
		}
		n := copy(dst, src[:min(len(src), blockSize-c.used)])
		ks := c.block[c.used : c.used+n]
		for i := range ks {
			dst[i] = src[i] ^ ks[i]
		}
		c.used += n
		dst, src = dst[n:], src[n:]
	}
}
