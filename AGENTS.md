# Agent Instructions

## Critical Rules

1. Read `llms-full.txt` before writing code or suggesting changes — it has the complete source reference.
2. The gateway is stdlib-only Go. Do not add external dependencies.
3. Never put `pod=~"..."` inside `{}` stream selectors — pod is structured metadata, use `| pod=~"..."` after the selector.
4. Never expose secrets in code or docs.
5. `make devenv` / `tilt up` handles the dev loop — don't suggest manual kubectl steps.

## Project: kargo-loki-gateway

Plain-text log proxy between [Kargo](https://kargo.akuity.io) and Loki.
Go 1.26, stdlib only, deployed to the `kargo` namespace.

## Quick Start

```bash
make devenv    # create kind cluster + tilt up
make test      # unit tests (17 tests, no cluster needed)
make e2e-infra # deploy kind infra
make e2e       # Chainsaw e2e tests
```

## Source layout

| Path | Role |
|---|---|
| `src/config.go` | Env config |
| `src/k8s.go` | AnalysisRun startedAt lookup |
| `src/loki.go` | Loki query, time window, auth headers |
| `src/handler.go` | HTTP handler, LogQL builder, input validation |
| `src/main.go` | Server wiring |
| `src/main_test.go` | Unit tests |
| `charts/kargo-loki-gateway/` | Helm chart |
| `hack/e2e-infra/` | kind e2e infra (Loki, Alloy, Argo Rollouts) |
| `test/e2e/` | Chainsaw test suites |

## Key design decisions

- Pod name is **structured metadata** in Loki (Alloy k8s-monitoring) — filter with `| pod=~"..."`, never inside `{}`
- Time window anchored to `AnalysisRun.status.startedAt` — works regardless of run age
- On error/empty: return human-readable plain-text body (Kargo displays it as log content)
- Auth: `LOKI_USERNAME`/`LOKI_PASSWORD` (basic) or `LOKI_BEARER_TOKEN` (bearer); `LOKI_TENANT_ID` for X-Scope-OrgID

## Don'ts

- Don't add external Go dependencies — everything is stdlib
- Don't use `pod=~` inside `{}` stream selectors
- Don't put business logic in `main.go` — it wires only
- Don't manually edit `llms.txt`, `llms-full.txt`, `AGENTS.md` — keep them in sync with code changes
