# Demo Walkthrough

A guided tour that shows the system's main behaviors in about fifteen minutes: acceptance and dispatch, credit correctness under concurrency, fairness between customers, Express isolation, and recovery from a provider outage.

## Prerequisites

- The stack is running: `make up` (see [Running locally](../090-operations/010-running-locally.md)).
- Demo customers exist and their keys are exported:

```sh
eval "$(make -s seed)"      # sets ACME_KEY, BULK_KEY, QUICK_KEY
```

| Customer | Credits | Rate limit | Key variable |
|---|---|---|---|
| `acme` | 10,000 | 100 msg/s | `ACME_KEY` |
| `bulkco` | 1,000,000 | 2,000 msg/s | `BULK_KEY` |
| `quickpay` | 50,000 | 200 msg/s | `QUICK_KEY` |

- The dashboard is open at `http://localhost:8081/dashboard` (user `admin`, password from `ADMIN_TOKEN`, `admin` by default).
- The API reference is open at `http://localhost:8080/docs`.

## 1. Send a message and follow it

```sh
curl -s -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer $ACME_KEY" \
  -H "Content-Type: application/json" \
  -d '{"to":"+989121234567","text":"Your code is 482913","client_ref":"otp-1"}'
```

The response is `202 Accepted` with `status: "accepted"`, `segments: 1`, and `cost: 1`. Fetch it again with `GET /v1/messages/{id}`: within a moment the status becomes `sent`, and about a second later `delivered` (or `undelivered`, which the fake provider produces for 5% of messages by default).

On the dashboard, **Messages** shows the same lifecycle with timestamps and attempts, and **Providers → A** shows the message as the operator received it.

Send the same request again: the response is the original message with the header `Idempotent-Replayed: true`, and the balance does not change.

## 2. Encoding and segments

```sh
curl -s -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer $ACME_KEY" -H "Content-Type: application/json" \
  -d '{"to":"+989121234567","text":"سلام، کد شما ۴۸۲۹۱۳ است"}'
```

The text contains Persian characters, so the encoding is `ucs2` (70 characters per single segment). A Persian text of 71–134 characters costs 2 credits; see [Segments and pricing](../030-domain/030-segments-and-pricing.md).

## 3. Credits under concurrency

```sh
make loadtest SCENARIO=balance-race
```

The scenario creates a customer with 100 credits and fires 1,000 concurrent single-segment sends. Expected result: exactly 100 responses are `202`, exactly 900 are `402 insufficient_credits`, and the final balance is `0`. The scenario ends by running the invariant checker, which must report every check as passing:

```sh
make check
```

## 4. Noisy neighbor

```sh
make loadtest SCENARIO=noisy-neighbor
```

`bulkco` floods the gateway at its full rate limit while `acme` sends a steady trickle. On the dashboard, **System** shows queue depth growing only in the lane `bulkco` hashes to, and the scenario's report shows `acme`'s accept-to-sent latency staying close to its unloaded value. See [Lanes and fairness](../060-processing/020-lanes-and-fairness.md).

## 5. Express under load

```sh
make loadtest SCENARIO=express-under-load
```

`bulkco` saturates the normal lanes while `quickpay` sends Express messages. The dashboard's Express latency panel (p50/p95/p99, accept to sent) stays low and the SLA breach counter stays at zero, because Express has its own lane, its own worker pool, and reserved provider capacity. See [Express messages](../030-domain/040-express-messages.md).

## 6. Provider outage and failover

1. On **Providers → A**, enable **Outage**. Provider A now answers every request with `503`.
2. Run `make loadtest SCENARIO=provider-outage`, which sends a mix of normal and Express messages.
3. Observe:
   - Provider A's circuit opens in each worker (**System → Workers**).
   - Normal messages move to provider B as long as B's circuit is closed; when both are unavailable, normal messages wait in the queue without consuming attempts.
   - Express messages fail over to provider B on their next attempt.
4. Disable the outage. The circuit probes A, closes, and the backlog drains.
5. Run `make check`. All invariants pass: no message was lost, and every refund corresponds to a message that terminally failed.

## 7. Failure injection

On **Providers → A**, set failure rate to `0.3` and timeout rate to `0.05`, then send traffic. Messages show several attempts with `last_error` values, eventually reaching `sent`. Timeouts illustrate the ambiguous case: provider A accepted the message but the response never arrived; the retry is deduplicated by the provider and returns the original provider reference. See [Retries and failover](../060-processing/030-retries-and-failover.md).

## 8. Worker crash

```sh
docker compose -f deploy/docker-compose.yml kill gateway-worker
docker compose -f deploy/docker-compose.yml up -d gateway-worker
```

`make chaos SCENARIO=worker-kill` runs the same failure repeatedly under load.

Messages that were claimed by the killed worker become claimable again when their lease expires (30 seconds) and are dispatched by the restarted worker. `make check` passes afterwards.

## Related

- [Test scenarios](../100-testing/020-scenarios.md)
- [Dashboard](../090-operations/050-dashboard.md)
