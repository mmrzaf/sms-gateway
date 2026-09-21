# SMS Gateway

A multi-tenant SMS gateway backend written in Go. Customers send SMS messages through a REST API and pay in credits; the gateway dispatches messages to SMS providers with retries, failover, and delivery tracking, and offers an Express service class with reserved capacity and latency tracking.

The system is designed for 100 million messages per day (10,000 messages per second at peak) and uses PostgreSQL as both the source of truth and the dispatch queue.

## Documentation

The design, API, and operations are documented in [`docs/`](docs/README.md). Good starting points:

- [Problem and scope](docs/010-overview/010-problem-and-scope.md)
- [Components](docs/020-architecture/020-components.md)
- [Architecture decisions](docs/110-decisions/README.md)

## Quick start

Requirements: Docker with Compose v2, Go (version in `go.mod`), and GNU Make.

```sh
make up        # build and start PostgreSQL, migrations, API, and worker
make down      # stop the stack
```

## Development

```sh
make test               # unit tests
make test-integration   # all tests, against the compose PostgreSQL
make lint               # go vet and staticcheck
make run                # run the gateway on the host in the "all" role
make help               # every target
```

Tests that need PostgreSQL read `TEST_DATABASE_URL`; each test runs in its own schema.
