# Fake Provider

The simulated SMS operator: what it does with each request, how its failures are controlled, and how it sends delivery reports.

## Purpose

The fake provider stands in for a real operator so that retries, failover, circuit breaking, delivery reports, and Express behavior can be demonstrated and tested. It is a separate binary and service, and the gateway talks to it only through the [provider send API](../050-api/040-internal-and-provider-api.md#provider-send-api), exactly as it would talk to an adapter for a real operator. Two instances, `A` and `B`, run in the standard deployment.

## Request processing

Each `POST /send` is processed in this order:

| Step | Condition | Response | Recorded result |
|---|---|---|---|
| 1 | `outage` is on | `503` immediately | `outage` |
| 2 | Message `id` seen before | `200` with the original `provider_ref`, after latency | Duplicate counter incremented |
| 3 | Recipient starts with `+999` | `400 invalid_recipient` | `rejected` |
| 4 | Random draw below `reject_rate` | `400 invalid_recipient` | `rejected` |
| 5 | Random draw below `failure_rate` | `500` | `failed` |
| 6 | Accept: assign `provider_ref`, schedule DLR | | `accepted` |
| 7 | Random draw below `timeout_rate` | Response held for 30 s | `timed_out` |
| 8 | Otherwise | `200` after latency | |

- **Latency** is `latency_ms` plus a uniform random value in `[0, jitter_ms]`, applied before every response except `outage`.
- **Timeouts accept the message first**, then hold the response longer than any gateway timeout. This reproduces the ambiguous case of a real operator: the message was accepted, but the gateway does not learn it until a retry is answered from the deduplication store.
- `provider_ref` has the form `<name>-<8 hex characters>`, for example `A-8f3a21c9`.
- Recipients starting with `+999` are always rejected and recipients starting with `+998` always receive an `undelivered` DLR, so tests can produce these outcomes deterministically.

## Delivery reports

For each accepted message, a report is scheduled after `dlr_delay_ms` plus a uniform random value in `[0, dlr_jitter_ms]`. The status is `delivered` with probability `delivery_ratio`, otherwise `undelivered` (always `undelivered` for `+998` recipients).

Reports are sent to `GATEWAY_DLR_URL` with `X-Provider-Secret`. A `503` or network error is retried with backoff starting at 1 s, doubling up to 60 s, for at most 10 attempts; `200`, `400`, and `401` end delivery. Reports that exhaust their attempts are counted as `dlr_failed`.

## Simulation settings

| Setting | Default | Range |
|---|---|---|
| `latency_ms` | 50 | 0–60,000 |
| `jitter_ms` | 50 | 0–60,000 |
| `failure_rate` | 0.0 | 0–1 |
| `timeout_rate` | 0.0 | 0–1 |
| `reject_rate` | 0.0 | 0–1 |
| `outage` | false | |
| `delivery_ratio` | 0.95 | 0–1 |
| `dlr_delay_ms` | 1000 | 0–60,000 |
| `dlr_jitter_ms` | 500 | 0–60,000 |

Initial values come from environment variables (see [Configuration](../090-operations/020-configuration.md#provider)); they can be changed at runtime through `PUT /admin/config` or the dashboard, and changes apply to the next request.

## State and limits

All state is in memory:

| State | Bound |
|---|---|
| Deduplication store (message ID → provider reference) | 1,000,000 entries, oldest evicted first |
| Recent messages for the admin view | 10,000 entries, ring buffer |
| Pending delivery reports | Unbounded; drained continuously |

Restarting a provider loses this state: previously accepted messages can be accepted again on retry, and their pending DLRs are lost, which leaves those messages in `sent`. This matches the behavior the gateway must tolerate from real operators.

## Related

- [Internal and provider APIs](../050-api/040-internal-and-provider-api.md)
- [Retries and failover](030-retries-and-failover.md)
- [Demo walkthrough](../010-overview/030-demo-walkthrough.md)
