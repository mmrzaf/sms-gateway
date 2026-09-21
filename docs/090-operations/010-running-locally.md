# Running Locally

How to start the system, create demo data, and run tests and load scenarios on a development machine.

## Prerequisites

| Tool | Version | Used for |
|---|---|---|
| Docker with Compose v2 | Recent | Running the stack and integration tests |
| Go | As pinned in `go.mod` | Building and unit tests |
| GNU Make | Any | Task shortcuts |
| k6 | Recent | Load and scenario tests |

## Start the stack

```sh
cp deploy/.env.example deploy/.env    # once; defaults work for local use
make up
```

`make up` builds the images and starts PostgreSQL, the migration job, the gateway API, one gateway worker, and providers A and B. When it returns, the services are healthy.

| URL | What |
|---|---|
| http://localhost:8080/docs | API reference (Swagger UI) |
| http://localhost:8080/v1/... | Customer API |
| http://localhost:8081/dashboard | Dashboard (user `admin`, password `ADMIN_TOKEN`) |
| http://localhost:9001/admin/config | Provider A settings |
| http://localhost:9002/admin/config | Provider B settings |

Create the demo customers and export their API keys into the current shell:

```sh
eval "$(make -s seed)"
```

`make seed` prints one `NAME_KEY=sk_...` line per customer (`ACME_KEY`, `BULK_KEY`, `QUICK_KEY`).

| Customer | Credits | Rate limit (msg/s) |
|---|---|---|
| `acme` | 10,000 | 100 |
| `bulkco` | 1,000,000 | 2,000 |
| `quickpay` | 50,000 | 200 |

Running `make seed` again rotates the keys of existing demo customers and prints the new keys; balances are not changed.

## Make targets

| Target | Action |
|---|---|
| `make up` | Build and start the stack |
| `make down` | Stop the stack and keep data |
| `make reset` | Stop the stack and delete the database volume |
| `make logs` | Follow logs of all services |
| `make db` | Start only PostgreSQL |
| `make migrate` | Apply migrations to the stack's database from the host |
| `make seed` | Create or refresh demo customers and print their keys |
| `make check` | Run the invariant checker against the stack |
| `make run` | Run `gateway serve --role=all` on the host against the stack's PostgreSQL |
| `make build` | Build both binaries into `bin/` with version information |
| `make test` | Unit tests; tests that need PostgreSQL are skipped |
| `make test-integration` | All tests, against the stack's PostgreSQL |
| `make lint` | `go vet` and `staticcheck` |
| `make fmt` | Format all Go code |
| `make loadtest SCENARIO=<name>` | Run one k6 scenario; see [Test scenarios](../100-testing/020-scenarios.md) |
| `make chaos SCENARIO=<name>` | Run one failure-injection scenario against the stack; see [Test scenarios](../100-testing/020-scenarios.md#chaos-scenarios) |
| `make bench` | Run the benchmark suite; see [Benchmarks](../080-scalability/020-benchmarks.md) |
| `make help` | List every target |

## Running without Docker

With a PostgreSQL 16 or later instance available:

```sh
export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway
export ADMIN_TOKEN=admin PROVIDER_SECRET=dev-secret
export PROVIDERS="A=http://localhost:9001,B=http://localhost:9002"

go run ./cmd/gateway migrate
PROVIDER_NAME=A PROVIDER_ADDR=:9001 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr go run ./cmd/provider &
PROVIDER_NAME=B PROVIDER_ADDR=:9002 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr go run ./cmd/provider &
go run ./cmd/gateway serve --role=all
```

## Related

- [Demo walkthrough](../010-overview/030-demo-walkthrough.md)
- [Configuration](020-configuration.md)
- [Deployment](030-deployment.md)
