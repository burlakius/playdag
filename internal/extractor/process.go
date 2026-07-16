package extractor

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"playdag/internal/loader"
)

// metaLine matches one declaration in an agent's header docstring:
//
//	playdag:provides: session.token
//
// It is deliberately a comment convention rather than a decorator or an import:
// an agent stays a plain script that owes the orchestrator no dependency.
var metaLine = regexp.MustCompile(`^\s*#?\s*playdag:([a-z_]+)\s*:\s*(.*?)\s*$`)

// headerLimit bounds how far into a file the header is looked for, so a stray
// mention deep in the code cannot be mistaken for a declaration.
const headerLimit = 40

// ProcessExtractor reads playdag metadata out of the header of agent scripts,
// the counterpart of the annotations the Playwright adapter attaches.
type ProcessExtractor struct {
	baseDir string
}

// NewProcess returns an extractor for the agent scripts under baseDir.
func NewProcess(baseDir string) *ProcessExtractor {
	return &ProcessExtractor{baseDir: baseDir}
}

// Extract walks baseDir and turns every script declaring playdag:id into a node.
// Scripts without it are ignored: a helper module is not a test.
func (e *ProcessExtractor) Extract(ctx context.Context) ([]Node, error) {
	var nodes []Node

	err := filepath.WalkDir(e.baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".py" {
			return nil
		}

		node, ok, err := e.nodeOf(path)
		if err != nil {
			return err
		}
		if ok {
			nodes = append(nodes, node)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("extractor: scan %s: %w", e.baseDir, err)
	}
	return nodes, nil
}

// skipDir keeps the walk out of trees that hold no agents but plenty of files.
func skipDir(name string) bool {
	switch name {
	case "node_modules", ".git", "__pycache__", "venv", ".venv":
		return true
	}
	return false
}

func (e *ProcessExtractor) nodeOf(path string) (Node, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Node{}, false, err
	}
	defer f.Close()

	meta := map[string]string{}
	idLine := 0

	scanner := bufio.NewScanner(f)
	for lineNo := 1; scanner.Scan() && lineNo <= headerLimit; lineNo++ {
		m := metaLine.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		meta[m[1]] = m[2]
		if m[1] == "id" {
			idLine = lineNo
		}
	}
	if err := scanner.Err(); err != nil {
		return Node{}, false, err
	}
	if meta["id"] == "" {
		return Node{}, false, nil
	}

	script, err := filepath.Rel(e.baseDir, path)
	if err != nil {
		script = path
	}

	return Node{
		TestNode: loader.TestNode{
			TestID:      meta["id"],
			Description: meta["description"],
			Script:      script,
			Contract: loader.DataContract{
				Requires: splitList(meta["requires"]),
				Provides: splitList(meta["provides"]),
				Scope:    meta["scope"],
			},
			Policy: loader.ExecutionPolicy{
				Criticality: criticality(meta["criticality"]),
				Retries:     atoi(meta["retries"]),
				Priority:    atoi(meta["priority"]),
				Timeout:     strings.TrimSpace(meta["timeout"]),
				RetryDelay:  strings.TrimSpace(meta["retry_delay"]),
				Backoff:     strings.TrimSpace(meta["backoff"]),
			},
			Resources: loader.ResourceConstraints{Tags: splitList(meta["tags"])},
		},
		File: script,
		Line: idLine,
	}, true, nil
}
