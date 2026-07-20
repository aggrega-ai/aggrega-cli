// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// TestIntegration_LintRelease_ClosureScoped exercises `aggrega lint --release`
// over a real example's Release (mariadb-adminer's compose catalog, resolved
// against the standard catalog for its hub-provided dependencies): a clean
// closure exits 0, and the mode is genuinely closure-scoped —
// a schema-broken entity OUTSIDE the Release's closure does not fail
// `lint --release` (it is never reached) while whole-catalog lint does catch it.
func TestIntegration_LintRelease_ClosureScoped(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	examplesRoot := localExamplesRoot(t, wd)

	ws := t.TempDir()
	copyTree(t, filepath.Join(examplesRoot, "mariadb-adminer"), ws)
	_ = os.RemoveAll(filepath.Join(ws, ".aggrega"))
	composeCatalog := filepath.Join(ws, "catalog-compose")
	std := standardCatalogDir(t)
	// Point the workspace config at the local compose catalog plus the standard
	// catalog (both absolute), so lint --release resolves the closure from Tier-1
	// with no network/lockfile — the local catalog supplies the example's own
	// entities, the standard catalog supplies its hub-provided dependencies.
	writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs:\n    - "+composeCatalog+"\n    - "+std+"\n")
	t.Chdir(ws)

	// Clean closure → exit 0.
	if err := validateRelease(nil, "release-compose.yaml", nil, compilerops.VerbosityDetailed, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("lint --release over a clean closure must pass; got %v", err)
	}

	// Closure-scoping: a schema-broken entity OUTSIDE the Release's closure (an
	// unresolvable cross-document $ref — parses + indexes, fails Pass-2) is never
	// reached by the closure walk, so lint --release still passes...
	writeFixture(t, filepath.Join(composeCatalog, "component", "orphan.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/orphan-out-of-closure
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)
	if err := validateRelease(nil, "release-compose.yaml", nil, compilerops.VerbosityDetailed, os.Stderr, os.Stdout); err != nil {
		t.Errorf("an out-of-closure broken entity must not fail closure-scoped lint --release; got %v", err)
	}

	// ...but whole-catalog lint DOES catch it — proving the modes scope differently.
	if err := validateCatalogWide("", "", []string{composeCatalog}, os.Stderr, os.Stdout); err == nil {
		t.Error("whole-catalog lint must catch the broken out-of-closure entity (closure-scoping difference unproven)")
	}
}
