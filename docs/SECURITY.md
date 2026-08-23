# Security

## Scope and posture

Forge is a trusted-operator, single-user local platform. It demonstrates concrete controls, but it is not a production multi-tenant sandbox. In particular, building an untrusted Dockerfile through a host Docker daemon grants that build a path to daemon-equivalent impact. Do not expose this deployment to repositories or users you do not trust.

## Threat model

Assets include the Forge database, environment secrets, Docker host, registry credentials, Kubernetes credentials, application workloads, deployment history, and network reachability from builders/pods.

Potential attackers include:

- a network client that can reach the unauthenticated local API;
- an author of a connected repository or Dockerfile;
- a forged or replayed GitHub webhook sender;
- a compromised deployed application;
- a malicious dependency fetched during a build; and
- an operator mistake that exposes local credentials or infrastructure ports.

Representative attacks are unauthorized project/deployment changes, SSRF, Git argument injection, path traversal/symlink escape, host takeover through Docker, secret exfiltration, webhook replay, resource exhaustion, privileged pod execution, cross-service traffic, and persisted-log flooding.

## Trust boundaries

The API, worker configuration, PostgreSQL, registry, and Kubernetes control plane are trusted. Browser/API input, webhook payloads, Git content, Dockerfile instructions, image contents, application traffic, and application output are untrusted.

Only the worker receives Docker and Kubernetes access. The API stores intent and cannot directly build or reconcile workloads. This separation limits an API-handler vulnerability, but a worker compromise remains high impact.

## Current defenses

### Identity and authorization

- Store queries take a user ID and join ownership relationships, so resources cannot be fetched through an unrelated owner ID.
- The local authenticator injects one deterministic configured user; it is an extension seam, not real authentication.
- CORS is restricted to the configured dashboard origin and responses include defensive browser headers. CORS does not provide authorization.

### Repository and command handling

- Repository URLs must be credential-free HTTPS URLs on `github.com` with exactly owner/repository path segments. This removes arbitrary Git-protocol SSRF from the source adapter.
- Git refs reject traversal, double separators, lock suffixes, and shell metacharacters.
- Git and Docker are invoked with argument arrays, never interpolated shell command strings.
- Clone uses an isolated random temporary directory, does not initialize submodules, has a deadline, and always removes the directory.
- The build context requires a regular root Dockerfile, rejects symlinks escaping the checkout, checks cancellation, and is capped at 100,000 files and 512 MiB.

### Secrets

- Environment values are encrypted before database persistence with AES-256-GCM, random nonces, and service-ID/key associated data.
- API responses expose only key/ID/timestamps. Values and ciphertext are never returned.
- Build steps do not receive runtime environment secrets.
- Captured runtime lines replace configured plaintext secret values with `[REDACTED]` before persistence.
- Kubernetes receives values through an opaque Secret referenced by the workload.
- Outside development/test, process startup fails unless `FORGE_ENCRYPTION_KEY` is a base64-encoded 32-byte key.

### Webhooks

- The optional endpoint is not mounted without a secret of at least 16 characters.
- The handler limits bodies to 1 MiB and verifies `X-Hub-Signature-256` using constant-time HMAC-SHA256 comparison before parsing.
- Repository, branch, and 40-character commit SHA are revalidated; branch deletions and non-branch events are ignored.
- `X-GitHub-Delivery` is inserted in the same transaction as generated deployments/jobs. Replays do not enqueue twice.

### Workloads and Kubernetes

- Pods run with numeric UID/GID 65532, `runAsNonRoot`, no privilege escalation, all Linux capabilities dropped, runtime-default seccomp, resource requests/limits, and no service-account token.
- Replicas are capped at 20, CPU at 8 cores, and memory at 16 GiB per pod.
- HTTP readiness gates traffic and deployment success; liveness delegates restart behavior to Kubernetes.
- Forge-labelled orphan resources are removed after their database service disappears.
- [worker-rbac.yaml](../deploy/kubernetes/worker-rbac.yaml) documents the namespaced permissions required by an in-cluster worker.

### Availability and auditability

- External source, build, push/reconcile, rollout, and health operations are time-bounded.
- Leased jobs, heartbeats, bounded retries, advisory locks, and compare-and-set state changes limit duplicate execution and recover worker crashes. A worker that loses heartbeat ownership cancels without stale finalization, and a failed session-lock release destroys its pooled connection.
- Request IDs, immutable deployments, ordered events, structured logs, and Prometheus metrics provide an audit trail.
- Source/build output is capped at 16 MiB or 20,000 records per stage; individual database log records are capped at 16 KiB.

## Known limitations

### Critical local-only limitations

- There is no login, session, API token, CSRF boundary, role model, tenant rate limiting, or per-user namespace. Anyone who can reach the API acts as the local user.
- The Compose worker mounts the Docker socket. The non-root Unix identity does not meaningfully reduce Docker-daemon privilege.
- Dockerfile build steps have normal build-network access and can scan internal networks, download mutable dependencies, consume host resources, and exploit builder/kernel vulnerabilities.
- Compose uses kind's administrator kubeconfig for local convenience. The RBAC manifest is a deployment reference, not what the Compose worker currently uses.
- Local HTTP, PostgreSQL, registry, and Kubernetes endpoints are not configured with an application-level TLS/authentication perimeter.
- Default PostgreSQL credentials and the deterministic development encryption fallback are intentionally insecure outside a developer machine.

### Other limitations

- All application Secrets share one namespace; the worker can read them to construct/update workloads.
- There are no NetworkPolicies, image signature verification, container-image scanning, admission policies, read-only root filesystem enforcement, registry retention, or image garbage collection. CI scans Go and npm dependency advisories; `make vuln` scans reachable Go vulnerabilities.
- Runtime redaction matches exact configured plaintext values. Encoded, transformed, partial, or newly fetched secrets can still be emitted by a malicious application.
- Build/runtime logs remain in PostgreSQL until their project is deleted. There is no retention policy or external immutable log store.
- A single user's project slug determines the hostname. Multi-user mode would require global hostname allocation or tenant-qualified domains.
- GitHub OAuth/App repository selection and automated webhook registration are not implemented.
- Webhook delivery records have no retention task.
- Environment changes affect the next deployment; Kubernetes Secret values are not rotated in an already-running pod automatically.
- The build-context cap is checked after Git fetch. A repository's Git object database can consume more temporary disk than the checked-out context before validation; dedicated builders need filesystem quotas.

## Production hardening path

1. Put authenticated TLS ingress in front of Forge; use OIDC/GitHub identity, secure cookies or scoped API tokens, CSRF protection, per-resource authorization, audit principals, and rate limits.
2. Move builds to disposable rootless BuildKit workers or dedicated VMs with no control-plane credentials, strict CPU/memory/disk/time quotas, restricted egress, and a one-way artifact push identity.
3. Use an authenticated external registry with immutable tags, retention, scanning, signatures/attestations, and admission verification.
4. Use a cloud KMS/secrets manager with envelope encryption, key versions/rotation, short-lived workload delivery, and audit logs.
5. Separate tenants by namespace or cluster, enforce Restricted Pod Security plus admission policy, default-deny NetworkPolicies, quotas, and narrowly scoped service accounts.
6. Replace kind-admin credentials with short-lived workload identity and the smallest verified RBAC rules. Separate build and deploy workers so builders never hold Kubernetes credentials.
7. Add durable log/object storage with retention and redaction controls, webhook-record expiry, registry cleanup, backups, restore tests, and security monitoring.

## Reporting

Do not commit credentials or vulnerability details that expose a running environment. Rotate any key that may have appeared in Git history or logs; changing the encryption key without a migration makes existing environment ciphertext unreadable.
