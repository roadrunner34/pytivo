package turing

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"testing"
)

// vector is one case recorded from pyTivo's turing.py by tools/gen_turing.py.
type vector struct {
	Name   string            `json:"name"`
	Key    string            `json:"key"`
	IV     string            `json:"iv"`
	Stream string            `json:"stream"`
	Skips  map[string]string `json:"skips"`
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v []vector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func keystream(t *testing.T, key, iv []byte, skip, n int) []byte {
	t.Helper()
	c, err := New(key, iv)
	if err != nil {
		t.Fatal(err)
	}
	c.Skip(skip)
	out := make([]byte, n)
	c.XORKeyStream(out, out)
	return out
}

func TestKeystreamMatchesPython(t *testing.T) {
	for _, v := range loadVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			key, iv, want := unhex(t, v.Key), unhex(t, v.IV), unhex(t, v.Stream)
			if got := keystream(t, key, iv, 0, len(want)); !bytes.Equal(got, want) {
				t.Fatalf("keystream differs from Python\n got %x\nwant %x", got[:40], want[:40])
			}
			for skipStr, wantHex := range v.Skips {
				skip, _ := strconv.Atoi(skipStr)
				want := unhex(t, wantHex)
				if got := keystream(t, key, iv, skip, len(want)); !bytes.Equal(got, want) {
					t.Errorf("skip %d differs from Python", skip)
				}
			}
		})
	}
}

// Reading the stream in uneven pieces must give the same bytes as one read.
func TestChunkedReadsAreSeamless(t *testing.T) {
	v := loadVectors(t)[0]
	key, iv, want := unhex(t, v.Key), unhex(t, v.IV), unhex(t, v.Stream)
	c, err := New(key, iv)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	for off, size := 0, 1; off < len(got); size = size*3%23 + 1 {
		end := min(off+size, len(got))
		c.XORKeyStream(got[off:end], got[off:end])
		off = end
	}
	if !bytes.Equal(got, want) {
		t.Fatal("chunked keystream differs from a single read")
	}
}

func TestCryptRoundTrip(t *testing.T) {
	key, iv := sha1.Sum([]byte("key")), sha1.Sum([]byte("iv"))
	data := bytes.Repeat([]byte("TiVo recording payload "), 40)

	enc := make([]byte, len(data))
	c, _ := New(key[:], iv[:])
	c.Skip(100)
	c.XORKeyStream(enc, data)
	if bytes.Equal(enc, data) {
		t.Fatal("encryption left the data unchanged")
	}

	c, _ = New(key[:], iv[:])
	c.Skip(100)
	c.XORKeyStream(enc, enc)
	if !bytes.Equal(enc, data) {
		t.Fatal("round trip did not restore the data")
	}
}

func TestBadLengths(t *testing.T) {
	if _, err := New([]byte("abc"), nil); !errors.Is(err, ErrKeyLength) {
		t.Errorf("3-byte key: got %v, want ErrKeyLength", err)
	}
	if _, err := New(nil, nil); !errors.Is(err, ErrKeyLength) {
		t.Errorf("empty key: got %v, want ErrKeyLength", err)
	}
	if _, err := New(make([]byte, 36), nil); !errors.Is(err, ErrKeyLength) {
		t.Errorf("36-byte key: got %v, want ErrKeyLength", err)
	}
	if _, err := New(make([]byte, 32), make([]byte, 20)); !errors.Is(err, ErrIVLength) {
		t.Errorf("key+IV of 52 bytes: got %v, want ErrIVLength", err)
	}
	if _, err := New(make([]byte, 16), make([]byte, 5)); !errors.Is(err, ErrIVLength) {
		t.Errorf("5-byte IV: got %v, want ErrIVLength", err)
	}
}

func BenchmarkXORKeyStream(b *testing.B) {
	key, iv := sha1.Sum([]byte("key")), sha1.Sum([]byte("iv"))
	c, _ := New(key[:], iv[:])
	buf := make([]byte, 1<<20)
	b.SetBytes(int64(len(buf)))
	for b.Loop() {
		c.XORKeyStream(buf, buf)
	}
}

// TiVo re-keys per stream block, so setup cost matters as much as throughput.
func BenchmarkSetup(b *testing.B) {
	key, iv := sha1.Sum([]byte("key")), sha1.Sum([]byte("iv"))
	for b.Loop() {
		if _, err := New(key[:], iv[:]); err != nil {
			b.Fatal(err)
		}
	}
}
