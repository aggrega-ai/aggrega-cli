// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// helmChartFilesGetInput compiles the helm-chart files-get example: a sourced
// chart whose template reads a loose chart-root file via `.Files.Get`, paired
// with a Recipe that declares that loose file as a standalone inline artifact.
func helmChartFilesGetInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath: "testdata/helm-chart-files-get/release.yaml",
		CatalogPaths: []string{
			"testdata/fullstack-app/catalog/base",
			"testdata/helm-chart-files-get/catalog",
		},
	}
}

// TestHelmChartFilesGet proves the standalone-inline-artifact placement the
// helm-chart provider performs end-to-end: a Recipe's inline artifact is placed
// at the sub-chart root (not under templates/), verbatim and host-evaluated, so
// the chart's own template can read it via `.Files.Get` at `helm install` time.
// This exercises the whole spine — catalog load (the inline artifact no longer
// rejected at load), render dispatch to the embedded helm-chart Wasm guest, and
// umbrella assembly — through the build-faithful harness.
func TestHelmChartFilesGet(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, helmChartFilesGetInput())

	loose, loosePath := findFile(res.Artifacts, "helm-chart/charts/", "app-config.properties")
	if loose == "" {
		t.Fatalf("loose chart-root file app-config.properties not found; files: %v", sortedKeys(res.Artifacts))
	}

	// The loose file lands at the sub-chart ROOT, never under templates/ — that
	// is the placement `.Files.Get` needs and the placement the deleted
	// load-time rule used to forbid.
	if strings.Contains(loosePath, "/templates/") {
		t.Errorf("loose file placed under templates/ (%s); a .Files-readable artifact must sit at the chart root", loosePath)
	}

	// Its body flowed through the artifacts projection: the host evaluated the
	// inline template with the helm-chart "[[ ]]" delimiter pair, so the
	// instance id and port are resolved ahead of time.
	for _, want := range []string{"service.name=app", "service.port=8080"} {
		if !strings.Contains(loose, want) {
			t.Errorf("loose file %s missing evaluated content %q; got:\n%s", loosePath, want, loose)
		}
	}

	// The chart template that consumes the loose file is co-located under the
	// same sub-chart's templates/, emitted verbatim (helm-chart copies the chart;
	// Helm renders `.Files.Get` at install time), so the reference resolves.
	cm, cmPath := findFile(res.Artifacts, "helm-chart/charts/", "templates/configmap.yaml")
	if cm == "" {
		t.Fatalf("chart template configmap.yaml not found; files: %v", sortedKeys(res.Artifacts))
	}
	if !strings.Contains(cm, `.Files.Get "app-config.properties"`) {
		t.Errorf("configmap template (%s) does not reference .Files.Get of the loose file:\n%s", cmPath, cm)
	}

	// The loose file and its consumer template share one sub-chart root.
	looseDir := strings.TrimSuffix(loosePath, "app-config.properties")
	if !strings.HasPrefix(cmPath, looseDir+"templates/") {
		t.Errorf("loose file (%s) and its consumer template (%s) are not in the same sub-chart", loosePath, cmPath)
	}
}

// TestHelmChartFilesGet_Deterministic guards byte-identical output across runs
// for the files-get example — the deterministic-output contract GitOps diffs
// rely on, asserted for the new standalone-inline-artifact placement path.
func TestHelmChartFilesGet_Deterministic(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, helmChartFilesGetInput())
}
