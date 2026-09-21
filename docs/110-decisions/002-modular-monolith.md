# 002. Modular Monolith with Process Roles

Status: Accepted

## Context

The system has distinct responsibilities (customer API, dispatch, delivery reports, administration) with different scaling needs: API capacity follows request rate, dispatch capacity follows provider latency and throughput. Separate microservices would give independent scaling but add network contracts, versioning, and deployment coordination between parts that share one database and one domain model.

## Decision

One Go module and one `gateway` binary, organized into packages with enforced dependency rules ([Code organization](../020-architecture/040-code-organization.md)). The binary runs in a role chosen at startup: `api`, `worker`, or `all`. Roles communicate only through PostgreSQL. The fake provider is a separate binary because it represents an external system.

## Alternatives considered

**Microservices** (accept service, dispatch service, DLR service, billing service). Rejected: billing and acceptance must share one transaction, so a billing service would either be called synchronously inside the hot path or reintroduce distributed consistency problems.

**A single process with no roles.** Rejected: it cannot scale API and dispatch independently and cannot demonstrate recovery from a worker crash while the API keeps serving.

## Consequences

Positive:

- API and workers scale independently and fail independently.
- One build, one image, one set of configuration variables.
- Domain code is shared directly, not through serialized contracts.
- Package boundaries keep the option of extracting a service later.

Negative:

- Package boundaries are enforced by review and tests rather than by the network; a careless import could couple modules.
- All roles deploy together; a change to dispatch code redeploys the API image.

## Related

- [Components](../020-architecture/020-components.md)
- [Deployment](../090-operations/030-deployment.md)
