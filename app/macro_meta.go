package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// macroMeta is what the UI keeps about a macro besides the macro document:
// the macro schema is strict and has no field for these. The file lives next
// to the database and is written atomically.
type macroMeta struct {
	Description string `json:"description,omitempty"`
	SourceID    string `json:"sourceId,omitempty"`
	// Warnings are the recorder warnings (canvas, file input) kept for the editor.
	Warnings []RecordWarning `json:"warnings,omitempty"`
	// Mappings holds the variable to column overrides per data source ID.
	Mappings map[string]map[string]string `json:"mappings,omitempty"`
}

type metaStore struct {
	mu   sync.Mutex
	path string
	m    map[string]macroMeta
}

var metaStores sync.Map // *Backend -> *metaStore

// metaOf returns the metadata store of a backend. MacroService and DataService
// share it.
func metaOf(b *Backend) *metaStore {
	if v, ok := metaStores.Load(b); ok {
		return v.(*metaStore)
	}
	s := &metaStore{path: filepath.Join(b.Store.Dir(), "macro-meta.json")}
	v, _ := metaStores.LoadOrStore(b, s)
	return v.(*metaStore)
}

func (s *metaStore) load() error {
	if s.m != nil {
		return nil
	}
	s.m = map[string]macroMeta{}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.m = nil
		return err
	}
	if err := json.Unmarshal(raw, &s.m); err != nil {
		s.m = nil
		return err
	}
	return nil
}

func (s *metaStore) save() error {
	raw, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// get returns the metadata of one macro (zero value when none).
func (s *metaStore) get(id string) (macroMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return macroMeta{}, err
	}
	return s.m[id], nil
}

// update changes the metadata of one macro and writes the file.
func (s *metaStore) update(id string, f func(*macroMeta)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	m := s.m[id]
	f(&m)
	if m.Description == "" && m.SourceID == "" && len(m.Warnings) == 0 && len(m.Mappings) == 0 {
		delete(s.m, id)
	} else {
		s.m[id] = m
	}
	return s.save()
}

// all returns a copy of every entry.
func (s *metaStore) all() (map[string]macroMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	out := make(map[string]macroMeta, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out, nil
}

// forgetSource removes a deleted data source from every macro.
func (s *metaStore) forgetSource(sourceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return err
	}
	for id, m := range s.m {
		if m.SourceID == sourceID {
			m.SourceID = ""
		}
		delete(m.Mappings, sourceID)
		if m.Description == "" && m.SourceID == "" && len(m.Warnings) == 0 && len(m.Mappings) == 0 {
			delete(s.m, id)
		} else {
			s.m[id] = m
		}
	}
	return s.save()
}
