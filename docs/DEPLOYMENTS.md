# Deployment Pipeline

## Durable request boundary

`POST /api/services/{id}/deployments` does not perform deployment work. One PostgreSQL transaction verifies the owner-scoped service, takes a short service enqueue lock, inserts the immutable deployment record, appends `deployment.queued`, and inserts its unique job row. The API returns `202` only after commit. There is no state where a visible deployment lacks its initial job, and simultaneous requests for one service receive a durable FIFO order.

The state machine permits these normal paths:

```text
QUEUED → CLONING → BUILDING → PUSHING → DEPLOYING → VERIFYING → LIVE
                           ↘ DEPLOYING                         ↘ FAILED
QUEUED (rollback) → DEPLOYING → VERIFYING → LIVE
```

`PUSHING` is skipped only when registry push is explicitly disabled. Any nonterminal state may transition to `FAILED` or `CANCELLED`; the current API does not expose cancellation. Terminal records cannot transition again.

Status and its corresponding event are updated in one transaction after a row lock and state-machine check. The `version` column increments on state or artifact metadata changes and makes unexpected concurrent mutations visible.

## Queue and crash recovery

Jobs live in PostgreSQL so enqueue participates in the same transaction as deployment creation. Workers select eligible rows with `FOR UPDATE SKIP LOCKED`, commit a lease, and execute outside the transaction. Heartbeats extend the lease only when both job ID and worker owner still match.

- a crashed worker stops heartbeating, so another worker can reclaim the expired row;
- each fresh attempt increments `attempts` and is bounded by `max_attempts`; reclaiming an expired lease resumes that logical attempt so a crash on the last attempt cannot strand the row;
- transient errors return the row to `available` with capped exponential backoff plus jitter;
- deterministic validation, Dockerfile/context, and rollout errors fail immediately;
- shutdown abandons the owned lease without consuming an attempt;
- newer jobs for one service remain in FIFO order while other services can run concurrently;
- a session-level PostgreSQL advisory lock keyed by service ID fences an expired prior owner that may still be executing.

The worker re-reads deployment state on every attempt. Terminal work is a no-op. A retry at `BUILDING` reuses an already-persisted image without another checkout/build; if no image was recorded, it checks out the recorded commit rather than a moving branch and rejects a different resolved SHA. Kubernetes resources use stable names derived from service ID, so reconciliation can be repeated.

This is at-least-once delivery with idempotent state/resource operations, not exactly-once execution.

## Source and build

The source adapter accepts only credential-free `https://github.com/{owner}/{repository}` URLs and safe Git refs. It creates a unique temporary directory, shallow-fetches the requested ref without tags or submodules, resolves a 40-character SHA, and always removes the directory. Before Docker receives the context, Forge requires a regular root Dockerfile, rejects escaping symlinks, limits the context to 100,000 regular files and 512 MiB, and honors the clone deadline while walking it.

Buildx builds and loads an OCI image through argument arrays—user values are never interpolated into a shell command. Stage output is drained concurrently from stdout/stderr, split safely even for hostile oversized lines, persisted at at most 16 MiB or 20,000 records per stage, and associated with the deployment.

Images use:

```text
<registry>/<repository>/<project-slug>:<deployment-uuid>
```

The deployment UUID, rather than branch or commit alone, distinguishes retries/rebuilds and gives rollback an exact artifact. BuildKit's normal layer cache makes repeated builds faster without introducing a Forge-specific cache service. Registry garbage collection is not yet implemented.

Docker builds execute untrusted Dockerfile instructions with daemon-equivalent power over the worker host. This is suitable only for trusted repositories on the documented local deployment; see [SECURITY.md](SECURITY.md).

## Kubernetes reconciliation

For each service Forge creates or updates a stable set of namespaced resources:

- an opaque Secret populated from decrypted environment values;
- a Deployment with immutable image, fixed UID/GID 65532, dropped capabilities, runtime-default seccomp, disabled service-account token, resource bounds, HTTP readiness/liveness probes, and rolling update `maxUnavailable: 0`, `maxSurge: 1`;
- a ClusterIP Service mapping port 80 to the configured container port; and
- a networking/v1 Ingress for the derived local hostname.

The worker waits until Kubernetes has observed the current Deployment generation and all desired replicas are updated and available. `ProgressDeadlineExceeded`, pull errors, crash-loop waiting reasons, terminated exit codes, and probe-related pod conditions are included in bounded diagnostics. Both reconciliation and readiness have deadlines.

The fixed runtime identity is a deliberate local safety policy. Images that require root or a specific filesystem-owning UID are incompatible unless rebuilt accordingly.

## Logs and live updates

Checkout/build output is appended while work runs. After readiness, Forge captures a bounded initial snapshot from the newest application pod before marking the deployment `LIVE`. The REST API supports ID cursors, and the SSE endpoint multiplexes event/log records in bounded reconnectable sessions. Continuous Kubernetes log tailing and long-term object storage are not implemented.

## Rollback

Rollback accepts only a `LIVE` source with an image tag. In one transaction Forge creates a new `QUEUED` deployment/job, sets `rollback_of`, and copies the source image tag and commit SHA. The worker skips source/build/push and reconciles that image through the same readiness gate. The original and intervening deployments remain unchanged.

Environment values are service configuration, not part of an image artifact, so rollback uses the service's current variables.

## Cleanup

Deleting a project or service cascades its relational history. The health monitor compares database service IDs with all Forge-labelled Deployments, Services, Ingresses, and Secrets and removes orphaned workload sets. Cleanup is periodic and was validated against kind. Local Docker images and registry blobs are currently retained; operators should prune them according to their registry retention policy.

## Failure expectations

| Failure | Observable result | Retry behavior |
|---|---|---|
| invalid URL/ref/Dockerfile or escaping context | stage remains contextual, then `FAILED` | permanent |
| Git/network interruption | event records retry scheduling | transient, bounded |
| deterministic Dockerfile/context error | `BUILDING → FAILED`, build output retained | permanent |
| ambiguous build command/network error | retry event, then `BUILDING → FAILED` if exhausted | transient, bounded |
| registry push/network error | `PUSHING`, then retry or `FAILED` | transient, bounded |
| image pull or pod crash | `VERIFYING → FAILED` with pod diagnostics | permanent after rollout deadline |
| worker process death | lease expires; another worker resumes durable state | resumes the interrupted attempt |
| API process death after `202` | no deployment loss; worker uses committed job | unaffected |
| duplicate webhook | existing delivery outcome returned; no new job | deduplicated transactionally |
