# Invariants

Properties that hold at every commit, how each is enforced, and how the invariant checker verifies them.

## Enforced by the schema

These cannot be violated by any code path, because the database rejects the write:

| Invariant | Enforcement |
|---|---|
| Balances are never negative | `CHECK (balance >= 0)` |
| Debits are negative; charges and refunds are positive | `CHECK` on `transactions` |
| A message has at most one debit and at most one refund | Unique index `(message_id, kind)` |
| A charge's `client_ref` is unique per customer | Unique partial index |
| A message's `client_ref` is unique per customer | Unique constraint |
| `failure_reason` is present exactly for `failed` and `expired` | `CHECK` on `messages` |
| A queue row always belongs to an existing message | Foreign key |

## Verified by the checker

These depend on application logic and are verified by `gateway check`, `make check`, and `GET /admin/api/invariants`. Each check returns the number of violations and up to 10 sample IDs, and is reported under the name shown here:

| Check | Name in reports |
|---|---|
| I1 | `balance_matches_transactions` |
| I2 | `one_debit_per_message` |
| I3 | `refund_iff_failed_or_expired` |
| I4 | `accepted_messages_are_queued` |
| I5 | `finished_messages_leave_the_queue` |
| I6 | `no_abandoned_leases` |
| I7 | `timestamps_consistent` |

**I1: balance equals the sum of transactions**

```sql
SELECT c.id
FROM customers c
LEFT JOIN transactions t ON t.customer_id = c.id
GROUP BY c.id, c.balance
HAVING c.balance <> COALESCE(SUM(t.amount), 0);
```

**I2: every message has exactly one debit equal to its cost**

```sql
SELECT m.id
FROM messages m
LEFT JOIN transactions t ON t.message_id = m.id AND t.kind = 'debit'
WHERE t.id IS NULL OR t.amount <> -m.cost;
```

**I3: a refund exists if and only if the message is failed or expired, and equals its cost**

```sql
SELECT m.id
FROM messages m
LEFT JOIN transactions t ON t.message_id = m.id AND t.kind = 'refund'
WHERE (m.status IN ('failed', 'expired')) <> (t.id IS NOT NULL)
   OR (t.id IS NOT NULL AND t.amount <> m.cost);
```

**I4: every accepted message has a queue row**

```sql
SELECT m.id
FROM messages m
LEFT JOIN queue q ON q.message_id = m.id
WHERE m.status = 'accepted' AND q.message_id IS NULL;
```

**I5: no terminal or sent message keeps a queue row beyond one lease plus one sweep**

```sql
SELECT q.message_id
FROM queue q
JOIN messages m ON m.id = q.message_id
WHERE m.status <> 'accepted'
  AND m.updated_at < now() - ($lease_duration + $sweep_interval) * 2;
```

**I6: no lease has been expired for longer than one minute**

```sql
SELECT message_id
FROM queue
WHERE lease_owner IS NOT NULL
  AND next_attempt_at < now() - interval '1 minute';
```

A violation of I6 means expired leases are not being reclaimed, which indicates that no worker is running for that lane.

**I7: status timestamps are consistent**

```sql
SELECT id
FROM messages
WHERE (status IN ('sent') AND sent_at IS NULL)
   OR (status IN ('delivered', 'undelivered', 'failed', 'expired') AND completed_at IS NULL)
   OR (sent_at IS NOT NULL AND sent_at < accepted_at)
   OR (completed_at IS NOT NULL AND completed_at < accepted_at);
```

## Running the checker

| Command | Output |
|---|---|
| `gateway check [--json]` | One line per check (or a JSON report); exit code `0` if all pass, `1` otherwise. Reads `DATABASE_URL`, and `LEASE_DURATION` and `SWEEP_INTERVAL` for the thresholds of I5 |
| `make check` | Runs `gateway check` against the local stack |
| `GET /admin/api/invariants` | JSON result; see [Admin API](../050-api/030-admin-api.md#invariants) |

Every integration test and every load scenario ends with the checker. The checker runs all checks in one `REPEATABLE READ` transaction, so they see a single consistent snapshot even while traffic is flowing. I5 and I6 use time thresholds so that work legitimately in progress is not reported. Every check must pass on a running system, not only on an idle one.

## Related

- [Credits and billing](../030-domain/020-credits-and-billing.md)
- [Failure modes](010-failure-modes.md)
- [Test strategy](../100-testing/010-strategy.md)
