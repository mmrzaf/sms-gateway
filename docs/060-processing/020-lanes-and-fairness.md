# Lanes and Fairness

How the gateway keeps a high-volume customer from delaying other customers, and normal traffic from delaying Express traffic.

## Two layers of protection

| Layer | Protects against | Mechanism |
|---|---|---|
| Ingestion | One customer overwhelming the API and filling the queue faster than it drains | Per-customer rate limit (token bucket) |
| Dispatch | A backlog from one customer delaying messages of others | Lanes with round-robin claiming, separate Express lane and pool, reserved provider capacity |

Ingestion limits alone are not enough: a customer with a large balance and a high rate limit can legitimately build a backlog of millions of messages. Dispatch fairness decides who is served while that backlog drains.

## Lanes

A lane is a value of `queue.lane`. Each lane is claimed independently.

| Lane | Contains |
|---|---|
| `express` | All Express messages |
| `normal-0` … `normal-15` | Normal messages; the lane is `normal-(fnv32a(customer_id) mod NORMAL_LANES)` |

`NORMAL_LANES` defaults to 16. A customer's normal messages always land in the same lane, so one customer's backlog is confined to one lane.

## Round-robin claiming

The normal pool rotates through its lanes and claims a batch from each lane in turn. When every lane has work, each lane receives an equal share of the normal pool's dispatch capacity. When only one lane has work, it receives all of it; capacity is never left idle while any work is ready.

**Example.** `bulkco` hashes to `normal-3` and builds a backlog of 500,000 messages. `acme` hashes to `normal-9` and sends 10 messages per second.

- Without other traffic, `normal-3` gets the whole normal pool and drains as fast as providers allow.
- When `acme`'s messages arrive, `normal-9` has ready rows. The pool claims from it on its next pass through the rotation, so `acme`'s messages wait for at most one rotation plus their own dispatch time, regardless of the size of `bulkco`'s backlog.

An empty lane costs one index lookup per rotation, so a full rotation over 16 lanes is cheap even when most are empty.

## Limits of this scheme

Customers that hash to the same lane share it in FIFO order. If `acme` shared `normal-3` with `bulkco`, it would wait behind `bulkco`'s backlog. With 16 lanes, a flood affects on average 1/16 of the other customers, and ingestion rate limits cap how quickly a backlog can grow. The path to tighter isolation (more lanes, dedicated lanes for designated high-volume customers, or per-customer round-robin) is described in [Scaling path](../080-scalability/030-scaling-path.md) and [Decision 005](../110-decisions/005-hashed-lanes-for-fairness.md).

## Express isolation

Express traffic does not compete with normal traffic at any stage:

| Stage | Isolation |
|---|---|
| Queue | Separate `express` lane; normal pools never claim it |
| Workers | Separate Express pool with its own concurrency (64 per worker) |
| Wake-up | `LISTEN express_ready`, so Express does not wait for a poll interval |
| Provider capacity | A reserved share of each provider's rate budget |

## Reserved provider capacity

Providers accept a limited rate of messages. Each worker process holds a rate budget per provider of `PROVIDER_RATE_LIMIT` messages per second (default 500), split into two token buckets:

| Bucket | Share | Used by |
|---|---|---|
| Reserved | `EXPRESS_RESERVED_RATIO` (default 20%): 100 msg/s | Express only |
| Shared | The remainder: 400 msg/s | Normal and Express |

Express takes a token from the reserved bucket when one is available, and otherwise from whichever bucket can grant one sooner. Normal takes tokens only from the shared bucket. Under full normal load, Express therefore always has at least 20% of provider capacity, and when Express is idle, normal traffic loses at most that 20%.

`PROVIDER_RATE_LIMIT` is per worker process. When running `k` worker processes against a provider that accepts `R` messages per second, set it to `R / k`.

## Ingestion rate limiting

Each customer's `rate_limit_rps` is enforced at the API with a token bucket of one token per message and capacity `max(2 × rate_limit_rps, 500)`. Details and headers are in [API conventions](../050-api/010-conventions.md#rate-limiting).

## Related

- [Queue and workers](010-queue-and-workers.md)
- [Express messages](../030-domain/040-express-messages.md)
- [Decision 005: hashed lanes](../110-decisions/005-hashed-lanes-for-fairness.md)
