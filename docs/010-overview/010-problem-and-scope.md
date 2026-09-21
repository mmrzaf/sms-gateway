# Problem and Scope

What the SMS Gateway must do, which qualities matter most, and what is deliberately outside the system's boundary.

## Problem

Businesses send SMS messages (one-time codes, notifications, marketing) through an SMS gateway rather than contracting operators directly. The gateway serves tens of thousands of customers with very uneven traffic: most send little, a few send enormous volumes, and traffic arrives in bursts. The gateway must charge accurately, never let one customer degrade service for others, survive provider failures, and offer a premium class of messages with stronger latency and delivery expectations.

The system is designed for **100 million messages per day** with a peak rate of **10,000 messages per second**, and is deployed at minimum scale (a single node). The capacity reasoning is in [Capacity analysis](../080-scalability/010-capacity-analysis.md).

## Functional requirements

| # | Requirement | Where |
|---|---|---|
| F1 | Customers send single messages and batches of messages through a REST API | [Customer API](../050-api/020-customer-api.md) |
| F2 | Each message is charged in credits at acceptance; a customer can never spend more than their balance | [Credits and billing](../030-domain/020-credits-and-billing.md) |
| F3 | Customers check their balance and add credits | [Customer API](../050-api/020-customer-api.md) |
| F4 | Customers query individual messages, list messages, and read summary reports | [Customer API](../050-api/020-customer-api.md) |
| F5 | Express messages get a separate, prioritized path with latency tracking and provider failover | [Express messages](../030-domain/040-express-messages.md) |
| F6 | Messages are dispatched to SMS providers with retries, and delivery reports update message status | [Queue and workers](../060-processing/010-queue-and-workers.md), [Delivery reports](../060-processing/040-delivery-reports.md) |
| F7 | An internal dashboard manages demo customers and shows system, worker, queue, and provider activity | [Dashboard](../090-operations/050-dashboard.md) |
| F8 | A simulated SMS provider with controllable latency, failures, and outages stands in for real operators | [Fake provider](../060-processing/050-fake-provider.md) |

## Quality goals

Ranked; when two goals conflict, the higher one wins.

| Rank | Goal | Meaning |
|---|---|---|
| 1 | Correctness | Credits are never lost, created, or overspent. An accepted message is never lost. Every credit movement is recorded. |
| 2 | Isolation | A high-volume customer or a burst cannot materially delay other customers. Normal traffic cannot delay Express traffic. |
| 3 | Reliability | Provider failures, worker crashes, and duplicate requests are handled without manual intervention. |
| 4 | Scalability | The design reaches 10,000 messages/s by adding instances and database capacity, without changing its structure. |
| 5 | Simplicity | The fewest moving parts that satisfy the goals above. Every component has to justify its existence. |

## In scope

- REST API for customers, authenticated with API keys.
- Credit accounting with a transaction history.
- Message acceptance, pricing, queueing, dispatch, retry, failover, expiry, and refunds.
- Delivery report (DLR) ingestion from providers.
- Express service class with reserved capacity and SLA tracking.
- Per-customer rate limiting and fair dispatch across customers.
- A simulated SMS provider service.
- An internal dashboard for demo customers and system visibility.
- Load and scenario test tooling.

## Out of scope

| Item | Reason |
|---|---|
| Customer-facing web application | Customers integrate through the API |
| User registration, login, and account management | Customers are created by operators; API keys are the only customer credential |
| Real SMS operator integration | The fake provider implements the same contract a real operator adapter would |
| Payments and currencies | Credits are added through a simulated charge operation |
| Inbound SMS, scheduling, templates, contact lists | Not required by the problem |
| Multi-region deployment | Discussed in [Scaling path](../080-scalability/030-scaling-path.md), not built |

## Assumptions

- Recipients are phone numbers in E.164 format (for example `+989121234567`).
- Message bodies are text; encoding (GSM-7 or UCS-2) is detected automatically.
- Providers identify messages by the gateway's message ID and deduplicate on it.
- Providers report delivery asynchronously through an HTTP callback.
- The gateway, its workers, and the providers share a private network; only the customer API is exposed publicly.

## Related

- [Glossary](020-glossary.md)
- [System context](../020-architecture/010-system-context.md)
- [Decisions](../110-decisions/README.md)
