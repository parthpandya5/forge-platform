#!/usr/bin/env bash
set -euo pipefail

for command_name in docker go kubectl; do
  command -v "${command_name}" >/dev/null || { echo "missing prerequisite: ${command_name}" >&2; exit 1; }
done

docker info >/dev/null
kubectl get namespace forge-apps >/dev/null

failure_namespace="forge-failure-tests"
cleanup() {
	status=$?
	trap - EXIT
	if ! kubectl delete namespace "${failure_namespace}" --ignore-not-found --wait=true >/dev/null; then
		echo "warning: failed to delete failure-test namespace ${failure_namespace}" >&2
	fi
	exit "${status}"
}
trap cleanup EXIT
kubectl delete namespace "${failure_namespace}" --ignore-not-found --wait=true >/dev/null

if docker buildx build --load --progress=plain --tag forge/expected-build-failure:test examples/failures/build-failure; then
  echo "invalid build fixture unexpectedly succeeded" >&2
  exit 1
fi
echo "verified deterministic Docker build failure" >&2

crash_image="localhost:5001/forge/failure-crash:test"
unhealthy_image="localhost:5001/forge/failure-unhealthy:test"
docker buildx build --load --tag "${crash_image}" examples/failures/crash
docker push "${crash_image}"
docker buildx build --load --tag "${unhealthy_image}" examples/failures/unhealthy
docker push "${unhealthy_image}"

FORGE_FAILURE_CRASH_IMAGE="${crash_image}" \
FORGE_FAILURE_UNHEALTHY_IMAGE="${unhealthy_image}" \
FORGE_KUBERNETES_NAMESPACE="${failure_namespace}" \
go test -count=1 -tags=kubernetes -v -run TestRuntimeReportsFailedWorkloads ./tests/kubernetes/...
