# Local development environment.
# Usage: tilt up
# Requires: kind, docker, helm

CLUSTER = 'gateway-e2e'
CONTEXT = 'kind-' + CLUSTER

# Ensure cluster exists and switch to it.
local('kind get clusters | grep -q ' + CLUSTER + ' || kind create cluster --name ' + CLUSTER + ' --config hack/kind-config.yaml --wait 5m')
local('kind export kubeconfig --name ' + CLUSTER + ' --kubeconfig .kubeconfig')
os.putenv('KUBECONFIG', os.path.join(config.main_dir, '.kubeconfig'))
allow_k8s_contexts(CONTEXT)

# Install Argo Rollouts CRDs + controller once (idempotent).
local_resource(
    'argo-rollouts',
    'kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f - && ' +
    'kubectl -n argo-rollouts apply -f https://github.com/argoproj/argo-rollouts/releases/download/v1.8.0/install.yaml && ' +
    'kubectl -n argo-rollouts wait --for=condition=available deployment/argo-rollouts --timeout=120s',
    labels=['infra'],
)

# Namespace for e2e / dev workloads.
local('kubectl create namespace e2e-infra --dry-run=client -o yaml | kubectl apply -f -')

# Static infra: Loki + Alloy.
k8s_yaml(['hack/e2e-infra/loki.yaml', 'hack/e2e-infra/alloy.yaml'])

# Build gateway image and load it into kind on every src/ change.
custom_build(
    'kargo-loki-gateway',
    'docker build -t $EXPECTED_REF . && kind load docker-image $EXPECTED_REF --name ' + CLUSTER,
    deps=['src/', 'Dockerfile'],
    ignore=['src/kargo-loki-gateway'],
)

# Gateway deployment (RBAC + Deployment + Service).
k8s_yaml('hack/e2e-infra/gateway.yaml')

k8s_resource(
    'kargo-loki-gateway',
    port_forwards=['8080:8080'],
    labels=['gateway'],
    objects=[
        'kargo-loki-gateway:serviceaccount:e2e-infra',
        'kargo-loki-gateway-e2e:clusterrole',
        'kargo-loki-gateway-e2e:clusterrolebinding',
    ],
)

k8s_resource('loki',  port_forwards=['3100:3100'], labels=['infra'])
k8s_resource('alloy', labels=['infra'])
