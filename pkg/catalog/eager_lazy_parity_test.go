// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestEagerLazyResolutionParity is the eager-vs-lazy catalog-resolution
// conformance guard. The pipeline (`RunPipeline`) is shared between the CLI and
// the serve daemon, but each feeds it a `resolver.CatalogLookup` from a different
// backing — eager (`catalog.Load` -> `*Catalog`) for the CLI, lazy (`LazyLookup`)
// for the daemon. The daemon-vs-CLI parity invariant requires those two backings
// to produce EQUIVALENT lookup results for the same workspace; otherwise the IDE
// (daemon-backed) silently diverges from what `build` produces. This test asserts
// that equivalence over a fixture exercising an `extends` chain and a
// cross-library `$ref`, and proves the check is not vacuously green by showing the
// un-primed lazy view (no SchemaLibraries) genuinely differs from eager — the
// exact cross-library schema-default divergence the guard must catch.
//
// Today the daemon reaches SchemaLibrary parity via `Prime` (copying the eager
// `*Catalog`'s libraries). When the shared per-entity resolution lands and `Prime`
// is removed, this test stays green by construction (the lazy view resolves the
// same way) — and grows a reject-parity assertion at that point (an invalid
// library rejected by both backings), which is not meaningfully testable while
// the lazy path resolves libraries only via `Prime`.
func TestEagerLazyResolutionParity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFixture(t, dir, "library.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`)
	writeFixture(t, dir, "base.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/base
  version: 1.0.0
spec:
  schema:
    values:
      common:
        type: string
        default: shared
`)
	writeFixture(t, dir, "child.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/child
  version: 1.0.0
spec:
  extends: core/base
  schema:
    values:
      own:
        type: string
        default: mine
`)
	writeFixture(t, dir, "consumer.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/database/mariadb
  version: 1.0.0
spec:
  schema:
    infra:
      storage:
        $ref: "DbSizing#/schemas/Storage"
`)
	writeFixture(t, dir, "feature.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/observability
  version: 1.0.0
spec:
  schema:
    values:
      enabled:
        type: boolean
        default: true
`)

	cat, err := Load("", dir)
	if err != nil {
		t.Fatalf("catalog Load: %v", err)
	}
	if verrs := cat.ResolveAllExtends(); len(verrs) != 0 {
		t.Fatalf("ResolveAllExtends: %v", verrs)
	}
	eager := cat.CatalogLookup()

	// Primed lazy view — what the daemon actually serves today.
	lazy := NewLazyLookup("", "", []string{dir}, nil)
	lazy.Prime(cat)
	primed := lazy.AsCatalogLookup()

	// --- Resolution parity: eager == primed-lazy for every fixture URI. ---
	components := []string{"core/base", "core/child", "core/database/mariadb"}
	for _, uri := range components {
		ec, eok := eager.Component(uri)
		lc, lok := primed.Component(uri)
		if eok != lok {
			t.Fatalf("Component %q: presence eager=%v lazy=%v", uri, eok, lok)
		}
		if !reflect.DeepEqual(ec, lc) {
			t.Errorf("Component %q diverges between eager and primed-lazy", uri)
		}
	}

	ef, eok := eager.Feature("core/observability")
	lf, lok := primed.Feature("core/observability")
	if eok != lok || !reflect.DeepEqual(ef, lf) {
		t.Errorf("Feature core/observability diverges (eager ok=%v, lazy ok=%v)", eok, lok)
	}

	if !reflect.DeepEqual(eager.SchemaLibraries, primed.SchemaLibraries) {
		t.Errorf("SchemaLibraries diverge between eager and primed-lazy")
	}

	// Sanity: the cross-library `$ref` target is actually present in the eager
	// view (so the canary below is meaningful, not green because there is no
	// library to lose).
	if _, ok := eager.SchemaLibraries["DbSizing"]; !ok {
		t.Fatalf("fixture invalid: eager view is missing the DbSizing SchemaLibrary")
	}

	// --- Non-vacuous canary: the UN-primed lazy view diverges. ---
	// Without Prime the lazy cascade resolves no SchemaLibraries, so the
	// cross-library-`$ref` default's target library is absent — the exact
	// cross-library schema-default divergence the parity guard exists to catch. Asserting
	// the divergence here proves the parity assertions above are earned, not
	// vacuously green.
	unprimed := NewLazyLookup("", "", []string{dir}, nil).AsCatalogLookup()
	if len(unprimed.SchemaLibraries) != 0 {
		t.Fatalf("un-primed lazy view should carry no SchemaLibraries; got %d", len(unprimed.SchemaLibraries))
	}
	if reflect.DeepEqual(eager.SchemaLibraries, unprimed.SchemaLibraries) {
		t.Errorf("canary failed to detect divergence: eager and un-primed-lazy SchemaLibraries compared equal")
	}
}

func writeFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}
