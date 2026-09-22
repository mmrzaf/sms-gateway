# Benchmark Report

Results of running the benchmark suite (B1–B7) locally, without Docker, on a single development machine, and what they say about the estimates in [Capacity analysis](010-capacity-analysis.md).

## Summary

| ID | Metric | Median | Runs |
|---|---|---|---|
| B1 | Accepts/s, single messages, 50 customers | 1,075.0/s | 1,090.8 / 925.4 / 1,075.0 |
| B2 | Accepts/s, single messages, 1 customer | 155.1/s | 155.1 / 158.6 / 151.7 |
| B3 | Accepts/s, batches of 100 | 22,671.5/s | 22,845.0 / 21,325.9 / 22,671.5 |
| B3 | Accepts/s, batches of 500 | 25,965.8/s | 25,965.8 / 27,037.1 / 25,775.5 |
| B4 | Dispatched/s, one worker (topology idle but running) | 3,286 msgs/s | 3,497 / 2,984 / 3,286 |
| B5 | Sustained end-to-end rate | 500 msg/s | PASS at 500; FAIL at 1,000 (backlog 14,431) |
| B6 | Express accept→sent p99 (normal lanes saturated) | 6.501 s | FAIL (target < 2 s); 0 SLA breaches |
| B7 | DLRs/s | 1,470.2/s | 1,369.9 / 1,478.7 / 1,470.2 |
| — | Invariant check (`gateway check`) | All 7 checks PASS | on the final database state |

None of these runs approach the 10,000 messages/s design peak: the highest measured single-run throughput (B3, batches of 500, run 2) is 27,037.1 msgs/s in *accepted* messages, but end-to-end dispatch only sustains 500 msg/s (B5) before the queue backs up, so acceptance is not the bottleneck on this machine — dispatch and claim throughput are. The per-customer ceiling (B2, median 155.1/s) is below even the low end of the capacity analysis's 500–2,000/s *estimate*; see [Comparison with the capacity analysis](#comparison-with-the-capacity-analysis) for why that estimate is not supported by this hardware.

## Environment

| | |
|---|---|
| CPU | 11th Gen Intel Core i5-11300H @ 3.10GHz, 4 cores / 8 threads |
| RAM | 15 GiB |
| Storage | NVMe SSD, ext4, non-rotational |
| OS | Debian GNU/Linux 13 (trixie) |
| Go | go1.27.0 linux/amd64 |
| k6 | v2.3.0 |
| PostgreSQL | 18.6 (Debian 18.6-1.pgdg13+2) |
| Gateway commit | `2eda80c11e3a` |

PostgreSQL settings (as configured on this host; not changed for this run):

| Setting | Value |
|---|---|
| `max_connections` | 200 |
| `shared_buffers` | 128 MB |
| `synchronous_commit` | on |
| `max_wal_size` | 1024 MB |
| `wal_compression` | off |

k6, the gateway processes, the fake providers, and PostgreSQL all ran on this one host, sharing its 8 threads, RAM, and disk. PostgreSQL is a shared instance that also hosts unrelated databases; only the dedicated `gateway_bench` database was used for these runs.

## Topology and configuration

Reproduces `deploy/docker-compose.bench.yml` with local processes instead of containers:

| Process | Command | Non-default settings |
|---|---|---|
| Provider A | `bin/provider` | `PROVIDER_NAME=A PROVIDER_ADDR=:9001`, `SIM_LATENCY_MS=0 SIM_JITTER_MS=0 SIM_DLR_DELAY_MS=200 SIM_DLR_JITTER_MS=100` |
| Provider B | `bin/provider` | `PROVIDER_NAME=B PROVIDER_ADDR=:9002`, same simulation settings as A |
| Gateway API | `bin/gateway serve --role=api` | `DB_MAX_CONNS=50` |
| Gateway worker 1 | `bin/gateway serve --role=worker` | `DB_MAX_CONNS=40 PROVIDER_RATE_LIMIT=20000 WORKER_ADDR=:8082` |
| Gateway worker 2 | `bin/gateway serve --role=worker` | `DB_MAX_CONNS=40 PROVIDER_RATE_LIMIT=20000 WORKER_ADDR=:8083` |

This matches the compose profile's two workers, no-latency/no-failure providers, and higher `DB_MAX_CONNS`/`PROVIDER_RATE_LIMIT`, except that PostgreSQL itself kept its host configuration above rather than the compose profile's `shared_buffers=1GB max_connections=300 max_wal_size=4GB wal_compression=on`, since this is a shared, already-running instance rather than a dedicated container.

## Method

- B1, B2, B3, and B7 each run a 15-second warm-up scenario followed by a 60-second measured scenario, as the scripts define; only the measured window's `measured_accepted`/`measured_reports` counter is reported.
- B5 offers each rate for 60 seconds in batches of 100 and passes when the queue holds less than two seconds of traffic (`2 × RATE`) two seconds after the offered load stops.
- B1, B2, B3 (each batch size), and B7 ran 3 times; B4 ran 3 times; B5 ran once per offered rate, stopping at the first failing rate per the suite's own rule; B6 ran once. Medians are the middle value of the 3 sorted runs.
- **Database reset between runs.** Because accept-heavy benchmarks (B1, B3, B7's setup) generate acceptances far faster than this machine's dispatch throughput, letting the queue drain naturally between runs took, in practice, from several minutes to over an hour per run as the un-drained backlog grew into the millions of rows and made claim queries progressively slower (see [Observations](#observations)). To get comparable, uncontaminated measurements for each run without that wait, `gateway_bench` was dropped and recreated (and the API/worker processes restarted) immediately before every individual run, rather than only once at the start of the suite. This means every run in this report reflects a small, freshly migrated database, not the cumulative growth `make bench`'s single continuous run would show; see [Limitations](#limitations).
- Before each run, `GET /admin/api/system` was checked to confirm every lane's `ready`, `delayed`, and `in_flight` were 0 — trivially true immediately after the reset described above.
- **Sampling.** PostgreSQL's cumulative `xact_commit` and `wal_bytes` were sampled every 5 seconds throughout the whole session into `postgres.csv`; host memory/swap/IO were sampled every 5 seconds with `vmstat 5`; process CPU and RSS were sampled every 5 seconds into `processes.csv` for the provider, PostgreSQL, and k6 processes. Per-run commit rate, WAL rate, and CPU are the difference (for commits/WAL) or average (for CPU) over each run's own time window, computed from these samples.
  - **Gap:** the process sampler captured the gateway API/worker PIDs once, at the very start of the session. Because the per-run reset described above restarts those processes with new PIDs, gateway process CPU/RSS was only actually sampled for the first few seconds of the very first run (B1, run 1) and is **not measured** for every run after that. Provider and PostgreSQL CPU are unaffected, since those processes were not restarted between runs.

## Results

### B1 — Accept, single messages, 50 customers

`k6 run loadtest/bench/accept.js`

| Run | Accepts/s | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| 1 | 1,090.8 | 97.64 ms | 259.17 ms | 329.31 ms | 565.58 ms |
| 2 | 925.4 | 102.88 ms | 298.8 ms | 376 ms | 580.3 ms |
| 3 | 1,075.0 | 97.87 ms | 264.33 ms | 339.55 ms | 519.25 ms |

Median: **1,075.0 accepts/s**.

| Run | Commits/s | WAL MB/s |
|---|---|---|
| 1 | 1,615 | 3.08 |
| 2 | 1,455 | 2.87 |
| 3 | 1,586 | 3.06 |

CPU during the runs (average, summed across processes of that kind): PostgreSQL backends ~406–445%, the two providers combined ~75–91%, k6 ~60–62%. Gateway process CPU: not measured (see Method).

### B2 — Accept, single messages, 1 customer

`k6 run -e CUSTOMERS=1 loadtest/bench/accept.js`

| Run | Accepts/s | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| 1 | 155.1 | 866.9 ms | 1.55 s | 1.96 s | 3.49 s |
| 2 | 158.6 | 830.08 ms | 1.53 s | 1.92 s | 4.48 s |
| 3 | 151.7 | 871.81 ms | 1.62 s | 2.04 s | 3.94 s |

Median: **155.1 accepts/s** — the per-customer row-contention ceiling on this hardware.

| Run | Commits/s | WAL MB/s |
|---|---|---|
| 1 | 1,008 | 0.93 |
| 2 | 1,061 | 1.01 |
| 3 | 970 | 0.98 |

CPU: PostgreSQL ~319–327%, providers combined ~65–69%, k6 ~15–18%. Latency here is an order of magnitude higher than B1's despite far lower throughput: every accept for this one customer serializes on its row lock, so p50 is dominated by lock wait, not server capacity.

### B3 — Accept, batches

`k6 run -e BATCH=100 loadtest/bench/batch.js`, then `-e BATCH=500`.

**Batch 100:**

| Run | Accepts/s | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| 1 | 22,845.0 | 150.65 ms | 338.76 ms | 553.35 ms | 1.32 s |
| 2 | 21,325.9 | 160.04 ms | 341.95 ms | 580.57 ms | 1.44 s |
| 3 | 22,671.5 | 147.75 ms | 327.86 ms | 520.81 ms | 1.57 s |

Median: **22,671.5 accepts/s** (in messages; ~227 batch requests/s).

| Run | Commits/s | WAL MB/s |
|---|---|---|
| 1 | 300 | 39.96 |
| 2 | 286 | 37.86 |
| 3 | 307 | 41.01 |

**Batch 500:**

| Run | Accepts/s | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| 1 | 25,965.8 | 638.86 ms | 1.47 s | 2.38 s | 3.62 s |
| 2 | 27,037.1 | 646.7 ms | 1.27 s | 1.72 s | 2.18 s |
| 3 | 25,775.5 | 650.75 ms | 1.48 s | 2.32 s | 2.83 s |

Median: **25,965.8 accepts/s** (in messages; ~70 batch requests/s).

| Run | Commits/s | WAL MB/s |
|---|---|---|
| 1 | 89 | 48.42 |
| 2 | 94 | 49.45 |
| 3 | 88 | 48.89 |

CPU across both batch sizes: PostgreSQL ~365–388%, providers combined ~25–63% (falling from batch-100 run 1's 63% as the cold-start effect described in Observations wore off), k6 ~15–23%. Batching amortizes commits exactly as the capacity analysis expects — commit rate falls by roughly an order of magnitude from B1 to B3 while accepted-message throughput rises by a similar factor — but WAL volume per message does not fall nearly as much, since every message still gets its own row writes.

### B4 — Dispatch only

`go test -run '^$' -bench BenchmarkDispatch -benchtime 20000x ./internal/dispatch/`, `TEST_DATABASE_URL` pointing at `gateway_test`, with the idle benchmark topology (all 5 processes above) still running.

| Run | ns/op | msgs/s |
|---|---|---|
| 1 | 285,935 | 3,497 |
| 2 | 335,092 | 2,984 |
| 3 | 304,287 | 3,286 |

Median: **3,286 msgs/s** per worker (claim, send, complete against a pre-filled queue, provider latency 0). This is markedly lower than an isolated run of the same benchmark on this machine (~10,000 msgs/s, measured in an earlier, unrelated session on the same hardware with nothing else running) — expected, since the task requires the benchmark topology to stay up and it visibly competes for the same 8 threads and the same PostgreSQL instance.

### B5 — End to end

`k6 run -e RATE=<n> loadtest/bench/e2e.js`, offered in batches of 100 for 60 seconds each.

| Offered rate | Result | Final backlog |
|---|---|---|
| 500 msg/s | PASS | 0 |
| 1,000 msg/s | FAIL | 14,431 |

Stopped after the first failure, per the suite's rule. **Sustained end-to-end rate: 500 msg/s.**

| Rate | Commits/s | WAL MB/s |
|---|---|---|
| 500 | 219 | 1.21 |
| 1,000 | 82 | 1.58 |

CPU: PostgreSQL ~62–145%, providers combined ~28%, k6 under 2% (the offering VUs spend almost all their time waiting on responses, not computing). The 1,000 msg/s run's lower commit rate despite twice the offered load reflects the run failing to keep the queue populated with *completed* work at that rate — commits from `commitSent`/`commitRetry` fall as claim and provider round-trips become the bottleneck.

### B6 — Express under normal saturation

`k6 run loadtest/scenarios/express-under-load.js`

| Criterion | Result |
|---|---|
| Express p99 accept→sent under 2 s | **FAIL** — p99 was 6.501 s (6,000 samples) |
| No express SLA breaches | PASS (0) |
| Invariants | PASS |

`http_req_duration`: avg 5.93 ms, p95 16.81 ms, max 586.76 ms — the HTTP accept latency itself stayed low; the 6.501 s figure is accept-to-*sent* latency (through dispatch), not accept latency.

Commits/s: 254; WAL: 3.43 MB/s; CPU: PostgreSQL ~186%, providers combined ~28%, k6 ~4%.

### B7 — DLR intake

`k6 run -e PROVIDER_SECRET=local-provider-secret loadtest/bench/dlr.js`

| Run | Reports/s | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| 1 | 1,369.9 | 54.82 ms | 63.47 ms | 109.63 ms | 956.95 ms |
| 2 | 1,478.7 | 52.88 ms | 55.66 ms | 186.42 ms | 539.72 ms |
| 3 | 1,470.2 | 52.84 ms | 55.16 ms | 58.22 ms | 603.48 ms |

Median: **1,470.2 reports/s**.

| Run | Commits/s | WAL MB/s |
|---|---|---|
| 1 | 194 | 0.29 |
| 2 | 251 | 0.24 |
| 3 | 310 | 0.28 |

CPU: PostgreSQL ~61–129%, providers combined ~28%, k6 ~29–31%.

## Observations

- **Dispatch/claim throughput, not acceptance, is the binding constraint on this machine.** Every accept-side benchmark (B1: 1,075/s; B3: up to ~26,000/s) comfortably exceeds what B4 (one worker's isolated dispatch ceiling, ~3,286/s) and B5 (sustained end-to-end, 500/s) can carry. Two workers at B4's rate give a rough combined ceiling in the low thousands per second, an order of magnitude below the 10,000/s target — consistent with B5 failing at 1,000/s.
- **A large, freshly-loaded queue table measurably slows PostgreSQL's own claim query**, independent of application code. When millions of accepted-but-undispatched rows accumulated (before the per-run reset described in Method was adopted), the claim query's planning grew stale relative to the table's actual size, and observed drain throughput fell from roughly 8,600 msgs/s immediately after a large batch load to under 1,000 msgs/s later in the same drain, recovering each time a manual `ANALYZE` was run on `messages` and `queue`. This is autovacuum/planner-statistics lag under a very large, very sudden insert, not a defect in the queue design — but it means a single-node deployment that lets a large backlog build up (e.g. during a provider outage, per [Capacity analysis §4](010-capacity-analysis.md#4-backlog-during-provider-outages)) may see claim throughput degrade until autovacuum's automatic `ANALYZE` catches up, which on this hardware could take longer than the default autovacuum thresholds assume for a table this size.
- **A small number of ready, unleased queue rows in one lane were observed to go unclaimed for over two hours** during one B3 (batch 100) run, despite satisfying the claim query's `WHERE` clause when queried directly and despite thousands of round-robin passes over that lane by two active workers in the meantime. Only 3 rows out of that run's 1.7 million messages were affected, and the anomaly did not recur in later runs against a smaller table. The root cause was not identified within the scope of this benchmark session; it may be related to the same large-table/planner-statistics effect above, since it appeared only once the table was in the millions of rows. It is reported here as an observation, not diagnosed as a defect.
- **B2's per-customer ceiling (155.1/s) is far below the capacity analysis's estimate** of 500–2,000 single-message accepts/s for one customer; see the comparison below.
- **CPU is not the limiting resource** in any of these runs: PostgreSQL backend CPU is high in absolute terms (hundreds of percent, i.e. using multiple of the 8 available threads) during accept-heavy runs, but never saturates all 8 threads, and the gateway processes themselves were not measured (see Method's sampling gap) so their CPU cannot be ruled in or out as a contributing bottleneck. The evidence available points to database-side row/lock contention (B2) and claim-query cost under a large queue (B4/B5) rather than raw CPU exhaustion, but this is not conclusive without the missing gateway process data.

## Comparison with the capacity analysis

| Estimate ([Capacity analysis](010-capacity-analysis.md)) | Estimate value | Measurement | Supported? |
|---|---|---|---|
| Per-customer row ceiling | 500–2,000 single-message accepts/s | B2 median: 155.1/s | **No** — measured well below even the low end. The estimate assumes a commit latency of 0.5–2 ms; this shared, single host's actual commit latency under concurrent load is evidently higher, and the estimate does not account for lock-wait queuing behind other lanes' work on the same host. |
| Accept commit rate at peak (single-message) | ≈ 10,000 commits/s | B1: 1,455–1,615 commits/s at 1,075 accepts/s | **Not directly comparable** — this run never reached anywhere near the 10,000 accepts/s the estimate assumes, and the measured commit rate includes concurrent dispatch/claim/complete/DLR commits from the running workers, not accept commits alone, so it cannot be scaled linearly to infer the peak figure. |
| Accept commit rate at peak (batched, batches of 100) | ≈ 220/s | B3 (batch 100): 286–307 commits/s | **Roughly consistent**, though again not at target throughput (22,671/s vs. the target's 10,000/s messages), and inflated by the same concurrent dispatch activity. |
| WAL volume at peak | 30–50 MB/s | B3 (batch 500): 48.4–49.5 MB/s | **Consistent in magnitude**, though again at a different (higher, batched) message rate than the target scenario, so this is a coincidental match rather than a validation of the peak-rate estimate specifically. |
| Dispatch rate per worker | Not stated as a single number (sizing targets 8 workers at 10,000/s combined, i.e. ~1,250/s/worker at default concurrency) | B4 median: 3,286 msgs/s per worker, with the benchmark topology (API + 2 workers) also running | **Exceeds** the rough per-worker rate implied by the sizing table, but was measured in isolation from real network/provider latency (provider latency 0) and while other processes competed for the same 8 threads — not a clean single-worker-alone measurement. |
| Storage growth (bytes/message) | ≈ 550–600 bytes/message | Not measured — this report did not track table size growth in relation to message count | Not measured |
| Backlog during outages | ≈ 36M queue rows/hour at peak, 5–7 GB | Not measured — no outage scenario was run in this session | Not measured |
| API sizing (3–4 instances of 4 vCPU) | — | Not measured — only one API instance was run | Not measured |
| PostgreSQL primary sizing (16–32 vCPU, 64–128 GB RAM) | — | Not measured — this host (8 threads, 15 GB RAM) is smaller than the sizing target, by design, since it is not intended to reach 10,000/s | Not measured |

## Limitations

- **Single host.** k6, both fake providers, both gateway roles, and PostgreSQL shared one 8-thread machine. Every number above reflects contention between all of these, not the isolated capacity of any one component. Expected effect: every throughput number is lower, and every latency number higher, than the same component would show with dedicated hardware — the design's own [Scaling path](030-scaling-path.md) assumes separated hosts for anything beyond stage 0.
- **k6 on the same machine as the system under test.** k6 itself consumed a visible share of CPU (up to ~62% in B1). Expected effect: measured latencies include k6's own scheduling jitter, and measured throughput is capped in part by k6's own capacity, not only the gateway's.
- **Local-disk PostgreSQL, not a dedicated host with the compose profile's tuned settings.** `shared_buffers`, `max_wal_size`, `wal_compression`, and `max_connections` were left at this host's existing values rather than the bench profile's `1GB`/`4GB`/`on`/`300`. Expected effect: WAL and checkpoint behavior likely differ from what the documented bench profile would show; the true effect size was not measured (no PostgreSQL restart was performed, since this is a shared instance — see Method).
- **No network between components.** All HTTP calls (k6 → gateway, gateway → providers) were loopback. Expected effect: measured provider round-trip and accept latencies are lower than a real multi-host deployment would see, so B5's failure at 1,000 msg/s likely understates how much worse a networked deployment's sustained rate would be relative to this one, not overstate it.
- **Short runs (60–120 seconds).** Long-tail effects — autovacuum catching up, connection pool warm-up, checkpoint timing — are visible in these numbers (see Observations) but were not run long enough to reach a true steady state in every case.
- **Per-run database resets (see Method) mean these numbers do not reflect a single continuous session's cumulative load**, unlike `make bench`'s intended single run. Expected effect: these numbers are closer to a "cold, lightly loaded database" ceiling than to the ceiling of a system that has been running and accumulating data for hours, which the Observations section shows can be meaningfully worse.
- **Gateway process CPU was not measured for all but the first few seconds of the session** (see Method). This report cannot rule gateway-side CPU in or out as a contributing bottleneck to the dispatch/claim ceiling described in Observations.

## Reproducing

```sh
# Step 2: database and binaries
createdb -O gateway gateway_bench   # role "gateway" already exists
go build -trimpath -o bin/ ./cmd/...
export DATABASE_URL="postgres://gateway:gateway@localhost:5432/gateway_bench?sslmode=disable"
export ADMIN_TOKEN=admin PROVIDER_SECRET=local-provider-secret
export PROVIDERS="A=http://localhost:9001,B=http://localhost:9002"
bin/gateway migrate

# Step 3: topology
SIM_LATENCY_MS=0 SIM_JITTER_MS=0 SIM_DLR_DELAY_MS=200 SIM_DLR_JITTER_MS=100 \
  PROVIDER_NAME=A PROVIDER_ADDR=:9001 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr bin/provider &
SIM_LATENCY_MS=0 SIM_JITTER_MS=0 SIM_DLR_DELAY_MS=200 SIM_DLR_JITTER_MS=100 \
  PROVIDER_NAME=B PROVIDER_ADDR=:9002 GATEWAY_DLR_URL=http://localhost:8081/internal/dlr bin/provider &
DB_MAX_CONNS=50 bin/gateway serve --role=api &
DB_MAX_CONNS=40 PROVIDER_RATE_LIMIT=20000 WORKER_ADDR=:8082 bin/gateway serve --role=worker &
DB_MAX_CONNS=40 PROVIDER_RATE_LIMIT=20000 WORKER_ADDR=:8083 bin/gateway serve --role=worker &

bin/gateway probe http://localhost:8080/readyz
bin/gateway probe http://localhost:8082/readyz
bin/gateway probe http://localhost:8083/readyz
bin/provider probe http://localhost:9001/healthz
bin/provider probe http://localhost:9002/healthz
curl -u admin:admin http://localhost:8081/admin/api/system

# Step 5: benchmarks (repeat 3x for B1, B2, B3 per batch size, B7; reset gateway_bench
# between runs as described in Method to avoid backlog buildup)
k6 run loadtest/bench/accept.js                             # B1
k6 run -e CUSTOMERS=1 loadtest/bench/accept.js               # B2
k6 run -e BATCH=100 loadtest/bench/batch.js                  # B3
k6 run -e BATCH=500 loadtest/bench/batch.js                  # B3
TEST_DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable \
  go test -run '^$' -bench BenchmarkDispatch -benchtime 20000x ./internal/dispatch/   # B4
k6 run -e RATE=500 loadtest/bench/e2e.js                     # B5 (stop at first FAIL)
k6 run -e RATE=1000 loadtest/bench/e2e.js
k6 run loadtest/scenarios/express-under-load.js              # B6
k6 run -e PROVIDER_SECRET=local-provider-secret loadtest/bench/dlr.js   # B7

bin/gateway check
curl -s localhost:8081/metrics
curl -s localhost:8082/metrics
```

## Related

- [Capacity analysis](010-capacity-analysis.md)
- [Benchmarks](020-benchmarks.md)
- [Scaling path](030-scaling-path.md)
- [Test scenarios](../100-testing/020-scenarios.md)
