// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// A Component whose schema declares the same property ("port") in both the
// values and infra pillars at the same depth — the schema-pillar-overlap the
// lint flags.
const overlapComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/overlap-comp
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port:
        type: integer
    infra:
      port:
        type: integer
`

// A second overlap component (alphabetically before overlap-comp) for the
// deterministic-order assertion.
const overlapComponentBYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/aaa-overlap
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port:
        type: integer
    infra:
      port:
        type: integer
`

// A clean Component — disjoint pillars, no overlap, no warning.
const cleanComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: test/clean-comp
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      size:
        type: string
    infra:
      replicas:
        type: integer
`

// TestLazyLookup_ClosureLintWarnings_CoversAllClosureEntities pins the cover-all
// behavior: every closure Component/Feature is linted (no partition), a clean
// entity emits nothing, and the URI is the per-entity locator in the notice.
func TestLazyLookup_ClosureLintWarnings_CoversAllClosureEntities(t *testing.T) {
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCatalogFile(t, catalogDir, "overlap.yaml", overlapComponentYAML)
	writeCatalogFile(t, catalogDir, "clean.yaml", cleanComponentYAML)

	lazy := NewLazyLookup(ws, ".aggrega/cache", []string{catalogDir}, nil)

	// A closure carrying both a clean and an overlap-tripping component: only the
	// overlap one produces a notice, and it is linted regardless of being local —
	// the cover-all set, not a registry-only partition.
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "test/clean-comp:1.0.0"},
		{URI: "test/overlap-comp:1.0.0"},
	}}
	warnings := lazy.ClosureLintWarnings(cl)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 overlap warning (overlap component only; clean emits none); got %d: %v",
			len(warnings), warnings)
	}
	// The notice names the kind, the entity name, the URI locator, and the
	// overlapping property.
	for _, want := range []string{"Component", "test/overlap-comp", "test/overlap-comp:1.0.0", "port"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning missing %q: %s", want, warnings[0])
		}
	}

	// A clean-only closure emits nothing (no spurious warning).
	clean := closure.Closure{Entities: []closure.ResolvedRef{{URI: "test/clean-comp:1.0.0"}}}
	if w := lazy.ClosureLintWarnings(clean); len(w) != 0 {
		t.Errorf("clean-only closure must emit no warning; got %v", w)
	}
}

// TestLazyLookup_ClosureLintWarnings_DeterministicOrder pins sorted-by-URI
// emission: two overlap entities surface in URI order regardless of the closure's
// entity order, so the advisory stream is reproducible run to run.
func TestLazyLookup_ClosureLintWarnings_DeterministicOrder(t *testing.T) {
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCatalogFile(t, catalogDir, "a.yaml", overlapComponentBYAML) // test/aaa-overlap
	writeCatalogFile(t, catalogDir, "b.yaml", overlapComponentYAML)  // test/overlap-comp

	lazy := NewLazyLookup(ws, ".aggrega/cache", []string{catalogDir}, nil)

	// Entities supplied in reverse-sorted order; the accessor sorts by URI.
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "test/overlap-comp:1.0.0"},
		{URI: "test/aaa-overlap:1.0.0"},
	}}
	warnings := lazy.ClosureLintWarnings(cl)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings; got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "test/aaa-overlap") || !strings.Contains(warnings[1], "test/overlap-comp") {
		t.Errorf("warnings not in sorted-URI order: %v", warnings)
	}
}
