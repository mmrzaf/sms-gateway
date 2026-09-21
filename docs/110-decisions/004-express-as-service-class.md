# 004. Express as a Service Class

Status: Accepted

## Context

Express messages need stronger latency and delivery behavior than normal messages. The system could implement Express as a separate pipeline with its own tables, code, and processes, or as the same pipeline with different policies. Either way, Express must not be slowed down by normal backlogs, and the implementation must stay small enough to be verified.

## Decision

Express is a value of `messages.type`. The message, lifecycle, tables, and code path are shared. A policy object per class supplies every decision that differs: lane, pool, rate budget share, retry settings, provider selection, TTL, SLA, and price. Isolation comes from a dedicated lane, a dedicated worker pool, a reserved share of provider capacity, and immediate wake-up via `NOTIFY`.

## Alternatives considered

**Separate Express pipeline** (own tables, own workers, own code). Rejected: duplicates the lifecycle, the accounting, and their tests, for isolation that lanes and pools already provide.

**One queue with a priority field.** Rejected: priority reorders work but does not reserve capacity. Workers busy with normal messages, and provider rate limits consumed by them, would still delay Express.

**Separate deployments for Express workers.** Not required; each worker process runs both pools with fixed concurrency. Separate deployments remain possible by configuration if Express needs to scale independently.

## Consequences

Positive:

- One lifecycle and one accounting model, tested once.
- Isolation is structural: normal load cannot occupy the Express lane, pool, or reserved provider capacity.
- Adding a third class would be a new policy and lane.

Negative:

- The reserved provider share is unavailable to normal traffic whenever Express is busy, reducing normal throughput by up to that share.
- Express failover after timeouts can transmit a message twice; this is documented as part of the class's contract.

## Related

- [Express messages](../030-domain/040-express-messages.md)
- [Lanes and fairness](../060-processing/020-lanes-and-fairness.md)
