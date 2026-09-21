# SMS Gateway Documentation

The SMS Gateway is a multi-tenant backend that accepts SMS messages from customers over a REST API, charges them in credits, and dispatches the messages to SMS providers with retries, failover, and delivery tracking. It offers two service classes: normal messages, and Express messages with a bounded-latency dispatch path.

This folder describes the system as built: what it does, how it is structured, why it is structured that way, and how to run, observe, and test it.

## Reading paths

| Reader | Time | Read |
|---|---|---|
| Reviewer, first pass | 5 min | [Problem and scope](010-overview/010-problem-and-scope.md), [Components](020-architecture/020-components.md), [Decisions](110-decisions/README.md) |
| Reviewer, full pass | 30–45 min | Sections 010 through 070 in order, then [Capacity analysis](080-scalability/010-capacity-analysis.md) |
| Running the system | 10 min | [Running locally](090-operations/010-running-locally.md), [Demo walkthrough](010-overview/030-demo-walkthrough.md) |
| Integrating as a customer | 10 min | [API conventions](050-api/010-conventions.md), [Customer API](050-api/020-customer-api.md) |
| Evaluating scale | 15 min | [Section 080](080-scalability/010-capacity-analysis.md) and [Decision 001](110-decisions/001-postgres-as-queue.md) |

## Sections

| Section | Contents |
|---|---|
| [010 Overview](010-overview/) | Problem statement, scope, glossary, guided demo |
| [020 Architecture](020-architecture/) | System context, components, end-to-end request flow, code organization |
| [030 Domain](030-domain/) | Message lifecycle, credits and billing, segments and pricing, Express messages |
| [040 Data](040-data/) | Database schema and idempotency rules |
| [050 API](050-api/) | API conventions, customer API, admin API, internal and provider APIs |
| [060 Processing](060-processing/) | Queue and workers, lanes and fairness, retries and failover, delivery reports, fake provider |
| [070 Reliability](070-reliability/) | Failure modes and recovery, system invariants |
| [080 Scalability](080-scalability/) | Capacity analysis for 100M messages/day, benchmarks, scaling path |
| [090 Operations](090-operations/) | Running locally, configuration, deployment, observability, dashboard |
| [100 Testing](100-testing/) | Test strategy and test scenarios |
| [110 Decisions](110-decisions/) | Architecture decision records |

## Conventions

- **Credits** are the only unit of value. A credit is an integer; there are no currencies and no fractional amounts.
- **Message** means one customer SMS request. A message is split into one or more **segments** for transmission and is priced per segment.
- **Times** are UTC and formatted as RFC 3339 in the API. All persisted timestamps come from the database clock (`now()`).
- **Identifiers** are UUIDv7 unless stated otherwise.
- **Diagrams** are Mermaid blocks embedded in the documents.
- Terms with a specific meaning are defined in the [Glossary](010-overview/020-glossary.md).
