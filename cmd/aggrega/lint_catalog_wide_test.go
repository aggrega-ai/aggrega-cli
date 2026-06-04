// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"path/filepath"
	"testing"
)

// TestLintCatalogWide_CatchesBrokenEntity pins the catalog-wide validation
// home: `aggrega lint` with no entity argument loads every catalog entity and
// runs the schema-pillar (Pass-2) + library resolution/rejection checks, so a
// broken entity — here a Component whose schema carries an unresolvable
// cross-document $ref — fails loudly. This is the surface a broken catalog
// entity is meant to surface on (build, by contrast, only validates a Release's
// closure).
func TestLintCatalogWide_CatchesBrokenEntity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)
	if err := lintCatalogWide("", "", []string{dir}); err == nil {
		t.Fatal("expected catalog validation to fail on the unresolvable cross-document $ref; got nil")
	}
}

// TestLintCatalogWide_PassesValidCatalog is the non-vacuity companion: the same
// shape WITHOUT the broken reference validates clean — so the failure above is
// the unresolved $ref, not an unconditional error.
func TestLintCatalogWide_PassesValidCatalog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	if err := lintCatalogWide("", "", []string{dir}); err != nil {
		t.Fatalf("valid catalog should pass catalog-wide validation; got %v", err)
	}
}
