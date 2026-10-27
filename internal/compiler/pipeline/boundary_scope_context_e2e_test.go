// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof, through the real pipeline and the shipped helm guest, that a
// boundary-scope FeatureRecipe renders by its boundary's own placement and not by
// the placement of whichever component's turn dispatched it.

package pipeline_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const boundaryScopeContextDir = "testdata/boundary-scope-context/"

// TestABoundaryScopeFeatureRecipeRendersByItsBoundary builds a workspace whose
// boundary writes aggrega.ai/namespace and aggrega.ai/image-registry, and whose one
// component's Release entry writes both again with other values. The boundary
// carries a boundary-scope attachment whose Workload writes neither, so the
// provider places it from the context it is handed.
//
// The component renders by its own entry; the boundary-wide attachment renders by
// the boundary, although that same component's turn is what dispatches it. A
// component's entry therefore never decides where a boundary-wide render lands.
//
// 🔴 PLANTED DEFECT: in dispatchBoundaryScopeFR (internal/compiler/renderer/
// boundary_fr.go) delete the line handing the render the boundary's context, so it
// keeps the dispatching component's: the attachment row renders component-ns and
// component.registry.example and fails. Dropping the release bucket from
// buildBoundaryFRRenderCtx instead makes it render no namespace and fails too.
func TestABoundaryScopeFeatureRecipeRendersByItsBoundary(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  boundaryScopeContextDir + "release.yaml",
		CatalogPaths: []string{boundaryScopeContextDir + "catalog/base"},
	})
	if result == nil {
		t.Fatal("the build returned no result")
	}

	rows := []struct {
		name          string
		fileSuffix    string
		wantNamespace string
		wantImage     string
		foreign       []string
	}{
		{"the component renders by its own entry", "/served-deployment.yaml",
			"component-ns", "component.registry.example/corp/served:v1",
			[]string{"boundary-ns", "boundary.registry.example"}},
		{"the boundary-wide attachment renders by the boundary", "--zone--agent-deployment.yaml",
			"boundary-ns", "boundary.registry.example/corp/agent:v1",
			[]string{"component-ns", "component.registry.example"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			body := artifactEndingIn(t, result.Artifacts, row.fileSuffix)
			if !strings.Contains(body, "\n  namespace: "+row.wantNamespace+"\n") {
				t.Errorf("namespace %q not rendered:\n%s", row.wantNamespace, body)
			}
			if !strings.Contains(body, "image: "+row.wantImage+"\n") {
				t.Errorf("image %q not rendered:\n%s", row.wantImage, body)
			}
			for _, f := range row.foreign {
				if strings.Contains(body, f) {
					t.Errorf("a value from the other placement, %q, reached this render:\n%s", f, body)
				}
			}
		})
	}
}
