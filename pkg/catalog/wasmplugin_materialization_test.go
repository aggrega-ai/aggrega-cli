// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// validWasmPlugin is a minimal schema-conforming WasmPlugin entity: the four
// top-level required fields plus the three required spec fields (plugin,
// pluginTypes, abiVersion). The plugin reference is the canonical
// content-addressed oci://<repo>@sha256:<64-hex> form the schema mandates.
const validWasmPlugin = `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata:
  name: core/linter/naming
  version: 1.0.0
spec:
  plugin: "oci://registry.example.com/wasm/linter/naming@sha256:0000000000000000000000000000000000000000000000000000000000000000"
  pluginTypes:
    - linter
  abiVersion: v1alpha1
`

// TestWasmPluginMaterialization proves the WasmPlugin entity is materialized into
// the catalog and retrievable by URI from BOTH backings (eager + lazy), with
// equivalent results, and that a malformed entity is rejected with parity (the
// eager load halts; the lazy enumeration skips it, leaving it absent).
func TestWasmPluginMaterialization(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFixture(t, dir, "naming-linter.yaml", validWasmPlugin)

	// --- Eager: the entity lands in the WasmPlugins map and resolves by URI. ---
	cat, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("eager LoadAll: %v", err)
	}
	if got := len(cat.WasmPlugins); got != 1 {
		t.Fatalf("eager WasmPlugins: want 1 entry, got %d", got)
	}

	eagerBare, ok := cat.WasmPluginByURI("core/linter/naming")
	if !ok {
		t.Fatalf("eager WasmPluginByURI(bare): not found")
	}
	if eagerBare.Spec.Plugin == "" || len(eagerBare.Spec.PluginTypes) != 1 || eagerBare.Spec.PluginTypes[0] != "linter" {
		t.Fatalf("eager WasmPluginByURI(bare): entity body not materialized as authored: %+v", eagerBare.Spec)
	}

	eagerVersioned, ok := cat.WasmPluginByURI("core/linter/naming:1.0.0")
	if !ok {
		t.Fatalf("eager WasmPluginByURI(versioned): not found")
	}
	if !reflect.DeepEqual(eagerBare, eagerVersioned) {
		t.Fatalf("eager WasmPluginByURI: bare and versioned URIs resolve to different entities")
	}

	// A non-existent URI misses cleanly.
	if _, ok := cat.WasmPluginByURI("core/linter/absent"); ok {
		t.Fatalf("eager WasmPluginByURI(absent): unexpectedly found")
	}

	// --- Lazy: same workspace, same entity, by-URI parity with eager. ---
	lazy := NewLazyLookup("", "", []string{dir}, nil)
	lazyBare, ok := lazy.WasmPluginByURI("core/linter/naming")
	if !ok {
		t.Fatalf("lazy WasmPluginByURI(bare): not found")
	}
	if !reflect.DeepEqual(eagerBare, lazyBare) {
		t.Errorf("WasmPlugin diverges between eager and lazy backings:\n eager=%+v\n lazy=%+v", eagerBare, lazyBare)
	}

	lazyVersioned, ok := lazy.WasmPluginByURI("core/linter/naming:1.0.0")
	if !ok {
		t.Fatalf("lazy WasmPluginByURI(versioned): not found")
	}
	if !reflect.DeepEqual(eagerVersioned, lazyVersioned) {
		t.Errorf("WasmPlugin (versioned URI) diverges between eager and lazy backings")
	}
}

// TestWasmPluginRejectParity proves a malformed WasmPlugin is rejected with the
// same effect on both backings: the eager load halts with an envelope violation,
// the lazy enumeration tolerantly skips it (no entry, recorded skip). The test
// first asserts the envelope genuinely rejects the fixture, so the parity check
// is not vacuously green over an accidentally-valid document.
func TestWasmPluginRejectParity(t *testing.T) {
	t.Parallel()

	// Missing the required spec.plugin field; abiVersion not the accepted value.
	const malformed = `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata:
  name: core/linter/broken
  version: 1.0.0
spec:
  pluginTypes:
    - linter
  abiVersion: v0
`

	// The envelope itself rejects the document — the reject-parity below is
	// meaningful only because this fixture is genuinely invalid.
	if _, eerr := envelope.ValidateBytes(envelope.KindWasmPlugin, []byte(malformed), "broken.yaml"); eerr == nil {
		t.Fatalf("fixture invalid: envelope must reject the malformed WasmPlugin (reject-parity is vacuous otherwise)")
	} else if !errors.Is(eerr, envelope.ErrEntityEnvelope) {
		t.Fatalf("envelope rejection should wrap ErrEntityEnvelope; got %v", eerr)
	}

	dir := t.TempDir()
	writeFixture(t, dir, "broken-linter.yaml", malformed)

	// Eager: the fail-fast load halts, surfacing the envelope violation.
	if _, err := LoadAll("", dir); err == nil {
		t.Fatalf("eager LoadAll must halt on a malformed WasmPlugin")
	} else if !errors.Is(err, envelope.ErrEntityEnvelope) {
		t.Errorf("eager load error should wrap ErrEntityEnvelope; got %v", err)
	}

	// Lazy: the tolerant enumeration skips the malformed entity — it is absent
	// from the by-URI surface rather than aborting the walk.
	lazy := NewLazyLookup("", "", []string{dir}, nil)
	if _, ok := lazy.WasmPluginByURI("core/linter/broken"); ok {
		t.Errorf("lazy WasmPluginByURI must miss a malformed (skipped) WasmPlugin")
	}
}
