# Getting started — Webhook mode

Webhook mode runs a small handler that wakes on `session.status_run_started`,
verifies the signature, and creates one `Job` per session. It scales to zero
between sessions and does not require KEDA.

## Prerequisites

- Kubernetes v1.28+ and `kubectl`.
- An Ingress controller (or another way to expose the handler `Service`).
- An Anthropic environment with a webhook configured.

## 1. Install CSC

```bash
helm install csc oci://ghcr.io/waynehoggett/charts/claude-sandbox-controller \
  --namespace claude-system --create-namespace
```

(For Webhook-only use you can disable KEDA reconciliation: `--set keda.enabled=false`.)

## 2. Create Secrets

```bash
kubectl -n claude-system create secret generic prod-env-key \
  --from-literal=environment-key=sk-ant-oat01-...

kubectl -n claude-system create secret generic prod-webhook-signing \
  --from-literal=signing-key=whsec_...
```

## 3. Apply a ClaudeEnvironment

```yaml
# webhook.yaml
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
  mode: Webhook
  workerImage: ghcr.io/waynehoggett/csc-worker:v0.1.0
  webhook:
    signingKeySecretRef:
      name: prod-webhook-signing
      key: signing-key
    ingress:
      enabled: true
      host: claude-webhook.example.com
    ttlSecondsAfterFinished: 600
```

```bash
kubectl apply -f webhook.yaml
```

Point the webhook in the Anthropic Console at your Ingress URL. The handler
verifies the `webhook-id` / `webhook-timestamp` / `webhook-signature` headers
against the signing key before acting.

## 4. Observe

```bash
kubectl -n claude-system get deploy,svc,ingress -l csc.io/component=webhook-handler
kubectl -n claude-system get jobs,pods -l csc.io/component=session
kubectl -n claude-system logs -l csc.io/component=webhook-handler --tail=100
```

A `Job` named `session-<id>` should appear within seconds of a session starting.
Repeat deliveries of the same event are idempotent — the Job name is derived
from the session id.

## TLS

Webhook TLS is not automated in v1. Terminate TLS at your Ingress (e.g. point
the Ingress at cert-manager) — set `webhook.ingress.annotations` and
`ingressClassName` as needed for your controller.
