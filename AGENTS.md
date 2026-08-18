# Agent Instructions

## Critical Rules

1. Read `llms-full.txt` before writing code or suggesting changes.
2. stdlib-only Go — never add external dependencies
3. pod name is Alloy structured metadata — use "| pod=~\"...\"" after the selector, never inside {}
4. Never expose secrets in code or docs.
5. `make devenv` / `tilt up` handles the dev loop — don't suggest manual kubectl steps.
6. do not manually edit llms.txt, llms-full.txt, AGENTS.md — run make docs-gen

## Project: kargo-loki-gateway

HTTP gateway that bridges Kargo's log viewer and Loki.
Go 1.26.6, stdlib only, deployed to the `kargo` namespace.

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

## Don'ts

- stdlib-only Go — never add external dependencies
- pod name is Alloy structured metadata — use "| pod=~\"...\"" after the selector, never inside {}
- do not put business logic in main.go — it wires only
- LOKI_USERNAME/LOKI_PASSWORD (basic) or LOKI_BEARER_TOKEN (bearer); LOKI_TENANT_ID is independent
- do not manually edit llms.txt, llms-full.txt, AGENTS.md — run make docs-gen
