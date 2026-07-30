package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"playdag/internal/loader"
	"playdag/internal/protocol"
)

// PytestExecutor runs a node as a pytest test, selected by its node id - a
// pytest node id is already unique, so unlike Playwright this needs no grep
// tag. Context travels in PLAYDAG_CONTEXT and the outcome comes back through
// the playdag-pytest plugin's report hook, so this adapter is the only place
// in the orchestrator that knows pytest exists.
type PytestExecutor struct {
	baseDir string
	baseURL string
}

// NewPytest returns an executor that runs pytest in baseDir, the directory
// holding the pytest project, against baseURL.
func NewPytest(baseDir, baseURL string) *PytestExecutor {
	return &PytestExecutor{baseDir: baseDir, baseURL: baseURL}
}

// Run executes one attempt of node as a single pytest test.
func (e *PytestExecutor) Run(ctx context.Context, node *loader.TestNode, in Input) (Result, error) {
	payload, err := protocol.EncodeRequest(e.request(node, in))
	if err != nil {
		return Result{}, err
	}

	data, output, err := e.invoke(ctx, []string{node.Script}, payload)
	if err != nil {
		return Result{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return Result{}, fmt.Errorf("pytest: no response from the plugin for %q: %s", node.TestID, tail(output))
	}

	resp, err := protocol.DecodeResponse(data)
	if err != nil {
		return Result{}, err
	}
	return resultOf(resp), nil
}

// RunBatch executes one attempt of every node in a single pytest process. This
// is where the interpreter and collection startup - which dominates a short
// test - is paid once instead of once per node.
func (e *PytestExecutor) RunBatch(ctx context.Context, nodes []*loader.TestNode, ins []Input) ([]Result, error) {
	reqs := make([]protocol.Request, len(nodes))
	ids := make([]string, len(nodes))
	nodeIDs := make([]string, len(nodes))
	for i, node := range nodes {
		reqs[i] = e.request(node, ins[i])
		ids[i] = node.TestID
		nodeIDs[i] = node.Script
	}

	payload, err := protocol.EncodeBatchRequest(reqs)
	if err != nil {
		return nil, err
	}

	data, output, err := e.invoke(ctx, nodeIDs, payload)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("pytest: no response from the plugin for %v: %s", ids, tail(output))
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

func (e *PytestExecutor) request(node *loader.TestNode, in Input) protocol.Request {
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

// invoke runs pytest against the given node ids and returns what the plugin
// wrote, plus the process output for diagnostics. A failing test makes pytest
// exit non-zero, which is a normal outcome here: the plugin still wrote a
// response. Only the absence of one is a transport failure, and that is the
// caller's to report, since it knows what it asked for.
func (e *PytestExecutor) invoke(ctx context.Context, nodeIDs []string, payload []byte) (data, output []byte, err error) {
	resultFile, err := os.CreateTemp("", "playdag-pytest-result-*.json")
	if err != nil {
		return nil, nil, fmt.Errorf("pytest: create result file: %w", err)
	}
	resultPath := resultFile.Name()
	if err := resultFile.Close(); err != nil {
		return nil, nil, fmt.Errorf("pytest: create result file: %w", err)
	}
	defer func() { _ = os.Remove(resultPath) }()

	args := append([]string{"-m", "pytest", "-q"}, nodeIDs...)
	cmd := exec.CommandContext(ctx, "python3", args...)
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
