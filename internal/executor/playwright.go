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
	payload, err := protocol.EncodeRequest(protocol.Request{
		TestID:   node.TestID,
		Script:   node.Script,
		Attempt:  in.Attempt,
		BaseURL:  e.baseURL,
		Requires: node.Contract.Requires,
		Provides: node.Contract.Provides,
		Context:  in.Context,
	})
	if err != nil {
		return Result{}, err
	}

	resultFile, err := os.CreateTemp("", "playdag-result-*.json")
	if err != nil {
		return Result{}, fmt.Errorf("playwright: create result file: %w", err)
	}
	resultPath := resultFile.Name()
	resultFile.Close()
	defer os.Remove(resultPath)

	cmd := exec.CommandContext(ctx, "npx", playwrightArgs(node)...)
	cmd.Dir = e.baseDir
	cmd.Env = append(os.Environ(),
		protocol.ContextEnv+"="+string(payload),
		resultEnv+"="+resultPath,
	)
	if e.baseURL != "" {
		cmd.Env = append(cmd.Env, "BASE_URL="+e.baseURL)
	}

	// A failing test makes Playwright exit non-zero, which is a normal outcome
	// here: the reporter still wrote a response. Only the absence of a response
	// is a transport failure.
	output, runErr := cmd.CombinedOutput()

	data, readErr := os.ReadFile(resultPath)
	if readErr != nil || len(strings.TrimSpace(string(data))) == 0 {
		return Result{}, fmt.Errorf("playwright: no response from the reporter for %q: %w: %s",
			node.TestID, runErr, tail(output))
	}

	resp, err := protocol.DecodeResponse(data)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Status:    resp.Status,
		Provides:  resp.Provides,
		Error:     resp.Error,
		Artifacts: resp.Artifacts,
	}, nil
}

// playwrightArgs builds the npx invocation for one node. Retries are pinned off:
// the orchestrator owns the retry policy, and a Playwright-level retry would run
// a node twice behind its back.
func playwrightArgs(node *loader.TestNode) []string {
	args := []string{"playwright", "test"}
	if node.Script != "" {
		args = append(args, node.Script)
	}
	return append(args, "--grep", grepFor(node.TestID), "--retries=0", "--reporter="+reporterModule)
}

// grepFor builds the regexp matching exactly the node's @id: tag. The id is
// quoted because a dotted id like card.create is otherwise a wildcard.
func grepFor(testID string) string {
	return regexp.QuoteMeta("@id:" + testID)
}

// tail returns the end of an agent's output, which is where the failure usually is.
func tail(out []byte) string {
	s := strings.TrimSpace(string(out))
	if len(s) > outputTail {
		return "..." + s[len(s)-outputTail:]
	}
	return s
}
