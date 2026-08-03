package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// buildQuery constructs a LogQL query. Pod name is Alloy structured metadata,
// not a stream label, so it must be filtered after the selector with | pod=~.
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
	logql := buildQuery(ns, pod, container)
	out, err := a.queryLoki(ctx, logql, start, end)
	if err != nil {
		slog.Error("loki query failed", "err", err, "logql", logql)
		// Return a human-readable message so Kargo UI shows something useful
		// instead of a blank log pane or raw HTTP error.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w,
			"[kargo-loki-gateway] Failed to query Loki: %s\n\n"+
				"LogQL that was attempted:\n  %s\n\n"+
				"Try searching in Grafana Explore (Loki datasource) with this query\n"+
				"and the time range: %s to %s",
			err,
			logql,
			time.Unix(0, start).UTC().Format(time.RFC3339),
			time.Unix(0, end).UTC().Format(time.RFC3339),
		)
		return
	}
	if out == "" {
		// No logs yet or none matched — give the operator a LogQL they can paste
		// into Grafana Explore to investigate.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w,
			"[kargo-loki-gateway] No logs found in Loki.\n\n"+
				"LogQL query used:\n  %s\n\n"+
				"Time range: %s to %s\n\n"+
				"Possible causes:\n"+
				"  - Alloy has not ingested the logs yet (wait a few seconds and reload)\n"+
				"  - The pod name or namespace does not match what Alloy collected\n"+
				"  - The AnalysisRun window did not cover when the job ran\n\n"+
				"To search manually: open Grafana → Explore → Loki → paste the query above.",
			logql,
			time.Unix(0, start).UTC().Format(time.RFC3339),
			time.Unix(0, end).UTC().Format(time.RFC3339),
		)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, out)
}
