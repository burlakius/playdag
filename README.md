# playdag

**playdag** is a minimal DAG orchestrator for Playwright test suites. It reads a JSON suite definition, resolves data dependencies between test nodes, executes them as Python subprocesses in the correct order (with controlled parallelism), and produces both a human-readable execution report and a machine-readable JSON snapshot — letting you run complex, stateful end-to-end test sequences as reliably and quickly as your infrastructure allows.

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
┌──────────┐   goroutines (semaphore-limited)
│  runner  │──────────────────────────────────────────────┐
│          │                                              │
│  retries │  stdin: JSON context                        ▼
│  timeout │  ─────────────────────►  python agent subprocess
│          │  stdout: AgentResult JSON  ◄────────────────
└────┬─────┘
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

Requires Go 1.24+ and Python 3.

## Usage

```bash
# Run with defaults (Django on localhost:8000)
./orchestrator -suite ../scrum_board/tests/suite.json -basedir ../scrum_board

# Run with 2 parallel workers
./orchestrator -suite ../scrum_board/tests/suite.json -workers 2

# Run with custom base URL
./orchestrator -suite ../scrum_board/tests/suite.json -baseurl http://staging.example.com
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-suite` | `tests/suite.json` | Path to suite definition |
| `-basedir` | `.` | Base directory for python scripts |
| `-baseurl` | `http://localhost:8000` | BASE_URL env var for agents |
| `-workers` | `4` | Max concurrent agents |
| `-snapshot` | `logs/context-snapshot.json` | Live context snapshot path |
| `-report` | `logs/report.json` | JSON report output path |
| `-timeout` | `300` | Global timeout in seconds |

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

Each agent script receives the required context keys as a JSON object on **stdin** and must write a JSON object to **stdout**:

```json
{"status": "PASSED", "provides": {"session.token": "abc123"}}
```
