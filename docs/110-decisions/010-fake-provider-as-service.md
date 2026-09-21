# 010. Fake Provider as a Separate Service

Status: Accepted

## Context

Real SMS operators are out of scope, but retries, failover, circuit breaking, delivery reports, and Express behavior must be demonstrated and tested against something that behaves like an operator: remote, slow, unreliable, and asynchronous.

## Decision

The fake provider is a separate binary and service implementing the same contract a real operator adapter would: `POST /send` with deduplication by message ID, and delivery reports through an HTTP callback. Two instances run. Its latency, failures, timeouts, rejections, outages, and delivery ratio are controllable at runtime. The gateway has no code specific to the fake provider.

## Alternatives considered

**An in-process simulated provider.** Rejected: it would not exercise timeouts, connection errors, network retries, or callback delivery, and the gateway would contain simulation code.

**A single provider instance.** Rejected: failover could not be demonstrated.

## Consequences

Positive:

- The gateway's provider code is the code that would run against a real operator.
- Every failure mode in [Failure modes](../070-reliability/010-failure-modes.md) can be reproduced on demand.
- Integration tests reuse the provider's handler in-process through `httptest`.

Negative:

- One more service to run locally.
- Provider state is in memory; a restart loses deduplication state and pending reports, which is also realistic.

## Related

- [Fake provider](../060-processing/050-fake-provider.md)
- [Internal and provider APIs](../050-api/040-internal-and-provider-api.md)
