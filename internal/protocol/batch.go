package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// BatchRequest asks an agent to run several nodes in one process. The nodes in
// it are independent of each other — the orchestrator only ever batches nodes
// that are ready at the same moment — so the agent may run them in any order.
type BatchRequest struct {
	SchemaVersion string    `json:"schema_version"`
	Tests         []Request `json:"tests"`
}

// BatchResponse is what the agent writes once the batch finishes. Every test id
// in the request must appear in Results; a node the agent never reached is the
// agent's failure to report, not a node that passed.
type BatchResponse struct {
	SchemaVersion string              `json:"schema_version"`
	Results       map[string]Response `json:"results"`
}

// EncodeBatchRequest serialises reqs as a single batch envelope. Unlike
// EncodeRequest it does not mirror context keys at the top level: a batch has no
// single context, and no agent predates the envelope it is defined in.
func EncodeBatchRequest(reqs []Request) ([]byte, error) {
	for i := range reqs {
		reqs[i].SchemaVersion = Version
	}
	out, err := json.Marshal(BatchRequest{SchemaVersion: Version, Tests: reqs})
	if err != nil {
		return nil, fmt.Errorf("protocol: encode batch request: %w", err)
	}
	return out, nil
}

// DecodeBatchResponse parses an agent's batch result and checks that every id in
// want was reported. Missing ids are named in the error rather than silently
// dropped, which would let a batch quietly shrink the suite.
func DecodeBatchResponse(data []byte, want []string) (map[string]Response, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("protocol: agent produced no output")
	}

	var raw struct {
		SchemaVersion string                     `json:"schema_version"`
		Results       map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("protocol: invalid JSON: %w", err)
	}
	if raw.SchemaVersion != "" && !supported[raw.SchemaVersion] {
		return nil, fmt.Errorf("protocol: unsupported schema_version %q (want %q)", raw.SchemaVersion, Version)
	}

	results := make(map[string]Response, len(raw.Results))
	for id, blob := range raw.Results {
		resp, err := DecodeResponse(blob)
		if err != nil {
			return nil, fmt.Errorf("protocol: results[%q]: %w", id, err)
		}
		results[id] = resp
	}

	var missing []string
	for _, id := range want {
		if _, ok := results[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("protocol: batch reported no result for %v", missing)
	}
	return results, nil
}
