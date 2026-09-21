#!/usr/bin/env bash
# Runs the benchmark suite (B1-B7) against a running stack started with the
# bench compose profile, and writes raw results to loadtest/results/<time>/.
# PostgreSQL statistics are sampled every 5 seconds during the runs.
set -euo pipefail

cd "$(dirname "$0")/../.."
K6=${K6:-k6}
COMPOSE=${COMPOSE:-"docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.bench.yml"}
OUT="loadtest/results/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$OUT"

sample_postgres() {
  echo "time,xact_commit,wal_bytes" > "$OUT/postgres.csv"
  while true; do
    $COMPOSE exec -T postgres psql -U gateway -d gateway -At -F, -c \
      "SELECT now(), (SELECT sum(xact_commit) FROM pg_stat_database), (SELECT wal_bytes FROM pg_stat_wal)" \
      >> "$OUT/postgres.csv" 2>/dev/null || true
    sleep 5
  done
}
sample_postgres &
SAMPLER=$!
trap 'kill $SAMPLER 2>/dev/null || true' EXIT

run() {
  local name=$1; shift
  echo "== $name"
  "$K6" run --quiet --summary-export "$OUT/$name.json" "$@" | tee "$OUT/$name.txt"
}

run B1-accept loadtest/bench/accept.js
run B2-accept-one-customer -e CUSTOMERS=1 loadtest/bench/accept.js
run B3-batch-100 -e BATCH=100 loadtest/bench/batch.js
run B3-batch-500 -e BATCH=500 loadtest/bench/batch.js
for rate in ${E2E_RATES:-500 1000 2000 4000}; do
  run "B5-e2e-$rate" -e RATE="$rate" loadtest/bench/e2e.js || true
done
run B6-express-under-load loadtest/scenarios/express-under-load.js || true
run B7-dlr loadtest/bench/dlr.js

echo "== B4-dispatch"
TEST_DATABASE_URL=${TEST_DATABASE_URL:-postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable} \
  go test -run '^$' -bench BenchmarkDispatch -benchtime 20000x ./internal/dispatch/ | tee "$OUT/B4-dispatch.txt"

$COMPOSE exec -T gateway-api gateway check | tee "$OUT/invariants.txt"
echo "Results written to $OUT"
