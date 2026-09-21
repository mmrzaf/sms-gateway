# Admin API

The JSON API behind the dashboard, for managing demo customers and inspecting the system. It is served on the admin port, which is not exposed publicly.

## Authentication

HTTP basic authentication with user `admin` and password `ADMIN_TOKEN`. The same credentials protect the dashboard pages. Requests without valid credentials receive `401` with `WWW-Authenticate: Basic realm="sms-gateway-admin"`.

Errors use the envelope described in [API conventions](010-conventions.md#errors).

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/admin/api/customers` | List customers |
| `POST` | `/admin/api/customers` | Create a customer |
| `GET` | `/admin/api/customers/{id}` | Get a customer with statistics |
| `PATCH` | `/admin/api/customers/{id}` | Update name or rate limit |
| `DELETE` | `/admin/api/customers/{id}` | Delete a customer and all its data |
| `POST` | `/admin/api/customers/{id}/rotate-key` | Replace the API key |
| `POST` | `/admin/api/customers/{id}/credits` | Add credits |
| `GET` | `/admin/api/messages` | List messages across customers |
| `GET` | `/admin/api/messages/{id}` | Get a message with operator fields |
| `GET` | `/admin/api/system` | Queue, worker, throughput, and latency overview |
| `GET` | `/admin/api/invariants` | Run the invariant checks |
| `GET` | `/admin/api/providers/{name}/config` | Read a provider's simulation settings |
| `PUT` | `/admin/api/providers/{name}/config` | Change a provider's simulation settings |
| `GET` | `/admin/api/providers/{name}/messages` | Recent messages received by a provider |
| `GET` | `/admin/api/providers/{name}/stats` | A provider's counters |

## Customers

**Customer resource**

```json
{
  "id": "01923f6e-...",
  "name": "acme",
  "api_key_prefix": "sk_3Qm8xT2a",
  "balance": 9412,
  "rate_limit_rps": 100,
  "created_at": "2026-09-21T09:00:00.000Z",
  "updated_at": "2026-09-21T10:15:30.123Z",
  "stats_24h": {
    "messages": 588,
    "by_status": { "accepted": 0, "sent": 3, "delivered": 560, "undelivered": 25, "failed": 0, "expired": 0 },
    "credits_spent": 588
  }
}
```

`stats_24h` covers messages accepted in the last 24 hours. The list endpoint returns customers without `stats_24h`, newest first, paginated with `limit` and `cursor`.

**POST /admin/api/customers**

```json
{ "name": "acme", "initial_credits": 10000, "rate_limit_rps": 100 }
```

| Field | Required | Rules |
|---|---|---|
| `name` | Yes | 1–100 characters |
| `initial_credits` | No | 0 – 1,000,000,000; default 0. A positive value creates a `charge` transaction. |
| `rate_limit_rps` | No | 1 – 100,000; default 100 |

Response `201`: the customer resource plus `"api_key": "sk_..."`. The full key is returned only here and by `rotate-key`.

**PATCH /admin/api/customers/{id}**

Accepts `name` and `rate_limit_rps`. The balance cannot be edited; use the credits endpoint. Response `200`: the customer resource.

**DELETE /admin/api/customers/{id}**

Deletes the customer with its messages, transactions, and queue rows. Response `204`.

**POST /admin/api/customers/{id}/rotate-key**

Generates a new key and invalidates the old one (within the 30-second key cache lifetime). Response `200`: `{ "api_key": "sk_...", "api_key_prefix": "sk_..." }`.

**POST /admin/api/customers/{id}/credits**

```json
{ "amount": 5000 }
```

Creates a `charge` transaction with `client_ref` `admin-<uuid>`. Response `201`: `{ "transaction": <transaction>, "balance": 14412 }`.

## Messages

**GET /admin/api/messages** accepts `customer_id`, `status`, `type`, `since`, `until`, `limit`, `cursor`. Without `customer_id`, it lists messages of all customers accepted within the last 24 hours.

**GET /admin/api/messages/{id}** returns the customer message resource plus operator fields:

```json
{
  "...": "all customer-facing fields",
  "customer_id": "01923f6e-...",
  "customer_name": "acme",
  "provider": "A",
  "provider_ref": "A-8f3a21c9",
  "last_error": "provider A: 503 Service Unavailable",
  "expires_at": "2026-09-22T10:15:30.123Z",
  "queue": { "lane": "normal-3", "next_attempt_at": "2026-09-21T10:15:45.000Z", "lease_owner": null }
}
```

`queue` is `null` when the message has no queue row.

## System overview

**GET /admin/api/system**

```json
{
  "generated_at": "2026-09-21T10:15:30.123Z",
  "queue": [
    { "lane": "express",  "ready": 0,    "delayed": 2,  "in_flight": 4,   "oldest_ready_age_s": 0 },
    { "lane": "normal-3", "ready": 5120, "delayed": 18, "in_flight": 200, "oldest_ready_age_s": 41 }
  ],
  "workers": [
    {
      "id": "gw-worker-1-7-a3f9",
      "role": "worker",
      "started_at": "2026-09-21T09:00:00.000Z",
      "last_seen": "2026-09-21T10:15:28.000Z",
      "stale": false,
      "stats": {
        "pools": { "express": { "concurrency": 64, "in_flight": 4 }, "normal": { "concurrency": 256, "in_flight": 200 } },
        "counters": { "sent": 918233, "retried": 1204, "deferred": 0, "failed": 17, "expired": 0 },
        "rates_per_s": { "sent": 1840.2, "retried": 3.1, "deferred": 0, "failed": 0.2, "expired": 0 },
        "circuits": { "A": "closed", "B": "closed" }
      }
    }
  ],
  "throughput_per_s": { "accepted": 2011.5 },
  "express_latency_s": { "window": "5m", "p50": 0.08, "p95": 0.21, "p99": 0.47, "sla_breaches": 0 }
}
```

| Field | Source |
|---|---|
| `queue` | Grouped count of `queue` by lane and derived state. Lanes with no rows are listed with zeros. |
| `workers` | `workers` table; `stale` when `last_seen` is older than 15 seconds. `stats` is the worker's own heartbeat document, including `rates_per_s`, which the worker computes from its counters between consecutive heartbeats. |
| `throughput_per_s.accepted` | Messages whose ID falls in the last 60 seconds, divided by 60. |
| `express_latency_s` | Percentiles of `sent_at - accepted_at` for Express messages accepted in the last 5 minutes that have been sent, and the number of those messages flagged as SLA breaches. |

## Invariants

**GET /admin/api/invariants** runs every check described in [Invariants](../070-reliability/020-invariants.md) and returns:

```json
{
  "ok": true,
  "checked_at": "2026-09-21T10:15:30.123Z",
  "checks": [
    { "name": "balance_matches_transactions", "ok": true, "violations": 0, "sample": [] }
  ]
}
```

`sample` holds up to 10 violating IDs per check.

## Providers

`{name}` is a provider name from `PROVIDERS` (`A` or `B`). These endpoints forward to the provider's own admin API (see [Internal and provider APIs](040-internal-and-provider-api.md)) and return its response unchanged. An unreachable provider returns `502` with code `provider_unreachable`.

## Related

- [Dashboard](../090-operations/050-dashboard.md)
- [Fake provider](../060-processing/050-fake-provider.md)
