# 003. Credit Ledger and Refund Rule

Status: Accepted

## Context

Credit correctness is the highest-ranked quality goal. Customers send concurrently, a customer can have far fewer credits than concurrent requests, and messages can fail after acceptance. The accounting must be provably consistent, auditable, and simple enough to verify mechanically.

## Decision

- Credits are integers, with no currency.
- Every credit movement is an append-only row in `transactions` (`charge`, `debit`, `refund`). `customers.balance` is a projection updated in the same database transaction.
- Acceptance debits with a conditional update (`balance >= cost`) as the last statement of the accept transaction. A `CHECK (balance >= 0)` constraint backs it.
- Credits are charged at acceptance. A message is refunded if and only if it ends `failed` or `expired`, which are exactly the outcomes in which no provider accepted it.
- There is no operation that sets a balance; operators add credits through the same `charge` transaction.

## Alternatives considered

**Balance column only, no history.** Rejected: the balance could not be audited or verified.

**Reserve at acceptance, settle on delivery.** Rejected: two-phase accounting doubles the credit writes per message, and settlement would depend on DLRs, which may never arrive.

**Refund on `undelivered` as well.** Rejected: the provider accepted and attempted delivery, so the cost was incurred; refunding would also make revenue depend on DLR reliability.

**Refund Express SLA breaches.** Rejected for simplicity: breaches are recorded and reported. One refund rule is easier to reason about and to verify.

**Serializable isolation or explicit locking reads.** Rejected: the conditional update achieves the same guarantee at `READ COMMITTED` with one statement.

## Consequences

Positive:

- Overspending is impossible, and the argument fits in one paragraph ([Credits and billing](../030-domain/020-credits-and-billing.md#debit-at-acceptance)).
- The invariant `balance = SUM(transactions.amount)` is checkable at any time.
- Schema constraints make double debits and double refunds impossible.

Negative:

- All accepts for one customer serialize on one row, bounding a single customer's single-message rate by commit latency. Batching is the primary mitigation; balance slots are the next step ([Scaling path, stage 4](../080-scalability/030-scaling-path.md#stage-4-hot-customers)).
- A message delivered despite being marked `failed` (all responses lost) is refunded; the customer is not charged for it.
- `transactions` grows by at least one row per message.

## Related

- [Credits and billing](../030-domain/020-credits-and-billing.md)
- [Invariants](../070-reliability/020-invariants.md)
