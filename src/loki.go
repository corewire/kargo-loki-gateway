package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type lokiResp struct {
	Data struct {
		Result []struct {
			Values [][]string `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func (a *app) lokiWindow(ctx context.Context, ns, analysisRun string) (start, end int64) {
	if analysisRun != "" {
		if t := a.analysisRunStartedAt(ctx, ns, analysisRun); !t.IsZero() {
			return t.Add(-time.Minute).UnixNano(),
				t.Add(a.cfg.logWindow).UnixNano()
		}
	}
	// Fallback: AnalysisRun GC'd, not provided, or lookup failed.
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
	if a.cfg.lokiUsername != "" {
		req.SetBasicAuth(a.cfg.lokiUsername, a.cfg.lokiPassword)
	} else if a.cfg.lokiBearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.lokiBearerToken)
	}
	if a.cfg.lokiTenantID != "" {
		req.Header.Set("X-Scope-OrgID", a.cfg.lokiTenantID)
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
