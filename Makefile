SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

KIND_CLUSTER       ?= gateway-e2e
CHAINSAW           ?= $(LOCALBIN)/chainsaw
CHAINSAW_VERSION   ?= v0.2.15

.PHONY: all
all: build

.PHONY: build
build:
	cd src && go build -o ../bin/gateway .

.PHONY: test
test:
	cd src && go test ./...

.PHONY: vet
vet:
	cd src && go vet ./...

.PHONY: fmt
fmt:
	cd src && go fmt ./...

.PHONY: lint
lint:
	cd src && golangci-lint run

.PHONY: lint-fix
lint-fix:
	cd src && golangci-lint run --fix

.PHONY: helm-lint
helm-lint:
	helm lint charts/kargo-loki-gateway
	helm template kargo-loki-gateway charts/kargo-loki-gateway

.PHONY: helm-package
helm-package:
	helm package charts/kargo-loki-gateway --destination bin/

.PHONY: e2e
e2e: chainsaw
	$(CHAINSAW) test test/e2e/

.PHONY: kind-create
kind-create:
	kind create cluster --name $(KIND_CLUSTER) --config hack/kind-config.yaml --wait 5m

.PHONY: kind-delete
kind-delete:
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: kind-load
kind-load: docker-build
	kind load docker-image kargo-loki-gateway:dev --name $(KIND_CLUSTER)

.PHONY: e2e-infra
e2e-infra:
	@chmod +x hack/e2e-infra/setup.sh && hack/e2e-infra/setup.sh

.PHONY: chainsaw
chainsaw: $(CHAINSAW)
$(CHAINSAW): $(LOCALBIN)
	@[ -f "$(CHAINSAW)" ] || { \
	  set -e; \
	  OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	  ARCH=$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'); \
	  URL="https://github.com/kyverno/chainsaw/releases/download/$(CHAINSAW_VERSION)/chainsaw_$${OS}_$${ARCH}.tar.gz"; \
	  echo "Downloading chainsaw $(CHAINSAW_VERSION) from $${URL}"; \
	  curl -sSfL "$$URL" | tar -xz -C $(LOCALBIN) chainsaw; \
	}

.PHONY: docs-gen
docs-gen: ## Regenerate llms.txt, llms-full.txt, AGENTS.md, knowledge.yaml from source annotations.
	cd hack/gen-docs && go run .

.PHONY: docs-diff
docs-diff: ## Show what docs-gen would change without writing files (exits 1 if stale).
	cd hack/gen-docs && go run . --diff

.PHONY: devenv
devenv: ## Start local dev environment via Tilt (creates kind cluster if needed).
	kind get clusters | grep -q $(KIND_CLUSTER) || kind create cluster --name $(KIND_CLUSTER) --config hack/kind-config.yaml --wait 5m
	tilt up

.PHONY: docker-build
docker-build:
	docker build -t kargo-loki-gateway:dev .
