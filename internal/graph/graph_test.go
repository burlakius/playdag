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
	skipped := dag.MarkFailed("A", "BLOCKER")
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

func TestMarkFailed_NonCriticalNoSkip(t *testing.T) {
	nodes := []loader.TestNode{
		node("A", "NON_CRITICAL", nil, []string{"a_out"}),
		node("B", "NON_CRITICAL", []string{"a_out"}, nil),
	}
	dag, err := NewDAG(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dag.MarkRunning("A")
	skipped := dag.MarkFailed("A", "NON_CRITICAL")
	if len(skipped) != 0 {
		t.Fatalf("expected no skips for NON_CRITICAL, got %v", skipped)
	}

	dag.mu.RLock()
	bStatus := dag.status["B"]
	dag.mu.RUnlock()
	// B's inDegree is still 1, so it stays pending (not skipped).
	if bStatus != StatusPending {
		t.Fatalf("B should remain pending, got %v", bStatus)
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
