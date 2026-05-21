# Architecture

CSC is a controller-manager that reconciles `ClaudeEnvironment` custom resources
into mode-specific trees of standard Kubernetes objects. It owns *lifecycle and
scaling*, not execution — the actual agent work runs inside Anthropic's worker
binaries (`ant` CLI / SDK helpers).

## Components

| Component                                     | AlwaysOn | Webhook |
| --------------------------------------------- | :------: | :-----: |
| `controller-manager`                          |    ✓     |    ✓    |
| Worker `Deployment`                           |    ✓     |    –    |
| KEDA `ScaledObject` + `TriggerAuthentication` |    ✓     |    –    |
| Webhook handler `Deployment` + `Service`      |    –     |    ✓    |
| Webhook handler `ServiceAccount`/`Role`/`RoleBinding` | – |    ✓    |
| Session `Job` (ephemeral)                     |    –     |    ✓    |
| Optional `Ingress`                            |    –     |    ✓    |

## AlwaysOn mode

The controller reconciles `mode: AlwaysOn` into:

- A `Deployment` of worker pods running `ant beta:worker poll`. Each pod claims
  sessions from the queue and runs tool calls in-process.
- A KEDA `ScaledObject` whose `metrics-api` trigger polls
  `/v1/environments/{id}/work/stats` for the queue `depth`.
- A `TriggerAuthentication` that supplies the **org API key** to KEDA as the
  `x-api-key` header. Workers never see this credential.

When KEDA is disabled (`--keda-enabled=false`), the worker `Deployment` is still
created and pinned to `minReplicas`; no autoscaling is configured.

> **KEDA header limitation.** The `metrics-api` scaler supplies the org key as a
> header but does not let CSC attach arbitrary static headers
> (`anthropic-version`, `anthropic-beta`). If your Anthropic endpoint requires
> those on the stats call, front it with a tiny proxy or use the future
> Prometheus-exporter path (roadmap v0.2). Tracked as a known limitation.

## Webhook mode

The controller reconciles `mode: Webhook` into:

- A webhook handler `Deployment` + `Service` (+ optional `Ingress`). The handler
  verifies the inbound signature, extracts the session id, and creates a `Job`.
- A tightly-scoped `ServiceAccount`/`Role`/`RoleBinding`: the handler may only
  create/manage Jobs in its namespace and `get` the one env-key Secret.
- A `ConfigMap` carrying the operator-supplied `jobTemplate`, mounted into the
  handler so per-session Jobs can be customized.
- Per `session.status_run_started` event: a `Job` running the worker image
  (`ant beta:worker run`) with `ANTHROPIC_SESSION_ID`, `ANTHROPIC_WORK_ID`,
  `ANTHROPIC_ENVIRONMENT_KEY`, `ANTHROPIC_ENVIRONMENT_ID`, and
  `ANTHROPIC_BASE_URL` injected. Outputs land at `/mnt/session/outputs`.

KEDA is not required — the system scales to zero between sessions.

The handler is **control-plane**: it only dispatches and creates Jobs; it never
executes agent code. A bug there cannot exfiltrate `/workspace` data.

## Reconciliation

`Reconcile` validates the spec (mode block present, referenced Secrets exist),
dispatches to the mode-specific path, and records `Ready`/`Degraded` conditions
plus observed names and replica counts in `status`. Configuration errors (e.g. a
missing Secret) requeue on a 30s timer rather than hot-looping.

All created objects carry an owner reference back to the `ClaudeEnvironment`, so
deleting the CR garbage-collects its entire tree.

## Implementation notes

- KEDA objects (`ScaledObject`, `TriggerAuthentication`) are managed as
  `unstructured.Unstructured` to avoid taking a hard dependency on the KEDA Go
  module and its transitive version pins.
- The webhook handler talks to Anthropic's documented Work API over HTTP and
  performs Svix-style signature verification; the heavy lifting (claim + run) is
  delegated to the worker binary inside each session Job.
