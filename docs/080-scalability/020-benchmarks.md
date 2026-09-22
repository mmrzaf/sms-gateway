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

| ID | Benchmark | Command | Measures |
|---|---|---|---|
| B1 | Accept, single messages, 50 customers | `k6 run loadtest/bench/accept.js` | Messages/s and latency at the API without row contention |
| B2 | Accept, single messages, one customer | `k6 run -e CUSTOMERS=1 loadtest/bench/accept.js` | The per-customer row ceiling |
| B3 | Accept, batches of 100 and 500 | `k6 run -e BATCH=100 loadtest/bench/batch.js` | Messages/s with amortized commits |
| B4 | Dispatch only | `go test -run '^$' -bench BenchmarkDispatch -benchtime 20000x ./internal/dispatch/` | Claim, send, and complete throughput of one worker from a pre-filled queue, provider latency 0 |
| B5 | End to end | `k6 run -e RATE=<n> loadtest/bench/e2e.js` | Whether dispatch keeps up with an offered rate; the highest rate that keeps up is the sustained rate |
| B6 | Express under normal saturation | `k6 run loadtest/scenarios/express-under-load.js` | Express accept-to-sent p99 while normal lanes are saturated |
| B7 | DLR intake | `k6 run loadtest/bench/dlr.js` | Delivery reports acknowledged per second |

B1, B2, B3, and B7 run a 15-second warm-up scenario followed by a 60-second measured scenario; only the measured scenario increments the `measured_accepted` or `measured_reports` counter, whose rate is the result. B5 offers the rate for 60 seconds in batches of 100 and passes if the queue holds less than two seconds of traffic at the end.

## Method

1. `make bench` starts the stack with the benchmark profile (`deploy/docker-compose.bench.yml`: two workers, a larger PostgreSQL configuration, providers with no latency or failures) and runs `loadtest/bench/run.sh`.
2. The script runs B1 through B7 in order (B5 at 500, 1,000, 2,000, and 4,000 messages/s, or the rates in `E2E_RATES`) and writes each k6 summary to `loadtest/results/<time>/`.
3. During the runs, PostgreSQL's cumulative commits (`pg_stat_database.xact_commit`) and WAL bytes (`pg_stat_wal.wal_bytes`) are sampled every 5 seconds into `postgres.csv`; the difference between samples gives commits/s and WAL bytes/s. Host CPU and I/O are observed with `docker stats` and the host's tools.
4. The script ends with `gateway check`. A benchmark whose run violates an invariant is invalid regardless of its throughput.

## Results

The Measured column is filled from the output of `make bench` on the reference machine described in the environment section above. The At-target column states the requirement at 10,000 messages/s from the [Capacity analysis](010-capacity-analysis.md). The current Measured values are from a smaller, single local machine rather than the reference machine; see the [Benchmark report](040-benchmark-report.md) for the full environment, method, and caveats.

| ID | Metric | Measured | At target |
|---|---|---|---|
| B1 | Accepts/s (single) | 2,284.2/s (median) | 10,000 across API instances |
| B1 | Accept latency p50 / p99 | 60.96 ms / 143.08 ms (medians) | p99 < 100 ms |
| B2 | Accepts/s for one customer | 184.2/s (median) | Informs per-customer limits |
| B3 | Accepts/s (batch 100 / 500) | 21,561.9/s / 24,612.2/s (medians) | 10,000 |
| B4 | Dispatched/s per worker | 10,517 msgs/s (median; topology idle but running) | 10,000 across workers |
| B5 | Sustained end-to-end messages/s | 500/s (FAIL at 1,000) | 10,000 |
| B6 | Express accept→sent p99 | 10.681 s (FAIL; target < 2 s) | < 2 s |
| B7 | DLRs/s | 1,481.9/s (median) | 10,000 |
| — | PostgreSQL commits/s at B1 median | 3,396.83 commits/s | ≈ 10,000 |
| — | WAL MB/s at B5 passing rate | 1.317 MB/s at 500/s offered | 30–50 |

## Related

- [Capacity analysis](010-capacity-analysis.md)
- [Test scenarios](../100-testing/020-scenarios.md)
