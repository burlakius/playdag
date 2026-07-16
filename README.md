# playdag

**playdag** is a minimal DAG orchestrator for end-to-end test suites. It reads a JSON suite definition, resolves the data dependencies between test nodes, runs them in the correct order with controlled parallelism, and produces both a human-readable execution report and a machine-readable JSON snapshot — letting you run complex, stateful test sequences as reliably and quickly as your infrastructure allows.

Values flow between tests through a shared context, so a session established by one test is reused by the tests that depend on it instead of being established again.

The core is framework-agnostic: it speaks the [agent protocol](docs/PROTOCOL.md) and dispatches through an `Executor`. Playwright is one adapter ([`adapters/playwright`](adapters/playwright/README.md)), not a dependency — see [`examples/playwright`](examples/playwright/README.md) for a browser suite and [`examples/process`](examples/process/README.md) for the same graph run by plain Python agents.

## Architecture

```
suite.json
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
│          │  SaveJSON() → logs/report.json
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
# Generic agents: any executable that speaks the protocol
./orchestrator -suite examples/process/suite.json -basedir examples/process

# The same graph, run as real Playwright tests
./orchestrator -executor playwright -suite examples/playwright/suite.json -basedir examples/playwright

# Run with 2 parallel workers
./orchestrator -suite examples/process/suite.json -workers 2

# Run against another environment
./orchestrator -suite examples/process/suite.json -baseurl http://staging.example.com
```

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

Exit code is `0` if all tests pass, `1` if any fail or the global timeout fires.

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

# Vet
go vet ./...
```

## Suite JSON format

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
