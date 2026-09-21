# Dashboard

The internal operator console: what each page shows, where its data comes from, and what it lets an operator do. It is a demonstration and debugging tool, not a customer product.

## Access and technology

- Served by the API role on the admin port at `/dashboard`.
- Protected by HTTP basic authentication (user `admin`, password `ADMIN_TOKEN`).
- Server-rendered with `html/template`; htmx refreshes live panels every 2 seconds by requesting HTML fragments. There is no separate frontend build.
- Every page is backed by the [Admin API](../050-api/030-admin-api.md); the dashboard adds no data access of its own.

## Pages

### Customers

| Element | Content |
|---|---|
| List | Name, key prefix, balance, rate limit, created time |
| Create form | Name, initial credits, rate limit; shows the new API key once, with a copy button |
| Detail | Balance, rate limit, last 24 h statistics by status, recent transactions, recent messages |
| Actions | Edit name and rate limit, add credits, rotate key (shows the new key once), delete with confirmation |

### Messages

| Element | Content |
|---|---|
| Filters | Customer, status, type, time range |
| List | ID, customer, type, recipient, segments, cost, status, attempts, accepted and sent times |
| Detail | All fields including provider, provider reference, last error, SLA flag, timestamps, and the current queue row (lane, next attempt, lease owner) |

### System

| Panel | Content | Source |
|---|---|---|
| Queue | Ready, delayed, and in-flight rows per lane; oldest ready age | `GET /admin/api/system` |
| Workers | One card per worker: pools and in-flight counts, circuit state per provider, rates of sent, retried, deferred, failed, expired; stale workers highlighted | `workers` table |
| Throughput | Accepted messages per second | Messages accepted in the last minute |
| Express | p50, p95, p99 accept-to-sent latency over 5 minutes; SLA breaches | Recent Express messages |
| Invariants | Button that runs the checker and shows each check's result | `GET /admin/api/invariants` |

### Providers

One tab per provider.

| Element | Content |
|---|---|
| Controls | Latency, jitter, failure rate, timeout rate, reject rate, outage toggle, delivery ratio, DLR delay and jitter; changes apply immediately |
| Counters | Received, accepted, duplicates, rejected, failed, timed out, outage responses, DLRs sent, pending, failed |
| Received messages | Latest messages as the provider saw them: ID, recipient, text, provider reference, result, DLR status, duplicate count |

## What the dashboard demonstrates

| Behavior | Where to look |
|---|---|
| Message lifecycle | Messages → detail |
| Credit correctness | Customers → detail transactions; System → Invariants |
| Fairness | System → Queue while running the noisy-neighbor scenario |
| Express isolation | System → Express latency while normal lanes are saturated |
| Retries and failover | Providers → controls, then Messages → detail attempts and provider |
| Circuit breaking | System → Workers circuit states during a provider outage |
| Crash recovery | System → Workers after killing a worker; in-flight rows return to ready |

## Related

- [Admin API](../050-api/030-admin-api.md)
- [Demo walkthrough](../010-overview/030-demo-walkthrough.md)
- [Observability](040-observability.md)
