// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Proves which VALUE a relative `source:` is resolved against when the source is
// written as a template, end to end through a real build.
//
// The property is that the resolution runs on the path the template produced,
// not on the template's own text. The two readings agree on where they look
// whenever the produced value is relative — a directory joined to a relative path
// is the same directory either way — which is exactly what makes the difference
// invisible in a build's output and is why the relative row asserts on the
// resolved value the build carries rather than on the files it emitted. The rows
// where the produced value is NOT relative are where the readings part company,
// and those assert on the build's outcome.
//
// Every case builds its own workspace under a temporary directory, so the
// anchoring is distinguishable from a resolution against the directory the tests
// run in, and so one row's failure is attributable to that row.

package compiler_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// anchorTraceSlot is the trace entry the source resolution writes for this
// fixture's single node. It is the build's own record of the value the source
// resolved to, which is what makes this an observation of a real build rather
// than of a hand-driven pass.
const anchorTraceSlot = "boundaries/cluster/app/05-source-resolution.yaml"

// writeAnchorFile writes body at path under a directory it creates.
func writeAnchorFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeAnchorChart plants a minimal helm chart at dir whose single template
// renders sentinel into the emitted output, so the rendered artifact names WHICH
// chart directory the build actually read.
func writeAnchorChart(t *testing.T, dir, sentinel string) {
	t.Helper()
	writeAnchorFile(t, filepath.Join(dir, "Chart.yaml"),
		"apiVersion: v2\nname: app\nversion: 0.1.0\ndescription: source-anchoring fixture chart.\n")
	writeAnchorFile(t, filepath.Join(dir, "values.yaml"), "image: app:latest\nport: 8080\n")
	writeAnchorFile(t, filepath.Join(dir, "templates", "configmap.yaml"),
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-config\ndata:\n  chart_read_from: "+sentinel+"\n")
}

// anchorWorkspace writes a whole buildable workspace under a temporary directory
// and returns its Release path. The Recipe's source block is supplied verbatim so
// each row authors exactly the `source:` (and any `locals:`) it is about.
func anchorWorkspace(t *testing.T, recipeSourceBlock string) (root, releasePath string) {
	t.Helper()
	root = t.TempDir()

	writeAnchorFile(t, filepath.Join(root, "aggregate.yaml"),
		"apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: anchor-app\n  version: \"1.0.0\"\nspec:\n  components:\n    app:\n      type: core/compute/api-server\n")

	writeAnchorFile(t, filepath.Join(root, "manifest.yaml"),
		"apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: anchor-manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n        core/compute/api-server: anchored-source:1.0.0\n")

	releasePath = filepath.Join(root, "release.yaml")
	writeAnchorFile(t, releasePath,
		"apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: anchor-release\n  environment: production\n  version: \"1.0.0\"\nspec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n  boundaries:\n    cluster:\n      directory: ./dist/app\n      components:\n        app:\n          infra:\n            port: 8080\n")

	// The Recipe sits one directory below the catalog root, so a value resolved
	// against the Recipe's own directory and one resolved against the catalog root
	// name different places.
	writeAnchorFile(t, filepath.Join(root, "catalog", "recipes", "anchored.yaml"),
		"apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: anchored-source\n  version: \"1.0.0\"\nspec:\n  capability: helm-chart\n  implements: core/compute/api-server\n"+
			recipeSourceBlock+
			"  provides:\n    host: \"{{ .context.instance_id }}.svc.cluster.local\"\n    service_port: \"{{ .infra.port }}\"\n"+
			"  valuesMapping: |\n    {\n      \"image\": \"app:latest\",\n      \"port\": {{ .infra.port }}\n    }\n")

	return root, releasePath
}

// anchorBuild compiles the generated workspace through the build-faithful
// harness, with the base catalog the fixture's component type comes from.
func anchorBuild(t *testing.T, releasePath string) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  releasePath,
		CatalogPaths: []string{"testdata/fullstack-app/catalog/base", filepath.Dir(releasePath) + "/catalog"},
		IncludeTrace: true,
	})
}

// resolvedSourceFromTrace reads the source value the build recorded at source
// resolution. Failing when the slot is absent is deliberate: an assertion made
// against a missing slot would be vacuous, which is the one failure mode a proof
// about a resolved value cannot afford.
func resolvedSourceFromTrace(t *testing.T, result *pipeline.Result) string {
	t.Helper()
	body, ok := result.Trace[anchorTraceSlot]
	if !ok {
		slots := make([]string, 0, len(result.Trace))
		for p := range result.Trace {
			slots = append(slots, p)
		}
		sort.Strings(slots)
		t.Fatalf("trace slot %q absent; the fixture's trace layout drifted. present: %v", anchorTraceSlot, slots)
	}
	for _, line := range strings.Split(string(body), "\n") {
		rest, found := strings.CutPrefix(line, "source:")
		if !found {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `"'`)
	}
	t.Fatalf("the source-resolution trace carries no source entry:\n%s", body)
	return ""
}

// emittedChartSentinel returns the sentinel the rendered output carries, which
// names the chart directory the build actually read.
func emittedChartSentinel(t *testing.T, result *pipeline.Result) string {
	t.Helper()
	paths := make([]string, 0, len(result.Artifacts))
	for p := range result.Artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		for _, line := range strings.Split(string(result.Artifacts[p]), "\n") {
			if rest, found := strings.CutPrefix(strings.TrimSpace(line), "chart_read_from:"); found {
				return strings.TrimSpace(rest)
			}
		}
	}
	t.Fatalf("no rendered file carries the chart sentinel; files: %v", paths)
	return ""
}

// TestPlainRelativeSourceResolvesAgainstTheRecipeDirectory is the unchanged-
// behaviour control for the templated rows below. A source carrying no template
// is resolved as the document is loaded, against the directory that document sits
// in, and this proves that reading survives the deferral the templated rows
// introduce — without it, a repair that simply stopped anchoring would pass every
// assertion the templated rows make.
func TestPlainRelativeSourceResolvesAgainstTheRecipeDirectory(t *testing.T) {
	t.Parallel()
	root, releasePath := anchorWorkspace(t, "  source: ./charts/app\n")
	writeAnchorChart(t, filepath.Join(root, "catalog", "recipes", "charts", "app"), "beside-the-recipe")

	result := anchorBuild(t, releasePath)

	want := filepath.Join(root, "catalog", "recipes", "charts", "app")
	if got := resolvedSourceFromTrace(t, result); got != want {
		t.Errorf("a plain relative source must resolve against the Recipe's own directory:\n got  %q\n want %q", got, want)
	}
	if got := emittedChartSentinel(t, result); got != "beside-the-recipe" {
		t.Errorf("the build read the wrong chart directory: sentinel %q", got)
	}
}

// TestTemplatedSourceResolvesAgainstTheValueTheTemplateProduced is the property.
//
// The Recipe's source is a template yielding a relative path that climbs out of
// the Recipe's own directory. Resolving the template's TEXT leaves the climb
// unresolved inside the value the build carries — the source reads
// `<recipeDir>/../shared/charts/app`, a path naming a directory nobody authored
// by a route nobody wrote — while resolving the produced VALUE yields the
// directory the author's path names.
//
// Both spellings open the same chart, which is why this row asserts on the value
// and the rows below assert on the outcome: the defect is invisible in what a
// build emits for exactly as long as the produced value stays relative.
func TestTemplatedSourceResolvesAgainstTheValueTheTemplateProduced(t *testing.T) {
	t.Parallel()
	root, releasePath := anchorWorkspace(t,
		"  source: \"{{ .locals.chart_dir }}\"\n  locals:\n    chart_dir: ../shared/charts/app\n")
	writeAnchorChart(t, filepath.Join(root, "catalog", "shared", "charts", "app"), "beside-the-catalog")

	result := anchorBuild(t, releasePath)

	got := resolvedSourceFromTrace(t, result)
	want := filepath.Join(root, "catalog", "shared", "charts", "app")
	if got != want {
		t.Errorf("a templated source must resolve against the value the template produced:\n got  %q\n want %q", got, want)
	}
	// Stated separately from the comparison above so the failure names the cause:
	// an unresolved climb is the signature of the template's text having been
	// anchored before the template ran.
	if strings.Contains(got, string(filepath.Separator)+".."+string(filepath.Separator)) {
		t.Errorf("the resolved source carries an unresolved climb, so the anchoring ran on the template's text: %q", got)
	}
	if sentinel := emittedChartSentinel(t, result); sentinel != "beside-the-catalog" {
		t.Errorf("the build read the wrong chart directory: sentinel %q", sentinel)
	}
}

// TestTemplatedSourceYieldingAnAbsolutePathIsNotPrefixed is where the two
// readings part company in the build's OUTCOME rather than in a value it carries.
//
// An absolute path already names where it is, so the resolution hands it back. A
// resolution that ran on the template's text has no such value to read — the
// directory is joined to the template's spelling, and the absolute path the
// author produced lands underneath it — so the build fails to find a chart that
// is sitting exactly where the author said.
func TestTemplatedSourceYieldingAnAbsolutePathIsNotPrefixed(t *testing.T) {
	t.Parallel()
	root, releasePath := anchorWorkspace(t,
		"  source: \"{{ .locals.chart_dir }}\"\n  locals:\n    chart_dir: PLACEHOLDER\n")

	// The chart sits outside the catalog tree entirely, so nothing but an absolute
	// resolution can reach it.
	chart := filepath.Join(root, "outside", "charts", "app")
	writeAnchorChart(t, chart, "outside-the-catalog")

	recipePath := filepath.Join(root, "catalog", "recipes", "anchored.yaml")
	body, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatal(err)
	}
	writeAnchorFile(t, recipePath, strings.Replace(string(body), "PLACEHOLDER", chart, 1))

	result := anchorBuild(t, releasePath)

	if got := resolvedSourceFromTrace(t, result); got != chart {
		t.Errorf("an absolute path a template produced must be handed back as it stands:\n got  %q\n want %q", got, chart)
	}
	if sentinel := emittedChartSentinel(t, result); sentinel != "outside-the-catalog" {
		t.Errorf("the build read the wrong chart directory: sentinel %q", sentinel)
	}
}
