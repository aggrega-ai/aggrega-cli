// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a scope-attached feature's
// `uses` block projects through the full build pipeline to rendered, injected
// provides.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestScopeFeatureUses_RendersInjectedProvides closes the render-level loop for
// the projected case: a feature attached subsystem-wide carries a `uses` edge
// (backend → db, injecting db's `host` provides as DB_HOST), and that edge must
// project to the subsystem's component (api) all the way to rendered output —
// api's DB_HOST environment variable carries db's host.
//
// It compiles through the build-faithful harness (pipelinetest.Build), so it
// runs the real pipeline with the CLI build's Options — including
// resolution-completeness. That means every feature instance, the scope feature
// `core/shared-backend` included, must resolve to a FeatureRecipe; shared-backend
// resolves to its no-op FR (a pure-wiring feature still resolves, uniformly).
// The api-recipe renders DB_HOST from `.context.inject.env.DB_HOST`; with no
// bridge FR in this fixture, the value is db's plain host provides (db's instance
// id, "db"), proving the injection came from the projected edge rather than any
// per-consumer rewrite. Neutralizing the scope projection drops the edge, so
// DB_HOST renders empty — the green is non-vacuous.
func TestScopeFeatureUses_RendersInjectedProvides(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/scope-feature-uses/release.yaml",
		CatalogPaths: []string{"testdata/scope-feature-uses/catalog/base"},
	})

	const wantPath = "dist/cluster/compose/docker-compose.yaml"
	body, ok := result.Artifacts[wantPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		t.Fatalf("expected merged compose at %q; got paths %v", wantPath, paths)
	}
	merged := string(body)

	for _, svcName := range []string{"api:", "db:"} {
		if !strings.Contains(merged, svcName) {
			t.Errorf("merged docker-compose.yaml missing service %q\n--- body ---\n%s", svcName, merged)
		}
	}

	// The projected edge injected db's host provides into api's DB_HOST. With no
	// bridge rewrite, the value is db's instance id ("db"). An empty/absent
	// DB_HOST would mean the projection did not reach render.
	if !strings.Contains(merged, "DB_HOST: db") {
		t.Errorf("expected api's DB_HOST to be injected with db's host provides ('db') via the subsystem feature's projected uses edge:\n%s", merged)
	}
}
