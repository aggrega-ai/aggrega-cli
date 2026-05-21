// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// TestHandlesArtifactsExternally_NoOp pins the marker method —
// invocation should not panic.
func TestHandlesArtifactsExternally_NoOp(t *testing.T) {
	t.Parallel()
	(&HelmEngine{}).HandlesArtifactsExternally()
}

// TestPathForArtifact_PrefixesTemplates pins the path-provider
// contract: every basename lands under templates/.
func TestPathForArtifact_PrefixesTemplates(t *testing.T) {
	t.Parallel()
	e := &HelmEngine{}
	got := e.PathForArtifact(renderer.NodeRenderContext{NodeID: "n"}, "deployment.yaml")
	if got != "templates/deployment.yaml" {
		t.Errorf("got %q; want templates/deployment.yaml", got)
	}
}

// TestArtifactDelimiters_HelmCompatible pins the [[ ]] pair (so Helm's
// own {{ }} survives Aggrega's pre-templating pass).
func TestArtifactDelimiters_HelmCompatible(t *testing.T) {
	t.Parallel()
	left, right := (&HelmEngine{}).ArtifactDelimiters()
	if left != "[[" || right != "]]" {
		t.Errorf("delims = (%q, %q); want ([[, ]])", left, right)
	}
}

// TestSanitizeChartName_AllReplacements pins the dot+underscore →
// hyphen substitution.
func TestSanitizeChartName_AllReplacements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"data.main-db", "data-main-db"},
		{"my_under_score", "my-under-score"},
		{"a.b_c", "a-b-c"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := sanitizeChartName(tc.in); got != tc.want {
			t.Errorf("sanitizeChartName(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuildAggregaLabels_EveryConditional pins each optional-emission
// arm of the helmchart label builder.
func TestBuildAggregaLabels_EveryConditional(t *testing.T) {
	t.Parallel()
	ctx := renderer.NodeRenderContext{
		NodeID:           "node-1",
		LabelComponentID: "comp-x",
		BoundaryPath:     "infra/web",
		RecipeName:       "myco/web",
		RecipeVersion:    "1.0.0",
		Provenance: renderer.ProvenanceContext{
			CompilerVersion: "v0.99",
			AggregateName:   "agg-prod",
			Environment:     "prod",
		},
		SyncWave: 5,
	}
	got := buildAggregaLabels(ctx)
	want := map[string]string{
		"app.kubernetes.io/name":      "comp-x",
		"aggrega.ai/component-id":     "comp-x",
		"aggrega.ai/boundary-path":    "infra/web",
		"aggrega.ai/recipe-uri":       "myco/web:1.0.0",
		"aggrega.ai/compiler-version": "v0.99",
		"aggrega.ai/aggregate-name":   "agg-prod",
		"aggrega.ai/environment":      "prod",
		"aggrega.ai/sync-wave":        "5",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q; want %q", k, got[k], v)
		}
	}
	// Empty ctx — only default identity labels.
	minimal := buildAggregaLabels(renderer.NodeRenderContext{NodeID: "n"})
	if minimal["app.kubernetes.io/name"] != "n" {
		t.Errorf("fallback to NodeID; got %q", minimal["app.kubernetes.io/name"])
	}
	if _, has := minimal["aggrega.ai/sync-wave"]; has {
		t.Errorf("zero SyncWave should not emit")
	}
}

// TestBuildAggregaAnnotations_SyncWaveAndDefault pins the conditional
// emission of argocd.argoproj.io/sync-wave.
func TestBuildAggregaAnnotations_SyncWaveAndDefault(t *testing.T) {
	t.Parallel()
	got := buildAggregaAnnotations(renderer.NodeRenderContext{SyncWave: 3})
	if got["argocd.argoproj.io/sync-wave"] != "3" {
		t.Errorf("got %v; want sync-wave=3", got)
	}
	got = buildAggregaAnnotations(renderer.NodeRenderContext{SyncWave: 0})
	if len(got) != 0 {
		t.Errorf("zero SyncWave → empty; got %v", got)
	}
}

// TestSortedLabelKeys_DeterministicOrder pins the sorted-key contract.
func TestSortedLabelKeys_DeterministicOrder(t *testing.T) {
	t.Parallel()
	m := map[string]string{"zebra": "z", "apple": "a", "mango": "m"}
	got := sortedLabelKeys(m)
	want := []string{"apple", "mango", "zebra"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q; want %q", i, got[i], want[i])
		}
	}
	// Empty map.
	if len(sortedLabelKeys(map[string]string{})) != 0 {
		t.Errorf("empty → empty slice")
	}
}

// TestHelmEngine_Render_RejectsEmptySource pins the empty-source
// rejection (sourceless mode unsupported in helmchart).
func TestHelmEngine_Render_RejectsEmptySource(t *testing.T) {
	t.Parallel()
	e := &HelmEngine{}
	ctx := renderer.NodeRenderContext{NodeID: "n", Source: ""}
	_, err := e.Render(context.Background(), nil, ctx)
	if err == nil {
		t.Errorf("empty source should reject")
	}
	if !strings.Contains(err.Error(), "source is required") {
		t.Errorf("error should mention source is required; got %v", err)
	}
}

// TestHelmEngine_Render_RejectsNonStringSource pins the non-string
// source rejection (Source is typed as `any`; only string is accepted).
func TestHelmEngine_Render_RejectsNonStringSource(t *testing.T) {
	t.Parallel()
	e := &HelmEngine{}
	ctx := renderer.NodeRenderContext{NodeID: "n", Source: map[string]any{"chart": "x"}}
	_, err := e.Render(context.Background(), nil, ctx)
	if err == nil {
		t.Errorf("non-string source should reject")
	}
}

// TestHelmEngine_Render_HappyPath pins the success path with a real
// chart directory.
func TestHelmEngine_Render_HappyPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	chartYAML := `apiVersion: v2
name: my-chart
version: 0.1.0
`
	if err := os.WriteFile(filepath.Join(tmp, "Chart.yaml"), []byte(chartYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "templates", "deploy.yaml"), []byte("kind: Deployment\n"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	e := &HelmEngine{}
	ctx := renderer.NodeRenderContext{NodeID: "n", Source: tmp}
	files, err := e.Render(context.Background(), nil, ctx)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
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

// TestCopySourcedChart_StatError pins the stat-fail path.
func TestCopySourcedChart_StatError(t *testing.T) {
	t.Parallel()
	_, err := copySourcedChart("/nonexistent/path/nope")
	if err == nil {
		t.Errorf("missing path should error")
	}
}

// TestCopySourcedChart_NotADirectory pins the not-a-directory rejection.
func TestCopySourcedChart_NotADirectory(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	file := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := copySourcedChart(file)
	if err == nil {
		t.Errorf("file path should reject")
	}
}

// TestCopySourcedChart_MissingChartYAML pins the missing-Chart.yaml
// rejection.
func TestCopySourcedChart_MissingChartYAML(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	// Populate with a non-Chart.yaml file so the walk runs but the
	// post-walk check fails.
	if err := os.WriteFile(filepath.Join(tmp, "templates.yaml"), []byte("k: v\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := copySourcedChart(tmp)
	if err == nil {
		t.Errorf("directory without Chart.yaml should reject")
	}
}

// TestCopySourcedChart_NestedFiles pins the recursive walk: nested
// files are captured with forward-slash relative paths.
func TestCopySourcedChart_NestedFiles(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "Chart.yaml"), []byte("apiVersion: v2\nname: n\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	subdir := filepath.Join(tmp, "templates", "deep")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "nested.yaml"), []byte("nested: true\n"), 0o644); err != nil {
		t.Fatalf("write nested: %v", err)
	}
	files, err := copySourcedChart(tmp)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if _, has := files["templates/deep/nested.yaml"]; !has {
		t.Errorf("nested file missing; got keys=%v", keys(files))
	}
}

// TestReadChartName_AllBranches pins missing-file, parse-error,
// empty-name, and happy paths.
func TestReadChartName_AllBranches(t *testing.T) {
	t.Parallel()
	// Missing file.
	_, err := readChartName("/nonexistent/path")
	if err == nil {
		t.Errorf("missing path should error")
	}

	// Parse error: malformed YAML in Chart.yaml.
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "Chart.yaml"), []byte("::: not yaml :::"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = readChartName(tmp)
	if err == nil {
		t.Errorf("malformed YAML should error")
	}

	// Empty name.
	tmp2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp2, "Chart.yaml"), []byte("apiVersion: v2\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	_, err = readChartName(tmp2)
	if err == nil {
		t.Errorf("empty name should error")
	}

	// Happy.
	tmp3 := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp3, "Chart.yaml"), []byte("apiVersion: v2\nname: real-name\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readChartName(tmp3)
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got != "real-name" {
		t.Errorf("name = %q; want real-name", got)
	}
}

// TestBuildUmbrella_AllBranches pins empty-boundary error, override-name
// path, default-name path with + without aggregate, sourced sub-chart
// (chart-name read), generative sub-chart (no Source), per-sub-chart
// values + nil Values.
func TestBuildUmbrella_AllBranches(t *testing.T) {
	t.Parallel()
	// Empty boundary name.
	_, err := BuildUmbrella("", "", nil, "")
	if err == nil {
		t.Errorf("empty boundary should error")
	}

	// Default name with aggregate prefix.
	files, err := BuildUmbrella("web", "agg", nil, "")
	if err != nil {
		t.Fatalf("default-with-agg: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: agg-web") {
		t.Errorf("aggregate-prefixed name missing; got\n%s", files["Chart.yaml"])
	}

	// Default name without aggregate.
	files, err = BuildUmbrella("solo", "", nil, "")
	if err != nil {
		t.Fatalf("default-no-agg: %v", err)
	}
	if !strings.Contains(string(files["Chart.yaml"]), "name: solo") {
		t.Errorf("solo name missing; got\n%s", files["Chart.yaml"])
	}

	// Override name (cascade).
	files, err = BuildUmbrella("web", "agg", nil, "override.name")
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
	files, err = BuildUmbrella("web", "", subCharts, "")
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
	files, err = BuildUmbrella("web", "", sourcedSC, "")
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
	_, err = BuildUmbrella("web", "", badSC, "")
	if err == nil {
		t.Errorf("missing source-chart should error")
	}
}

// TestAssembleBoundary_DelegatesToBuildUmbrella pins the engine's
// BoundaryAssembler contract.
func TestAssembleBoundary_DelegatesToBuildUmbrella(t *testing.T) {
	t.Parallel()
	e := &HelmEngine{}
	files, nestPerNode, err := e.AssembleBoundary(context.Background(), renderer.BoundaryInfo{
		BoundaryPath:  "web",
		AggregateName: "agg",
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !nestPerNode {
		t.Errorf("helmchart should nest per node")
	}
	if _, has := files["Chart.yaml"]; !has {
		t.Errorf("Chart.yaml missing")
	}
}

// TestAssembleBoundary_PropagatesError pins the error-propagation
// arm of AssembleBoundary.
func TestAssembleBoundary_PropagatesError(t *testing.T) {
	t.Parallel()
	e := &HelmEngine{}
	// Empty boundary → BuildUmbrella errors.
	_, _, err := e.AssembleBoundary(context.Background(), renderer.BoundaryInfo{})
	if err == nil {
		t.Errorf("empty boundary should propagate error")
	}
}
