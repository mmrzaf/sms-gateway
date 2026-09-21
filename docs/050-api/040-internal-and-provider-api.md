# Internal and Provider APIs

The two contracts between the gateway and SMS providers: the provider's send API, which the gateway calls, and the DLR callback, which providers call. Also the fake provider's own admin API.

## Provider send API

Implemented by each provider; called by gateway workers.

**POST /send**

```json
{ "id": "01923f6e-7a1c-7b3e-9f10-2c4d5e6f7a8b", "to": "+989121234567", "text": "Your code is 482913" }
```

`id` is the gateway's message ID. The provider must treat it as an idempotency key: a repeated `id` returns the original result without transmitting again.

| Status | Body | Gateway interpretation |
|---|---|---|
| `200` | `{ "provider_ref": "A-8f3a21c9", "accepted_at": "..." }` | Accepted: message becomes `sent` |
| `400` | `{ "error": "invalid_recipient" }` or `{ "error": "invalid_request" }` | Permanent: message `failed`, reason `rejected` |
| `429` | `{ "error": "throttled" }` | Retryable |
| `5xx` | any | Retryable |
| Timeout or connection error | — | Retryable; outcome unknown, resolved by provider deduplication on retry |

Any other `4xx` is treated as permanent. The gateway sends `Content-Type: application/json` and applies the class's request timeout (5 s normal, 2 s Express).

Gateway-to-provider traffic stays on the private network and is not authenticated.

## DLR callback

Implemented by the gateway on the admin port; called by providers.

**POST /internal/dlr**

```
X-Provider-Secret: <PROVIDER_SECRET>
```

```json
{
  "message_id": "01923f6e-7a1c-7b3e-9f10-2c4d5e6f7a8b",
  "provider": "A",
  "provider_ref": "A-8f3a21c9",
  "status": "delivered",
  "reported_at": "2026-09-21T10:15:31.480Z"
}
```

| Field | Rules |
|---|---|
| `message_id` | UUID |
| `provider` | A configured provider name |
| `provider_ref` | Non-empty |
| `status` | `delivered` or `undelivered` |
| `reported_at` | RFC 3339; informational, not used for ordering |

| Status | When | Provider behavior |
|---|---|---|
| `200` | Report committed, or recognized as a duplicate, unknown, or late | Stop |
| `400` | Malformed body | Stop; the report is unusable |
| `401` | Wrong or missing secret | Stop |
| `503` | The database is unavailable | Retry with backoff |

The gateway answers `200` only after the report's batch has committed, so a `200` means the report is durable. Unknown message IDs and reports for terminal messages are acknowledged with `200` to stop the provider from retrying forever; both are counted in metrics. Processing rules are in [Delivery reports](../060-processing/040-delivery-reports.md).

## Fake provider admin API

Served by each fake provider instance on its own port, on the private network. The gateway's admin API forwards to these endpoints.

**GET /admin/config** returns the current simulation settings:

```json
{
  "latency_ms": 50,
  "jitter_ms": 50,
  "failure_rate": 0.0,
  "timeout_rate": 0.0,
  "reject_rate": 0.0,
  "outage": false,
  "delivery_ratio": 0.95,
  "dlr_delay_ms": 1000,
  "dlr_jitter_ms": 500
}
```

**PUT /admin/config** accepts any subset of these fields and returns the full resulting configuration. Rates are between `0` and `1`; durations are 0 – 60,000 ms. Invalid values return `422`.

**GET /admin/messages?limit=100** returns the most recent received messages, newest first (`limit` 1–1,000):

```json
{
  "data": [
    {
      "id": "01923f6e-...", "to": "+989121234567", "text": "Your code is 482913",
      "provider_ref": "A-8f3a21c9", "received_at": "...",
      "result": "accepted", "dlr_status": "delivered", "dlr_sent_at": "...", "duplicates": 0
    }
  ]
}
```

`result` is `accepted`, `rejected`, `failed`, `timed_out`, or `outage`. `dlr_status` is `pending`, `delivered`, or `undelivered`. `duplicates` counts repeated sends of the same `id`.

**GET /admin/stats** returns cumulative counters:

```json
{
  "received": 120433, "accepted": 118902, "duplicates": 211, "rejected": 12,
  "failed": 1302, "timed_out": 0, "outage": 6, "dlr_sent": 118800,
  "dlr_pending": 102, "dlr_failed": 0
}
```

## Related

- [Delivery reports](../060-processing/040-delivery-reports.md)
- [Fake provider](../060-processing/050-fake-provider.md)
- [Retries and failover](../060-processing/030-retries-and-failover.md)
