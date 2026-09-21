# 008. Delivery Reports Committed in Batches

Status: Accepted

## Context

At the target rate, providers send about 10,000 delivery reports per second. One transaction per report would add 10,000 commits per second to the database. Acknowledging a report before it is stored would lose it on a crash, because the provider would not resend it.

## Decision

The DLR handler places each report in an in-process batcher and waits. The batcher commits up to 500 reports in one transaction, at least every 50 ms, and releases each waiting handler with its result. The handler responds `200` only after its report's batch has committed.

## Alternatives considered

**One transaction per report.** Rejected: commit rate.

**Acknowledge immediately and write asynchronously.** Rejected: reports are lost on a crash.

**Write reports to a staging table and apply them in the background.** Rejected: the same commit cost plus a second write per report.

## Consequences

Positive:

- About 20 commits per second at the target rate instead of 10,000.
- A `200` always means the report is durable.

Negative:

- Each callback waits up to one flush interval (50 ms) longer.
- A failed batch fails every report in it; providers retry them all.

## Related

- [Delivery reports](../060-processing/040-delivery-reports.md)
