# Shared helpers for chaos scenarios. Each scenario runs the steady load in
# the background, injects a failure, waits for the load to finish, lets the
# backlog drain, and runs the invariant checker.

set -euo pipefail
cd "$(dirname "$0")/../.."

K6=${K6:-k6}
COMPOSE=${COMPOSE:-"docker compose -f deploy/docker-compose.yml"}
DURATION=${DURATION:-4m}

start_load() {
  CHAOS=1 DURATION="$DURATION" "$K6" run --quiet loadtest/scenarios/steady.js &
  LOAD_PID=$!
  sleep 20
}

finish() {
  wait "$LOAD_PID"
  echo "== invariants"
  $COMPOSE exec -T gateway-api gateway check
}

log() {
  echo "== $(date -u +%H:%M:%S) $*"
}
