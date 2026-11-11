package decrypt

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

const (
	tsSize = 188
	// tivoStreamType marks the stream that carries the keys.
	tivoStreamType = 0x97
	// maxPending bounds how many packets wait while the end of a run of
	// headers is looked for. The reference's buffer holds ten.
	maxPending = 10
)

// tsPacket is one transport-stream packet.
type tsPacket struct {
	data    [tsSize]byte
	payload int // offset of the payload
	// clear is how many payload bytes are headers, which are not encrypted.
	clear int
}

func (p *tsPacket) pid() uint16     { return binary.BigEndian.Uint16(p.data[1:]) & 0x1FFF }
func (p *tsPacket) unitStart() bool { return p.data[1]&0x40 != 0 }
func (p *tsPacket) scrambled() bool { return p.data[3]&0xC0 != 0 }

// tsStream is what is known about one PID.
type tsStream struct {
	// private is true for the stream that carries the keys.
	private  bool
	streamID byte
	key      [16]byte // private data for this stream, from the key stream
	pending  []*tsPacket
}

type tsDecoder struct {
	keys    *keystreams
	w       *bufio.Writer
	streams map[uint16]*tsStream
	pmtPID  uint16
	havePMT bool
	stats   *Stats
	free    []*tsPacket
}

// readPacket reads the next packet. If the stream has lost its place, it
// skips ahead to where three packets in a row start with the sync byte.
func readPacket(r *bufio.Reader, p *tsPacket, stats *Stats) error {
	for {
		head, err := r.Peek(tsSize)
		if err != nil {
			if len(head) == 0 || err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
				return io.EOF // a partial packet at the end is dropped
			}
			return err
		}
		if head[0] == 0x47 {
			copy(p.data[:], head)
			r.Discard(tsSize)
			return nil
		}
		// Lost sync: find the next place where three packets line up.
		for {
			stats.SkippedBytes++
			if _, err := r.Discard(1); err != nil {
				return io.EOF
			}
			three, err := r.Peek(tsSize*2 + 1)
			if err != nil {
				return io.EOF
			}
			if three[0] == 0x47 && three[tsSize] == 0x47 && three[tsSize*2] == 0x47 {
				break
			}
		}
	}
}

func (d *tsDecoder) run(r *bufio.Reader) error {
	for {
		var p *tsPacket
		if n := len(d.free); n > 0 {
			p, d.free = d.free[n-1], d.free[:n-1]
			p.clear = 0
		} else {
			p = &tsPacket{}
		}
		if err := readPacket(r, p, d.stats); err != nil {
			if err == io.EOF {
				return d.finish()
			}
			return err
		}
		d.stats.Packets++

		p.payload = 4
		if p.data[3]&0x20 != 0 { // an adaptation field comes first
			p.payload = min(5+int(p.data[4]), tsSize)
		}

		pid := p.pid()
		switch {
		case pid == 0:
			d.programAssociation(p)
		case d.havePMT && pid == d.pmtPID:
			d.programMap(p)
		default:
			if s := d.streams[pid]; s != nil && s.private {
				d.tivoKeys(p)
			}
		}

		s := d.streams[pid]
		if s == nil {
			// Not part of the programme: pass it through untouched.
			if _, err := d.w.Write(p.data[:]); err != nil {
				return err
			}
			d.free = append(d.free, p)
			continue
		}
		if err := d.add(s, p); err != nil {
			return err
		}
	}
}

// finish writes out packets still waiting when the input ends.
func (d *tsDecoder) finish() error {
	for _, s := range d.streams {
		if err := d.flush(s); err != nil {
			return err
		}
	}
	return nil
}

func (d *tsDecoder) stream(pid uint16) *tsStream {
	s := d.streams[pid]
	if s == nil {
		s = &tsStream{}
		d.streams[pid] = s
	}
	return s
}

// section returns the payload of a table packet, after the pointer field.
func section(p *tsPacket) []byte {
	b := p.data[p.payload:]
	if p.unitStart() && len(b) > 0 {
		b = b[1:]
	}
	return b
}

// programAssociation reads the PAT, which says where the programme map is.
func (d *tsDecoder) programAssociation(p *tsPacket) {
	d.stream(0)
	b := section(p)
	if len(b) < 8 || b[0] != 0x00 {
		return
	}
	length := int(binary.BigEndian.Uint16(b[1:]) & 0x03FF)
	entries := b[8:]
	if n := length - 5 - 4; n < len(entries) { // less the header and the CRC
		entries = entries[:max(n, 0)]
	}
	for ; len(entries) >= 4; entries = entries[4:] {
		d.pmtPID = binary.BigEndian.Uint16(entries[2:]) & 0x1FFF
		d.havePMT = true
		d.stream(d.pmtPID)
	}
}

// programMap reads the PMT, which lists the programme's streams.
func (d *tsDecoder) programMap(p *tsPacket) {
	b := section(p)
	if len(b) < 12 {
		return
	}
	length := int(binary.BigEndian.Uint16(b[1:]) & 0x0FFF)
	info := int(binary.BigEndian.Uint16(b[10:]) & 0x0FFF)
	entries := b[12:]
	if n := length - 9 - 4; n < len(entries) {
		entries = entries[:max(n, 0)]
	}
	if info > len(entries) {
		return
	}
	entries = entries[info:]
	for len(entries) >= 5 {
		kind := entries[0]
		pid := binary.BigEndian.Uint16(entries[1:]) & 0x1FFF
		esInfo := int(binary.BigEndian.Uint16(entries[3:]) & 0x0FFF)
		if d.streams[pid] == nil {
			// The reference treats a type it does not know as the key
			// stream too; a packet without the "TiVo" mark is ignored.
			d.stream(pid).private = kind == tivoStreamType || !knownStreamType[kind]
		}
		if 5+esInfo > len(entries) {
			return
		}
		entries = entries[5+esInfo:]
	}
}

// Stream types the reference recognises as video, audio or other data.
var knownStreamType = func() (t [256]bool) {
	for _, k := range []byte{
		0x01, 0x02, 0x10, 0x1b, 0x80, 0xea, // video
		0x03, 0x04, 0x11, 0x0f, 0x81, 0x8a, // audio
		0x08, 0x0a, 0x0b, 0x0c, 0x0d, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19,
		0x05, 0x06, 0x07, 0x09, 0x0e, 0x12, 0x13, 0x1a, 0x7f,
	} {
		t[k] = true
	}
	return t
}()

// tivoKeys reads a packet of the key stream:
//
//	4 bytes  "TiVo"
//	5 bytes  not understood
//	1 byte   length of what follows
//	then, for each stream: PID (2), stream ID (1), reserved (1), and the
//	16 bytes of private data that hold its block number
func (d *tsDecoder) tivoKeys(p *tsPacket) {
	b := p.data[p.payload:]
	if len(b) < 10 || string(b[:4]) != "TiVo" {
		return
	}
	n := int(b[9])
	b = b[10:]
	if n < len(b) {
		b = b[:n]
	}
	for ; len(b) >= 20; b = b[20:] {
		s := d.streams[binary.BigEndian.Uint16(b)]
		if s == nil {
			return
		}
		s.streamID = b[2]
		copy(s.key[:], b[4:20])
	}
}

// add takes the next packet of a stream. A packet that starts a PES packet
// is held, with those after it if need be, until it is known where the
// headers end; only what follows them is encrypted.
func (d *tsDecoder) add(s *tsStream, p *tsPacket) error {
	if !p.unitStart() && len(s.pending) == 0 {
		s.pending = append(s.pending, p)
		return d.flush(s)
	}
	s.pending = append(s.pending, p)

	var buf []byte
	for _, q := range s.pending {
		buf = append(buf, q.data[q.payload:]...)
	}
	lengths := headerLengths(buf)
	total := 0
	for _, l := range lengths {
		total += l
	}
	total /= 8
	if total >= len(buf) && len(s.pending) < maxPending {
		return nil // the headers may run on into the next packet
	}

	// Mark, packet by packet, how much of each is header. This follows the
	// reference exactly, including what it does when a header straddles
	// two packets.
	for _, q := range s.pending {
		if total <= 0 {
			break
		}
		for len(lengths) > 0 {
			l := lengths[0] / 8
			lengths = lengths[1:]
			if l+q.payload+q.clear < tsSize {
				q.clear += l
				total -= l
				continue
			}
			if room := tsSize - q.payload - q.clear; room < 4 {
				// The packet ends inside a start code; the rest of the
				// header is in the next packet.
				lengths = append([]int{(l - room) * 8}, lengths...)
			}
			break
		}
	}
	return d.flush(s)
}

// flush decrypts and writes the packets a stream is holding.
func (d *tsDecoder) flush(s *tsStream) error {
	for _, p := range s.pending {
		write := true
		if p.scrambled() {
			p.data[3] &^= 0xC0
			block, ok := blockNumber(s.key[:])
			if !ok {
				// No key has arrived for this stream yet. The reference
				// drops such packets, and so do we: they cannot be played.
				d.stats.Undecryptable++
				write = false
			} else {
				if err := d.keys.prepare(s.streamID, block); err != nil {
					return err
				}
				from := min(p.payload+p.clear, tsSize)
				d.keys.active.cipher.XORKeyStream(p.data[from:], p.data[from:])
				d.stats.Decrypted++
			}
		}
		if write {
			if _, err := d.w.Write(p.data[:]); err != nil {
				return err
			}
		}
		d.free = append(d.free, p)
	}
	s.pending = s.pending[:0]
	return nil
}
