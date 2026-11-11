// Package secrets stores the Media Access Key and other credentials outside
// the configuration file, in the operating system's credential store
// (Windows Credential Manager).
package secrets

import (
	"errors"
	"regexp"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound means no secret is stored under that name.
var ErrNotFound = errors.New("secrets: not found")

// Names of the secrets TivoDesk keeps.
const (
	MAK             = "mak"               // the account's Media Access Key
	RPCCertPassword = "rpc-cert-password" // password of the RPC client certificate
)

// TiVoMAK is the name of a Media Access Key that applies to one TiVo only.
func TiVoMAK(tsn string) string { return "mak:" + tsn }

// Store keeps named secrets.
type Store interface {
	Get(name string) (string, error) // ErrNotFound if absent
	Set(name, value string) error
	Delete(name string) error // deleting an absent secret is not an error
}

var makPattern = regexp.MustCompile(`^\d{10}$`)

// ValidMAK reports whether s looks like a Media Access Key: ten digits.
func ValidMAK(s string) bool { return makPattern.MatchString(s) }

// service is the name the secrets are filed under in the credential store.
const service = "TivoDesk"

type keyringStore struct{}

// System returns the store backed by the operating system.
func System() Store { return keyringStore{} }

func (keyringStore) Get(name string) (string, error) {
	v, err := keyring.Get(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (keyringStore) Set(name, value string) error { return keyring.Set(service, name, value) }

func (keyringStore) Delete(name string) error {
	err := keyring.Delete(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is a Store that lives only in this process. It is for tests and
// for running without a credential store.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (s *Memory) Get(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Memory) Set(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[name] = value
	return nil
}

func (s *Memory) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, name)
	return nil
}
