// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIrEntityInfra_DerivesUsingSetFromIRRenderSet pins that the per-instance
// FeatureRecipe using-set is derived from the IR render-set (graph.Spec) — every
// attachment entry the renderer renders, keyed by the Manifest-mapped
// FeatureRecipe URI, carrying its own IR-resolved infra. Two instances of one FR on
// different nodes yield two references with distinct infra; a credential attachment
// is walked through the same one bucket as an operational one; an instance whose
// type has no Manifest mapping is skipped. (The IR's five-tier assembly is tested in
// the ir package; this pins that pull reads that render-set rather than a
// tier-short proxy.)
func TestIrEntityInfra_DerivesUsingSetFromIRRenderSet(t *testing.T) {
	t.Parallel()
	entry := func(typ, infraKey, infraVal string) types.IRFeatureEntry {
		e := types.IRFeatureEntry{Type: typ, Infra: types.OrderedMap[any]{}}
		if infraKey != "" {
			e.Infra.Set(infraKey, infraVal)
		}
		return e
	}
	apiFeats := types.OrderedMap[types.IRFeatureEntry]{}
	apiFeats.Set("tls", entry("core/tls", "chart_version", "1.2.3"))
	apiFeats.Set("orphan", entry("core/unmapped", "", "")) // no Manifest mapping
	// A credential attachment shares the one bucket, so it is pinned through the
	// same walk as an operational attachment.
	apiFeats.Set("db", entry("core/db-creds", "size", "large"))
	webFeats := types.OrderedMap[types.IRFeatureEntry]{}
	webFeats.Set("tls", entry("core/tls", "chart_version", "4.5.6"))
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFeats)
	fvm.Set("web", webFeats)

	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: fvm}}

	flatF := &types.MappingTier{}
	flatF.Type.Set("core/tls", "tls-helm:1.0.0")
	flatF.Type.Set("core/db-creds", "db-creds-helm:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Features: flatF}}}

	componentScope := func(_, _ string) (string, bool) { return "component", true }
	out, err := irEntityInfra(graph, manifest, componentScope)
	if err != nil {
		t.Fatalf("irEntityInfra: %v", err)
	}

	tls := out["tls-helm:1.0.0"]
	if len(tls) != 2 {
		t.Fatalf("tls-helm references: %d; want 2 (one per node instance)", len(tls))
	}
	gotInfra := map[string]any{}
	for _, c := range tls {
		gotInfra[c.Component] = c.Infra["chart_version"]
	}
	if gotInfra["api.features.tls"] != "1.2.3" || gotInfra["web.features.tls"] != "4.5.6" {
		t.Errorf("per-instance infra not carried from the IR: %v", gotInfra)
	}
	db := out["db-creds-helm:1.0.0"]
	if len(db) != 1 || db[0].Component != "api.features.db" || db[0].Infra["size"] != "large" {
		t.Errorf("credential using-set not derived from the IR render-set: %v", db)
	}
	if _, ok := out["core/unmapped"]; ok {
		t.Error("a type with no Manifest mapping must not appear in the using-set")
	}
}

// TestIrEntityInfra_BoundaryScopePinnedOnceFromProjection pins the source-identity
// granularity for a boundary-scope FeatureRecipe. A boundary-scope FR is Tier-4
// folded into every subtree component's FeatureValuesMapping (N per-component
// entries at the wrong granularity for source identity), and ALSO appears once on
// the boundary-feature projection. The pin must come exclusively from the
// projection — one reference keyed by (boundaryPath, instance) carrying the
// boundary's own infra — with the per-component FVM entries skipped, so the
// lock-time source identity matches the renderer's single boundary render. A
// component-scope FR keeps its per-component pins.
func TestIrEntityInfra_BoundaryScopePinnedOnceFromProjection(t *testing.T) {
	t.Parallel()
	fEntry := func(typ, k, v string) types.IRFeatureEntry {
		e := types.IRFeatureEntry{Type: typ, Infra: types.OrderedMap[any]{}}
		if k != "" {
			e.Infra.Set(k, v)
		}
		return e
	}
	// bridge (boundary-scope) folded onto both api and web; tls (component-scope)
	// on api only.
	apiFeats := types.OrderedMap[types.IRFeatureEntry]{}
	apiFeats.Set("bridge", fEntry("core/bridge", "mappedPort", "folded-api"))
	apiFeats.Set("tls", fEntry("core/tls", "chart_version", "1.2.3"))
	apiFeats.Set("legacy", fEntry("core/legacy", "v", "x")) // scope unresolvable → graceful FVM pin
	webFeats := types.OrderedMap[types.IRFeatureEntry]{}
	webFeats.Set("bridge", fEntry("core/bridge", "mappedPort", "folded-web"))
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", apiFeats)
	fvm.Set("web", webFeats)

	// Boundary-feature projection: bridge attached once at "cluster" with the
	// boundary's authoritative infra.
	bridgeBFInfra := types.OrderedMap[any]{}
	bridgeBFInfra.Set("mappedPort", "boundary-authoritative")
	clusterBF := types.OrderedMap[types.IRBoundaryFeature]{}
	clusterBF.Set("bridge", types.IRBoundaryFeature{Type: "core/bridge", Infra: bridgeBFInfra})
	bfProj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	bfProj.Set("cluster", clusterBF)

	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: fvm, BoundaryFeatures: bfProj}}

	flatF := &types.MappingTier{}
	flatF.Type.Set("core/bridge", "bridge-helm:1.0.0")
	flatF.Type.Set("core/tls", "tls-helm:1.0.0")
	flatF.Type.Set("core/legacy", "legacy-helm:1.0.0")
	manifest := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Features: flatF}}}

	scopeOf := func(_, featType string) (string, bool) {
		switch featType {
		case "core/bridge":
			return "boundary", true
		case "core/legacy":
			return "", false // FR scope unresolvable — must fall through to the per-component pin
		}
		return "component", true
	}
	out, err := irEntityInfra(graph, manifest, scopeOf)
	if err != nil {
		t.Fatalf("irEntityInfra: %v", err)
	}

	// bridge pinned ONCE, from the projection's boundary infra — not twice from
	// the folded per-component FVM entries.
	bridge := out["bridge-helm:1.0.0"]
	if len(bridge) != 1 {
		t.Fatalf("boundary-scope FR references: %d; want 1 (pinned once from the projection)", len(bridge))
	}
	if bridge[0].Component != "cluster.features.bridge" {
		t.Errorf("boundary-scope pin identity = %q; want cluster.features.bridge", bridge[0].Component)
	}
	if bridge[0].Infra["mappedPort"] != "boundary-authoritative" {
		t.Errorf("boundary-scope pin infra = %v; want the boundary projection's infra, not a folded FVM entry", bridge[0].Infra["mappedPort"])
	}
	// component-scope FR keeps its per-component pin.
	if tls := out["tls-helm:1.0.0"]; len(tls) != 1 || tls[0].Component != "api.features.tls" {
		t.Errorf("component-scope FR pin changed: %v", tls)
	}
	// graceful: an FR whose scope is unresolvable is NOT dropped — it keeps
	// the per-component FVM pin (prior behavior), never the projection pin.
	if legacy := out["legacy-helm:1.0.0"]; len(legacy) != 1 || legacy[0].Component != "api.features.legacy" {
		t.Errorf("unresolvable-scope FR must keep its per-component pin (graceful fallback); got %v", legacy)
	}
}
