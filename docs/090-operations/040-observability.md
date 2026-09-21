# Observability

The metrics, logs, and health signals the system exposes, and how to read them.

## Metrics

Every process serves Prometheus metrics at `/metrics`: the API role on the admin port, the worker role on the worker port, providers on their own port. Standard Go runtime and process metrics are included.

### API

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `sms_api_requests_total` | counter | `route`, `code` | Requests by route pattern and HTTP status |
| `sms_api_request_duration_seconds` | histogram | `route` | Request latency |
| `sms_messages_accepted_total` | counter | `type` | Messages accepted |
| `sms_rate_limited_total` | counter | — | Messages refused with `429` |
| `sms_credits_debited_total` | counter | — | Credits debited at acceptance |
| `sms_credits_charged_total` | counter | — | Credits added by charges |
| `sms_dlr_received_total` | counter | `outcome` | DLRs: `applied`, `duplicate`, `ignored_terminal`, `unknown`, `invalid` |
| `sms_dlr_batch_size` | histogram | — | Reports per DLR commit |

### Worker

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `sms_dispatch_attempts_total` | counter | `provider`, `type`, `outcome` | Attempts: `sent`, `retry`, `rejected`, `timeout` |
| `sms_messages_completed_total` | counter | `type`, `status` | Messages reaching `sent`, `failed`, or `expired` through this worker |
| `sms_deferrals_total` | counter | `type` | Messages deferred for lack of a usable provider |
| `sms_credits_refunded_total` | counter | — | Credits refunded |
| `sms_provider_request_duration_seconds` | histogram | `provider` | Provider call latency |
| `sms_circuit_state` | gauge | `provider` | `0` closed, `1` half-open, `2` open |
| `sms_pool_in_flight` | gauge | `pool` | Sends in progress |
| `sms_claim_size` | histogram | `pool` | Rows per claim |
| `sms_completer_batch_size` | histogram | — | Outcomes per completer commit |
| `sms_message_latency_seconds` | histogram | `type`, `stage` | Accept-to-`sent` and accept-to-terminal latency |
| `sms_express_sla_breaches_total` | counter | — | Express messages flagged as breached |
| `sms_queue_depth` | gauge | `lane`, `state` | Rows per lane and state, sampled by the sweeper |
| `sms_queue_oldest_ready_seconds` | gauge | `lane` | Age of the oldest ready row, sampled by the sweeper |

### Provider

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `provider_requests_total` | counter | `result` | Send requests by result |
| `provider_dlr_sent_total` | counter | `status` | DLRs delivered to the gateway |
| `provider_dlr_pending` | gauge | — | DLRs waiting to be sent |

## Signals worth alerting on

| Condition | Meaning |
|---|---|
| `sms_queue_oldest_ready_seconds{lane="express"}` > 5 | Express is not being claimed promptly |
| Any `sms_queue_oldest_ready_seconds` > 300 | A lane is falling behind |
| `sms_circuit_state` = 2 for more than 1 minute | A provider is down |
| `rate(sms_express_sla_breaches_total[5m])` > 0 | Express SLA is being missed |
| Invariant checker reports a violation | Correctness defect; investigate immediately |
| `rate(sms_dlr_received_total{outcome="unknown"}[5m])` rising | Providers report messages the gateway does not know |

## Logs

All processes log JSON lines through `log/slog` to standard output.

| Field | Present on |
|---|---|
| `time`, `level`, `msg` | Every line |
| `role`, `worker_id` | Every line from gateway processes |
| `request_id` | Lines produced while handling an HTTP request |
| `customer_id` | Customer API lines after authentication |
| `message_id`, `lane`, `provider`, `attempt` | Dispatch lines |
| `error` | Failures |

Levels:

- `error`: failures needing attention (completer transaction failed after retries, database unreachable).
- `warn`: degraded operation (circuit opened, lease reclaimed, DLR batch failed).
- `info`: lifecycle events (startup, shutdown, configuration summary, circuit closed).
- `debug`: per-message events, including recipients and bodies.

API keys and secrets are never logged at any level.

## Health

| Endpoint | Process | Meaning |
|---|---|---|
| `/healthz` | All | Process is alive |
| `/readyz` | Gateway | Database reachable within 1 second |

## Dashboard

The dashboard presents the most useful of these signals without a metrics stack; see [Dashboard](050-dashboard.md). The Prometheus endpoints allow the same data to be scraped into any monitoring system.

## Related

- [Dashboard](050-dashboard.md)
- [Invariants](../070-reliability/020-invariants.md)
