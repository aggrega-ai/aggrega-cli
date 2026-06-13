// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// edgeSchemaCatalog returns a CatalogLookup with one consuming Component
// "app/svc:1.0.0" whose usesSchema declares two sockets: "databases" carries a
// per-edge valueSchema (pool_size 1..100) and infraSchema (storage_class
// string); "plain" declares no per-edge contract. Built from full Component
// YAML so ValueSchema/InfraSchema are field-node mappings, exactly as the
// catalog loader produces them.
func edgeSchemaCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	const compYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    databases:
      selector: "component.aggrega.ai/type=db"
      valueSchema:
        pool_size: { type: integer, minimum: 1, maximum: 100 }
      infraSchema:
        storage_class: { type: string }
    plain:
      selector: "component.aggrega.ai/type=db"
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(compYAML), &comp); err != nil {
		t.Fatalf("component fixture parse: %v", err)
	}
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "app/svc:1.0.0" {
				return comp, true
			}
			return types.Component{}, false
		},
	}
}

// aggWithEdge builds a one-component Aggregate ("svc") whose `uses` declares a
// verbose entry under the given socket/alias carrying the supplied per-edge
// values.
func aggWithEdge(socket, alias string, values types.OrderedMap[any]) *types.Aggregate {
	verbose := types.OrderedMap[types.VerboseUsesEntry]{}
	verbose.Set(alias, types.VerboseUsesEntry{Target: "db", Values: values})
	uses := types.OrderedMap[types.UsesSocketValue]{}
	uses.Set(socket, types.UsesSocketValue{Verbose: verbose})
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: "app/svc:1.0.0", Uses: uses})
	return &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "demo"},
		Spec:     types.AggregateSpec{Components: comps},
	}
}

// releaseWithEdgeInfra builds a Release whose boundary "prod" configures node
// "svc"'s (socket, alias) edge with the supplied per-edge infra.
func releaseWithEdgeInfra(socket, alias string, infra types.OrderedMap[any]) *types.Release {
	aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
	aliases.Set(alias, types.ReleaseUsesEntry{Infra: infra})
	ru := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	ru.Set(socket, aliases)
	comps := types.OrderedMap[types.BoundaryComponent]{}
	comps.Set("svc", types.BoundaryComponent{Uses: ru})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: comps})
	rel := &types.Release{Metadata: types.ReleaseMetadata{Name: "demo-prod"}}
	rel.Spec.Boundaries = boundaries
	return rel
}

func om(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// normalizedReleaseTopLevelEdge builds a Release that authors node "svc"'s
// (socket, alias) edge at the top-level `.spec.components` surface, then folds
// it through NormalizeRelease — producing the synthesized `default` boundary the
// resolver guards see. Mirrors releaseWithEdgeInfra but for the root surface.
func normalizedReleaseTopLevelEdge(socket, alias string, infra types.OrderedMap[any]) *types.Release {
	aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
	aliases.Set(alias, types.ReleaseUsesEntry{Infra: infra})
	ru := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	ru.Set(socket, aliases)
	comps := types.OrderedMap[types.BoundaryComponent]{}
	comps.Set("svc", types.BoundaryComponent{Uses: ru})
	rel := &types.Release{Metadata: types.ReleaseMetadata{Name: "demo-prod"}}
	rel.Spec.Components = comps
	return types.NormalizeRelease(rel).Release()
}

// TestReleaseComponentFieldPath pins the Release-side path renderer: a
// synthesized-default component renders at the root shape, a declared boundary
// renders qualified, and a boundary a user *declares* as "default" still renders
// qualified — the key is the flag, not the path string.
func TestReleaseComponentFieldPath(t *testing.T) {
	t.Parallel()
	if got := releaseComponentFieldPath("default", "svc", true); got != `.spec.components["svc"]` {
		t.Errorf("synthesized default must render root-shaped; got %q", got)
	}
	if got := releaseComponentFieldPath("prod", "svc", false); got != `.spec.boundaries["prod"].components["svc"]` {
		t.Errorf("declared boundary must render qualified; got %q", got)
	}
	if got := releaseComponentFieldPath("default", "svc", false); got != `.spec.boundaries["default"].components["svc"]` {
		t.Errorf("a declared boundary named default must stay qualified; got %q", got)
	}
}

// TestGuardReleaseUsesSocket_SynthesizedDefaultElidesPath is the R1 elision
// red/green: a top-level Release component (folded into the synthesized default)
// wiring an undeclared socket halts with a root-shaped locator — never under the
// generated `default` boundary. Closes the finding that a top-level Release
// component's uses could leak the synthesized boundary into a guard diagnostic.
func TestGuardReleaseUsesSocket_SynthesizedDefaultElidesPath(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	rel := normalizedReleaseTopLevelEdge("caches", "primary", om("x", "y")) // undeclared socket
	errs := guardReleaseUsesSocket(normalized(t, agg), rel, edgeSchemaCatalog(t))
	if len(errs) != 1 || !errors.Is(errs[0], ErrUnknownUsesSocket) {
		t.Fatalf("want exactly 1 ErrUnknownUsesSocket; got %v", errs)
	}
	if errs[0].Field != `.spec.components["svc"].uses["caches"]` {
		t.Errorf("synthesized-default Release edge must render root-shaped, never under default; got %q", errs[0].Field)
	}
}

// TestGuardEdgeConformance_SynthesizedDefaultElidesInfraPath is the R2 elision
// red/green: a per-edge infra violation on a top-level Release component renders
// the root-shaped locator, never the synthesized `default` boundary.
func TestGuardEdgeConformance_SynthesizedDefaultElidesInfraPath(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	rel := normalizedReleaseTopLevelEdge("databases", "main", om("storage_clas", "fast")) // typo'd key
	errs := guardEdgeConformance(normalized(t, agg), rel, edgeSchemaCatalog(t), nil)
	requireConformance(t, errs, 1, `.spec.components["svc"].uses["databases"]["main"]`)
}

// --- per-edge values conformance (Aggregate, the reuse-proving cases) -------

// TestGuardEdgeConformance_ValuesConforming proves the shared
// ValidatePillarConformance entry accepts a conforming per-edge values map:
// nothing halts.
func TestGuardEdgeConformance_ValuesConforming(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", om("pool_size", 20))
	if errs := guardEdgeConformance(normalized(t, agg), nil, edgeSchemaCatalog(t), nil); len(errs) != 0 {
		t.Fatalf("conforming per-edge values should not halt; got %v", errs)
	}
}

// TestGuardEdgeConformance_ValuesTypoHalts proves a property the socket
// valueSchema forbids halts with ErrSchemaConformance, the diagnostic naming
// the edge (consumer, socket, alias).
func TestGuardEdgeConformance_ValuesTypoHalts(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", om("pool_sze", 20)) // typo'd key
	errs := guardEdgeConformance(normalized(t, agg), nil, edgeSchemaCatalog(t), nil)
	requireConformance(t, errs, 1, `.spec.components["svc"].uses["databases"]["main"]`)
}

// TestGuardEdgeConformance_ValuesOutOfRangeHalts proves constraint enforcement
// on a per-edge value.
func TestGuardEdgeConformance_ValuesOutOfRangeHalts(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", om("pool_size", 9999)) // maximum 100
	errs := guardEdgeConformance(normalized(t, agg), nil, edgeSchemaCatalog(t), nil)
	requireConformance(t, errs, 1, `.spec.components["svc"].uses["databases"]["main"].values.pool_size`)
}

// TestGuardEdgeConformance_NoSchemaSocketSkips proves the opt-in posture: an
// edge on a socket with no per-edge schema is not validated even when it
// carries values.
func TestGuardEdgeConformance_NoSchemaSocketSkips(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("plain", "main", om("anything", "goes"))
	if errs := guardEdgeConformance(normalized(t, agg), nil, edgeSchemaCatalog(t), nil); len(errs) != 0 {
		t.Fatalf("socket with no per-edge schema should skip; got %v", errs)
	}
}

// --- per-edge infra conformance (Release) -----------------------------------

// TestGuardEdgeConformance_ReleaseInfraConforming proves a conforming Release
// per-edge infra map passes.
func TestGuardEdgeConformance_ReleaseInfraConforming(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	rel := releaseWithEdgeInfra("databases", "main", om("storage_class", "fast"))
	if errs := guardEdgeConformance(normalized(t, agg), rel, edgeSchemaCatalog(t), nil); len(errs) != 0 {
		t.Fatalf("conforming per-edge infra should not halt; got %v", errs)
	}
}

// TestGuardEdgeConformance_ReleaseInfraTypoHalts proves a Release per-edge
// infra violation halts, the diagnostic naming the Release edge.
func TestGuardEdgeConformance_ReleaseInfraTypoHalts(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	rel := releaseWithEdgeInfra("databases", "main", om("storage_clas", "fast")) // typo'd key
	errs := guardEdgeConformance(normalized(t, agg), rel, edgeSchemaCatalog(t), nil)
	requireConformance(t, errs, 1, `.spec.boundaries["prod"].components["svc"].uses["databases"]["main"]`)
}

// --- Release undeclared-socket guard ----------------------------------------

// TestGuardReleaseUsesSocket_DeclaredSocketPasses proves a Release edge on a
// declared socket does not halt.
func TestGuardReleaseUsesSocket_DeclaredSocketPasses(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	rel := releaseWithEdgeInfra("databases", "main", om("storage_class", "fast"))
	if errs := guardReleaseUsesSocket(normalized(t, agg), rel, edgeSchemaCatalog(t)); len(errs) != 0 {
		t.Fatalf("Release edge on a declared socket should pass; got %v", errs)
	}
}

// TestGuardReleaseUsesSocket_UndeclaredSocketHalts proves a Release edge on a
// socket absent from the consuming component's usesSchema halts with
// ErrUnknownUsesSocket.
func TestGuardReleaseUsesSocket_UndeclaredSocketHalts(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	rel := releaseWithEdgeInfra("caches", "primary", om("x", "y")) // no such socket
	errs := guardReleaseUsesSocket(normalized(t, agg), rel, edgeSchemaCatalog(t))
	if len(errs) != 1 || !errors.Is(errs[0], ErrUnknownUsesSocket) {
		t.Fatalf("want exactly 1 ErrUnknownUsesSocket; got %v", errs)
	}
}

// TestGuardReleaseUsesSocket_UnknownNodeSkips proves a Release edge on a node
// the resolver cannot resolve a type for (e.g. a sub-aggregate component) is
// skipped rather than halting — the documented coverage boundary.
func TestGuardReleaseUsesSocket_UnknownNodeSkips(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	// Release names a node "data.sub" absent from the resolver-visible set.
	aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
	aliases.Set("x", types.ReleaseUsesEntry{Target: "y"})
	ru := types.OrderedMap[types.OrderedMap[types.ReleaseUsesEntry]]{}
	ru.Set("whatever", aliases)
	comps := types.OrderedMap[types.BoundaryComponent]{}
	comps.Set("data.sub", types.BoundaryComponent{Uses: ru})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set("prod", types.Boundary{Components: comps})
	rel := &types.Release{Metadata: types.ReleaseMetadata{Name: "demo-prod"}}
	rel.Spec.Boundaries = boundaries

	if errs := guardReleaseUsesSocket(normalized(t, agg), rel, edgeSchemaCatalog(t)); len(errs) != 0 {
		t.Fatalf("unresolved-node Release edge should skip; got %v", errs)
	}
}
