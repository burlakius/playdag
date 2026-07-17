#!/usr/bin/env bash
# Measures where orchestration starts to pay.
#
#   ./experiments/sensitivity.sh [output-dir] [runs]
#
# The main sweep answers "is it faster on this suite". This one answers the more
# useful question: "on what suites is it faster at all". Sharing a session saves
# the cost of establishing it, once per test that no longer has to; running each
# node in its own framework process costs a fixed startup each time. The balance
# therefore depends on one number — how expensive the repeated operation is —
# and that is what this varies, by making the demo application's login cost
# DEMO_LOGIN_MS.
#
# For each cost it runs the baseline and the orchestrated suite, so the crossing
# point of the two curves is measured rather than argued.
set -euo pipefail

OUT_DIR="${1:-../experiments/sensitivity}"
RUNS="${2:-6}" # 1 warm-up + 5
PORT="${PORT:-8991}"
DELAYS="${DELAYS:-0 500 1000 2000 3000}"

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"
mkdir -p "$OUT_DIR"
OUT_DIR="$(cd "$OUT_DIR" && pwd)"

export BASE_URL="http://localhost:${PORT}"
PROJECT="examples/playwright"
SUITE="$OUT_DIR/suite.json"

log() { printf '%s\n' "$*" >&2; }

start_server() {
  DEMO_LOGIN_MS="$1" node examples/demo-app/server.js "$PORT" >"$OUT_DIR/server.log" 2>&1 &
  SERVER_PID=$!
  for _ in $(seq 1 50); do
    curl -sf -o /dev/null "$BASE_URL/login" && return 0
    sleep 0.1
  done
  log "server did not come up"
  return 1
}

stop_server() {
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
}

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

record() {
  python3 - "$OUT_DIR/$1_run_$2.json" "$1" "$2" "$3" "$4" "$5" <<'PY'
import json, sys
out, config, run, seconds, code, delay = sys.argv[1:7]
with open(out, "w") as f:
    json.dump({
        "config": config,
        "run": int(run),
        "wall_clock_seconds": float(seconds),
        "exit_code": int(code),
        "login_ms": int(delay),
    }, f, indent=2)
PY
}

trap stop_server EXIT

log "building the orchestrator"
go build -o orchestrator ./cmd/orchestrator/
./orchestrator extract -extractor playwright -basedir "$PROJECT" -o "$SUITE"
: >"$OUT_DIR/run.log"

for delay in $DELAYS; do
  log "── login costs ${delay}ms"

  for i in $(seq 1 "$RUNS"); do
    start_server "$delay"
    # --fully-parallel for the same reason as in run.sh: without it Playwright
    # runs the file's tests in one worker and the baseline is not the parallel
    # one it claims to be.
    time_it npx playwright test tests/baseline.spec.ts --workers=4 --fully-parallel \
      --config="$PROJECT/playwright.config.ts"
    stop_server
    record "baseline-${delay}" "$i" "$LAST_SECONDS" "$LAST_EXIT" "$delay"
  done
  log "   baseline done"

  for i in $(seq 1 "$RUNS"); do
    start_server "$delay"
    time_it ./orchestrator -executor playwright -suite "$SUITE" -basedir "$PROJECT" \
      -baseurl "$BASE_URL" -workers 4 \
      -report "$OUT_DIR/playdag-${delay}_report_${i}.json" \
      -snapshot "$OUT_DIR/playdag-${delay}_snapshot_${i}.json"
    stop_server
    record "playdag-${delay}" "$i" "$LAST_SECONDS" "$LAST_EXIT" "$delay"
  done
  log "   playdag done"
done

log ""
log "now run: python3 experiments/sensitivity.py $OUT_DIR"
