package decrypt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"tivodesk/internal/tivocrypt/tivofile"
)

const testMAK = "1234567890"

// fileHeader returns a .TiVo header with one plain-text chunk, which is all
// the stream key needs.
func fileHeader(ts bool) []byte {
	chunk := []byte("<?xml version=\"1.0\"?><key>synthetic</key>")
	size := 12 + len(chunk)
	b := make([]byte, 16+size)
	copy(b, "TiVo")
	binary.BigEndian.PutUint16(b[4:], 4)
	if ts {
		binary.BigEndian.PutUint16(b[6:], 0x20)
	}
	binary.BigEndian.PutUint32(b[10:], uint32(len(b)))
	binary.BigEndian.PutUint16(b[14:], 1)
	binary.BigEndian.PutUint32(b[16:], uint32(size))
	binary.BigEndian.PutUint32(b[20:], uint32(len(chunk)))
	binary.BigEndian.PutUint16(b[24:], tivofile.ChunkXMLKey)
	copy(b[28:], chunk)
	return b
}

func testKeys() *keystreams {
	h, _ := tivofile.ReadHeader(bytes.NewReader(fileHeader(true)))
	return newKeystreams(testMAK, h.Chunks[0].Data)
}

// privateData returns 16 bytes that carry a block number, with the marker
// bits set as a TiVo sets them.
func privateData(block int) []byte {
	b := make([]byte, 16)
	b[0] = 0x80
	b[1] = 0x40 | byte(block>>18)&0x3f
	b[2] = byte(block >> 10)
	b[3] = 0x20 | byte(block>>2)&0xc0 | byte(block>>3)&0x1f
	b[4] = 0x10 | byte(block<<5)&0xe0
	b[0xd] = 0x02
	b[0xf] = 0x01
	return b
}

func TestBlockNumber(t *testing.T) {
	for _, want := range []int{0, 1, 7, 0x123456, 0xFFFFFF} {
		got, ok := blockNumber(privateData(want))
		if !ok || got != want {
			t.Errorf("block %#x came back as %#x, ok=%v", want, got, ok)
		}
	}
	if _, ok := blockNumber(make([]byte, 16)); ok {
		t.Error("sixteen zero bytes are not private data")
	}
}

// The headers that open a picture in MPEG-2 video.
var (
	pesHeader = []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 5, 0x21, 0, 1, 0, 1} // with a PTS
	seqHeader = []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x33, 0xFF, 0xFF, 0xE0, 0x18}
	gopHeader = []byte{0, 0, 1, 0xB8, 0x00, 0x08, 0x00, 0x40}
	picHeader = []byte{0, 0, 1, 0x00, 0x00, 0x0F, 0xFF, 0xF8} // an I picture
	slice     = []byte{0, 0, 1, 0x01, 0x12, 0x34}
)

func TestHeaderLengths(t *testing.T) {
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	filler := bytes.Repeat([]byte{0xAA}, 40)
	for _, c := range []struct {
		name string
		buf  []byte
		want []int
	}{
		{"video with every header", join(pesHeader, seqHeader, gopHeader, picHeader, slice, filler), []int{112, 96, 64, 61}},
		{"PES header then a slice", join(pesHeader, slice, filler), []int{112}},
		{"audio: PES header then data", join([]byte{0, 0, 1, 0xBD, 0, 0, 0x80, 0, 0}, []byte{0x0B, 0x77}, filler), []int{72}},
		{"H.264: four-byte start code", join(pesHeader, []byte{0, 0, 0, 1, 9, 0x10}, filler), []int{120}},
		{"no start code at all", filler, nil},
		{"stuffing after the PES header", join(pesHeader, []byte{0xFF, 0xFF}, slice, filler), []int{128}},
	} {
		if got := headerLengths(c.buf); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// A buffer that ends inside the headers must report at least its own
	// length, so that the caller waits for more.
	short := join(pesHeader, seqHeader[:6])
	total := 0
	for _, l := range headerLengths(short) {
		total += l
	}
	if total/8 < len(short) {
		t.Errorf("a cut-off header measured %d bytes of %d", total/8, len(short))
	}
}

// tsPkt builds one transport-stream packet.
func tsPkt(pid uint16, start, scrambled bool, counter int, payload []byte) []byte {
	p := bytes.Repeat([]byte{0xFF}, tsSize)
	p[0] = 0x47
	binary.BigEndian.PutUint16(p[1:], pid)
	if start {
		p[1] |= 0x40
	}
	p[3] = 0x10 | byte(counter&0x0F)
	if scrambled {
		p[3] |= 0xC0
	}
	if len(payload) > tsSize-4 {
		panic("payload too long")
	}
	copy(p[4:], payload)
	return p
}

const (
	videoPID = 0x31
	audioPID = 0x34
	keyPID   = 0x36
	pmtPID   = 0x64
)

// keyPacket lists the private data for the video and audio streams.
func keyPacket(videoBlock, audioBlock int) []byte {
	b := []byte("TiVo\x81\x03\x7d\x00\x00\x28")
	b = append(b, 0, videoPID, 0xE0, 0x10)
	b = append(b, privateData(videoBlock)...)
	b = append(b, 0, audioPID, 0xC0, 0x10)
	b = append(b, privateData(audioBlock)...)
	return tsPkt(keyPID, false, false, 0, b)
}

// plainStream builds an unencrypted transport stream with the scrambling
// bits set on the packets a TiVo would encrypt, and returns it with the
// positions of those packets.
//
// A picture header ends part-way through a byte, and the reference counts
// that last byte as encrypted. Once it is, the header no longer measures
// the same, so a stream with picture headers cannot be put through twice.
// withPictureHeaders leaves them out for tests that do that.
func plainStream(withPictureHeaders bool) (stream []byte, scrambled []int) {
	add := func(p []byte) {
		if p[3]&0xC0 != 0 {
			scrambled = append(scrambled, len(stream))
		}
		stream = append(stream, p...)
	}
	pat := []byte{0, 0x00, 0xB0, 0x0D, 0, 1, 0xC1, 0, 0, 0, 1, 0xE0 | pmtPID>>8, pmtPID & 0xFF, 0, 0, 0, 0}
	pmt := []byte{0, 0x02, 0xB0, 0x1C, 0, 1, 0xC1, 0, 0, 0xE0, videoPID, 0xF0, 0,
		0x02, 0xE0, videoPID, 0xF0, 0,
		0x81, 0xE0, audioPID, 0xF0, 0,
		0x97, 0xE0, keyPID, 0xF0, 0,
		0, 0, 0, 0}
	fill := func(n int, seed byte) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = seed + byte(i*7)
			if b[i] == 0 {
				b[i] = 0x55 // no accidental start codes
			}
		}
		return b
	}
	counter := 0
	picture := func(seed byte, withSequence bool) {
		head := append([]byte{}, pesHeader...)
		if withSequence {
			head = append(head, seqHeader...)
			head = append(head, gopHeader...)
		}
		if withPictureHeaders {
			head = append(head, picHeader...)
		}
		head = append(head, slice...)
		add(tsPkt(videoPID, true, true, counter, append(head, fill(tsSize-4-len(head), seed)...)))
		for i := 1; i <= 6; i++ {
			// As in a real recording, only some packets are encrypted.
			add(tsPkt(videoPID, false, i%3 != 0, counter+i, fill(tsSize-4, seed+byte(i))))
		}
		counter += 7
	}
	audio := func(seed byte) {
		head := []byte{0, 0, 1, 0xBD, 0, 0, 0x80, 0x80, 5, 0x21, 0, 1, 0, 1, 0x0B, 0x77}
		add(tsPkt(audioPID, true, true, int(seed), append(head, fill(tsSize-4-len(head), seed)...)))
		add(tsPkt(audioPID, false, true, int(seed)+1, fill(tsSize-4, seed+9)))
	}

	add(tsPkt(0, true, false, 0, pat))
	add(tsPkt(pmtPID, true, false, 0, pmt))
	add(keyPacket(5, 6))
	picture(1, true)
	audio(40)
	picture(80, false)
	add(tsPkt(0x1FFF, false, false, 0, nil)) // a null packet passes through
	add(keyPacket(7, 6))                     // the video's block number changes
	picture(120, false)
	audio(160)
	return stream, scrambled
}

func TestTransportStreamRoundTrip(t *testing.T) {
	header := fileHeader(true)

	// With every header present, the first video packet's headers stay in
	// the clear and what follows the picture header does not.
	{
		plain, scrambled := plainStream(true)
		var out bytes.Buffer
		if _, err := Decrypt(bytes.NewReader(append(header, plain...)), &out, testMAK); err != nil {
			t.Fatal(err)
		}
		enc, first := out.Bytes(), scrambled[0]
		clear := len(pesHeader) + len(seqHeader) + len(gopHeader) + 7 // 61 bits of picture header
		if !bytes.Equal(enc[first+4:first+4+clear], plain[first+4:first+4+clear]) {
			t.Error("the headers of the first picture were altered")
		}
		if bytes.Equal(enc[first+4+clear:first+tsSize], plain[first+4+clear:first+tsSize]) {
			t.Error("the data after the first picture's headers was not altered")
		}
	}

	plain, scrambled := plainStream(false)

	// Decrypting plain data encrypts it, since the cipher is an XOR. Do
	// that, mark the same packets as scrambled again, and decrypt: the
	// original must come back.
	var encrypted bytes.Buffer
	stats, err := Decrypt(bytes.NewReader(append(header, plain...)), &encrypted, testMAK)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.TS || stats.Packets != int64(len(plain)/tsSize) || stats.Decrypted != int64(len(scrambled)) || stats.Undecryptable != 0 {
		t.Errorf("stats: %+v, want %d packets, %d decrypted", stats, len(plain)/tsSize, len(scrambled))
	}
	enc := encrypted.Bytes()
	if len(enc) != len(plain) {
		t.Fatalf("output is %d bytes, input was %d", len(enc), len(plain))
	}

	want := append([]byte{}, plain...)
	for _, at := range scrambled {
		want[at+3] &^= 0xC0
	}
	changed := 0
	for i := 0; i < len(plain); i += tsSize {
		in, out := want[i:i+tsSize], enc[i:i+tsSize]
		wasScrambled := plain[i+3]&0xC0 != 0
		if !wasScrambled && !bytes.Equal(in, out) {
			t.Errorf("packet at %d was not encrypted but came out changed", i)
		}
		if wasScrambled {
			if bytes.Equal(in[4:], out[4:]) {
				t.Errorf("packet at %d was encrypted but came out unchanged", i)
			}
			changed++
		}
	}
	for _, at := range scrambled {
		enc[at+3] |= 0xC0
	}
	var back bytes.Buffer
	if _, err := Decrypt(bytes.NewReader(append(header, enc...)), &back, testMAK); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back.Bytes(), want) {
		t.Error("decrypting what was encrypted did not give back the original")
	}

	// Another key gives something else.
	back.Reset()
	Decrypt(bytes.NewReader(append(header, enc...)), &back, "0987654321")
	if bytes.Equal(back.Bytes(), want) {
		t.Error("a different key decrypted the stream")
	}
}

func TestTransportStreamDamage(t *testing.T) {
	plain, _ := plainStream(true)
	header := fileHeader(true)

	// Junk in the middle is skipped, and a partial packet at the end is
	// dropped; the packets either side survive.
	cut := 5 * tsSize
	damaged := append(append(append([]byte{}, plain[:cut]...), 1, 2, 3, 4, 5), plain[cut:]...)
	damaged = append(damaged, plain[:100]...)
	var out bytes.Buffer
	stats, err := Decrypt(bytes.NewReader(append(header, damaged...)), &out, testMAK)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SkippedBytes != 5 || out.Len() != len(plain) {
		t.Errorf("skipped %d bytes and wrote %d; want 5 and %d", stats.SkippedBytes, out.Len(), len(plain))
	}

	// Encrypted packets that arrive before any key cannot be decrypted.
	early := tsPkt(videoPID, false, true, 0, bytes.Repeat([]byte{0x55}, 100))
	out.Reset()
	stats, err = Decrypt(bytes.NewReader(append(append(header, plain[:2*tsSize]...), early...)), &out, testMAK)
	if err != nil || stats.Undecryptable != 1 || out.Len() != 2*tsSize {
		t.Errorf("a packet before its key: %+v, %v, %d bytes written", stats, err, out.Len())
	}
}

// programStream builds a program stream: a pack header, then video and
// audio packets, encrypted as a TiVo would if encrypt is set.
func programStream(keys *keystreams) (plain, encrypted []byte) {
	emit := func(b []byte) {
		plain = append(plain, b...)
		encrypted = append(encrypted, b...)
	}
	// pes writes a packet whose header carries private data.
	pes := func(code byte, block int, body []byte) {
		header := []byte{0x80, 0x01, 0x11, 0x80} // flags; extension present; 17 bytes follow; private data present
		header = append(header, privateData(block)...)
		length := len(header) + len(body)
		start := []byte{0, 0, 1, code, byte(length >> 8), byte(length)}

		plain = append(plain, start...)
		plain = append(plain, header...)
		plain = append(plain, body...)

		secret := append([]byte{}, body...)
		keys.prepare(code, block)
		keys.active.cipher.Skip(4)
		keys.active.cipher.XORKeyStream(secret, secret)
		header[0] |= 0x30 // scrambled
		encrypted = append(encrypted, start...)
		encrypted = append(encrypted, header...)
		encrypted = append(encrypted, secret...)
	}
	video := func(seed byte) []byte {
		var b []byte
		for i := 0; i < 12; i++ {
			b = append(b, 0, 0, 1, byte(i+1), seed, seed+1, seed+2, 0x99)
		}
		return b
	}

	emit([]byte{0, 0, 1, 0xBA, 0x44, 0, 4, 0, 4, 1, 1, 0x89, 0xC3, 0xF8}) // pack header
	pes(0xE0, 3, video(10))
	pes(0xC0, 4, bytes.Repeat([]byte{0x77, 0x11}, 50))
	emit([]byte{0, 0, 1, 0xBE, 0, 4, 0xFF, 0xFF, 0xFF, 0xFF}) // padding
	pes(0xE0, 3, video(20))                                   // same block: the keystream carries on
	pes(0xE0, 9, video(30))
	emit([]byte{0, 0, 1, 0xB9}) // end of stream
	return plain, encrypted
}

func TestProgramStream(t *testing.T) {
	plain, encrypted := programStream(testKeys())
	header := fileHeader(false)
	if bytes.Equal(plain, encrypted) {
		t.Fatal("the test stream was not encrypted")
	}

	var out bytes.Buffer
	stats, err := Decrypt(bytes.NewReader(append(header, encrypted...)), &out, testMAK)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), plain) {
		t.Errorf("decrypted stream differs from the original (%d bytes against %d)", out.Len(), len(plain))
	}
	if stats.TS || stats.Decrypted != 4 || stats.Extension() != ".mpg" {
		t.Errorf("stats: %+v", stats)
	}

	out.Reset()
	if _, err := Decrypt(bytes.NewReader(append(header, encrypted...)), &out, "0987654321"); !errors.Is(err, ErrWrongMAK) {
		t.Errorf("wrong key: got %v", err)
	}

	// A file that stops in the middle of a packet ends quietly.
	out.Reset()
	if _, err := Decrypt(bytes.NewReader(append(header, encrypted[:len(encrypted)-60]...)), &out, testMAK); err != nil {
		t.Errorf("a cut-off file: %v", err)
	}
}

func TestNotARecording(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("short"), bytes.Repeat([]byte("not a tivo file "), 10)} {
		if _, err := Decrypt(bytes.NewReader(in), &bytes.Buffer{}, testMAK); !errors.Is(err, tivofile.ErrNotTiVo) {
			t.Errorf("%d bytes of something else: got %v", len(in), err)
		}
	}
}

// TestRealRecordings decrypts every .tivo file in the folder named by
// TIVODESK_FIXTURES with the key in TIVO_MAK, and checks that what comes
// out is a sound stream. It is skipped unless both are set. Use
// "tivodesk-cli decrypt -check" to have ffmpeg judge the result as well.
func TestRealRecordings(t *testing.T) {
	dir, mak := os.Getenv("TIVODESK_FIXTURES"), os.Getenv("TIVO_MAK")
	if dir == "" || mak == "" {
		t.Skip("set TIVODESK_FIXTURES and TIVO_MAK to run this against real recordings")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.tivo"))
	if len(files) == 0 {
		t.Fatalf("no .tivo files in %s", dir)
	}
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			in, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			var out bytes.Buffer
			stats, err := Decrypt(in, &out, mak)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%+v", stats)
			if stats.Decrypted == 0 || stats.Undecryptable != 0 || stats.SkippedBytes != 0 {
				t.Errorf("unexpected stats: %+v", stats)
			}
			if !stats.TS {
				return
			}
			// Every packet that starts a PES packet must begin with a
			// start code, and none may still be marked as scrambled.
			b := out.Bytes()
			var starts, bad int
			for i := 0; i+tsSize <= len(b); i += tsSize {
				p := b[i : i+tsSize]
				if p[0] != 0x47 || p[3]&0xC0 != 0 {
					t.Fatalf("packet at %d is out of step or still scrambled", i)
				}
				pid := binary.BigEndian.Uint16(p[1:]) & 0x1FFF
				if p[1]&0x40 == 0 || pid < 0x20 || p[3]&0x10 == 0 {
					continue
				}
				off := 4
				if p[3]&0x20 != 0 {
					off = 5 + int(p[4])
				}
				if off+4 > tsSize || (p[off] == 0 && p[off+1] != 0) {
					continue // a table, not a PES packet
				}
				starts++
				if !bytes.HasPrefix(p[off:], []byte{0, 0, 1}) {
					bad++
				}
			}
			if starts == 0 || bad > 0 {
				t.Errorf("%d of %d PES packets do not begin with a start code", bad, starts)
			}
		})
	}
}
