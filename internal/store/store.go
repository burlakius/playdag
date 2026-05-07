// Package store holds the shared context produced and consumed by test nodes.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// ContextValue is a single entry in the store, carrying provenance metadata.
type ContextValue struct {
	Value      string `json:"value"`
	ProvidedBy string `json:"provided_by"`
	Scope      string `json:"scope"`
}

// ContextStore is a thread-safe key/value store with optional snapshot persistence.
type ContextStore struct {
	mu           sync.RWMutex
	data         map[string]ContextValue
	snapshotPath string
}

// NewContextStore returns an empty store. snapshotPath may be empty to disable
// file persistence.
func NewContextStore(snapshotPath string) *ContextStore {
	return &ContextStore{
		data:         make(map[string]ContextValue),
		snapshotPath: snapshotPath,
	}
}

// Set stores a value and, if snapshotPath is configured, flushes the store to
// disk. The in-memory write always succeeds; an error is returned only if the
// snapshot write fails.
func (s *ContextStore) Set(key, value, providedBy, scope string) error {
	s.mu.Lock()
	s.data[key] = ContextValue{Value: value, ProvidedBy: providedBy, Scope: scope}
	s.mu.Unlock()

	if s.snapshotPath == "" {
		return nil
	}
	return s.writeSnapshot()
}

// Get returns the raw string value for key and a boolean indicating presence.
func (s *ContextStore) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cv, ok := s.data[key]
	return cv.Value, ok
}

// Collect resolves every key in the slice and returns a key→value map. It
// returns an error on the first missing key.
func (s *ContextStore) Collect(keys []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]string, len(keys))
	for _, k := range keys {
		cv, ok := s.data[k]
		if !ok {
			return nil, fmt.Errorf("required key %q not found in ContextStore", k)
		}
		out[k] = cv.Value
	}
	return out, nil
}

// Snapshot returns a deep copy of the internal data map.
func (s *ContextStore) Snapshot() map[string]ContextValue {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cp := make(map[string]ContextValue, len(s.data))
	for k, v := range s.data {
		cp[k] = v
	}
	return cp
}

// snapshotFile is the on-disk representation of the store.
type snapshotFile struct {
	GeneratedAt string                  `json:"generated_at"`
	Values      map[string]ContextValue `json:"values"`
}

func (s *ContextStore) writeSnapshot() error {
	s.mu.RLock()
	cp := make(map[string]ContextValue, len(s.data))
	for k, v := range s.data {
		cp[k] = v
	}
	s.mu.RUnlock()

	payload := snapshotFile{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Values:      cp,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal snapshot: %w", err)
	}
	if err := os.WriteFile(s.snapshotPath, b, 0o644); err != nil {
		return fmt.Errorf("store: write snapshot: %w", err)
	}
	return nil
}
