# Forge Interview Guide

Use these answers as a map, not a script. Tie every claim to code, a test, or an explicit limitation.

## Give me a 60-second overview of Forge

Forge is a self-hosted application delivery control plane. A Go API stores projects and services and atomically queues immutable deployment records in PostgreSQL. Separate concurrent Go workers lease those jobs, shallow-clone a public GitHub ref, build and push a Docker image with Buildx, then reconcile a Secret, Deployment, Service, and Ingress through Kubernetes' Go client. A deployment becomes `LIVE` only after the Kubernetes rollout is available. The Next.js dashboard exposes state, events, bounded logs, encrypted environment configuration, health, and artifact-based rollback. The design emphasizes real failure semantics: a validated state machine, transaction boundaries, at-least-once leased jobs, heartbeats, retries, advisory locks, stable resource names, timeouts, request IDs, Prometheus metrics, and kind-based E2E validation.

## Walk me through what happens after someone clicks Deploy

The browser sends `POST /api/services/{id}/deployments`. The API validates ownership/ref and commits three records in one PostgreSQL transaction: `QUEUED` deployment, initial event, and unique job. It returns `202`; no build runs in the request.

A worker claims an eligible row with `SKIP LOCKED`, starts a heartbeat, and takes a service-scoped advisory lock. Per-service FIFO eligibility prevents distinct deployments from racing over the same Kubernetes objects while other services remain parallel. The engine advances only through legal states. It clones into a random directory, resolves and persists the SHA, validates the context, builds an image tagged by deployment UUID, pushes it, and reconciles Kubernetes resources with stable names. It then waits for the observed generation and all desired replicas to be available, captures a redacted initial runtime-log snapshot, marks `LIVE`, and completes the job. Each transition and its event are one transaction. Every source/build/Kubernetes operation is bounded, and checkout cleanup is deferred on every exit path.

## Why did you use a queue?

Deployments take seconds or minutes, cross unreliable systems, and must outlive an HTTP client/API process. PostgreSQL jobs let the API durably accept intent and respond immediately. Using the same database makes deployment+event+job creation atomic—there is no dual-write gap with Redis at this scale. The lease interface is narrow enough to move later if queue volume justifies a broker.

## What happens if a worker crashes?

Its process stops extending the job lease. After expiry, another uniquely identified worker slot can claim the row and resume the same logical attempt, even at the attempt limit. It re-reads durable deployment state rather than restarting blindly. If the commit was already recorded, source recovery checks out that immutable SHA; if an image tag was recorded, the build is not repeated. If Kubernetes resources were partially created, reconciliation updates the same stable service-ID-derived names. The advisory lock protects against the old worker still executing during a partition. A worker that loses a heartbeat cancels its work and does not write retry/failure state because it can no longer prove lease ownership. The database integration suite proves final-attempt lease reclaim and safe lock-connection cleanup; coordinator tests cover heartbeat loss, shutdown abandonment, retries, and final failure.

## Why is this at-least-once rather than exactly-once?

No transaction spans PostgreSQL, Docker, a registry, and Kubernetes. A crash can occur after an external side effect but before the database records it. Claiming exactly once would be misleading. Forge instead makes database transitions conditional and external resource names/tags deterministic, so safe replay converges. Immutable deployment UUID image tags also prevent two attempts from overwriting a branch tag.

## How do you avoid duplicate deployments?

Each manually requested deployment is intentionally a new history record. A deployment has one job through a unique foreign key. `SKIP LOCKED` prevents duplicate claims, lease-owner checks fence stale writes, and FIFO eligibility plus a service advisory lock prevent separate deployments from racing the same workload. GitHub retries are a separate concern: the webhook delivery ID is inserted in the same transaction as generated deployments; an `ON CONFLICT` replay creates nothing.

## Why Kubernetes?

Forge's purpose is to demonstrate application delivery, not build a scheduler. Kubernetes provides rolling replacement, probes, service discovery, restart semantics, resource constraints, and a declarative API. Forge adds the control-plane concerns Kubernetes does not: repository intent, builds, artifact history, queued orchestration, events, routes, and rollback. For a tiny single-host product, Docker Compose alone would be simpler; Kubernetes is justified here because workload reconciliation is core to the project.

## How does rollback work?

Only a successful deployment with a non-null image can be selected. Forge creates a new deployment/job, sets `rollback_of`, and copies the exact image tag and commit. The worker skips clone/build/push but runs normal Kubernetes reconciliation and readiness verification. Nothing rewrites old history, and the E2E test verified the rollback ID differs from the source and reaches `LIVE`.

## Where are the consistency boundaries?

Owner-scoped CRUD is strongly consistent after commit. Deployment+initial event+job and status+event are atomic database boundaries. Docker/registry/Kubernetes are eventually consistent with database intent and reconciled idempotently. Job leases are time-based ownership; advisory locks are session-based execution guards. Health is explicitly periodic/stale. Runtime routes can take a short interval to converge after pod readiness, which the real-cluster test polls with a deadline.

## How do environment secrets work?

The API validates the key/value, encrypts the value with AES-256-GCM using a random nonce and service/key associated data, and returns metadata only. The worker decrypts values just before reconciliation and writes a Kubernetes Secret. Build steps never receive them, and captured runtime lines redact exact configured values. The master key is still a process environment secret; production would use KMS envelope encryption and short-lived workload delivery.

## How does zero-downtime deployment work?

The Deployment uses rolling update with `maxUnavailable: 0` and `maxSurge: 1`. Kubernetes sends traffic only to ready pods, while Forge waits for all requested replicas to become available. This avoids deliberate downtime for a compatible app with enough cluster capacity. It is not a proof of zero downtime for one-replica apps whose image cannot start, long-lived connections, incompatible schema changes, or an exhausted cluster.

## What would break first at 10x scale?

Build CPU, disk, registry bandwidth, and the single Docker daemon would saturate before the Go API. PostgreSQL log volume and job polling would follow. I would separate build and deploy worker pools, put logs in object storage, use an authenticated registry and remote BuildKit cache, add quotas/rate limits, and measure queue age. API replicas are straightforward because requests are stateless apart from PostgreSQL.

## What changes at 10,000 deployments/day?

Use horizontally scaled API/worker processes, dedicated sandboxed remote builders, externally managed PostgreSQL with replicas/backups, object log storage, registry retention/scanning, queue partitioning or a broker if polling/row churn becomes material, per-tenant concurrency quotas, and multiple workload clusters. Keep PostgreSQL authoritative and use an outbox/event stream for derived consumers.

## What changes at one million deployments/day?

Use regional control planes with explicit tenant/home-region ownership, a distributed scheduler over an event stream, separately autoscaled build and deploy fleets, remote content-addressed build cache, multi-cluster placement/capacity services, sharded metadata, global artifact replication, hierarchical rate limiting, and a scalable telemetry pipeline. Cross-region operations need idempotency keys and sagas; a global serializable transaction is not plausible. Forge intentionally does not simulate this complexity locally.

## What security concerns exist when building untrusted repositories?

Dockerfile steps can exploit the builder, consume resources, reach internal networks, poison caches, or exfiltrate any mounted credential. Forge currently restricts Git URLs/refs/paths, caps contexts/logs/time/resources, and keeps Docker/Kubernetes credentials out of the API, but the local worker still mounts the Docker socket. Production needs ephemeral rootless builders or VMs, no control-plane credentials, egress allowlists, quotas, scanning/signing/attestation, cache isolation, and tenant-separated runtime clusters.

## Why Go?

The system coordinates subprocess streams, timeouts, worker slots, heartbeats, HTTP, PostgreSQL, and Kubernetes. Go gives inexpensive concurrency and cancellation, strong standard HTTP/process primitives, official Kubernetes support, and small static service binaries. A modular monolith shares state-machine and validation rules without network boundaries that would add failure modes but no product value.

## Why PostgreSQL rather than Redis?

Forge needs relational ownership, constraints, immutable history, ordered events, and atomic deployment enqueue. PostgreSQL can also implement the modest local queue with `SKIP LOCKED` and leases. Redis would create a dual-write problem or require an outbox while adding another service. At high queue volume I would move delivery behind the same queue interface, not pretend PostgreSQL is universally optimal.

## Why REST rather than GraphQL?

The domain is resource-oriented and has a handful of explicit commands. REST gives inspectable status codes, `Location`, cursor endpoints, simple curl/E2E scripts, and an OpenAPI contract. GraphQL would not simplify long-running state transitions and would make HTTP caching/operational inspection less direct.

## How would you make builds more isolated?

Split orchestration from build execution. The worker would submit a content-addressed build request to a pool of disposable rootless BuildKit pods or short-lived VMs. Builders would receive source through object storage, have strict CPU/memory/disk/PID/time quotas, no Kubernetes/database credentials, restricted outbound DNS/network, per-tenant cache namespaces, and one scoped registry push token. Destroy the sandbox after producing a signed image digest and attestation.

## How would you deploy Forge across multiple regions?

Assign each tenant/project a home region and route writes there. Run regional API and worker pools with regional workload clusters/registries and a highly available regional metadata shard. Replicate immutable artifacts and read models asynchronously. Publish deployment events through a durable stream; use idempotency keys and explicit failover leases when moving ownership. Secrets use region-aware KMS. Avoid active-active writes to one deployment state machine unless there is a clear consensus/ownership protocol.

## Be candid about current limitations

The API has only local-user identity, the Docker builder is not safe for hostile tenants, the Compose worker uses kind-admin credentials, runtime logs are an initial snapshot rather than a durable tail, registry/image retention is manual, custom domains/TLS are absent, and GitHub OAuth/App installation is not implemented. These are documented boundaries, not hidden mocks.
