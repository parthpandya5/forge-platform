# Benchmarks

These are local engineering measurements, not capacity promises. The API and BuildKit cache measurements were refreshed on 2026-08-23; the queue-to-live sample was recorded on 2026-08-20. The environment was an Apple M3 Pro (12 logical CPUs, 18 GiB RAM), macOS arm64, Docker Engine 29.1.3, kind 0.32.0 with one Kubernetes 1.36.1 control-plane node, PostgreSQL 17 in Docker, and Forge's production containers. The machine was not otherwise isolated, so tail latency is expected to vary.

## API

ApacheBench used a new HTTP/1.0 connection per request. Forge's structured access log remained enabled.

| Endpoint | Requests / concurrency | Failed | Requests/s | p50 | p95 | p99 | max |
|---|---:|---:|---:|---:|---:|---:|---:|
| `GET /health` | 5,000 / 50 | 0 | 8,265.77 | 5 ms | 12 ms | 17 ms | 26 ms |
| `GET /api/projects?limit=100` (PostgreSQL-backed, one seeded project) | 2,000 / 20 | 0 | 6,483.34 | 2 ms | 6 ms | 12 ms | 60 ms |

Reproduction:

```bash
ab -n 5000 -c 50 http://127.0.0.1:8080/health
ab -n 2000 -c 20 'http://127.0.0.1:8080/api/projects?limit=100'
```

The test does not model authentication, large response bodies, write contention, WAN latency, or a large deployment table. The high p99 jump on the database endpoint is a reason to measure under a controlled load generator before making scaling claims.

## BuildKit cache

The bundled `examples/hello-go` image was built twice. The no-cache run still reused already-downloaded base-image blobs; it forced every Dockerfile instruction to execute. The second run used the normal local BuildKit cache.

| Build | Wall time |
|---|---:|
| `--no-cache` | 7.22 s |
| cached | 1.39 s |

```bash
/usr/bin/time -p docker buildx build --load --no-cache --quiet \
  --tag forge-benchmark:hello-go-cold examples/hello-go
/usr/bin/time -p docker buildx build --load --quiet \
  --tag forge-benchmark:hello-go-cached examples/hello-go
```

The 5.2x ratio applies only to this tiny fixture on this machine; application dependency graphs and cache invalidation patterns dominate real builds.

## Queue-to-live deployment

The full E2E path used the public `traefik/whoami` repository and included PostgreSQL enqueue, clone, Buildx build/load, push to the local registry, Kubernetes reconciliation, readiness, Ingress routing, and persisted events/logs. Durations came from PostgreSQL `created_at` and `finished_at`, not shell timing.

| Attempt | Queue → terminal | Queue wait | Clone completed at | Build completed at | Push completed at | Reconcile completed at | Readiness completed at |
|---|---:|---:|---:|---:|---:|---:|---:|
| first deploy | 32.127 s | 0.543 s | 2.168 s | 22.946 s | 23.862 s | 24.114 s | 32.127 s |
| redeploy | 18.948 s | 0.251 s | 0.886 s | 14.134 s | 14.862 s | 14.933 s | 18.948 s |
| rollback | 8.386 s | 0.329 s | skipped | skipped | skipped | 0.374 s | 8.386 s |

This is one sample per path and therefore has no percentile significance. The rollback result demonstrates artifact reuse; it does not prove a general latency SLO. The E2E script also verified each generated route and cleaned the project, after which the health monitor removed all four workload resource types.

Reproduction:

```bash
FORGE_E2E_REPOSITORY_URL=https://github.com/traefik/whoami \
FORGE_E2E_BRANCH=master \
FORGE_E2E_PORT=80 \
FORGE_E2E_HEALTH_PATH=/ \
FORGE_E2E_KEEP_RESOURCES=true \
make e2e
```

Query `deployment_events.created_at - deployments.created_at` before deleting the retained project.

## Concurrency and throughput

The database integration test concurrently claimed 12 queued deployments for 12 different services and verified every deployment was leased exactly once. It separately verified same-service FIFO blocking, service-lock exclusion and failed-unlock connection cleanup, then expired and reclaimed a final-attempt lease without incrementing its logical attempt. These are correctness tests, not throughput benchmarks, so no jobs/second number is reported.

Worker throughput at concurrency 1/5/10 and simultaneous real deployments at 1/5/10 have not been measured. A credible future benchmark should use separate controlled repositories, pre-pulled bases, fixed CPU/memory, registry metrics, and repeated samples; otherwise build-network and cache variance would overwhelm worker scheduling cost.
