// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// notFoundCatalog is a CatalogLookup whose Component lookup always misses — the
// "resolved-but-absent from the guard's view" condition the guards now turn into
// a loud halt for a bare catalog reference.
func notFoundCatalog() CatalogLookup {
	return CatalogLookup{Component: func(string) (types.Component, bool) {
		return types.Component{}, false
	}}
}

func requireViewInconsistency(t *testing.T, errs []*ResolverError) {
	t.Helper()
	for _, e := range errs {
		if errors.Is(e, ErrGuardEntityViewInconsistency) {
			return
		}
	}
	t.Fatalf("want an ErrGuardEntityViewInconsistency halt; got %v", errs)
}

func oneBareComponent(typeRef string, inject *types.InjectBlock) *types.Aggregate {
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("svc", types.ComponentInstance{Type: typeRef, Inject: inject})
	return &types.Aggregate{Metadata: types.AggregateMetadata{Name: "demo"}, Spec: types.AggregateSpec{Components: comps}}
}

// TestGuardEntityViewInconsistency_Classifies pins the shared classifier: a
// Tier-1 relative-path type is a legitimate skip (nil); a bare catalog reference
// absent from the view is the loud halt.
func TestGuardEntityViewInconsistency_Classifies(t *testing.T) {
	t.Parallel()
	if verr := guardEntityViewInconsistency("E", "F", "./local-variant"); verr != nil {
		t.Errorf("relative-path type must classify as a legitimate skip (nil); got %v", verr)
	}
	if verr := guardEntityViewInconsistency("E", "F", "../sibling"); verr != nil {
		t.Errorf("parent-relative-path type must classify as a legitimate skip (nil); got %v", verr)
	}
	verr := guardEntityViewInconsistency("E", "F", "core/absent:1.0.0")
	if verr == nil || !errors.Is(verr, ErrGuardEntityViewInconsistency) {
		t.Fatalf("bare catalog ref must halt with ErrGuardEntityViewInconsistency; got %v", verr)
	}
}

// The halt arm per silent-skip guard: a bare-token catalog reference absent from
// the guard's view HALTS with ErrGuardEntityViewInconsistency. (The skip arm — a
// `./local` type — lives in each guard's own _test.go.)

func TestGuardSelfInject_ResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	agg := oneBareComponent("core/absent:1.0.0", &types.InjectBlock{Env: map[string]any{"X": "1"}})
	requireViewInconsistency(t, guardSelfInject(normalized(t, agg), notFoundCatalog()))
}

func TestGuardInjectKeys_ResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	agg := oneBareComponent("core/absent:1.0.0", &types.InjectBlock{Env: map[string]any{"X": map[string]any{"from": "nope"}}})
	requireViewInconsistency(t, guardInjectKeys(normalized(t, agg), notFoundCatalog()))
}

func TestGuardRequiredUsesSockets_ResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	agg := oneBareComponent("core/absent:1.0.0", nil)
	requireViewInconsistency(t, guardRequiredUsesSockets(normalized(t, agg), nil, notFoundCatalog()))
}

func TestGuardSchemaConformance_ResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	agg := oneBareComponent("core/absent:1.0.0", nil)
	requireViewInconsistency(t, guardSchemaConformance(normalized(t, agg), notFoundCatalog(), nil, nil))
}

func TestGuardUsesSocket_ResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	// aggWithEdge builds component "svc" of bare type "app/svc:1.0.0" with a uses
	// socket — guardUsesSocket reaches ValidateUsesAgainstSchema, which on a
	// missing catalog entity now halts for a bare ref.
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	requireViewInconsistency(t, guardUsesSocket(normalized(t, agg), notFoundCatalog()))
}

func TestGuardReleaseUsesSocket_ResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", types.OrderedMap[any]{})
	rel := releaseWithEdgeInfra("databases", "main", om("k", "v"))
	requireViewInconsistency(t, guardReleaseUsesSocket(normalized(t, agg), rel, notFoundCatalog()))
}

// TestGuardEdgeConformance_SocketPillarResolvedCatalogRefAbsentHalts is the
// operator-required socket-pillar arm: the halt must surface through the
// CompileSocketPillarSchema path (its (Schema, bool) return must not swallow the
// halt into ok==false). The edge carries values so the socket-pillar path is
// exercised.
func TestGuardEdgeConformance_SocketPillarResolvedCatalogRefAbsentHalts(t *testing.T) {
	t.Parallel()
	agg := aggWithEdge("databases", "main", om("size", "10Gi"))
	requireViewInconsistency(t, guardEdgeConformance(normalized(t, agg), nil, notFoundCatalog(), nil))
}
