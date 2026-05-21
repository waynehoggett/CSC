# Image URLs to use for building/pushing.
CONTROLLER_IMG ?= ghcr.io/waynehoggett/csc-controller:latest
WEBHOOK_IMG    ?= ghcr.io/waynehoggett/csc-webhook-handler:latest
WORKER_IMG     ?= ghcr.io/waynehoggett/csc-worker:latest

# Local tool binaries.
LOCALBIN ?= $(shell pwd)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
KUSTOMIZE      ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN_VERSION ?= v0.16.5
KUSTOMIZE_VERSION      ?= v5.4.3

CRD_OPTIONS ?= crd

.PHONY: all
all: build

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRD, RBAC manifests.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths=./api/... output:crd:dir=config/crd/bases
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths=./internal/... output:rbac:dir=config/rbac
	cp config/crd/bases/csc.io_claudeenvironments.yaml charts/claude-sandbox-controller/crds/

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods.
	$(CONTROLLER_GEN) object paths=./api/...

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet ## Run unit tests.
	go test ./... -coverprofile cover.out

.PHONY: build
build: manifests generate fmt vet ## Build controller and webhook-handler binaries.
	go build -o bin/manager ./cmd/controller
	go build -o bin/webhook-handler ./cmd/webhook-handler

.PHONY: run
run: manifests generate fmt vet ## Run the controller locally against the configured cluster.
	go run ./cmd/controller

##@ Build artifacts

.PHONY: install-manifest
install-manifest: manifests kustomize ## Regenerate manifests/install.yaml.
	$(KUSTOMIZE) build config/default > manifests/install.yaml

.PHONY: docker-build
docker-build: ## Build all container images.
	docker build -t $(CONTROLLER_IMG) -f Dockerfile .
	docker build -t $(WEBHOOK_IMG) -f images/webhook-handler/Dockerfile .
	docker build -t $(WORKER_IMG) -f images/worker/Dockerfile images/worker

##@ Deployment

.PHONY: install
install: manifests kustomize ## Install CRDs into the cluster.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the cluster.
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the cluster.
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found -f -

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart.
	helm lint charts/claude-sandbox-controller

##@ Tools

.PHONY: controller-gen
controller-gen: $(LOCALBIN)
	@test -x $(CONTROLLER_GEN) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: kustomize
kustomize: $(LOCALBIN)
	@test -x $(KUSTOMIZE) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)

$(LOCALBIN):
	mkdir -p $(LOCALBIN)
