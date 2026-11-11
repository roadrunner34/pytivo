package decrypt

// MPEG start codes that may sit, unencrypted, between a PES header and the
// encrypted picture data.
const (
	pictureStart   = 0x100
	sliceStartMin  = 0x101
	sliceStartMax  = 0x1AF
	userDataStart  = 0x1B2
	sequenceHeader = 0x1B3
	extensionStart = 0x1B5
	sequenceEnd    = 0x1B7
	groupStart     = 0x1B8
	ancillaryData  = 0x1F9
)

// bits reads a buffer bit by bit and counts how many bits each header
// takes.
type bits struct {
	buf []byte
	pos int  // in bits
	eof bool // the position, or a look ahead, has passed the end
	hdr int  // bits consumed since the count was last cleared
}

func (b *bits) aligned() bool { return b.pos%8 == 0 }

func (b *bits) advance(n int) {
	b.pos += n
	b.hdr += n
	if b.pos >= len(b.buf)*8 {
		b.eof = true
	}
}

// next returns the next n bits (at most 32) without consuming them. Bits
// past the end of the buffer read as zero.
func (b *bits) next(n int) uint32 {
	var v uint32
	for i := b.pos; i < b.pos+n; i++ {
		v <<= 1
		if at := i / 8; at < len(b.buf) {
			v |= uint32(b.buf[at]>>(7-i%8)) & 1
		} else {
			b.eof = true
		}
	}
	return v
}

// nextStartCode moves to the next byte boundary and then over zero bytes
// that pad the way to a start code.
func (b *bits) nextStartCode() {
	for !b.aligned() {
		b.advance(1)
	}
	for !b.eof {
		v := b.next(24)
		if v != 0 {
			break // a start code, or something else
		}
		b.advance(8)
	}
}

func (b *bits) flag() bool {
	v := b.next(1)
	b.advance(1)
	return v == 1
}

func (b *bits) sequenceHeader() {
	b.advance(44) // start code, horizontal size
	b.advance(12) // vertical size
	b.advance(38) // aspect, frame rate, bit rate, marker, vbv size, constrained flag
	if b.flag() { // an intra quantiser matrix follows
		b.advance(8 * 64)
	}
	if b.flag() { // a non-intra quantiser matrix follows
		b.advance(8 * 64)
	}
	b.nextStartCode()
}

func (b *bits) userData() {
	b.advance(32)
	for !b.eof && b.next(24) != 1 {
		b.advance(8)
	}
	b.nextStartCode()
}

func (b *bits) extension() {
	b.advance(32)
	switch b.next(4) {
	case 1: // sequence extension
		b.advance(12)
		b.advance(1) // progressive sequence
		b.advance(35)
		b.nextStartCode()
	case 2: // sequence display extension
		b.advance(7)
		if b.flag() { // a colour description follows
			b.advance(24)
		}
		b.advance(30)
		b.nextStartCode()
	case 8: // picture coding extension
		b.advance(22)
		b.advance(2) // picture structure
		b.advance(1) // top field first
		b.advance(5)
		b.advance(1) // repeat first field
		b.advance(2)
		if b.flag() { // a composite display description follows
			b.advance(20)
		}
		b.nextStartCode()
	}
}

func (b *bits) groupOfPictures() {
	b.advance(59)
	b.nextStartCode()
}

// pictureHeader does not finish on a byte boundary, and the reference does
// not move on to one. Whatever follows a picture header is therefore never
// recognised as a start code, and counts as encrypted.
func (b *bits) pictureHeader() {
	b.advance(42) // start code, temporal reference
	kind := b.next(3)
	b.advance(3)
	b.advance(16) // vbv delay
	if kind == 2 || kind == 3 {
		b.advance(4)
	}
	if kind == 3 {
		b.advance(4)
	}
	for !b.eof && b.next(1) == 1 {
		b.advance(9)
	}
}

func (b *bits) pesHeader() {
	b.advance(24)
	id := b.next(8)
	b.advance(8)
	b.advance(16) // packet length
	if id == 0xBD || (id >= 0xC0 && id <= 0xEF) {
		b.pesExtension()
	}
	b.nextStartCode()
}

// pesExtension measures the optional part of a PES header from its flags,
// as the reference does, rather than from its stated length.
func (b *bits) pesExtension() {
	b.advance(8)
	ptsDts := b.next(2)
	b.advance(2)
	escr := b.flag()
	esRate := b.flag()
	b.advance(1) // trick mode
	copyInfo := b.flag()
	crc := b.flag()
	ext := b.flag()
	b.advance(8) // header data length

	switch ptsDts {
	case 2:
		b.advance(40)
	case 3:
		b.advance(80)
	}
	if escr {
		b.advance(48)
	}
	if esRate {
		b.advance(24)
	}
	if copyInfo {
		b.advance(8)
	}
	if crc {
		b.advance(16)
	}
	var private, pack, counter, std, ext2 bool
	if ext {
		private = b.flag()
		pack = b.flag()
		counter = b.flag()
		std = b.flag()
		b.advance(3)
		ext2 = b.flag()
	}
	if private {
		b.advance(8 * 16)
	}
	if pack {
		b.advance(8)
	}
	if counter {
		b.advance(16)
	}
	if std {
		b.advance(16)
	}
	if ext2 {
		b.advance(1)
		n := int(b.next(7))
		b.advance(7)
		b.advance(8)
		b.advance(8 * n)
	}
	for !b.eof && b.next(8) == 0xFF { // stuffing
		b.advance(8)
	}
}

// headerLengths walks the headers at the start of a PES packet and returns
// the length of each in bits. Everything after them is encrypted. The walk
// stops at the first slice, at anything that is not a start code, and at
// the end of the buffer; in the last case the caller needs more data to be
// sure where the headers end.
func headerLengths(buf []byte) []int {
	var lengths []int
	b := &bits{buf: buf}
	for !b.eof && len(buf) > b.pos/8 {
		if b.next(24) != 1 {
			break
		}
		code := b.next(32)
		b.hdr = 0
		switch {
		case code == extensionStart:
			b.extension()
		case code == groupStart:
			b.groupOfPictures()
		case code == userDataStart:
			b.userData()
		case code == pictureStart:
			b.pictureHeader()
		case code == sequenceHeader:
			b.sequenceHeader()
		case code == sequenceEnd, code == ancillaryData:
			b.advance(32)
		case code == 0x1BD, code >= 0x1C0 && code <= 0x1EF:
			b.pesHeader()
		default:
			// A slice, where the picture data starts, or a code the
			// reference does not know. Either way the headers end here.
			return lengths
		}
		if b.hdr != 0 {
			lengths = append(lengths, b.hdr)
		}
	}
	return lengths
}
