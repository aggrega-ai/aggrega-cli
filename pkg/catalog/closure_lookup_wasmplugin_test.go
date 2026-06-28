// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// localWasmPluginEntity is a minimal schema-conforming WasmPlugin entity authored
// in a local catalog directory — the Tier-1 case a workspace plugin socket (or a
// recipe pipeline step) references by entity URI.
const localWasmPluginEntity = `apiVersion: aggrega.ai/v1alpha1
kind: WasmPlugin
metadata:
  name: core/linter/baseline
  version: 1.0.0
spec:
  plugin: "oci://hub.aggrega.ai/wasm/linter/baseline@sha256:0000000000000000000000000000000000000000000000000000000000000000"
  pluginTypes:
    - linter
  abiVersion: v1alpha1
`

// TestClosureLookupResolvesLocalWasmPluginEntityAtTier1 proves a local WasmPlugin
// entity resolves through the PRODUCTION catalogLookup at Tier-1 — assertion (a),
// the direct unit assertion that closes the local-plugin-halts-the-build gap. Run
// through both backings (eager + lazy) for parity — assertion (c).
func TestClosureLookupResolvesLocalWasmPluginEntityAtTier1(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "wasm-plugins/baseline.yaml", localWasmPluginEntity)

	const entityURI = "core/linter/baseline:1.0.0"

	check := func(t *testing.T, lookup closure.Lookup, backing string) {
		res, err := lookup.ResolveCatalogEntity(entityURI, "wasm_plugin")
		if err != nil {
			t.Fatalf("[%s] ResolveCatalogEntity: %v", backing, err)
		}
		if res.NotFound {
			t.Fatalf("[%s] expected Tier-1 hit for a local WasmPlugin entity; got NotFound", backing)
		}
		if res.ResolutionPath != "tier-1-catalog" {
			t.Errorf("[%s] ResolutionPath: got %q; want tier-1-catalog", backing, res.ResolutionPath)
		}
		if res.ResolvedVersion != "1.0.0" {
			t.Errorf("[%s] ResolvedVersion: got %q; want 1.0.0", backing, res.ResolvedVersion)
		}
		if res.Digest == "" {
			t.Errorf("[%s] Digest: empty; want the entity's content digest", backing)
		}
	}

	// Eager backing.
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	eager := cat.ClosureLookup(nil, dir, ".aggrega/cache")
	var eagerRes closure.Resolution
	t.Run("eager", func(t *testing.T) {
		check(t, eager, "eager")
		eagerRes, _ = eager.ResolveCatalogEntity(entityURI, "wasm_plugin")
	})

	// Lazy backing — parity (c).
	lazy := catalog.NewLazyLookup("", ".aggrega/cache", []string{dir}, nil)
	lazyLookup := lazy.ClosureLookup(nil, "", ".aggrega/cache", nil)
	t.Run("lazy", func(t *testing.T) {
		check(t, lazyLookup, "lazy")
		lazyRes, _ := lazyLookup.ResolveCatalogEntity(entityURI, "wasm_plugin")
		if lazyRes != eagerRes {
			t.Errorf("eager/lazy parity: eager=%+v lazy=%+v", eagerRes, lazyRes)
		}
	})
}

// TestComputeClosureLocalWasmPluginSocketDoesNotHalt proves ComputeClosure over a
// Release whose workspace plugin socket references a LOCAL WasmPlugin entity does
// NOT halt with ErrUnresolvableURI — assertion (b). The socket rides the
// catalog-entity axis; the local entity must resolve at Tier-1 rather than
// falling to a fatal Tier-3 NotFound. Run through the production catalogLookup.
func TestComputeClosureLocalWasmPluginSocketDoesNotHalt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "wasm-plugins/baseline.yaml", localWasmPluginEntity)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")

	// A minimal Release (non-nil so the walk reaches the workspace-plugin axis;
	// nil aggregate/manifest leave those axes empty). The workspace config wires a
	// policy plugin socket at the local WasmPlugin entity URI.
	rel := &types.Release{}
	rel.Metadata.Name = "prod"
	rel.Metadata.Version = "1.0.0"

	cfg := workspaceconfig.Config{}
	cfg.Plugins.Policy = []workspaceconfig.PluginRef{
		{URI: "core/linter/baseline:1.0.0"},
	}

	cl, err := closure.ComputeClosure(rel, nil, nil, cfg, lookup)
	if err != nil {
		if errors.Is(err, closure.ErrUnresolvableURI) {
			t.Fatalf("ComputeClosure halted on a LOCAL WasmPlugin socket (the closed gap): %v", err)
		}
		t.Fatalf("ComputeClosure: %v", err)
	}

	// The socket entity is pinned on the catalog-entity axis.
	var found bool
	for _, e := range cl.Entities {
		if e.URI == "core/linter/baseline:1.0.0" {
			found = true
			if e.KindHint != "wasm_plugin" {
				t.Errorf("entity KindHint: got %q; want wasm_plugin", e.KindHint)
			}
		}
	}
	if !found {
		t.Errorf("workspace plugin socket entity not pinned in closure entities; got %d entities", len(cl.Entities))
	}
}
