#!/usr/bin/env bash
set -euo pipefail

kind delete cluster --name forge
if docker inspect forge-registry >/dev/null 2>&1; then
  docker stop forge-registry >/dev/null
  docker rm forge-registry >/dev/null
fi
rm -f deploy/kubernetes/generated/worker-kubeconfig
