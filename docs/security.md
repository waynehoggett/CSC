# Security model

## Credential tiers

CSC keeps two distinct credentials pinned to two tiers and never lets them mix.

| Tier          | Secret                        | Used by               | Scope                                    |
| ------------- | ----------------------------- | --------------------- | ---------------------------------------- |
| Control plane | `ANTHROPIC_API_KEY` (org key) | controller, KEDA      | Full org — create agents, sessions, envs |
| Data plane    | `ANTHROPIC_ENVIRONMENT_KEY`   | workers, session Jobs | One environment — claim work, post results |

Anthropic's docs are explicit:

> These endpoints authenticate with your organization API key, not the
> environment key. Call them from outside the worker host. Setting
> `ANTHROPIC_API_KEY` on the worker host exposes an organization-scoped
> credential to agent tool calls.

So `/work/stats` (used by KEDA) gets the org key via `TriggerAuthentication`;
worker and session pods only ever receive the environment key. The webhook
signing key (`whsec_…`) is mounted into the webhook handler only.

CSC enforces this in code: `dataPlaneEnv` builds the worker/Job environment and
never includes `ANTHROPIC_API_KEY`; unit tests assert the org key cannot leak
into a worker or session pod.

## Pod hardening

- Worker and session pods set `automountServiceAccountToken: false` — their only
  credential is the environment key.
- The webhook handler runs with a dedicated ServiceAccount whose Role allows
  only: create/get/list/watch/delete `jobs` in its namespace, and `get` on the
  single env-key Secret (scoped via `resourceNames`).
- The controller-manager image is distroless/nonroot with a read-only root
  filesystem and all capabilities dropped.

## Network egress

Workers need outbound access to `api.anthropic.com` (or your configured
`baseURL`) only. A starting NetworkPolicy:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: csc-worker-egress
  namespace: claude-system
spec:
  podSelector:
    matchLabels:
      csc.io/component: worker
  policyTypes: ["Egress"]
  egress:
    - to: []
      ports:
        - protocol: TCP
          port: 443
    # DNS
    - to: []
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
```

Tighten `to:` with an IP/CIDR or an egress gateway once you know the resolved
range for your `baseURL`. Apply the same selector pattern with
`csc.io/component: session` for Webhook-mode Jobs.

## Untrusted agent code

Skill code is downloaded at session start and executes arbitrary user-provided
code inside the pod. For stronger isolation, run worker/session pods under
gVisor or Kata via a `RuntimeClass` (set it in `podTemplate`/`jobTemplate`).
CSC documents this but does not enforce it.
