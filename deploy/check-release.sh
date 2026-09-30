#!/bin/sh
# Runs release checks against the PostgreSQL that TEST_DATABASE_URL points at.
# Each test creates and drops its own schema, so any reachable PostgreSQL works.
# If it is a container on a Docker network, set TEST_DATABASE_NETWORK to that network
# and use the container name as the host in TEST_DATABASE_URL.
set -eu

: "${TEST_DATABASE_URL:?TEST_DATABASE_URL must point at a reachable PostgreSQL}"
: "${GO_IMAGE:=golang:1.27-bookworm}"
: "${RUNTIME_IMAGE:=gcr.io/distroless/static-debian12:nonroot}"
: "${GOPROXY:=https://proxy.golang.org,direct}"
: "${DEBIAN_MIRROR:=http://deb.debian.org/debian}"
: "${DEBIAN_SECURITY_MIRROR:=http://deb.debian.org/debian-security}"

suffix=$(head -c 8 /dev/urandom | od -An -tx1 | tr -d ' \n')
name="sms-gateway-test-$suffix"
cleanup() {
  docker rm -f "$name-go" >/dev/null 2>&1 || true
  docker image rm "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 1' INT TERM

docker build --pull=false --target test \
  --build-arg GO_IMAGE="$GO_IMAGE" \
  --build-arg RUNTIME_IMAGE="$RUNTIME_IMAGE" \
  --build-arg GOPROXY="$GOPROXY" \
  --build-arg DEBIAN_MIRROR="$DEBIAN_MIRROR" \
  --build-arg DEBIAN_SECURITY_MIRROR="$DEBIAN_SECURITY_MIRROR" \
  -f deploy/Dockerfile -t "$name" .
docker run --rm --name "$name-go" \
  ${TEST_DATABASE_NETWORK:+--network "$TEST_DATABASE_NETWORK"} \
  -e TEST_DATABASE_URL="$TEST_DATABASE_URL" \
  "$name"
