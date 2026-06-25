#!/usr/bin/env bash
set -euo pipefail

# Real-world context-compression validation against a live Kubernetes cluster.
# This script:
# 1) sources .env for model/provider credentials
# 2) creates an isolated noisy pod with a unique log marker
# 3) runs the s24_k8s_context_e2e harness
# 4) cleans up the namespace

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is required" >&2
  exit 1
fi

if [[ -z "${OMNIS_PROVIDER:-}" ]]; then
  echo "OMNIS_PROVIDER is not set (.env should define it)" >&2
  exit 1
fi

NS="context-e2e-$(date +%s)"
POD="cm-loggen"
MARKER="CTX-MARKER-$(date +%s)-$RANDOM"

cleanup() {
  kubectl delete ns "$NS" --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> kube context: $(kubectl config current-context)"
echo "==> creating namespace: $NS"
kubectl create ns "$NS" >/dev/null

echo "==> creating noisy pod"
kubectl -n "$NS" run "$POD" \
  --image=busybox:1.36 \
  --restart=Never \
  -- /bin/sh -c "i=0; while true; do echo marker=$MARKER line=\$i ts=\$(date -u +%s); i=\$((i+1)); sleep 0.1; done" >/dev/null

kubectl -n "$NS" wait --for=condition=Ready "pod/$POD" --timeout=120s >/dev/null
sleep 6

echo "==> running e2e harness"
go run ./examples/s24_k8s_context_e2e \
  --namespace "$NS" \
  --pod "$POD" \
  --marker "$MARKER" \
  --audit-path ".agent_memory_k8s_e2e_${NS}.md"

echo "==> done"
