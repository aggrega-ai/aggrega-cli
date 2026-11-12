// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestIntegration_LintRelease_ExtendsInheritsBaseConstraint proves, at the
// altitude of the real command and through the closure path a Release lint
// actually walks, that a constraint declared on a base Component is enforced on
// an extending Component's instance. The extending Component authors no pillar
// content, so nothing but inheritance can make this halt: a synthetic local
// catalog supplies a base carrying the constraint plus a child that declares only
// the inheritance marker, and the standard catalog supplies the compose provider
// the recipe targets.
//
// The opposite shape — a child REDECLARING its base's constraint more loosely,
// surfaced as an advisory notice — is not authorable: an extending Component
// declares `schema: inherited` and authors no pillar content of its own, so that
// advisory has no Component-side producer to exercise.
func TestIntegration_LintRelease_ExtendsInheritsBaseConstraint(t *testing.T) {
	std := standardCatalogDir(t)
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")

	writeFixture(t, filepath.Join(cd, "parent.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/lint-parent
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      x: { type: integer, minimum: 5 }
`)
	// The child extends the parent and authors no pillar content, so the parent's
	// x and its minimum are the child's contract.
	writeFixture(t, filepath.Join(cd, "child.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/lint-child
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: test/lint-parent
  schema: inherited
`)
	writeFixture(t, filepath.Join(cd, "recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: lint-child
  version: "1.0.0"
spec:
  capability: compose
  implements: test/lint-child
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: busybox:1
`)
	writeFixture(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/lint-child
      values:
        x: 3
`)
	// x=3 violates the parent's minimum 5, and the child never authored x — so the
	// only way this halts is if the child inherited the parent's constraint.
	writeFixture(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/lint-child:1.0.0: compose/lint-child:1.0.0
`)
	writeFixture(t, filepath.Join(ws, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
	// catalog_dirs: the local extends catalog (first) + the standard catalog for
	// the compose provider the recipe targets.
	writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs:\n    - "+cd+"\n    - "+std+"\n")
	t.Chdir(ws)

	var buf bytes.Buffer
	err := validateRelease(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", nil, compilerops.VerbosityDetailed, false, false, &buf, io.Discard)
	out := buf.String()

	if err == nil {
		t.Fatalf("x=3 violates the base's minimum 5, inherited by the child; lint --release must halt. diagnostics:\n%s", out)
	}
	// One assertion carries all three things this test has to show, and carries
	// them together rather than as three independent searches of the stream. The
	// diagnostic must open with the conformance condition's own sentence; it must
	// name the inherited minimum, which is the only evidence that the base's
	// constraint is the one being applied; and it must be positioned at the child
	// instance's own offending key rather than anywhere the base is named.
	// Requiring all three on the one composed line is what rules out a conformance
	// diagnostic about a different key that merely mentions this one somewhere
	// else in the stream.
	assertComposedDiagnostic(t, out, resolver.ErrSchemaConformance(),
		`at .spec.components.app.values.x`,
		`is less than 5`)
}
