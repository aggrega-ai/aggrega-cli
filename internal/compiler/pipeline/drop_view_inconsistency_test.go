// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestDropCatalogViewInconsistencies pins the halt-set filter that backs
// Inputs.TolerateCatalogViewInconsistency: a resilient-inspection caller drops
// the resolved-set / view divergence while every other guard violation still
// halts. Order is preserved so the surviving halt renders deterministically.
func TestDropCatalogViewInconsistencies(t *testing.T) {
	viewInconsistency := &resolver.ResolverError{Entity: "A", Field: "f", Message: "absent", Err: resolver.ErrGuardEntityViewInconsistency()}
	realViolation := &resolver.ResolverError{Entity: "B", Field: "g", Message: "bad", Err: resolver.ErrSchemaConformance()}

	t.Run("drops only the view-inconsistency finding, preserving order", func(t *testing.T) {
		kept := dropCatalogViewInconsistencies([]*resolver.ResolverError{realViolation, viewInconsistency, realViolation})
		if len(kept) != 2 {
			t.Fatalf("want 2 surviving violations; got %d (%v)", len(kept), kept)
		}
		for _, e := range kept {
			if errors.Is(e, resolver.ErrGuardEntityViewInconsistency()) {
				t.Fatalf("view-inconsistency finding survived the filter: %v", e)
			}
		}
	})

	t.Run("empties a slice of only view-inconsistency findings", func(t *testing.T) {
		if kept := dropCatalogViewInconsistencies([]*resolver.ResolverError{viewInconsistency}); len(kept) != 0 {
			t.Fatalf("want empty halt set; got %v", kept)
		}
	})

	t.Run("leaves a slice with no view-inconsistency findings intact", func(t *testing.T) {
		if kept := dropCatalogViewInconsistencies([]*resolver.ResolverError{realViolation}); len(kept) != 1 {
			t.Fatalf("want the real violation retained; got %v", kept)
		}
	})
}
