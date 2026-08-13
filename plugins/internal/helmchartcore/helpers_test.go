// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// readChartMeta is the test-side stand-in for the removed host chart-meta reader:
// it reads <dir>/Chart.yaml and resolves name/version through the shared
// ReadChartMetaBytes core, so the guardrail assertions exercise the same
// decode-and-validate core the umbrella assembly and the provider guest run.
func readChartMeta(dir string) (name, version string, err error) {
	data, rerr := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if rerr != nil {
		return "", "", rerr
	}
	return ReadChartMetaBytes(data, dir)
}

// buildUmbrella is the test-side stand-in for the removed host umbrella adapter:
// it reads each sourced sub-chart's pristine Chart.yaml from disk into a
// filesystem-free CoreSubChart — mirroring the generic host projection the
// assembler now runs — then assembles through AssembleUmbrellaCore, the same core
// the provider guest runs.
func buildUmbrella(boundaryName, aggregateName string, subCharts []renderer.SubChartInfo, chartNameOverride string, indent int) (map[string][]byte, error) {
	subs := make([]CoreSubChart, 0, len(subCharts))
	for _, sc := range subCharts {
		cs := CoreSubChart{InstanceID: sc.InstanceID, Version: sc.Version, Values: sc.Values}
		if s, ok := sc.Source.(string); ok && s != "" {
			data, err := os.ReadFile(filepath.Join(s, "Chart.yaml"))
			if err != nil {
				return nil, err
			}
			cs.ChartYAML = data
		}
		subs = append(subs, cs)
	}
	return AssembleUmbrellaCore(boundaryName, aggregateName, subs, chartNameOverride, indent)
}

// TestSanitizeChartName_AllReplacements pins the dot + underscore + path-separator
// → hyphen substitution. The path-separator cases are the regression guard for the
// umbrella chart name: a boundary path's "/" must become "-" so Helm can load the
// chart (Helm rejects a name containing "/"). Non-vacuous — reverting the "/"
// replacement leaves the slash in the output and the slash cases go red while the
// dot/underscore cases stay green.
func TestSanitizeChartName_AllReplacements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"data.main-db", "data-main-db"},
		{"my_under_score", "my-under-score"},
		{"a.b_c", "a-b-c"},
		{"dist/backend/helm-chart", "dist-backend-helm-chart"},
		{"fullstack-app-dist/backend/helm-chart", "fullstack-app-dist-backend-helm-chart"},
		// Distinct boundaries keep distinct names (no leaf-only collapse).
		{"dist/frontend/helm-chart", "dist-frontend-helm-chart"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := sanitizeChartName(tc.in); got != tc.want {
			t.Errorf("sanitizeChartName(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestRenderSourcedChart_HappyPath pins the sourced-render success path: a packed
// chart projection is rebuilt into the file map by RenderSourcedChartCore — the
// same render core the provider guest runs on the projection the host packs.
func TestRenderSourcedChart_HappyPath(t *testing.T) {
	t.Parallel()
	chartYAML := `apiVersion: v2
name: my-chart
version: 0.1.0
`
	proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
		{Path: "Chart.yaml", Content: []byte(chartYAML)},
		{Path: "templates/deploy.yaml", Content: []byte("kind: Deployment\n")},
	}}
	files := RenderSourcedChartCore(proj)
	if _, has := files["Chart.yaml"]; !has {
		t.Errorf("Chart.yaml missing; got keys=%v", keys(files))
	}
	if _, has := files["templates/deploy.yaml"]; !has {
		t.Errorf("templates/deploy.yaml missing")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestValidateSourcedChartListing_HappyPath pins that a directory listing
// carrying Chart.yaml at its root validates.
func TestValidateSourcedChartListing_HappyPath(t *testing.T) {
	t.Parallel()
	listing := &renderer.SourceListing{Root: "/acquired/chart", RootIsDir: true, Entries: []renderer.SourceEntry{
		{Path: "Chart.yaml", IsDir: false},
		{Path: "templates", IsDir: true},
		{Path: "templates/deploy.yaml", IsDir: false},
	}}
	if err := ValidateSourcedChartListing(listing); err != nil {
		t.Errorf("valid chart listing rejected: %v", err)
	}
}

// TestValidateSourcedChartListing_NotADirectory pins the not-a-directory
// rejection: a non-directory source (a file or archive) is not a chart.
func TestValidateSourcedChartListing_NotADirectory(t *testing.T) {
	t.Parallel()
	listing := &renderer.SourceListing{Root: "/acquired/notadir", RootIsDir: false}
	err := ValidateSourcedChartListing(listing)
	if err == nil {
		t.Fatal("non-directory source should reject")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("error should name the not-a-directory cause; got %v", err)
	}
}

// TestValidateSourcedChartListing_MissingChartYAML pins the missing-Chart.yaml
// rejection.
func TestValidateSourcedChartListing_MissingChartYAML(t *testing.T) {
	t.Parallel()
	listing := &renderer.SourceListing{Root: "/acquired/nochart", RootIsDir: true, Entries: []renderer.SourceEntry{
		{Path: "templates.yaml", IsDir: false},
	}}
	err := ValidateSourcedChartListing(listing)
	if err == nil {
		t.Fatal("directory without Chart.yaml should reject")
	}
	if !strings.Contains(err.Error(), "has no Chart.yaml") {
		t.Errorf("error should name the missing Chart.yaml; got %v", err)
	}
}

// TestRenderSourcedChart_NestedFiles pins the recursive rebuild: a packed
// projection carrying nested forward-slash paths is rebuilt into the file map by
// the render core with those paths preserved.
func TestRenderSourcedChart_NestedFiles(t *testing.T) {
	t.Parallel()
	proj := &renderer.SourceProjection{Files: []renderer.SourceFile{
		{Path: "Chart.yaml", Content: []byte("apiVersion: v2\nname: n\nversion: 0.1.0\n")},
		{Path: "templates/deep/nested.yaml", Content: []byte("nested: true\n")},
	}}
	files := RenderSourcedChartCore(proj)
	if _, has := files["templates/deep/nested.yaml"]; !has {
		t.Errorf("nested file missing; got keys=%v", keys(files))
	}
}

// TestReadChartMeta_AllBranches pins missing-file, parse-error, empty-name,
// empty-version, and happy paths.
func TestReadChartMeta_AllBranches(t *testing.T) {
	t.Parallel()
	// Missing file.
	_, _, err := readChartMeta("/nonexistent/path")
	if err == nil {
		t.Errorf("missing path should error")
	}

	// Parse error: malformed YAML in Chart.yaml.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "Chart.yaml"), []byte("::: not yaml :::"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err = readChartMeta(tmp)
	if err == nil {
		t.Errorf("malformed YAML should error")
	}

	// Empty name.
	tmp2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp2, "Chart.yaml"), []byte("apiVersion: v2\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	_, _, err = readChartMeta(tmp2)
	if err == nil {
		t.Errorf("empty name should error")
	}

	// Empty version (the parallel guard).
	tmp2b := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp2b, "Chart.yaml"), []byte("apiVersion: v2\nname: real-name\n"), 0o644); err != nil {
		t.Fatalf("write empty version: %v", err)
	}
	if _, _, err := readChartMeta(tmp2b); err == nil {
		t.Errorf("empty version should error")
	}

	// Happy.
	tmp3 := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp3, "Chart.yaml"), []byte("apiVersion: v2\nname: real-name\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, gotVer, err := readChartMeta(tmp3)
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got != "real-name" {
		t.Errorf("name = %q; want real-name", got)
	}
	if gotVer != "0.1.0" {
		t.Errorf("version = %q; want 0.1.0", gotVer)
	}
}

// TestBuildUmbrella_AllBranches pins empty-boundary error, override-name
// path, default-name path with + without aggregate, sourced sub-chart
// (chart-name read), generative sub-chart (no Source), per-sub-chart
// values + nil Values.
func TestBuildUmbrella_AllBranches(t *testing.T) {
	t.Parallel()
	// Empty boundary name.
	_, err := buildUmbrella("", "", nil, "", 2)
	if err == nil {
		t.Errorf("empty boundary should error")
	}

	// Default name with aggregate prefix.
	files, err := buildUmbrella("web", "agg", nil, "", 2)
	if err != nil {
		t.Fatalf("default-with-agg: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: agg-web") {
		t.Errorf("aggregate-prefixed name missing; got\n%s", files["Chart.yaml"])
	}

	// Default name without aggregate.
	files, err = buildUmbrella("solo", "", nil, "", 2)
	if err != nil {
		t.Fatalf("default-no-agg: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: solo") {
		t.Errorf("solo name missing; got\n%s", files["Chart.yaml"])
	}

	// Override name (cascade).
	files, err = buildUmbrella("web", "agg", nil, "override.name", 2)
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: override-name") {
		t.Errorf("override name should be sanitized; got\n%s", files["Chart.yaml"])
	}

	// Generative sub-chart (no Source) + sub-chart with Values.
	subCharts := []renderer.SubChartInfo{
		{InstanceID: "gen-1", Version: "1.0.0", Values: map[string]any{"k": "v"}},
		{InstanceID: "gen-2", Version: ""}, // empty version → default
	}
	files, err = buildUmbrella("web", "", subCharts, "", 2)
	if err != nil {
		t.Fatalf("generative: %v", err)
	}
	// Both sub-charts should appear as dependencies.
	if !strings.Contains(string(files["Chart.yaml"]), "alias: gen-1") {
		t.Errorf("gen-1 dependency missing")
	}
	if !strings.Contains(string(files["Chart.yaml"]), "alias: gen-2") {
		t.Errorf("gen-2 dependency missing")
	}

	// Sourced sub-chart: requires reading Chart.yaml from disk.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "Chart.yaml"), []byte("apiVersion: v2\nname: real-name\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sourcedSC := []renderer.SubChartInfo{
		{InstanceID: "sourced-i", Version: "1.0.0", Source: tmp},
	}
	files, err = buildUmbrella("web", "", sourcedSC, "", 2)
	if err != nil {
		t.Fatalf("sourced: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: real-name") {
		t.Errorf("sourced dependency should use chart's name; got\n%s", files["Chart.yaml"])
	}

	// Sourced sub-chart with missing Chart.yaml → propagates error.
	badSC := []renderer.SubChartInfo{
		{InstanceID: "bad-i", Source: "/nonexistent/path"},
	}
	_, err = buildUmbrella("web", "", badSC, "", 2)
	if err == nil {
		t.Errorf("missing source-chart should error")
	}
}

// TestBuildUmbrella_EmitsChart pins the umbrella assembly contract: a boundary
// with an aggregate name produces an umbrella Chart.yaml.
func TestBuildUmbrella_EmitsChart(t *testing.T) {
	t.Parallel()
	files, err := buildUmbrella("web", "agg", nil, "", 2)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if _, has := files["Chart.yaml"]; !has {
		t.Errorf("Chart.yaml missing")
	}
}

// TestBuildUmbrella_EmptyBoundaryErrors pins the empty-boundary guardrail.
func TestBuildUmbrella_EmptyBoundaryErrors(t *testing.T) {
	t.Parallel()
	_, err := buildUmbrella("", "", nil, "", 2)
	if err == nil {
		t.Errorf("empty boundary should error")
	}
}
