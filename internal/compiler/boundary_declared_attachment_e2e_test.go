// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// Three things are independent, and this file exists because running them
// together produced a build that refused legal authoring:
//
//   - WHERE the attachment is declared — on a component, or on a boundary, where
//     it reaches every component nested inside.
//   - WHAT SCOPE the bound FeatureRecipe generates at — once per component, which
//     is what a recipe stating no scope does, or once for the boundary.
//   - WHAT A STEP ADDRESSES — the attachment's own documents, or its host
//     component's, which the step's own plugin declares.
//
// A boundary-DECLARED attachment of a component-SCOPE FeatureRecipe generates
// once for each component the boundary holds, and each generation stands against
// the component it generated for. So its /host step has a host, and the build
// must complete. A boundary-SCOPE FeatureRecipe stands against no component
// however it was declared, so its /host step addresses nothing and the build
// must halt.
//
// The two cases are asserted together, and deliberately: a relaxation that let
// the first through by no longer refusing the second would have replaced a wrong
// halt with a missing guardrail, which is the worse of the two.

// boundaryDeclaredFixtureInput names the host-patch-helmchart fixture with the
// attachment written on the BOUNDARY rather than on the component, binding the
// component-scope FeatureRecipe whose pipeline runs a /host PATCH.
//
// It stages the same co-located catalog the component-declared input stages, so
// the two differ in the authored documents alone and never in how the guest was
// resolved.
func boundaryDeclaredFixtureInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	return pipelinetest.Input{
		ReleasePath:  "testdata/host-patch-helmchart/release-boundary-declared.yaml",
		CatalogPaths: hostPatchHelmchartCatalog(t),
	}
}

// hostPatchHelmchartCatalog stages the shared host-patch-helmchart catalog with
// its guest laid down beside the Plugin entity that declares it.
func hostPatchHelmchartCatalog(t *testing.T) []string {
	t.Helper()
	return stageColocatedCatalog(t, []string{
		"testdata/host-patch-helmchart/catalog/base",
		"testdata/host-patch-helmchart/catalog/recipes/helm-chart",
	}, map[string]string{
		"pipeline-step/host-patch": "frp-step-host-patch-rust.wasm",
	})
}

// TestBoundaryDeclaredComponentScopeAttachment_HostPatchReachesHost compiles a
// boundary-declared attachment of a component-scope FeatureRecipe and asserts
// the build completes AND that the pipeline's /host PATCH reached the host
// component's rendered bytes.
//
// The byte assertion is the load-bearing half. A build that merely completes
// would also be satisfied by an attachment the boundary declared and nothing
// ever generated for, which is the outcome this fixture exists to tell apart
// from the working one.
func TestBoundaryDeclaredComponentScopeAttachment_HostPatchReachesHost(t *testing.T) {
	in := boundaryDeclaredFixtureInput(t)
	t.Parallel()

	result := pipelinetest.Build(t, in)

	// The /host PATCH ran against the component the boundary holds: the host
	// chart's values.yaml carries the injected env var.
	values := string(findArtifactBySuffix(t, result.Artifacts, "charts/api/values.yaml"))
	if !strings.Contains(values, "INJECTED_BY_FEATURE: frp-step-host-patch") {
		t.Errorf("the boundary-declared attachment's /host PATCH did not reach the host's "+
			"rendered values; got:\n%s", values)
	}
	// The surgical edit left the rest of the document alone, exactly as it does
	// when the same attachment is written on the component.
	if !strings.Contains(values, "EXISTING_VAR: 'keep-me'") {
		t.Errorf("host values.yaml lost a pre-existing env var after PATCH; got:\n%s", values)
	}

	// The attachment generated for the component and its own output landed under
	// that component, which is what says the recipe ran per component rather than
	// once for the boundary.
	guardrail := string(findArtifactBySuffix(t, result.Artifacts,
		"charts/api--guardrail/guardrail-config.yaml"))
	if !strings.Contains(guardrail, "kind: ConfigMap") {
		t.Errorf("the attachment's own output is not the resource it declares; got:\n%s", guardrail)
	}
}

// TestBoundaryScopeAttachment_HostStepStillHalts compiles the same
// boundary-declared attachment bound to a BOUNDARY-scope FeatureRecipe and
// asserts the build still halts on the step addressing host documents.
//
// It is the negative half of the pair above and carries one fault: the recipe
// generates once for the boundary, so it stands against no component and its
// /host step addresses a document set that site never has. Asserted against the
// condition's own exported handle rather than against its message or its
// registered code, either of which keeps compiling after the condition moves.
func TestBoundaryScopeAttachment_HostStepStillHalts(t *testing.T) {
	in := pipelinetest.Input{
		ReleasePath:  "testdata/host-patch-helmchart/release-boundary-scope.yaml",
		CatalogPaths: hostPatchHelmchartCatalog(t),
	}
	t.Parallel()

	_, err := pipelinetest.BuildE(t, in)
	if err == nil {
		t.Fatal("a boundary-scope FeatureRecipe whose pipeline addresses host documents must halt: " +
			"it stands against no component, so there is no host document set for the step to reach")
	}
	if !errors.Is(err, renderer.ErrPipelineHostScopeUnavailable()) {
		t.Fatalf("want ErrPipelineHostScopeUnavailable for a boundary-scope /host step; got %v", err)
	}
}
