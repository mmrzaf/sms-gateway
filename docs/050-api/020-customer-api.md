# Customer API

Every customer endpoint: request fields, validation, responses, and examples. Conventions shared by all endpoints are in [API conventions](010-conventions.md).

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/messages` | Send one message |
| `POST` | `/v1/messages/batch` | Send up to 500 messages atomically |
| `GET` | `/v1/messages/{id}` | Get one message |
| `GET` | `/v1/messages` | List messages |
| `GET` | `/v1/balance` | Get the current balance |
| `POST` | `/v1/balance/charges` | Add credits |
| `GET` | `/v1/balance/transactions` | List credit transactions |
| `GET` | `/v1/reports/summary` | Aggregate statistics for a time range |

## Message resource

```json
{
  "id": "01923f6e-7a1c-7b3e-9f10-2c4d5e6f7a8b",
  "type": "normal",
  "to": "+989121234567",
  "text": "Your code is 482913",
  "encoding": "gsm7",
  "segments": 1,
  "cost": 1,
  "status": "delivered",
  "attempts": 1,
  "failure_reason": null,
  "sla_breached": false,
  "client_ref": "otp-1",
  "accepted_at": "2026-09-21T10:15:30.123Z",
  "sent_at": "2026-09-21T10:15:30.241Z",
  "completed_at": "2026-09-21T10:15:31.502Z"
}
```

| Field | Type | Notes |
|---|---|---|
| `type` | `normal` \| `express` | |
| `encoding` | `gsm7` \| `ucs2` | See [Segments and pricing](../030-domain/030-segments-and-pricing.md) |
| `status` | `accepted` \| `sent` \| `delivered` \| `undelivered` \| `failed` \| `expired` | See [Message lifecycle](../030-domain/010-message-lifecycle.md) |
| `failure_reason` | `rejected` \| `attempts_exhausted` \| `expired` \| `null` | Set for `failed` and `expired` |
| `sla_breached` | boolean | Always `false` for normal messages |
| `client_ref` | string \| `null` | |

## POST /v1/messages

Send one message.

**Request**

```json
{ "to": "+989121234567", "text": "Your code is 482913", "type": "normal", "client_ref": "otp-1" }
```

| Field | Required | Rules |
|---|---|---|
| `to` | Yes | E.164: `^\+[1-9][0-9]{7,14}$` |
| `text` | Yes | Non-empty valid UTF-8 without NUL; at most `MAX_SEGMENTS` segments |
| `type` | No | `normal` (default) or `express` |
| `client_ref` | No | 1–64 characters `[A-Za-z0-9._:-]` |

**Responses**

| Status | Body | When |
|---|---|---|
| `202` | Message resource | Accepted, or idempotent replay (`Idempotent-Replayed: true`) |
| `402` | Error `insufficient_credits` | Balance lower than cost; nothing is recorded |
| `409` | Error `idempotency_conflict` | `client_ref` used with a different payload |
| `422` | Error `validation_failed` | |
| `429` | Error `rate_limited` | |

`202` means the message is durably stored, paid for, and queued. It does not mean it has been sent.

## POST /v1/messages/batch

Send 1–500 messages in one request. The batch is validated, priced, and accepted as a unit: either every message is accepted and the total cost is debited, or nothing is recorded.

**Request**

```json
{
  "messages": [
    { "to": "+989121234567", "text": "Order 1001 shipped", "client_ref": "ship-1001" },
    { "to": "+989351234567", "text": "Order 1002 shipped", "client_ref": "ship-1002", "type": "express" }
  ]
}
```

Each item follows the rules of `POST /v1/messages`. Items may mix types. Validation errors refer to items by index, for example `messages[1].to`. More than 500 items returns `422` with field `messages`, code `too_many_items`.

**Responses**

| Status | Body | When |
|---|---|---|
| `202` | `{ "messages": [ ... ], "total_cost": 4 }` | All accepted, in request order; or full replay |
| `402` | Error `insufficient_credits` | Balance lower than the total cost |
| `409` | Error `idempotency_conflict`, `details` list conflicting indexes | Partial overlap or payload mismatch |
| `422` | Error `validation_failed` | Any invalid item |
| `429` | Error `rate_limited` | Not enough tokens for all items |

Batch idempotency rules are in [Idempotency](../040-data/020-idempotency.md).

## GET /v1/messages/{id}

Returns the message resource, or `404 not_found` if it does not exist or belongs to another customer.

## GET /v1/messages

List the customer's messages, newest first.

| Query parameter | Rules |
|---|---|
| `status` | One of the status values |
| `type` | `normal` or `express` |
| `recipient` | Exact E.164 number |
| `since` | RFC 3339; accepted at or after |
| `until` | RFC 3339; accepted before |
| `limit` | 1–200, default 50 |
| `cursor` | From `next_cursor` |

**Response** `200`: `{ "data": [ <message>, ... ], "next_cursor": "..." }`

## GET /v1/balance

**Response** `200`:

```json
{ "balance": 9412, "updated_at": "2026-09-21T10:15:30.123Z" }
```

## POST /v1/balance/charges

Add credits. The operation represents a completed top-up; there is no payment step.

**Request**

```json
{ "amount": 5000, "client_ref": "topup-2026-09-21-01" }
```

| Field | Required | Rules |
|---|---|---|
| `amount` | Yes | Integer, 1 – 1,000,000,000 |
| `client_ref` | Yes | 1–64 characters `[A-Za-z0-9._:-]` |

**Responses**

| Status | Body | When |
|---|---|---|
| `201` | `{ "transaction": <transaction>, "balance": 14412 }` | Added, or replay (`Idempotent-Replayed: true`, `balance` is the current balance) |
| `409` | Error `idempotency_conflict` | Same `client_ref` with a different `amount` |
| `422` | Error `validation_failed` | |

## GET /v1/balance/transactions

List credit transactions, newest first.

| Query parameter | Rules |
|---|---|
| `kind` | `charge`, `debit`, or `refund` |
| `since`, `until` | RFC 3339 |
| `limit`, `cursor` | As in [Pagination](010-conventions.md#pagination) |

**Transaction resource**

```json
{
  "id": "01923f6e-8b2d-7c4f-a021-3d5e6f7a8b9c",
  "kind": "debit",
  "amount": -1,
  "message_id": "01923f6e-7a1c-7b3e-9f10-2c4d5e6f7a8b",
  "client_ref": null,
  "created_at": "2026-09-21T10:15:30.123Z"
}
```

## GET /v1/reports/summary

Aggregate statistics over the customer's messages accepted in a time range.

| Query parameter | Rules |
|---|---|
| `since` | RFC 3339; default 24 hours before `until` |
| `until` | RFC 3339; default now |

The range may not exceed 31 days (`422`, field `since`, code `out_of_range`).

**Response** `200`:

```json
{
  "since": "2026-09-20T10:15:30.000Z",
  "until": "2026-09-21T10:15:30.000Z",
  "totals": {
    "messages": 12840,
    "segments": 13902,
    "credits_spent": 15210,
    "credits_refunded": 12
  },
  "by_status": {
    "accepted": 40, "sent": 310, "delivered": 12011,
    "undelivered": 467, "failed": 9, "expired": 3
  },
  "by_type": {
    "normal":  { "messages": 12186, "credits": 13248 },
    "express": { "messages": 654,   "credits": 1962, "sla_breached": 2 }
  }
}
```

`credits_spent` is the sum of debits for messages in the range; `credits_refunded` is the sum of their refunds. The report reflects current statuses: messages still in flight appear as `accepted` or `sent`.

## Related

- [API conventions](010-conventions.md)
- [Idempotency](../040-data/020-idempotency.md)
- [Message lifecycle](../030-domain/010-message-lifecycle.md)
