#!/usr/bin/env bash
set -euo pipefail

: "${FORGE_E2E_REPOSITORY_URL:?Set FORGE_E2E_REPOSITORY_URL to a public GitHub repository containing a root Dockerfile}"

api_url="${FORGE_E2E_API_URL:-http://localhost:8080}"
branch="${FORGE_E2E_BRANCH:-main}"
port="${FORGE_E2E_PORT:-8080}"
health_path="${FORGE_E2E_HEALTH_PATH:-/health}"
timeout_seconds="${FORGE_E2E_TIMEOUT_SECONDS:-1200}"
route_timeout_seconds="${FORGE_E2E_ROUTE_TIMEOUT_SECONDS:-60}"
slug="forge-e2e-$(date +%s)-$$"
project_id=""

for command_name in curl jq; do
  command -v "${command_name}" >/dev/null || { echo "missing prerequisite: ${command_name}" >&2; exit 1; }
done

cleanup() {
  if [ -n "${project_id}" ] && [ "${FORGE_E2E_KEEP_RESOURCES:-false}" != "true" ]; then
		if ! curl -fsS -X DELETE "${api_url}/api/projects/${project_id}" >/dev/null; then
			echo "warning: failed to delete E2E project ${project_id}" >&2
		fi
  fi
}
trap cleanup EXIT

project_json=$(curl -fsS -X POST "${api_url}/api/projects" -H 'Content-Type: application/json' -d "$(jq -n --arg name "Forge E2E" --arg slug "${slug}" --arg repository_url "${FORGE_E2E_REPOSITORY_URL}" --arg repository_branch "${branch}" '{name:$name,slug:$slug,repository_url:$repository_url,repository_branch:$repository_branch}')")
project_id=$(jq -er '.data.id' <<<"${project_json}")

service_json=$(curl -fsS -X POST "${api_url}/api/projects/${project_id}/services" -H 'Content-Type: application/json' -d "$(jq -n --argjson port "${port}" --arg health "${health_path}" '{name:"web",internal_port:$port,desired_replicas:1,health_check_path:$health}')")
service_id=$(jq -er '.data.id' <<<"${service_json}")

curl -fsS -X PUT "${api_url}/api/services/${service_id}/environment/FORGE_E2E" -H 'Content-Type: application/json' -d '{"value":"true"}' >/dev/null

wait_for_deployment() {
  local deployment_id="$1" deployment_json started status
  started=$(date +%s)
  while true; do
    deployment_json=$(curl -fsS "${api_url}/api/deployments/${deployment_id}")
    status=$(jq -er '.data.status' <<<"${deployment_json}")
    echo "deployment ${deployment_id}: ${status}" >&2
    case "${status}" in
      LIVE) jq -c '.data' <<<"${deployment_json}"; return 0 ;;
      FAILED|CANCELLED)
        curl -fsS "${api_url}/api/deployments/${deployment_id}/logs?limit=1000" | jq -r '.data[] | "[\(.stream)] \(.message)"' >&2
        return 1 ;;
    esac
    if [ "$(( $(date +%s) - started ))" -ge "${timeout_seconds}" ]; then
      echo "deployment timed out after ${timeout_seconds}s" >&2
      return 1
    fi
    sleep 2
  done
}

deploy_and_wait() {
  local deployment_json deployment_id
  deployment_json=$(curl -fsS -X POST "${api_url}/api/services/${service_id}/deployments" -H 'Content-Type: application/json' -d '{}')
  deployment_id=$(jq -er '.data.id' <<<"${deployment_json}")
  wait_for_deployment "${deployment_id}"
}

wait_for_route() {
  local route="$1" path="$2" started http_code
  started=$(date +%s)
  while true; do
    http_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "${route}${path}" 2>/dev/null || true)
    case "${http_code}" in
      2??) return 0 ;;
    esac
    if [ "$(( $(date +%s) - started ))" -ge "${route_timeout_seconds}" ]; then
      echo "route ${route}${path} did not converge within ${route_timeout_seconds}s (last HTTP status: ${http_code:-none})" >&2
      return 1
    fi
    sleep 1
  done
}

first=$(deploy_and_wait)
first_id=$(jq -er '.id' <<<"${first}")
route=$(jq -er '.route_url' <<<"${first}")
wait_for_route "${route}" "${health_path}"
curl -fsS "${api_url}/api/deployments/${first_id}/events?limit=1000" | jq -e 'any(.data[]; .event_type == "deployment.live")' >/dev/null
curl -fsS "${api_url}/api/deployments/${first_id}/logs?limit=1000" | jq -e '.data | length > 0' >/dev/null

second=$(deploy_and_wait)
second_id=$(jq -er '.id' <<<"${second}")
test "${first_id}" != "${second_id}"
second_route=$(jq -er '.route_url' <<<"${second}")
wait_for_route "${second_route}" "${health_path}"

rollback_json=$(curl -fsS -X POST "${api_url}/api/deployments/${first_id}/rollback" -H 'Content-Type: application/json' -d '{}')
rollback_id=$(jq -er '.data.id' <<<"${rollback_json}")
test "${rollback_id}" != "${first_id}"
rollback=$(wait_for_deployment "${rollback_id}")
test "$(jq -er '.rollback_of' <<<"${rollback}")" = "${first_id}"
rollback_route=$(jq -er '.route_url' <<<"${rollback}")
wait_for_route "${rollback_route}" "${health_path}"

echo "E2E passed: initial=${first_id} redeploy=${second_id} rollback=${rollback_id} route=${route}"
