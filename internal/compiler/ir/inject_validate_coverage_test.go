// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// injectSurfaceFixture builds an Aggregate and a Release placing a map-shaped
// delivery on exactly one *InjectBlock surface, which [ValidateInjectBlocks]
// must refuse.
type injectSurfaceFixture func() (*types.Aggregate, *types.Release)

// expectedInjectBlockFields lists every *InjectBlock-bearing field reachable
// from each root the walk reads, keyed "<Root>: TypeName.FieldName", each with
// a fixture planting a map-shaped delivery on that surface. The key carries the
// root because one type can hang from both — [types.FeatureUsesEntry] is an
// attachment's connection on the Aggregate and on the Release alike — and a key
// without the root would let a surface reached from the Release pass as the
// Aggregate one the walk already covers.
//
// MAINTENANCE CONTRACT — when a *InjectBlock field is legitimately added
// or removed from any pkg/types struct, update BOTH this map AND the
// corresponding walker code in inject_validate.go. The test failure
// messages at [TestInjectBlockSurfaceInventory_NoDrift] and
// [TestInjectBlockSurfaceInventory_EverySurfaceIsWalked] name what changed.
//
// The surfaces walked today:
//
//   - Aggregate: ComponentInstance.Inject (component self-inject)
//   - Aggregate: FeatureInstance.Inject (feature attachment inject)
//   - Aggregate: VerboseUsesEntry.Inject (uses[].inject on a component's own
//     block)
//   - Aggregate: FeatureUsesEntry.Inject (uses[].inject on an attachment's
//     block, through the one projection that walks it with the component's own
//     block's walker)
//   - Release: FeatureUsesEntry.Inject (uses[].inject on a connection a
//     Release attachment draws, boundary-wide or on one component)
var expectedInjectBlockFields = map[string]injectSurfaceFixture{
	"Aggregate: ComponentInstance.Inject": func() (*types.Aggregate, *types.Release) {
		components := types.OrderedMap[types.ComponentInstance]{}
		components.Set("api", types.ComponentInstance{Inject: mapShapedInject()})
		return &types.Aggregate{Spec: types.AggregateSpec{Components: components}}, nil
	},
	"Aggregate: FeatureInstance.Inject": func() (*types.Aggregate, *types.Release) {
		features := types.OrderedMap[types.FeatureInstance]{}
		features.Set("creds", types.FeatureInstance{Inject: mapShapedInject()})
		components := types.OrderedMap[types.ComponentInstance]{}
		components.Set("api", types.ComponentInstance{Features: features})
		return &types.Aggregate{Spec: types.AggregateSpec{Components: components}}, nil
	},
	"Aggregate: VerboseUsesEntry.Inject": func() (*types.Aggregate, *types.Release) {
		verbose := types.OrderedMap[types.VerboseUsesEntry]{}
		verbose.Set("primary", types.VerboseUsesEntry{Target: "db", Inject: mapShapedInject()})
		uses := types.OrderedMap[types.UsesSocketValue]{}
		uses.Set("databases", types.UsesSocketValue{Verbose: verbose})
		components := types.OrderedMap[types.ComponentInstance]{}
		components.Set("api", types.ComponentInstance{Uses: uses})
		return &types.Aggregate{Spec: types.AggregateSpec{Components: components}}, nil
	},
	"Aggregate: FeatureUsesEntry.Inject": func() (*types.Aggregate, *types.Release) {
		features := types.OrderedMap[types.FeatureInstance]{}
		features.Set("metrics", types.FeatureInstance{Uses: featureUsesWithInject(mapShapedInject())})
		components := types.OrderedMap[types.ComponentInstance]{}
		components.Set("api", types.ComponentInstance{Features: features})
		return &types.Aggregate{Spec: types.AggregateSpec{Components: components}}, nil
	},
	"Release: FeatureUsesEntry.Inject": func() (*types.Aggregate, *types.Release) {
		features := types.OrderedMap[types.BoundaryFeature]{}
		features.Set("metrics", types.BoundaryFeature{Uses: featureUsesWithInject(mapShapedInject())})
		boundaries := types.OrderedMap[types.Boundary]{}
		boundaries.Set("prod", types.Boundary{Features: features})
		return nil, &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}}
	},
}

// mapShapedInject is an inject block whose env delivery is a mapping rather than
// a list, which every walked surface must refuse.
func mapShapedInject() *types.InjectBlock {
	return &types.InjectBlock{Env: map[string]any{"DB_URL": "host"}}
}

// featureUsesWithInject is an attachment's uses block drawing one connection
// that carries block.
func featureUsesWithInject(block *types.InjectBlock) types.OrderedMap[types.FeatureUsesSocketValue] {
	verbose := types.OrderedMap[types.FeatureUsesEntry]{}
	verbose.Set("primary", types.FeatureUsesEntry{Target: "db", Inject: block})
	uses := types.OrderedMap[types.FeatureUsesSocketValue]{}
	uses.Set("backend", types.FeatureUsesSocketValue{Verbose: verbose})
	return uses
}

// TestInjectBlockSurfaceInventory_EverySurfaceIsWalked holds the walk to the
// inventory: for every surface the inventory lists, a map-shaped delivery
// planted there is refused. Together with
// [TestInjectBlockSurfaceInventory_NoDrift], which holds the inventory to the
// type system, it fails when the type system carries a surface the walk does
// not reach — whether the surface is new or the walk stopped visiting it.
func TestInjectBlockSurfaceInventory_EverySurfaceIsWalked(t *testing.T) {
	t.Parallel()

	surfaces := make([]string, 0, len(expectedInjectBlockFields))
	for surface := range expectedInjectBlockFields {
		surfaces = append(surfaces, surface)
	}
	sort.Strings(surfaces)
	for _, surface := range surfaces {
		agg, rel := expectedInjectBlockFields[surface]()
		err := ValidateInjectBlocks(normalized(t, agg), rel)
		if !errors.Is(err, ErrInjectDeliveryShape()) {
			t.Errorf("a map-shaped delivery planted on %s was not refused (got %v).\n"+
				"  The walk in inject_validate.go does not reach this surface; walk it there.", surface, err)
		}
	}
}

// TestInjectBlockSurfaceInventory_NoDrift is a defense-in-depth coverage
// guard that reflection-walks pkg/types from the Aggregate and Release
// root types and asserts every *InjectBlock-bearing field discovered
// matches the hardcoded [expectedInjectBlockFields] inventory. Drift in
// either direction fails the test with a directed message:
//
//   - A new field surfaces without an inventory entry — typically the
//     failure mode when a future schema revision adds a *InjectBlock
//     field to an existing type (e.g., Subsystem-level self-inject,
//     Release-side BoundaryFeature.Inject) and the walker is not
//     updated in lockstep.
//
//   - An expected field disappears without an inventory removal —
//     typically a type rename or field deletion that should also remove
//     the corresponding walker call site.
//
// The intent is to catch the failure mode "someone added a new
// *InjectBlock field to a walked type but forgot to update
// ValidateInjectBlocks." A new surface that bypasses the validator
// silently bypasses Invariant 4's loud-guardrails contract for inject
// mutual-exclusivity violations; this test is the type-system-level
// mirror that prevents the bypass at merge time.
//
// Coverage scope:
//
//   - Aggregate-rooted type graph (covers Aggregate.Spec.Components,
//     Aggregate.Spec.Subsystems recursively, and every nested instance
//     type reachable from there: ComponentInstance, FeatureInstance,
//     UsesSocketValue → VerboseUsesEntry, Subsystem recursive).
//
//   - Release-rooted type graph (covers Release.Spec.Boundaries and
//     Release.Spec.Components recursively, through BoundaryComponent,
//     BoundaryFeature and the routing entries: today it surfaces
//     FeatureUsesEntry.Inject, reached through BoundaryFeature.Uses).
//
// Each root is scanned on its own and its surfaces keyed by it, so a type both
// roots reach is listed once per root.
//
// What this test does NOT catch:
//
//   - A walk that stops visiting a listed surface: the inventory is read from
//     the type system, not from the walker. That is
//     [TestInjectBlockSurfaceInventory_EverySurfaceIsWalked]'s job.
//
//   - A walker bug that visits an existing field but fails to validate
//     it correctly (the unit tests in inject_validate_test.go cover that
//     class).
//
//   - A *InjectBlock surface that appears via a type imported from
//     outside pkg/types, or via a root not walked by
//     ValidateInjectBlocks (Aggregate + Release are the only roots; a
//     future root would require coordinated updates here AND in the
//     walker's signature).
func TestInjectBlockSurfaceInventory_NoDrift(t *testing.T) {
	t.Parallel()

	// Each root is scanned with a visited set of its own: a set shared across
	// roots would stop the Release scan at every type the Aggregate scan already
	// passed through, and a surface both roots reach would be recorded once.
	found := map[string]struct{}{}
	for _, root := range []struct {
		name string
		typ  reflect.Type
	}{
		{"Aggregate", reflect.TypeOf(types.Aggregate{})},
		{"Release", reflect.TypeOf(types.Release{})},
	} {
		fields := map[string]struct{}{}
		scanInjectBlockFields(root.typ, fields, map[reflect.Type]bool{})
		for field := range fields {
			found[root.name+": "+field] = struct{}{}
		}
	}

	// Detect drift in either direction. Sort the diff slices so the
	// failure message is deterministic across runs.
	var unexpected, missing []string
	for path := range found {
		if _, ok := expectedInjectBlockFields[path]; !ok {
			unexpected = append(unexpected, path)
		}
	}
	for path := range expectedInjectBlockFields {
		if _, ok := found[path]; !ok {
			missing = append(missing, path)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(missing)

	for _, path := range unexpected {
		t.Errorf("new *InjectBlock surface detected: %s\n"+
			"  Either add %q to expectedInjectBlockFields AND update ValidateInjectBlocks "+
			"to walk the new surface per the maintenance contract,\n"+
			"  or remove the field if added unintentionally.", path, path)
	}
	for _, path := range missing {
		t.Errorf("expected *InjectBlock surface missing: %s\n"+
			"  The type system no longer carries this field; "+
			"remove %q from expectedInjectBlockFields if the removal is intentional, "+
			"and remove the corresponding walker call site from inject_validate.go.", path, path)
	}
}

// scanInjectBlockFields recursively walks a struct type, collecting paths
// of every *InjectBlock-bearing field reachable from the root. Container
// types (OrderedMap, slice, array, map, pointer) are followed; cycles
// (e.g., Subsystem.Subsystems being OrderedMap[Subsystem]) are broken via
// the visited set.
//
// OrderedMap[V] is detected by name prefix and traversed via its
// unexported `values` map[string]V field — reflect.Type.Field() exposes
// unexported field types even though Value access is restricted, so the
// element type V is reachable. NOTE: if [types.OrderedMap]'s internal
// map field is ever renamed (today `values`, the literal passed to
// [reflect.Type.FieldByName] below), this walker will silently stop
// recursing through OrderedMap containers, and *InjectBlock fields newly
// added under OrderedMap values would NOT appear in `found` — drift
// detection would pass with a stale inventory. If OrderedMap's internal
// field is refactored, update the FieldByName lookup here.
//
// Interface-typed fields (e.g., InjectBlock.Env which is `any`, or
// OrderedMap[any] containers like BoundaryComponent.Infra) are not recursed
// into — interface storage is runtime-polymorphic, not a compile-time
// typed surface, and any runtime-typed *InjectBlock stored there would
// not be a *direct* type-system surface.
func scanInjectBlockFields(t reflect.Type, found map[string]struct{}, visited map[reflect.Type]bool) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if visited[t] {
		return
	}
	visited[t] = true

	switch t.Kind() {
	case reflect.Struct:
		if strings.HasPrefix(t.Name(), "OrderedMap[") {
			if vf, ok := t.FieldByName("values"); ok && vf.Type.Kind() == reflect.Map {
				scanInjectBlockFields(vf.Type.Elem(), found, visited)
			}
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			ft := f.Type
			if ft.Kind() == reflect.Ptr && isTypesInjectBlock(ft.Elem()) {
				found[fmt.Sprintf("%s.%s", t.Name(), f.Name)] = struct{}{}
				continue
			}
			if isTypesInjectBlock(ft) {
				// Non-pointer embed (not the current convention, but
				// the guard catches a future direct-embed surface for
				// completeness).
				found[fmt.Sprintf("%s.%s (direct)", t.Name(), f.Name)] = struct{}{}
				continue
			}
			scanInjectBlockFields(ft, found, visited)
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		scanInjectBlockFields(t.Elem(), found, visited)
	}
}

// isTypesInjectBlock reports whether the reflect.Type names the
// pkg/types.InjectBlock struct. The pkg-path check guards against
// false-positive matches on any similarly-named type from another
// package.
func isTypesInjectBlock(t reflect.Type) bool {
	return t.PkgPath() == "github.com/aggrega-ai/aggrega-cli/pkg/types" && t.Name() == "InjectBlock"
}
