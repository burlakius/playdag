// Package executor runs a single test node on behalf of the runner. The core
// talks only to the Executor interface, which is what keeps the orchestrator
// independent of any test framework: a framework lives in an implementation
// here, never in the graph, the scheduler, or the runner.
package executor

import (
	"context"
	"fmt"

	"playdag/internal/loader"
	"playdag/internal/protocol"
)

// Input carries what an executor needs beyond the node definition itself.
type Input struct {
	Context map[string]string // resolved values for the node's required keys
	Attempt int               // 1-based attempt number
}

// Result is the outcome an agent reported for one attempt.
type Result struct {
	Status    string
	Provides  map[string]string
	Error     string // agent-reported failure detail
	Artifacts []protocol.Artifact
}

// Executor runs one node and reports what the agent said. A returned error means
// no usable result was obtained at all — the agent could not be started, was
// killed, or violated the protocol. A Result with StatusFailed means the agent
// ran and reported a failure, which is a normal outcome.
type Executor interface {
	Run(ctx context.Context, node *loader.TestNode, in Input) (Result, error)
}

// Names lists the executors New accepts, for CLI help and error messages.
var Names = []string{"process"}

// New builds the executor selected by name. baseDir resolves script paths and
// baseURL points at the system under test.
func New(name, baseDir, baseURL string) (Executor, error) {
	switch name {
	case "process":
		return NewProcess(baseDir, baseURL), nil
	default:
		return nil, fmt.Errorf("executor: unknown executor %q (want one of %v)", name, Names)
	}
}
