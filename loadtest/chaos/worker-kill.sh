#!/usr/bin/env bash
# Kills the worker every 60 s and restarts it 10 s later. Leased messages are
# reclaimed after their lease expires; nothing may be lost.
source "$(dirname "$0")/lib.sh"

start_load
for _ in 1 2 3; do
  log "killing gateway-worker"
  $COMPOSE kill gateway-worker
  sleep 10
  log "starting gateway-worker"
  $COMPOSE up -d gateway-worker
  sleep 50
done
finish
