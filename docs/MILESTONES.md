# Implementation Milestones

Legend: `[x]` implemented and locally validated, `[~]` implemented but requires an unavailable external prerequisite for full validation, `[ ]` not implemented.

## 0 — Repository foundation

- [x] Monorepo, Go module, strict Next.js application, configuration, logging
- [x] Health endpoint, container development environment, Make targets
- [x] Formatting, linting, baseline tests, and GitHub Actions

## 1 — Database and core API

- [x] Versioned PostgreSQL migrations and migration runner
- [x] Users, projects, services, deployments, and deployment events
- [x] Validated CRUD/read APIs, ownership, errors, request IDs

## 2 — Durable deployment worker

- [x] Atomic enqueue, leased claims, heartbeat, retry/backoff, crash recovery
- [x] Separate worker process, concurrency limit, graceful shutdown

## 3 — Git source stage

- [x] Public HTTPS GitHub repository validation, clone, ref checkout, SHA capture
- [x] Isolated temporary directories and cleanup; Dockerfile/context validation

## 4 — Docker build stage

- [x] Immutable deployment image tags, streamed/persisted build output
- [x] Buildx cache, registry push, and documented daemon threat boundary

## 5–6 — Kubernetes and routing

- [x] Programmatic Secret, Deployment, Service, and Ingress reconciliation
- [x] Rolling strategy, probes, rollout deadline, diagnostic failures
- [x] Local `*.localhost` route generation and kind workflow (real cluster validated)

## 7 — Web dashboard

- [x] Project list/create/detail, service configuration, deployment history/detail
- [x] Status, timeline, logs, environment configuration, rollback controls
- [x] Responsive loading, empty, and error states

## 8–10 — Secrets, logs, rollback

- [x] Write-only encrypted environment values and workload injection
- [x] Bounded build/deployment logs plus event/log SSE
- [x] Rollback as a new deployment referencing an immutable live artifact (E2E validated)

## 11–13 — GitHub automation and zero downtime

- [x] Signed GitHub push webhook and transactional delivery deduplication
- [x] Optional automatic deploy and rolling-update behavior
- [ ] OAuth/repository selection (optional; requires an external GitHub application and is not implemented)

## 14–17 — Observability, reliability, concurrency, resources

- [x] Prometheus API/worker metrics and structured contextual logs
- [x] Failure-path tests, timeout handling, idempotency, transaction boundaries
- [x] Concurrent cross-service claims with per-service FIFO ordering and advisory-lock race prevention
- [x] Validated CPU/memory/replica controls with safe defaults and upper bounds

## 18–20 — Cache, monitoring, security

- [x] BuildKit layer cache support and measured reproducible benchmark procedure
- [x] Periodic workload health reconciliation and orphan garbage collection
- [x] Threat model, current defenses, limitations, and RBAC guidance

## Completion validation

- [x] `make fmt-check`
- [x] `make lint`
- [x] `make test`
- [x] `make build`
- [x] `make vuln`
- [x] `make integration` (cache disabled; real PostgreSQL)
- [x] `make kubernetes-integration` (real kind cluster)
- [x] `make failure-tests` (invalid build, crash loop, failed readiness)
- [x] `make e2e` (deploy, redeploy, route, rollback, cleanup)
- [x] Repository-wide stale TODO, secret, race, timeout, cleanup, dependency, and documentation audit

The completion matrix above was rerun locally on 2026-08-23. The checklist is updated only after the corresponding behavior exists; environment-specific commands and measured output are documented rather than inferred.
