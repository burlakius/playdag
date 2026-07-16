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

	s.Set("auth.token", "tok123", "auth.login", "session", "")

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
	s.Set("a", "1", "nodeA", "session", "")

	_, err := s.Collect([]string{"a", "b"}, "")
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
	s.Set("x", "1", "n1", "session", "")
	s.Set("y", "2", "n2", "session", "")

	got, err := s.Collect([]string{"x", "y"}, "")
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
			s.Set(key, fmt.Sprintf("val%d", i), "node", "suite", "")
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
	s.Set("auth.session_key", "abc123", "auth.login", "session", "")
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file not created: %v", err)
	}
}

func TestSnapshotFileStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context-snapshot.json")

	s := NewContextStore(path)
	s.Set("auth.session_key", "abc123", "auth.login", "session", "")
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
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

// A session-scoped value belongs to the agent that produced it. Another agent
// must not see it: that is the difference between reusing a browser context and
// handing someone else's session to a test that has no right to it.
func TestScope_SessionIsPrivateToItsAgent(t *testing.T) {
	s := NewContextStore("")
	s.Set("session.token", "tok-a", "auth.login", ScopeSession, "agent-1")

	got, err := s.Collect([]string{"session.token"}, "agent-1")
	if err != nil {
		t.Fatalf("the owning agent must see its own value: %v", err)
	}
	if got["session.token"] != "tok-a" {
		t.Fatalf("value: got %q", got["session.token"])
	}

	if _, err := s.Collect([]string{"session.token"}, "agent-2"); err == nil {
		t.Fatal("another agent must not see a session-scoped value")
	}
}

// Two agents can hold their own value under the same name at the same time.
func TestScope_SessionValuesDoNotCollide(t *testing.T) {
	s := NewContextStore("")
	s.Set("session.token", "tok-a", "auth.login", ScopeSession, "agent-1")
	s.Set("session.token", "tok-b", "auth.login", ScopeSession, "agent-2")

	for agent, want := range map[string]string{"agent-1": "tok-a", "agent-2": "tok-b"} {
		got, err := s.Collect([]string{"session.token"}, agent)
		if err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		if got["session.token"] != want {
			t.Errorf("%s: want %q, got %q", agent, want, got["session.token"])
		}
	}
}

func TestScope_SuiteIsVisibleEverywhere(t *testing.T) {
	s := NewContextStore("")
	s.Set("card.id", "42", "card.create", ScopeSuite, "agent-1")

	for _, agent := range []string{"agent-1", "agent-2", ""} {
		got, err := s.Collect([]string{"card.id"}, agent)
		if err != nil {
			t.Fatalf("suite scope must be visible from %q: %v", agent, err)
		}
		if got["card.id"] != "42" {
			t.Errorf("from %q: got %q", agent, got["card.id"])
		}
	}
}

func TestSessionFor_FindsTheOwningAgent(t *testing.T) {
	s := NewContextStore("")
	s.Set("session.token", "tok", "auth.login", ScopeSession, "agent-2")
	s.Set("card.id", "42", "card.create", ScopeSuite, "agent-1")

	if got, ok := s.SessionFor("session.token"); !ok || got != "agent-2" {
		t.Errorf("session.token: want agent-2, got %q (ok=%v)", got, ok)
	}
	// A suite-scoped value pins nothing: it can be read anywhere.
	if _, ok := s.SessionFor("card.id"); ok {
		t.Error("a suite-scoped key must not pin a node to an agent")
	}
}

func TestSnapshot_NamesSessionValuesByAgent(t *testing.T) {
	s := NewContextStore("")
	s.Set("session.token", "tok-a", "auth.login", ScopeSession, "agent-1")
	s.Set("session.token", "tok-b", "auth.login", ScopeSession, "agent-2")
	s.Set("card.id", "42", "card.create", ScopeSuite, "agent-1")

	snap := s.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("every value should appear once, got %d: %v", len(snap), snap)
	}
	if snap["session.token@agent-1"].Value != "tok-a" || snap["session.token@agent-2"].Value != "tok-b" {
		t.Errorf("session values not separated: %v", snap)
	}
	if snap["card.id"].Value != "42" {
		t.Errorf("suite value should keep its plain name: %v", snap)
	}
}
