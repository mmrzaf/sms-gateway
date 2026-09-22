# Benchmark Report

Results of running B1–B7 locally, without Docker, on one development machine. This report records the measured ceilings and compares them with [Capacity analysis](010-capacity-analysis.md).

## Summary

| ID | Metric | Median or result | Runs |
|---|---|---|---|
| B1 | Single-message accepts, 50 customers | 2,284.2/s | 2,409.0 / 1,060.5 / 2,284.2 |
| B2 | Single-message accepts, 1 customer | 184.2/s | 170.8 / 184.2 / 184.7 |
| B3 | Batch-100 accepts | 21,561.9/s | 23,272.2 / 18,296.0 / 21,561.9 |
| B3 | Batch-500 accepts | 24,612.2/s | 24,612.2 / 24,937.2 / 19,882.6 |
| B4 | Dispatch, one worker | 10,517 msgs/s | 10,416 / 10,517 / 10,652 |
| B5 | Sustained end-to-end rate | 500 msg/s | PASS at 500; FAIL at 1,000 (backlog 14,704) |
| B6 | Express accept-to-sent p99 | 10.681 s | FAIL (target < 2 s); 0 SLA breaches |
| B7 | DLR intake | 1,481.9/s | 1,481.9 / 1,482.5 / 1,474.6 |
| — | `gateway check` | All 7 checks PASS | Final database state |

B3 acceptance exceeds the 10,000 msg/s design peak, but the topology sustains only 500 msg/s end to end before failing at 1,000 msg/s. The B2 median is below the capacity analysis estimate of 500–2,000 single-message accepts/s for one customer.

## Environment

| Item | Value |
|---|---|
| Gateway commit | `7fbfd9601e31e24870a625c4ef1a7fdbf39b789f` |
| OS | Debian GNU/Linux 13 (trixie) |
| CPU | Intel Core i5-11300H @ 3.10 GHz, 4 cores / 8 threads |
| RAM | 15 GiB |
| Storage | Non-rotational NVMe, ext4 |
| Go / k6 | go1.27.0 linux/amd64 / v2.3.0 |
| PostgreSQL client / server | 18.6 / 18.6, 64-bit |

| PostgreSQL setting | Value |
|---|---|
| `max_connections` | 200 |
| `shared_buffers` | 128 MB |
| `synchronous_commit` | on |
| `max_wal_size` | 1 GB |
| `wal_compression` | off |

These settings remain unchanged. k6, all gateway and provider processes, and PostgreSQL share this host. PostgreSQL is shared, although the benchmark uses dedicated `gateway_bench` and `gateway_test` databases.

## Topology and configuration

This reproduces `deploy/docker-compose.bench.yml` with local processes.

| Process | Command and non-default settings |
|---|---|
| Provider A | `bin/provider`; `PROVIDER_NAME=A PROVIDER_ADDR=:9001 SIM_LATENCY_MS=0 SIM_JITTER_MS=0 SIM_DLR_DELAY_MS=200 SIM_DLR_JITTER_MS=100 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr` |
| Provider B | Same, with `PROVIDER_NAME=B PROVIDER_ADDR=:9002` |
| API | `DB_MAX_CONNS=50 bin/gateway serve --role=api` |
| Worker 1 | `DB_MAX_CONNS=40 PROVIDER_RATE_LIMIT=20000 WORKER_ADDR=:8082 bin/gateway serve --role=worker` |
| Worker 2 | Same, with `WORKER_ADDR=:8083` |

All gateway processes use the documented `DATABASE_URL`, `ADMIN_TOKEN=admin`, `PROVIDER_SECRET=local-provider-secret`, and both provider URLs. Unlike Compose, PostgreSQL retains the host settings above rather than `shared_buffers=1GB`, `max_connections=300`, `max_wal_size=4GB`, and `wal_compression=on`.

## Method

- The working tree is clean before the run. Build, vet, and `go test -race -count=1 ./...` pass; tests use `gateway_test`.
- B1, B2, B3, and B7 have a 15-second warm-up and 60-second measured scenario. Only `measured_accepted` or `measured_reports` supplies throughput.
- B1, B2, each B3 size, B4, and B7 run three times. Each headline is the middle sorted value. B5 and B6 run once per required case.
- Before every run, `gateway_bench` is dropped, recreated, migrated, and served by restarted API/workers. The admin API confirms every lane has zero `ready`, `delayed`, and `in_flight` messages. Providers remain running.
- B5 offers each rate for 60 seconds in batches of 100, checks backlog two seconds later, then drains. Testing stops at the first failure.
- PostgreSQL commits and WAL bytes, host `vmstat`, and process CPU/RSS are sampled every 5 seconds. Rates use the first and last samples within each run. CPU is the average sampled `ps` `%CPU`, summed by process class; 100% is one logical CPU.
- PostgreSQL commits and CPU cover the shared server, as required, so unrelated database activity can contribute.
- The final invariant check passes all seven checks. All five service processes then receive SIGINT and exit with status 0.

## Results

CPU columns are PostgreSQL / API / both workers / both providers / k6.

### B1 — Accept, single messages, 50 customers

`k6 run loadtest/bench/accept.js`

| Run | Accepts/s | p50 / p95 / p99 | Commits/s | WAL MB/s | CPU |
|---|---:|---|---:|---:|---|
| 1 | 2,409.0 | 59.75 / 105.85 / 136.15 ms | 3,550.61 | 7.778 | 388.3% / 120.0% / 32.7% / 37.2% / 73.5% |
| 2 | 1,060.5 | 101.16 / 262.99 / 342.67 ms | 1,746.52 | 3.316 | 454.9% / 79.9% / 7.6% / 49.9% / 62.5% |
| 3 | 2,284.2 | 60.96 / 111.61 / 143.08 ms | 3,396.83 | 7.230 | 373.6% / 114.6% / 32.7% / 50.1% / 74.8% |
| Median | **2,284.2** | **60.96 / 111.61 / 143.08 ms** | **3,396.83** | **7.230** | — |

### B2 — Accept, single messages, one customer

`k6 run -e CUSTOMERS=1 loadtest/bench/accept.js`

| Run | Accepts/s | p50 / p95 / p99 | Commits/s | WAL MB/s | CPU |
|---|---:|---|---:|---:|---|
| 1 | 170.8 | 768.86 / 1,384.55 / 1,788.83 ms | 1,638.65 | 1.180 | 382.8% / 20.0% / 11.8% / 43.3% / 17.6% |
| 2 | 184.2 | 677.71 / 1,336.20 / 1,684.61 ms | 1,440.87 | 1.298 | 365.4% / 22.6% / 10.4% / 38.2% / 16.9% |
| 3 | 184.7 | 743.71 / 1,301.32 / 1,661.93 ms | 1,111.66 | 1.214 | 341.4% / 19.0% / 7.2% / 34.6% / 14.8% |
| Median | **184.2** | **743.71 / 1,336.20 / 1,684.61 ms** | **1,440.87** | **1.214** | — |

### B3 — Accept, batches

`k6 run -e BATCH=100 loadtest/bench/batch.js`

| Run | Accepts/s | p50 / p95 / p99 | Commits/s | WAL MB/s | CPU |
|---|---:|---|---:|---:|---|
| 1 | 23,272.2 | 149.83 / 313.71 / 441.31 ms | 329.03 | 44.592 | 431.0% / 94.3% / 3.4% / 31.1% / 19.9% |
| 2 | 18,296.0 | 182.23 / 407.89 / 738.41 ms | 344.35 | 43.173 | 298.5% / 107.1% / 31.7% / 38.6% / 16.6% |
| 3 | 21,561.9 | 147.92 / 354.52 / 697.07 ms | 326.57 | 43.882 | 390.6% / 93.7% / 4.2% / 45.8% / 19.8% |
| Median | **21,561.9** | **149.83 / 354.52 / 697.07 ms** | **329.03** | **43.882** | — |

`k6 run -e BATCH=500 loadtest/bench/batch.js`

| Run | Accepts/s | p50 / p95 / p99 | Commits/s | WAL MB/s | CPU |
|---|---:|---|---:|---:|---|
| 1 | 24,612.2 | 633.87 / 1,723.78 / 2,378.11 ms | 93.69 | 49.729 | 394.9% / 82.8% / 2.0% / 41.9% / 15.4% |
| 2 | 24,937.2 | 630.96 / 1,772.39 / 2,563.27 ms | 94.21 | 50.174 | 370.8% / 84.0% / 3.6% / 38.8% / 14.0% |
| 3 | 19,882.6 | 826.97 / 1,743.37 / 2,809.59 ms | 122.62 | 44.788 | 362.5% / 98.4% / 18.5% / 39.9% / 14.1% |
| Median | **24,612.2** | **633.87 / 1,743.37 / 2,563.27 ms** | **94.21** | **49.729** | — |

### B4 — Dispatch only

`TEST_DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable go test -run '^$' -bench BenchmarkDispatch -benchtime 20000x ./internal/dispatch/`

| Run | ns/op | msgs/s | Commits/s / WAL / CPU |
|---|---:|---:|---|
| 1 | 96,007 | 10,416 | Not measured |
| 2 | 95,082 | 10,517 | Not measured |
| 3 | 93,877 | 10,652 | Not measured |
| Median | **95,082** | **10,517** | — |

The topology remains idle but running while the benchmark starts its own worker against its test schema. Each run lasts about three seconds, so zero or one five-second sample cannot produce a rate or representative CPU average.

### B5 — End to end

`k6 run -e RATE=<rate> loadtest/bench/e2e.js`

| Rate | Result | Backlog | HTTP median / p95 | Commits/s | WAL MB/s | CPU |
|---:|---|---:|---|---:|---:|---|
| 500/s | PASS | 0 | 6.18 / 7.51 ms | 428.40 | 1.317 | 52.8% / 8.3% / 5.6% / 49.0% / 1.6% |
| 1,000/s | FAIL | 14,704 | 7.66 / 13.68 ms | 91.74 | 1.849 | 174.0% / 14.0% / 6.7% / 47.1% / 1.4% |

The highest passing offered rate is **500 msg/s**.

### B6 — Express under normal saturation

`k6 run loadtest/scenarios/express-under-load.js`

| Criterion | Result |
|---|---|
| Express p99 accept-to-sent under 2 s | **FAIL** — 10.681 s across 6,001 messages |
| No Express SLA breaches | PASS — 0 |
| Scenario invariants | PASS |

HTTP request duration is 6.40 ms median, 20.64 ms p95, and 516.90 ms maximum. The run records 259.11 commits/s, 3.372 MB/s WAL, and CPU of 224.1% / 18.9% / 8.6% / 44.7% / 4.3%.

### B7 — DLR intake

`k6 run -e PROVIDER_SECRET=local-provider-secret loadtest/bench/dlr.js`

| Run | Reports/s | p50 / p95 / p99 | Commits/s | WAL MB/s | CPU |
|---|---:|---|---:|---:|---|
| 1 | 1,481.9 | 52.66 / 54.62 / 62.57 ms | 355.41 | 0.736 | 84.3% / 19.5% / 7.9% / 42.4% / 33.5% |
| 2 | 1,482.5 | 52.69 / 54.88 / 65.89 ms | 321.80 | 0.750 | 112.3% / 18.0% / 6.8% / 40.5% / 32.4% |
| 3 | 1,474.6 | 53.05 / 55.85 / 60.90 ms | 225.88 | 0.710 | 191.6% / 18.8% / 5.2% / 38.8% / 31.9% |
| Median | **1,481.9** | **52.69 / 54.88 / 62.57 ms** | **321.80** | **0.736** | — |

### Final worker metrics

The post-B7 snapshot from worker `:8082` represents one worker, not both workers combined.

| Metric | Measurement |
|---|---|
| Dispatch attempts | 7,378; all provider A, normal, sent |
| Provider latency | 6,807 <= 5 ms; 7,320 <= 10 ms; all 7,378 <= 20 ms; sum 17.581617641 s |
| Completer batches | 40; 3 <= 64 outcomes and all 40 <= 256; sum 7,378 outcomes |

## Observations

- B1 run 2 is less than half the throughput of runs 1 and 3. No host-wide CPU resource is saturated, and the samples do not establish a cause; the variance is inconclusive.
- B2 is much slower than B1 while API CPU is low. This is consistent with serialization on one customer row, but commit latency and lock-wait scheduling are not isolated.
- B3 amortizes commits: batch-100 reaches 21,561.9 msg/s at 329.03 median commits/s and batch-500 reaches 24,612.2 msg/s at 94.21 median commits/s. WAL remains 43–50 MB/s because every message still writes rows and indexes.
- B4 exceeds 10,000 msgs/s, yet B5 fails at 1,000 msg/s. B5 shows neither sampled CPU saturation nor high provider latency, so these measurements do not identify the end-to-end limiter.
- B6 keeps HTTP latency low but fails accept-to-sent latency. Zero SLA breaches and a 10.681 s scenario p99 are distinct recorded measurements; their divergence is not explained by this run.
- B7 throughput varies by less than 0.6%, with no sampled host-wide CPU saturation.

## Comparison with the capacity analysis

| Estimate | Estimated | Measured | Assessment |
|---|---|---|---|
| Per-customer ceiling | 500–2,000 accepts/s | B2: 184.2/s | Not supported on this host |
| Single-message peak commits | About 10,000/s | B1: 3,396.83/s at 2,284.2 accepts/s | Not validated; target load is not reached and commits are server-wide |
| Batch-100 peak commits | About 220/s at 10,000 msg/s | B3: 329.03/s at 21,561.9 msg/s | Same order of magnitude; not the estimated workload |
| WAL at peak | 30–50 MB/s | B3: 43.882 MB/s at batch 100; 49.729 MB/s at batch 500 | Same magnitude; measured above 10,000 msg/s |
| Dispatch per worker | About 1,250/s implied by 8 workers at 10,000/s | B4: 10,517/s with zero provider latency | Exceeds the isolated requirement; does not predict B5 |
| Storage growth | 550–600 bytes/message | Not measured; table growth was not sampled | Not measured |
| Outage backlog | 36M rows/hour, 5–7 GB | No outage run | Not measured |
| API sizing | 3–4 instances of 4 vCPU | One API process | Not measured |
| PostgreSQL sizing | 16–32 vCPU, 64–128 GB | 8 threads, 15 GiB host | Not measured |

## Limitations

- **Single host:** expected effect is lower throughput and higher latency from resource contention; the size is not measured.
- **k6 shares the host:** expected effect is load-generator competition and scheduling jitter.
- **Shared local PostgreSQL:** server-wide samples can include unrelated databases, and settings differ from Compose. Expected effects are sample contamination and different WAL/checkpoint behavior.
- **No network:** loopback is expected to produce lower transport latency than multiple hosts.
- **Short runs:** 60–120 seconds may omit long-term checkpoint, autovacuum, thermal, and growth effects.
- **Fresh database per run:** cumulative table growth and backlog effects are not measured.
- **`ps` CPU:** `%CPU` is a lifetime average, not an interval counter, so short phase changes are smoothed.

## Reproducing

```sh
go build ./... && go vet ./...
TEST_DATABASE_URL="postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable" go test -race -count=1 ./...
createdb -O gateway gateway_bench
go build -trimpath -o bin/ ./cmd/...
mkdir -p /tmp/sms-bench
export DATABASE_URL="postgres://gateway:gateway@localhost:5432/gateway_bench?sslmode=disable"
export ADMIN_TOKEN=admin PROVIDER_SECRET=local-provider-secret
export PROVIDERS="A=http://localhost:9001,B=http://localhost:9002"
bin/gateway migrate

SIM_LATENCY_MS=0 SIM_JITTER_MS=0 SIM_DLR_DELAY_MS=200 SIM_DLR_JITTER_MS=100 \
  PROVIDER_NAME=A PROVIDER_ADDR=:9001 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr \
  bin/provider >/tmp/sms-bench/provider-a.log 2>&1 & echo $! >/tmp/sms-bench/provider-a.pid
SIM_LATENCY_MS=0 SIM_JITTER_MS=0 SIM_DLR_DELAY_MS=200 SIM_DLR_JITTER_MS=100 \
  PROVIDER_NAME=B PROVIDER_ADDR=:9002 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr \
  bin/provider >/tmp/sms-bench/provider-b.log 2>&1 & echo $! >/tmp/sms-bench/provider-b.pid
DB_MAX_CONNS=50 bin/gateway serve --role=api >/tmp/sms-bench/gateway-api.log 2>&1 & echo $! >/tmp/sms-bench/gateway-api.pid
DB_MAX_CONNS=40 PROVIDER_RATE_LIMIT=20000 WORKER_ADDR=:8082 bin/gateway serve --role=worker >/tmp/sms-bench/gateway-worker1.log 2>&1 & echo $! >/tmp/sms-bench/gateway-worker1.pid
DB_MAX_CONNS=40 PROVIDER_RATE_LIMIT=20000 WORKER_ADDR=:8083 bin/gateway serve --role=worker >/tmp/sms-bench/gateway-worker2.log 2>&1 & echo $! >/tmp/sms-bench/gateway-worker2.pid

bin/gateway probe http://localhost:8080/readyz
bin/gateway probe http://localhost:8082/readyz
bin/gateway probe http://localhost:8083/readyz
bin/provider probe http://localhost:9001/healthz
bin/provider probe http://localhost:9002/healthz
curl -u admin:admin http://localhost:8081/admin/api/system

# Sampling runs in separate shells for the whole session.
vmstat 5 >/tmp/sms-bench/vmstat.log & echo $! >/tmp/sms-bench/vmstat.pid
while true; do
  psql "$DATABASE_URL" -At -F, -c \
    "SELECT now(), (SELECT sum(xact_commit) FROM pg_stat_database), (SELECT wal_bytes FROM pg_stat_wal)" \
    >>/tmp/sms-bench/postgres.csv
  sleep 5
done & echo $! >/tmp/sms-bench/sample-postgres.pid
while true; do
  pids="$(pgrep -f 'bin/gateway serve|bin/provider'; pgrep -x postgres; pgrep -f 'k6 run')"
  ps -o pid,pcpu,rss,comm -p "$(printf '%s\n' "$pids" | paste -sd, -)" >>/tmp/sms-bench/processes.csv
  sleep 5
done & echo $! >/tmp/sms-bench/sample-processes.pid

# Before every run: SIGINT and wait for the API/workers, then recreate,
# migrate, and restart gateway_bench using the three gateway commands above.
kill -SIGINT "$(cat /tmp/sms-bench/gateway-api.pid)" \
  "$(cat /tmp/sms-bench/gateway-worker1.pid)" "$(cat /tmp/sms-bench/gateway-worker2.pid)"
wait "$(cat /tmp/sms-bench/gateway-api.pid)" \
  "$(cat /tmp/sms-bench/gateway-worker1.pid)" "$(cat /tmp/sms-bench/gateway-worker2.pid)"
dropdb gateway_bench
createdb -O gateway gateway_bench
bin/gateway migrate
curl -u admin:admin http://localhost:8081/admin/api/system

# Repeat B1, B2, both B3 commands, B4, and B7 three times, substituting
# the run number in each summary and console filename.
k6 run --summary-export /tmp/sms-bench/B1-run1.json loadtest/bench/accept.js >/tmp/sms-bench/B1-run1.txt 2>&1
k6 run --summary-export /tmp/sms-bench/B2-run1.json -e CUSTOMERS=1 loadtest/bench/accept.js >/tmp/sms-bench/B2-run1.txt 2>&1
k6 run --summary-export /tmp/sms-bench/B3-100-run1.json -e BATCH=100 loadtest/bench/batch.js >/tmp/sms-bench/B3-100-run1.txt 2>&1
k6 run --summary-export /tmp/sms-bench/B3-500-run1.json -e BATCH=500 loadtest/bench/batch.js >/tmp/sms-bench/B3-500-run1.txt 2>&1
TEST_DATABASE_URL="postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable" go test -run '^$' -bench BenchmarkDispatch -benchtime 20000x ./internal/dispatch/
k6 run --summary-export /tmp/sms-bench/B5-500.json -e RATE=500 loadtest/bench/e2e.js >/tmp/sms-bench/B5-500.txt 2>&1
k6 run --summary-export /tmp/sms-bench/B5-1000.json -e RATE=1000 loadtest/bench/e2e.js >/tmp/sms-bench/B5-1000.txt 2>&1
k6 run --summary-export /tmp/sms-bench/B6.json loadtest/scenarios/express-under-load.js >/tmp/sms-bench/B6.txt 2>&1
k6 run --summary-export /tmp/sms-bench/B7-run1.json -e PROVIDER_SECRET=local-provider-secret loadtest/bench/dlr.js >/tmp/sms-bench/B7-run1.txt 2>&1

bin/gateway check
curl -s localhost:8081/metrics > /tmp/sms-bench/metrics-api.txt
curl -s localhost:8082/metrics > /tmp/sms-bench/metrics-worker.txt
# Stop all five service processes with SIGINT and wait for exit status 0.
```

## Related

- [Capacity analysis](010-capacity-analysis.md)
- [Benchmarks](020-benchmarks.md)
- [Scaling path](030-scaling-path.md)
- [Configuration](../090-operations/020-configuration.md)
- [Test scenarios](../100-testing/020-scenarios.md)
