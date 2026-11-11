package secrets

import (
	"errors"
	"testing"
)

func exercise(t *testing.T, s Store, name string) {
	t.Helper()
	if err := s.Delete(name); err != nil {
		t.Fatalf("deleting an absent secret: %v", err)
	}
	if _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of an absent secret: got %v, want ErrNotFound", err)
	}
	if err := s.Set(name, "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(name, "second"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get(name); err != nil || v != "second" {
		t.Fatalf("Get = %q, %v; want the latest value", v, err)
	}
	if err := s.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestMemory(t *testing.T) { exercise(t, NewMemory(), MAK) }

// The real credential store, under a name TivoDesk never uses. It is
// skipped where there is no store to talk to, such as a headless CI runner.
func TestSystem(t *testing.T) {
	s := System()
	const name = "selftest-not-a-real-secret"
	if err := s.Set(name, "probe"); err != nil {
		t.Skipf("no usable credential store: %v", err)
	}
	exercise(t, s, name)
}

func TestValidMAK(t *testing.T) {
	for in, want := range map[string]bool{
		"1234567890": true, "0000000000": true,
		"123456789": false, "12345678901": false, "12345 7890": false, "": false, "abcdefghij": false,
	} {
		if ValidMAK(in) != want {
			t.Errorf("ValidMAK(%q) = %v, want %v", in, !want, want)
		}
	}
	if TiVoMAK("746000") != "mak:746000" {
		t.Error("TiVoMAK name changed; stored keys would be orphaned")
	}
}
