# kargo-loki-gateway

HTTP gateway that lets the [Kargo](https://kargo.akuity.io) UI stream
AnalysisRun (smoke-test Job) logs from Loki.

## Why it exists

Kargo's log viewer makes a single `GET` against `api.rollouts.logs.urlTemplate`
and streams the response body verbatim as plain text. It never parses JSON.

Loki's `query_range` only returns a JSON envelope — even a perfectly matching
query renders raw JSON in the UI, not log lines. A gateway is required to
flatten it.

A second problem exists on clusters using **Grafana Alloy / k8s-monitoring**:
Alloy keeps stream-label cardinality low and stores the pod name as
*structured metadata*, not a stream label. Putting `pod=~"..."` inside `{}`
matches nothing; the filter must come after the selector: `| pod=~"..."`.

## What it does

```
Kargo UI  ──GET /logs?namespace=…&pod=…&analysisRun=…──▶  gateway  ──▶  Loki
```

1. Looks up the `AnalysisRun` via the in-cluster k8s API and reads
   `.status.startedAt` to build a precise `[startedAt−1m, startedAt+30m]`
   time window. Falls back to `[now−24h, now]` if the run is GC'd.
2. Queries Loki `query_range` with a LogQL that uses `| pod=~"<jobName>.*"`
   (structured metadata filter, not a stream label).
3. Sorts all log entries by nanosecond timestamp and returns them as
   newline-joined plain text.
4. On any error or empty result returns a human-readable body with the exact
   LogQL and time range so the operator can paste it into Grafana Explore.

## Configuration

| Env | Default | Meaning |
|---|---|---|
| `LOKI_URL` | `http://loki-gateway.loki.svc.cluster.local` | Loki base URL |
| `LISTEN_ADDR` | `:8080` | Listen address |
| `LOG_WINDOW` | `30m` | Window after `AnalysisRun.startedAt` |
| `FALLBACK_WINDOW` | `24h` | Window when AnalysisRun is missing |
| `LIMIT` | `5000` | Loki result line limit |
| `LOKI_TIMEOUT` | `15s` | Per-request Loki timeout |
| `K8S_TIMEOUT` | `5s` | k8s API lookup timeout |
| `LOKI_BASIC_AUTH` | — | `user:password` for basic auth (e.g. Grafana Cloud) |
| `LOKI_BEARER_TOKEN` | — | Bearer token (e.g. Loki behind an auth proxy) |
| `LOKI_TENANT_ID` | — | `X-Scope-OrgID` header for multi-tenant Loki |

## Development

```bash
make devenv    # create kind cluster + tilt up (live reload on src/ changes)
make test      # unit tests
make e2e       # Chainsaw e2e tests (requires kind cluster + make e2e-infra)
make helm-lint # lint the Helm chart
```

Releases are cut via the [Cut Release](.github/workflows/cut-release.yml)
workflow. Each tag triggers the
[Release](.github/workflows/release.yml) workflow which builds a
multi-arch image, signs it with cosign, and pushes the Helm chart to
`oci://ghcr.io/corewire/charts`.
