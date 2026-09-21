# API Conventions

Rules shared by every endpoint of the customer API: authentication, formats, errors, pagination, and rate limiting.

## Base URL and versioning

The customer API is served on the public port under `/v1`. A breaking change would introduce `/v2`; additive changes (new fields, new endpoints, new optional parameters) are made within `v1`. Clients must ignore response fields they do not recognize.

The OpenAPI 3.1 specification is served at `/openapi.yaml` and rendered with Swagger UI at `/docs`. The source is `api/openapi.yaml`.

## Authentication

Every `/v1` request carries the customer's API key:

```
Authorization: Bearer sk_3Qm8x...
```

- Keys are `sk_` followed by 43 base62 characters (256 bits of randomness).
- The gateway stores only the SHA-256 hash. A fast hash is appropriate because the key is random and high-entropy; slow password hashes protect low-entropy secrets.
- A missing, malformed, or unknown key returns `401 unauthorized`.
- Validated keys are cached in memory for 30 seconds. A deleted customer or a rotated key stops working within that time.

## Requests

- Bodies are JSON, UTF-8, with `Content-Type: application/json`. Other content types return `415 unsupported_media_type`.
- Maximum body size is 1 MiB. Larger bodies return `413 payload_too_large`.
- Unknown fields are rejected with `400 invalid_json`, so typos in field names surface immediately.
- Each request may carry `X-Request-Id`; if absent, the gateway generates one. The value is echoed in the response and included in every log line for the request.

## Responses

- Bodies are JSON.
- Timestamps are RFC 3339 in UTC with millisecond precision, for example `2026-09-21T10:15:30.123Z`.
- Identifiers are UUIDv7 strings.
- Credit amounts are JSON integers.
- Absent optional values are `null`, not omitted.

## Errors

Every error uses one envelope:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "The request contains invalid fields.",
    "details": [
      { "field": "to", "code": "invalid_format", "message": "must be an E.164 phone number" }
    ],
    "request_id": "01923f6e-..."
  }
}
```

`code` is stable and meant for programs; `message` is for humans and may change. `details` is present for validation and conflict errors, otherwise `null`.

| HTTP | `code` | When |
|---|---|---|
| 400 | `invalid_json` | Body is not valid JSON or contains unknown fields |
| 401 | `unauthorized` | Missing or invalid API key |
| 402 | `insufficient_credits` | Balance is lower than the cost |
| 404 | `not_found` | Resource does not exist or belongs to another customer |
| 409 | `idempotency_conflict` | `client_ref` reused with a different payload |
| 413 | `payload_too_large` | Body exceeds 1 MiB |
| 415 | `unsupported_media_type` | Body is not JSON |
| 422 | `validation_failed` | One or more fields are invalid; see `details` |
| 429 | `rate_limited` | Rate limit exceeded; see `Retry-After` |
| 500 | `internal_error` | Unexpected failure; safe to retry with the same `client_ref` |
| 503 | `unavailable` | The database is unreachable; safe to retry with the same `client_ref` |

Field error codes in `details`: `required`, `invalid_format`, `out_of_range`, `too_many_items`, `duplicate`, `text_too_long`, `invalid_text`, and `conflict` (for `409 idempotency_conflict`, naming each conflicting `client_ref`).

## Pagination

List endpoints use keyset pagination:

- `limit`: 1–200, default 50.
- `cursor`: opaque string from the previous response's `next_cursor`.
- Results are ordered newest first.
- `next_cursor` is `null` on the last page.

```json
{ "data": [ ... ], "next_cursor": "MDE5MjNmNmUtN2E..." }
```

Keyset pagination costs the same on page 1,000 as on page 1 and does not skip or repeat items when new rows are inserted during paging. Offsets are not supported.

## Rate limiting

Each customer has a limit in messages per second (`rate_limit_rps`), enforced as a token bucket:

- One token per message; a batch of `n` messages takes `n` tokens.
- Bucket capacity is `max(2 × rate_limit_rps, 500)`, so a full bucket always admits a maximum-size batch.
- Only message submission endpoints consume tokens. Reads, balance, and charges are not limited per customer.
- When the bucket lacks tokens, the response is `429 rate_limited` with `Retry-After` in whole seconds.

Every message submission response includes:

```
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 87
```

With several API instances, each instance enforces `rate_limit_rps / API_INSTANCES`; see [Decision 007](../110-decisions/007-in-process-rate-limiting.md).

## Health endpoints

| Endpoint | Meaning |
|---|---|
| `GET /healthz` | The process is running. Always `200`. |
| `GET /readyz` | The process can serve traffic: database reachable within 1 second. `200` or `503`. |

## Related

- [Customer API](020-customer-api.md)
- [Idempotency](../040-data/020-idempotency.md)
