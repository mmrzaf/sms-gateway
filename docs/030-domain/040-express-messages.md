# Express Messages

What the Express service class promises, what it does not promise, and the mechanisms that deliver the promise.

## Definition

Express is a **service class**, not a different kind of message. An Express message has the same fields, the same lifecycle, and goes through the same code path as a normal message. The class selects a different policy at every point where the system makes a choice: lane, worker pool, provider rate budget, retry timing, provider selection, TTL, and price.

A customer sends an Express message by setting `"type": "express"`.

## What Express guarantees

| Guarantee | Meaning | Mechanism |
|---|---|---|
| Durable acceptance | Once `202` is returned, the message survives any process crash | Accept transaction committed before responding (same as normal) |
| Isolated dispatch | Normal-class backlog does not delay Express dispatch | Dedicated lane, dedicated worker pool, reserved provider capacity |
| Bounded dispatch latency | Accept-to-sent latency is measured against an SLA (30 seconds by default) and every breach is recorded | `sla_breached` flag, latency histograms |
| Fast recovery from provider failure | A failing provider is bypassed on the next attempt | Failover to the next provider on every retry |
| Delivery tracking | The DLR outcome is recorded and reported | Same DLR path as normal |

## What Express does not guarantee

- **Delivery to the handset.** No gateway can guarantee this; the handset may be off or the number inactive. The DLR reports the outcome.
- **Exactly-once transmission.** If provider A times out after actually accepting a message, the retry goes to provider B, and the recipient can receive the message twice. Express deliberately trades a rare duplicate for latency. Normal messages do not fail over on timeouts, so they do not have this duplicate path. See [Decision 006](../110-decisions/006-at-least-once-dispatch.md).

## Policy comparison

| Policy | Normal | Express |
|---|---|---|
| Price per segment | 1 credit | 3 credits |
| Lane | `normal-<n>` by customer hash | `express` |
| Worker pool concurrency (per worker) | 256 | 64 |
| Wake-up | Polling every 100 ms when idle | `LISTEN express_ready`, plus polling |
| Provider rate budget | Shared portion only | Reserved portion first, then shared |
| Provider selection | Primary provider; the next one only while the primary's circuit is open | Rotates to the next usable provider on every retry |
| Provider request timeout | 5 s | 2 s |
| Maximum attempts | 8 | 5 |
| Backoff | 5 s × 2ⁿ⁻¹, capped at 10 min | 1 s × 2ⁿ⁻¹, capped at 10 s |
| TTL | 24 h | 5 min |
| SLA | None | Accepted by a provider within 30 s |

All values are configuration; see [Configuration](../090-operations/020-configuration.md).

## Isolation mechanisms

**Separate lane.** Express rows live in the `express` lane. Normal pools never claim from it, and Express pools never claim normal lanes, so a normal backlog of any size has no effect on which rows the Express pool sees.

**Dedicated pool.** Each worker runs an Express pool with its own concurrency limit. Busy normal goroutines cannot occupy Express capacity.

**Reserved provider capacity.** The real scarce resource is provider throughput. Each worker's rate budget for a provider is split: 20% (`EXPRESS_RESERVED_RATIO`) is available only to Express; the remaining 80% is shared. Express draws from its reservation first and then from the shared portion; normal traffic draws only from the shared portion. See [Lanes and fairness](../060-processing/020-lanes-and-fairness.md).

**Immediate wake-up.** The accept transaction issues `NOTIFY express_ready` for Express messages. The notification is delivered on commit, and idle Express pools claim immediately instead of waiting for the next poll.

## SLA tracking

An Express message is marked `sla_breached = true` when any of the following is true:

- It became `sent` more than `EXPRESS_SLA` after `accepted_at`.
- It is still `accepted` more than `EXPRESS_SLA` after `accepted_at` (flagged by the sweeper, so breaches are visible while they happen).
- It ended `failed` or `expired`.

Breaches are exposed on the message (`sla_breached`), in the summary report, in the metric `sms_express_sla_breaches_total`, and on the dashboard. A breach does not trigger a refund unless the message also ends `failed` or `expired`.

## Latency measurement

The histogram `sms_message_latency_seconds{type, stage}` records accept-to-sent (`stage="sent"`) and accept-to-terminal (`stage="completed"`) latency for both classes. The dashboard shows Express p50, p95, and p99 accept-to-sent latency over the last 5 minutes.

## Related

- [Decision 004: Express as a service class](../110-decisions/004-express-as-service-class.md)
- [Retries and failover](../060-processing/030-retries-and-failover.md)
- [Lanes and fairness](../060-processing/020-lanes-and-fairness.md)
