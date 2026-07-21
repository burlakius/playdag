package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"playdag/internal/loader"
	"playdag/internal/protocol"
)

// resultEnv names the file the reporter writes its protocol response to. It is a
// file rather than stdout because Playwright owns stdout for its own output.
const resultEnv = "PLAYDAG_RESULT"

// reporterModule is the reporter shipped by the npm adapter. The test project
// must have @playdag/playwright installed for Playwright to resolve it.
const reporterModule = "@playdag/playwright/reporter"

// outputTail caps how much Playwright output is quoted back when a run produces
// no response at all.
const outputTail = 800

// PlaywrightExecutor runs a node as a Playwright test, selecting it by the
// @id: tag the npm adapter attaches. Context travels in PLAYDAG_CONTEXT and the
// outcome comes back through the adapter's reporter, so this adapter is the only
// place in the orchestrator that knows Playwright exists.
type PlaywrightExecutor struct {
	baseDir string
	baseURL string
}

// NewPlaywright returns an executor that runs Playwright in baseDir, the
// directory holding the Playwright project, against baseURL.
func NewPlaywright(baseDir, baseURL string) *PlaywrightExecutor {
	return &PlaywrightExecutor{baseDir: baseDir, baseURL: baseURL}
}

// Run executes one attempt of node as a single Playwright test.
func (e *PlaywrightExecutor) Run(ctx context.Context, node *loader.TestNode, in Input) (Result, error) {
	payload, err := protocol.EncodeRequest(e.request(node, in))
	if err != nil {
		return Result{}, err
	}

	data, output, err := e.invoke(ctx, playwrightArgs([]*loader.TestNode{node}), payload)
	if err != nil {
		return Result{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return Result{}, fmt.Errorf("playwright: no response from the reporter for %q: %s", node.TestID, tail(output))
	}

	resp, err := protocol.DecodeResponse(data)
	if err != nil {
		return Result{}, err
	}
	return resultOf(resp), nil
}

// RunBatch executes one attempt of every node in a single Playwright process.
// This is where the framework startup — which dominates a short test — is paid
// once instead of once per node.
func (e *PlaywrightExecutor) RunBatch(ctx context.Context, nodes []*loader.TestNode, ins []Input) ([]Result, error) {
	reqs := make([]protocol.Request, len(nodes))
	ids := make([]string, len(nodes))
	for i, node := range nodes {
		reqs[i] = e.request(node, ins[i])
		ids[i] = node.TestID
	}

	payload, err := protocol.EncodeBatchRequest(reqs)
	if err != nil {
		return nil, err
	}

	data, output, err := e.invoke(ctx, playwrightArgs(nodes), payload)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("playwright: no response from the reporter for %v: %s", ids, tail(output))
	}

	responses, err := protocol.DecodeBatchResponse(data, ids)
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(nodes))
	for i, id := range ids {
		results[i] = resultOf(responses[id])
	}
	return results, nil
}

func (e *PlaywrightExecutor) request(node *loader.TestNode, in Input) protocol.Request {
	return protocol.Request{
		TestID:   node.TestID,
		Script:   node.Script,
		Attempt:  in.Attempt,
		BaseURL:  e.baseURL,
		Requires: node.Contract.Requires,
		Provides: node.Contract.Provides,
		Context:  in.Context,
	}
}

// invoke runs Playwright with the encoded request and returns what the reporter
// wrote, plus the process output for diagnostics. A failing test makes Playwright
// exit non-zero, which is a normal outcome here: the reporter still wrote a
// response. Only the absence of one is a transport failure, and that is the
// caller's to report, since it knows what it asked for.
func (e *PlaywrightExecutor) invoke(ctx context.Context, args []string, payload []byte) (data, output []byte, err error) {
	resultFile, err := os.CreateTemp("", "playdag-result-*.json")
	if err != nil {
		return nil, nil, fmt.Errorf("playwright: create result file: %w", err)
	}
	resultPath := resultFile.Name()
	if err := resultFile.Close(); err != nil {
		return nil, nil, fmt.Errorf("playwright: create result file: %w", err)
	}
	// The reporter writes this file; we only needed its name. Removing it is
	// best effort — the run has already produced its result by then.
	defer func() { _ = os.Remove(resultPath) }()

	cmd := exec.CommandContext(ctx, "npx", args...)
	killGroup(cmd)
	cmd.Dir = e.baseDir
	cmd.Env = append(os.Environ(),
		protocol.ContextEnv+"="+string(payload),
		resultEnv+"="+resultPath,
	)
	if e.baseURL != "" {
		cmd.Env = append(cmd.Env, "BASE_URL="+e.baseURL)
	}

	output, _ = cmd.CombinedOutput()
	data, _ = os.ReadFile(resultPath)
	return data, output, nil
}

func resultOf(resp protocol.Response) Result {
	return Result{
		Status:    resp.Status,
		Provides:  resp.Provides,
		Error:     resp.Error,
		Artifacts: resp.Artifacts,
	}
}

// playwrightArgs builds the npx invocation for a group of nodes. Retries are
// pinned off: the orchestrator owns the retry policy, and a Playwright-level
// retry would run a node twice behind its back. So is parallelism — one agent is
// one worker, and letting Playwright fan out inside a batch would make the
// orchestrator's own concurrency figure a fiction.
func playwrightArgs(nodes []*loader.TestNode) []string {
	args := []string{"playwright", "test"}
	for _, script := range scriptsOf(nodes) {
		args = append(args, script)
	}
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.TestID
	}
	return append(args, "--grep", grepFor(ids...), "--workers=1", "--retries=0", "--reporter="+reporterModule)
}

// scriptsOf lists the distinct scripts a group of nodes lives in, keeping the
// order they appear in so the command line stays reproducible.
func scriptsOf(nodes []*loader.TestNode) []string {
	var scripts []string
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.Script == "" || seen[n.Script] {
			continue
		}
		seen[n.Script] = true
		scripts = append(scripts, n.Script)
	}
	return scripts
}

// grepFor builds the regexp matching exactly the given nodes' @id: tags. Each id
// is quoted because a dotted id like card.create is otherwise a wildcard.
func grepFor(testIDs ...string) string {
	quoted := make([]string, len(testIDs))
	for i, id := range testIDs {
		quoted[i] = regexp.QuoteMeta("@id:" + id)
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return "(" + strings.Join(quoted, "|") + ")"
}

// tail returns the end of an agent's output, which is where the failure usually is.
func tail(out []byte) string {
	s := strings.TrimSpace(string(out))
	if len(s) > outputTail {
		return "..." + s[len(s)-outputTail:]
	}
	return s
}
