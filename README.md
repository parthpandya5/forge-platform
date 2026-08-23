# Forge

Forge is a self-hosted application delivery control plane. Give it a public GitHub repository with a root Dockerfile; Forge asynchronously builds an immutable image, pushes it to a registry, reconciles a Kubernetes workload, waits for readiness, and assigns a local Ingress route.

This repository is intentionally backend/infrastructure-heavy. The dashboard is a client of real APIs, not a mock: deployment states, jobs, logs, health, secrets, routes, and rollback are persisted or reconciled by the control plane.

## Demo

The validated E2E flow performs:

~~~text
create project/service
  → QUEUED → CLONING → BUILDING → PUSHING → DEPLOYING → VERIFYING → LIVE
  → verify http://<project>.localhost
  → build and deploy again
  → create a new rollback deployment from the first immutable image
  → VERIFYING → LIVE
  → delete project → garbage-collect Kubernetes resources
~~~

On the measured local run, the first deployment reached LIVE in 32.127 s, a redeploy in 18.948 s, and artifact rollback in 8.386 s. These are single-machine samples, not SLOs; see [benchmarks](docs/BENCHMARKS.md).

To capture a dashboard demo, start the full stack, run make seed, open http://localhost:3000, deploy **Forge Demo**, then record the project and deployment-detail pages while the timeline/log panel updates. Screenshots are not checked in because no browser capture was available during repository validation.

## What Forge does

- models users, projects, web services, deployments, events, logs, environment metadata, webhook deliveries, and durable jobs in PostgreSQL migrations;
- returns 202 Accepted only after deployment + initial event + queue job commit atomically;
- runs separate concurrent workers with leases, heartbeats, crash recovery, capped retry/backoff, graceful shutdown, per-service FIFO ordering, and advisory locks;
- restricts source to public credential-free GitHub HTTPS URLs, shallow-fetches a safe ref, resolves an immutable SHA, and validates a bounded build context;
- builds through Docker Buildx, persists bounded source/build output, uses deployment-UUID image tags, and pushes to a local or external registry;
- reconciles Kubernetes Secrets, Deployments, Services, and Ingresses through client-go;
- configures HTTP probes, rolling updates, resource bounds, a numeric non-root identity, dropped capabilities, seccomp, and disabled workload service-account tokens;
- waits for observed rollout availability and reports pull/crash/probe diagnostics on timeout;
- exposes project/deployment history, encrypted write-only environment values, resource controls, workload health, polling, bounded SSE, build events/logs, and initial redacted runtime logs;
- rolls back by creating a new deployment that references an old successful artifact;
- verifies GitHub webhook HMACs and transactionally deduplicates delivery IDs; and
- exposes structured logs, request IDs, API/worker Prometheus metrics, readiness, and periodic health/resource cleanup.

## Architecture

~~~mermaid
flowchart LR
    Browser[Next.js dashboard] -->|REST / SSE| API[Go API]
    API -->|owner-scoped transactions| DB[(PostgreSQL)]
    Worker[Go workers] -->|lease / heartbeat / events| DB
    Worker -->|shallow HTTPS fetch| Git[GitHub]
    Worker -->|Buildx over Docker API| Docker[Docker builder]
    Docker --> Registry[OCI registry]
    Worker -->|client-go reconcile| Kubernetes[Kubernetes API]
    Kubernetes --> Deployment[Application pods]
    Ingress[ingress-nginx] --> Deployment
    Browser --> Ingress
~~~

Forge is a modular monolith plus workers. The API never receives Docker or Kubernetes credentials. PostgreSQL is both authoritative relational storage and the initial job queue, which makes accepting deployment intent atomic without a Redis dual write. Workload pods are the data plane and do not depend on Forge for request serving.

See [architecture](docs/ARCHITECTURE.md) for the sequence/ER diagrams, consistency boundaries, failure semantics, tradeoffs, and scale evolution.

## Deployment lifecycle

~~~mermaid
sequenceDiagram
    actor Developer
    participant API
    participant DB as PostgreSQL
    participant Worker
    participant Buildx
    participant K8s as Kubernetes

    Developer->>API: POST /api/services/{id}/deployments
    API->>DB: deployment + event + job transaction
    API-->>Developer: 202 QUEUED + Location
    Worker->>DB: SKIP LOCKED lease + advisory lock
    Worker->>Worker: clone, resolve SHA, validate context
    Worker->>Buildx: build immutable UUID tag and push
    Worker->>K8s: reconcile Secret/Deployment/Service/Ingress
    Worker->>K8s: wait for observed readiness
    Worker->>DB: runtime snapshot + LIVE event + complete job
    Developer->>API: poll or stream events/logs
~~~

The delivery contract is at least once. Exactly once is not claimed across PostgreSQL, Docker, a registry, and Kubernetes; stable identifiers and state-aware reconciliation make replay converge safely.

## Technical highlights

- **Go control plane:** idiomatic net/http and chi, slog, pgx, official Kubernetes client, explicit configuration/timeouts, and small process entrypoints.
- **Deliberate state machine:** valid edges are code and unit tests; status + event is transactional and terminal history is immutable.
- **Durable concurrency:** PostgreSQL leases with FOR UPDATE SKIP LOCKED, heartbeats, owner compare-and-set, expiry recovery, per-service FIFO ordering/locks, and jittered backoff.
- **Real build/deploy path:** isolated Git checkout, hostile-path/context checks, Buildx layer cache, registry push, Kubernetes reconciliation, readiness diagnostics, and Ingress route.
- **Secrets:** AES-256-GCM at rest with service/key associated data, write-only API responses, Kubernetes Secret injection, and exact-value runtime-log redaction.
- **Observability:** request IDs propagated into deployment intent/events, structured contextual logs, durable timelines, cursored logs, SSE, health state, and Prometheus metrics for HTTP, queue, jobs, builds, deployments, failures, and active work.
- **Verification:** unit/race tests, real PostgreSQL integration, fake-client Kubernetes tests, real kind routing/log/cleanup integration, container builds, and full deploy→redeploy→rollback E2E.

## Quick start

Prerequisites are Docker with Compose/Buildx, Go 1.25.13+, Node 22+, kind, kubectl, curl, and jq. Ports and troubleshooting are in [DEVELOPMENT.md](docs/DEVELOPMENT.md).

Control plane and dashboard:

~~~bash
cp .env.example .env
make dev
~~~

Full worker/registry/kind path:

~~~bash
make kind-up
docker compose --profile runtime up -d --build
make seed
~~~

Open http://localhost:3000, select **Forge Demo**, and click **Deploy latest**. The seeded image listens on port 80 and uses / for health. API request metrics are at http://localhost:8080/metrics; queue/build/job/deployment metrics are at http://localhost:9090/metrics.

Stop Compose with make down. Delete only the disposable forge kind cluster, forge-registry, and generated worker kubeconfig with make kind-down.

## Example deployment

The repository includes:

- [examples/hello-go](examples/hello-go): static Go HTTP service with /health;
- [examples/hello-node](examples/hello-node): non-root Express service with /health; and
- [examples/failures](examples/failures): invalid build, immediate crash, and failed-readiness fixtures.

Forge intentionally accepts GitHub HTTPS repositories rather than local filesystem paths. Run the complete public-fixture scenario:

~~~bash
FORGE_E2E_REPOSITORY_URL=https://github.com/traefik/whoami \
FORGE_E2E_BRANCH=master \
FORGE_E2E_PORT=80 \
FORGE_E2E_HEALTH_PATH=/ \
make e2e
~~~

## API

The human guide is [docs/API.md](docs/API.md); [docs/openapi.yaml](docs/openapi.yaml) is the OpenAPI 3.1 contract.

~~~bash
curl -fsS http://localhost:8080/health
curl -fsS 'http://localhost:8080/api/projects?limit=100' | jq
~~~

Errors use one envelope and every response has X-Request-ID:

~~~json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "resource does not exist"
  }
}
~~~

## Testing

~~~bash
make fmt-check
make lint
make test
make build
make vuln
make integration
make failure-tests
~~~

Real-cluster boundary:

~~~bash
docker build -t localhost:5001/forge/hello-go:k8s-test examples/hello-go
docker push localhost:5001/forge/hello-go:k8s-test
FORGE_TEST_IMAGE=localhost:5001/forge/hello-go:k8s-test make kubernetes-integration
~~~

CI checks Go formatting/vet/race/build, Go and npm dependency advisories, PostgreSQL migrations/API/queue/webhook integration, frontend lint/type/test/build, and both valid example images. The kind/E2E suites remain explicit local jobs because they require privileged Docker networking and a public repository.

`make failure-tests` deliberately rejects an invalid Docker context, then deploys crash-looping and unhealthy images to kind and asserts that readiness fails with Kubernetes diagnostics. It expects the local cluster and registry from `make kind-up`.

## Benchmarks

Measured on an Apple M3 Pro local stack:

| Scenario | Result |
|---|---:|
| GET /health, concurrency 50 | 8,265.77 requests/s; p50 5 ms, p95 12 ms, p99 17 ms |
| PostgreSQL-backed project list, concurrency 20 | 6,483.34 requests/s; p50 2 ms, p95 6 ms, p99 12 ms |
| bundled Go image, Buildx no-cache | 7.22 s |
| same image, cached | 1.39 s |
| E2E immutable artifact rollback | 8.386 s |

Methodology, exact commands, caveats, and intentionally unmeasured throughput cases are in [BENCHMARKS.md](docs/BENCHMARKS.md).

## Failure handling

- A build error leaves BUILDING output and finalizes the deployment as FAILED.
- A missing image, crash loop, or failed probe remains VERIFYING until the rollout deadline, then records bounded pod diagnostics.
- A transient Git/push/Kubernetes error schedules a durable retry with backoff.
- A worker death makes its lease reclaimable; a retry resumes from persisted state.
- Duplicate webhook deliveries create no duplicate job.
- API death after 202 loses no accepted deployment.
- Project/service deletion is followed by label-aware Kubernetes garbage collection.

See [DEPLOYMENTS.md](docs/DEPLOYMENTS.md) for exact semantics and deterministic failure fixtures.

## Security

Forge is not safe for hostile multi-tenancy. The local worker mounts the Docker socket and Compose uses kind administrator credentials. The API has a single local identity and no real login. Those are explicit boundaries, not production claims.

Implemented controls include strict Git URL/ref validation, argument-array process execution, bounded/cancelable contexts, encrypted write-only values, signed/deduplicated webhooks, non-root constrained pods, API/worker credential separation, RBAC guidance, defensive headers, output limits, and secret redaction.

Read the full [threat model and limitations](docs/SECURITY.md) before exposing Forge beyond a developer machine.

## Engineering tradeoffs

- **PostgreSQL queue over Redis:** atomic accept/enqueue and one operational store matter more than broker throughput at local scale.
- **Modular monolith over microservices:** API and worker are separate failure/scaling units while sharing domain rules without network-versioning overhead.
- **Dockerfile first:** explicit build contracts make failures inspectable; buildpack detection is deferred.
- **Kubernetes over direct Docker runtime:** rolling readiness, service discovery, resource policy, and reconciliation are core learning goals, not resume decoration.
- **REST + polling/SSE over GraphQL/WebSockets:** resource transitions, curl scripts, status codes, and one-way progress need less protocol machinery.
- **UUID image tags over branch tags:** every deployment has an unambiguous artifact and rollback never depends on a mutable tag.
- **One namespace locally:** operationally simple, but not a tenant isolation boundary.

## Known limitations and future improvements

1. Replace local identity with GitHub/OIDC authentication, real sessions/API tokens, per-user authorization tests, and GitHub App repository/webhook installation.
2. Move untrusted builds from the Docker socket to disposable rootless BuildKit sandboxes with egress/resource isolation, signed digests, scanning, and remote cache.
3. Add continuous runtime-log shipping/object retention, registry/image garbage collection, authenticated TLS/custom domains, NetworkPolicies, and tenant-separated namespaces/clusters.

Additional scaling choices are documented in [ARCHITECTURE.md](docs/ARCHITECTURE.md), and interview-oriented explanations are in [INTERVIEW_GUIDE.md](docs/INTERVIEW_GUIDE.md).
