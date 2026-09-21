# Test Scenarios

Every integration test, load scenario, and chaos scenario, with what it proves and how it passes.

## Integration tests

All run with `make test-integration`, and all end with the invariant checker.

| ID | Test | Setup and action | Passes when |
|---|---|---|---|
| IT01 | Overspend under concurrency | Balance 100; 1,000 concurrent single-segment accepts | Exactly 100 accepted, 900 `insufficient_credits`, balance 0 |
| IT02 | Charges and sends interleaved | Balance 0; 500 concurrent charges of 1 and 1,000 concurrent sends | Accepted count equals credits available at each commit; final balance equals charges minus accepted cost |
| IT03 | Concurrent duplicate `client_ref` | 50 concurrent identical sends with one `client_ref` | One message, one debit; 49 replays |
| IT04 | `client_ref` conflict | Same `client_ref`, different text | `idempotency_conflict`; no second debit |
| IT05 | Batch atomicity | Balance 250; batch of 300 | `insufficient_credits`; no messages, no transactions |
| IT06 | Batch idempotency | Full replay, partial overlap, duplicate within a batch | Replay returns originals; overlap `409`; duplicate `422` |
| IT07 | Charge idempotency | Same charge `client_ref` twice, then with a different amount | One transaction; then `409` |
| IT08 | Lease recovery | Claim rows and abandon them; lease 200 ms | Rows reclaimed by another worker and sent; each message sent once by the provider |
| IT09 | Crash after provider acceptance | Provider accepts, completer outcome dropped | After lease expiry, the retry is deduplicated; one provider acceptance; status `sent` |
| IT10 | Early DLR | DLR `delivered` committed before the completer's `sent` | Final status `delivered`; `sent_at` and provider reference recorded |
| IT11 | Permanent rejection | Provider returns `400` | `failed`, `rejected`, exactly one refund |
| IT12 | Attempts exhausted | Provider always `500`; max attempts 3; tiny backoff | `failed`, `attempts_exhausted`, 3 attempts, one refund |
| IT13 | Expiry during outage | All providers in outage; TTL 500 ms | Sweeper marks `expired`; one refund; attempts not incremented by deferrals |
| IT14 | Late DLR on failed message | Message `failed`, then `delivered` DLR | Status stays `failed`; DLR counted `ignored_terminal`; refund unchanged |
| IT15 | Circuit breaker | Provider A fails; threshold 5 | Circuit opens after 5 failures; normal traffic goes to B; A is probed and closes after recovery |
| IT16 | Express rotation | Provider A times out | Second attempt goes to B; message `sent` via B |
| IT17 | Normal does not rotate on timeout | Provider A times out once | Retry goes to A and is deduplicated; B receives nothing |
| IT18 | Lane fairness | 10,000 rows in `normal-0`, 10 rows in `normal-1` | All `normal-1` rows claimed within the first rotation |
| IT19 | Express isolation | 10,000 normal rows ready, then 1 Express message | Express message claimed by the Express pool before any further normal claim completes |
| IT20 | Concurrent sweepers | Two sweepers over the same expired rows | Each message expired and refunded exactly once |
| IT21 | Customer deletion in flight | Delete a customer whose rows are leased | Completion affects zero rows; no errors surface; DLRs counted `unknown` |
| IT22 | Invariant checker detects faults | Corrupt a balance, delete a debit, add a second refund row via a bypass | Each targeted check reports exactly the corrupted IDs |

IT22 tests the checker itself, so a passing checker is meaningful.

## Load scenarios

Run with `make loadtest SCENARIO=<name>`. Each scenario creates its own customers through the admin API, using the balances and rate limits of the demo customers whose names appear below, and ends by calling `GET /admin/api/invariants`.

| Scenario | Load | Passes when |
|---|---|---|
| `balance-race` | One customer with 100 credits; 1,000 concurrent single-segment sends over HTTP | 100 × `202`, 900 × `402`; balance 0; invariants pass |
| `steady` | 500 messages/s, 90% normal, 10% Express, 5 minutes | No `5xx`; queue depth stable; all messages leave `accepted` within 10 s |
| `burst` | Ramp from 100 to 5,000 messages/s in 5 s, hold 30 s, drop to 100 | Only `202` and `429` responses; backlog drains within 2 minutes after the burst |
| `noisy-neighbor` | `bulkco` at its full limit; `acme` at 10 messages/s | `acme` accept-to-sent p95 < 2 s during the flood |
| `express-under-load` | Normal lanes saturated by `bulkco`; `quickpay` sends 50 Express messages/s | Express accept-to-sent p99 < 2 s; zero SLA breaches |
| `provider-outage` | Mixed traffic; provider A outage for 60 s, then recovery | Express accept-to-sent p99 < 5 s during the outage; no message `failed` or `expired`; backlog drains after recovery |
| `provider-chaos` | Provider A: failure rate 0.3, timeout rate 0.05; 5 minutes | Every message ends `sent`, `delivered`, `undelivered`, or `failed`; every normal message is accepted by providers at most once (timeouts are resolved by deduplication); invariants pass |

Latency figures are computed from `accepted_at` and `sent_at` of the scenario's messages, read through the admin API after the run.

## Chaos scenarios

Run with `make chaos SCENARIO=<name>`. Each runs the `steady` load while injecting the failure, then waits for the backlog to drain and runs the invariant checker.

| Scenario | Failure injected | Passes when |
|---|---|---|
| `worker-kill` | `docker compose kill gateway-worker` every 60 s, restarted after 10 s | No message lost; every leased row reclaimed; invariants pass |
| `api-kill` | Kill and restart `gateway-api` during load | Requests either completed or failed at the client; retries with `client_ref` produce no duplicates |
| `provider-restart` | Restart provider A during load | Messages whose DLRs were lost remain `sent`; invariants pass |
| `db-restart` | Restart PostgreSQL during load | API returns `503` during the restart; after recovery all accepted messages are dispatched; invariants pass |

## Related

- [Test strategy](010-strategy.md)
- [Demo walkthrough](../010-overview/030-demo-walkthrough.md)
- [Benchmarks](../080-scalability/020-benchmarks.md)
