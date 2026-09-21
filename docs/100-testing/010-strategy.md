# Test Strategy

What is tested at which level, and why the emphasis is on correctness under concurrency rather than on coverage numbers.

## Principles

- **Correctness claims are proven by tests, not argued.** Every guarantee in [Credits and billing](../030-domain/020-credits-and-billing.md), [Idempotency](../040-data/020-idempotency.md), and [Failure modes](../070-reliability/010-failure-modes.md) has a test that attacks it.
- **Concurrency is tested against a real database.** Row locks, `SKIP LOCKED`, unique constraints, and `NOTIFY` behave only in PostgreSQL; mocks would test the mocks.
- **Every non-unit test ends with the invariant checker.** A test that passes its own assertions but leaves a violated invariant fails.
- **Time is controlled, not waited on.** Durations such as leases, backoff, TTLs, and SLA are configuration, and tests set them to milliseconds.

## Levels

| Level | Scope | Runs against | Command | When |
|---|---|---|---|---|
| Unit | Pure logic | Nothing | `make test` | Every change |
| Integration | Package behavior with the database and an in-process fake provider | PostgreSQL from `TEST_DATABASE_URL` | `make test-integration` | Every change |
| Scenario | The whole system over HTTP | The compose stack | `make loadtest SCENARIO=...` | Before release; demonstration |
| Chaos | Recovery from process and dependency failures | The compose stack | `make chaos SCENARIO=...` | Before release |
| Benchmark | Throughput and latency ceilings | The compose stack with the bench profile | `make bench` | When performance-relevant code changes |

## Unit tests

| Area | Cases |
|---|---|
| Segments | GSM-7 and UCS-2 detection; every row of the examples table in [Segments and pricing](../030-domain/030-segments-and-pricing.md); escape sequences and surrogate pairs on segment boundaries; the maximum-length limit |
| Pricing | Cost for each class and segment count |
| Validation | E.164 patterns, `client_ref` characters and length, batch size, unknown fields |
| Retry policy | Backoff bounds for every attempt; attempts-exhausted and TTL termination |
| Provider selection | Normal priority and failover; Express rotation; skipping unusable providers |
| Circuit breaker | Every state transition; single probe in half-open |
| Error classification | Every row of the classification table |
| Configuration | Defaults, each validation rule, and the combined error message |
| Pagination | Cursor round trip; UUIDv7 time bounds |

Table-driven tests are used throughout.

## Integration tests

Integration tests run against the PostgreSQL named by `TEST_DATABASE_URL`: the compose database locally (`make test-integration` starts it) and a service container in CI. Each test creates its own schema, applies the migrations into it, and drops it at the end, so tests are isolated from one another and can run in parallel against one database. When `TEST_DATABASE_URL` is not set, these tests are skipped, so `go test ./...` works anywhere.

Providers are `httptest` servers running the fake provider's handler, so their behavior can be scripted per test.

The cases are listed in [Test scenarios](020-scenarios.md#integration-tests).

## Scenario, chaos, and benchmark tests

k6 scripts in `loadtest/` drive the running system through the public and admin APIs. Each scenario states its setup, load, and pass criteria, and calls the invariant endpoint at the end. Chaos scenarios combine a scenario with scripted failures (killing or restarting containers, toggling provider outages). See [Test scenarios](020-scenarios.md) and [Benchmarks](../080-scalability/020-benchmarks.md).

## Related

- [Test scenarios](020-scenarios.md)
- [Invariants](../070-reliability/020-invariants.md)
