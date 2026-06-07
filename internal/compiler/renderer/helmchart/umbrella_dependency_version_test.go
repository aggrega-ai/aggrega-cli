// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

func writeFileMkdir(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeVendoredChart writes a minimal sourced chart whose ConfigMap template
// reads .Values.secret. A rendered non-empty secret proves the umbrella's
// alias-keyed values reached this sub-chart — i.e. Helm bound the dependency.
func writeVendoredChart(t *testing.T, dir, name, version string) {
	t.Helper()
	writeFileMkdir(t, filepath.Join(dir, "Chart.yaml"), "apiVersion: v2\nname: "+name+"\nversion: "+version+"\n")
	writeFileMkdir(t, filepath.Join(dir, "values.yaml"), "secret: \"\"\n")
	writeFileMkdir(t, filepath.Join(dir, "templates", "cm.yaml"),
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\ndata:\n  secret: \"{{ .Values.secret }}\"\n")
}

// renderThroughDependencyPipeline assembles a chart from disk and renders it
// through the SAME dependency pipeline `helm template` uses:
// ProcessDependenciesWithMerge applies the Chart.yaml dependency aliases — and
// only binds a dependency whose name AND version match the vendored sub-chart —
// then CoalesceValues (inside ToRenderValues) scopes the alias-keyed value block
// into that sub-chart. A bare engine.Render would skip this and prove nothing.
func renderThroughDependencyPipeline(t *testing.T, chartDir string) string {
	t.Helper()
	ch, err := loader.Load(chartDir)
	if err != nil {
		t.Fatalf("load chart: %v", err)
	}
	if err := chartutil.ProcessDependenciesWithMerge(ch, ch.Values); err != nil {
		t.Fatalf("ProcessDependenciesWithMerge: %v", err)
	}
	rv, err := chartutil.ToRenderValues(ch, ch.Values, chartutil.ReleaseOptions{Name: "demo", Namespace: "default"}, chartutil.DefaultCapabilities)
	if err != nil {
		t.Fatalf("ToRenderValues: %v", err)
	}
	out, err := engine.Render(ch, rv)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(out[k])
		b.WriteString("\n")
	}
	return b.String()
}

// assembleUmbrellaOnDisk writes BuildUmbrella's Chart.yaml + values.yaml plus the
// vendored sub-chart under charts/<alias>/, mirroring what the orchestrator nests.
func assembleUmbrellaOnDisk(t *testing.T, files map[string][]byte, vendoredSrc, alias string) string {
	t.Helper()
	dist := t.TempDir()
	for name, body := range files {
		writeFileMkdir(t, filepath.Join(dist, name), string(body))
	}
	// Copy the vendored chart into charts/<alias>/.
	_ = filepath.WalkDir(vendoredSrc, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(vendoredSrc, p)
		data, _ := os.ReadFile(p)
		writeFileMkdir(t, filepath.Join(dist, "charts", alias, rel), string(data))
		return nil
	})
	return dist
}

// TestUmbrellaSourcedDependencyVersion_ValuesReachSubChart is the install-time
// proof: the umbrella's alias-keyed value block reaches the vendored sub-chart's
// container ONLY when the dependency version matches the vendored chart. The
// builder now derives that version from the vendored Chart.yaml, so it matches —
// even though the SubChartInfo.Version (spec/component version) differs.
func TestUmbrellaSourcedDependencyVersion_ValuesReachSubChart(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "src")
	writeVendoredChart(t, src, "widget", "0.1.0") // vendored chart version != SubChartInfo.Version below

	files, err := BuildUmbrella("app", "demo", []renderer.SubChartInfo{{
		InstanceID: "db",
		Version:    "1.0.0", // the spec/component version — the OLD (wrong) source for the dep version
		Values:     map[string]any{"secret": "s3cr3t"},
		Source:     src,
	}}, "", 2)
	if err != nil {
		t.Fatalf("BuildUmbrella: %v", err)
	}
	dist := assembleUmbrellaOnDisk(t, files, src, "db")

	rendered := renderThroughDependencyPipeline(t, dist)
	if !strings.Contains(rendered, "secret: \"s3cr3t\"") {
		t.Fatalf("the umbrella value did not reach the sub-chart — the dependency alias did not bind.\n%s", rendered)
	}

	// Non-vacuous control: force the dependency version to mismatch the vendored
	// chart. The SAME pipeline must then leave the value empty (alias unbound) —
	// proving the version match is what binds the alias, not something incidental.
	chartYAML, _ := os.ReadFile(filepath.Join(dist, "Chart.yaml"))
	// Anchor on the alias line so we rewrite the DEPENDENCY version (the bug's
	// locus), not the umbrella's own version (both are 0.1.0).
	mismatched := strings.Replace(string(chartYAML), "version: 0.1.0\n    alias: db", "version: 1.0.0\n    alias: db", 1)
	if mismatched == string(chartYAML) {
		t.Fatalf("control setup failed to rewrite the dependency version; Chart.yaml=\n%s", chartYAML)
	}
	control := t.TempDir()
	writeFileMkdir(t, filepath.Join(control, "Chart.yaml"), mismatched)
	data, _ := os.ReadFile(filepath.Join(dist, "values.yaml"))
	writeFileMkdir(t, filepath.Join(control, "values.yaml"), string(data))
	_ = filepath.WalkDir(filepath.Join(dist, "charts"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dist, p)
		b, _ := os.ReadFile(p)
		writeFileMkdir(t, filepath.Join(control, rel), string(b))
		return nil
	})
	controlRendered := renderThroughDependencyPipeline(t, control)
	if strings.Contains(controlRendered, "secret: \"s3cr3t\"") {
		t.Fatalf("control with a mismatched dependency version still propagated the value — the test is vacuous (the version gate is not what this exercises)")
	}
}

// TestUmbrellaSourcedDependencyVersion_MatchesVendoredChart pins the derivation:
// the emitted dependency version equals the vendored chart's Chart.yaml version,
// not the SubChartInfo.Version.
func TestUmbrellaSourcedDependencyVersion_MatchesVendoredChart(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "src")
	writeVendoredChart(t, src, "widget", "2.4.6")

	files, err := BuildUmbrella("app", "demo", []renderer.SubChartInfo{{
		InstanceID: "db",
		Version:    "1.0.0",
		Source:     src,
	}}, "", 2)
	if err != nil {
		t.Fatalf("BuildUmbrella: %v", err)
	}
	chartYAML := string(files["Chart.yaml"])
	if !strings.Contains(chartYAML, "version: 2.4.6") {
		t.Errorf("dependency version not derived from the vendored chart (want 2.4.6):\n%s", chartYAML)
	}
	if strings.Contains(chartYAML, "version: 1.0.0") {
		t.Errorf("dependency version wrongly taken from SubChartInfo.Version (1.0.0):\n%s", chartYAML)
	}
}

// TestUmbrellaSourcedDependencyVersion_EmptyVersionHaltsLoud pins the parallel
// Loud Guardrail: a vendored chart whose Chart.yaml has no version halts.
func TestUmbrellaSourcedDependencyVersion_EmptyVersionHaltsLoud(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "src")
	// Chart.yaml with a name but no version.
	writeFileMkdir(t, filepath.Join(src, "Chart.yaml"), "apiVersion: v2\nname: widget\n")

	_, err := BuildUmbrella("app", "demo", []renderer.SubChartInfo{{
		InstanceID: "db",
		Source:     src,
	}}, "", 2)
	if err == nil {
		t.Fatal("expected an error for a vendored Chart.yaml with no version")
	}
	if !strings.Contains(err.Error(), "empty version") {
		t.Errorf("error should name the missing version; got %v", err)
	}
}
