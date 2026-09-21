# 005. Hashed Lanes for Fair Dispatch

Status: Accepted

## Context

A single FIFO queue lets one customer's backlog of millions delay every other customer. Ingestion rate limits bound how fast a backlog grows but not who is served while it drains. True per-customer fair queuing requires tracking every customer with pending work and costs more queries per claim.

## Decision

Normal messages are assigned to one of `NORMAL_LANES` lanes (default 16) by hashing the customer ID. The normal pool claims from lanes in round-robin order. Express has its own lane.

## Alternatives considered

**Single FIFO.** Rejected: no isolation.

**Per-customer round-robin (deficit round robin over active customers).** Fairest, but requires maintaining a set of active customers and one claim per customer per rotation. Kept as a later stage ([Scaling path, stage 3](../080-scalability/030-scaling-path.md#stage-3-tighter-fairness)).

**Dedicated lanes for designated heavy customers.** Useful, but requires deciding in advance who is heavy. Also kept for stage 3.

## Consequences

Positive:

- A backlog is confined to one lane; customers in other lanes wait at most one rotation.
- Work-conserving: an idle lane gives its share to busy ones.
- One column and one index; claims stay single-statement.

Negative:

- Customers sharing a lane with a flooding customer are delayed behind it. With 16 lanes this affects about 1/16 of customers, and the number of lanes is configurable.
- Changing `NORMAL_LANES` requires the sweeper to reassign rows from removed lanes.

## Related

- [Lanes and fairness](../060-processing/020-lanes-and-fairness.md)
