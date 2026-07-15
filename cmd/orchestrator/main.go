// Command orchestrator is the CLI entry point for the playdag DAG executor.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"playdag/internal/graph"
	"playdag/internal/loader"
	"playdag/internal/report"
	"playdag/internal/runner"
	"playdag/internal/store"
)

func main() {
	suiteFlag    := flag.String("suite",    "tests/suite.json",           "path to suite.json")
	basedirFlag  := flag.String("basedir",  ".",                           "base directory for python scripts")
	baseurlFlag  := flag.String("baseurl",  "http://localhost:8000",       "BASE_URL for agents")
	workersFlag  := flag.Int("workers",     4,                             "max concurrent agents")
	snapshotFlag := flag.String("snapshot", "logs/context-snapshot.json",  "path for context snapshot JSON")
	reportFlag   := flag.String("report",   "logs/report.json",            "path for JSON report output")
	timeoutFlag  := flag.Int("timeout",     300,                           "global timeout in seconds")
	jsonFlag     := flag.Bool("json",       false,                         "emit machine-readable JSON event log")
	flag.Parse()

	suite, err := loader.LoadSuite(*suiteFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load suite: %v\n", err)
		os.Exit(1)
	}

	dag, err := graph.NewDAG(suite.Nodes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build graph: %v\n", err)
		os.Exit(1)
	}

	// Ensure parent directories for output files exist.
	for _, p := range []string{*snapshotFlag, *reportFlag} {
		if dir := filepath.Dir(p); dir != "." {
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				fmt.Fprintf(os.Stderr, "warning: mkdir %s: %v\n", dir, mkErr)
			}
		}
	}

	s := store.NewContextStore(*snapshotFlag)
	r := runner.NewRunner(dag, s, runner.RunOptions{
		MaxConcurrency: *workersFlag,
		BaseDir:        *basedirFlag,
		BaseURL:        *baseurlFlag,
		JSONLog:        *jsonFlag,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutFlag)*time.Second)
	defer cancel()

	startedAt := time.Now()
	runErr := r.Run(ctx)
	finishedAt := time.Now()

	if err := s.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: flush snapshot: %v\n", err)
	}

	nodeOrder := make([]string, len(suite.Nodes))
	for i, n := range suite.Nodes {
		nodeOrder[i] = n.TestID
	}

	rep := &report.Report{
		SuiteID:         suite.SuiteID,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
		TotalDuration:   finishedAt.Sub(startedAt),
		Results:         r.Results(),
		ContextSnapshot: s.Snapshot(),
		NodeOrder:       nodeOrder,
	}

	// In JSON mode stdout carries only the event stream; the boxed summary goes
	// to stderr so the two never mix.
	if *jsonFlag {
		fmt.Fprint(os.Stderr, report.Generate(rep))
	} else {
		fmt.Print(report.Generate(rep))
	}

	if err := report.SaveJSON(rep, *reportFlag); err != nil {
		fmt.Fprintf(os.Stderr, "warning: save report: %v\n", err)
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "run error: %v\n", runErr)
		os.Exit(1)
	}

	if dag.Summary()["failed"] > 0 {
		os.Exit(1)
	}
}
