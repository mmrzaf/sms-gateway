# Configuration

Every configuration variable of the gateway and the fake provider, with defaults and validation rules.

All configuration comes from environment variables. Durations use Go syntax (`50ms`, `10s`, `24h`). On startup each binary validates all variables and, if any are invalid, exits with a message listing every invalid variable.

## Gateway

### Process and database

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | required | PostgreSQL connection string |
| `DB_MAX_CONNS` | `20` | Maximum pool connections per process |
| `HTTP_ADDR` | `:8080` | Public listener (api role) |
| `ADMIN_ADDR` | `:8081` | Admin, dashboard, DLR, metrics listener (api role) |
| `WORKER_ADDR` | `:8082` | Metrics and health listener (worker role) |
| `ADMIN_TOKEN` | required | Basic-auth password for the dashboard and admin API |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `SHUTDOWN_TIMEOUT` | `10s` | Grace period for in-flight work on shutdown |

### Customer API

| Variable | Default | Description |
|---|---|---|
| `API_INSTANCES` | `1` | Number of API instances; per-instance rate limits are divided by it |
| `DEFAULT_RATE_LIMIT_RPS` | `100` | Rate limit for customers created without one |
| `KEY_CACHE_TTL` | `30s` | Lifetime of cached API-key lookups |
| `PRICE_NORMAL` | `1` | Credits per segment, normal |
| `PRICE_EXPRESS` | `3` | Credits per segment, Express |
| `MAX_SEGMENTS` | `10` | Maximum segments per message (1–10) |

### Providers

| Variable | Default | Description |
|---|---|---|
| `PROVIDERS` | required | Ordered list `NAME=URL,NAME=URL`; order is the priority order |
| `PROVIDER_SECRET` | required | Shared secret expected in `X-Provider-Secret` on DLR callbacks |
| `PROVIDER_RATE_LIMIT` | `500` | Messages/s per provider per worker process |
| `EXPRESS_RESERVED_RATIO` | `0.2` | Share of the rate limit reserved for Express (0–1) |
| `CIRCUIT_FAILURE_THRESHOLD` | `20` | Consecutive counted failures that open a circuit |
| `CIRCUIT_OPEN_DURATION` | `10s` | Time a circuit stays open before a probe; also the deferral delay |

### Dispatch

| Variable | Default | Description |
|---|---|---|
| `NORMAL_LANES` | `16` | Number of normal lanes (1–1024); must be equal on API and worker processes |
| `NORMAL_CONCURRENCY` | `256` | Normal pool concurrency per worker |
| `EXPRESS_CONCURRENCY` | `64` | Express pool concurrency per worker |
| `CLAIM_BATCH_SIZE` | `200` | Maximum rows per claim |
| `LEASE_DURATION` | `30s` | Lease length; at least 3 × the largest provider timeout |
| `POLL_INTERVAL` | `100ms` | Idle wait after a rotation finds no work |
| `COMPLETER_BATCH_SIZE` | `200` | Outcomes per completer transaction |
| `COMPLETER_FLUSH_INTERVAL` | `50ms` | Maximum wait before a completer flush |
| `HEARTBEAT_INTERVAL` | `5s` | Worker heartbeat period |
| `SWEEP_INTERVAL` | `10s` | Sweeper period |

### Retry policies

| Variable | Default | Description |
|---|---|---|
| `NORMAL_MAX_ATTEMPTS` | `8` | |
| `NORMAL_BACKOFF_BASE` | `5s` | |
| `NORMAL_BACKOFF_MAX` | `10m` | |
| `NORMAL_TTL` | `24h` | |
| `NORMAL_TIMEOUT` | `5s` | Provider request timeout |
| `EXPRESS_MAX_ATTEMPTS` | `5` | |
| `EXPRESS_BACKOFF_BASE` | `1s` | |
| `EXPRESS_BACKOFF_MAX` | `10s` | |
| `EXPRESS_TTL` | `5m` | |
| `EXPRESS_TIMEOUT` | `2s` | Provider request timeout |
| `EXPRESS_SLA` | `30s` | Accept-to-sent limit for the SLA flag |

### Delivery reports

| Variable | Default | Description |
|---|---|---|
| `DLR_BATCH_SIZE` | `500` | Reports per batch |
| `DLR_FLUSH_INTERVAL` | `50ms` | Maximum wait before a batch commits |

`gateway migrate` and `gateway seed` read only `DATABASE_URL` and `DB_MAX_CONNS`. `gateway check` also reads `LEASE_DURATION` and `SWEEP_INTERVAL`, which set the thresholds of its time-based checks and should match the workers' values.

### Cross-field validation

- `LEASE_DURATION >= 3 × max(NORMAL_TIMEOUT, EXPRESS_TIMEOUT)`.
- `*_BACKOFF_BASE <= *_BACKOFF_MAX`.
- `EXPRESS_TIMEOUT <= EXPRESS_SLA`.
- `PROVIDERS` names are unique, match `^[A-Za-z0-9_-]{1,32}$`, and URLs are absolute `http` or `https` URLs.
- All counts, sizes, and durations are positive.

## Provider

| Variable | Default | Description |
|---|---|---|
| `PROVIDER_NAME` | required | Name, as used in the gateway's `PROVIDERS` |
| `PROVIDER_ADDR` | `:9001` | Listener |
| `GATEWAY_DLR_URL` | required | Full URL of the gateway's `/internal/dlr` |
| `PROVIDER_SECRET` | required | Sent as `X-Provider-Secret` |
| `LOG_LEVEL` | `info` | |
| `SIM_LATENCY_MS` | `50` | Initial `latency_ms` |
| `SIM_JITTER_MS` | `50` | Initial `jitter_ms` |
| `SIM_FAILURE_RATE` | `0` | Initial `failure_rate` |
| `SIM_TIMEOUT_RATE` | `0` | Initial `timeout_rate` |
| `SIM_REJECT_RATE` | `0` | Initial `reject_rate` |
| `SIM_OUTAGE` | `false` | Initial `outage` |
| `SIM_DELIVERY_RATIO` | `0.95` | Initial `delivery_ratio` |
| `SIM_DLR_DELAY_MS` | `1000` | Initial `dlr_delay_ms` |
| `SIM_DLR_JITTER_MS` | `500` | Initial `dlr_jitter_ms` |

Simulation settings can be changed at runtime; see [Fake provider](../060-processing/050-fake-provider.md#simulation-settings).

## Related

- [Deployment](030-deployment.md)
- [Retries and failover](../060-processing/030-retries-and-failover.md)
