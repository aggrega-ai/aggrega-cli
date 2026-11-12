// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// Where a component's artifacts land when the target places its contributors flat,
// proved on the arrangement that has no author-side remedy: two plain components in
// one boundary, both bound to one Recipe whose artifacts: block declares a fixed
// filename. The Recipe cannot vary the name — the map key is the output name, taken
// as written and never template-evaluated — and neither component has a surface on
// which to separate them, so without a per-contributor directory the two write one
// path and the build halts on the assembled-output collision.
//
// The two nesting and merging targets are here as controls rather than as coverage.
// A nesting assembler already gives each contributor a directory, and a merging one
// consumes its contributors' artifacts as fragments and places none of them, so
// neither target's output may move: a change reaching either has gone past the
// position it was aimed at. Both run the same two components in one boundary, which
// is the arrangement the flat case fails on, so a control that stays put is saying
// something about this change rather than about an unrelated shape.

// artifactsCollisionFixture names one two-contributors-in-one-boundary run: the
// recipes directory the variant's Manifest maps to, and the Release that puts both
// components in one boundary.
type artifactsCollisionFixture struct {
	name    string
	recipes string
	release string
	// wantPaths is the whole emitted path set in path order, written out rather than
	// composed from the code under test.
	wantPaths []string
}

func artifactsCollisionFixtures() []artifactsCollisionFixture {
	return []artifactsCollisionFixture{
		{
			// The subject. Each component's artifact sits under that component's own
			// name; the manifests the target rendered from the chart stay at the
			// dispatch path, because those already carry their component in the name
			// the target gave them.
			name:    "helm places each contributor's artifacts under that contributor's name",
			recipes: "helm-sourced-with-artifacts",
			release: "helm-artifacts-two-in-one-boundary",
			wantPaths: []string{
				"dist/app/helm/alpha-deployment-app-zone-alpha.yaml",
				"dist/app/helm/alpha-service-app-zone-alpha.yaml",
				"dist/app/helm/alpha/app-config.yaml",
				"dist/app/helm/beta-deployment-app-zone-beta.yaml",
				"dist/app/helm/beta-service-app-zone-beta.yaml",
				"dist/app/helm/beta/app-config.yaml",
			},
		},
		{
			// Control. The assembler nests every contributor, so the artifact was
			// already separated and nothing here may move.
			name:    "helm-chart keeps each contributor in its own chart, artifacts included",
			recipes: "helm-chart-with-artifacts",
			release: "helm-chart-artifacts-two-in-one-boundary",
			wantPaths: []string{
				"dist/app/helm-chart/Chart.yaml",
				"dist/app/helm-chart/charts/alpha/Chart.yaml",
				"dist/app/helm-chart/charts/alpha/templates/api-server-policy.yaml",
				"dist/app/helm-chart/charts/alpha/templates/deployment.yaml",
				"dist/app/helm-chart/charts/alpha/templates/service.yaml",
				"dist/app/helm-chart/charts/alpha/values.yaml",
				"dist/app/helm-chart/charts/beta/Chart.yaml",
				"dist/app/helm-chart/charts/beta/templates/api-server-policy.yaml",
				"dist/app/helm-chart/charts/beta/templates/deployment.yaml",
				"dist/app/helm-chart/charts/beta/templates/service.yaml",
				"dist/app/helm-chart/charts/beta/values.yaml",
				"dist/app/helm-chart/values.yaml",
			},
		},
		{
			// Control. A merging assembler consumes each contributor's artifacts as
			// fragments of the one document it assembles, so no artifact is ever
			// placed as a file and there is nothing for a placement rule to reach.
			name:    "compose merges both contributors' artifacts into one document",
			recipes: "compose-with-artifacts",
			release: "compose-artifacts-two-in-one-boundary",
			wantPaths: []string{
				"dist/app/compose/docker-compose.yaml",
			},
		},
	}
}

func runArtifactsCollisionFixture(t *testing.T, fx artifactsCollisionFixture) map[string][]byte {
	t.Helper()
	res := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/fullstack-app/releases/" + fx.release + ".yaml",
		CatalogPaths: []string{"testdata/fullstack-app/catalog/base", "testdata/fullstack-app/catalog/recipes/" + fx.recipes},
	})
	return res.Artifacts
}

func sortedArtifactKeys(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestTwoContributorsInOneBoundaryEachKeepTheirOwnArtifacts pins each target's whole
// emitted path set. The flat case is the subject; the other two are the controls that
// fail if the change reached past the position it was aimed at.
func TestTwoContributorsInOneBoundaryEachKeepTheirOwnArtifacts(t *testing.T) {
	t.Parallel()

	for _, fx := range artifactsCollisionFixtures() {
		fx := fx
		t.Run(fx.name, func(t *testing.T) {
			t.Parallel()
			got := sortedArtifactKeys(runArtifactsCollisionFixture(t, fx))
			if len(got) != len(fx.wantPaths) {
				t.Fatalf("emitted paths\n got: %v\nwant: %v", got, fx.wantPaths)
			}
			for i := range got {
				if got[i] != fx.wantPaths[i] {
					t.Fatalf("emitted paths\n got: %v\nwant: %v", got, fx.wantPaths)
				}
			}
		})
	}
}

// TestTwoContributorsInOneBoundaryKeepBothArtifactBodies is the half a path set cannot
// state: both artifacts survive with their own content. A path set proves two files
// exist; it does not prove neither was written over the other, which is the failure
// the collision guard was refusing the build to avoid.
func TestTwoContributorsInOneBoundaryKeepBothArtifactBodies(t *testing.T) {
	t.Parallel()

	files := runArtifactsCollisionFixture(t, artifactsCollisionFixtures()[0])
	alpha, okA := files["dist/app/helm/alpha/app-config.yaml"]
	beta, okB := files["dist/app/helm/beta/app-config.yaml"]
	if !okA || !okB {
		t.Fatalf("both artifacts expected; got keys %v", sortedArtifactKeys(files))
	}
	if string(alpha) == string(beta) {
		t.Fatal("the two artifacts are byte-identical, so nothing here would notice one " +
			"contributor's file standing in for the other's")
	}
	// The filename is the author-declared key on both, so the only thing telling the
	// two apart is the body each component's own render produced.
	if !strings.Contains(string(alpha), "SERVICE: alpha") {
		t.Errorf("alpha's artifact does not carry alpha's own rendered content:\n%s", alpha)
	}
	if !strings.Contains(string(beta), "SERVICE: beta") {
		t.Errorf("beta's artifact does not carry beta's own rendered content:\n%s", beta)
	}
}

// TestFlatArtifactsPlacementMovesOnlyArtifactsOriginFiles states the selection rule as
// its own assertion rather than leaving it implied by the pinned path set: a target
// placing contributors flat moves the files its Recipe's artifacts block named, and
// nothing else. The manifests beside them come from the chart the Recipe sources, and
// a change selecting on the node rather than on each file's origin would move those
// too — which the pinned set would also catch, but not say.
func TestFlatArtifactsPlacementMovesOnlyArtifactsOriginFiles(t *testing.T) {
	t.Parallel()

	files := runArtifactsCollisionFixture(t, artifactsCollisionFixtures()[0])
	for _, p := range sortedArtifactKeys(files) {
		rest := strings.TrimPrefix(p, "dist/app/helm/")
		nested := strings.Contains(rest, "/")
		isArtifact := strings.HasSuffix(p, "/app-config.yaml")
		if nested != isArtifact {
			t.Errorf("path %q: nested=%v but artifacts-origin=%v — the placement selected on "+
				"something other than the file's own origin", p, nested, isArtifact)
		}
	}
}
