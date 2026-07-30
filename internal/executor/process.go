package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"playdag/internal/loader"
	"playdag/internal/protocol"
)

// stderrLimit caps how much of a failed agent's stderr is quoted back into the
// error, so one broken agent cannot flood the report with a stack trace.
const stderrLimit = 500

// ProcessExecutor runs a node as a subprocess that speaks the playdag protocol:
// the request arrives on stdin (and in PLAYDAG_CONTEXT), the response is read
// from stdout. It is the generic adapter - the agent may be written in any
// language.
type ProcessExecutor struct {
	baseDir string
	baseURL string
}

// NewProcess returns an executor that resolves scripts relative to baseDir and
// advertises baseURL to every agent.
func NewProcess(baseDir, baseURL string) *ProcessExecutor {
	return &ProcessExecutor{baseDir: baseDir, baseURL: baseURL}
}

// Run executes one attempt of node and returns the agent's reported result.
func (e *ProcessExecutor) Run(ctx context.Context, node *loader.TestNode, in Input) (Result, error) {
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

	argv := command(filepath.Join(e.baseDir, node.Script))
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	killGroup(cmd)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(os.Environ(), protocol.ContextEnv+"="+string(payload))
	if e.baseURL != "" {
		cmd.Env = append(cmd.Env, "BASE_URL="+e.baseURL)
	}

	output, err := cmd.Output()
	if err != nil {
		return Result{}, processError(err)
	}

	resp, err := protocol.DecodeResponse(output)
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

// command builds the argv for a script. Python scripts go through python3, which
// is what the existing agents expect; anything else is executed directly and is
// expected to speak the protocol itself.
func command(path string) []string {
	if filepath.Ext(path) == ".py" {
		return []string{"python3", path}
	}
	return []string{path}
}

// processError enriches a subprocess failure with whatever the agent said on
// stderr, which is usually the only clue about why it died.
func processError(err error) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return err
	}
	detail := strings.TrimSpace(string(exit.Stderr))
	if detail == "" {
		return err
	}
	if len(detail) > stderrLimit {
		detail = detail[:stderrLimit] + "..."
	}
	return fmt.Errorf("%w: %s", err, detail)
}
