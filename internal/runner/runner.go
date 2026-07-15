// Package runner orchestrates DAG execution by dispatching test nodes as
// python subprocesses and wiring context through the shared store.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"playdag/internal/graph"
	"playdag/internal/loader"
	"playdag/internal/store"
)

// AgentResult is the JSON payload returned by a python agent on stdout.
// Duration is populated by the runner after execution (not part of the agent protocol).
type AgentResult struct {
	Status   string            `json:"status"`              // "PASSED", "FAILED", or "SKIPPED"
	Provides map[string]string `json:"provides"`
	Error    string            `json:"error"`
	Duration time.Duration     `json:"duration_ns,omitempty"` // total node time including retries
}

// RunOptions configures runner behaviour.
type RunOptions struct {
	MaxConcurrency int    // 0 → 64 (effectively unlimited for test suites)
	BaseDir        string // working directory for python scripts
	BaseURL        string // passed as BASE_URL env var to agents
}

// Runner drives the DAG to completion, launching each node as a subprocess.
type Runner struct {
	store   *store.ContextStore
	dag     *graph.DAG
	opts    RunOptions
	sem     chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	results map[string]AgentResult
}

// NewRunner creates a Runner ready to execute.
func NewRunner(dag *graph.DAG, store *store.ContextStore, opts RunOptions) *Runner {
	concurrency := opts.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 64
	}
	return &Runner{
		dag:     dag,
		store:   store,
		opts:    opts,
		sem:     make(chan struct{}, concurrency),
		results: make(map[string]AgentResult),
	}
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
	result AgentResult
}

// Run drives the orchestration loop until every node reaches a terminal state
// or ctx is cancelled. It is safe to call once per Runner.
func (r *Runner) Run(ctx context.Context) error {
	// Buffer = semaphore capacity: at most that many goroutines can finish and
	// signal simultaneously before the main loop drains them.
	done := make(chan nodeCompletion, cap(r.sem))
	inFlight := 0

	dispatch := func(nodes []*loader.TestNode) {
		for _, n := range nodes {
			r.dag.MarkRunning(n.TestID)
			inFlight++
			r.wg.Add(1)
			n := n
			go func() {
				defer r.wg.Done()
				r.sem <- struct{}{}        // acquire slot
				result := r.runNode(ctx, n)
				<-r.sem                    // release slot before notifying
				done <- nodeCompletion{node: n, result: result}
			}()
		}
	}

	dispatch(r.dag.ReadyNodes())

	for inFlight > 0 {
		select {
		case <-ctx.Done():
			r.wg.Wait()
			return ctx.Err()
		case c := <-done:
			inFlight--
			r.mu.Lock()
			r.results[c.node.TestID] = c.result
			r.mu.Unlock()

			if c.result.Status == "PASSED" {
				dispatch(r.dag.MarkPassed(c.node.TestID))
			} else {
				skipped := r.dag.MarkFailed(c.node.TestID)
				for _, sid := range skipped {
					logLine("SKIP", sid, "blocked by: "+c.node.TestID)
					r.mu.Lock()
					r.results[sid] = AgentResult{Status: "SKIPPED", Error: "blocked by: " + c.node.TestID}
					r.mu.Unlock()
				}
			}
		}
	}

	return nil
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
		logLine("RUN", node.TestID, fmt.Sprintf("attempt %d/%d", attempt, maxAttempts))
		attemptStart := time.Now()
		result = r.execNode(ctx, node)
		attemptElapsed := time.Since(attemptStart)

		if result.Status == "PASSED" {
			result.Duration = time.Since(nodeStart)
			logLine("PASS", node.TestID, fmt.Sprintf("%.2fs", attemptElapsed.Seconds()))
			for k, v := range result.Provides {
				r.store.Set(k, v, node.TestID, node.Contract.Scope) //nolint:errcheck
			}
			return result
		}
	}

	result.Duration = time.Since(nodeStart)
	logLine("FAIL", node.TestID, result.Error)
	return result
}

// execNode runs the python subprocess for one attempt and returns the parsed result.
func (r *Runner) execNode(ctx context.Context, node *loader.TestNode) AgentResult {
	ctxData, err := r.store.Collect(node.Contract.Requires)
	if err != nil {
		return AgentResult{Status: "FAILED", Error: err.Error()}
	}

	inputJSON, err := json.Marshal(ctxData)
	if err != nil {
		return AgentResult{Status: "FAILED", Error: "marshal context: " + err.Error()}
	}

	// Per-node timeout wraps the parent context when specified.
	cmdCtx := ctx
	if node.Policy.Timeout != "" {
		if d, err := time.ParseDuration(node.Policy.Timeout); err == nil {
			var cancel context.CancelFunc
			cmdCtx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}

	scriptPath := filepath.Join(r.opts.BaseDir, node.Script)
	cmd := exec.CommandContext(cmdCtx, "python3", scriptPath)
	cmd.Stdin = bytes.NewReader(inputJSON)

	env := os.Environ()
	if r.opts.BaseURL != "" {
		env = append(env, "BASE_URL="+r.opts.BaseURL)
	}
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if cmdCtx.Err() == context.DeadlineExceeded {
			msg = "timeout exceeded"
		}
		return AgentResult{Status: "FAILED", Error: msg}
	}

	return ParseAgentResult(output)
}

// ParseAgentResult parses the JSON stdout of an agent process.
// On any failure (empty, bad JSON, missing status) it returns a FAILED result
// with a descriptive Error field. Exported for unit testing.
func ParseAgentResult(output []byte) AgentResult {
	if len(bytes.TrimSpace(output)) == 0 {
		return AgentResult{Status: "FAILED", Error: "agent produced no output"}
	}

	result, err := parseAgentResult(output)
	if err != nil {
		return AgentResult{Status: "FAILED", Error: "invalid JSON: " + err.Error()}
	}

	if result.Status == "" {
		return AgentResult{Status: "FAILED", Error: "missing status field in agent output"}
	}

	return result
}

// parseAgentResult unmarshals agent JSON, coercing all Provides values to strings
// regardless of their JSON type (string, number, bool).
func parseAgentResult(output []byte) (AgentResult, error) {
	var raw struct {
		Status   string                     `json:"status"`
		Provides map[string]json.RawMessage `json:"provides"`
		Error    string                     `json:"error"`
		Duration time.Duration              `json:"duration_ns,omitempty"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return AgentResult{}, err
	}

	provides := make(map[string]string, len(raw.Provides))
	for k, v := range raw.Provides {
		if len(v) > 0 && v[0] == '"' {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return AgentResult{}, err
			}
			provides[k] = s
		} else {
			provides[k] = string(v)
		}
	}

	return AgentResult{
		Status:   raw.Status,
		Provides: provides,
		Error:    raw.Error,
		Duration: raw.Duration,
	}, nil
}

// logLine emits a single structured log line to stdout.
func logLine(tag, testID, detail string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	// %-6s pads [RUN] (5 chars) to 6, matching [PASS]/[FAIL]/[SKIP] (6 chars) visually.
	fmt.Printf("[%s] %-6s %-20s %s\n", ts, "["+tag+"]", testID, detail)
}
