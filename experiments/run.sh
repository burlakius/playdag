#!/usr/bin/env bash
# Runs the experiment configurations against the demo application and writes one
# JSON per run for experiments/analyze.py to read.
#
#   ./experiments/run.sh [output-dir] [runs]
#
# The default output directory is outside this repository: measurements are
# evidence for the write-up, not source code.
#
# Configurations (each is one row of the experiment table):
#
#   e1   baseline: npx playwright test, workers=1 — every test signs in itself
#   e1p  baseline: npx playwright test, workers=4 — the same, run in parallel
#   e2   playdag, workers=1          — isolates context sharing alone
#   e3   playdag, workers=4          — context sharing plus dynamic parallelism
#   e4   playdag, workers=4, login broken     — what early termination saves
#   e4b  baseline, workers=4, login broken    — what it costs without it
#   sN   playdag, workers=N          — the scaling sweep behind Amdahl's law
#
# Every run gets a fresh server, so no run inherits another's state. The first
# run of each configuration is a warm-up and is discarded by the analysis.
set -euo pipefail

OUT_DIR="${1:-../experiments/results}"
RUNS="${2:-11}" # 1 warm-up + 10 measured
PORT="${PORT:-8990}"

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"

# Exported, not just set: the baseline reaches the application through
# playwright.config.ts, which reads BASE_URL from the environment. Without the
# export it would quietly measure connection refusals instead of a test suite.
export BASE_URL="http://localhost:${PORT}"
PROJECT="examples/playwright"
SUITE="$OUT_DIR/suite.json"

log() { printf '%s\n' "$*" >&2; }

# start_server brings up a fresh demo application. DEMO_BREAK is passed through,
# which is how the failure is injected.
start_server() {
  DEMO_BREAK="${1:-}" node examples/demo-app/server.js "$PORT" >"$OUT_DIR/server.log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 1 50); do
    if curl -sf -o /dev/null "$BASE_URL/login"; then return 0; fi
    sleep 0.1
  done
  log "server did not come up on $BASE_URL"
  return 1
}

stop_server() {
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
}

# record writes what a run cost, in the same shape for every configuration, so
# the analysis never has to know which tool produced it.
record() {
  local config=$1 run=$2 seconds=$3 exit_code=$4 report=${5:-}
  python3 - "$OUT_DIR/${config}_run_${run}.json" "$config" "$run" "$seconds" "$exit_code" "$report" <<'PY'
import json, os, sys

out, config, run, seconds, exit_code, report = sys.argv[1:7]
record = {
    "config": config,
    "run": int(run),
    "wall_clock_seconds": float(seconds),
    "exit_code": int(exit_code),
}

# A playdag run also reports what happened inside it; the baseline only has a
# wall clock, which is exactly the point of comparing them on wall clock.
if report and os.path.exists(report):
    with open(report) as f:
        data = json.load(f)
    record.update({
        "total_tests": data.get("total_tests"),
        "passed": data.get("passed"),
        "failed": data.get("failed"),
        "skipped": data.get("skipped"),
        "worker_seconds": data.get("worker_seconds"),
        "utilization": data.get("utilization"),
        "workers": data.get("workers"),
        "total_duration_ms": data.get("total_duration_ms"),
    })

with open(out, "w") as f:
    json.dump(record, f, indent=2)
PY
}

# time_it runs a command and prints how long it took, whatever its exit code:
# a failing run costs time too, which is the whole subject of E4.
time_it() {
  local start end
  start=$(date +%s.%N)
  set +e
  "$@" >>"$OUT_DIR/run.log" 2>&1
  local code=$?
  set -e
  end=$(date +%s.%N)
  LAST_SECONDS=$(python3 -c "print(f'{$end - $start:.4f}')")
  LAST_EXIT=$code
}

run_baseline() {
  local config=$1 workers=$2 brk=${3:-} retries=${4:-0}
  log "── $config: baseline playwright, workers=$workers ${brk:+(login broken)}"
  for i in $(seq 1 "$RUNS"); do
    start_server "$brk"
    # --fully-parallel is what makes --workers mean anything: without it
    # Playwright runs a file's tests in one worker, so a "parallel" baseline
    # would quietly be a sequential one and every comparison against it would
    # flatter the orchestrator. It is also what a generated Playwright config
    # ships with, so this is the ordinary way to run these tests.
    #
    # Retries are matched to the orchestrated suite's policy, not left at zero:
    # letting only one side retry a broken node would measure the policy rather
    # than the method.
    time_it npx playwright test tests/baseline.spec.ts --workers="$workers" --fully-parallel \
      --retries="$retries" --config="$PROJECT/playwright.config.ts"
    stop_server
    record "$config" "$i" "$LAST_SECONDS" "$LAST_EXIT"
    log "   run $i/$RUNS: ${LAST_SECONDS}s (exit $LAST_EXIT)"
  done
}

run_playdag() {
  local config=$1 workers=$2 brk=${3:-} extra=${4:-}
  log "── $config: playdag, workers=$workers ${brk:+(login broken)} ${extra}"
  for i in $(seq 1 "$RUNS"); do
    start_server "$brk"
    time_it ./orchestrator -executor playwright -suite "$SUITE" -basedir "$PROJECT" \
      -baseurl "$BASE_URL" -workers "$workers" $extra \
      -report "$OUT_DIR/${config}_report_${i}.json" \
      -snapshot "$OUT_DIR/${config}_snapshot_${i}.json"
    stop_server
    record "$config" "$i" "$LAST_SECONDS" "$LAST_EXIT" "$OUT_DIR/${config}_report_${i}.json"
    log "   run $i/$RUNS: ${LAST_SECONDS}s (exit $LAST_EXIT)"
  done
}

trap stop_server EXIT

log "building the orchestrator"
go build -o orchestrator ./cmd/orchestrator/

log "extracting the suite from the tests"
./orchestrator extract -extractor playwright -basedir "$PROJECT" -o "$SUITE"

: >"$OUT_DIR/run.log"

# CONFIGS selects what to measure; the default is everything. Naming a subset is
# for re-measuring one configuration after a fix, not for cherry-picking results.
CONFIGS="${CONFIGS:-e1 e1p e2 e3 e4 e4b s1 s2 s4 s8}"
wanted() { [[ " $CONFIGS " == *" $1 "* ]]; }

wanted e1  && run_baseline e1 1
wanted e1p && run_baseline e1p 4
wanted e2  && run_playdag e2 1
wanted e3  && run_playdag e3 4
wanted e4  && run_playdag e4 4 login "-fail-fast"
# The suite gives auth.login one retry, so the baseline gets one too.
wanted e4b && run_baseline e4b 4 login 1

for w in 1 2 4 8; do
  wanted "s${w}" && run_playdag "s${w}" "$w"
done

true

log ""
log "wrote $(ls "$OUT_DIR"/*_run_*.json | wc -l) run files to $OUT_DIR"
log "now run: python3 experiments/analyze.py $OUT_DIR"
