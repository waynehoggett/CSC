# Claude Sandbox Controller (CSC)

A Kubernetes controller for [Anthropic self-hosted sandboxes](https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes),
modeled after [actions-runner-controller](https://github.com/actions/actions-runner-controller).
It packages the scaffolding every org otherwise reimplements — Secret handling,
KEDA queue-depth scaling, webhook ingress + signature verification, per-session
pod isolation, and RBAC scoping — behind a single `ClaudeEnvironment` CRD.

> **Status:** pre-alpha (`v1alpha1`). See [SPEC.md](SPEC.md) for the full design.

## What it does

A `ClaudeEnvironment` runs sandbox workers for one Anthropic environment in one
of two modes:

- **AlwaysOn** — a long-lived `Deployment` of `ant beta:worker poll` workers,
  scaled 0→N by KEDA on work-queue depth.
- **Webhook** — a handler that wakes on `session.status_run_started`, verifies
  the signature, and creates one `Job` per session. Scales to zero, no KEDA.

The controller owns *lifecycle and scaling*; Anthropic's worker binaries do the
execution. Control-plane credentials (org API key) and data-plane credentials
(environment key) are strictly separated — worker pods only ever see the env key.

## Quick start

Install (Helm):

```bash
helm install csc oci://ghcr.io/waynehoggett/charts/claude-sandbox-controller \
  --namespace claude-system --create-namespace
```

or plain manifests:

```bash
kubectl apply -f https://github.com/waynehoggett/csc/releases/download/v0.1.0/install.yaml
```

Then follow a mode guide:

- [Getting started — AlwaysOn](docs/getting-started-alwayson.md)
- [Getting started — Webhook](docs/getting-started-webhook.md)

## Documentation

- [Architecture](docs/architecture.md)
- [Security model](docs/security.md)
- [`ClaudeEnvironment` reference](docs/reference.md)
- [Design / spec](SPEC.md)

## Development

Requires Go 1.24+, Docker, and Make. Tooling (`controller-gen`, `kustomize`) is
installed into `./bin` on demand.

```bash
make test            # generate, vet, unit tests
make build           # build manager + webhook-handler binaries
make manifests       # regenerate CRD + RBAC
make install-manifest# regenerate manifests/install.yaml
make docker-build    # build all three images
```

Tagged suites:

```bash
# Integration (envtest):
KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test -tags integration ./test/integration/...
# E2E (against a kind cluster with CSC deployed):
go test -tags e2e ./test/e2e/...
```

### Layout

```
api/v1alpha1/         ClaudeEnvironment types
cmd/controller/       controller-manager entrypoint
cmd/webhook-handler/  webhook handler entrypoint
internal/controller/  reconciler (alwayson, webhook, secrets)
internal/webhook/      signature verify + job spawner
config/               kustomize manifests (CRD, RBAC, manager)
charts/               Helm chart
images/               worker + webhook-handler Dockerfiles
docs/                 guides and reference
test/                 integration (envtest) + e2e (kind)
```
