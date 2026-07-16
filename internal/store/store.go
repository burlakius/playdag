// Package store holds the shared context produced and consumed by test nodes.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Scopes a value can be declared with. The scope decides how long the value
// lives and who can see it, which is the difference between reusing a session
// and leaking one.
const (
	// ScopeSession keeps the value inside the agent that produced it: it stands
	// for something bound to one worker, such as a browser context.
	ScopeSession = "session"
	// ScopeSuite keeps the value for the rest of the run. It is the default.
	ScopeSuite = "suite"
	// ScopeGlobal keeps the value beyond the run: it is persisted and reloaded,
	// so an expensive setup is paid for once rather than once per run.
	ScopeGlobal = "global"
)

// ContextValue is a single entry in the store, carrying provenance metadata.
type ContextValue struct {
	Value      string `json:"value"`
	ProvidedBy string `json:"provided_by"`
	Scope      string `json:"scope"`
	Session    string `json:"session,omitempty"` // agent that owns it, for session scope
}

// valueKey identifies a value. Session-scoped values are held per session;
// everything else is shared by the whole run.
type valueKey struct {
	session string
	key     string
}

// ContextStore is a thread-safe key/value store with optional snapshot
// persistence.
type ContextStore struct {
	mu           sync.RWMutex
	values       map[valueKey]ContextValue
	snapshotPath string
	globalPath   string
}

// NewContextStore returns an empty store. snapshotPath may be empty to disable
// file persistence.
func NewContextStore(snapshotPath string) *ContextStore {
	return &ContextStore{
		values:       make(map[valueKey]ContextValue),
		snapshotPath: snapshotPath,
	}
}

// UseGlobalFile makes global-scoped values outlive the run: whatever the file
// already holds is loaded now, and Flush writes the file back. A missing file is
// simply an empty history, which is what the first run sees.
func (s *ContextStore) UseGlobalFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.globalPath = path

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: read global context: %w", err)
	}

	var file snapshotFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("store: parse global context %s: %w", path, err)
	}
	for key, cv := range file.Values {
		// Only global values are carried over; anything else in the file belonged
		// to a run that has ended.
		if cv.Scope != ScopeGlobal {
			continue
		}
		s.values[valueKey{key: key}] = cv
	}
	return nil
}

// Set stores a value produced by providedBy while running on session. A
// session-scoped value is visible only to nodes on that same session; any other
// scope is visible to the whole run. The snapshot file is written by Flush, not
// on every write, to keep hot-path writes off the disk.
func (s *ContextStore) Set(key, value, providedBy, scope, session string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	owner := ""
	if scope == ScopeSession {
		owner = session
	}
	s.values[valueKey{session: owner, key: key}] = ContextValue{
		Value:      value,
		ProvidedBy: providedBy,
		Scope:      scope,
		Session:    owner,
	}
}

// Get returns the value visible to the whole run for key.
func (s *ContextStore) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cv, ok := s.values[valueKey{key: key}]
	return cv.Value, ok
}

// Collect resolves every key as seen from session, returning a key→value map.
// A value the session owns wins over a shared one of the same name. It returns
// an error on the first key that is not visible.
func (s *ContextStore) Collect(keys []string, session string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]string, len(keys))
	for _, k := range keys {
		cv, ok := s.lookup(k, session)
		if !ok {
			return nil, fmt.Errorf("required key %q not found in ContextStore", k)
		}
		out[k] = cv.Value
	}
	return out, nil
}

// lookup finds a key as seen from session. Callers hold the lock.
func (s *ContextStore) lookup(key, session string) (ContextValue, bool) {
	if cv, ok := s.values[valueKey{session: session, key: key}]; ok {
		return cv, true
	}
	cv, ok := s.values[valueKey{key: key}]
	return cv, ok
}

// SessionFor reports which session owns a session-scoped key. A node needing
// that key can only run there, since the value exists nowhere else.
func (s *ContextStore) SessionFor(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for vk, cv := range s.values {
		if vk.key == key && cv.Scope == ScopeSession {
			return vk.session, true
		}
	}
	return "", false
}

// Snapshot returns a copy of the store for the report. A session-scoped value is
// rendered as "key@session", since the same key may exist once per session.
func (s *ContextStore) Snapshot() map[string]ContextValue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot()
}

// snapshot builds the flat view. Callers hold the lock.
func (s *ContextStore) snapshot() map[string]ContextValue {
	cp := make(map[string]ContextValue, len(s.values))
	for vk, cv := range s.values {
		name := vk.key
		if vk.session != "" {
			name = vk.key + "@" + vk.session
		}
		cp[name] = cv
	}
	return cp
}

// snapshotFile is the on-disk representation of the store.
type snapshotFile struct {
	GeneratedAt string                  `json:"generated_at"`
	Values      map[string]ContextValue `json:"values"`
}

// Flush writes the snapshot and, when one is configured, the global context
// file. Call it once the run is complete.
func (s *ContextStore) Flush() error {
	s.mu.RLock()
	all := s.snapshot()
	global := make(map[string]ContextValue)
	for name, cv := range all {
		if cv.Scope == ScopeGlobal {
			global[name] = cv
		}
	}
	globalPath, snapshotPath := s.globalPath, s.snapshotPath
	s.mu.RUnlock()

	if snapshotPath != "" {
		if err := writeValues(snapshotPath, all); err != nil {
			return err
		}
	}
	if globalPath != "" {
		if err := writeValues(globalPath, global); err != nil {
			return err
		}
	}
	return nil
}

// writeValues serialises a set of values to path.
func writeValues(path string, values map[string]ContextValue) error {
	payload := snapshotFile{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Values:      values,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal %s: %w", path, err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("store: write %s: %w", path, err)
	}
	return nil
}
