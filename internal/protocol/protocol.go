// Package protocol defines the wire contract between the orchestrator and a
// test agent. The contract is deliberately language-agnostic: an agent is any
// program that reads a JSON request (stdin or the PLAYDAG_CONTEXT environment
// variable) and writes a JSON response to stdout. Nothing here knows about a
// particular test framework. See docs/PROTOCOL.md for the specification.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Version is the schema_version this orchestrator emits. A response may omit
// schema_version, which identifies an agent written before the envelope existed.
const Version = "1.0"

// ContextEnv carries the encoded request for agents that cannot read stdin,
// such as a test framework launched through its own CLI.
const ContextEnv = "PLAYDAG_CONTEXT"

// Node statuses reported by an agent.
const (
	StatusPassed  = "PASSED"
	StatusFailed  = "FAILED"
	StatusSkipped = "SKIPPED"
)

// Request is what the orchestrator hands to an agent before it runs a node.
type Request struct {
	SchemaVersion string            `json:"schema_version"`
	TestID        string            `json:"test_id"`
	Script        string            `json:"script"`
	Attempt       int               `json:"attempt"`
	BaseURL       string            `json:"base_url,omitempty"`
	Requires      []string          `json:"requires,omitempty"`
	Provides      []string          `json:"provides,omitempty"`
	Context       map[string]string `json:"context,omitempty"`
}

// Response is what an agent writes to stdout once the node finishes.
type Response struct {
	SchemaVersion string            `json:"schema_version,omitempty"`
	Status        string            `json:"status"`
	Provides      map[string]string `json:"provides,omitempty"`
	Error         string            `json:"error,omitempty"`
	Artifacts     []Artifact        `json:"artifacts,omitempty"`
}

// Artifact points at a file the agent produced for a node: a trace, a video, a
// screenshot, a log.
type Artifact struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// envelopeFields are the request field names a context key may not shadow,
// because the flat mirror below writes context keys into the same object.
var envelopeFields = map[string]bool{
	"schema_version": true,
	"test_id":        true,
	"script":         true,
	"attempt":        true,
	"base_url":       true,
	"requires":       true,
	"provides":       true,
	"context":        true,
}

// EncodeRequest serialises req for stdin or ContextEnv. Every context entry is
// mirrored at the top level of the object alongside the envelope, so agents that
// predate the envelope and read the flat context map directly keep working.
func EncodeRequest(req Request) ([]byte, error) {
	req.SchemaVersion = Version

	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("protocol: encode request: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("protocol: encode request: %w", err)
	}

	for k, v := range req.Context {
		if envelopeFields[k] {
			return nil, fmt.Errorf("protocol: context key %q collides with a protocol field", k)
		}
		val, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("protocol: encode context key %q: %w", k, err)
		}
		obj[k] = val
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("protocol: encode request: %w", err)
	}
	return out, nil
}

// DecodeResponse parses an agent's stdout. It accepts both a versioned response
// and a bare {"status","provides"} object from a pre-envelope agent. An error
// means the output cannot be trusted as a result at all.
func DecodeResponse(data []byte) (Response, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Response{}, fmt.Errorf("protocol: agent produced no output")
	}

	var raw struct {
		SchemaVersion string                     `json:"schema_version"`
		Status        string                     `json:"status"`
		Provides      map[string]json.RawMessage `json:"provides"`
		Error         string                     `json:"error"`
		Artifacts     []Artifact                 `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Response{}, fmt.Errorf("protocol: invalid JSON: %w", err)
	}

	if raw.SchemaVersion != "" && raw.SchemaVersion != Version {
		return Response{}, fmt.Errorf("protocol: unsupported schema_version %q (want %q)", raw.SchemaVersion, Version)
	}
	switch raw.Status {
	case StatusPassed, StatusFailed, StatusSkipped:
	case "":
		return Response{}, fmt.Errorf("protocol: missing status field in agent output")
	default:
		return Response{}, fmt.Errorf("protocol: unknown status %q", raw.Status)
	}

	provides := make(map[string]string, len(raw.Provides))
	for k, v := range raw.Provides {
		s, err := coerceString(v)
		if err != nil {
			return Response{}, fmt.Errorf("protocol: provides[%q]: %w", k, err)
		}
		provides[k] = s
	}

	return Response{
		SchemaVersion: raw.SchemaVersion,
		Status:        raw.Status,
		Provides:      provides,
		Error:         raw.Error,
		Artifacts:     raw.Artifacts,
	}, nil
}

// coerceString renders a provided value as a string whatever its JSON type, so
// an agent may emit a number or a boolean without quoting it.
func coerceString(v json.RawMessage) (string, error) {
	if len(v) > 0 && v[0] == '"' {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return string(v), nil
}
