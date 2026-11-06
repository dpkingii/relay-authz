#!/usr/bin/env bash
# Runs the load test in four modes at each rate, RUNS times each, and appends one JSON
# line per run to loadtest/results/<LABEL>.jsonl:
#   uncached         server without Redis, the baseline
#   cached           default checks, which need the latest revision
#   cached-at-least  checks that accept results from the seed revision on
#   cached-writes    default checks with 1% of requests writing
# Redis is flushed before every cached run so each one starts cold.
#
# Everything, k6 included, runs inside Docker's network. On a Mac, Docker Desktop's port
# forwarding becomes the bottleneck long before the server does.
#
#   LABEL=m4pro RATES="1000 2000 4000" RUNS=3 DURATION=60s loadtest/run-all.sh
set -euo pipefail
cd "$(dirname "$0")/.."

RATES=${RATES:-"1000 2000 4000"}
RUNS=${RUNS:-3}
DURATION=${DURATION:-60s}
mkdir -p loadtest/results
out=loadtest/results/${LABEL:-local}.jsonl

# An empty REDIS_URL runs the server uncached.
server() {
  REDIS_URL=$1 docker compose --profile server up -d --wait >/dev/null 2>&1
}

run() {
  local mode=$1 rate=$2
  shift 2
  [ "$mode" = uncached ] || docker compose exec -T redis redis-cli flushall >/dev/null
  docker compose --profile loadtest run --rm k6 run -q -e RATE="$rate" -e DURATION="$DURATION" "$@" /scripts/check.js 2>/dev/null |
    tail -1 | sed "s/^{/{\"mode\":\"$mode\",/" | tee -a "$out"
}

docker compose --profile server build -q
for rate in $RATES; do
  for _ in $(seq "$RUNS"); do
    server ""
    run uncached "$rate"
    server redis://redis:6379
    run cached "$rate"
    run cached-at-least "$rate" -e AT_LEAST=1
    run cached-writes "$rate" -e WRITE_RATIO=0.01
  done
done
echo "results in $out"
