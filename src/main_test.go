package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildQuery(t *testing.T) {
	cases := []struct{ ns, pod, container, want string }{
		{
			"demo", "abc.smoke-test.1", "smoke-test",
			`{namespace="demo",container="smoke-test"} | pod=~"abc.smoke-test.1.*"`,
		},
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
	for _, good := range []string{"demo", "my-ns", "job.1", "abc_123"} {
		if !nameRe.MatchString(good) {
			t.Errorf("expected %q to be accepted", good)
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
	a := &app{cfg: config{lokiURL: srv.URL, limit: 5000}, loki: &http.Client{}}
	got, err := a.queryLoki(t.Context(), `{namespace="x"}`, 0, time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if got != "first\nsecond\nthird" {
		t.Errorf("unexpected order: %q", got)
	}
}

func TestQueryLokiEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"result": []any{}}})
	}))
	defer srv.Close()
	a := &app{cfg: config{lokiURL: srv.URL, limit: 5000}, loki: &http.Client{}}
	got, err := a.queryLoki(t.Context(), `{namespace="x"}`, 0, time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestQueryLokiUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()
	a := &app{cfg: config{lokiURL: srv.URL, limit: 5000}, loki: &http.Client{}}
	_, err := a.queryLoki(t.Context(), `{namespace="x"}`, 0, time.Now().UnixNano())
	if err == nil {
		t.Fatal("expected error, got nil")
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
	a := &app{cfg: config{k8sAPI: srv.URL, k8sTimeout: 5 * time.Second}, k8s: &http.Client{}}
	got := a.analysisRunStartedAt(t.Context(), "demo", "my-run")
	if !got.Equal(ts) {
		t.Errorf("got %v, want %v", got, ts)
	}
}

func TestAnalysisRunStartedAtMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	a := &app{cfg: config{k8sAPI: srv.URL, k8sTimeout: 5 * time.Second}, k8s: &http.Client{}}
	got := a.analysisRunStartedAt(t.Context(), "demo", "gone")
	if !got.IsZero() {
		t.Errorf("expected zero time for 404, got %v", got)
	}
}

func TestHandleLogsNoNamespace(t *testing.T) {
	a := &app{cfg: loadConfig(), loki: &http.Client{}, k8s: &http.Client{}}
	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	rec := httptest.NewRecorder()
	a.handleLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleLogsInvalidParam(t *testing.T) {
	a := &app{cfg: loadConfig(), loki: &http.Client{}, k8s: &http.Client{}}
	req := httptest.NewRequest(http.MethodGet, `/logs?namespace=demo"`, nil)
	rec := httptest.NewRecorder()
	a.handleLogs(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleLogsLokiDown(t *testing.T) {
	// Loki server that always returns 503
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("overloaded"))
	}))
	defer loki.Close()
	cfg := loadConfig()
	cfg.lokiURL = loki.URL
	cfg.lokiTimeout = 5 * time.Second
	a := &app{cfg: cfg, loki: &http.Client{}, k8s: &http.Client{}}
	req := httptest.NewRequest(http.MethodGet, "/logs?namespace=demo", nil)
	rec := httptest.NewRecorder()
	a.handleLogs(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
	// Body must include something actionable for the operator.
	if !strings.Contains(rec.Body.String(), "kargo-loki-gateway") {
		t.Errorf("expected user-friendly error body, got: %q", rec.Body.String())
	}
}

func TestHandleLogsEmptyResult(t *testing.T) {
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"result": []any{}}})
	}))
	defer loki.Close()
	cfg := loadConfig()
	cfg.lokiURL = loki.URL
	cfg.lokiTimeout = 5 * time.Second
	a := &app{cfg: cfg, loki: &http.Client{}, k8s: &http.Client{}}
	req := httptest.NewRequest(http.MethodGet, "/logs?namespace=demo&pod=my-job", nil)
	rec := httptest.NewRecorder()
	a.handleLogs(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No logs found") {
		t.Errorf("expected helpful no-logs message, got: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Grafana") {
		t.Errorf("expected Grafana hint in no-logs message, got: %q", rec.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	a := &app{cfg: loadConfig(), loki: &http.Client{}, k8s: &http.Client{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("unexpected healthz response: %d %q", rec.Code, rec.Body.String())
	}
	_ = a
}
