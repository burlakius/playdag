package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSetAndGet(t *testing.T) {
	s := NewContextStore("")

	if err := s.Set("auth.token", "tok123", "auth.login", "session"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	v, ok := s.Get("auth.token")
	if !ok {
		t.Fatal("expected key to be present")
	}
	if v != "tok123" {
		t.Fatalf("expected tok123, got %q", v)
	}

	_, ok = s.Get("missing")
	if ok {
		t.Fatal("expected missing key to return false")
	}
}

func TestCollect_MissingKey(t *testing.T) {
	s := NewContextStore("")
	s.Set("a", "1", "nodeA", "session") //nolint:errcheck

	_, err := s.Collect([]string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	want := `required key "b" not found in ContextStore`
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
	}
}

func TestCollect_AllPresent(t *testing.T) {
	s := NewContextStore("")
	s.Set("x", "1", "n1", "session") //nolint:errcheck
	s.Set("y", "2", "n2", "session") //nolint:errcheck

	got, err := s.Collect([]string{"x", "y"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["x"] != "1" || got["y"] != "2" {
		t.Fatalf("unexpected values: %v", got)
	}
}

func TestConcurrentSetAndGet(t *testing.T) {
	t.Parallel()

	s := NewContextStore("")
	const workers = 50

	var wg sync.WaitGroup
	wg.Add(workers * 2)

	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key%d", i)
			s.Set(key, fmt.Sprintf("val%d", i), "node", "session") //nolint:errcheck
		}()
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key%d", i)
			s.Get(key) // result doesn't matter; race detector validates safety
		}()
	}
	wg.Wait()
}

func TestSnapshotFileWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context-snapshot.json")

	s := NewContextStore(path)
	if err := s.Set("auth.session_key", "abc123", "auth.login", "session"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file not created: %v", err)
	}
}

func TestSnapshotFileStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context-snapshot.json")

	s := NewContextStore(path)
	if err := s.Set("auth.session_key", "abc123", "auth.login", "session"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	var got struct {
		GeneratedAt string                  `json:"generated_at"`
		Values      map[string]ContextValue `json:"values"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.GeneratedAt == "" {
		t.Error("generated_at must not be empty")
	}
	cv, ok := got.Values["auth.session_key"]
	if !ok {
		t.Fatal("expected auth.session_key in values")
	}
	if cv.Value != "abc123" {
		t.Errorf("value: want abc123, got %q", cv.Value)
	}
	if cv.ProvidedBy != "auth.login" {
		t.Errorf("provided_by: want auth.login, got %q", cv.ProvidedBy)
	}
	if cv.Scope != "session" {
		t.Errorf("scope: want session, got %q", cv.Scope)
	}
}
