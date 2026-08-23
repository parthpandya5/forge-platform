# Forge Engineering Guide

This file contains permanent repository instructions for every contributor and coding agent.

## Product intent

Forge is a self-hosted application platform, not a dashboard mock. Prefer a small, credible control plane with explicit failure behavior over broad but simulated functionality. Never present a feature as complete unless its real execution path exists and is tested at the appropriate boundary.

## Architecture boundaries

- `apps/api` is the short-lived HTTP control-plane entrypoint. It validates requests, authorizes access, persists intent, and enqueues work. It must not clone repositories, build images, or wait for Kubernetes rollouts.
- `apps/worker` owns long-running deployment jobs. Jobs must be lease-based, retryable, idempotent where practical, time-bounded, and safe under concurrent workers.
- `internal/domain` contains state and business rules and must not depend on HTTP, PostgreSQL, Docker, or Kubernetes.
- `internal/store` owns SQL and transaction boundaries. Keep SQL explicit and schema-backed.
- `internal/deployments` coordinates deployment stages through narrow source, image, and runtime interfaces.
- `internal/build` and `internal/kubernetes` are infrastructure adapters. Treat repositories and built containers as untrusted input.
- `apps/web` consumes the public REST API; it must not connect directly to PostgreSQL or Kubernetes.
- `apps/web/go.mod` is an intentionally dependency-free module boundary. It prevents root Go tooling from traversing Next.js `[id]` routes and `node_modules`; the frontend itself remains TypeScript/npm-managed.

## Invariants

- Deployment history is immutable. A rollback creates a new deployment referencing the successful artifact it restores.
- Deployment status changes only through the domain state machine and compare-and-set persistence.
- Creating a deployment and its queue job is atomic.
- A job may be delivered more than once. Side effects therefore use stable deployment IDs and Kubernetes resource names.
- Secret values are write-only through the API and must never appear in logs, events, or response payloads.
- Every external operation has a timeout and returns contextual errors.
- Temporary checkout/build directories are always removed.
- Never interpolate user values into shell command strings. Use argument arrays and validate repository URLs, Git refs, resource quantities, ports, names, and paths.

## Development workflow

1. Read `docs/ARCHITECTURE.md` and `docs/MILESTONES.md` before changing a subsystem.
2. Keep changes scoped to a coherent milestone or behavior.
3. Run `make fmt`, `make lint`, `make test`, and `make build` for material changes. Run `make integration` or `make e2e` when their prerequisites are available.
4. Add failure-path tests, not only happy paths.
5. Update API and architecture documentation whenever externally visible behavior or a boundary changes.
6. Mark environment-dependent validation as not run; never infer success.

## Code conventions

- Use idiomatic Go, `context.Context`, wrapped errors, `log/slog`, and constructor injection at infrastructure boundaries.
- Keep handlers thin and use typed request/response structures with the common error envelope.
- Avoid god files, speculative interfaces, package globals, hidden retries, swallowed errors, and unexplained TODOs.
- Use strict TypeScript. Model API responses explicitly and render loading, empty, and failure states.
- Migrations are ordered SQL files and are never edited after release; add a new migration instead.
- Use UTC timestamps in storage and APIs.

## Security posture

Forge's local Docker builder is intentionally a trusted-operator feature, not a safe multi-tenant sandbox. Do not weaken host protections for convenience. Do not mount the Docker socket into the API. Keep the worker's Docker access and Kubernetes RBAC documented in `docs/SECURITY.md`.
