# Credits and Billing

How credits are represented, how they move, and why a customer can never spend more than their balance.

## The credit

A credit is an integer unit of value. There are no currencies, no fractional credits, and no conversion. All amounts are stored as `BIGINT`.

| Service class | Price per segment |
|---|---|
| `normal` | 1 credit |
| `express` | 3 credits |

Prices are configuration (`PRICE_NORMAL`, `PRICE_EXPRESS`). A message's cost is fixed at acceptance and stored in `messages.cost`; a later price change never affects accepted messages.

## Model

The balance is a **projection** of an append-only history.

- `transactions` records every credit movement. Rows are never updated or deleted, except by the cascade when a customer is deleted.
- `customers.balance` holds the current total, updated in the same database transaction as every `transactions` insert.
- The invariant `customers.balance = SUM(transactions.amount)` holds for every customer at every commit. The invariant checker verifies it; see [Invariants](../070-reliability/020-invariants.md).

## Transaction kinds

| Kind | Sign | `message_id` | Created by |
|---|---|---|---|
| `charge` | + | null | `POST /v1/balance/charges`, or the admin credit operation |
| `debit` | − | required | Message acceptance, one per message |
| `refund` | + | required | Transition to `failed` or `expired`, at most one per message |

Database constraints enforce the sign per kind, the presence or absence of `message_id` per kind, one debit and at most one refund per message, and unique `client_ref` per customer for charges. See [Schema](../040-data/010-schema.md).

## Debit at acceptance

The debit is a conditional update inside the accept transaction:

```sql
UPDATE customers
SET balance = balance - $cost, updated_at = now()
WHERE id = $customer AND balance >= $cost
RETURNING balance;
```

**Why this cannot overspend.** PostgreSQL takes a row lock for the update. Concurrent accept transactions for the same customer queue on that lock; each one re-evaluates `balance >= $cost` against the latest committed balance after acquiring it. A transaction whose condition fails updates zero rows and rolls back with `402 insufficient_credits`. The `CHECK (balance >= 0)` constraint is a second, independent guard. No isolation level above the default `READ COMMITTED` is required.

**Why the update is the last statement.** The customer row is the only row that concurrent requests contend on. Executing its update last means the lock is held only for the remainder of the transaction, which is the commit itself.

A **batch** debits the total cost of all its messages with one update and inserts one `debit` transaction per message, so per-message accounting stays exact while the contended row is touched once per request.

## Refunds

**Rule:** a message is refunded if and only if it reaches a terminal status without any provider having accepted it — that is, `failed` or `expired`.

| Outcome | Refunded | Reason |
|---|---|---|
| `delivered` | No | Service delivered |
| `undelivered` | No | A provider accepted the message; the delivery attempt consumed operator capacity |
| `failed` | Yes | No provider accepted it |
| `expired` | Yes | No provider accepted it |
| Express SLA breach, but `sent` or `delivered` | No | The breach is recorded and reported, not compensated |

The refund is applied in the same transaction as the status change:

```sql
-- inside the completer or sweeper transaction
UPDATE messages SET status = 'failed', failure_reason = $reason, completed_at = now(), updated_at = now()
WHERE id = ANY($ids) AND status = 'accepted'
RETURNING id, customer_id, cost;

INSERT INTO transactions (id, customer_id, kind, amount, message_id)
SELECT gen_id, customer_id, 'refund', cost, id FROM <returned rows>
ON CONFLICT DO NOTHING;

-- one update per customer, in ascending customer_id order to prevent deadlocks
UPDATE customers SET balance = balance + $sum, updated_at = now() WHERE id = $customer;
```

Only rows the compare-and-set actually changed are refunded, so a message cannot be refunded twice.

## Charges

A customer adds credits with `POST /v1/balance/charges`. The charge requires a `client_ref`; repeating a charge with the same `client_ref` returns the original transaction without adding credits again. Charges have no payment step; they represent a completed top-up.

The admin credit operation creates the same `charge` transaction with a generated `client_ref` of the form `admin-<uuid>`.

There is no operation that sets a balance directly. Every change to a balance is a transaction, including those made by operators.

## Deleting a customer

Deleting a customer removes the customer, its messages, its transactions, and its queue rows through `ON DELETE CASCADE`. Queue rows currently leased by a worker disappear; the worker's completion affects zero rows and is discarded.

## Contention limit

All accepts for one customer serialize on that customer's row. The sustainable rate for a single customer is therefore bounded by commit latency, not by server size. Batching raises the per-customer message rate by the batch size. The analysis is in [Capacity analysis](../080-scalability/010-capacity-analysis.md).

## Related

- [Decision 003: credits ledger and refund rule](../110-decisions/003-credit-ledger-and-refunds.md)
- [Schema](../040-data/010-schema.md)
- [Invariants](../070-reliability/020-invariants.md)
