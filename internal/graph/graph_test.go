package graph

import (
	"sort"
	"strings"
	"testing"

	"playdag/internal/loader"
)

// node is a helper to build a loader.TestNode concisely.
func node(id, criticality string, requires, provides []string) loader.TestNode {
	return loader.TestNode{
		TestID: id,
		Script: id + ".spec.ts",
		Policy: loader.ExecutionPolicy{Criticality: criticality},
		Contract: loader.DataContract{
			Requires: requires,
			Provides: provides,
		},
	}
}

func nodeIDs(nodes []*loader.TestNode) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.TestID
	}
	sort.Strings(ids)
	return ids
}

func TestNewDAG_LinearChain(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", nil, []string{"a_out"}),
		node("B", "BLOCKER", []string{"a_out"}, []string{"b_out"}),
		node("C", "BLOCKER", []string{"b_out"}, nil),
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ready := nodeIDs(dag.ReadyNodes())
	if len(ready) != 1 || ready[0] != "A" {
		t.Fatalf("expected only A ready, got %v", ready)
	}
}

func TestNewDAG_ParallelBranches(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", nil, []string{"a_out"}),
		node("B", "BLOCKER", []string{"a_out"}, nil),
		node("C", "BLOCKER", []string{"a_out"}, nil),
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dag.MarkRunning("A")
	newlyReady := nodeIDs(dag.MarkPassed("A"))
	if len(newlyReady) != 2 {
		t.Fatalf("expected B and C ready after A passes, got %v", newlyReady)
	}
	if newlyReady[0] != "B" || newlyReady[1] != "C" {
		t.Fatalf("expected [B C], got %v", newlyReady)
	}
}

func TestNewDAG_CycleDetection(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", []string{"b_out"}, []string{"a_out"}),
		node("B", "BLOCKER", []string{"a_out"}, []string{"b_out"}),
	}
	_, err := NewDAG(nodes)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected 'cycle' in error, got: %v", err)
	}
}

func TestMarkFailed_BlockerSkipsDependants(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", nil, []string{"a_out"}),
		node("B", "BLOCKER", []string{"a_out"}, []string{"b_out"}),
		node("C", "BLOCKER", []string{"b_out"}, nil),
		node("D", "BLOCKER", nil, nil), // independent
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dag.MarkRunning("A")
	skipped := dag.MarkFailed("A")
	sort.Strings(skipped)

	if len(skipped) != 2 || skipped[0] != "B" || skipped[1] != "C" {
		t.Fatalf("expected [B C] skipped, got %v", skipped)
	}

	// D must be unaffected.
	dag.mu.RLock()
	dStatus := dag.status["D"]
	dag.mu.RUnlock()
	if dStatus != StatusPending {
		t.Fatalf("D should still be pending, got %v", dStatus)
	}
}

func TestMarkFailed_NonCriticalSkipsDependants(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "NON_CRITICAL", nil, []string{"a_out"}),
		node("B", "NON_CRITICAL", []string{"a_out"}, nil),
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dag.MarkRunning("A")
	skipped := dag.MarkFailed("A")
	if len(skipped) != 1 || skipped[0] != "B" {
		t.Fatalf("expected B skipped, got %v", skipped)
	}

	dag.mu.RLock()
	bStatus := dag.status["B"]
	dag.mu.RUnlock()
	if bStatus != StatusSkipped {
		t.Fatalf("B should be skipped, got %v", bStatus)
	}
	if !dag.AllDone() {
		t.Fatal("suite should be done: A failed, B skipped, none left pending")
	}
}

func TestCriticalPath_DiamondAndChain(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", nil, []string{"a"}),
		node("B", "BLOCKER", []string{"a"}, []string{"b"}),
		node("C", "BLOCKER", []string{"a"}, []string{"c"}),
		node("D", "BLOCKER", []string{"b", "c"}, nil),
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cp := dag.CriticalPath()
	want := map[string]int{"A": 3, "B": 2, "C": 2, "D": 1}
	for id, w := range want {
		if cp[id] != w {
			t.Errorf("critical path %s: want %d, got %d", id, w, cp[id])
		}
	}
}

func TestAllTags_DistinctSorted(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", nil, []string{"a"}),
		node("B", "BLOCKER", []string{"a"}, nil),
	}
	nodes[0].Resources.Tags = []string{"firefox", "chromium"}
	nodes[1].Resources.Tags = []string{"chromium"}

	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := dag.AllTags()
	want := []string{"chromium", "firefox"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AllTags: want %v, got %v", want, got)
	}
}

func TestNewDAG_DuplicateProvides(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", nil, []string{"shared"}),
		node("B", "BLOCKER", nil, []string{"shared"}),
	}
	_, err := NewDAG(nodes)
	if err == nil {
		t.Fatal("expected error for duplicate provides, got nil")
	}
	if !strings.Contains(err.Error(), "shared") {
		t.Fatalf("expected key name in error, got: %v", err)
	}
}

func TestAllDone(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", nil, []string{"a_out"}),
		node("B", "BLOCKER", []string{"a_out"}, nil),
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dag.AllDone() {
		t.Fatal("should not be done yet")
	}

	dag.MarkRunning("A")
	dag.MarkPassed("A")

	if dag.AllDone() {
		t.Fatal("B is still pending; should not be done")
	}

	dag.MarkRunning("B")
	dag.MarkPassed("B")

	if !dag.AllDone() {
		t.Fatal("all nodes passed; should be done")
	}
}

func TestNewDAG_MissingKey(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "BLOCKER", []string{"nonexistent"}, nil),
	}
	_, err := NewDAG(nodes)
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("expected key name in error, got: %v", err)
	}
}

// exportDAG builds the example shape: login → create → move, with move also
// depending on the login directly.
func exportDAG(t *testing.T) *DAG {
	t.Helper()
	nodes := []loader.TestNode{
		node("auth.login", "BLOCKER", nil, []string{"session.token"}),
		node("card.create", "BLOCKER", []string{"session.token"}, []string{"card.id"}),
		node("card.move", "NON_CRITICAL", []string{"session.token", "card.id"}, nil),
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	return dag
}

// The edge labels are the point of the picture: they show the graph comes from
// data contracts, not from a hand-written order.
func TestMermaid_EdgesAreLabelledWithTheKey(t *testing.T) {
	out := exportDAG(t).Mermaid()

	for _, want := range []string{
		"flowchart TD",
		`auth_login["auth.login"]`,
		"auth_login -->|session.token| card_create",
		"card_create -->|card.id| card_move",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mermaid should contain %q:\n%s", want, out)
		}
	}
}

// A test id contains dots, which a Mermaid identifier may not.
func TestMermaid_IDsAreSanitised(t *testing.T) {
	out := exportDAG(t).Mermaid()
	if strings.Contains(out, "auth.login[") {
		t.Errorf("a raw dotted id would not parse as a mermaid node:\n%s", out)
	}
}

func TestMermaid_NonCriticalNodesAreMarked(t *testing.T) {
	out := exportDAG(t).Mermaid()
	if !strings.Contains(out, "class card_move nonCritical") {
		t.Errorf("the non-critical node should be drawn apart:\n%s", out)
	}
	if strings.Contains(out, "class auth_login nonCritical") {
		t.Errorf("a blocker must not be marked non-critical:\n%s", out)
	}
}

func TestDOT_Structure(t *testing.T) {
	out := exportDAG(t).DOT()
	for _, want := range []string{
		"digraph playdag {",
		`"auth.login" -> "card.create" [label="session.token"];`,
		`"card.move" [style=dashed];`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dot should contain %q:\n%s", want, out)
		}
	}
}

// The same suite must render the same file, or the diagram in the write-up
// churns on every regeneration.
func TestExport_IsDeterministic(t *testing.T) {
	for i := 0; i < 5; i++ {
		if got, want := exportDAG(t).Mermaid(), exportDAG(t).Mermaid(); got != want {
			t.Fatalf("mermaid output is not stable:\n%s\nvs\n%s", got, want)
		}
		if got, want := exportDAG(t).DOT(), exportDAG(t).DOT(); got != want {
			t.Fatalf("dot output is not stable:\n%s\nvs\n%s", got, want)
		}
	}
}
