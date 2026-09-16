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
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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

// The collision fixture carries exactly one fault — a single recipe naming
// "hosting" in both outputLabels and outputComponentLabels — so the assertion
// below cannot pass on some other rejection. Deliberately isolated from the
// whole-catalog load for the same reason the reserved fixture is.
func collidingOutputLabelsInput() pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/fullstack-app/releases/helm-synthesized-with-artifacts.yaml",
		CatalogPaths: []string{olBase, "testdata/output-labels/collision"},
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

// TestOutputComponentLabels_HelmSynthesis_ValueFollowsTheComponent proves a
// promoted key is resolved by reference: the recipes name the key
// component.aggrega.ai/type and each node's rendered output carries the value
// that key holds on ITS OWN component. api-server declares "server" and postgres
// declares "stateful-server", so two nodes stamped from the same named key
// render two different values — which a literal outputLabels entry could not do.
func TestOutputComponentLabels_HelmSynthesis_ValueFollowsTheComponent(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, helmSynthesizedOutputLabelsInput())

	backend, backendPath := findFile(res.Artifacts, "dist/backend/", "backend", "deployment")
	if backend == "" {
		t.Fatalf("backend deployment not found; files: %v", sortedKeys(res.Artifacts))
	}
	// Object metadata.labels AND pod-template metadata.labels, the same dual
	// placement the literal output labels get on the synthesis path.
	if n := strings.Count(backend, "component.aggrega.ai/type: server"); n < 2 {
		t.Errorf("backend must carry its own promoted value on object AND pod template (want >=2, got %d) in %s:\n%s", n, backendPath, backend)
	}
	if strings.Contains(backend, "component.aggrega.ai/type: stateful-server") {
		t.Errorf("backend must not carry the datastore's promoted value in %s:\n%s", backendPath, backend)
	}

	data, dataPath := findFile(res.Artifacts, "dist/data/", "deployment")
	if data == "" {
		t.Fatalf("data deployment not found; files: %v", sortedKeys(res.Artifacts))
	}
	if n := strings.Count(data, "component.aggrega.ai/type: stateful-server"); n < 2 {
		t.Errorf("data node must carry its own promoted value (want >=2, got %d) in %s:\n%s", n, dataPath, data)
	}
	// The promoted key coexists with the literal output labels and with the
	// compiler-owned provenance labels.
	if !strings.Contains(data, "hosting: self-hosted") || !strings.Contains(data, "aggrega.ai/component-id: data-zdmain-db") {
		t.Errorf("promoted, literal and provenance labels must coexist in %s:\n%s", dataPath, data)
	}
}

// TestOutputComponentLabels_HelmArtifacts_HostPath proves the host-computed
// artifacts path stamps the promoted key too, so the promotion is not confined
// to workload synthesis.
func TestOutputComponentLabels_HelmArtifacts_HostPath(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, helmSynthesizedOutputLabelsInput())

	cm, path := findFile(res.Artifacts, "dist/backend/", "app-config")
	if cm == "" {
		t.Fatalf("backend app-config artifact not found; files: %v", sortedKeys(res.Artifacts))
	}
	if !strings.Contains(cm, "component.aggrega.ai/type: server") {
		t.Errorf("artifact ConfigMap missing the promoted label in %s:\n%s", path, cm)
	}
}

// TestOutputComponentLabels_Compose_ServiceLabels proves the promotion reaches a
// non-helm surface unchanged — the compose service labels map — which is what
// makes it a property of the shared label rail rather than of one provider.
func TestOutputComponentLabels_Compose_ServiceLabels(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, composeOutputLabelsInput())

	compose, path := findFile(res.Artifacts, "compose", "docker-compose.yaml")
	if compose == "" {
		t.Fatalf("compose output not found; files: %v", sortedKeys(res.Artifacts))
	}
	// api is core/compute/api-server ("server"), db is core/datastore/postgres
	// ("stateful-server") — one promoted key, two values, one compose file.
	if !strings.Contains(compose, "component.aggrega.ai/type: server") {
		t.Errorf("compose service labels missing the api service's promoted value in %s:\n%s", path, compose)
	}
	if !strings.Contains(compose, "component.aggrega.ai/type: stateful-server") {
		t.Errorf("compose service labels missing the db service's promoted value in %s:\n%s", path, compose)
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
	if !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
		t.Errorf("build error should carry ErrRecipeOutputLabelReservedNamespace; got: %v", err)
	}
	if !strings.Contains(err.Error(), "CATALOG_ERROR") {
		t.Errorf("build error should be a loud CATALOG_ERROR; got: %s", err.Error())
	}
}

// TestOutputComponentLabels_CollidesWithLiteral_HaltsBuild proves the one-source
// rule is enforced on the real build path, not only in isolation: a recipe naming
// one key both literally and as a promoted key halts the build at catalog load
// with the collision sentinel, so a stamped key can never have two sources.
func TestOutputComponentLabels_CollidesWithLiteral_HaltsBuild(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, collidingOutputLabelsInput())
	if err == nil {
		t.Fatalf("expected the build to halt on a promoted/literal key collision, got nil error")
	}
	if !errors.Is(err, validator.ErrRecipeOutputComponentLabelCollision()) {
		t.Errorf("build error should carry ErrRecipeOutputComponentLabelCollision; got: %v", err)
	}
	if !strings.Contains(err.Error(), "CATALOG_ERROR") {
		t.Errorf("build error should be a loud CATALOG_ERROR; got: %s", err.Error())
	}
	// This diagnostic carries its position in two halves, so the assertions
	// below read two surfaces rather than one. The file a reader opens rides
	// the chain frame that carries the source location, and cerrors.CauseChain
	// is what flattens the whole chain onto one line — each frame's own Error()
	// reports itself alone, so reading any single frame drops the other half.
	// The offending key and the two block names ride the validation detail,
	// which is the sentence saying what to change. Asserting the file against
	// the detail would ask the half holding the authored key to supply the
	// half holding the document.
	var ve *validator.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("the build error must carry the validation detail; got: %v", err)
	}
	for _, want := range []string{`"hosting"`, "outputComponentLabels", "outputLabels"} {
		if !strings.Contains(ve.Error(), want) {
			t.Errorf("the validation detail must name %s; got: %s", want, ve.Error())
		}
	}
	if chain := cerrors.CauseChain(err); !strings.Contains(chain, "api-server.yaml") {
		t.Errorf("the rendered chain must name the file to open, api-server.yaml; got: %s", chain)
	}
}

// TestOutputLabels_Deterministic asserts the output-label merge preserves the
// byte-identical output contract across runs (Invariant 8).
func TestOutputLabels_Deterministic(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, helmSynthesizedOutputLabelsInput())
}

// TestGPUSynthesis_ExtendedResourceAndRuntimeClass_E2E proves the full build
// pipeline — including the host→guest render boundary where the Workload crosses
// the codec as a WorkloadProjection — carries a container's extended-resource
// limit (nvidia.com/gpu) and the pod-level runtimeClassName all the way into the
// rendered helm Deployment. This is the end-to-end proof that the schema
// relaxation, the pkg/types codec, and the helm synth wiring compose correctly
// through the real guest, not only in isolation.
func TestGPUSynthesis_ExtendedResourceAndRuntimeClass_E2E(t *testing.T) {
	t.Parallel()
	res := pipelinetest.Build(t, helmSynthesizedOutputLabelsInput())

	dep, path := findFile(res.Artifacts, "dist/data/", "deployment")
	if dep == "" {
		t.Fatalf("data deployment not found; files: %v", sortedKeys(res.Artifacts))
	}
	if !strings.Contains(dep, "nvidia.com/gpu: \"1\"") {
		t.Errorf("rendered Deployment must carry the extended-resource limit nvidia.com/gpu: \"1\" in %s:\n%s", path, dep)
	}
	if !strings.Contains(dep, "runtimeClassName: nvidia") {
		t.Errorf("rendered Deployment pod spec must carry runtimeClassName: nvidia in %s:\n%s", path, dep)
	}
	// The named quantities still translate alongside the extended key.
	if !strings.Contains(dep, "cpu: \"2\"") || !strings.Contains(dep, "memory: 8Gi") {
		t.Errorf("cpu/memory must survive alongside the extended key in %s:\n%s", path, dep)
	}
}
