#!/usr/bin/env bash
# Restarts provider A during load. Its deduplication store and pending
# delivery reports are lost, so some messages stay sent; invariants must hold.
source "$(dirname "$0")/lib.sh"

start_load
log "restarting provider-a"
$COMPOSE restart provider-a
finish
