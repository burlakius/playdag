// Integration tests with a real subprocess are in runner_integration_test.go
// with build tag //go:build integration
package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"playdag/internal/executor"
	"playdag/internal/graph"
	"playdag/internal/loader"
	"playdag/internal/protocol"
	"playdag/internal/store"
)

// fakeExecutor answers with a canned outcome per test_id and records what each
// node was handed, which is what lets the orchestration logic be tested without
// spawning anything.
type fakeExecutor struct {
	mu        sync.Mutex
	results   map[string]executor.Result
	errs      map[string]error
	failUntil map[string]int // test_id → attempt number that finally passes
	seen      map[string]map[string]string
	attempts  map[string]int
}

func newFake() *fakeExecutor {
	return &fakeExecutor{
		results:   make(map[string]executor.Result),
		errs:      make(map[string]error),
		failUntil: make(map[string]int),
		seen:      make(map[string]map[string]string),
		attempts:  make(map[string]int),
	}
}

func (f *fakeExecutor) Run(ctx context.Context, node *loader.TestNode, in executor.Input) (executor.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.attempts[node.TestID] = in.Attempt
	f.seen[node.TestID] = in.Context

	if err, ok := f.errs[node.TestID]; ok {
		return executor.Result{}, err
	}
	if pass, ok := f.failUntil[node.TestID]; ok && in.Attempt < pass {
		return executor.Result{Status: protocol.StatusFailed, Error: "flaky"}, nil
	}
	if res, ok := f.results[node.TestID]; ok {
		return res, nil
	}
	return executor.Result{Status: protocol.StatusPassed}, nil
}

func (f *fakeExecutor) contextFor(testID string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[testID]
}

func (f *fakeExecutor) attemptsFor(testID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts[testID]
}

func node(id string, requires, provides []string) loader.TestNode {
	return loader.TestNode{
		TestID:   id,
		Script:   id + ".py",
		Policy:   loader.ExecutionPolicy{Criticality: "BLOCKER"},
		Contract: loader.DataContract{Requires: requires, Provides: provides},
	}
}

// run builds a runner over nodes and drives it to completion.
func run(t *testing.T, nodes []loader.TestNode, exec executor.Executor) (*Runner, *store.ContextStore, error) {
	t.Helper()
	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	s := store.NewContextStore("")
	r := NewRunner(dag, s, RunOptions{MaxConcurrency: 2, Executor: exec})
	return r, s, r.Run(context.Background())
}

func TestRun_ProvidesReachTheStoreAndTheNextNode(t *testing.T) {
	f := newFake()
	f.results["auth.login"] = executor.Result{
		Status:   protocol.StatusPassed,
		Provides: map[string]string{"session.token": "tok123"},
	}

	nodes := []loader.TestNode{
		node("auth.login", nil, []string{"session.token"}),
		node("card.create", []string{"session.token"}, nil),
	}
	r, s, err := run(t, nodes, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if v, ok := s.Get("session.token"); !ok || v != "tok123" {
		t.Fatalf("session.token in store: got %q, present=%v", v, ok)
	}
	if got := f.contextFor("card.create")["session.token"]; got != "tok123" {
		t.Fatalf("card.create was not given the token, got %q", got)
	}
	if r.Results()["card.create"].Status != protocol.StatusPassed {
		t.Fatalf("card.create: %+v", r.Results()["card.create"])
	}
}

func TestRun_FailedNodeSkipsDependants(t *testing.T) {
	f := newFake()
	f.results["auth.login"] = executor.Result{Status: protocol.StatusFailed, Error: "login rejected"}

	nodes := []loader.TestNode{
		node("auth.login", nil, []string{"session.token"}),
		node("card.create", []string{"session.token"}, []string{"card.id"}),
		node("card.archive", []string{"card.id"}, nil),
	}
	r, _, err := run(t, nodes, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	results := r.Results()
	if len(results) != 3 {
		t.Fatalf("every node must be reported, got %d: %v", len(results), results)
	}
	if results["auth.login"].Status != protocol.StatusFailed {
		t.Errorf("auth.login: %+v", results["auth.login"])
	}
	for _, id := range []string{"card.create", "card.archive"} {
		if results[id].Status != protocol.StatusSkipped {
			t.Errorf("%s: want SKIPPED, got %+v", id, results[id])
		}
	}
}

// A transport failure is not a passing node: the runner must record it as FAILED
// and keep the agent's diagnostic.
func TestRun_ExecutorErrorFailsTheNode(t *testing.T) {
	f := newFake()
	f.errs["auth.login"] = fmt.Errorf("exit status 3: connection refused")

	r, _, err := run(t, []loader.TestNode{node("auth.login", nil, nil)}, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := r.Results()["auth.login"]
	if got.Status != protocol.StatusFailed {
		t.Fatalf("want FAILED, got %+v", got)
	}
	if !strings.Contains(got.Error, "connection refused") {
		t.Fatalf("error detail lost: %q", got.Error)
	}
}

func TestRun_RetriesUntilPass(t *testing.T) {
	f := newFake()
	f.failUntil["flaky"] = 3

	n := node("flaky", nil, nil)
	n.Policy.Retries = 2

	r, _, err := run(t, []loader.TestNode{n}, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := r.Results()["flaky"].Status; got != protocol.StatusPassed {
		t.Fatalf("want PASSED on the third attempt, got %q", got)
	}
	if got := f.attemptsFor("flaky"); got != 3 {
		t.Fatalf("want 3 attempts, got %d", got)
	}
}

func TestRun_RetriesExhausted(t *testing.T) {
	f := newFake()
	f.failUntil["flaky"] = 5 // more attempts than the policy allows

	n := node("flaky", nil, nil)
	n.Policy.Retries = 1

	r, _, err := run(t, []loader.TestNode{n}, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := r.Results()["flaky"].Status; got != protocol.StatusFailed {
		t.Fatalf("want FAILED, got %q", got)
	}
	if got := f.attemptsFor("flaky"); got != 2 {
		t.Fatalf("want 2 attempts (1 + 1 retry), got %d", got)
	}
}

// Every node reaching a terminal state is what keeps the report complete.
func TestRun_AllNodesReported(t *testing.T) {
	f := newFake()
	nodes := []loader.TestNode{
		node("a", nil, []string{"a.out"}),
		node("b", []string{"a.out"}, nil),
		node("c", nil, nil),
	}
	r, _, err := run(t, nodes, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Results()) != len(nodes) {
		t.Fatalf("want %d results, got %d", len(nodes), len(r.Results()))
	}
}
