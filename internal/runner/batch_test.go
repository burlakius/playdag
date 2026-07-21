package runner

import (
	"context"
	"errors"
	"sync"
	"testing"

	"playdag/internal/executor"
	"playdag/internal/graph"
	"playdag/internal/loader"
	"playdag/internal/protocol"
	"playdag/internal/store"
)

// batchFake is a fakeExecutor that also implements executor.Batcher, recording
// which nodes were handed to it together so a test can see what shared a process.
type batchFake struct {
	*fakeExecutor
	mu     sync.Mutex
	groups [][]string
	err    error
}

func newBatchFake() *batchFake {
	return &batchFake{fakeExecutor: newFake()}
}

func (b *batchFake) RunBatch(ctx context.Context, nodes []*loader.TestNode, ins []executor.Input) ([]executor.Result, error) {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.TestID
	}
	b.mu.Lock()
	b.groups = append(b.groups, ids)
	err := b.err
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}

	results := make([]executor.Result, len(nodes))
	for i, n := range nodes {
		res, err := b.fakeExecutor.Run(ctx, n, ins[i])
		if err != nil {
			return nil, err
		}
		results[i] = res
	}
	return results, nil
}

func (b *batchFake) batches() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.groups
}

func runBatched(t *testing.T, nodes []loader.TestNode, exec executor.Executor, size, workers int) (*Runner, error) {
	t.Helper()
	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}
	r := NewRunner(dag, store.NewContextStore(""), RunOptions{
		MaxConcurrency: workers,
		Executor:       exec,
		BatchSize:      size,
	})
	return r, r.Run(context.Background())
}

// Independent nodes that become ready together are what batching is for: one
// process instead of four.
func TestRun_ReadyNodesShareOneProcess(t *testing.T) {
	f := newBatchFake()
	nodes := []loader.TestNode{
		node("a", nil, nil), node("b", nil, nil), node("c", nil, nil), node("d", nil, nil),
	}

	r, err := runBatched(t, nodes, f, 4, 1)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.batches(); len(got) != 1 || len(got[0]) != 4 {
		t.Fatalf("want one batch of four, got %v", got)
	}
	for id, res := range r.Results() {
		if res.Status != protocol.StatusPassed {
			t.Errorf("%s: %+v", id, res)
		}
		if res.Batch != 4 {
			t.Errorf("%s: batch = %d, want 4", id, res.Batch)
		}
	}
}

func TestRun_BatchSizeIsRespected(t *testing.T) {
	f := newBatchFake()
	nodes := []loader.TestNode{
		node("a", nil, nil), node("b", nil, nil), node("c", nil, nil),
		node("d", nil, nil), node("e", nil, nil),
	}

	if _, err := runBatched(t, nodes, f, 2, 1); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, g := range f.batches() {
		if len(g) > 2 {
			t.Fatalf("batch of %d exceeds the limit of 2: %v", len(g), f.batches())
		}
	}
}

// The whole design rests on this: a node may not share a process with something
// it depends on, because it has to see that node's values before it starts.
func TestRun_DependentNodesAreNeverBatchedTogether(t *testing.T) {
	f := newBatchFake()
	f.results["auth.login"] = executor.Result{
		Status:   protocol.StatusPassed,
		Provides: map[string]string{"session.token": "tok"},
	}
	nodes := []loader.TestNode{
		node("auth.login", nil, []string{"session.token"}),
		node("card.create", []string{"session.token"}, nil),
		node("card.list", []string{"session.token"}, nil),
	}

	r, err := runBatched(t, nodes, f, 8, 1)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, g := range f.batches() {
		if len(g) > 1 && contains(g, "auth.login") {
			t.Fatalf("login shared a process with what depends on it: %v", g)
		}
	}
	if got := f.contextFor("card.create")["session.token"]; got != "tok" {
		t.Errorf("card.create context = %q, want the token from the earlier node", got)
	}
	if r.Results()["card.list"].Status != protocol.StatusPassed {
		t.Errorf("card.list: %+v", r.Results()["card.list"])
	}
}

// A retry belongs to the node, not to whoever it happened to be batched with,
// so it runs on its own.
func TestRun_FailureInABatchIsRetriedAlone(t *testing.T) {
	f := newBatchFake()
	f.failUntil["b"] = 2

	nodes := []loader.TestNode{node("a", nil, nil), node("b", nil, nil)}
	nodes[1].Policy.Retries = 1

	r, err := runBatched(t, nodes, f, 4, 1)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := r.Results()["b"]
	if res.Status != protocol.StatusPassed || !res.Flaky {
		t.Fatalf("b: %+v, want a flaky pass", res)
	}
	if got := f.attemptsFor("b"); got != 2 {
		t.Errorf("attempts = %d, want the retry to have run", got)
	}
	if len(f.batches()) != 1 {
		t.Errorf("the retry went through the batch path: %v", f.batches())
	}
}

// The nodes shared a process, so a failure to start it belongs to all of them.
func TestRun_BatchTransportErrorFailsEveryNodeInIt(t *testing.T) {
	f := newBatchFake()
	f.err = errors.New("npx: command not found")

	nodes := []loader.TestNode{node("a", nil, nil), node("b", nil, nil)}
	r, err := runBatched(t, nodes, f, 4, 1)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"a", "b"} {
		res := r.Results()[id]
		if res.Status != protocol.StatusFailed || res.Error != "npx: command not found" {
			t.Errorf("%s: %+v", id, res)
		}
	}
}

// An executor that cannot batch is not asked to; the run is the same as before.
func TestRun_ExecutorWithoutBatchingStillRuns(t *testing.T) {
	f := newFake()
	nodes := []loader.TestNode{node("a", nil, nil), node("b", nil, nil), node("c", nil, nil)}

	r, err := runBatched(t, nodes, f, 4, 2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Results()) != 3 {
		t.Fatalf("got %d results, want 3", len(r.Results()))
	}
	for id, res := range r.Results() {
		if res.Status != protocol.StatusPassed {
			t.Errorf("%s: %+v", id, res)
		}
		if res.Batch != 0 {
			t.Errorf("%s: batch = %d, want it recorded as having run alone", id, res.Batch)
		}
	}
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}
