# 007. In-Process Rate Limiting

Status: Accepted

## Context

Each customer has a message rate limit. With one API instance, an in-memory token bucket is exact. With several instances behind a load balancer, limits must be either shared (a central store) or divided among instances.

## Decision

Each API instance keeps per-customer token buckets in memory, with rate `rate_limit_rps / API_INSTANCES`. There is no shared rate-limit store.

## Alternatives considered

**Redis token buckets shared by all instances.** Exact across instances, but adds a stateful component to the request path and a failure mode (Redis unavailable) to every request.

**Database-backed counters.** Rejected: adds a write per request to the most contended part of the system.

## Consequences

Positive:

- No extra component; no network call on the request path.
- Limits are exact with one instance and close to exact with even load balancing.

Negative:

- With uneven load balancing, a customer can be limited below or above its nominal rate by the imbalance.
- `API_INSTANCES` must be kept equal to the real instance count.

Redis becomes worthwhile when instances are numerous or load balancing is uneven ([Scaling path](../080-scalability/030-scaling-path.md#not-covered)).

## Related

- [API conventions](../050-api/010-conventions.md#rate-limiting)
