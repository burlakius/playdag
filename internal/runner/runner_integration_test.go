//go:build integration

package runner

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"playdag/internal/graph"
	"playdag/internal/loader"
	"playdag/internal/store"
)

func TestRun_RealSubprocess(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	testdataDir := filepath.Join(filepath.Dir(thisFile), "testdata")

	nodes := []loader.TestNode{
		{
			TestID: "mock",
			Script: "mock_agent.py",
			Policy: loader.ExecutionPolicy{Criticality: "BLOCKER", Retries: 0},
			Contract: loader.DataContract{
				Requires: nil,
				Provides: []string{"mock.key"},
			},
		},
	}

	dag, err := graph.NewDAG(nodes)
	if err != nil {
		t.Fatalf("NewDAG: %v", err)
	}

	s := store.NewContextStore("")
	r := NewRunner(dag, s, RunOptions{
		MaxConcurrency: 1,
		BaseDir:        testdataDir,
	})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	results := r.Results()
	res, ok := results["mock"]
	if !ok {
		t.Fatal("no result for 'mock'")
	}
	if res.Status != "PASSED" {
		t.Fatalf("expected PASSED, got %q: %s", res.Status, res.Error)
	}

	val, ok := s.Get("mock.key")
	if !ok {
		t.Fatal("mock.key not written to store")
	}
	if val != "mock_value" {
		t.Fatalf("expected mock_value, got %q", val)
	}
}
