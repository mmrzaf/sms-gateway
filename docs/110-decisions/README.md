# Decisions

Architecture decision records (ADRs) for the SMS Gateway. Each record states the context, the decision, the alternatives that were considered, and the consequences, including the negative ones. Records are numbered in the order they were made and are not renumbered.

| # | Decision | Status |
|---|---|---|
| [001](001-postgres-as-queue.md) | PostgreSQL is both the source of truth and the dispatch queue | Accepted |
| [002](002-modular-monolith.md) | One codebase and binary, deployed as separate process roles | Accepted |
| [003](003-credit-ledger-and-refunds.md) | Credits as a ledger with a balance projection; refund only when no provider accepted | Accepted |
| [004](004-express-as-service-class.md) | Express is a service class with its own policies, not a separate pipeline | Accepted |
| [005](005-hashed-lanes-for-fairness.md) | Fair dispatch through hashed lanes and round-robin claiming | Accepted |
| [006](006-at-least-once-dispatch.md) | At-least-once dispatch to providers, deduplicated by message ID | Accepted |
| [007](007-in-process-rate-limiting.md) | Per-customer rate limiting in process memory | Accepted |
| [008](008-dlr-group-commit.md) | Delivery reports committed in batches, acknowledged after commit | Accepted |
| [009](009-uuidv7-identifiers.md) | UUIDv7 identifiers and keyset pagination | Accepted |
| [010](010-fake-provider-as-service.md) | The fake provider is a separate service behind the real provider contract | Accepted |

## Template

```markdown
# NNN. Title

Status: Proposed | Accepted | Superseded by NNN

## Context
## Decision
## Alternatives considered
## Consequences
## Related
```
