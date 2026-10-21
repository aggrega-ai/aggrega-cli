// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// The narrowing advisory rides on the pillar composition an extends chain
// performs, and after the Component reduction only a Feature chain composes: an
// extending Component authors no pillar content, so it has nothing to redeclare
// and cannot widen anything. These fixtures are therefore Features.
//
// Parent constrains values.x and values.y (minimum 5). The children extend it and
// REDECLARE those properties with a lower minimum (1 < 5) — widening an inherited
// constraint, which the merger flags as a non-halting narrowing notice.
const narrowParentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/narrow-parent
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  schema:
    values:
      x: { type: integer, minimum: 5 }
      y: { type: integer, minimum: 5 }
`

// aaa-narrow widens one inherited constraint (x); zzz-narrow widens two (x, y).
const narrowChildAYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/aaa-narrow
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  extends: test/narrow-parent
  schema:
    values:
      x: { type: integer, minimum: 1 }
`

const narrowChildZYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/zzz-narrow
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  extends: test/narrow-parent
  schema:
    values:
      x: { type: integer, minimum: 1 }
      y: { type: integer, minimum: 1 }
`

// A child that redeclares x at the SAME minimum — no widening, no narrowing.
const narrowChildCleanYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: test/clean-narrow
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  extends: test/narrow-parent
  schema:
    values:
      x: { type: integer, minimum: 5 }
`

// TestLazyLookup_ClosureNarrowingWarnings_Deterministic pins that the closure
// narrowing advisories are (1) produced from ResolveClosureExtends' composition
// (not recomputed), (2) deterministic across runs in BOTH layers — the
// sorted-name closure walk and the per-entity violation slice — and (3) empty for
// a closure with no widening.
func TestLazyLookup_ClosureNarrowingWarnings_Deterministic(t *testing.T) {
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "parent.yaml", narrowParentYAML)
	writeCatalogFile(t, cd, "aaa.yaml", narrowChildAYAML)
	writeCatalogFile(t, cd, "zzz.yaml", narrowChildZYAML)

	run := func() []string {
		lazy := NewLazyLookup(ws, ".aggrega/cache", []string{cd}, nil)
		cl := closure.Closure{Entities: []closure.ResolvedRef{
			// Supplied reverse-sorted; the walk sorts by name.
			{URI: "test/zzz-narrow:1.0.0"},
			{URI: "test/aaa-narrow:1.0.0"},
		}}
		if errs := lazy.ResolveClosureExtends(t.Context(), cl); len(errs) > 0 {
			t.Fatalf("ResolveClosureExtends: %v", errs)
		}
		return lazy.ClosureNarrowingWarnings()
	}

	got1, got2 := run(), run()
	if len(got1) == 0 {
		t.Fatal("expected narrowing notices for widening children; got none")
	}
	// Both layers deterministic: identical output across runs.
	if strings.Join(got1, "\n") != strings.Join(got2, "\n") {
		t.Errorf("non-deterministic narrowing output:\n--run1--\n%s\n--run2--\n%s",
			strings.Join(got1, "\n"), strings.Join(got2, "\n"))
	}
	// Sorted-name walk: aaa-narrow's notice precedes any zzz-narrow notice.
	firstZ := -1
	lastA := -1
	for i, w := range got1 {
		if strings.Contains(w, "test/aaa-narrow") {
			lastA = i
		}
		if strings.Contains(w, "test/zzz-narrow") && firstZ == -1 {
			firstZ = i
		}
	}
	if firstZ == -1 || lastA == -1 || lastA > firstZ {
		t.Errorf("narrowings not in sorted-name order (aaa before zzz): %v", got1)
	}

	// A closure whose only child does not widen → no narrowing notices.
	cleanWs := t.TempDir()
	cleanCd := filepath.Join(cleanWs, "catalog")
	writeCatalogFile(t, cleanCd, "parent.yaml", narrowParentYAML)
	writeCatalogFile(t, cleanCd, "clean.yaml", narrowChildCleanYAML)
	cleanLazy := NewLazyLookup(cleanWs, ".aggrega/cache", []string{cleanCd}, nil)
	cleanCl := closure.Closure{Entities: []closure.ResolvedRef{{URI: "test/clean-narrow:1.0.0"}}}
	if errs := cleanLazy.ResolveClosureExtends(t.Context(), cleanCl); len(errs) > 0 {
		t.Fatalf("ResolveClosureExtends (clean): %v", errs)
	}
	if w := cleanLazy.ClosureNarrowingWarnings(); len(w) != 0 {
		t.Errorf("non-widening closure must emit no narrowing notice; got %v", w)
	}
}
