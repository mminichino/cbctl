package settings

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when a keychain entry is absent.
var ErrNotFound = errors.New("keychain entry not found")

const keyringService = "cbctl"

// Keyring stores Capella API keys outside the filesystem.
type Keyring interface {
	Get(profile string) (string, error)
	Set(profile, apiKey string) error
	Delete(profile string) error
}

// OSKeyring wraps the OS credential store via go-keyring. Service is the keychain service; empty
// is cbctl's ("cbctl"), LegacyKeyringService reads capellactl's entries.
type OSKeyring struct{ Service string }

func (k OSKeyring) service() string {
	if k.Service == "" {
		return keyringService
	}
	return k.Service
}

// Get returns the API key for profile.
func (k OSKeyring) Get(profile string) (string, error) {
	v, err := keyring.Get(k.service(), profile)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	if v == "" {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores the API key for profile.
func (k OSKeyring) Set(profile, apiKey string) error {
	return keyring.Set(k.service(), profile, apiKey)
}

// Delete removes the API key for profile.
func (k OSKeyring) Delete(profile string) error {
	err := keyring.Delete(k.service(), profile)
	if err != nil && errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// MemKeyring is an in-memory Keyring for tests.
type MemKeyring struct {
	mu   sync.Mutex
	data map[string]string
}

// NewMemKeyring returns an empty MemKeyring.
func NewMemKeyring() *MemKeyring {
	return &MemKeyring{data: map[string]string{}}
}

// Get returns the stored key or ErrNotFound.
func (m *MemKeyring) Get(profile string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[profile]
	if !ok || v == "" {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores a key.
func (m *MemKeyring) Set(profile, apiKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]string{}
	}
	m.data[profile] = apiKey
	return nil
}

// Delete removes a key.
func (m *MemKeyring) Delete(profile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, profile)
	return nil
}
