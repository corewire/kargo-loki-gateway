#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS="e2e-infra"
ARGO_ROLLOUTS_VERSION="${ARGO_ROLLOUTS_VERSION:-v1.9.1}"
# Pass KUBECTL_CONTEXT=kind-gateway-e2e (or any context) to target a specific cluster.
KC="${KUBECTL_CONTEXT:+--context ${KUBECTL_CONTEXT}}"
K="kubectl $KC"

echo "[e2e-infra] Creating namespace $NS..."
$K create namespace "$NS" --dry-run=client -o yaml | $K apply -f -

echo "[e2e-infra] Installing Argo Rollouts $ARGO_ROLLOUTS_VERSION (CRDs + controller)..."
$K create namespace argo-rollouts --dry-run=client -o yaml | $K apply -f -
$K -n argo-rollouts apply -f "https://github.com/argoproj/argo-rollouts/releases/download/${ARGO_ROLLOUTS_VERSION}/install.yaml"

echo "[e2e-infra] Deploying Loki..."
$K apply -n "$NS" -f "$SCRIPT_DIR/loki.yaml"

echo "[e2e-infra] Deploying Alloy (pod logs → Loki with structured metadata)..."
$K apply -f "$SCRIPT_DIR/alloy.yaml"

echo "[e2e-infra] Deploying gateway..."
$K apply -f "$SCRIPT_DIR/gateway.yaml"

echo "[e2e-infra] Waiting for Argo Rollouts controller..."
$K -n argo-rollouts wait --for=condition=available deployment/argo-rollouts --timeout=120s

echo "[e2e-infra] Waiting for Loki..."
$K -n "$NS" wait --for=condition=available deployment/loki --timeout=300s

echo "[e2e-infra] Waiting for Alloy..."
$K -n "$NS" wait --for=condition=available deployment/alloy --timeout=120s

echo "[e2e-infra] Waiting for gateway..."
$K -n "$NS" wait --for=condition=available deployment/kargo-loki-gateway --timeout=120s

echo "[e2e-infra] Infrastructure ready."
echo "  Loki:    http://loki.$NS.svc.cluster.local:3100"
echo "  Gateway: http://kargo-loki-gateway.$NS.svc.cluster.local"
