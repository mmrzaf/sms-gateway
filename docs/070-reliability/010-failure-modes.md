# Failure Modes

What happens when each part of the system fails, what the customer observes, and how the system recovers.

## Delivery semantics

| Boundary | Guarantee |
|---|---|
| Customer → gateway | A `202` response means the message is committed. Without a response, the customer retries with the same `client_ref` and receives the original result. |
| Gateway → provider | At least once. Duplicates are absorbed by provider deduplication on the message ID, except after Express failover following a timeout. |
| Provider → gateway (DLR) | At least once. Duplicates are harmless because status changes are compare-and-set. |
| Credits | Exactly once. Each message has one debit and at most one refund, enforced by unique constraints. |

## Failure table

| # | Failure | Effect | Recovery | Customer observes |
|---|---|---|---|---|
| 1 | API process crashes before commit | Transaction rolled back; nothing recorded | Customer retries | Connection error; retry succeeds |
| 2 | API process crashes after commit, before responding | Message accepted and debited | Customer retries with the same `client_ref`; replay returns the original | Connection error; retry returns `202` with `Idempotent-Replayed: true` |
| 3 | Database unavailable | API returns `503`; workers cannot claim; DLR intake returns `503` | Automatic when the database returns; providers retry DLRs | `503` responses; no accepted message lost |
| 4 | Worker crashes after claiming | Claimed rows stay leased | Lease expires after 30 s; rows are claimed again | Up to 30 s extra latency for affected messages |
| 5 | Worker crashes after the provider accepted, before the completer committed | Provider accepted; database still shows `accepted` | Lease expires; retry is deduplicated by the provider and returns the original reference | Extra latency; no duplicate SMS |
| 6 | Completer transaction fails repeatedly | Outcomes dropped | Leases expire; messages retried and deduplicated | Extra latency |
| 7 | Provider returns `5xx` or throttles | Retryable failure | Backoff and retry; normal messages switch provider if the circuit opens, Express rotates | Extra attempts visible on the message |
| 8 | Provider times out | Outcome unknown | Retry: same provider for normal (deduplicated); next provider for Express | Possible duplicate SMS for Express only |
| 9 | Provider rejects the message | Permanent failure | None needed | `failed`, `rejected`, refunded |
| 10 | Provider outage | Circuit opens in every worker within 20 requests | Traffic moves to the other provider; if none is usable, messages are deferred without consuming attempts; circuit probes every 10 s | Latency; messages past TTL expire and are refunded |
| 11 | All providers down longer than the TTL | Messages cannot be dispatched | Sweeper expires and refunds them | `expired`, refunded |
| 12 | DLR never arrives | Message stays `sent` | None; `sent` is the final known state | Status remains `sent` |
| 13 | DLR arrives twice | Second report matches no source status | Acknowledged as duplicate | No effect |
| 14 | DLR arrives before the `sent` commit | Applied from `accepted` | Completer preserves the terminal status | Correct final status |
| 15 | DLR arrives for a message already `failed` | Ignored | Counted as `ignored_terminal` | Message stays `failed` and refunded |
| 16 | Provider restarts | Deduplication store and pending DLRs lost | Retries of in-flight messages may be accepted again; lost DLRs leave messages `sent` | Rare duplicate SMS; some messages stay `sent` |
| 17 | Customer retries without `client_ref` | Treated as a new message | None; this is the documented contract | Duplicate message, charged twice |
| 18 | Burst from one customer | Token bucket exhausted | `429` with `Retry-After`; backlog confined to the customer's lane | `429` responses for that customer only |
| 19 | Large backlog in one lane | Lane FIFO grows | Round-robin keeps other lanes served | Delay only for customers in that lane |
| 20 | Customer deleted with messages in flight | Rows removed by cascade | Worker completions affect zero rows; DLRs counted as `unknown` | Not applicable |
| 21 | Clock skew between processes | None | All persisted times come from the database clock | None |
| 22 | Hot customer row contention | Accepts for that customer serialize | Batch endpoint reduces the lock rate | Higher latency for that customer only |

## Invariants that recovery preserves

Every recovery path above keeps the invariants in [Invariants](020-invariants.md) true. The chaos scenarios in [Test scenarios](../100-testing/020-scenarios.md) exercise rows 4, 5, 8, 10, and 16 and run the invariant checker afterwards.

## Related

- [Invariants](020-invariants.md)
- [Retries and failover](../060-processing/030-retries-and-failover.md)
- [Decision 006: at-least-once dispatch](../110-decisions/006-at-least-once-dispatch.md)
