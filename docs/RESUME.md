# Resume

## Forge — Self-Hosted Cloud Application Platform | Go, TypeScript, PostgreSQL, Docker Buildx, Kubernetes, Next.js

- Engineered a self-hosted PaaS control plane that asynchronously clones GitHub repositories, builds and pushes immutable OCI images, and programmatically reconciles hardened Kubernetes Deployments, Services, Secrets, and Ingress routes.
- Designed an at-least-once deployment engine with a validated state machine, atomic enqueue/event transactions, leased concurrent workers, heartbeats, exponential backoff, advisory locks, readiness diagnostics, encrypted environment values, immutable history, and artifact-based rollback.
- Delivered an observable, reproducible local platform with Prometheus metrics, structured request/deployment tracing, SSE event/log updates, kind/registry automation, GitHub webhook deduplication, CI race/integration/image tests, and a validated deploy→redeploy→rollback E2E path.

Measured figures suitable for a longer project description, if space and context justify them: 8,265 requests/s for the local liveness endpoint, 6,483 requests/s for a one-row PostgreSQL-backed project listing, a 7.22 s no-cache versus 1.39 s cached bundled build, and an 8.386 s local artifact rollback. Keep the hardware/test caveats from [BENCHMARKS.md](BENCHMARKS.md); do not present these local samples as production scale or user impact.
