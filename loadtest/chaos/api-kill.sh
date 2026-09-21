#!/usr/bin/env bash
# Kills and restarts the API during load. Requests either complete or fail at
# the client; the load retries unanswered requests with the same client_ref,
# which must not create duplicates.
source "$(dirname "$0")/lib.sh"

start_load
log "killing gateway-api"
$COMPOSE kill gateway-api
sleep 5
log "starting gateway-api"
$COMPOSE up -d gateway-api
finish
