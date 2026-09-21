# Capacity Analysis

Whether the design reaches 100 million messages per day, where the limits are, and what each component needs at the target rate. Figures marked *estimate* are engineering estimates to be confirmed by [Benchmarks](020-benchmarks.md).

## Target

| Quantity | Value |
|---|---|
| Daily volume | 100,000,000 messages |
| Average rate | 100,000,000 / 86,400 ≈ **1,160 messages/s** |
| Design peak | **10,000 messages/s** (≈ 8.6 × average) |
| Customers | Tens of thousands; traffic heavily skewed toward a few |

## Database work per message

| Step | Writes | Commits |
|---|---|---|
| Accept | Insert `messages`, insert `transactions`, insert `queue`, update `customers` | 1 per request (a batch of `b` messages shares 1) |
| Claim | Update `queue` | 1 per claim of up to 200 |
| Complete (sent) | Update `messages`, delete `queue` | 1 per completer flush of up to 200 |
| DLR | Update `messages` | 1 per DLR batch of up to 500 |

About 7 row writes per message. At 10,000 messages/s that is about **70,000 row writes/s**, most of them in large batches.

### Commit rate at peak

| Source | Single-message requests | Batches of 100 |
|---|---|---|
| Accept | 10,000/s | 100/s |
| Claim | 50/s | 50/s |
| Complete | 50/s | 50/s |
| DLR | 20/s | 20/s |
| **Total** | **≈ 10,100/s** | **≈ 220/s** |

The accept path dominates. With single-message requests, PostgreSQL must sustain about 10,000 commits/s. Group commit lets concurrent transactions share one WAL flush, which makes this rate achievable on NVMe storage (*estimate*). With batching, the commit rate becomes negligible. High-volume senders, who produce most of the traffic, are the customers most likely to use batches.

## The binding constraints

In order of which is reached first.

### 1. Per-customer row contention

Every accept for a customer updates that customer's row, and the row lock is held until commit. Accepts for one customer are therefore serialized at a rate of roughly `1 / commit latency`. With commit latency around 0.5–2 ms, one customer can sustain roughly **500–2,000 single-message accepts/s** (*estimate*), independent of server size.

A customer producing 30% of peak traffic (3,000 messages/s) exceeds this with single requests. The same customer using batches of 100 needs only 30 row updates/s.

Mitigations, in order: the batch endpoint (built); balance slots for designated customers ([Scaling path](030-scaling-path.md#stage-4-hot-customers)).

### 2. Accept commit rate

Covered above: about 10,000 commits/s with single-message traffic at peak, reduced by batching.

### 3. Storage growth

| Table | Bytes per message including indexes (*estimate*) |
|---|---|
| `messages` | ≈ 350–400 |
| `transactions` | ≈ 200 |
| **Total** | **≈ 550–600** |

At 100 M messages/day: **≈ 55–60 GB/day**, about **1.7 TB for 30 days**. Retention requires partitioning; see [Scaling path](030-scaling-path.md#stage-2-table-growth).

WAL volume, including index updates and full-page writes, is about 3–5 KB per message (*estimate*): **30–50 MB/s** at peak, well within NVMe write bandwidth and ordinary replication bandwidth.

### 4. Backlog during provider outages

A one-hour outage at peak accumulates 36 M queue rows, roughly 5–7 GB including indexes (*estimate*). The claim index remains efficient because it is ordered by `next_attempt_at`, and autovacuum on `queue` is tuned for this churn. After recovery, the backlog drains at the difference between provider capacity and incoming traffic, so providers need headroom above peak for backlogs to clear in reasonable time.

## Components at peak

| Component | Requirement | Sizing (*estimate*) |
|---|---|---|
| API | 10,000 requests/s with single messages | 3–4 instances of 4 vCPU; fewer with batching |
| Workers | In flight = rate × provider latency = 10,000 × 0.2 s = 2,000 concurrent sends | 8 workers at default concurrency (320 each), or fewer with higher concurrency; goroutines are cheap, provider throughput is the limit |
| Providers | ≥ 10,000 messages/s aggregate, plus drain headroom | `PROVIDER_RATE_LIMIT` × workers ≥ provider capacity |
| PostgreSQL primary | ≈ 10,000 commits/s (single) or ≈ 220/s (batched); ≈ 70,000 row writes/s; 30–50 MB/s WAL | 16–32 vCPU, 64–128 GB RAM, NVMe |
| Connections | API and workers share a small server-side pool | PgBouncer in transaction mode; see [Deployment](../090-operations/030-deployment.md) |
| Reads | Reports and dashboard | Read replica |

## Conclusion

A single well-provisioned PostgreSQL primary is expected to carry the design peak when high-volume customers use batching, and comfortably carries the average rate even with single-message traffic (*estimate*). The design does not depend on this: the system partitions cleanly by customer, and each shard is a complete copy of the design with its own queue. See [Scaling path](030-scaling-path.md).

## Related

- [Benchmarks](020-benchmarks.md)
- [Scaling path](030-scaling-path.md)
- [Decision 001: PostgreSQL as the queue](../110-decisions/001-postgres-as-queue.md)
