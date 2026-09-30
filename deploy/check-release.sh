#!/bin/sh
# Runs release checks against an isolated, disposable PostgreSQL container.
set -eu

: "${GO_IMAGE:=golang:1.27-bookworm}"
: "${RUNTIME_IMAGE:=gcr.io/distroless/static-debian12:nonroot}"
: "${GOPROXY:=https://proxy.golang.org,direct}"
: "${DEBIAN_MIRROR:=http://deb.debian.org/debian}"
: "${DEBIAN_SECURITY_MIRROR:=http://deb.debian.org/debian-security}"

suffix=$(head -c 8 /dev/urandom | od -An -tx1 | tr -d ' \n')
name="sms-gateway-test-$suffix"
cleanup() {
  docker rm -f "$name-go" "$name-db" >/dev/null 2>&1 || true
  docker network rm "$name" >/dev/null 2>&1 || true
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
docker network create "$name" >/dev/null
docker run -d --name "$name-db" --network "$name" --network-alias postgres \
  -e POSTGRES_USER=gateway -e POSTGRES_PASSWORD=gateway -e POSTGRES_DB=gateway \
  postgres:17-alpine >/dev/null
ready=false
for attempt in $(seq 1 30); do
  if docker exec "$name-db" pg_isready -U gateway -d gateway >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  docker logs "$name-db" >&2
  exit 1
fi
docker run --rm --name "$name-go" --network "$name" \
  -e TEST_DATABASE_URL='postgres://gateway:gateway@postgres:5432/gateway?sslmode=disable' \
  "$name"
