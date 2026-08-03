package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	//+docs:config env="LOKI_URL" default="http://loki-gateway.loki.svc.cluster.local" meaning="Loki base URL"
	lokiURL string
	//+docs:config env="LISTEN_ADDR" default=":8080" meaning="Listen address"
	listen string
	//+docs:config env="LOG_WINDOW" default="30m" meaning="Window after AnalysisRun.startedAt"
	logWindow time.Duration
	//+docs:config env="FALLBACK_WINDOW" default="24h" meaning="Window when AnalysisRun is missing or GC'd"
	fallbackWindow time.Duration
	//+docs:config env="LIMIT" default="5000" meaning="Loki result line limit"
	limit int
	//+docs:config env="LOKI_TIMEOUT" default="15s" meaning="Per-request Loki timeout"
	lokiTimeout time.Duration
	//+docs:config env="K8S_TIMEOUT" default="5s" meaning="k8s API lookup timeout (separate from Loki)"
	k8sTimeout time.Duration
	//+docs:config env="K8S_API" default="https://kubernetes.default.svc" meaning="k8s API server (in-cluster default)"
	k8sAPI string
	// Loki auth — at most one of username/password or bearerToken should be set.
	//+docs:config env="LOKI_USERNAME" default="" meaning="Basic auth username (e.g. Grafana Cloud user ID)"
	lokiUsername string
	//+docs:config env="LOKI_PASSWORD" default="" meaning="Basic auth password / API key"
	lokiPassword string
	//+docs:config env="LOKI_BEARER_TOKEN" default="" meaning="Bearer token (e.g. Loki behind an auth proxy)"
	lokiBearerToken string
	//+docs:config env="LOKI_TENANT_ID" default="" meaning="X-Scope-OrgID header for multi-tenant Loki"
	lokiTenantID string
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
		lokiURL:         strings.TrimRight(lokiURL, "/"),
		listen:          listen,
		logWindow:       envDur("LOG_WINDOW", 30*time.Minute),
		fallbackWindow:  envDur("FALLBACK_WINDOW", 24*time.Hour),
		limit:           limit,
		lokiTimeout:     envDur("LOKI_TIMEOUT", 15*time.Second),
		k8sTimeout:      envDur("K8S_TIMEOUT", 5*time.Second),
		k8sAPI:          strings.TrimRight(k8sAPI, "/"),
		lokiUsername:    os.Getenv("LOKI_USERNAME"),
		lokiPassword:    os.Getenv("LOKI_PASSWORD"),
		lokiBearerToken: os.Getenv("LOKI_BEARER_TOKEN"),
		lokiTenantID:    os.Getenv("LOKI_TENANT_ID"),
	}
}
