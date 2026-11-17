// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestAClosureEntityOfAnotherKindReadsNoComponentOfItsName drives the readings that
// take a closure's Components from the catalog — the eager and the lazy
// closure-scoped extends resolution and pillar validation, and the lazy view's
// ClosureComponent — over a catalog holding three Components: core/child-absent,
// whose extends parent the catalog does not hold, so resolving its chain is
// refused; core/broken-ref, whose schema names a library no catalog holds, so
// validating its pillars is refused; and core/base, which extends nothing.
//
// Each closure reaches its Component once — core/child-absent:1.0.0 for the
// extends readings, core/broken-ref:1.0.0 for the pillar readings, core/base:1.0.0
// for the direct read — collected as the kind each row names. Collected as a
// Component, every reading reaches the Component and the refusal fires; collected
// with no kind, every reading may read it as a Component. Collected as a Recipe or
// a Manifest, the closure reached another entity that shares the name: no reading
// reaches the Component, so a Component the Release never used is neither resolved
// nor refused on its behalf.
func TestAClosureEntityOfAnotherKindReadsNoComponentOfItsName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		hint          string
		readComponent bool
	}{
		{hint: closure.KindComponent, readComponent: true},
		{hint: "", readComponent: true},
		{hint: closure.KindRecipe, readComponent: false},
		{hint: closure.KindManifest, readComponent: false},
	} {
		t.Run("collected as "+tc.hint, func(t *testing.T) {
			t.Parallel()
			ws, cd := unsatisfiedPinWorkspace(t)
			// A Component whose schema names a library no catalog holds: closure-scoped
			// pillar validation refuses it wherever it reads it.
			writeCatalogFile(t, cd, "broken-ref.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/broken-ref
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)
			// The eager catalog is read without the whole-catalog pillar pass, which
			// would refuse core/broken-ref before any closure-scoped reading ran: the
			// closure-scoped readings are what decide whether that Component is read.
			eager, _, err := loadAllPassOneImpl(ws, "", nil, false, cd)
			if err != nil {
				t.Fatalf("catalog load: %v", err)
			}
			lazy := NewLazyLookup(ws, ".aggrega/cache", []string{cd}, nil)
			ref := closure.ResolvedRef{URI: "core/child-absent:1.0.0", KindHint: tc.hint}
			cl := closure.Closure{Entities: []closure.ResolvedRef{ref}}

			// The direct read is of core/base, which extends nothing, so whether it is
			// read does not depend on a chain resolving.
			_, read := lazy.ClosureComponent(t.Context(), closure.ResolvedRef{URI: "core/base:1.0.0", KindHint: tc.hint})
			pillars := closure.Closure{Entities: []closure.ResolvedRef{{URI: "core/broken-ref:1.0.0", KindHint: tc.hint}}}
			readings := map[string]bool{
				"eager closure extends refused": len(eager.ResolveClosureExtends(cl)) > 0,
				"lazy closure extends refused":  len(lazy.ResolveClosureExtends(t.Context(), cl, lazy.DefaultLibraries().Schemas)) > 0,
				"lazy ClosureComponent read":    read,
				"eager closure pillars refused": eager.ValidateClosureScopedPillars(pillars) != nil,
				"lazy closure pillars refused":  lazy.ValidateClosureScopedPillars(t.Context(), pillars, lazy.DefaultLibraries().Schemas) != nil,
			}
			for name, got := range readings {
				if got != tc.readComponent {
					t.Errorf("%s = %v; want %v (the Component is read only for an entity collected as one, or with no kind)",
						name, got, tc.readComponent)
				}
			}
		})
	}
}
