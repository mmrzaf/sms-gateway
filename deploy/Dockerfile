ARG GO_IMAGE=golang:1.26-bookworm
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM ${GO_IMAGE} AS build

ARG DEBIAN_MIRROR=http://linux-mirror.liara.ir/repository/debian
ARG DEBIAN_SECURITY_MIRROR=http://linux-mirror.liara.ir/repository/debian-security
ARG GOPROXY=https://mirror.abrha.net/repository/go/,direct

ENV GOPROXY=${GOPROXY} \
    GOTOOLCHAIN=local

RUN set -eu; \
    rm -f /etc/apt/sources.list.d/debian.sources; \
    printf 'deb %s bookworm main\ndeb %s bookworm-updates main\ndeb %s bookworm-security main\n' \
        "$DEBIAN_MIRROR" "$DEBIAN_MIRROR" "$DEBIAN_SECURITY_MIRROR" > /etc/apt/sources.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends git ca-certificates; \
    rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown

RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -ldflags "-s -w \
        -X github.com/mmrzaf/sms-gatway/internal/buildinfo.Version=${VERSION} \
        -X github.com/mmrzaf/sms-gatway/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/ ./cmd/...

FROM ${RUNTIME_IMAGE}

COPY --from=build /out/ /usr/local/bin/

USER nonroot:nonroot

CMD ["gateway", "serve"]
