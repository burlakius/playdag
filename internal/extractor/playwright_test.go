package extractor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubNpx puts an npx on PATH that answers --list with listJSON, so the
// extractor exercises the real command path without a browser.
func stubNpx(t *testing.T, listJSON string) {
	t.Helper()
	bin := t.TempDir()

	out := filepath.Join(bin, "list.json")
	if err := os.WriteFile(out, []byte(listJSON), 0o644); err != nil {
		t.Fatalf("write list.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "npx"), []byte("#!/bin/sh\ncat "+out+"\n"), 0o755); err != nil {
		t.Fatalf("write fake npx: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// realList is the output a real `playwright test --list --reporter=json` gave
// for examples/playwright, with the project root left as a placeholder.
func realList(t *testing.T, baseDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "playwright_list.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return strings.ReplaceAll(string(data), "__ROOT__", baseDir)
}

func TestPlaywright_ExtractsRealListOutput(t *testing.T) {
	base := t.TempDir()
	stubNpx(t, realList(t, base))

	nodes, err := NewPlaywright(base).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d: %v", len(nodes), ids(nodes))
	}

	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.TestID] = n
	}

	login, ok := byID["auth.login"]
	if !ok {
		t.Fatalf("auth.login missing, got %v", ids(nodes))
	}
	if login.Script != filepath.Join("tests", "board.spec.ts") {
		t.Errorf("script: got %q", login.Script)
	}
	if login.Line != 5 {
		t.Errorf("line: got %d", login.Line)
	}
	if login.Description != "sign in" {
		t.Errorf("description should fall back to the test title, got %q", login.Description)
	}
	if len(login.Contract.Provides) != 1 || login.Contract.Provides[0] != "session.token" {
		t.Errorf("provides: got %v", login.Contract.Provides)
	}
	if login.Policy.Criticality != "BLOCKER" || login.Policy.Priority != 10 {
		t.Errorf("policy: got %+v", login.Policy)
	}
	if len(login.Resources.Tags) != 1 || login.Resources.Tags[0] != "chromium" {
		t.Errorf("tags: got %v", login.Resources.Tags)
	}

	move := byID["card.move"]
	want := []string{"session.token", "card.id"}
	if len(move.Contract.Requires) != 2 || move.Contract.Requires[0] != want[0] || move.Contract.Requires[1] != want[1] {
		t.Errorf("card.move requires: want %v, got %v", want, move.Contract.Requires)
	}
	if move.Policy.Criticality != "NON_CRITICAL" {
		t.Errorf("card.move criticality: got %q", move.Policy.Criticality)
	}
}

// What the real project declares must survive extraction into a runnable suite.
func TestPlaywright_ExtractedSuiteValidates(t *testing.T) {
	base := t.TempDir()
	stubNpx(t, realList(t, base))

	nodes, err := NewPlaywright(base).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if err := Validate(nodes); err != nil {
		t.Fatalf("the extracted suite does not validate: %v", err)
	}
}

func TestPlaywright_IgnoresTestsWithoutAnnotations(t *testing.T) {
	base := t.TempDir()
	stubNpx(t, `{
	  "config": {"rootDir": "`+base+`/tests"},
	  "suites": [{"file": "a.spec.ts", "specs": [
	    {"title": "plain test", "file": "a.spec.ts", "line": 3, "tests": [{"annotations": []}]},
	    {"title": "declared", "file": "a.spec.ts", "line": 9, "tests": [{"annotations": [
	      {"type": "playdag:id", "description": "a"}]}]}
	  ]}],
	  "errors": []
	}`)

	nodes, err := NewPlaywright(base).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 1 || nodes[0].TestID != "a" {
		t.Fatalf("want only the declared test, got %v", ids(nodes))
	}
}

// Playwright nests describe blocks, and a node may be declared in any of them.
func TestPlaywright_WalksNestedSuites(t *testing.T) {
	base := t.TempDir()
	stubNpx(t, `{
	  "config": {"rootDir": "`+base+`/tests"},
	  "suites": [{"file": "a.spec.ts", "specs": [], "suites": [
	    {"file": "a.spec.ts", "specs": [
	      {"title": "nested", "file": "a.spec.ts", "line": 4, "tests": [{"annotations": [
	        {"type": "playdag:id", "description": "nested.node"}]}]}
	    ]}
	  ]}],
	  "errors": []
	}`)

	nodes, err := NewPlaywright(base).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 1 || nodes[0].TestID != "nested.node" {
		t.Fatalf("want the nested node, got %v", ids(nodes))
	}
}

func TestPlaywright_ReportedErrorIsSurfaced(t *testing.T) {
	base := t.TempDir()
	stubNpx(t, `{"config":{"rootDir":"/x"},"suites":[],"errors":[{"message":"a.spec.ts: SyntaxError"}]}`)

	_, err := NewPlaywright(base).Extract(context.Background())
	if err == nil {
		t.Fatal("expected an error when playwright reports one")
	}
	if !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("playwright's message should survive, got: %v", err)
	}
}

func TestPlaywright_BrokenOutputIsAnError(t *testing.T) {
	base := t.TempDir()
	stubNpx(t, "not json")

	if _, err := NewPlaywright(base).Extract(context.Background()); err == nil {
		t.Fatal("expected an error for unparsable output")
	}
}
