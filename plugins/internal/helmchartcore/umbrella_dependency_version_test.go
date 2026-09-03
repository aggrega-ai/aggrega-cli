// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/charttest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
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

// unreachedSubChartSecret is the value every vendored test chart in this package
// defaults its secret to: a string no mapped value would ever be, so a sub-chart
// the umbrella's values never reached renders THIS rather than something that
// reads like a success.
//
// It is declared once because a chart's default and the assertion that reads it
// back are two halves of one fact. An empty default is the shape to avoid: a
// chart that silently fell back to its own defaults renders an empty value,
// which is indistinguishable from a delivered empty string — and a chart with
// unaddressable values renders its defaults and reports success, so an
// assertion that only checks the chart rendered would pass on values going
// nowhere.
const unreachedSubChartSecret = "NO-VALUES-REACHED-THIS-SUBCHART"

// writeVendoredChart writes a minimal sourced chart whose ConfigMap template
// reads .Values.secret. A rendered secret other than unreachedSubChartSecret
// proves the umbrella's alias-keyed values reached this sub-chart — i.e. Helm
// bound the dependency.
func writeVendoredChart(t *testing.T, dir, name, version string) {
	t.Helper()
	writeFileMkdir(t, filepath.Join(dir, "Chart.yaml"), "apiVersion: v2\nname: "+name+"\nversion: "+version+"\n")
	writeFileMkdir(t, filepath.Join(dir, "values.yaml"), "secret: \""+unreachedSubChartSecret+"\"\n")
	writeFileMkdir(t, filepath.Join(dir, "templates", "cm.yaml"),
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\ndata:\n  secret: \"{{ .Values.secret }}\"\n")
}

// renderThroughDependencyPipeline renders an assembled chart from disk through
// the same dependency pipeline `helm template` drives. The pipeline itself is
// single-sourced in charttest; this wrapper only carries the fatal-on-error
// behaviour a test wants.
func renderThroughDependencyPipeline(t *testing.T, chartDir string) string {
	t.Helper()
	rendered, err := charttest.RenderThroughDependencyPipeline(chartDir)
	if err != nil {
		t.Fatalf("rendering the assembled chart at %q: %v", chartDir, err)
	}
	return rendered
}

// assembleUmbrellaOnDisk writes BuildUmbrella's Chart.yaml + values.yaml plus the
// vendored sub-chart under charts/<instance_id>/, mirroring what the orchestrator
// nests. The directory carries the instance id as authored, which is what the
// dependency's repository names — it is the alias, not the directory, that is
// encoded, so the two are the same word only for an instance id the chart-name
// alphabet already admits.
//
// The sub-chart's files are placed through the same render core the provider
// guest runs rather than copied off disk, because that core is what takes over
// the chart's manifest name. Copying the source raw would place a manifest
// naming whatever its author wrote, which is a chart no dependency in the
// assembled umbrella names — so an install-time assertion over a raw copy would
// be measuring an arrangement the compiler never emits.
func assembleUmbrellaOnDisk(t *testing.T, files map[string][]byte, vendoredSrc, instanceID string) string {
	t.Helper()
	dist := t.TempDir()
	for name, body := range files {
		writeFileMkdir(t, filepath.Join(dist, name), string(body))
	}
	for path, body := range placeSourcedChart(t, vendoredSrc, instanceID) {
		writeFileMkdir(t, filepath.Join(dist, "charts", instanceID, path), string(body))
	}
	return dist
}

// placeSourcedChart projects a chart directory into the packed shape the host
// hands the guest, then runs the shared render core over it — the same two steps
// the sourced render path takes — and returns the placed file set.
func placeSourcedChart(t *testing.T, vendoredSrc, instanceID string) map[string][]byte {
	t.Helper()
	var proj renderer.SourceProjection
	if err := filepath.WalkDir(vendoredSrc, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(vendoredSrc, p)
		if rerr != nil {
			return rerr
		}
		data, derr := os.ReadFile(p)
		if derr != nil {
			return derr
		}
		proj.Files = append(proj.Files, renderer.SourceFile{Path: filepath.ToSlash(rel), Content: data})
		return nil
	}); err != nil {
		t.Fatalf("projecting %q: %v", vendoredSrc, err)
	}
	placed, err := RenderSourcedChartCore(&proj, instanceID, 2)
	if err != nil {
		t.Fatalf("placing %q as %q: %v", vendoredSrc, instanceID, err)
	}
	return placed
}

// TestUmbrellaSourcedDependencyVersion_ValuesReachSubChart is the install-time
// proof that the umbrella's per-instance value block reaches the vendored
// sub-chart's container, with the dependency version derived from the vendored
// chart rather than from the spec/component version the sub carries.
//
// It is non-vacuous without a second arrangement: the vendored chart defaults
// the key to the empty string, so a sub-chart the values never reached renders
// an empty secret and the assertion reddens.
//
// 🔴 A control this test used to carry has been RE-POINTED rather than weakened,
// and the reason is a measured change in what the version buys. It forced the
// dependency version to mismatch the vendored chart and asserted the value then
// failed to arrive — which worked while the placed sub-chart still carried the
// name its author wrote, because delivery then depended on the alias binding and
// the alias bound only on a name-and-version match. Now that the placed manifest
// carries the instance's own encoded name, the umbrella's value key matches the
// sub-chart's name directly and delivery no longer passes through the alias at
// all, so a mismatched version has no render-time consequence to observe.
// Measured against the Helm CLI: with the name owned, a mismatched dependency
// version leaves `helm template` at exit 0 with the value still delivered, while
// `helm dependency list` reports that dependency as "wrong version" rather than
// "unpacked" — so the version's consequence is at dependency-resolution time,
// which no in-process render assertion can see. What still gates delivery in
// process is the name agreement, and that is controlled directly by
// TestPlacingASourcedChartRawLeavesItsValuesUnaddressable, which places a
// sub-chart under its authored name and asserts nothing arrives.
func TestUmbrellaSourcedDependencyVersion_ValuesReachSubChart(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "src")
	writeVendoredChart(t, src, "widget", "0.1.0") // vendored chart version != SubChartInfo.Version below

	files, err := buildUmbrella("app", "demo", []renderer.SubChartInfo{{
		InstanceID: "db",
		Version:    "1.0.0", // the spec/component version — the OLD (wrong) source for the dep version
		Values:     map[string]any{"secret": "s3cr3t"},
		Source:     src,
	}}, "", 2)
	if err != nil {
		t.Fatalf("BuildUmbrella: %v", err)
	}
	// The emitted dependency pins the vendored chart's version, not the sub's.
	if !strings.Contains(string(files[chartMetadataFileName]), "version: 0.1.0") {
		t.Fatalf("dependency version not derived from the vendored chart:\n%s", files[chartMetadataFileName])
	}
	dist := assembleUmbrellaOnDisk(t, files, src, "db")

	rendered := renderThroughDependencyPipeline(t, dist)
	if !strings.Contains(rendered, "secret: \"s3cr3t\"") {
		t.Fatalf("the umbrella value did not reach the sub-chart.\n%s", rendered)
	}
}

// TestUmbrellaSourcedDependencyVersion_MatchesVendoredChart pins the derivation:
// the emitted dependency version equals the vendored chart's Chart.yaml version,
// not the SubChartInfo.Version.
func TestUmbrellaSourcedDependencyVersion_MatchesVendoredChart(t *testing.T) {
	t.Parallel()
	src := filepath.Join(t.TempDir(), "src")
	writeVendoredChart(t, src, "widget", "2.4.6")

	files, err := buildUmbrella("app", "demo", []renderer.SubChartInfo{{
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

	_, err := buildUmbrella("app", "demo", []renderer.SubChartInfo{{
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

// TestUmbrellaEncodedAliasBindsAPunctuatedInstanceID is the install-time proof for
// an instance id the chart-name alphabet does not admit: the alias is encoded, the
// sub-chart directory is not, and the umbrella's alias-keyed values must still
// reach that sub-chart through the same dependency pipeline `helm template` runs.
//
// It also pins the two spellings apart, which is the whole of the defect it
// guards: the dependency's repository names the directory the sub-chart is
// actually in, and the alias is the encoded form. Pointing the repository at the
// alias again leaves the emitted chart resolvable by `helm template` and broken
// for `helm dependency build`, which is a failure no render-time assertion sees —
// so the repository is asserted directly here.
func TestUmbrellaEncodedAliasBindsAPunctuatedInstanceID(t *testing.T) {
	t.Parallel()
	const instanceID = "data.main-db"
	const wantAlias = "data-zdmain-db"

	src := filepath.Join(t.TempDir(), "src")
	writeVendoredChart(t, src, "postgres", "0.1.0")

	files, err := buildUmbrella("app", "demo", []renderer.SubChartInfo{{
		InstanceID: instanceID,
		Version:    "1.0.0",
		Values:     map[string]any{"secret": "s3cr3t"},
		Source:     src,
	}}, "", 2)
	if err != nil {
		t.Fatalf("BuildUmbrella: %v", err)
	}

	chartYAML := string(files["Chart.yaml"])
	if !strings.Contains(chartYAML, "alias: "+wantAlias) {
		t.Errorf("umbrella does not carry the encoded alias %q:\n%s", wantAlias, chartYAML)
	}
	if !strings.Contains(chartYAML, "repository: file://charts/"+instanceID) {
		t.Errorf("the repository must name the sub-chart's own directory %q:\n%s", instanceID, chartYAML)
	}
	if strings.Contains(chartYAML, "repository: file://charts/"+wantAlias) {
		t.Errorf("the repository names the alias, so it points at a directory that is not there:\n%s", chartYAML)
	}
	if !strings.Contains(string(files["values.yaml"]), wantAlias+":") {
		t.Errorf("umbrella values are not keyed by the encoded alias %q:\n%s", wantAlias, files["values.yaml"])
	}

	// The directory carries the instance id as authored, exactly as the
	// orchestrator nests it, so the alias and the directory differ here.
	dist := assembleUmbrellaOnDisk(t, files, src, instanceID)
	rendered := renderThroughDependencyPipeline(t, dist)
	if !strings.Contains(rendered, "secret: \"s3cr3t\"") {
		t.Fatalf("the umbrella value did not reach the sub-chart, so the encoded alias did not bind.\n%s", rendered)
	}
}
