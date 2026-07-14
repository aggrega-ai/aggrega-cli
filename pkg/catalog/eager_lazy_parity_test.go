// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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
  labels:
    component.aggrega.ai/type: server
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
  labels:
    component.aggrega.ai/type: server
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
  name: core/datastore/mariadb
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
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
	writeFixture(t, dir, "plugin.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm-renderer
  version: 1.0.0
spec:
  pluginType: provider
  abiVersion: v1alpha1
  capabilities:
    - helm
  exports:
    - name: render_node
`)

	cat, err := LoadAll("", dir)
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
	components := []string{"core/base", "core/child", "core/datastore/mariadb"}
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

	// Plugin (currently unguarded) and ProviderPlugin parity — both backings must
	// resolve the same Plugin entity for a URI and the same provider→Plugin binding
	// for a provider name, or the daemon's render dispatch silently diverges from
	// the CLI's.
	ep, eok := eager.Plugin("helm-renderer:1.0.0")
	lp, lok := primed.Plugin("helm-renderer:1.0.0")
	if eok != lok || !reflect.DeepEqual(ep, lp) {
		t.Errorf("Plugin helm-renderer:1.0.0 diverges (eager ok=%v, lazy ok=%v)", eok, lok)
	}
	if !eok {
		t.Fatal("fixture invalid: eager view is missing the helm-renderer Plugin (Plugin/ProviderPlugin parity is vacuous otherwise)")
	}

	epp, eok, eerr := eager.ProviderPlugin("helm")
	lpp, lok, lerr := primed.ProviderPlugin("helm")
	if eerr != nil || lerr != nil {
		t.Fatalf("ProviderPlugin helm errored (eager=%v, lazy=%v)", eerr, lerr)
	}
	if eok != lok || !reflect.DeepEqual(epp, lpp) {
		t.Errorf("ProviderPlugin helm diverges (eager ok=%v, lazy ok=%v)", eok, lok)
	}
	if !eok || epp.Metadata.Name != "helm-renderer" {
		t.Errorf("ProviderPlugin(\"helm\"): got (%q, %v); want (helm-renderer, true)", epp.Metadata.Name, eok)
	}

	if !reflect.DeepEqual(eager.SchemaLibraries, primed.SchemaLibraries) {
		t.Errorf("SchemaLibraries diverge between eager and primed-lazy")
	}

	// Sanity: the cross-library `$ref` target is actually present in the eager
	// view (so the parity assertion above is meaningful, not green because there
	// is no library to compare).
	if _, ok := eager.SchemaLibraries["DbSizing"]; !ok {
		t.Fatalf("fixture invalid: eager view is missing the DbSizing SchemaLibrary")
	}

	// Positive semantic invariant — the materialization INPUT is present and correct.
	// The cross-library $ref in core/datastore/mariadb is NOT expanded at this
	// (CatalogLookup) level; it expands downstream at IR build. So a pure eager==lazy
	// DeepEqual over the raw-$ref components could pass even if the resolved library
	// silently lost the default the $ref targets (both identically wrong). Assert the
	// resolved DbSizing.Storage actually carries its size default "10Gi"; a resolution
	// bug that dropped it fails here rather than surfacing as wrong IR downstream. The
	// SchemaLibraries DeepEqual above ties the lazy view to this eager assertion.
	dbSizing := eager.SchemaLibraries["DbSizing"]
	storage, ok := dbSizing.Spec.Schemas.Get("Storage")
	if !ok {
		t.Fatalf("DbSizing library is missing the Storage schema entry")
	}
	storageBody, err := yaml.Marshal(&storage.Content)
	if err != nil {
		t.Fatalf("marshal DbSizing.Storage content: %v", err)
	}
	if !strings.Contains(string(storageBody), "10Gi") {
		t.Errorf("resolved DbSizing.Storage lost its size default %q — the cross-library $ref target's materialization input is not preserved; got:\n%s", "10Gi", storageBody)
	}

	// --- The lazy backing resolves libraries itself, without Prime. ---
	// The lazy backing no longer depends on being Prime-fed eager libraries; an
	// un-primed lazy view walks its catalog dirs and resolves the same library set.
	// This is the post-unification shape: both backings share one resolution.
	unprimed := NewLazyLookup("", "", []string{dir}, nil).AsCatalogLookup()
	if !reflect.DeepEqual(eager.SchemaLibraries, unprimed.SchemaLibraries) {
		t.Errorf("un-primed lazy view should resolve the same SchemaLibraries as eager (no Prime dependency)")
	}

	// --- Non-vacuous reject-parity: an invalid library is rejected by BOTH
	// backings. --- This is what the shared per-entity resolve buys once Prime's
	// library copy is gone: a SchemaLibrary carrying a forbidden JSON Schema
	// keyword that the eager `Load` rejects is rejected by the lazy backing's
	// direct resolution too — the lazy path does not silently accept a library the
	// build halts on. Proves the parity above is earned, not vacuously green.
	rejectDir := t.TempDir()
	writeFixture(t, rejectDir, "bad-library.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Bad
  version: 1.0.0
spec:
  schemas:
    Broken:
      $dynamicAnchor: forbidden
      type: string
`)
	if _, eagerErr := LoadAll("", rejectDir); eagerErr == nil {
		t.Fatalf("fixture invalid: eager Load must reject a library with a forbidden keyword (the reject-parity assertion is vacuous otherwise)")
	}
	rejectLazy := NewLazyLookup("", "", []string{rejectDir}, nil)
	if lazyErr := rejectLazy.buildSchemaLibraries(); lazyErr == nil {
		t.Errorf("lazy backing must reject the same invalid library the eager Load rejects (reject-parity); got no error")
	}
}

func writeFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}
