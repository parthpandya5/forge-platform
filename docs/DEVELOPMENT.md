# Development

## Prerequisites

- Docker Desktop or a compatible Docker Engine with Compose and Buildx
- Go 1.25 or newer for native backend work
- Node.js 22 and npm for native dashboard work
- `kind`, `kubectl`, `curl`, and `jq` for cluster/E2E work
- host ports `80`, `3000`, `5001`, `8080`, `9090`, and `55432` available for the full local stack

Forge's tested local path is a kind cluster backed by a registry container. Docker Desktop Kubernetes can work with a separately configured registry and kubeconfig, but the provided automation targets kind.

## Control-plane development

```bash
cp .env.example .env
make dev
```

This starts PostgreSQL, runs ordered SQL migrations, then starts the API and dashboard. The worker is deliberately excluded because it requires Docker and Kubernetes credentials.

- dashboard: `http://localhost:3000`
- API liveness: `http://localhost:8080/health`
- API dependency readiness: `http://localhost:8080/ready`
- API request/deployment-intent metrics: `http://localhost:8080/metrics`
- worker queue/build/job/deployment metrics (full runtime): `http://localhost:9090/metrics`
- PostgreSQL: `localhost:55432`

Use `make seed` to idempotently create a deployable `traefik/whoami` sample project for the local user.

## Full deployment runtime

```bash
make kind-up
make dev-full
```

`make dev-full` also runs the idempotent `kind-up` prerequisite. The setup script:

1. creates the `forge` kind cluster if absent;
2. starts `forge-registry` on `localhost:5001` and connects it to kind's Docker network;
3. installs the pinned ingress-nginx manifest;
4. publishes local-registry discovery metadata; and
5. generates an ignored, container-safe kubeconfig at `deploy/kubernetes/generated/worker-kubeconfig`.

The generated kubeconfig rewrites the kind API endpoint to `host.docker.internal` while preserving TLS server-name validation. The worker runs as a non-root user, receives supplemental group `0` for the local Docker socket, and is the only Forge process with Docker/Kubernetes access.

The default full-stack settings push immutable images to `localhost:5001` and use `Always` pull policy. The exact primary `web` service is routed as `<project>.localhost`; other service names use `<service>-<project>-<service-id-prefix>.localhost` so normalized names cannot collide.

## Native processes

Run PostgreSQL through Compose while iterating on Go:

```bash
docker compose up -d postgres
make migrate
go run ./apps/api
go run ./apps/worker
npm --prefix apps/web run dev
```

The native worker uses the active kubeconfig and host Docker socket. The defaults expect PostgreSQL on `55432`; set `FORGE_REGISTRY=localhost:5001` and `FORGE_PUSH_IMAGES=true` when deploying to kind.

## Validation

```bash
make fmt-check
make lint
make test
make build
make vuln
make integration
FORGE_TEST_IMAGE=localhost:5001/forge/hello-go:k8s-test make kubernetes-integration
make failure-tests
```

`make integration` requires the Compose PostgreSQL service. The Kubernetes test requires a pushed image; the exact preparation is:

```bash
docker build -t localhost:5001/forge/hello-go:k8s-test examples/hello-go
docker push localhost:5001/forge/hello-go:k8s-test
FORGE_TEST_IMAGE=localhost:5001/forge/hello-go:k8s-test make kubernetes-integration
```

The direct runtime suite defaults to its own `forge-kubernetes-tests` namespace so a concurrently running Forge worker cannot garbage-collect test workloads that intentionally have no database service row.

`make failure-tests` performs three expected-failure checks: the invalid Docker context must not build, and crash-looping plus probe-failing images must not become ready in kind. The Kubernetes cases run concurrently and take about one rollout-test deadline.

The full E2E test requires a public GitHub repository because the product intentionally rejects local paths and arbitrary Git hosts:

```bash
FORGE_E2E_REPOSITORY_URL=https://github.com/traefik/whoami \
FORGE_E2E_BRANCH=master \
FORGE_E2E_PORT=80 \
FORGE_E2E_HEALTH_PATH=/ \
make e2e
```

It creates a project and environment value, deploys twice, waits for the Ingress route to converge, rolls back to the first image, waits for the rollback to become `LIVE`, and removes the project. Route convergence has a separate 60-second bound (`FORGE_E2E_ROUTE_TIMEOUT_SECONDS`) because ingress-nginx can observe a ready workload just after Forge does. Set `FORGE_E2E_KEEP_RESOURCES=true` only for debugging.

## Migrations

Migrations are ordered `*.up.sql` files recorded in `schema_migrations` and run under PostgreSQL advisory lock. Add a new version rather than editing a migration that another checkout may already have applied.

```bash
make migrate
```

Down files document reversibility but the migration runner intentionally applies only forward migrations.

## Troubleshooting

- **Port 80 is occupied:** kind cannot publish ingress. Stop the conflicting service or alter both kind's port mapping and the documented route URLs.
- **Worker cannot connect to Kubernetes:** rerun `make kind-up`; kind recreations change the API port and require a regenerated container kubeconfig.
- **Image pull failure:** verify `docker ps` includes `forge-registry`, the worker tag begins with `localhost:5001`, and `FORGE_PUSH_IMAGES=true`.
- **Docker permission denied in the worker:** inspect the mounted socket ownership and Compose `group_add`. Do not solve this by mounting the socket into the API.
- **`*.localhost` does not resolve:** send a request to `127.0.0.1` with `Host: <project>.localhost` or configure an equivalent local wildcard resolver.
- **Rollout remains `VERIFYING`:** inspect the deployment's persisted error/log records and `kubectl describe pods -n forge-apps`. Forge records waiting/terminated container diagnostics when the deadline expires.

Stop the stack with `make down`. Remove the disposable cluster and registry with `make kind-down`; that operation deletes only resources named by the Forge scripts.
