// hack/gen-docs generates llms.txt, llms-full.txt, AGENTS.md, and knowledge.yaml
// from //+docs: annotations in source files and static project config.
//
// Usage:
//   go run ./hack/gen-docs/          # write files
//   go run ./hack/gen-docs/ --diff   # print unified diff without writing
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// ── project metadata (edit here when the project changes) ──────────────────

const (
	projectName        = "kargo-loki-gateway"
	projectDescription = "HTTP gateway that bridges Kargo's log viewer and Loki"
	projectModule      = "github.com/corewire/kargo-loki-gateway"
	projectLicense     = "MIT"
	deployNamespace    = "kargo"
)

var conventions = []string{
	"stdlib-only Go — never add external dependencies",
	`pod name is Alloy structured metadata — use "| pod=~\"...\"" after the selector, never inside {}`,
	"do not put business logic in main.go — it wires only",
	"LOKI_USERNAME/LOKI_PASSWORD (basic) or LOKI_BEARER_TOKEN (bearer); LOKI_TENANT_ID is independent",
	"do not manually edit llms.txt, llms-full.txt, AGENTS.md — run make docs-gen",
}

// ── data model ──────────────────────────────────────────────────────────────

type ConfigField struct {
	Env     string
	Default string
	Meaning string
}

type TestEntry struct {
	Name    string
	Meaning string
}

type MakeTarget struct {
	Name    string
	Meaning string
}

type Knowledge struct {
	GoVersion   string
	Module      string
	ConfigFields []ConfigField
	Tests        []TestEntry
	MakeTargets  []MakeTarget
}

// ── extraction ──────────────────────────────────────────────────────────────

var docsMarkerRe = regexp.MustCompile(`//\+docs:(\w+)\s+(.+)`)
var kvRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

func parseMarker(line string) (kind string, attrs map[string]string) {
	m := docsMarkerRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", nil
	}
	kind = m[1]
	attrs = map[string]string{}
	for _, kv := range kvRe.FindAllStringSubmatch(m[2], -1) {
		attrs[kv[1]] = kv[2]
	}
	return kind, attrs
}

func extractConfig(srcDir string) []ConfigField {
	var fields []ConfigField
	seen := map[string]bool{}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, srcDir, nil, parser.ParseComments)
	if err != nil {
		fatalf("parse %s: %v", srcDir, err)
	}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name.Name != "config" {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, f := range st.Fields.List {
						// Check doc comment (line above) for +docs:config.
						for _, cg := range []*ast.CommentGroup{f.Doc, f.Comment} {
							if cg == nil {
								continue
							}
							for _, c := range cg.List {
								kind, attrs := parseMarker(c.Text)
								if kind != "config" {
									continue
								}
								env := attrs["env"]
								if env == "" || seen[env] {
									continue
								}
								seen[env] = true
								fields = append(fields, ConfigField{
									Env:     env,
									Default: attrs["default"],
									Meaning: attrs["meaning"],
								})
							}
						}
					}
				}
			}
		}
	}
	// Preserve declaration order via line position.
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Env < fields[j].Env })
	// Re-sort by the canonical order: non-auth fields first, then auth.
	order := map[string]int{
		"LOKI_URL": 0, "LISTEN_ADDR": 1, "LOG_WINDOW": 2, "FALLBACK_WINDOW": 3,
		"LIMIT": 4, "LOKI_TIMEOUT": 5, "K8S_TIMEOUT": 6, "K8S_API": 7,
		"LOKI_USERNAME": 8, "LOKI_PASSWORD": 9, "LOKI_BEARER_TOKEN": 10, "LOKI_TENANT_ID": 11,
	}
	sort.SliceStable(fields, func(i, j int) bool {
		oi, ok1 := order[fields[i].Env]
		oj, ok2 := order[fields[j].Env]
		if ok1 && ok2 {
			return oi < oj
		}
		return ok1
	})
	return fields
}

func extractTests(srcDir string) []TestEntry {
	var tests []TestEntry
	fset := token.NewFileSet()
	pkgs, _ := parser.ParseDir(fset, srcDir, nil, parser.ParseComments)
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || !strings.HasPrefix(fd.Name.Name, "Test") {
					continue
				}
				meaning := ""
				if fd.Doc != nil {
					meaning = strings.TrimSpace(fd.Doc.Text())
				}
				tests = append(tests, TestEntry{Name: fd.Name.Name, Meaning: meaning})
			}
		}
	}
	return tests
}

func extractMakeTargets(makefilePath string) []MakeTarget {
	f, err := os.Open(makefilePath)
	if err != nil {
		return nil
	}
	defer f.Close()
	var targets []MakeTarget
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.Index(line, "## "); idx > 0 {
			parts := strings.SplitN(line, "## ", 2)
			name := strings.TrimSpace(strings.TrimSuffix(parts[0], ":"))
			name = strings.TrimPrefix(name, ".PHONY: ")
			if name != "" {
				targets = append(targets, MakeTarget{Name: name, Meaning: strings.TrimSpace(parts[1])})
			}
		}
	}
	return targets
}

func parseGoVersion(goModPath string) string {
	f, err := os.Open(goModPath)
	if err != nil {
		return "1.26"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "go ") {
			return strings.TrimPrefix(line, "go ")
		}
	}
	return "1.26"
}

// ── templates ───────────────────────────────────────────────────────────────

var llmsTxtTmpl = template.Must(template.New("llms").Parse(`# {{.Name}}

> {{.Description}} | Go {{.GoVersion}} | stdlib only | Deployed to the ` + "`" + `{{.Namespace}}` + "`" + ` namespace

## HTTP contract

| Method / path | Params | Success | Errors |
|---|---|---|---|
| ` + "`GET /logs`" + ` | ` + "`namespace`" + ` (required), ` + "`pod`" + `, ` + "`container`" + `, ` + "`analysisRun`" + ` | ` + "`200 text/plain`" + ` | ` + "`400`" + ` invalid param; ` + "`502`" + ` Loki error |
| ` + "`GET /healthz`" + ` | — | ` + "`200 text/plain \"ok\"`" + ` | — |

## Source layout

| File | Role |
|---|---|
| ` + "`src/config.go`" + ` | Env-var loading, ` + "`config`" + ` struct |
| ` + "`src/k8s.go`" + ` | AnalysisRun lookup, SA token handling |
| ` + "`src/loki.go`" + ` | Loki query, time window, auth headers |
| ` + "`src/handler.go`" + ` | HTTP handler, LogQL builder, input validation |
| ` + "`src/main.go`" + ` | ` + "`app`" + ` struct, server wiring |
| ` + "`src/main_test.go`" + ` | {{len .Tests}} unit tests |
| ` + "`charts/kargo-loki-gateway/`" + ` | Helm chart |
| ` + "`hack/e2e-infra/`" + ` | kind-based e2e infra (Loki, Alloy, Argo Rollouts, gateway) |
| ` + "`test/e2e/`" + ` | Chainsaw test suites |

## Configuration
{{template "configTable" .ConfigFields}}

## Full reference

See [llms-full.txt](llms-full.txt) for complete source descriptions, all tests, and deployment details.
`))

var llmsFullTxtTmpl = template.Must(template.New("full").Funcs(template.FuncMap{
	"join": strings.Join,
}).Parse(`# {{.Name}} — Full Reference for AI Agents

## Project

- **Name**: {{.Name}}
- **Description**: {{.Description}}
- **Language**: Go {{.GoVersion}}
- **Module**: {{.Module}}
- **Dependencies**: stdlib only
- **Image**: ` + "`ghcr.io/corewire/kargo-loki-gateway`" + `
- **Helm chart**: ` + "`oci://ghcr.io/corewire/charts/kargo-loki-gateway`" + `
- **Deployed namespace**: ` + "`" + `{{.Namespace}}` + "`" + `
- **License**: {{.License}}

## Architecture

` + "```" + `
Kargo UI  ──GET /logs?namespace&pod&container&analysisRun──▶  gateway  ──GET query_range──▶  Loki
                                                                  │
                                                         k8s API (AnalysisRun.status.startedAt)
` + "```" + `

## Key design decisions

- Pod name is **structured metadata** in Loki (Alloy k8s-monitoring) — filter with ` + "`| pod=~\"...\"`" + `, never inside ` + "`{}`" + `
- Time window anchored to ` + "`AnalysisRun.status.startedAt`" + ` — works regardless of run age
- On error/empty: return human-readable plain-text body (Kargo displays it as log content)
- Auth: ` + "`LOKI_USERNAME`" + `/` + "`LOKI_PASSWORD`" + ` (basic) or ` + "`LOKI_BEARER_TOKEN`" + ` (bearer); ` + "`LOKI_TENANT_ID`" + ` for X-Scope-OrgID

## Configuration
{{template "configTable" .ConfigFields}}

## Unit tests (src/main_test.go)

| Test | Description |
|---|---|
{{- range .Tests}}
| ` + "`{{.Name}}`" + ` | {{if .Meaning}}{{.Meaning}}{{else}}—{{end}} |
{{- end}}

## Build commands

| Target | Description |
|---|---|
{{- range .MakeTargets}}
| ` + "`make {{.Name}}`" + ` | {{.Meaning}} |
{{- end}}

## Conventions
{{range .Conventions}}
- {{.}}
{{- end}}
`))

var agentsMdTmpl = template.Must(template.New("agents").Parse(`# Agent Instructions

## Critical Rules

1. Read ` + "`llms-full.txt`" + ` before writing code or suggesting changes.
2. {{index .Conventions 0}}
3. {{index .Conventions 1}}
4. Never expose secrets in code or docs.
5. ` + "`make devenv`" + ` / ` + "`tilt up`" + ` handles the dev loop — don't suggest manual kubectl steps.
6. {{index .Conventions 4}}

## Project: {{.Name}}

{{.Description}}.
Go {{.GoVersion}}, stdlib only, deployed to the ` + "`" + `{{.Namespace}}` + "`" + ` namespace.

## Quick Start

` + "```bash" + `
make devenv    # create kind cluster + tilt up
make test      # unit tests ({{len .Tests}} tests, no cluster needed)
make e2e-infra # deploy kind infra
make e2e       # Chainsaw e2e tests
` + "```" + `

## Source layout

| Path | Role |
|---|---|
| ` + "`src/config.go`" + ` | Env config |
| ` + "`src/k8s.go`" + ` | AnalysisRun startedAt lookup |
| ` + "`src/loki.go`" + ` | Loki query, time window, auth headers |
| ` + "`src/handler.go`" + ` | HTTP handler, LogQL builder, input validation |
| ` + "`src/main.go`" + ` | Server wiring |
| ` + "`src/main_test.go`" + ` | Unit tests |
| ` + "`charts/kargo-loki-gateway/`" + ` | Helm chart |
| ` + "`hack/e2e-infra/`" + ` | kind e2e infra (Loki, Alloy, Argo Rollouts) |
| ` + "`test/e2e/`" + ` | Chainsaw test suites |

## Don'ts
{{range .Conventions}}
- {{.}}
{{- end}}
`))

var knowledgeYAMLTmpl = template.Must(template.New("yaml").Parse(`# Auto-generated by make docs-gen — do not edit manually.
project:
  name: {{.Name}}
  description: {{.Description | printf "%q"}}
  module: {{.Module}}
  go_version: {{.GoVersion | printf "%q"}}
  license: {{.License}}
  namespace: {{.Namespace}}
config:
{{- range .ConfigFields}}
  - env: {{.Env}}
    default: {{.Default | printf "%q"}}
    meaning: {{.Meaning | printf "%q"}}
{{- end}}
tests:
{{- range .Tests}}
  - name: {{.Name}}
    meaning: {{.Meaning | printf "%q"}}
{{- end}}
make_targets:
{{- range .MakeTargets}}
  - name: {{.Name}}
    meaning: {{.Meaning | printf "%q"}}
{{- end}}
conventions:
{{- range .Conventions}}
  - {{. | printf "%q"}}
{{- end}}
`))

// shared sub-template used by multiple templates
var configTableTmpl = `{{define "configTable"}}
| Env | Default | Meaning |
|---|---|---|
{{- range .}}
| ` + "`{{.Env}}`" + ` | {{if .Default}}` + "`{{.Default}}`" + `{{else}}—{{end}} | {{.Meaning}} |
{{- end}}
{{end}}`

func init() {
	for _, t := range []*template.Template{llmsTxtTmpl, llmsFullTxtTmpl, agentsMdTmpl} {
		template.Must(t.Parse(configTableTmpl))
	}
}

// ── render data ─────────────────────────────────────────────────────────────

type renderData struct {
	Name         string
	Description  string
	GoVersion    string
	Module       string
	License      string
	Namespace    string
	ConfigFields []ConfigField
	Tests        []TestEntry
	MakeTargets  []MakeTarget
	Conventions  []string
}

// ── output ──────────────────────────────────────────────────────────────────

type output struct {
	path string
	tmpl *template.Template
}

func outputs() []output {
	return []output{
		{"llms.txt", llmsTxtTmpl},
		{"llms-full.txt", llmsFullTxtTmpl},
		{"AGENTS.md", agentsMdTmpl},
		{"knowledge.yaml", knowledgeYAMLTmpl},
	}
}

// ── main ────────────────────────────────────────────────────────────────────

func main() {
	diffOnly := flag.Bool("diff", false, "print diff without writing files")
	flag.Parse()

	root := repoRoot()
	goVersion := parseGoVersion(filepath.Join(root, "src", "go.mod"))

	data := renderData{
		Name:         projectName,
		Description:  projectDescription,
		GoVersion:    goVersion,
		Module:       projectModule,
		License:      projectLicense,
		Namespace:    deployNamespace,
		ConfigFields: extractConfig(filepath.Join(root, "src")),
		Tests:        extractTests(filepath.Join(root, "src")),
		MakeTargets:  extractMakeTargets(filepath.Join(root, "Makefile")),
		Conventions:  conventions,
	}

	anyDiff := false
	for _, o := range outputs() {
		var buf bytes.Buffer
		if err := o.tmpl.Execute(&buf, data); err != nil {
			fatalf("render %s: %v", o.path, err)
		}
		dest := filepath.Join(root, o.path)
		if *diffOnly {
			existing, _ := os.ReadFile(dest)
			if !bytes.Equal(existing, buf.Bytes()) {
				anyDiff = true
				fmt.Printf("--- %s\n+++ %s (generated)\n", o.path, o.path)
				printDiff(dest, buf.Bytes())
			}
		} else {
			if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
				fatalf("write %s: %v", o.path, err)
			}
			fmt.Printf("✓ %s\n", o.path)
		}
	}
	if *diffOnly && !anyDiff {
		fmt.Println("docs are up to date")
	}
	if *diffOnly && anyDiff {
		os.Exit(1) // non-zero so CI can catch stale docs
	}
}

func printDiff(path string, want []byte) {
	tmp, _ := os.CreateTemp("", "docs-gen-*")
	tmp.Write(want)
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := exec.Command("diff", "-u", path, tmp.Name())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run() //nolint:errcheck // diff exits 1 when files differ, that's expected
}

func repoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "src", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fatalf("could not find repo root (no src/go.mod found)")
		}
		dir = parent
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-docs: "+format+"\n", args...)
	os.Exit(1)
}

// keep strconv imported for potential future use of Atoi in templates
var _ = strconv.Itoa
