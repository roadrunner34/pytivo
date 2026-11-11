package tivofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// testdata/synthetic.tivo is built and read back by pyTivo
// (tools/gen_turing.py), so this compares Go against Python.
func TestDetailsMatchesPython(t *testing.T) {
	raw, err := os.ReadFile("testdata/synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		MAK     string `json:"mak"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open("testdata/synthetic.tivo")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	h, err := ReadHeader(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(h.Chunks))
	}
	got, err := h.Details(want.MAK)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want.Details {
		t.Fatalf("details differ from Python\n got %q\nwant %q", got, want.Details)
	}

	wrong, err := h.Details("0000000000")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(wrong, got) {
		t.Fatal("a wrong MAK produced the right plaintext")
	}
}

func TestReadHeaderRejectsBadInput(t *testing.T) {
	if _, err := ReadHeader(strings.NewReader("MPEGnot a tivo file at all")); !errors.Is(err, ErrNotTiVo) {
		t.Errorf("wrong magic: got %v, want ErrNotTiVo", err)
	}
	if _, err := ReadHeader(strings.NewReader("TiVo")); err == nil {
		t.Error("truncated header: got nil error")
	}

	good, err := os.ReadFile("testdata/synthetic.tivo")
	if err != nil {
		t.Fatal(err)
	}
	// Claim one more chunk than the header holds.
	bad := bytes.Clone(good)
	bad[15]++
	if _, err := ReadHeader(bytes.NewReader(bad)); err == nil {
		t.Error("overstated chunk count: got nil error")
	}
	// Cut the file off in the middle of the chunks.
	if _, err := ReadHeader(bytes.NewReader(good[:40])); err == nil {
		t.Error("truncated chunks: got nil error")
	}
}
