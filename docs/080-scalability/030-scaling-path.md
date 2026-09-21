# Scaling Path

How the deployment grows from a single node to the full target and beyond, and which change each stage introduces. Each stage is taken only when measurements show the previous one is insufficient.

## Overview

| Stage | Trigger | Change | New components |
|---|---|---|---|
| 0 | — | Single node: all processes and PostgreSQL on one host | None |
| 1 | Host CPU or database I/O saturated | Dedicated PostgreSQL host; multiple API and worker instances; connection pooler; read replica | PgBouncer, replica, load balancer |
| 2 | Table size or retention cost | Partition `messages` and `transactions` by day; drop old partitions; report rollups | None |
| 3 | Customers delayed by lane-mates | More lanes; dedicated lanes for designated customers | None |
| 4 | A single customer exceeds its row ceiling despite batching | Balance slots for that customer | None |
| 5 | One primary cannot carry the write load | Shard by customer | Additional PostgreSQL clusters |
| 6 | Other systems need message events | Event outbox relayed to a log | Kafka or similar, for fan-out only |

## Stage 0: single node

The deployed configuration. Described in [Deployment](../090-operations/030-deployment.md).

## Stage 1: scale out stateless processes

- PostgreSQL moves to a dedicated host with NVMe storage. Relevant settings: `shared_buffers` ≈ 25% of RAM, `max_wal_size` large enough that checkpoints are time-driven, `wal_compression = on`, `synchronous_commit = on` (never relaxed; acceptance durability depends on it).
- API instances scale behind a load balancer. `API_INSTANCES` is set to the instance count so that per-instance rate limits sum to each customer's limit.
- Worker instances scale freely; `PROVIDER_RATE_LIMIT` is divided by the worker count.
- PgBouncer in transaction pooling mode sits in front of PostgreSQL. The worker's notification listener uses a direct session connection, because `LISTEN` requires a session.
- Report queries, customer listings, and admin views read from a streaming replica.

## Stage 2: table growth

- **Partitioning.** `messages` and `transactions` are partitioned by `RANGE (id)`, with one partition per day. Because IDs are UUIDv7, a day's partition bounds are the minimum UUIDv7 values of that day and the next. The primary key remains `id`, and time-bounded queries prune partitions automatically.
- **Retention.** Partitions older than the retention period (for example 90 days) are detached, archived to object storage, and dropped. Dropping a partition is instant and produces no vacuum load.
- **Report rollups.** A `customer_daily_stats (customer_id, day, type, status, messages, segments, credits)` table is maintained incrementally by the completer, DLR batcher, and sweeper, in the same transactions as the status changes. Summary reports read the rollup instead of scanning messages.

## Stage 3: tighter fairness

- **More lanes.** Raising `NORMAL_LANES` from 16 to 64 or 256 reduces the share of customers affected by any one backlog. Claim cost per empty lane is one index probe, so rotation cost grows slowly.
- **Dedicated lanes.** A nullable `customers.lane` column overrides the hash for designated high-volume customers, giving each its own lane.
- **Per-customer round-robin.** Where hashing is still not fair enough, the lane key becomes the customer ID and pools rotate over customers with ready work, tracked in a small `active_customers` table maintained by triggers or by the accept transaction.

## Stage 4: hot customers

For a customer whose single-message rate exceeds the per-row ceiling even though it uses batching where it can:

- The balance is split across `k` rows in a `balance_slots (customer_id, slot, balance)` table.
- An accept debits a randomly chosen slot; if that slot is short, it tries the others.
- A background task rebalances slots so that credits are not stranded in one slot while another runs dry.
- `customers.balance` becomes the sum of the slots, and invariant I1 is checked against that sum.

This multiplies the customer's accept ceiling by `k`, at the cost of more complex behavior near a zero balance.

## Stage 5: sharding by customer

No operation spans two customers: messages, credits, and the queue are all per customer. The system therefore shards by `customer_id` without distributed transactions.

- Each shard is a complete PostgreSQL cluster with the full schema, its own queue, and its own worker pool.
- A small directory maps customers to shards. The API resolves the shard during authentication, and the key cache stores it with the customer.
- Workers are assigned to one shard each.
- The admin API aggregates across shards for system-wide views.
- A heavy customer can be placed on a shard of its own.

Four shards at 2,500 messages/s each carry the target with wide margins.

## Stage 6: event fan-out

When other systems need message events (analytics, customer webhooks, billing exports), status changes also write rows to an `events` outbox table in the same transaction. A relay publishes them to a log such as Kafka. Dispatch stays on the PostgreSQL queue, because only the database can enqueue in the same transaction as the credit debit.

## Not covered

- **Multi-region active-active.** Credits would need either a single home region per customer or a globally consistent store. Placing each customer in one home region (sharding by region) fits the design; cross-region failover of a customer's shard is an operational procedure outside this system.
- **Global rate limiting.** If load balancing becomes too uneven for per-instance limits, a shared token bucket in Redis replaces the in-process limiter; see [Decision 007](../110-decisions/007-in-process-rate-limiting.md).

## Related

- [Capacity analysis](010-capacity-analysis.md)
- [Decision 001: PostgreSQL as the queue](../110-decisions/001-postgres-as-queue.md)
- [Lanes and fairness](../060-processing/020-lanes-and-fairness.md)
