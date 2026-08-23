#!/usr/bin/env bash
set -euo pipefail

cluster_name="forge"
registry_name="forge-registry"
registry_port="5001"
ingress_version="controller-v1.12.1"
container_kubeconfig="deploy/kubernetes/generated/worker-kubeconfig"

for command_name in docker kind kubectl; do
  command -v "${command_name}" >/dev/null || { echo "missing prerequisite: ${command_name}" >&2; exit 1; }
done

if ! docker inspect "${registry_name}" >/dev/null 2>&1; then
  docker run -d --restart=always -p "127.0.0.1:${registry_port}:5000" --name "${registry_name}" registry:2 >/dev/null
elif [ "$(docker inspect -f '{{.State.Running}}' "${registry_name}")" != "true" ]; then
  docker start "${registry_name}" >/dev/null
fi

if ! kind get clusters | grep -qx "${cluster_name}"; then
  kind create cluster --name "${cluster_name}" --config deploy/kubernetes/kind-config.yaml
fi

if ! docker inspect "${registry_name}" --format '{{json .NetworkSettings.Networks}}' | grep -q 'kind'; then
  docker network connect kind "${registry_name}"
fi

kubectl apply -f "https://raw.githubusercontent.com/kubernetes/ingress-nginx/${ingress_version}/deploy/static/provider/kind/deploy.yaml"
kubectl wait --namespace ingress-nginx --for=condition=Available deployment/ingress-nginx-controller --timeout=180s

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${registry_port}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

mkdir -p "$(dirname "${container_kubeconfig}")"
temporary_kubeconfig=$(mktemp "${container_kubeconfig}.XXXXXX")
trap 'rm -f "${temporary_kubeconfig}"' EXIT
kind get kubeconfig --name "${cluster_name}" | awk '
  /server: https:\/\/127\.0\.0\.1:/ {
    sub("127.0.0.1", "host.docker.internal")
    print
    print "    tls-server-name: localhost"
    next
  }
  { print }
' > "${temporary_kubeconfig}"
chmod 600 "${temporary_kubeconfig}"
mv "${temporary_kubeconfig}" "${container_kubeconfig}"
trap - EXIT

echo "Forge kind cluster is ready; registry: localhost:${registry_port}; worker kubeconfig: ${container_kubeconfig}"
