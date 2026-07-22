// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof of recipe-declared output labels.
//
// A Recipe's spec.outputLabels ride the same rail as the fixed build-provenance
// labels: they are stamped onto every object the recipe's rendered output
// produces, provider-translated onto each engine's label surface. These tests
// compile through the build-faithful harness (pipelinetest.Build) — the real
// Step 1-5 pipeline with the CLI build's Options — and assert the declared
// labels land on real rendered output across the shipping surfaces:
//
//   - helm workload synthesis: both the object metadata.labels and the pod
//     template metadata.labels (the synth-path provenance placement);
//   - helm artifacts: the emitted artifact manifest's metadata.labels (the
//     host-computed artifacts path);
//   - compose: the service labels map;
//   - the reserved-namespace guardrail halts the build loudly at load.
//
// The fixtures reuse the fullstack-app base catalog; only the recipe layer
// changes (catalog/recipes/*-outputlabels), so no existing golden is perturbed.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The output-label recipe fixtures live under testdata/output-labels (outside
// the fullstack-app catalog tree, which whole-tree LoadAll consumers scan) and
// reuse the fullstack-app base catalog and Releases. The recipes keep the same
// metadata.name the shared Manifests map to, so the Release/Manifest mapping
// resolves against the output-label recipe layer unchanged. The reserved fixture
// (an intentionally invalid recipe) is deliberately isolated so it never enters
// a whole-catalog load.
const olBase = "testdata/fullstack-app/catalog/base"

func helmSynthesizedOutputLabelsInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/fullstack-app/releases/helm-synthesized-with-artifacts.yaml",
		CatalogPaths: []string{olBase, "testdata/output-labels/helm"},
	}
}

func composeOutputLabelsInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/fullstack-app/releases/compose-synthesized.yaml",
		CatalogPaths: []string{olBase, "testdata/output-labels/compose"},
	}
}

func reservedOutputLabelsInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/fullstack-app/releases/helm-synthesized-with-artifacts.yaml",
		CatalogPaths: []string{olBase, "testdata/output-labels/reserved"},
	}
}

// TestOutputLabels_HelmSynthesis_ObjectAndPodTemplate proves the synthesized
// Deployment carries the recipe's output labels on BOTH the object
// metadata.labels and the pod-template metadata.labels — the same dual
// placement the fixed provenance labels get on the synthesis path.
func TestOutputLabels_HelmSynthesis_ObjectAndPodTemplate(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, helmSynthesizedOutputLabelsInput())

	dep, path := findFile(res.Artifacts, "dist/backend/", "backend", "deployment")
	if dep == "" {
		t.Fatalf("backend deployment not found; files: %v", sortedKeys(res.Artifacts))
	}
	// The bare-key label and the namespaced (non-reserved) label both land, and
	// each appears twice: once on the object, once on the pod template.
	if n := strings.Count(dep, "hosting: self-hosted"); n < 2 {
		t.Errorf("expected hosting label on object AND pod template (>=2 occurrences), got %d in %s:\n%s", n, path, dep)
	}
	if n := strings.Count(dep, "example.io/team: platform"); n < 2 {
		t.Errorf("expected namespaced output label on object AND pod template (>=2), got %d in %s", n, path)
	}
	// The output labels coexist with the provenance labels (disjoint merge).
	if !strings.Contains(dep, "aggrega.ai/component-id: backend") {
		t.Errorf("provenance labels must survive the merge in %s:\n%s", path, dep)
	}
}

// TestOutputLabels_HelmArtifacts_HostPath proves the host-computed artifacts
// path stamps the output labels: the app-config ConfigMap (an artifacts: entry)
// carries them on its metadata.labels.
func TestOutputLabels_HelmArtifacts_HostPath(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, helmSynthesizedOutputLabelsInput())

	cm, path := findFile(res.Artifacts, "dist/backend/", "app-config")
	if cm == "" {
		t.Fatalf("backend app-config artifact not found; files: %v", sortedKeys(res.Artifacts))
	}
	if !strings.Contains(cm, "hosting: self-hosted") {
		t.Errorf("artifact ConfigMap missing output label hosting: self-hosted in %s:\n%s", path, cm)
	}
	if !strings.Contains(cm, "example.io/team: platform") {
		t.Errorf("artifact ConfigMap missing namespaced output label in %s:\n%s", path, cm)
	}
}

// TestOutputLabels_Compose_ServiceLabels proves the compose engine folds the
// output labels onto the service labels map alongside provenance.
func TestOutputLabels_Compose_ServiceLabels(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, composeOutputLabelsInput())

	compose, path := findFile(res.Artifacts, "compose", "docker-compose.yaml")
	if compose == "" {
		t.Fatalf("compose output not found; files: %v", sortedKeys(res.Artifacts))
	}
	// Both services (api, db) declare the labels, so each key appears at least
	// twice across the merged compose file.
	if n := strings.Count(compose, "hosting: self-hosted"); n < 2 {
		t.Errorf("compose service labels missing hosting: self-hosted (want >=2, got %d) in %s:\n%s", n, path, compose)
	}
	if n := strings.Count(compose, "example.io/team: platform"); n < 2 {
		t.Errorf("compose service labels missing namespaced output label (want >=2, got %d) in %s", n, path)
	}
}

// TestOutputLabels_ReservedNamespace_HaltsBuild proves a recipe declaring an
// output label under a reserved namespace (aggrega.ai/) halts the build loudly
// at catalog load — the reserved-namespace guardrail fires before any provider
// consumes the labels.
func TestOutputLabels_ReservedNamespace_HaltsBuild(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, reservedOutputLabelsInput())
	if err == nil {
		t.Fatalf("expected the build to halt on a reserved output-label namespace, got nil error")
	}
	// The build halts loudly as a catalog error, and the reserved-namespace
	// sentinel threads through the wrapped chain.
	if !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace) {
		t.Errorf("build error should carry ErrRecipeOutputLabelReservedNamespace; got: %v", err)
	}
	if !strings.Contains(err.Error(), "CATALOG_ERROR") {
		t.Errorf("build error should be a loud CATALOG_ERROR; got: %s", err.Error())
	}
}

// TestOutputLabels_Deterministic asserts the output-label merge preserves the
// byte-identical output contract across runs (Invariant 8).
func TestOutputLabels_Deterministic(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, helmSynthesizedOutputLabelsInput())
}
