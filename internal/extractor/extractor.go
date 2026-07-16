// Package extractor discovers test nodes from the tests themselves and turns
// them into a suite. Metadata is declared next to the test it describes, so the
// suite becomes a generated artefact rather than a file kept in sync by hand.
package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"playdag/internal/graph"
	"playdag/internal/loader"
)

// Node is a test node plus where it was declared, so a validation error can
// point at the line the developer has to fix.
type Node struct {
	loader.TestNode
	File string
	Line int
}

// at renders the node's declaration site as file:line.
func (n Node) at() string {
	if n.File == "" {
		return n.TestID
	}
	if n.Line == 0 {
		return n.File
	}
	return fmt.Sprintf("%s:%d", n.File, n.Line)
}

// Extractor reads node metadata out of a test suite. Each implementation knows
// one way tests declare themselves; none of them know how tests are run.
type Extractor interface {
	Extract(ctx context.Context) ([]Node, error)
}

// Names lists the extractors New accepts, for CLI help and error messages.
var Names = []string{"process", "playwright"}

// New builds the extractor selected by name, reading tests under baseDir.
func New(name, baseDir string) (Extractor, error) {
	switch name {
	case "process":
		return NewProcess(baseDir), nil
	case "playwright":
		return NewPlaywright(baseDir), nil
	default:
		return nil, fmt.Errorf("extractor: unknown extractor %q (want one of %v)", name, Names)
	}
}

// Validate rejects a suite that could not be executed, naming the declaration
// site of every node involved. Catching this at extraction time is the point:
// the developer hears about a broken contract when they write it, not when the
// run stalls.
func Validate(nodes []Node) error {
	if len(nodes) == 0 {
		return fmt.Errorf("extractor: no test declared playdag metadata")
	}

	if err := checkDuplicateIDs(nodes); err != nil {
		return err
	}
	providers, err := checkProviders(nodes)
	if err != nil {
		return err
	}
	if err := checkRequires(nodes, providers); err != nil {
		return err
	}
	return checkGraph(nodes)
}

func checkDuplicateIDs(nodes []Node) error {
	seen := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		if n.TestID == "" {
			return fmt.Errorf("extractor: %s: test has no playdag id", n.at())
		}
		if first, dup := seen[n.TestID]; dup {
			return fmt.Errorf("extractor: duplicate node id %q declared at %s and %s",
				n.TestID, first.at(), n.at())
		}
		seen[n.TestID] = n
	}
	return nil
}

// checkProviders maps each provided key to its node, rejecting two nodes that
// claim to produce the same key: the graph could not tell which one to wait for.
func checkProviders(nodes []Node) (map[string]Node, error) {
	providers := make(map[string]Node)
	for _, n := range nodes {
		for _, key := range n.Contract.Provides {
			if first, taken := providers[key]; taken {
				return nil, fmt.Errorf("extractor: key %q is provided twice: by %q at %s and by %q at %s",
					key, first.TestID, first.at(), n.TestID, n.at())
			}
			providers[key] = n
		}
	}
	return providers, nil
}

func checkRequires(nodes []Node, providers map[string]Node) error {
	for _, n := range nodes {
		for _, key := range n.Contract.Requires {
			if _, ok := providers[key]; !ok {
				return fmt.Errorf("extractor: %s: node %q requires key %q, which no test provides%s",
					n.at(), n.TestID, key, didYouMean(key, providers))
			}
		}
	}
	return nil
}

// didYouMean points at the closest known key, since an unknown key is usually a
// typo in one of the two places the key is spelled.
func didYouMean(key string, providers map[string]Node) string {
	var candidates []string
	for known := range providers {
		if strings.EqualFold(known, key) || strings.Contains(known, key) || strings.Contains(key, known) {
			candidates = append(candidates, known)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	return fmt.Sprintf(" (did you mean %q?)", candidates[0])
}

// checkGraph runs the real graph builder, so extraction rejects exactly what
// execution would reject — a cycle above all.
func checkGraph(nodes []Node) error {
	testNodes := make([]loader.TestNode, len(nodes))
	for i, n := range nodes {
		testNodes[i] = n.TestNode
	}
	if _, err := graph.NewDAG(testNodes); err != nil {
		return fmt.Errorf("extractor: %w%s", err, locations(nodes))
	}
	return nil
}

// locations lists where every node was declared, which is the only useful thing
// to say about a cycle: it names the file to look at.
func locations(nodes []Node) string {
	var sb strings.Builder
	sb.WriteString("\ndeclared at:")
	for _, n := range nodes {
		fmt.Fprintf(&sb, "\n  %-20s %s", n.TestID, n.at())
	}
	return sb.String()
}

// Suite assembles extracted nodes into a suite, ordered by id so that
// regenerating an unchanged test tree produces an unchanged file.
func Suite(suiteID string, nodes []Node) *loader.Suite {
	sorted := make([]Node, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TestID < sorted[j].TestID })

	testNodes := make([]loader.TestNode, len(sorted))
	for i, n := range sorted {
		testNodes[i] = n.TestNode
	}
	return &loader.Suite{
		SuiteID:     suiteID,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Nodes:       testNodes,
	}
}

// Save writes the suite as JSON to path, or to stdout when path is "-".
func Save(s *loader.Suite, path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("extractor: marshal suite: %w", err)
	}
	b = append(b, '\n')

	if path == "-" {
		_, err := os.Stdout.Write(b)
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("extractor: write suite: %w", err)
	}
	return nil
}
