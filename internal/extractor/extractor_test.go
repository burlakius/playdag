package extractor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"playdag/internal/loader"
)

func node(id, file string, line int, requires, provides []string) Node {
	return Node{
		TestNode: loader.TestNode{
			TestID: id,
			Script: file,
			Policy: loader.ExecutionPolicy{Criticality: "BLOCKER"},
			Contract: loader.DataContract{
				Requires: requires,
				Provides: provides,
			},
		},
		File: file,
		Line: line,
	}
}

func TestValidate_Chain(t *testing.T) {
	nodes := []Node{
		node("auth.login", "login.py", 3, nil, []string{"session.token"}),
		node("card.create", "card.py", 3, []string{"session.token"}, nil),
	}
	if err := Validate(nodes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_NoNodes(t *testing.T) {
	if err := Validate(nil); err == nil {
		t.Fatal("expected an error when nothing declared metadata")
	}
}

// Every validation error must name the file and line, since that is the whole
// point of validating at extraction time.
func TestValidate_DuplicateIDPointsAtBothSites(t *testing.T) {
	nodes := []Node{
		node("auth.login", "a.py", 3, nil, nil),
		node("auth.login", "b.py", 7, nil, nil),
	}
	err := Validate(nodes)
	if err == nil {
		t.Fatal("expected a duplicate-id error")
	}
	for _, want := range []string{"a.py:3", "b.py:7", "auth.login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestValidate_DuplicateProvidesPointsAtBothSites(t *testing.T) {
	nodes := []Node{
		node("a", "a.py", 3, nil, []string{"session.token"}),
		node("b", "b.py", 9, nil, []string{"session.token"}),
	}
	err := Validate(nodes)
	if err == nil {
		t.Fatal("expected a duplicate-provides error")
	}
	for _, want := range []string{"session.token", "a.py:3", "b.py:9"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestValidate_UnknownKey(t *testing.T) {
	nodes := []Node{
		node("a", "a.py", 3, nil, []string{"session.token"}),
		node("b", "b.py", 5, []string{"nonexistent.key"}, nil),
	}
	err := Validate(nodes)
	if err == nil {
		t.Fatal("expected an unknown-key error")
	}
	for _, want := range []string{"nonexistent.key", "b.py:5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// A typo is the usual cause, so the error points at the key that was probably meant.
func TestValidate_UnknownKeySuggests(t *testing.T) {
	nodes := []Node{
		node("a", "a.py", 3, nil, []string{"session.token"}),
		node("b", "b.py", 5, []string{"session.Token"}, nil),
	}
	err := Validate(nodes)
	if err == nil || !strings.Contains(err.Error(), `did you mean "session.token"`) {
		t.Fatalf("expected a suggestion, got: %v", err)
	}
}

func TestValidate_Cycle(t *testing.T) {
	nodes := []Node{
		node("a", "a.py", 3, []string{"b.out"}, []string{"a.out"}),
		node("b", "b.py", 4, []string{"a.out"}, []string{"b.out"}),
	}
	err := Validate(nodes)
	if err == nil {
		t.Fatal("expected a cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention the cycle, got: %v", err)
	}
	for _, want := range []string{"a.py:3", "b.py:4"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should point at %q, got: %v", want, err)
		}
	}
}

func TestSuite_SortedByID(t *testing.T) {
	nodes := []Node{
		node("z", "z.py", 1, nil, nil),
		node("a", "a.py", 1, nil, nil),
		node("m", "m.py", 1, nil, nil),
	}
	s := Suite("demo", nodes)

	if s.SuiteID != "demo" {
		t.Errorf("suite_id: got %q", s.SuiteID)
	}
	want := []string{"a", "m", "z"}
	for i, w := range want {
		if s.Nodes[i].TestID != w {
			t.Fatalf("node order: want %v, got %s at %d", want, s.Nodes[i].TestID, i)
		}
	}
}

// The generated suite must load back through the real loader, or extraction has
// produced a file the orchestrator cannot run.
func TestSaveAndLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suite.json")
	nodes := []Node{
		node("auth.login", "login.py", 3, nil, []string{"session.token"}),
		node("card.create", "card.py", 3, []string{"session.token"}, nil),
	}

	if err := Save(Suite("demo", nodes), path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	suite, err := loader.LoadSuite(path)
	if err != nil {
		t.Fatalf("the generated suite does not load: %v", err)
	}
	if len(suite.Nodes) != 2 || suite.Nodes[0].TestID != "auth.login" {
		t.Fatalf("unexpected suite: %+v", suite.Nodes)
	}
	if suite.GeneratedAt == "" {
		t.Error("generated_at should be stamped")
	}
}

func TestSave_IsValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suite.json")
	if err := Save(Suite("demo", []Node{node("a", "a.py", 1, nil, nil)}), path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("file should end with a newline")
	}
}

func TestNew_Unknown(t *testing.T) {
	if _, err := New("cypress", "."); err == nil {
		t.Fatal("expected an error for an unknown extractor")
	}
}
