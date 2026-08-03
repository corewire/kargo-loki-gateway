package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	lokiURL        string
	listen         string
	logWindow      time.Duration
	fallbackWindow time.Duration
	limit          int
	lokiTimeout    time.Duration
	k8sTimeout     time.Duration
	k8sAPI         string
	// Loki auth — at most one of username/password or bearerToken should be set.
	lokiUsername    string
	lokiPassword    string
	lokiBearerToken string
	lokiTenantID    string
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
