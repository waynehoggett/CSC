# ClaudeEnvironment reference

`apiVersion: csc.io/v1alpha1`, `kind: ClaudeEnvironment` (namespaced).
Short names: `cenv`, `cenvs`.

## Spec

| Field | Type | Required | Default | Description |
| ----- | ---- | :------: | ------- | ----------- |
| `environmentId` | string | ✓ | — | Anthropic environment id (e.g. `env_abc123`). |
| `environmentKeySecretRef` | SecretKeySelector | ✓ | — | Env key (data-plane). Mounted into workers/Jobs. |
| `mode` | enum | ✓ | — | `AlwaysOn` or `Webhook`. |
| `workerImage` | string | ✓ | — | Worker image. Must contain `/bin/bash`. |
| `baseURL` | string | | `https://api.anthropic.com` | Anthropic API base URL. |
| `alwaysOn` | AlwaysOnSpec | when `mode=AlwaysOn` | — | AlwaysOn configuration. |
| `webhook` | WebhookSpec | when `mode=Webhook` | — | Webhook configuration. |

### SecretKeySelector

| Field | Type | Description |
| ----- | ---- | ----------- |
| `name` | string | Secret name (same namespace as the CR). |
| `key` | string | Key within the Secret. |

### AlwaysOnSpec

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `minReplicas` | int32 | `0` | KEDA floor (0 = scale to zero). |
| `maxReplicas` | int32 | `10` | KEDA ceiling. |
| `workdir` | string | `/workspace` | Worker working directory. |
| `podTemplate` | PodTemplateSpec | — | Base pod template; worker container/env are overlaid. |
| `scaling.orgApiKeySecretRef` | SecretKeySelector | — | Org API key for `/work/stats` (control-plane). |
| `scaling.targetQueueDepth` | int32 | `1` | Target queue depth per worker. |

### WebhookSpec

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `signingKeySecretRef` | SecretKeySelector | — | Webhook signing key (`whsec_…`). |
| `handlerReplicas` | int32 | `1` | Handler replicas. |
| `handlerImage` | string | controller default | Override the handler image. |
| `ingress.enabled` | bool | `false` | Create an Ingress. |
| `ingress.host` | string | — | Ingress host. |
| `ingress.ingressClassName` | string | — | IngressClass. |
| `ingress.annotations` | map | — | Extra Ingress annotations. |
| `jobTemplate` | PodTemplateSpec | — | Base pod template for session Jobs. |
| `ttlSecondsAfterFinished` | int32 | `600` | TTL on completed session Jobs. |

## Status

| Field | Description |
| ----- | ----------- |
| `conditions` | `Ready` / `Degraded` conditions. |
| `observedGeneration` | Last reconciled generation. |
| `workerDeploymentName` | Worker Deployment name (AlwaysOn). |
| `webhookServiceName` | Webhook Service name (Webhook). |
| `observedQueueDepth` | Last observed queue depth. |
| `workersPolling` | Ready worker replicas (AlwaysOn). |
| `lastWebhookReceivedAt` | Last handled webhook timestamp. |

## Controller flags

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--metrics-bind-address` | `:8080` | Metrics endpoint. |
| `--health-probe-bind-address` | `:8081` | Health/readiness endpoint. |
| `--leader-elect` | `false` | Enable leader election. |
| `--keda-enabled` | `true` | Reconcile KEDA objects for AlwaysOn. |
| `--webhook-handler-image` | `ghcr.io/waynehoggett/csc-webhook-handler:latest` | Default handler image. |

## Anthropic header constants

Pinned in `internal/naming`:

- `anthropic-version: 2023-06-01`
- `anthropic-beta: managed-agents-2026-04-01`

Roll the beta header forward in one place (`naming.AnthropicBetaHeader`).
