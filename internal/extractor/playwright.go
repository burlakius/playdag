package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"playdag/internal/loader"
)

// Annotation types the npm adapter attaches. They mirror the constants in
// adapters/playwright/src/annotations.ts.
const (
	annID          = "playdag:id"
	annDescription = "playdag:description"
	annRequires    = "playdag:requires"
	annProvides    = "playdag:provides"
	annCriticality = "playdag:criticality"
	annPriority    = "playdag:priority"
	annRetries     = "playdag:retries"
	annTimeout     = "playdag:timeout"
	annRetryDelay  = "playdag:retry_delay"
	annBackoff     = "playdag:backoff"
	annScope       = "playdag:scope"
	annTags        = "playdag:tags"
)

// PlaywrightExtractor asks Playwright itself which tests exist and what they
// declared. Listing rather than parsing the source is what keeps the extractor
// honest: it sees the same tests the run will.
type PlaywrightExtractor struct {
	baseDir string
}

// NewPlaywright returns an extractor for the Playwright project in baseDir.
func NewPlaywright(baseDir string) *PlaywrightExtractor {
	return &PlaywrightExtractor{baseDir: baseDir}
}

// listReport is the part of `playwright test --list --reporter=json` we read.
type listReport struct {
	Config struct {
		RootDir string `json:"rootDir"`
	} `json:"config"`
	Suites []listSuite `json:"suites"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type listSuite struct {
	File   string      `json:"file"`
	Specs  []listSpec  `json:"specs"`
	Suites []listSuite `json:"suites"`
}

type listSpec struct {
	Title string `json:"title"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Tests []struct {
		Annotations []struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"annotations"`
	} `json:"tests"`
}

// Extract lists the Playwright suite and turns every test carrying playdag
// annotations into a node. Tests without them are ignored, so a project can
// adopt the orchestrator one test at a time.
func (e *PlaywrightExtractor) Extract(ctx context.Context) ([]Node, error) {
	cmd := exec.CommandContext(ctx, "npx", "playwright", "test", "--list", "--reporter=json")
	cmd.Dir = e.baseDir

	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("extractor: playwright --list failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var report listReport
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("extractor: parse playwright --list output: %w", err)
	}
	if len(report.Errors) > 0 {
		return nil, fmt.Errorf("extractor: playwright reported an error: %s", report.Errors[0].Message)
	}

	var nodes []Node
	for _, s := range report.Suites {
		nodes = append(nodes, e.nodesOf(s, report.Config.RootDir)...)
	}
	return nodes, nil
}

// nodesOf walks a suite and its children, since Playwright nests describe blocks.
func (e *PlaywrightExtractor) nodesOf(s listSuite, rootDir string) []Node {
	var nodes []Node
	for _, spec := range s.Specs {
		if n, ok := e.nodeOf(spec, rootDir); ok {
			nodes = append(nodes, n)
		}
	}
	for _, child := range s.Suites {
		nodes = append(nodes, e.nodesOf(child, rootDir)...)
	}
	return nodes
}

func (e *PlaywrightExtractor) nodeOf(spec listSpec, rootDir string) (Node, bool) {
	ann := map[string]string{}
	for _, t := range spec.Tests {
		for _, a := range t.Annotations {
			ann[a.Type] = a.Description
		}
	}
	if ann[annID] == "" {
		return Node{}, false
	}

	script := e.scriptOf(spec.File, rootDir)
	node := Node{
		TestNode: loader.TestNode{
			TestID:      ann[annID],
			Description: description(ann, spec.Title),
			Script:      script,
			Contract: loader.DataContract{
				Requires: splitList(ann[annRequires]),
				Provides: splitList(ann[annProvides]),
				Scope:    ann[annScope],
			},
			Policy: loader.ExecutionPolicy{
				Criticality: criticality(ann[annCriticality]),
				Retries:     atoi(ann[annRetries]),
				Priority:    atoi(ann[annPriority]),
				Timeout:     ann[annTimeout],
				RetryDelay:  ann[annRetryDelay],
				Backoff:     ann[annBackoff],
			},
			Resources: loader.ResourceConstraints{Tags: splitList(ann[annTags])},
		},
		File: script,
		Line: spec.Line,
	}
	return node, true
}

// scriptOf renders a spec path the way the suite refers to it: relative to the
// project directory, which is what the executor joins onto -basedir.
func (e *PlaywrightExtractor) scriptOf(specFile, rootDir string) string {
	abs := specFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(rootDir, specFile)
	}
	base, err := filepath.Abs(e.baseDir)
	if err != nil {
		return specFile
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return specFile
	}
	return rel
}

func description(ann map[string]string, title string) string {
	if d := ann[annDescription]; d != "" {
		return d
	}
	return title
}

// criticality defaults to BLOCKER: a test whose result nothing depends on is the
// exception, and a wrong default here would silently let failures through.
func criticality(v string) string {
	if v == "" {
		return "BLOCKER"
	}
	return v
}

// splitList parses the comma-separated form the adapter encodes lists in.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return []string{}
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoi(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}
