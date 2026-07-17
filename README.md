# playdag

**playdag** is a DAG orchestrator for end-to-end test suites. Tests declare the data they need and produce; playdag derives the order from those declarations, runs the suite with controlled parallelism, and hands each test the values the earlier ones produced — so a session established once is reused rather than established again.

## When this pays off

Sharing a session removes work: one sign-in instead of one per test. Running
each node in its own framework process adds work: about 1.6 s of startup per
node for Playwright. Which wins depends on your suite, and the
[experiments](experiments/README.md) measure where the line is:

- **Worth it** when the repeated setup is expensive — measured break-even is a
  sign-in costing **~3 s** — or when a broken blocker should stop the run
  (**−45%** of CI time in the fail-fast experiment), or when the suite's shape
  gives the scheduler something to overlap (**1.55×** over a single agent).
- **Not worth it** on suites of many cheap tests against a fast application. On
  the demo suite, whose sign-in costs milliseconds, plain `npx playwright test`
  is **2.4× faster**. The measurement is in `experiments/results.md`, and the
  reason — and what would change it — is in
  [ADR 3](docs/adr/0003-one-process-per-node.md).

That trade is the honest summary: this removes repeated work, and charges a
process per node for doing so.

Each value declares a **scope**, which decides how long it lives and who may see it:

| Scope | Lives | Seen by |
|-------|-------|---------|
| `session` | one agent | only nodes on that agent, which are scheduled there for that reason |
| `suite` (default) | the run | every node |
| `global` | beyond the run, in `-global-context` | every node, including later runs |

A node that declares `global` scope and whose provided keys are already in the
context file is not run again: an expensive one-off setup is paid for once
rather than once per run. It is opt-in — without `-global-context` nothing
persists, and every node runs.

The core is framework-agnostic: it speaks the [agent protocol](docs/PROTOCOL.md) and dispatches through an `Executor`. Playwright is one adapter ([`adapters/playwright`](adapters/playwright/README.md)), not a dependency — see [`examples/playwright`](examples/playwright/README.md) for a browser suite and [`examples/process`](examples/process/README.md) for the same graph run by plain Python agents.

Why the order comes from data contracts rather than `depends_on`, why the core is Go, and what running each node in its own process costs: [`docs/`](docs/README.md). What it measures out at, including where it loses: [`experiments/`](experiments/README.md).

## Architecture

```
tests (annotations / script headers)
    │
    │  orchestrator extract
    ▼
suite.json  (generated artefact)
    │
    ▼
┌──────────┐   validate & index
│  loader  │──────────────────────────────────────────────┐
└──────────┘                                              │
                                                          ▼
┌──────────┐   build edges + Kahn cycle check   ┌──────────────┐
│  graph   │◀──────────────────────────────────│  TestNode[]  │
│  (DAG)   │                                    └──────────────┘
│          │  ReadyNodes / MarkRunning
│          │  MarkPassed / MarkFailed (BFS skip)
└────┬─────┘
     │ ready nodes
     ▼
┌──────────┐
│scheduler │  priority queue (priority → critical path → id)
│          │  tag-aware agent pool
└────┬─────┘
     │ node + agent
     ▼
┌──────────┐
│  runner  │  retries, timeouts, skip propagation
└────┬─────┘
     │ Executor.Run(ctx, node, input)
     ▼
┌──────────┐   playdag protocol (JSON)      ┌─────────────────────────┐
│ executor │◀─────────────────────────────▶│ agent                   │
│          │                                │                         │
│ process  │  request:  context, attempt    │ process:    any binary  │
│playwright│  response: status, provides,   │ playwright: npx test +  │
│          │            artifacts           │   @playdag/playwright   │
└────┬─────┘                                └─────────────────────────┘
     │ provides key-value pairs
     ▼
┌──────────┐
│  store   │  thread-safe key→value map
│(context) │  optional context-snapshot.json flush
└────┬─────┘
     │ snapshot
     ▼
┌──────────┐
│  report  │  Generate() → text report
│          │  SaveJSON()  → logs/report.json  (+ cost metrics)
│          │  SaveHTML()  → timeline, one self-contained file
│          │  SaveJUnit() → GitLab / Jenkins
└──────────┘
```

## Installation

```bash
git clone <repo-url>
cd playdag
go build ./cmd/orchestrator/
```

Requires Go 1.24+. The generic agents in the example need Python 3; the
Playwright adapter needs Node 18+ (`npm install`).

## Usage

```bash
# Generate the suite from the metadata the tests declare
./orchestrator extract -extractor process -basedir examples/process -o examples/process/suite.json

# Generic agents: any executable that speaks the protocol
./orchestrator -suite examples/process/suite.json -basedir examples/process

# The same graph, run as real Playwright tests
./orchestrator -executor playwright -suite examples/playwright/suite.json -basedir examples/playwright

# Run with 2 parallel workers
./orchestrator -suite examples/process/suite.json -workers 2

# Run against another environment
./orchestrator -suite examples/process/suite.json -baseurl http://staging.example.com
```

### Commands

| Command | Description |
|---------|-------------|
| `orchestrator [flags]` | Run a suite |
| `orchestrator extract [flags]` | Generate `suite.json` from the tests |
| `orchestrator graph [flags]` | Render the dependency graph as Mermaid or DOT |
| `orchestrator validate [flags]` | Check a suite without running it |
| `orchestrator version` | Print the version and the protocol it speaks |

`extract` reads the metadata where it is declared — Playwright annotations, or a
header convention in agent scripts — validates the contracts, and writes the
suite. A key nobody provides, a key two tests provide, or a cycle is reported
against the file and line that declared it. `suite.json` is a build artefact; it
is not meant to be edited.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-suite` | `tests/suite.json` | Path to suite definition |
| `-executor` | `process` | How nodes are run: `process` or `playwright` |
| `-basedir` | `.` | Base directory for agent scripts or the Playwright project |
| `-baseurl` | `http://localhost:8000` | BASE_URL env var for agents |
| `-workers` | `4` | Max concurrent agents |
| `-agents` | | Agent pool as a JSON file or inline; default: universal agents |
| `-snapshot` | `logs/context-snapshot.json` | Context snapshot path |
| `-report` | `logs/report.json` | JSON report output path |
| `-timeout` | `300` | Global timeout in seconds |
| `-json` | `false` | Emit a machine-readable JSON event log |
| `-fail-fast` | `false` | Stop the run as soon as a BLOCKER node fails |
| `-max-failures` | `0` | Stop after this many failed nodes; 0 = no limit |
| `-global-context` | | File where `global`-scoped values persist between runs |
| `-html` | | Path for a single-file HTML report with a timeline |
| `-junit` | | Path for a JUnit XML report (GitLab, Jenkins) |

`extract` takes `-extractor` (`process` or `playwright`), `-basedir`, `-o` (output path, `-` for stdout), `-suite-id` and `-timeout`.
`graph` takes `-suite`, `-format` (`mermaid` or `dot`) and `-o`.

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | Every node passed |
| `1` | The suite ran and something in it failed |
| `2` | The suite could not be run: bad flags, unreadable suite, a node no agent can take, the global timeout |

`1` is a suite to investigate; `2` is the orchestrator or its configuration to fix. CI can tell them apart.

## Output example

```
╔══════════════════════════════════════════════════════╗
║         PlayDAG — Execution Report                   ║
╚══════════════════════════════════════════════════════╝
Suite:     scrum_board_regression
Started:   2026-05-06 12:00:00
Finished:  2026-05-06 12:00:47
Duration:  47.23s
── Results ─────────────────────────────────────────────
✓ PASSED  auth.register        2.14s
✓ PASSED  auth.login           1.87s
✓ PASSED  board.view           0.92s
✓ PASSED  card.create          1.03s
✓ PASSED  card.move            0.88s
✗ FAILED  card.search          timeout: context deadline exceeded
↷ SKIPPED card.archive         blocked by: card.search
✓ PASSED  card.deadline        0.77s
── Summary ──────────────────────────────────────────────
Total:    8 tests
Passed:   6  (75.0%)
Failed:   1  (12.5%)
Skipped:  1  (12.5%)
── Context Store Snapshot ───────────────────────────────
auth.session_key     → [provided by auth.login, scope: session]
auth.user_id         → [provided by auth.login, scope: session]
card.id              → [provided by card.create, scope: suite]
── Performance ──────────────────────────────────────────
Sequential estimate: 8.61s (sum of all durations)
Actual (parallel):   4.23s
Speedup:             2.04x
╔══════════════════════════════════════════════════════╗
║  Suite FAILED — 1 test(s) did not pass              ║
╚══════════════════════════════════════════════════════╝
```

## Running tests

```bash
# All unit tests
go test ./...

# With race detector
go test -race ./...

# Integration tests (requires python3)
go test -tags integration ./internal/runner/...

# Adapter (requires npm install at the repository root)
npm run build --workspace @playdag/playwright

# Vet and lint
go vet ./...
golangci-lint run ./...

# Coverage of the core packages
go test -cover ./internal/graph/ ./internal/scheduler/ ./internal/store/
```

## Suite JSON format

Generated by `orchestrator extract`; shown here because it is the contract
between extraction and execution.

```json
{
  "suite_id": "my_suite",
  "generated_at": "2026-05-06T00:00:00Z",
  "nodes": [
    {
      "test_id": "auth.login",
      "script": "tests/auth/login.py",
      "contract": {
        "requires": [],
        "provides": ["session.token"],
        "scope": "session"
      },
      "policy": {
        "criticality": "BLOCKER",
        "retries": 1,
        "timeout": "30s"
      }
    }
  ]
}
```

## Agents

An agent receives the request as JSON on **stdin** (and in `PLAYDAG_CONTEXT`) and writes one JSON response to **stdout**:

```json
{"schema_version": "1.0", "status": "PASSED", "provides": {"session.token": "abc123"}}
```

That is the whole contract, so an agent can be written in any language. See [docs/PROTOCOL.md](docs/PROTOCOL.md) for the specification, and [adapters/playwright](adapters/playwright/README.md) for an adapter that implements it inside a test framework.
