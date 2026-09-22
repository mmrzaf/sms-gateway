# Common development tasks. Run "make help" for the list.

COMPOSE      := docker compose -f deploy/docker-compose.yml
COMPOSE_BENCH := $(COMPOSE) -f deploy/docker-compose.bench.yml
K6           ?= k6
SCENARIO     ?= steady
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT       ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS      := -s -w \
                -X github.com/mmrzaf/sms-gatway/internal/buildinfo.Version=$(VERSION) \
                -X github.com/mmrzaf/sms-gatway/internal/buildinfo.Commit=$(COMMIT)
TEST_DATABASE_URL ?= postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
LOCAL_ENV    := DATABASE_URL=$(TEST_DATABASE_URL) ADMIN_TOKEN=admin \
                PROVIDER_SECRET=local-provider-secret \
                PROVIDERS=A=http://localhost:9001,B=http://localhost:9002

export VERSION COMMIT

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

deploy/.env:
	cp deploy/.env.example deploy/.env

.PHONY: up
up: deploy/.env ## Build and start the stack
	$(COMPOSE) up -d --build --wait

.PHONY: down
down: ## Stop the stack and keep data
	$(COMPOSE) down

.PHONY: reset
reset: ## Stop the stack and delete the database volume
	$(COMPOSE) down -v

.PHONY: logs
logs: ## Follow logs of all services
	$(COMPOSE) logs -f

.PHONY: db
db: deploy/.env ## Start only PostgreSQL
	$(COMPOSE) up -d --wait postgres

.PHONY: migrate
migrate: db ## Apply migrations to the stack's database from the host
	$(LOCAL_ENV) go run ./cmd/gateway migrate

.PHONY: seed
seed: ## Create or refresh demo customers and print their API keys
	@$(COMPOSE) exec -T gateway-api gateway seed

.PHONY: check
check: ## Run the invariant checker against the stack
	@$(COMPOSE) exec -T gateway-api gateway check

.PHONY: run
run: migrate ## Run the gateway on the host in the "all" role
	$(LOCAL_ENV) go run ./cmd/gateway serve --role=all

.PHONY: local
local: ## Run the whole system on the host against a local PostgreSQL, no Docker (migrates, starts fake providers A and B, then the gateway)
	@trap 'kill %1 %2 2>/dev/null' EXIT; \
	PROVIDER_SECRET=local-provider-secret PROVIDER_NAME=A PROVIDER_ADDR=:9001 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr go run ./cmd/provider & \
	PROVIDER_SECRET=local-provider-secret PROVIDER_NAME=B PROVIDER_ADDR=:9002 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr go run ./cmd/provider & \
	sleep 1; \
	$(LOCAL_ENV) go run ./cmd/gateway migrate; \
	$(LOCAL_ENV) go run ./cmd/gateway serve --role=all

.PHONY: build
build: ## Build binaries into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

.PHONY: test
test: ## Run unit tests (database tests are skipped)
	go test -race -count=1 ./...

.PHONY: test-integration
test-integration: db ## Run all tests, including those that need PostgreSQL
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 ./...

.PHONY: lint
lint: ## Run go vet and staticcheck
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

.PHONY: loadtest
loadtest: ## Run one k6 scenario against the stack (SCENARIO=name)
	$(K6) run loadtest/scenarios/$(SCENARIO).js

.PHONY: chaos
chaos: ## Run one failure-injection scenario against the stack (SCENARIO=name)
	loadtest/chaos/$(SCENARIO).sh

.PHONY: bench
bench: deploy/.env ## Start the stack with the bench profile and run the benchmark suite
	$(COMPOSE_BENCH) up -d --build --wait
	COMPOSE="$(COMPOSE_BENCH)" K6="$(K6)" loadtest/bench/run.sh

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -w -s .
