# Glossary

Terms that have a specific meaning in this system. Other documents use them exactly as defined here.

| Term | Definition |
|---|---|
| **API key** | The secret a customer sends as a bearer token. Format `sk_` followed by 43 base62 characters. Stored only as a SHA-256 hash. |
| **Attempt** | One call from a worker to a provider for one message. `messages.attempts` counts completed attempts. |
| **Balance** | A customer's current credits, stored in `customers.balance`. Always equal to the sum of the customer's transactions. |
| **Batch** | Up to 500 messages submitted in one API request and accepted or rejected as a unit. |
| **Circuit breaker** | Per-provider, per-worker-process guard that stops sending to a provider after repeated failures and probes it again after a pause. |
| **Claim** | A worker taking a set of ready queue rows under a lease, using `FOR UPDATE SKIP LOCKED`. |
| **`client_ref`** | A customer-supplied reference, unique per customer, that makes message submission and credit charges idempotent. |
| **Completer** | The component in a worker that collects attempt outcomes and commits them to the database in batches. |
| **Cost** | Credits charged for a message: segments × price of its service class. |
| **Credit** | The unit of value. An integer. |
| **Customer** | A tenant of the gateway. Has a name, an API key, a balance, and a rate limit. |
| **Deferral** | Returning a claimed message to the queue without counting an attempt, because no provider is currently usable. |
| **Delivery report (DLR)** | A provider's asynchronous callback stating whether a message reached the handset (`delivered` or `undelivered`). |
| **Encoding** | How the message text is transmitted: `gsm7` (7-bit GSM alphabet) or `ucs2` (16-bit). Determines characters per segment. |
| **Express message** | A message of the `express` service class: dedicated lane and worker pool, reserved provider capacity, fast retries, failover, SLA tracking, higher price. |
| **Lane** | A partition of the queue. `express` is one lane; normal messages are spread over `normal-0` … `normal-N-1` by customer. |
| **Lease** | A worker's temporary ownership of a claimed queue row, recorded in `queue.lease_owner` and bounded by `queue.next_attempt_at`. An expired lease makes the row claimable again. |
| **Message** | One SMS request from a customer to one recipient. Stored in `messages`. |
| **Normal message** | A message of the `normal` service class. |
| **Pool** | A set of dispatch goroutines in a worker process with a fixed concurrency, serving specific lanes. Each worker has an Express pool and a normal pool. |
| **Provider** | An SMS operator endpoint that accepts messages for delivery. In this system, instances of the fake provider named `A` and `B`. |
| **Provider reference** | The identifier a provider returns when it accepts a message. Stored in `messages.provider_ref`. |
| **Queue** | The `queue` table: one row per message that still needs dispatch work. |
| **Rate budget** | The per-provider send rate a worker process may use, split into a shared part and an Express-reserved part. |
| **Refund** | A credit transaction returning a message's cost when it terminally fails without being accepted by any provider. |
| **Segment** | One transmitted SMS part. A message longer than one segment is split into several and priced per segment. |
| **Service class** | `normal` or `express`. Selects the lane, pool, retry policy, provider selection, and price. |
| **SLA breach** | An Express message that was not accepted by a provider within the Express SLA (30 seconds by default), or that failed or expired. Recorded in `messages.sla_breached`. |
| **Sweeper** | Periodic maintenance in the worker process: expires overdue messages, flags SLA breaches, removes orphaned queue rows and stale worker records. |
| **Terminal status** | `delivered`, `undelivered`, `failed`, or `expired`. A message in a terminal status never changes status again. |
| **Transaction** | A row in `transactions`: a single credit movement (`charge`, `debit`, or `refund`). |
| **Worker** | A gateway process running in the `worker` role: dispatch pools, completer, heartbeat, and sweeper. |

## Related

- [Message lifecycle](../030-domain/010-message-lifecycle.md)
- [Credits and billing](../030-domain/020-credits-and-billing.md)
