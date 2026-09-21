# 001. PostgreSQL as the Dispatch Queue

Status: Accepted

## Context

An accepted message must be paid for and queued in the same atomic step. If the debit and the enqueue can diverge, a crash between them either loses a paid message or sends an unpaid one. The system is designed for 10,000 messages/s at peak and deployed at minimum scale, and every additional component must justify its operational cost.

The dispatch work is a set of independent jobs that need per-message acknowledgement, delayed retry, priorities (Express), per-customer fairness, and recovery from crashed consumers.

## Decision

PostgreSQL holds a `queue` table, separate from `messages`, with one row per message awaiting dispatch. The accept transaction inserts the queue row together with the message, the debit, and the balance update. Workers claim batches with `FOR UPDATE SKIP LOCKED`, lease them by pushing `next_attempt_at` forward, and delete or reschedule them in batched completion transactions. No message broker is used.

## Alternatives considered

**API writes to PostgreSQL, then publishes to a broker.** Rejected: a dual write. A crash between commit and publish loses a paid message; publishing first sends unpaid ones.

**Transactional outbox relayed to RabbitMQ.** Correct, but adds a broker, a relay process, publisher confirms, and idempotent consumers, only to recreate the atomicity that a queue row in the same transaction already provides. RabbitMQ's task-queue semantics would suit the workload, and its delayed retries need dead-letter routing where the table needs one column.

**Outbox relayed to Kafka.** Same cost as RabbitMQ, plus a poor fit: a partitioned log blocks later messages behind one that awaits a delayed retry, and partitioning by customer creates hot partitions that work against fairness.

**Redis streams or lists.** Fast, but not transactional with the credit debit, and a second durable store to operate.

## Consequences

Positive:

- Acceptance is exactly one transaction; no message is lost or unpaid in any crash scenario.
- Delayed retry, deferral, lanes, and priorities are columns and `WHERE` clauses.
- A crashed worker's leases expire and are reclaimed by the ordinary claim query.
- When the system shards by customer, each shard carries its own queue; queue capacity grows with data capacity without a second system to scale.
- The deployment has one stateful component.

Negative:

- The queue adds churn (insert, update, delete per message) to the database that also carries the ledger. The table is kept small, batched, and tuned for vacuum, but at very high rates it competes with other writes.
- Idle workers poll. The poll interval bounds added latency (100 ms), and Express uses `LISTEN/NOTIFY` to avoid it.
- A broker would be needed anyway if other systems had to consume message events; that case is handled by a separate event outbox, not by moving dispatch ([Scaling path, stage 6](../080-scalability/030-scaling-path.md#stage-6-event-fan-out)).

Note that a broker would not remove the main database load: status updates, the ledger, and DLRs write to PostgreSQL regardless of how work is distributed.

## Related

- [Queue and workers](../060-processing/010-queue-and-workers.md)
- [Capacity analysis](../080-scalability/010-capacity-analysis.md)
