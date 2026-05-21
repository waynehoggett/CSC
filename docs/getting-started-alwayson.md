# Getting started — AlwaysOn mode

AlwaysOn runs a long-lived `Deployment` of polling workers, scaled 0→N by KEDA
based on the Anthropic work-queue depth.

## Prerequisites

- Kubernetes v1.28+ (kind, minikube, or a real cluster) and `kubectl`.
- KEDA installed:
  ```bash
  helm install keda kedacore/keda -n keda --create-namespace
  ```
- An Anthropic environment — see the
  [Anthropic docs](https://platform.claude.com/docs/en/managed-agents/self-hosted-sandboxes).

## 1. Install CSC

Helm:

```bash
helm install csc oci://ghcr.io/waynehoggett/charts/claude-sandbox-controller \
  --namespace claude-system --create-namespace
```

or plain manifests:

```bash
kubectl apply -f https://github.com/waynehoggett/csc/releases/download/v0.1.0/install.yaml
```

## 2. Create Secrets

```bash
kubectl -n claude-system create secret generic prod-env-key \
  --from-literal=environment-key=sk-ant-oat01-...

kubectl -n claude-system create secret generic anthropic-org-key \
  --from-literal=api-key=sk-ant-api03-...
```

## 3. Apply a ClaudeEnvironment

```yaml
# alwayson.yaml
apiVersion: csc.io/v1alpha1
kind: ClaudeEnvironment
metadata:
  name: prod
  namespace: claude-system
spec:
  environmentId: env_abc123
  environmentKeySecretRef:
    name: prod-env-key
    key: environment-key
  mode: AlwaysOn
  workerImage: ghcr.io/waynehoggett/csc-worker:v0.1.0
  alwaysOn:
    minReplicas: 0
    maxReplicas: 5
    scaling:
      orgApiKeySecretRef:
        name: anthropic-org-key
        key: api-key
```

```bash
kubectl apply -f alwayson.yaml
kubectl -n claude-system get claudeenvironment prod -w
```

Create a session via the Anthropic API targeting `env_abc123`. KEDA should scale
the worker `Deployment` from 0 → 1 within ~30s:

```bash
kubectl -n claude-system get pods -l csc.io/environment=prod -w
```

## 4. Observe

```bash
kubectl -n claude-system describe claudeenvironment prod
kubectl -n claude-system get scaledobject,deploy,pods
kubectl -n claude-system logs -l csc.io/component=worker --tail=100
```
