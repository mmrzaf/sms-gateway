#!/usr/bin/env bash
# Restarts PostgreSQL during load. The API answers 503 while it is down;
# afterwards every accepted message is dispatched and invariants hold.
source "$(dirname "$0")/lib.sh"

start_load
log "restarting postgres"
$COMPOSE restart postgres
finish
