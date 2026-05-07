// Package loader reads and validates playdag suite definition files.
package loader

import (
	"encoding/json"
	"fmt"
	"os"
)

// Suite represents the top-level test suite definition.
type Suite struct {
	SuiteID     string     `json:"suite_id"`
	GeneratedAt string     `json:"generated_at"`
	Nodes       []TestNode `json:"nodes"`
}

// TestNode describes a single test in the suite.
type TestNode struct {
	TestID      string              `json:"test_id"`
	Description string              `json:"description"`
	Script      string              `json:"script"`
	Contract    DataContract        `json:"contract"`
	Policy      ExecutionPolicy     `json:"policy"`
	Resources   ResourceConstraints `json:"resources"`
}

// DataContract declares the data dependencies a test node requires and provides.
type DataContract struct {
	Requires []string `json:"requires"`
	Provides []string `json:"provides"`
	Scope    string   `json:"scope"`
}

// ExecutionPolicy controls how a node is scheduled and retried.
type ExecutionPolicy struct {
	Criticality string `json:"criticality"`
	Retries     int    `json:"retries"`
	Priority    int    `json:"priority"`
	Timeout     string `json:"timeout"`
}

// ResourceConstraints restricts which runner tags may execute the node.
type ResourceConstraints struct {
	Tags []string `json:"tags"`
}

// LoadSuite reads the JSON file at path, parses it, and validates its contents.
// It returns a non-nil error if the file cannot be read, is malformed, or fails
// any validation rule.
func LoadSuite(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loader: read file: %w", err)
	}

	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("loader: parse JSON: %w", err)
	}

	if err := validate(&suite); err != nil {
		return nil, err
	}

	return &suite, nil
}

func validate(s *Suite) error {
	if s.SuiteID == "" {
		return fmt.Errorf("loader: suite_id is required")
	}
	if len(s.Nodes) == 0 {
		return fmt.Errorf("loader: suite %q has no nodes", s.SuiteID)
	}

	seen := make(map[string]struct{}, len(s.Nodes))
	for i, n := range s.Nodes {
		if n.TestID == "" {
			return fmt.Errorf("loader: node[%d]: test_id is required", i)
		}
		if n.Script == "" {
			return fmt.Errorf("loader: node %q: script is required", n.TestID)
		}
		switch n.Policy.Criticality {
		case "BLOCKER", "NON_CRITICAL":
			// valid
		default:
			return fmt.Errorf("loader: node %q: invalid criticality %q (must be BLOCKER or NON_CRITICAL)", n.TestID, n.Policy.Criticality)
		}
		if _, dup := seen[n.TestID]; dup {
			return fmt.Errorf("loader: duplicate test_id %q", n.TestID)
		}
		seen[n.TestID] = struct{}{}
	}

	return nil
}
