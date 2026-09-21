# syntax=docker/dockerfile:1

FROM golang:1.26 AS build
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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
USER nonroot:nonroot
CMD ["gateway", "serve"]
