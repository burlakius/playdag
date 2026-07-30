package extractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// agentDir writes agent scripts into a temp tree: name -> file body.
func agentDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

const loginAgent = `#!/usr/bin/env python3
"""Logs in once and publishes the session.

playdag:id: auth.login
playdag:description: Log in over the API
playdag:provides: session.token
playdag:criticality: BLOCKER
playdag:priority: 10
playdag:retries: 1
playdag:timeout: 30s
playdag:scope: suite
playdag:tags: api
"""
import sys
`

func TestProcess_ReadsHeaderMetadata(t *testing.T) {
	dir := agentDir(t, map[string]string{"agents/login.py": loginAgent})

	nodes, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}

	n := nodes[0]
	if n.TestID != "auth.login" {
		t.Errorf("test_id: got %q", n.TestID)
	}
	if n.Description != "Log in over the API" {
		t.Errorf("description: got %q", n.Description)
	}
	if n.Script != filepath.Join("agents", "login.py") {
		t.Errorf("script: got %q", n.Script)
	}
	if len(n.Contract.Provides) != 1 || n.Contract.Provides[0] != "session.token" {
		t.Errorf("provides: got %v", n.Contract.Provides)
	}
	if n.Contract.Scope != "suite" {
		t.Errorf("scope: got %q", n.Contract.Scope)
	}
	if n.Policy.Criticality != "BLOCKER" || n.Policy.Retries != 1 || n.Policy.Priority != 10 {
		t.Errorf("policy: got %+v", n.Policy)
	}
	if n.Policy.Timeout != "30s" {
		t.Errorf("timeout: got %q", n.Policy.Timeout)
	}
	if len(n.Resources.Tags) != 1 || n.Resources.Tags[0] != "api" {
		t.Errorf("tags: got %v", n.Resources.Tags)
	}
	if n.Line != 4 {
		t.Errorf("line should point at the id declaration, got %d", n.Line)
	}
}

func TestProcess_MultipleKeysAreSplit(t *testing.T) {
	dir := agentDir(t, map[string]string{"move.py": `"""
playdag:id: card.move
playdag:requires: session.token, card.id
"""`})

	nodes, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got := nodes[0].Contract.Requires
	if len(got) != 2 || got[0] != "session.token" || got[1] != "card.id" {
		t.Fatalf("requires: got %v", got)
	}
}

// A helper module is not a test.
func TestProcess_IgnoresScriptsWithoutID(t *testing.T) {
	dir := agentDir(t, map[string]string{
		"agents/login.py":  loginAgent,
		"agents/helper.py": `"""Shared HTTP helpers, not a test."""`,
	})

	nodes, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 1 || nodes[0].TestID != "auth.login" {
		t.Fatalf("want only auth.login, got %+v", nodes)
	}
}

func TestProcess_DefaultsToBlocker(t *testing.T) {
	dir := agentDir(t, map[string]string{"a.py": `"""
playdag:id: a
"""`})

	nodes, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if nodes[0].Policy.Criticality != "BLOCKER" {
		t.Fatalf("criticality should default to BLOCKER, got %q", nodes[0].Policy.Criticality)
	}
}

// A mention far below the header is code, not a declaration.
func TestProcess_IgnoresMentionsBelowTheHeader(t *testing.T) {
	body := `"""Real agent."""
`
	for i := 0; i < headerLimit; i++ {
		body += "# filler\n"
	}
	body += "# playdag:id: sneaky\n"

	dir := agentDir(t, map[string]string{"a.py": body})
	nodes, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("want no nodes, got %+v", nodes)
	}
}

func TestProcess_SkipsNonPythonAndVendorTrees(t *testing.T) {
	dir := agentDir(t, map[string]string{
		"agents/login.py":              loginAgent,
		"agents/notes.txt":             "playdag:id: not-a-script",
		"node_modules/pkg/vendored.py": `"""` + "\nplaydag:id: vendored\n" + `"""`,
		"__pycache__/cached.py":        `"""` + "\nplaydag:id: cached\n" + `"""`,
	})

	nodes, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 1 || nodes[0].TestID != "auth.login" {
		t.Fatalf("want only auth.login, got %+v", nodes)
	}
}

// Regenerating an unchanged tree must produce an unchanged file.
func TestProcess_OrderIsStable(t *testing.T) {
	files := map[string]string{}
	for _, id := range []string{"z", "a", "m"} {
		files[id+".py"] = `"""` + "\nplaydag:id: " + id + "\n" + `"""`
	}
	dir := agentDir(t, files)

	first, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	second, err := NewProcess(dir).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for i := range first {
		if first[i].TestID != second[i].TestID {
			t.Fatalf("order is not stable: %v vs %v", ids(first), ids(second))
		}
	}
}

func ids(nodes []Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.TestID
	}
	return out
}
