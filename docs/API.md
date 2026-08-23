# Forge API

The canonical machine-readable contract is [openapi.yaml](openapi.yaml). The local API listens on `http://localhost:8080`; JSON endpoints return either `{"data": ...}` or the stable error envelope below.

```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "resource does not exist"
  }
}
```

Every response includes `X-Request-ID`. A deployment stores the ID from the request that queued it, and the initial durable event includes that value. The local MVP injects the configured development user; it has owner-scoped queries but no production identity boundary. See [SECURITY.md](SECURITY.md).

## Resource workflow

Create a project and service, then queue a deployment:

```bash
project_id=$(curl -fsS http://localhost:8080/api/projects \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"Demo",
    "slug":"demo",
    "repository_url":"https://github.com/traefik/whoami",
    "repository_branch":"master",
    "auto_deploy":false
  }' | jq -r .data.id)

service_id=$(curl -fsS "http://localhost:8080/api/projects/${project_id}/services" \
  -H 'Content-Type: application/json' \
  -d '{"name":"web","internal_port":80,"desired_replicas":1,"health_check_path":"/"}' \
  | jq -r .data.id)

deployment_id=$(curl -fsS "http://localhost:8080/api/services/${service_id}/deployments" \
  -H 'Content-Type: application/json' -d '{}' | jq -r .data.id)

curl -fsS "http://localhost:8080/api/deployments/${deployment_id}" | jq
curl -fsS "http://localhost:8080/api/deployments/${deployment_id}/events" | jq
curl -fsS "http://localhost:8080/api/deployments/${deployment_id}/logs" | jq
```

Deployment creation returns `202 Accepted` because clone, build, push, reconciliation, and readiness are worker-owned operations. The `Location` header points at the deployment resource. List endpoints accept bounded `limit` and `offset`; event and log endpoints use monotonically increasing `after_id` cursors.

## Environment values

Values are write-only. `PUT` returns only metadata, and `GET` never returns plaintext or ciphertext.

```bash
curl -fsS -X PUT "http://localhost:8080/api/services/${service_id}/environment/API_TOKEN" \
  -H 'Content-Type: application/json' -d '{"value":"local-example"}' | jq
```

The value is encrypted before persistence and becomes a Kubernetes Secret entry on the next deployment. Updating a value does not silently redeploy a running service.

## Streaming

`GET /api/deployments/{id}/stream` emits `deployment.event` and `deployment.log` Server-Sent Events. A stream is intentionally capped at 25 seconds so API shutdown and proxy timeouts remain predictable. Reconnect using `after_event_id` and `after_log_id`; polling the deployment resource remains the simplest status mechanism.

```bash
curl -N "http://localhost:8080/api/deployments/${deployment_id}/stream?after_event_id=0&after_log_id=0"
```

## Rollback

Rollback is not a mutation of an old record. It creates a new queued deployment that references the selected `LIVE` deployment and reuses its immutable image.

```bash
curl -fsS -X POST "http://localhost:8080/api/deployments/${deployment_id}/rollback" \
  -H 'Content-Type: application/json' -d '{}' | jq
```

## GitHub webhook

Set `FORGE_GITHUB_WEBHOOK_SECRET` before starting the API, configure GitHub to send `push` events to `/api/webhooks/github`, and enable `auto_deploy` for the project. Forge verifies `X-Hub-Signature-256` with HMAC-SHA256, validates the repository/branch/SHA, and atomically records `X-GitHub-Delivery` with any new deployments. Repeated deliveries return `202` without creating duplicate jobs. Branch deletions and non-branch pushes are ignored.

The endpoint is not registered when the secret is empty. OAuth, repository discovery, and automatic webhook registration are not implemented.
