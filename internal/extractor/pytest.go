package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"playdag/internal/loader"
)

// PytestExtractor asks pytest itself which tests exist and what they declared,
// through the collect hook the playdag-pytest package registers. Listing rather
// than parsing the source is what keeps the extractor honest: it sees the same
// tests the run will.
type PytestExtractor struct {
	baseDir string
}

// NewPytest returns an extractor for the pytest project in baseDir.
func NewPytest(baseDir string) *PytestExtractor {
	return &PytestExtractor{baseDir: baseDir}
}

// pytestManifest is what --playdag-collect writes: one entry per marked test,
// its declaration site, and the contract carried on the marker.
type pytestManifest struct {
	Nodes []pytestNode `json:"nodes"`
}

type pytestNode struct {
	NodeID      string   `json:"nodeid"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Requires    []string `json:"requires"`
	Provides    []string `json:"provides"`
	Criticality string   `json:"criticality"`
	Priority    int      `json:"priority"`
	Weight      int      `json:"weight"`
	Retries     int      `json:"retries"`
	Timeout     string   `json:"timeout"`
	RetryDelay  string   `json:"retry_delay"`
	Backoff     string   `json:"backoff"`
	Scope       string   `json:"scope"`
	Tags        []string `json:"tags"`
}

// Extract runs pytest's own collection and reads the manifest the
// playdag-pytest plugin writes for every test carrying the playdag marker.
func (e *PytestExtractor) Extract(ctx context.Context) ([]Node, error) {
	manifestFile, err := os.CreateTemp("", "playdag-pytest-collect-*.json")
	if err != nil {
		return nil, fmt.Errorf("extractor: create manifest file: %w", err)
	}
	manifestPath := manifestFile.Name()
	if err := manifestFile.Close(); err != nil {
		return nil, fmt.Errorf("extractor: create manifest file: %w", err)
	}
	defer func() { _ = os.Remove(manifestPath) }()

	cmd := exec.CommandContext(ctx, "python3", "-m", "pytest", "--collect-only", "-q",
		"--playdag-collect="+manifestPath)
	cmd.Dir = e.baseDir

	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("extractor: pytest --collect-only failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("extractor: read manifest: %w", err)
	}

	var manifest pytestManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("extractor: parse manifest: %w", err)
	}

	nodes := make([]Node, len(manifest.Nodes))
	for i, n := range manifest.Nodes {
		nodes[i] = e.nodeOf(n)
	}
	return nodes, nil
}

func (e *PytestExtractor) nodeOf(n pytestNode) Node {
	return Node{
		TestNode: loader.TestNode{
			TestID:      n.ID,
			Description: fallbackDescription(n.Description, n.NodeID),
			Script:      n.NodeID,
			Contract: loader.DataContract{
				Requires: orEmpty(n.Requires),
				Provides: orEmpty(n.Provides),
				Scope:    n.Scope,
			},
			Policy: loader.ExecutionPolicy{
				Criticality: criticality(n.Criticality),
				Retries:     n.Retries,
				Priority:    n.Priority,
				Weight:      n.Weight,
				Timeout:     n.Timeout,
				RetryDelay:  n.RetryDelay,
				Backoff:     n.Backoff,
			},
			Resources: loader.ResourceConstraints{Tags: orEmpty(n.Tags)},
		},
		File: n.File,
		Line: n.Line,
	}
}

// fallbackDescription falls back to the node id, which pytest always has,
// unlike a Playwright test title that a spec may omit a description for.
func fallbackDescription(description, nodeID string) string {
	if description != "" {
		return description
	}
	return nodeID
}

// orEmpty turns a nil slice into an empty one, so a generated suite.json reads
// "[]" for a node with no requires or provides rather than "null".
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
