# Kargo AnalysisRun Logs via Loki (deimos)

How the Kargo UI streams smoke-test (verification) Job logs from Loki, why a
gateway is required, and the exact configuration.

## TL;DR

- Kargo's log viewer does **one HTTP GET** against an operator-defined
  `urlTemplate` and **streams the response body verbatim** (as SSE). It does
  **not** parse Loki JSON and has **no** native kubelet/apiserver log reader.
- Loki (fed by Grafana Alloy / k8s-monitoring) **has no `pod` stream label** —
  the pod name is **structured metadata**, so it must be matched with
  `| pod=~"..."` after the selector, not inside `{}`.
- Therefore a small **gateway** (`kargo-loki-gateway`, OpenResty + Lua) sits
  between Kargo and Loki: it queries Loki `query_range` and flattens the JSON
  into plain-text, time-sorted log lines.

## Problem

Configuring Kargo's `api.rollouts.logs.urlTemplate` to point **directly** at
Loki produced this in the Kargo UI / AnalysisRun logs:

```json
{"status":"success","data":{"resultType":"streams","result":[], ... }}
```

i.e. the raw Loki JSON envelope with an **empty `result`**. Two independent root
causes:

### Root cause 1 — wrong label semantics (empty `result`)

The original template used:

```
{namespace="${{ jobNamespace }}",pod=~"${{ jobName }}.*"}
```

On this cluster logs are collected by **Grafana Alloy (k8s-monitoring)**, which
keeps stream-label cardinality low. Verified label set:

```
__stream_shard__, app_kubernetes_io_name, cluster, container, instance, job,
k8s_cluster_name, level, namespace, node, reason, service_name,
service_namespace, source, unit
```

There is **no `pod` stream label**. The pod name is stored as **structured
metadata**, which in LogQL must be filtered **after** the stream selector:

```logql
{namespace="demo"} | pod=~"<jobName>.*"
```

Putting `pod=~` inside `{}` matches nothing → `result: []`.

### Root cause 2 — Kargo needs plain text, Loki returns JSON

From `akuity/kargo` `pkg/server/get_analysisrun_logs.go`: the API server builds
an HTTP GET, then **streams the response body byte-for-byte** as Server-Sent
Events (splits on `\n`, prefixes each line with `data: `). It never parses the
body. Loki's `query_range` only ever returns a JSON envelope, so even a
**matching** query would render the JSON blob, not log lines.

The Kargo docs state this is by design ("lowest common denominator"): the
operator must forward/store logs somewhere retrievable via HTTP GET and adapt
the format. There is **no official/community Loki gateway**; the Akuity SaaS
handles it internally.

## Key facts (verified)

### Kargo template variables

`urlTemplate` supports `${{ ... }}` with: `project`, `namespace` (= project
name), `shard`, `stage`, `analysisRun`, `metricName`, `jobNamespace`, `jobName`,
`container`.

`jobName` / `jobNamespace` come from the AnalysisRun status:

```
analysisRun.status.metricResults[].measurements[0].metadata["job-name"]
analysisRun.status.metricResults[].measurements[0].metadata["job-namespace"]
```

`jobName` is the **k8s Job** name (e.g.
`91c3c8eb-a691-45da-9e5b-21553e639315.smoke-test.1`); the pod is
`<jobName>-<hash>` (e.g. `...smoke-test.1-jfhx7`). So `pod=~"<jobName>.*"` is the
correct pod match.

### AnalysisRun ownership (why Argo Rollouts UI can't show these)

Kargo's AnalysisRuns are **standalone** — owned by a Kargo `Freight`, not a
`Rollout` (there are **zero** Rollout objects in the cluster). The Argo Rollouts
dashboard is Rollout-centric and only renders AnalysisRuns as children of a
Rollout, so these never appear there. Kargo's own UI is the display surface.

### Why Loki and not the kubelet/apiserver

The kube-apiserver pod-log endpoint
(`GET /api/v1/namespaces/{ns}/pods/{pod}/log`) returns **plain text** (no
gateway needed) but only while the **pod still exists**. Rollouts smoke-test Job
pods are short-lived and GC'd (and `reset-demo` deletes the AnalysisRuns), so
their logs vanish. Loki **persists** them for later review. It would also only
give a `jobName` (Job), not the pod name the apiserver endpoint requires, so a
proxy would still be needed.

## Solution: `kargo-loki-gateway` (Go)

See the spec below. Deployed to the `kargo` namespace; the current live
implementation is OpenResty/Lua (see git history) pending the Go migration.

---

# Go implementation spec (`kargo-loki-gateway`)

Looks up the AnalysisRun's `startedAt` from the k8s API and queries Loki over
`[startedAt−1m, startedAt+30m]` — a **precise** window anchored to when that
particular run happened. Works regardless of how old the run is.

Lives in its own build repo (e.g. `corewire/k8s/tools/kargo-loki-gateway`); the
k8s manifests stay in this GitOps repo and only the image changes.

## Time window: the key design decision

| Approach | What it uses | Problem |
|---|---|---|
| `[now−6h, now]` (Lua / naive) | current time | misses runs older than 6h |
| `[now−7d, now]` (wide fixed) | current time | correct but scans a lot of Loki data |
| **`[startedAt−1m, startedAt+30m]`** (Go) | AnalysisRun `.status.startedAt` | precise; works regardless of age |

The Go gateway accepts `analysisRun=<name>` and `namespace=<ns>` (both already
in the urlTemplate), looks up the AnalysisRun via the in-cluster k8s API, reads
`.status.startedAt`, and builds a 31-minute Loki window around that instant.
Falls back to `[now−24h, now]` if the AnalysisRun no longer exists (GC'd) or
has no `startedAt`.

## HTTP contract

| Method / path | Query params | Success | Errors |
|---|---|---|---|
| `GET /logs` | `namespace` (required), `pod`, `container`, `analysisRun` | `200 text/plain`, newline-joined log lines (time-ascending) | `400` invalid/missing param; `502` upstream error |
| `GET /healthz` | — | `200 text/plain` `ok` | — |

Validation: every non-empty param must match `^[A-Za-z0-9_.-]+$`.

LogQL built as: `{namespace="<ns>"[,container="<c>"]} | pod=~"<pod>.*"`
(pod is structured metadata — post-pipe filter, not a stream label).

## `urlTemplate` change in `app.yaml`

Add `&analysisRun=${{ analysisRun }}` to pass the run name:

```yaml
urlTemplate: >-
  http://kargo-loki-gateway.kargo.svc.cluster.local/logs?namespace=${{ jobNamespace }}&pod=${{ jobName }}&container=${{ container }}&analysisRun=${{ analysisRun }}
```

## Configuration (env)

| Var | Default | Meaning |
|---|---|---|
| `LOKI_URL` | `http://loki-gateway.loki.svc.cluster.local` | Loki base URL |
| `LISTEN_ADDR` | `:8080` | listen address |
| `LOG_WINDOW` | `30m` | Loki window after AnalysisRun `startedAt` |
| `FALLBACK_WINDOW` | `24h` | window when AnalysisRun is missing/GC'd |
| `LIMIT` | `5000` | Loki result line limit |
| `LOKI_TIMEOUT` | `15s` | per-request Loki timeout |
| `K8S_TIMEOUT` | `5s` | k8s API lookup timeout (separate from Loki) |
| `K8S_API` | `https://kubernetes.default.svc` | k8s API server (in-cluster default) |

## `main.go` (reference)

```go
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type config struct {
	lokiURL        string
	listen         string
	logWindow      time.Duration
	fallbackWindow time.Duration
	limit          int
	lokiTimeout    time.Duration
	k8sTimeout     time.Duration
	k8sAPI         string
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func loadConfig() config {
	limit := 5000
	if v := os.Getenv("LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	lokiURL := os.Getenv("LOKI_URL")
	if lokiURL == "" {
		lokiURL = "http://loki-gateway.loki.svc.cluster.local"
	}
	listen := os.Getenv("LISTEN_ADDR")
	if listen == "" {
		listen = ":8080"
	}
	k8sAPI := os.Getenv("K8S_API")
	if k8sAPI == "" {
		k8sAPI = "https://kubernetes.default.svc"
	}
	return config{
		lokiURL:        strings.TrimRight(lokiURL, "/"),
		listen:         listen,
		logWindow:      envDur("LOG_WINDOW", 30*time.Minute),
		fallbackWindow: envDur("FALLBACK_WINDOW", 24*time.Hour),
		limit:          limit,
		lokiTimeout:    envDur("LOKI_TIMEOUT", 15*time.Second),
		k8sTimeout:     envDur("K8S_TIMEOUT", 5*time.Second),
		k8sAPI:         strings.TrimRight(k8sAPI, "/"),
	}
}

// buildQuery: pod name is Alloy structured metadata, not a stream label.
func buildQuery(ns, pod, container string) string {
	sel := fmt.Sprintf("namespace=%q", ns)
	if container != "" {
		sel += fmt.Sprintf(",container=%q", container)
	}
	q := "{" + sel + "}"
	if pod != "" {
		q += fmt.Sprintf(" | pod=~%q", pod+".*")
	}
	return q
}

type lokiResp struct {
	Data struct {
		Result []struct {
			Values [][]string `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

const (
	tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

type app struct {
	cfg     config
	loki    *http.Client // plain HTTP to in-cluster Loki
	k8s     *http.Client // mTLS client for the k8s apiserver
}

// readToken reads the projected SA token fresh on every call; tokens rotate ~hourly.
func readToken() string {
	b, _ := os.ReadFile(tokenFile)
	return strings.TrimSpace(string(b))
}

// analysisRunStartedAt looks up the AnalysisRun and returns its startedAt time.
// Returns zero time if not found or startedAt is unset.
func (a *app) analysisRunStartedAt(ctx context.Context, ns, name string) time.Time {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.k8sTimeout)
	defer cancel()
	u := fmt.Sprintf("%s/apis/argoproj.io/v1alpha1/namespaces/%s/analysisruns/%s",
		a.cfg.k8sAPI, ns, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return time.Time{}
	}
	req.Header.Set("Authorization", "Bearer "+readToken())
	resp, err := a.k8s.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return time.Time{}
	}
	defer resp.Body.Close()
	var ar struct {
		Status struct {
			StartedAt *time.Time `json:"startedAt"`
		} `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&ar); err != nil {
		return time.Time{}
	}
	if ar.Status.StartedAt == nil {
		return time.Time{}
	}
	return *ar.Status.StartedAt
}

func (a *app) lokiWindow(ctx context.Context, ns, analysisRun string) (start, end int64) {
	if analysisRun != "" {
		if t := a.analysisRunStartedAt(ctx, ns, analysisRun); !t.IsZero() {
			return t.Add(-time.Minute).UnixNano(),
				t.Add(a.cfg.logWindow).UnixNano()
		}
	}
	// Fallback: AnalysisRun GC'd or not provided.
	now := time.Now()
	return now.Add(-a.cfg.fallbackWindow).UnixNano(), now.UnixNano()
}

func (a *app) queryLoki(ctx context.Context, logql string, start, end int64) (string, error) {
	q := url.Values{}
	q.Set("query", logql)
	q.Set("start", strconv.FormatInt(start, 10))
	q.Set("end", strconv.FormatInt(end, 10))
	q.Set("limit", strconv.Itoa(a.cfg.limit))
	q.Set("direction", "forward")
	u := a.cfg.lokiURL + "/loki/api/v1/query_range?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := a.loki.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("loki %d: %s", resp.StatusCode, body)
	}
	// Guard against a runaway Loki response; 32 MB is generous for limit=5000 lines.
	var lr lokiResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&lr); err != nil {
		return "", err
	}
	type entry struct {
		ts   int64
		line string
	}
	var entries []entry
	for _, s := range lr.Data.Result {
		for _, v := range s.Values {
			if len(v) < 2 {
				continue
			}
			ts, _ := strconv.ParseInt(v[0], 10, 64)
			entries = append(entries, entry{ts, v[1]})
		}
	}
	// Stable sort: preserves original stream order for same-timestamp lines.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ts < entries[j].ts })
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = e.line
	}
	return strings.Join(lines, "\n"), nil
}

func (a *app) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ns := q.Get("namespace")
	pod := q.Get("pod")
	container := q.Get("container")
	analysisRun := q.Get("analysisRun")
	for _, v := range []string{ns, pod, container, analysisRun} {
		if v != "" && !nameRe.MatchString(v) {
			http.Error(w, "invalid parameter", http.StatusBadRequest)
			return
		}
	}
	if ns == "" {
		http.Error(w, "namespace is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.lokiTimeout)
	defer cancel()
	start, end := a.lokiWindow(ctx, ns, analysisRun)
	out, err := a.queryLoki(ctx, buildQuery(ns, pod, container), start, end)
	if err != nil {
		slog.Error("loki query failed", "err", err)
		http.Error(w, "error querying Loki", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, out)
}

func main() {
	cfg := loadConfig()

	// Build a TLS-aware client for the k8s apiserver using the mounted cluster CA.
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		slog.Error("cannot read cluster CA", "err", err)
		os.Exit(1)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)
	k8sClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}

	a := &app{
		cfg:  cfg,
		loki: &http.Client{},
		k8s:  k8sClient,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /logs", a.handleLogs)
	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("listening", "addr", cfg.listen, "loki", cfg.lokiURL, "k8s", cfg.k8sAPI)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
```

## `main_test.go`

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildQuery(t *testing.T) {
	cases := []struct{ ns, pod, container, want string }{
		{"demo", "abc.smoke-test.1", "smoke-test",
			`{namespace="demo",container="smoke-test"} | pod=~"abc.smoke-test.1.*"`},
		{"demo", "", "", `{namespace="demo"}`},
		{"demo", "job1", "", `{namespace="demo"} | pod=~"job1.*"`},
	}
	for _, c := range cases {
		if got := buildQuery(c.ns, c.pod, c.container); got != c.want {
			t.Errorf("buildQuery(%q,%q,%q)\n got  %q\n want %q", c.ns, c.pod, c.container, got, c.want)
		}
	}
}

func TestNameValidation(t *testing.T) {
	for _, b := range []string{`demo"}`, "ns space", "a|b", "x{y}"} {
		if nameRe.MatchString(b) {
			t.Errorf("expected %q to be rejected", b)
		}
	}
}

func TestQueryLokiSorted(t *testing.T) {
	// Loki returns two streams with out-of-order entries; output must be ascending.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"result": []any{
					map[string]any{"values": []any{[]any{"200", "second"}, []any{"100", "first"}}},
					map[string]any{"values": []any{[]any{"300", "third"}}},
				},
			},
		})
	}))
	defer srv.Close()
	a := &app{cfg: config{lokiURL: srv.URL, limit: 5000}, client: &http.Client{}}
	got, err := a.queryLoki(t.Context(), `{namespace="x"}`, 0, time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if got != "first\nsecond\nthird" {
		t.Errorf("unexpected order: %q", got)
	}
}

func TestAnalysisRunStartedAt(t *testing.T) {
	ts := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": map[string]any{"startedAt": ts.Format(time.RFC3339)},
		})
	}))
	defer srv.Close()
	a := &app{cfg: config{k8sAPI: srv.URL}, client: &http.Client{}}
	got := a.analysisRunStartedAt(t.Context(), "demo", "my-run")
	if !got.Equal(ts) {
		t.Errorf("got %v, want %v", got, ts)
	}
}
```

## RBAC (new — needed by the Go version only)

The gateway needs `get` on `analysisruns` across the namespaces Kargo uses. A
`ClusterRole` + `ClusterRoleBinding` is the simplest option since Kargo namespaces
vary per project:

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kargo-loki-gateway
  namespace: kargo
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kargo-loki-gateway
rules:
  - apiGroups: [argoproj.io]
    resources: [analysisruns]
    verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kargo-loki-gateway
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kargo-loki-gateway
subjects:
  - kind: ServiceAccount
    name: kargo-loki-gateway
    namespace: kargo
```

Add `serviceAccountName: kargo-loki-gateway` to the Deployment pod spec.

## `Dockerfile` (multi-stage; `final` target for the shared CI template)

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway .

FROM gcr.io/distroless/static:nonroot AS final
COPY --from=build /out/gateway /gateway
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gateway"]
```

## `.gitlab-ci.yml` (reuse the corewire shared template)

```yaml
include:
  - project: corewire/gitlab-ci/templates
    file: basic-docker.yaml

# basic-docker.yaml builds `--target final` on any push and pushes
#   ${CI_REGISTRY}/${CI_PROJECT_PATH}:<CI_PIPELINE_IID> (+ :latest on main).
go_test:
  stage: build_mr
  image: golang:1.23-alpine
  script:
    - go vet ./...
    - go test ./...
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
```

## Manifest changes in this repo

Edit `clusters/deimos/workload/kargo/extra-manifests/loki-log-gateway.yaml`:

- Add the `ServiceAccount`, `ClusterRole`, `ClusterRoleBinding` above.
- **Remove** the `ConfigMap` (`nginx.conf`) and its volume/volumeMount.
- **Swap the image** to the built Go image (pin a tag; let Renovate bump it).
- Drop the `/tmp` emptyDir — the static binary needs no writable FS.
- Add `serviceAccountName: kargo-loki-gateway` to the pod spec.
- Keep the `Service`, probes, `securityContext`, resources.

Update `app.yaml` urlTemplate to pass `analysisRun`:

```yaml
urlTemplate: >-
  http://kargo-loki-gateway.kargo.svc.cluster.local/logs?namespace=${{ jobNamespace }}&pod=${{ jobName }}&container=${{ container }}&analysisRun=${{ analysisRun }}
```

## Advantages over the interim OpenResty shim

- **Precise time window** anchored to `AnalysisRun.status.startedAt` — correct for any run age.
- Unit tests on query-building, validation, sort, and the k8s lookup.
- Typed Loki decoding + numeric int64 sort (no equal-length ns string assumption).
- Explicit per-request timeout, structured logs, easy `/metrics` later.
- ~10–20 MB distroless image.

## What it costs

- Build repo, Dockerfile, CI, registry, Renovate for base image + Go modules.
- RBAC (`ClusterRole` for `analysisruns/get`).

## Remaining weak spots / known limitations

| # | Issue | Severity | Fix |
|---|---|---|---|
| 1 | `limit=5000` silently truncates very chatty verifications | low | paginate with `after` param or raise limit |
| 2 | No graceful shutdown (`http.Server.Shutdown`) | negligible | add signal handler if uptime SLA matters |
| 3 | `lokiWindow` calls k8s API inside the main `lokiTimeout` context — leaves < `lokiTimeout − k8sTimeout` for the actual Loki call | low | already mitigated by separate `K8S_TIMEOUT`; make the two contexts independent if needed |
| 4 | k8s API URL is built by string formatting `ns`/`name` already validated by `nameRe`; safe, but not path-escaped | negligible | `url.PathEscape` for defence-in-depth |
| 5 | No `/metrics` endpoint | n/a | add `promhttp.Handler()` if Prometheus scraping is desired |

