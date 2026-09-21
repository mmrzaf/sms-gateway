# Idempotency

How customers can safely retry requests without sending a message twice or adding credits twice.

## Why it matters

A customer whose request times out cannot know whether the gateway accepted it. Without idempotency, retrying risks a duplicate SMS and a double charge; not retrying risks losing the message. `client_ref` makes retrying always safe.

## The `client_ref`

- Supplied by the customer, unique per customer.
- 1–64 characters from `A–Z a–z 0–9 . _ : -`.
- Optional for messages, required for charges.
- Stored permanently with the message or transaction, so a replay is recognized at any later time.

A replay is compared with the original on the fields that define the request:

| Operation | Compared fields |
|---|---|
| Message | `to`, `text`, `type` |
| Charge | `amount` |

## Single message

| Situation | Response |
|---|---|
| New `client_ref` | `202`, new message |
| Existing `client_ref`, same payload | `202`, the original message, header `Idempotent-Replayed: true`; no new debit |
| Existing `client_ref`, different payload | `409 idempotency_conflict` |
| No `client_ref` | `202`, new message; the request is not idempotent |

**Implementation.** The accept transaction's first statement inserts the message with `ON CONFLICT (customer_id, client_ref) DO NOTHING`. If no row is inserted, the transaction rolls back before touching the balance, the existing message is loaded, and the payload is compared.

**Concurrent duplicates.** When two requests with the same `client_ref` run at the same time, the second insert waits on the unique index until the first transaction finishes. If the first commits, the second sees the conflict and becomes a replay. If the first rolls back (for example, insufficient credits), the second proceeds as a new request. Exactly one message and one debit can result.

## Batch

A batch is accepted or rejected as a whole. Idempotency applies per item and resolves to one outcome for the batch:

| Situation | Response |
|---|---|
| No item's `client_ref` exists | `202`, all messages accepted |
| Every item has a `client_ref`, all exist, all payloads match | `202`, the original messages in request order, `Idempotent-Replayed: true` |
| Any other combination of existing `client_ref`s | `409 idempotency_conflict`, with the indexes of the conflicting items |
| The same `client_ref` appears twice in one batch | `422`, field `messages[i].client_ref`, code `duplicate` |

A customer that wants safe batch retries sets a `client_ref` on every item.

## Charges

| Situation | Response |
|---|---|
| New `client_ref` | `201`, credits added |
| Existing `client_ref`, same `amount` | `201`, the original transaction and the current balance, `Idempotent-Replayed: true` |
| Existing `client_ref`, different `amount` | `409 idempotency_conflict` |

The unique index `transactions_charge_ref_uidx` enforces this with the same insert-first pattern as messages.

## What idempotency does not cover

- Requests without a `client_ref`.
- Duplicate transmission to the handset caused by Express failover after a provider timeout; see [Express messages](../030-domain/040-express-messages.md).

## Related

- [Customer API](../050-api/020-customer-api.md)
- [Schema](010-schema.md)
