// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// The advisory accumulators are appended to by passes that nothing stops a
// caller running twice on one catalog. Each pass therefore clears what it
// contributed on entry. These tests are the needles for that: they run each pass
// twice and assert the advisory set is identical, which fails outright if a clear
// is dropped.
//
// The assertion is on the whole slice, not on its length, so it also catches the
// wrong fix — de-duplicating in the accessor would make the counts agree while
// silently collapsing two genuinely identical advisories raised by two different
// entities.

// TestResolveAllExtends_NarrowingAdvisoriesDoNotAccumulate covers the eager
// whole-catalog path. It also pins that clearing the narrowing advisories leaves
// the pillar-overlap advisories the earlier load pass contributed intact — the
// two live in separate fields precisely so one pass's clear cannot erase the
// other's findings.
func TestResolveAllExtends_NarrowingAdvisoriesDoNotAccumulate(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "parent.yaml", narrowParentYAML)
	writeCatalogFile(t, cd, "zzz.yaml", narrowChildZYAML)

	cat, err := LoadAll("", cd)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if errs := cat.ResolveAllExtends(); len(errs) > 0 {
		t.Fatalf("first extends resolution: %v", errs)
	}
	first := strings.Join(cat.LintWarnings(), "\n")
	// Non-vacuity: an empty advisory set would satisfy the equality below for the
	// wrong reason.
	if first == "" {
		t.Fatal("fixture produced no narrowing advisory; the needle is blunt, not the code correct")
	}

	if errs := cat.ResolveAllExtends(); len(errs) > 0 {
		t.Fatalf("second extends resolution: %v", errs)
	}
	if second := strings.Join(cat.LintWarnings(), "\n"); second != first {
		t.Errorf("a second extends resolution changed the advisory set.\n--- after one ---\n%s\n--- after two ---\n%s",
			first, second)
	}
}

// TestValidateSchemaPillarsPass_OverlapAdvisoriesDoNotAccumulate covers the load
// pass's own accumulator on the same principle.
func TestValidateSchemaPillarsPass_OverlapAdvisoriesDoNotAccumulate(t *testing.T) {
	t.Parallel()
	const overlapFeature = `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: test/pillar-overlap
  version: "1.0.0"
  labels:
    feature.aggrega.ai/type: policy
spec:
  schema:
    values:
      shared: { type: string }
    infra:
      shared: { type: string }
`
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "overlap.yaml", overlapFeature)

	cat, err := LoadAll("", cd)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	first := strings.Join(cat.LintWarnings(), "\n")
	if !strings.Contains(first, "shared") {
		t.Fatalf("fixture produced no pillar-overlap advisory; the needle is blunt: %q", first)
	}
	if err := cat.validateSchemaPillarsPass(); err != nil {
		t.Fatalf("second pillar pass: %v", err)
	}
	if second := strings.Join(cat.LintWarnings(), "\n"); second != first {
		t.Errorf("a second pillar pass changed the advisory set.\n--- after one ---\n%s\n--- after two ---\n%s",
			first, second)
	}
}

// TestResolveClosureExtends_NarrowingAdvisoriesDoNotAccumulate covers the lazy
// path, which carried the same defect and is the one where it is most reachable:
// the daemon is long-lived and resolves a closure's chains on every inspection,
// so an unbounded accumulator grows for as long as the session lasts.
func TestResolveClosureExtends_NarrowingAdvisoriesDoNotAccumulate(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	cd := filepath.Join(ws, "catalog")
	writeCatalogFile(t, cd, "parent.yaml", narrowParentYAML)
	writeCatalogFile(t, cd, "zzz.yaml", narrowChildZYAML)

	lazy := NewLazyLookup(ws, ".aggrega/cache", []string{cd}, nil)
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: "test/zzz-narrow:1.0.0"}}}

	if errs := lazy.ResolveClosureExtends(t.Context(), cl, lazy.DefaultLibraries().Schemas); len(errs) > 0 {
		t.Fatalf("first closure extends: %v", errs)
	}
	first := strings.Join(lazy.ClosureNarrowingWarnings(), "\n")
	if first == "" {
		t.Fatal("fixture produced no narrowing advisory; the needle is blunt, not the code correct")
	}

	// Three runs, not two: an off-by-one clear that trimmed only the previous run
	// would still pass a two-run comparison.
	for i := 0; i < 2; i++ {
		if errs := lazy.ResolveClosureExtends(t.Context(), cl, lazy.DefaultLibraries().Schemas); len(errs) > 0 {
			t.Fatalf("repeat closure extends %d: %v", i, errs)
		}
	}
	if got := strings.Join(lazy.ClosureNarrowingWarnings(), "\n"); got != first {
		t.Errorf("repeated closure extends changed the advisory set.\n--- after one ---\n%s\n--- after three ---\n%s",
			first, got)
	}
}
