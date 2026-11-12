package decrypt

import (
	"bufio"
	"errors"
	"io"
)

// How a program-stream packet with a given start code is laid out.
const (
	packSpecial = iota // no length; passed through byte by byte
	packSimple         // a length, then data
	packComplex        // a length and a PES header, then data
)

func packKind(code byte) int {
	switch {
	case code <= 0xBA:
		return packSpecial
	case code == 0xBD, code >= 0xC0 && code <= 0xEF, code >= 0xF3 && code <= 0xF7, code == 0xF9:
		return packComplex
	}
	return packSimple
}

// ErrWrongMAK means the Media Access Key does not open this recording.
var ErrWrongMAK = errors.New("the Media Access Key does not match this recording")

type psDecoder struct {
	keys  *keystreams
	r     *bufio.Reader
	w     *bufio.Writer
	stats *Stats
	// verified is set once decrypted video has been seen to make sense.
	verified bool
	packet   []byte
}

// run copies the stream, looking for start codes (00 00 01 xx) and
// decrypting the packets that follow the ones marked as scrambled.
func (d *psDecoder) run() error {
	marker := uint32(0xFFFFFFFF)
	first := true
	var b byte
	for {
		if marker&0xFFFFFF00 == 0x100 {
			done, err := d.frame(b)
			if err != nil {
				return err
			}
			if done {
				marker = 0xFFFFFFFF
			} else if err := d.w.WriteByte(b); err != nil {
				return err
			}
		} else if !first {
			if err := d.w.WriteByte(b); err != nil {
				return err
			}
		}
		marker <<= 8
		var err error
		if b, err = d.r.ReadByte(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		marker |= uint32(b)
		first = false
	}
}

// frameStart reads the private data in a scrambled packet's PES header and
// readies the keystream for the packet. It follows the reference's walk
// through the header's optional fields, which is not how the standard lays
// them out but is what lines up with real recordings.
func (d *psDecoder) frameStart(head []byte, code byte) error {
	off, ext := 6, 5
	for {
		if ext >= len(head) {
			return nil
		}
		if head[ext]&0x20 != 0 {
			off += 4
		}
		if head[ext]&0x80 != 0 && off+16 <= len(head) {
			block, _ := blockNumber(head[off:])
			if err := d.keys.prepare(code, block); err != nil {
				return err
			}
			// Four bytes of keystream cover a value in the private data
			// that nothing uses, but they must be consumed.
			d.keys.active.cipher.Skip(4)
		}
		if head[ext]&0x10 != 0 {
			off += 2
		}
		if head[ext]&0x01 == 0 {
			return nil
		}
		ext = off
		off++
	}
}

// frame handles what follows a start code. It reports true if it consumed
// and wrote a whole packet, and false if the start code needs no handling.
func (d *psDecoder) frame(code byte) (bool, error) {
	kind := packKind(code)
	if kind == packSpecial {
		return false, nil
	}
	// A short read here means the file ends inside a packet; the reference
	// stops quietly, and so do we.
	read := func(b []byte) (bool, error) {
		if _, err := io.ReadFull(d.r, b); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}

	var head [32]byte
	have, headerLen, scrambled := 2, 0, false
	if kind == packComplex {
		have = 5
	}
	if ok, err := read(head[:have]); !ok {
		return false, err
	}
	if kind == packComplex {
		scrambled = head[2]&0x30 == 0x30
		headerLen = 5 + int(head[4])
		if scrambled && head[3]&0x01 != 0 { // the header has an extension
			if headerLen > len(head) {
				return false, errors.New("decrypt: a packet header is longer than any real one")
			}
			if ok, err := read(head[have:headerLen]); !ok {
				return false, err
			}
			have = headerLen
			if err := d.frameStart(head[:have], code); err != nil {
				return false, err
			}
		}
	}

	size := int(head[0])<<8 | int(head[1]) + 2
	if size < have {
		size = have
	}
	if cap(d.packet) < size {
		d.packet = make([]byte, 65538)
	}
	packet := d.packet[:size]
	copy(packet, head[:have])
	if ok, err := read(packet[have:]); !ok {
		return false, err
	}

	body := packet[min(2, size):]
	if headerLen > 0 {
		body = packet[min(headerLen, size):]
	}
	if scrambled {
		if d.keys.active == nil {
			return false, errors.New("decrypt: encrypted data came before any key")
		}
		d.keys.active.cipher.XORKeyStream(body, body)
		packet[2] &^= 0x30
		d.stats.Decrypted++
		if !d.verified && code == 0xE0 {
			// Real video is full of slice start codes; noise is not.
			slices := 0
			for i := 0; i+4 < len(body); i++ {
				if body[i] == 0 && body[i+1] == 0 && body[i+2] == 1 && body[i+3] >= 0x01 && body[i+3] <= 0xAF {
					slices++
				}
			}
			if slices <= 8 {
				return false, ErrWrongMAK
			}
			d.verified = true
		}
	} else if code == 0xBC && len(packet) > 2 {
		packet[2] &^= 0x20 // the reference clears this bit; the reason is unknown
	}
	d.stats.Packets++
	if err := d.w.WriteByte(code); err != nil {
		return false, err
	}
	_, err := d.w.Write(packet)
	return true, err
}
