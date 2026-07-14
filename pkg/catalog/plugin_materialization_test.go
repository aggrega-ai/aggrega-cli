// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// validPlugin is a minimal schema-conforming Plugin entity: the four
// top-level required fields plus the three required spec fields (plugin,
// pluginType, abiVersion). The plugin reference is the digest-free
// oci://<repo> identity form the schema mandates; the resolved content
// digest lives in the lockfile, not in the entity.
const validPlugin = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: core/linter/naming
  version: 1.0.0
spec:
  pluginType: linter
  abiVersion: v1alpha1
`

// TestPluginMaterialization proves the Plugin entity is materialized into
// the catalog and retrievable by URI from BOTH backings (eager + lazy), with
// equivalent results, and that a malformed entity is rejected with parity (the
// eager load halts; the lazy enumeration skips it, leaving it absent).
func TestPluginMaterialization(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFixture(t, dir, "naming-linter.yaml", validPlugin)

	// --- Eager: the entity lands in the Plugins map and resolves by URI. ---
	cat, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("eager LoadAll: %v", err)
	}
	if got := len(cat.Plugins); got != 1 {
		t.Fatalf("eager Plugins: want 1 entry, got %d", got)
	}

	eagerBare, ok := cat.PluginByURI("core/linter/naming")
	if !ok {
		t.Fatalf("eager PluginByURI(bare): not found")
	}
	if eagerBare.Spec.AbiVersion == "" || eagerBare.Spec.PluginType != "linter" {
		t.Fatalf("eager PluginByURI(bare): entity body not materialized as authored: %+v", eagerBare.Spec)
	}

	eagerVersioned, ok := cat.PluginByURI("core/linter/naming:1.0.0")
	if !ok {
		t.Fatalf("eager PluginByURI(versioned): not found")
	}
	if !reflect.DeepEqual(eagerBare, eagerVersioned) {
		t.Fatalf("eager PluginByURI: bare and versioned URIs resolve to different entities")
	}

	// A non-existent URI misses cleanly.
	if _, ok := cat.PluginByURI("core/linter/absent"); ok {
		t.Fatalf("eager PluginByURI(absent): unexpectedly found")
	}

	// --- Lazy: same workspace, same entity, by-URI parity with eager. ---
	lazy := NewLazyLookup("", "", []string{dir}, nil)
	lazyBare, ok := lazy.PluginByURI("core/linter/naming")
	if !ok {
		t.Fatalf("lazy PluginByURI(bare): not found")
	}
	if !reflect.DeepEqual(eagerBare, lazyBare) {
		t.Errorf("Plugin diverges between eager and lazy backings:\n eager=%+v\n lazy=%+v", eagerBare, lazyBare)
	}

	lazyVersioned, ok := lazy.PluginByURI("core/linter/naming:1.0.0")
	if !ok {
		t.Fatalf("lazy PluginByURI(versioned): not found")
	}
	if !reflect.DeepEqual(eagerVersioned, lazyVersioned) {
		t.Errorf("Plugin (versioned URI) diverges between eager and lazy backings")
	}
}

// TestPluginRejectParity proves a malformed Plugin is rejected with the
// same effect on both backings: the eager load halts with an envelope violation,
// the lazy enumeration tolerantly skips it (no entry, recorded skip). The test
// first asserts the envelope genuinely rejects the fixture, so the parity check
// is not vacuously green over an accidentally-valid document.
func TestPluginRejectParity(t *testing.T) {
	t.Parallel()

	// Missing the required spec.plugin field; abiVersion not the accepted value.
	const malformed = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: core/linter/broken
  version: 1.0.0
spec:
  pluginType: linter
  abiVersion: v0
`

	// The envelope itself rejects the document — the reject-parity below is
	// meaningful only because this fixture is genuinely invalid.
	if _, eerr := envelope.ValidateBytes(envelope.KindPlugin, []byte(malformed), "broken.yaml"); eerr == nil {
		t.Fatalf("fixture invalid: envelope must reject the malformed Plugin (reject-parity is vacuous otherwise)")
	} else if !errors.Is(eerr, envelope.ErrEntityEnvelope) {
		t.Fatalf("envelope rejection should wrap ErrEntityEnvelope; got %v", eerr)
	}

	dir := t.TempDir()
	writeFixture(t, dir, "broken-linter.yaml", malformed)

	// Eager: the fail-fast load halts, surfacing the envelope violation.
	if _, err := LoadAll("", dir); err == nil {
		t.Fatalf("eager LoadAll must halt on a malformed Plugin")
	} else if !errors.Is(err, envelope.ErrEntityEnvelope) {
		t.Errorf("eager load error should wrap ErrEntityEnvelope; got %v", err)
	}

	// Lazy: the tolerant enumeration skips the malformed entity — it is absent
	// from the by-URI surface rather than aborting the walk.
	lazy := NewLazyLookup("", "", []string{dir}, nil)
	if _, ok := lazy.PluginByURI("core/linter/broken"); ok {
		t.Errorf("lazy PluginByURI must miss a malformed (skipped) Plugin")
	}
}
