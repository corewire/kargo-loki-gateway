package main

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

type app struct {
	cfg  config
	loki *http.Client // plain HTTP to in-cluster Loki
	k8s  *http.Client // TLS client for the k8s apiserver
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
