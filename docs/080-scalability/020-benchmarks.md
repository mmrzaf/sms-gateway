# Benchmarks

How the capacity estimates are measured: the benchmark suite, the method, and the results format.

## Principle

The deployed system runs on a single node, so the benchmarks measure each component's throughput separately and on one machine, and the [Capacity analysis](010-capacity-analysis.md) scales those numbers to the target. A component measured in isolation shows its own ceiling; an end-to-end run shows which ceiling is reached first.

## Environment

Every result records:

- CPU model and core count, RAM, storage type.
- PostgreSQL version and non-default settings.
- Gateway commit, Go version.
- Number of API and worker processes and their concurrency settings.
- Fake provider settings (latency 0 and failure rate 0 unless stated).

## Suite

| ID | Benchmark | Tool | Measures |
|---|---|---|---|
| B1 | Accept, single messages, many customers | k6 `bench-accept.js` | Messages/s and latency at the API with no row contention |
| B2 | Accept, single messages, one customer | k6 `bench-accept.js --env CUSTOMERS=1` | The per-customer row ceiling |
| B3 | Accept, batches of 100 and 500 | k6 `bench-batch.js` | Messages/s with amortized commits |
| B4 | Dispatch only | Go benchmark `BenchmarkDispatch` | Claim + send + complete throughput from a pre-filled queue, provider latency 0 |
| B5 | End to end | k6 `bench-e2e.js` | Sustained rate at which queue depth stays flat |
| B6 | Express under normal saturation | k6 `express-under-load.js` | Express accept-to-sent p50/p95/p99 while normal lanes are saturated |
| B7 | DLR intake | k6 `bench-dlr.js` | DLRs/s through the batcher |

Each benchmark runs for 60 seconds after a 15-second warm-up. The reported value is the median of three runs.

## Method

1. `make up` with the benchmark compose profile, then `make seed`.
2. `make bench` runs B1–B7 in order and writes raw results to `loadtest/results/<timestamp>/`.
3. During each run, PostgreSQL statistics are sampled every 5 seconds: commits/s (`pg_stat_database.xact_commit`), WAL bytes/s (`pg_stat_wal.wal_bytes`), CPU, and I/O utilization.
4. After each run, `gateway check` must pass. A benchmark whose run violates an invariant is invalid regardless of its throughput.

## Results

The Measured column is filled from the output of `make bench` on the reference machine described in the environment section above. The At-target column states the requirement at 10,000 messages/s from the [Capacity analysis](010-capacity-analysis.md).

| ID | Metric | Measured | At target |
|---|---|---|---|
| B1 | Accepts/s (single) | — | 10,000 across API instances |
| B1 | Accept latency p50 / p99 at 50% of max | — | p99 < 100 ms |
| B2 | Accepts/s for one customer | — | Informs per-customer limits |
| B3 | Accepts/s (batch 100 / 500) | — | 10,000 |
| B4 | Dispatched/s per worker | — | 10,000 across workers |
| B5 | Sustained end-to-end messages/s | — | 10,000 |
| B6 | Express accept→sent p99 | — | < 2 s |
| B7 | DLRs/s | — | 10,000 |
| — | PostgreSQL commits/s at max (B1) | — | ≈ 10,000 |
| — | WAL MB/s at max (B5) | — | 30–50 |

## Related

- [Capacity analysis](010-capacity-analysis.md)
- [Test scenarios](../100-testing/020-scenarios.md)
