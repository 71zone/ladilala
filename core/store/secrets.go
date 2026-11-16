package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// ErrNoSecret is returned by Secrets.Get for an unknown key.
var ErrNoSecret = errors.New("store: no such secret")

// Secrets keeps values that must never reach the SQLite file: proxy
// passwords and tokens. Keys look like "proxy/<id>".
type Secrets interface {
	Set(ctx context.Context, key, value string) error
	Get(ctx context.Context, key string) (string, error)
	Delete(ctx context.Context, key string) error
}

// MemorySecrets is the in-memory implementation for tests.
type MemorySecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *MemorySecrets) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}

func (s *MemorySecrets) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNoSecret
	}
	return v, nil
}

func (s *MemorySecrets) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

// Keys lists the stored keys (tests).
func (s *MemorySecrets) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.m {
		out = append(out, k)
	}
	return out
}

// codec turns a secret into bytes for the file and back.
type codec struct {
	seal func([]byte) ([]byte, error)
	open func([]byte) ([]byte, error)
}

// fileSecrets keeps sealed values in one JSON file: key -> base64(sealed).
type fileSecrets struct {
	path string
	c    codec
	mu   sync.Mutex
}

func (f *fileSecrets) load() (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

func (f *fileSecrets) save(m map[string]string) error {
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f *fileSecrets) Set(_ context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	sealed, err := f.c.seal([]byte(value))
	if err != nil {
		return err
	}
	m[key] = base64.StdEncoding.EncodeToString(sealed)
	return f.save(m)
}

func (f *fileSecrets) Get(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	enc, ok := m[key]
	if !ok {
		return "", ErrNoSecret
	}
	sealed, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	v, err := f.c.open(sealed)
	return string(v), err
}

func (f *fileSecrets) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	delete(m, key)
	return f.save(m)
}

// NewInsecureFileSecrets keeps secrets in a file as plain base64. It is NOT
// encryption. It exists for the macOS and Linux development build only; the
// Windows build uses NewDPAPISecrets through DefaultSecrets.
func NewInsecureFileSecrets(path string) Secrets {
	id := func(b []byte) ([]byte, error) { return b, nil }
	return &fileSecrets{path: path, c: codec{seal: id, open: id}}
}
