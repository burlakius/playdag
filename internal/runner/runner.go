// Package runner orchestrates DAG execution by dispatching test nodes to an
// executor and wiring context through the shared store. It owns the execution
// policy — retries, timeouts, ordering — and knows nothing about what actually
// runs a test.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"playdag/internal/executor"
	"playdag/internal/graph"
	"playdag/internal/loader"
	"playdag/internal/protocol"
	"playdag/internal/scheduler"
	"playdag/internal/store"
)

// AgentResult is the outcome recorded for one node. Duration is measured by the
// runner and covers every attempt, so it is not part of the agent protocol.
type AgentResult struct {
	Status   string            `json:"status"` // "PASSED", "FAILED", or "SKIPPED"
	Provides map[string]string `json:"provides"`
	Error    string            `json:"error"`
	Duration time.Duration     `json:"duration_ns,omitempty"` // total node time including retries
}

// RunOptions configures runner behaviour.
type RunOptions struct {
	MaxConcurrency int                // 0 → 64 (effectively unlimited for test suites)
	JSONLog        bool               // emit one JSON object per log event instead of text
	Executor       executor.Executor  // runs a single node; required
	Agents         []*scheduler.Agent // explicit agent pool; empty → MaxConcurrency universal agents
}

// Runner drives the DAG to completion, launching each node as a subprocess.
type Runner struct {
	store   *store.ContextStore
	dag     *graph.DAG
	opts    RunOptions
	sched   *scheduler.Scheduler
	agents  int
	wg      sync.WaitGroup
	mu      sync.Mutex
	results map[string]AgentResult
	logMu   sync.Mutex
}

// NewRunner creates a Runner ready to execute.
func NewRunner(dag *graph.DAG, store *store.ContextStore, opts RunOptions) *Runner {
	agents := opts.Agents
	if len(agents) == 0 {
		agents = defaultAgents(dag.AllTags(), opts.MaxConcurrency)
	}
	return &Runner{
		dag:     dag,
		store:   store,
		opts:    opts,
		sched:   scheduler.New(agents, dag.CriticalPath()),
		agents:  len(agents),
		results: make(map[string]AgentResult),
	}
}

// defaultAgents builds n interchangeable agents that advertise every tag in the
// suite, so any node has a matching agent. n <= 0 falls back to 64.
func defaultAgents(tags []string, n int) []*scheduler.Agent {
	if n <= 0 {
		n = 64
	}
	agents := make([]*scheduler.Agent, n)
	for i := range agents {
		agents[i] = &scheduler.Agent{Name: fmt.Sprintf("agent-%d", i+1), Tags: tags}
	}
	return agents
}

// Results returns a copy of all collected AgentResults, keyed by test_id.
func (r *Runner) Results() map[string]AgentResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make(map[string]AgentResult, len(r.results))
	for k, v := range r.results {
		cp[k] = v
	}
	return cp
}

// nodeCompletion carries the outcome of a finished goroutine back to Run.
type nodeCompletion struct {
	node   *loader.TestNode
	agent  *scheduler.Agent
	result AgentResult
}

// Run drives the orchestration loop until every node reaches a terminal state
// or ctx is cancelled. Ready nodes are taken from the scheduler, so at a given
// concurrency the execution order is reproducible. It is safe to call once.
func (r *Runner) Run(ctx context.Context) error {
	// Buffer covers every agent so a finishing goroutine never blocks on send,
	// even while the main loop sits in wg.Wait during cancellation.
	done := make(chan nodeCompletion, r.agents)
	inFlight := 0

	for _, n := range r.dag.ReadyNodes() {
		r.sched.Push(n)
	}

	for {
		for {
			node, agent, ok := r.sched.Next()
			if !ok {
				break
			}
			r.dag.MarkRunning(node.TestID)
			inFlight++
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				result := r.runNode(ctx, node)
				done <- nodeCompletion{node: node, agent: agent, result: result}
			}()
		}

		if inFlight == 0 {
			if r.sched.Pending() == 0 {
				return nil
			}
			return fmt.Errorf("runner: %d node(s) cannot be scheduled on any agent (tags: %v)",
				r.sched.Pending(), r.sched.PendingTags())
		}

		select {
		case <-ctx.Done():
			r.wg.Wait()
			return ctx.Err()
		case c := <-done:
			inFlight--
			r.sched.Release(c.agent)
			r.mu.Lock()
			r.results[c.node.TestID] = c.result
			r.mu.Unlock()

			if c.result.Status == protocol.StatusPassed {
				for _, n := range r.dag.MarkPassed(c.node.TestID) {
					r.sched.Push(n)
				}
			} else {
				skipped := r.dag.MarkFailed(c.node.TestID)
				for _, sid := range skipped {
					r.log("SKIP", sid, "blocked by: "+c.node.TestID)
					r.mu.Lock()
					r.results[sid] = AgentResult{Status: protocol.StatusSkipped, Error: "blocked by: " + c.node.TestID}
					r.mu.Unlock()
				}
			}
		}
	}
}

// runNode executes a single node with retries and writes its provides into the
// store on success. It logs every attempt and the final outcome.
func (r *Runner) runNode(ctx context.Context, node *loader.TestNode) AgentResult {
	nodeStart := time.Now()
	maxAttempts := 1 + node.Policy.Retries
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var result AgentResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		r.log("RUN", node.TestID, fmt.Sprintf("attempt %d/%d", attempt, maxAttempts))
		attemptStart := time.Now()
		result = r.execNode(ctx, node, attempt)
		attemptElapsed := time.Since(attemptStart)

		if result.Status == protocol.StatusPassed {
			result.Duration = time.Since(nodeStart)
			r.log("PASS", node.TestID, fmt.Sprintf("%.2fs", attemptElapsed.Seconds()))
			for k, v := range result.Provides {
				r.store.Set(k, v, node.TestID, node.Contract.Scope)
			}
			return result
		}
	}

	result.Duration = time.Since(nodeStart)
	r.log("FAIL", node.TestID, result.Error)
	return result
}

// execNode resolves the node's required context and hands one attempt to the
// executor. A transport-level error becomes a FAILED result: from the DAG's
// point of view an agent that never reported is a node that did not pass.
func (r *Runner) execNode(ctx context.Context, node *loader.TestNode, attempt int) AgentResult {
	ctxData, err := r.store.Collect(node.Contract.Requires)
	if err != nil {
		return AgentResult{Status: protocol.StatusFailed, Error: err.Error()}
	}

	// Per-node timeout wraps the parent context when specified.
	execCtx := ctx
	if node.Policy.Timeout != "" {
		if d, err := time.ParseDuration(node.Policy.Timeout); err == nil {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}

	res, err := r.opts.Executor.Run(execCtx, node, executor.Input{Context: ctxData, Attempt: attempt})
	if err != nil {
		msg := err.Error()
		if execCtx.Err() == context.DeadlineExceeded {
			msg = "timeout exceeded"
		}
		return AgentResult{Status: protocol.StatusFailed, Error: msg}
	}

	return AgentResult{Status: res.Status, Provides: res.Provides, Error: res.Error}
}

// log emits a single execution event to stdout, either as a human-readable line
// or, when JSONLog is set, as one JSON object per line. The lock keeps lines
// from interleaving across concurrent node goroutines.
func (r *Runner) log(tag, testID, detail string) {
	r.logMu.Lock()
	defer r.logMu.Unlock()

	if r.opts.JSONLog {
		ev := struct {
			Time   string `json:"time"`
			Event  string `json:"event"`
			TestID string `json:"test_id"`
			Detail string `json:"detail,omitempty"`
		}{time.Now().Format(time.RFC3339), tag, testID, detail}
		if b, err := json.Marshal(ev); err == nil {
			fmt.Println(string(b))
		}
		return
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	// %-6s pads [RUN] (5 chars) to 6, matching [PASS]/[FAIL]/[SKIP] (6 chars) visually.
	fmt.Printf("[%s] %-6s %-20s %s\n", ts, "["+tag+"]", testID, detail)
}
