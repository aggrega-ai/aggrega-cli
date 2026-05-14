// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// expectedInjectBlockFields lists every TypeName.FieldName path in
// pkg/types that should carry a *InjectBlock field. The list is the
// type-system-level mirror of [ValidateInjectBlocks]'s walker coverage:
// each entry corresponds to a walker call site in
// [internal/compiler/ir/inject_validate.go].
//
// MAINTENANCE CONTRACT — when a *InjectBlock field is legitimately added
// or removed from any pkg/types struct, update BOTH this map AND the
// corresponding walker code in inject_validate.go. The test failure
// message at [TestInjectBlockSurfaceInventory_NoDrift] names what
// changed.
//
// The four entries below correspond to the four typed surfaces walked
// today:
//
//   - ComponentInstance.Inject (component self-inject)
//   - FeatureInstance.Inject (feature attachment inject)
//   - SecretInstance.Inject (secret attachment inject)
//   - VerboseUsesEntry.Inject (uses[].inject — reached via every
//     ComponentInstance / FeatureInstance / SecretInstance with a Uses block)
var expectedInjectBlockFields = map[string]struct{}{
	"ComponentInstance.Inject": {},
	"FeatureInstance.Inject":   {},
	"SecretInstance.Inject":    {},
	"VerboseUsesEntry.Inject":  {},
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
//     SecretInstance, UsesSocketValue → VerboseUsesEntry, Subsystem
//     recursive).
//
//   - Release-rooted type graph (covers Release.Spec.Boundaries
//     recursively; today this surfaces no *InjectBlock fields per the
//     type system, but future schema revisions may add some — the guard
//     catches them at merge time).
//
// What this test does NOT catch:
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

	found := map[string]struct{}{}
	visited := map[reflect.Type]bool{}

	scanInjectBlockFields(reflect.TypeOf(types.Aggregate{}), found, visited)
	scanInjectBlockFields(reflect.TypeOf(types.Release{}), found, visited)

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
// OrderedMap[any] containers like Boundary.Features) are not recursed
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
	return t.PkgPath() == "github.com/cognitio/aggrega-cli/pkg/types" && t.Name() == "InjectBlock"
}
