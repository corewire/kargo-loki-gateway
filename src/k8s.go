package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	tokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	caFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

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
		a.cfg.k8sAPI, url.PathEscape(ns), url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return time.Time{}
	}
	req.Header.Set("Authorization", "Bearer "+readToken())
	resp, err := a.k8s.Do(req)
	if err != nil {
		slog.Warn("k8s AnalysisRun lookup failed", "ns", ns, "name", name, "err", err)
		return time.Time{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("k8s AnalysisRun not found", "ns", ns, "name", name, "status", resp.StatusCode)
		return time.Time{}
	}
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
