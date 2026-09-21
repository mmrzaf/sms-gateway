# 006. At-Least-Once Dispatch with Provider Deduplication

Status: Accepted

## Context

Between a worker's call to a provider and the commit of its outcome, the worker can crash, the response can be lost, or the request can time out after the provider accepted it. Exactly-once delivery across two independent systems is not achievable without the provider's cooperation.

## Decision

Dispatch is at least once. The gateway sends its message ID with every request, and providers deduplicate on it, returning the original result for a repeated ID. Leases guarantee that any message whose outcome was not committed is attempted again. Normal messages retry the same provider after a timeout, so the retry is deduplicated. Express messages move to the next provider on every retry and accept that a timeout after acceptance can produce a duplicate transmission.

## Alternatives considered

**At most once** (mark as sent before calling the provider). Rejected: a crash loses paid messages.

**Two-phase handshake with providers.** Rejected: real operators do not offer it.

**Never fail over after timeouts, including Express.** Rejected for Express: waiting on a struggling provider would defeat the latency goal.

## Consequences

Positive:

- No accepted message is lost to any process crash.
- Normal messages are transmitted once in every failure case in which the provider keeps its deduplication state.
- Recovery needs no special code: an expired lease makes a row claimable again.

Negative:

- Express can transmit twice after a timeout followed by failover.
- A provider restart that loses its deduplication state can produce a duplicate for a message in flight.

## Related

- [Retries and failover](../060-processing/030-retries-and-failover.md)
- [Failure modes](../070-reliability/010-failure-modes.md)
