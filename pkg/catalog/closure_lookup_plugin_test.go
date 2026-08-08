// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// localPluginEntity is a minimal schema-conforming Plugin entity authored
// in a local catalog directory — the Tier-1 case a workspace plugin socket (or a
// recipe pipeline step) references by entity URI.
const localPluginEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: baseline
  pluginType: governance
  version: 1.0.0
spec:
  abiVersion: v1alpha1
`

// TestClosureLookupResolvesLocalPluginEntityAtTier1 proves a local Plugin
// entity resolves through the PRODUCTION catalogLookup at Tier-1 — assertion (a),
// the direct unit assertion that closes the local-plugin-halts-the-build gap. Run
// through both backings (eager + lazy) for parity — assertion (c).
func TestClosureLookupResolvesLocalPluginEntityAtTier1(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "wasm-plugins/baseline.yaml", localPluginEntity)

	const entityURI = "governance/baseline:1.0.0"

	check := func(t *testing.T, lookup closure.Lookup, backing string) {
		res, err := lookup.ResolveCatalogEntity(entityURI, "plugin")
		if err != nil {
			t.Fatalf("[%s] ResolveCatalogEntity: %v", backing, err)
		}
		if res.NotFound {
			t.Fatalf("[%s] expected Tier-1 hit for a local Plugin entity; got NotFound", backing)
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
		eagerRes, _ = eager.ResolveCatalogEntity(entityURI, "plugin")
	})

	// Lazy backing — parity (c).
	lazy := catalog.NewLazyLookup("", ".aggrega/cache", []string{dir}, nil)
	lazyLookup := lazy.ClosureLookup(nil, "", ".aggrega/cache", nil)
	t.Run("lazy", func(t *testing.T) {
		check(t, lazyLookup, "lazy")
		lazyRes, _ := lazyLookup.ResolveCatalogEntity(entityURI, "plugin")
		if lazyRes != eagerRes {
			t.Errorf("eager/lazy parity: eager=%+v lazy=%+v", eagerRes, lazyRes)
		}
	})
}

// TestComputeClosureLocalPluginSocketDoesNotHalt proves ComputeClosure over a
// Release whose workspace plugin socket references a LOCAL Plugin entity does
// NOT halt with ErrUnresolvableURI — assertion (b). The socket rides the
// catalog-entity axis; the local entity must resolve at Tier-1 rather than
// falling to a fatal Tier-3 NotFound. Run through the production catalogLookup.
func TestComputeClosureLocalPluginSocketDoesNotHalt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeYAMLToCatalog(t, dir, "wasm-plugins/baseline.yaml", localPluginEntity)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")

	// A minimal Release (non-nil so the walk reaches the workspace-plugin axis;
	// nil aggregate/manifest leave those axes empty). The workspace config wires the
	// output-shaping label-transform governance attachment — the one the closure
	// pins — at the local Plugin entity URI.
	rel := &types.Release{}
	rel.Metadata.Name = "prod"
	rel.Metadata.Version = "1.0.0"

	cfg := workspaceconfig.Config{}
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{
		{URI: "governance/baseline:1.0.0", Role: plugintype.RoleLabelTransform},
	}

	cl, err := closure.ComputeClosure(rel, nil, types.NormalizedAggregate{}, cfg, lookup)
	if err != nil {
		if errors.Is(err, closure.ErrUnresolvableURI()) {
			t.Fatalf("ComputeClosure halted on a LOCAL Plugin socket (the closed gap): %v", err)
		}
		t.Fatalf("ComputeClosure: %v", err)
	}

	// The socket entity is pinned on the catalog-entity axis.
	var found bool
	for _, e := range cl.Entities {
		if e.URI == "governance/baseline:1.0.0" {
			found = true
			if e.KindHint != "plugin" {
				t.Errorf("entity KindHint: got %q; want plugin", e.KindHint)
			}
		}
	}
	if !found {
		t.Errorf("workspace plugin socket entity not pinned in closure entities; got %d entities", len(cl.Entities))
	}
}
