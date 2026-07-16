// Integration tests with a real subprocess are in runner_integration_test.go
// with build tag //go:build integration
package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

// blockingExecutor lets a test control exactly when a node finishes, so the
// fail-fast path can be observed while other nodes are genuinely in flight.
type blockingExecutor struct {
	started chan string
	release chan struct{}
	mu      sync.Mutex
	results map[string]executor.Result
	ctxErr  map[string]error // test_id → the context error the node saw
}

func newBlocking() *blockingExecutor {
	return &blockingExecutor{
		started: make(chan string, 16),
		release: make(chan struct{}),
		results: make(map[string]executor.Result),
		ctxErr:  make(map[string]error),
	}
}

func (b *blockingExecutor) Run(ctx context.Context, node *loader.TestNode, in executor.Input) (executor.Result, error) {
	b.mu.Lock()
	res, canned := b.results[node.TestID]
	b.mu.Unlock()

	b.started <- node.TestID
	if canned {
		return res, nil
	}

	// No canned result: block until the test releases us or the run cancels.
	select {
	case <-b.release:
		return executor.Result{Status: protocol.StatusPassed}, nil
	case <-ctx.Done():
		b.mu.Lock()
		b.ctxErr[node.TestID] = ctx.Err()
		b.mu.Unlock()
		return executor.Result{}, ctx.Err()
	}
}

func (b *blockingExecutor) sawCancel(testID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ctxErr[testID] != nil
}

// The point of fail-fast: a node already running must be torn down, not left to
// finish work whose result the run has decided it does not want.
func TestRun_FailFastCancelsNodesInFlight(t *testing.T) {
	b := newBlocking()
	b.results["boom"] = executor.Result{Status: protocol.StatusFailed, Error: "blocker failed"}

	nodes := []loader.TestNode{
		node("boom", nil, nil),
		node("slow", nil, nil), // independent: nothing links it to boom
	}
	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, store.NewContextStore(""), RunOptions{
		MaxConcurrency: 2,
		Executor:       b,
		FailFast:       true,
	})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !b.sawCancel("slow") {
		t.Fatal("the in-flight node was never cancelled")
	}
	res := r.Results()["slow"]
	if res.Status != protocol.StatusSkipped {
		t.Fatalf("a cancelled node must not be reported as failed: %+v", res)
	}
	if !strings.Contains(res.Error, "fail-fast") {
		t.Errorf("the reason should say why: %q", res.Error)
	}
	if r.Results()["boom"].Status != protocol.StatusFailed {
		t.Errorf("boom should be FAILED: %+v", r.Results()["boom"])
	}
}

// A NON_CRITICAL failure is expected to be survivable; stopping on it would
// make the label meaningless.
func TestRun_FailFastIgnoresNonCriticalFailure(t *testing.T) {
	f := newFake()
	f.results["flaky"] = executor.Result{Status: protocol.StatusFailed, Error: "meh"}

	nonCritical := node("flaky", nil, nil)
	nonCritical.Policy.Criticality = "NON_CRITICAL"
	nodes := []loader.TestNode{nonCritical, node("other", nil, nil)}

	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, store.NewContextStore(""), RunOptions{
		MaxConcurrency: 1,
		Executor:       f,
		FailFast:       true,
	})
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := r.Results()["other"].Status; got != protocol.StatusPassed {
		t.Fatalf("the run should have carried on, got %q for other", got)
	}
}

func TestRun_MaxFailuresStopsTheRun(t *testing.T) {
	f := newFake()
	for _, id := range []string{"a", "b", "c", "d"} {
		f.results[id] = executor.Result{Status: protocol.StatusFailed, Error: "no"}
	}
	nodes := []loader.TestNode{node("a", nil, nil), node("b", nil, nil), node("c", nil, nil), node("d", nil, nil)}

	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, store.NewContextStore(""), RunOptions{
		MaxConcurrency: 1, // one at a time, so the threshold is exact
		Executor:       f,
		MaxFailures:    2,
	})
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	results := r.Results()
	if len(results) != 4 {
		t.Fatalf("every node must still be reported, got %d", len(results))
	}
	failed := 0
	for _, res := range results {
		if res.Status == protocol.StatusFailed {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("the run should have stopped at 2 failures, got %d", failed)
	}
}

// Without fail-fast the whole suite still runs, which is what makes fail-fast a
// choice rather than the only behaviour.
func TestRun_NoFailFastRunsEverything(t *testing.T) {
	f := newFake()
	f.results["boom"] = executor.Result{Status: protocol.StatusFailed, Error: "blocker failed"}
	nodes := []loader.TestNode{node("boom", nil, nil), node("other", nil, nil)}

	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, store.NewContextStore(""), RunOptions{MaxConcurrency: 1, Executor: f})
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := r.Results()["other"].Status; got != protocol.StatusPassed {
		t.Fatalf("other should have run, got %q", got)
	}
}

func TestRetryDelay_FixedAndExponential(t *testing.T) {
	fixed := loader.ExecutionPolicy{RetryDelay: "2s"}
	for attempt := 1; attempt <= 3; attempt++ {
		if got := retryDelay(fixed, attempt); got != 2*time.Second {
			t.Errorf("fixed delay attempt %d: want 2s, got %s", attempt, got)
		}
	}

	exp := loader.ExecutionPolicy{RetryDelay: "1s", Backoff: "exponential"}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	for i, w := range want {
		if got := retryDelay(exp, i+1); got != w {
			t.Errorf("exponential delay attempt %d: want %s, got %s", i+1, w, got)
		}
	}
}

func TestRetryDelay_UnsetIsImmediate(t *testing.T) {
	if got := retryDelay(loader.ExecutionPolicy{}, 1); got != 0 {
		t.Fatalf("want no delay, got %s", got)
	}
}

// The delay must not outlive the run: a suite that stopped should not sit
// waiting to retry a node it has already abandoned.
func TestRun_RetryDelayIsCancellable(t *testing.T) {
	f := newFake()
	f.failUntil["slow.retry"] = 99 // never passes, so it always waits to retry

	n := node("slow.retry", nil, nil)
	n.Policy.Retries = 5
	n.Policy.RetryDelay = "30s"

	dag, err := graph.NewDAG([]loader.TestNode{n})
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, store.NewContextStore(""), RunOptions{MaxConcurrency: 1, Executor: f})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Let the first attempt fail and the wait begin.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if err := r.Run(ctx); err == nil {
		t.Fatal("Run should report the cancellation")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the retry delay ignored cancellation: took %s", elapsed)
	}
}

// A node that only passed on a retry is still green, but the run should say it
// needed a second roll of the dice.
func TestRun_PassOnRetryIsFlaky(t *testing.T) {
	f := newFake()
	f.failUntil["flaky"] = 2

	n := node("flaky", nil, nil)
	n.Policy.Retries = 2

	r, _, err := run(t, []loader.TestNode{n}, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := r.Results()["flaky"]
	if got.Status != protocol.StatusPassed {
		t.Fatalf("want PASSED, got %q", got.Status)
	}
	if !got.Flaky {
		t.Error("a node that passed on attempt 2 must be marked flaky")
	}
	if got.Attempts != 2 {
		t.Errorf("attempts: want 2, got %d", got.Attempts)
	}
}

func TestRun_FirstAttemptPassIsNotFlaky(t *testing.T) {
	f := newFake()
	r, _, err := run(t, []loader.TestNode{node("solid", nil, nil)}, f)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := r.Results()["solid"]
	if got.Flaky {
		t.Error("a node that passed first time must not be marked flaky")
	}
	if got.Attempts != 1 {
		t.Errorf("attempts: want 1, got %d", got.Attempts)
	}
}

// A chain built on a session-scoped value must still run end to end. (That the
// dependants are pinned to the owning agent is decided by affinityFor and the
// scheduler, and is tested there: this chain is serial, so it would land on one
// agent either way.)
func TestRun_SessionScopedChainPasses(t *testing.T) {
	f := newFake()
	f.results["auth.login"] = executor.Result{
		Status:   protocol.StatusPassed,
		Provides: map[string]string{"session.token": "tok"},
	}
	f.results["card.create"] = executor.Result{
		Status:   protocol.StatusPassed,
		Provides: map[string]string{"card.id": "42"},
	}

	login := node("auth.login", nil, []string{"session.token"})
	login.Contract.Scope = "session"
	create := node("card.create", []string{"session.token"}, []string{"card.id"})
	archive := node("card.archive", []string{"card.id", "session.token"}, nil)

	nodes := []loader.TestNode{login, create, archive}
	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	s := store.NewContextStore("")
	r := NewRunner(dag, s, RunOptions{MaxConcurrency: 4, Executor: f})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, id := range []string{"auth.login", "card.create", "card.archive"} {
		if got := r.Results()[id].Status; got != protocol.StatusPassed {
			t.Fatalf("%s: want PASSED, got %q (%s)", id, got, r.Results()[id].Error)
		}
	}
	// The token reached the dependants, which can only happen on the owning agent.
	if got := f.contextFor("card.create")["session.token"]; got != "tok" {
		t.Errorf("card.create did not get the session token: %q", got)
	}
	if got := f.contextFor("card.archive")["session.token"]; got != "tok" {
		t.Errorf("card.archive did not get the session token: %q", got)
	}
}

// A suite-scoped value pins nothing, so the same graph is free to spread out.
func TestRun_SuiteScopeDoesNotPin(t *testing.T) {
	f := newFake()
	f.results["auth.login"] = executor.Result{
		Status:   protocol.StatusPassed,
		Provides: map[string]string{"session.token": "tok"},
	}
	login := node("auth.login", nil, []string{"session.token"})
	login.Contract.Scope = "suite"

	nodes := []loader.TestNode{login, node("a", []string{"session.token"}, nil), node("b", []string{"session.token"}, nil)}
	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, store.NewContextStore(""), RunOptions{MaxConcurrency: 4, Executor: f})
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		if got := r.Results()[id].Status; got != protocol.StatusPassed {
			t.Fatalf("%s: %q %s", id, got, r.Results()[id].Error)
		}
	}
}

func TestAffinityFor_SessionKeyPinsToItsAgent(t *testing.T) {
	s := store.NewContextStore("")
	s.Set("session.token", "tok", "auth.login", "session", "agent-2")

	dag, err := graph.NewDAG([]loader.TestNode{node("a", nil, nil)})
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, s, RunOptions{MaxConcurrency: 2, Executor: newFake()})

	n := node("card.create", []string{"session.token"}, nil)
	got, err := r.affinityFor(&n)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "agent-2" {
		t.Fatalf("want agent-2, got %q", got)
	}
}

func TestAffinityFor_SuiteKeyPinsNothing(t *testing.T) {
	s := store.NewContextStore("")
	s.Set("card.id", "42", "card.create", "suite", "agent-1")

	dag, err := graph.NewDAG([]loader.TestNode{node("a", nil, nil)})
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, s, RunOptions{MaxConcurrency: 2, Executor: newFake()})

	n := node("card.move", []string{"card.id"}, nil)
	got, err := r.affinityFor(&n)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("a suite-scoped key must not pin the node, got %q", got)
	}
}

// A node needing sessions from two agents can run nowhere. Saying that plainly
// beats letting it fail later with a missing key it could never have had.
func TestAffinityFor_TwoSessionsIsAnError(t *testing.T) {
	s := store.NewContextStore("")
	s.Set("a.token", "1", "a", "session", "agent-1")
	s.Set("b.token", "2", "b", "session", "agent-2")

	dag, err := graph.NewDAG([]loader.TestNode{node("x", nil, nil)})
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, s, RunOptions{MaxConcurrency: 2, Executor: newFake()})

	n := node("needs.both", []string{"a.token", "b.token"}, nil)
	if _, err := r.affinityFor(&n); err == nil {
		t.Fatal("expected an error for keys from two agents")
	}
}

// The literal saving the global scope exists for: work already done by an
// earlier run is not done again.
func TestRun_GlobalValuesFromAPreviousRunSkipTheNode(t *testing.T) {
	f := newFake()
	s := store.NewContextStore("")
	// Stand in for a previous run having registered the account.
	s.Set("account.id", "acct-777", "account.register", store.ScopeGlobal, "")

	register := node("account.register", nil, []string{"account.id"})
	register.Contract.Scope = store.ScopeGlobal
	use := node("account.use", []string{"account.id"}, nil)

	dag, err := graph.NewDAG([]loader.TestNode{register, use})
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, s, RunOptions{MaxConcurrency: 2, Executor: f})
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := r.Results()["account.register"]
	if !got.Cached {
		t.Error("the node should have been served from the previous run")
	}
	if f.attemptsFor("account.register") != 0 {
		t.Error("the cached node must not have been executed")
	}
	// Its dependant still runs, and with the carried-over value.
	if r.Results()["account.use"].Status != protocol.StatusPassed {
		t.Fatalf("account.use: %+v", r.Results()["account.use"])
	}
	if f.contextFor("account.use")["account.id"] != "acct-777" {
		t.Error("the dependant did not receive the carried-over value")
	}
}

// Only global scope caches: a suite-scoped node runs every time, even if some
// other run left a value of the same name lying around.
func TestRun_SuiteScopedNodeIsNotCached(t *testing.T) {
	f := newFake()
	f.results["card.create"] = executor.Result{
		Status:   protocol.StatusPassed,
		Provides: map[string]string{"card.id": "42"},
	}
	s := store.NewContextStore("")
	s.Set("card.id", "stale", "card.create", store.ScopeSuite, "")

	create := node("card.create", nil, []string{"card.id"})
	create.Contract.Scope = store.ScopeSuite

	dag, err := graph.NewDAG([]loader.TestNode{create})
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, s, RunOptions{MaxConcurrency: 1, Executor: f})
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if r.Results()["card.create"].Cached {
		t.Fatal("a suite-scoped node must not be served from the store")
	}
	if f.attemptsFor("card.create") != 1 {
		t.Fatal("the node should have run")
	}
}
