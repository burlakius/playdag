// Command orchestrator is the CLI entry point for the playdag DAG executor.
//
//	orchestrator [flags]              run a suite
//	orchestrator extract [flags]      generate a suite from the tests
//	orchestrator graph [flags]        render the suite's dependency graph
//	orchestrator validate [flags]     check a suite without running it
//	orchestrator version              print the version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"playdag/internal/executor"
	"playdag/internal/extractor"
	"playdag/internal/graph"
	"playdag/internal/loader"
	"playdag/internal/protocol"
	"playdag/internal/report"
	"playdag/internal/runner"
	"playdag/internal/scheduler"
	"playdag/internal/store"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/orchestrator/
var version = "dev"

// Exit codes. Telling a failing suite from a broken run is the difference
// between a red build someone investigates and a red build someone reruns.
const (
	exitOK      = 0 // every node passed
	exitFailed  = 1 // the suite ran, and something in it failed
	exitProblem = 2 // the suite could not be run at all
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, ok := commands[args[0]]
		if !ok {
			fmt.Fprintf(os.Stderr, "error: unknown command %q (want one of %v, or flags to run a suite)\n",
				args[0], commandNames())
			os.Exit(exitProblem)
		}
		if err := cmd(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(exitProblem)
		}
		return
	}
	run(args)
}

// commands are the subcommands; anything else is flags for a run.
var commands = map[string]func([]string) error{
	"extract":  extract,
	"graph":    graphCmd,
	"validate": validateCmd,
	"version":  versionCmd,
}

func versionCmd([]string) error {
	fmt.Printf("orchestrator %s (agent protocol %s)\n", version, protocol.Version)
	return nil
}

// validateCmd checks a suite without running it: the same checks the run does,
// paid for in a second rather than in a CI job that stalls halfway.
func validateCmd(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	suiteFlag   := fs.String("suite",   "tests/suite.json", "path to suite.json")
	basedirFlag := fs.String("basedir", ".",                "directory the scripts are relative to")
	if err := fs.Parse(args); err != nil {
		return err
	}

	suite, err := loader.LoadSuite(*suiteFlag)
	if err != nil {
		return err
	}
	dag, err := graph.NewDAG(suite.Nodes)
	if err != nil {
		return err
	}

	// A contract can be perfect while the script it names does not exist. The
	// run would only find that out when it reached the node.
	var missing []string
	for _, n := range suite.Nodes {
		path := filepath.Join(*basedirFlag, n.Script)
		if _, statErr := os.Stat(path); statErr != nil {
			missing = append(missing, fmt.Sprintf("%s: %s", n.TestID, path))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("script not found for %d node(s):\n  %s", len(missing), strings.Join(missing, "\n  "))
	}

	ready := len(dag.ReadyNodes())
	fmt.Printf("%s: %d node(s), %d with nothing to wait for, no cycles, every key provided\n",
		suite.SuiteID, len(suite.Nodes), ready)
	return nil
}

func commandNames() []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// graphCmd renders the suite's dependency graph for documentation.
func graphCmd(args []string) error {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	suiteFlag  := fs.String("suite",  "tests/suite.json", "path to suite.json")
	formatFlag := fs.String("format", "mermaid",          "mermaid or dot")
	outFlag    := fs.String("o",      "-",                "output path, or - for stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	suite, err := loader.LoadSuite(*suiteFlag)
	if err != nil {
		return err
	}
	// Building the real DAG also validates it, so a graph is never rendered for a
	// suite that could not run.
	dag, err := graph.NewDAG(suite.Nodes)
	if err != nil {
		return err
	}

	var out string
	switch *formatFlag {
	case "mermaid":
		out = dag.Mermaid()
	case "dot":
		out = dag.DOT()
	default:
		return fmt.Errorf("unknown format %q (want mermaid or dot)", *formatFlag)
	}

	if *outFlag == "-" {
		fmt.Print(out)
		return nil
	}
	if err := os.WriteFile(*outFlag, []byte(out), 0o644); err != nil {
		return fmt.Errorf("write graph: %w", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", *outFlag)
	return nil
}

// extract generates a suite file from the metadata the tests declare.
func extract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	extractorFlag := fs.String("extractor", "process",       "where metadata is read from: process (script headers) or playwright (test annotations)")
	basedirFlag   := fs.String("basedir",   ".",             "directory holding the tests")
	outFlag       := fs.String("o",         "suite.json",    "output path, or - for stdout")
	suiteIDFlag   := fs.String("suite-id",  "",              "suite_id for the generated file; default: the basedir name")
	timeoutFlag   := fs.Int("timeout",      120,             "timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ex, err := extractor.New(*extractorFlag, *basedirFlag)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutFlag)*time.Second)
	defer cancel()

	nodes, err := ex.Extract(ctx)
	if err != nil {
		return err
	}
	if err := extractor.Validate(nodes); err != nil {
		return err
	}

	suite := extractor.Suite(suiteID(*suiteIDFlag, *basedirFlag), nodes)
	if err := extractor.Save(suite, *outFlag); err != nil {
		return err
	}

	// The summary goes to stderr so that -o - stays a clean suite on stdout.
	fmt.Fprintf(os.Stderr, "extracted %d node(s) into %s\n", len(nodes), *outFlag)
	return nil
}

// suiteID falls back to the name of the directory the tests live in.
func suiteID(explicit, baseDir string) string {
	if explicit != "" {
		return explicit
	}
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return "suite"
	}
	return filepath.Base(abs)
}

// run executes a suite and reports on it.
func run(args []string) {
	fs := flag.NewFlagSet("orchestrator", flag.ExitOnError)
	suiteFlag    := fs.String("suite",          "tests/suite.json",           "path to suite.json")
	basedirFlag  := fs.String("basedir",        ".",                          "base directory for agent scripts or the test project")
	baseurlFlag  := fs.String("baseurl",        "http://localhost:8000",      "BASE_URL for agents")
	workersFlag  := fs.Int("workers",           4,                            "max concurrent agents")
	snapshotFlag := fs.String("snapshot",       "logs/context-snapshot.json", "path for context snapshot JSON")
	reportFlag   := fs.String("report",         "logs/report.json",           "path for JSON report output")
	timeoutFlag  := fs.Int("timeout",           300,                          "global timeout in seconds")
	jsonFlag     := fs.Bool("json",             false,                        "emit machine-readable JSON event log")
	agentsFlag   := fs.String("agents",         "",                           "agent pool as JSON file or inline; default: universal agents")
	executorFlag := fs.String("executor",       "process",                    "how nodes are run: process (any protocol-speaking agent) or playwright")
	failfastFlag := fs.Bool("fail-fast",        false,                        "stop the run as soon as a BLOCKER node fails")
	maxfailFlag  := fs.Int("max-failures",      0,                            "stop after this many failed nodes; 0 = no limit")
	globalFlag   := fs.String("global-context", "",                           "file where global-scoped values persist between runs; empty = off")
	htmlFlag     := fs.String("html",           "",                           "path for a single-file HTML report with a timeline; empty = off")
	junitFlag    := fs.String("junit",          "",                           "path for a JUnit XML report, read natively by GitLab and Jenkins; empty = off")
	fs.Parse(args)

	suite, err := loader.LoadSuite(*suiteFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load suite: %v\n", err)
		os.Exit(exitProblem)
	}

	dag, err := graph.NewDAG(suite.Nodes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build graph: %v\n", err)
		os.Exit(exitProblem)
	}

	// Ensure parent directories for output files exist.
	for _, p := range []string{*snapshotFlag, *reportFlag, *htmlFlag, *junitFlag} {
		if p == "" {
			continue
		}
		if dir := filepath.Dir(p); dir != "." {
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				fmt.Fprintf(os.Stderr, "warning: mkdir %s: %v\n", dir, mkErr)
			}
		}
	}

	agents, err := scheduler.ParseAgents(*agentsFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(exitProblem)
	}

	exec, err := executor.New(*executorFlag, *basedirFlag, *baseurlFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(exitProblem)
	}

	s := store.NewContextStore(*snapshotFlag)
	if *globalFlag != "" {
		if err := s.UseGlobalFile(*globalFlag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(exitProblem)
		}
	}
	r := runner.NewRunner(dag, s, runner.RunOptions{
		MaxConcurrency: *workersFlag,
		JSONLog:        *jsonFlag,
		Executor:       exec,
		Agents:         agents,
		FailFast:       *failfastFlag,
		MaxFailures:    *maxfailFlag,
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
		Workers:         r.AgentCount(),
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

	if *htmlFlag != "" {
		if err := report.SaveHTML(rep, *htmlFlag); err != nil {
			fmt.Fprintf(os.Stderr, "warning: save html report: %v\n", err)
		}
	}

	if *junitFlag != "" {
		if err := report.SaveJUnit(rep, *junitFlag); err != nil {
			fmt.Fprintf(os.Stderr, "warning: save junit report: %v\n", err)
		}
	}

	// A run that could not be performed is not a run that found bugs: the
	// timeout, the unschedulable node and the broken agent pool are the
	// orchestrator's problem, not the suite's.
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "run error: %v\n", runErr)
		os.Exit(exitProblem)
	}

	if dag.Summary()["failed"] > 0 {
		os.Exit(exitFailed)
	}
}
