// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// placementRoot imports one module, data, which itself imports inner.
const placementRoot = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    data:
      type: ./data.yaml
`

// placementData declares its components out of sorted order, so a placement
// that sorted rather than kept the module's own order would be seen.
const placementData = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: data
spec:
  components:
    proxy:
      type: app/db:1.0.0
    backend:
      type: app/db:1.0.0
  aggregates:
    inner:
      type: ./inner.yaml
`

const placementInner = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: inner
spec:
  components:
    cache:
      type: app/db:1.0.0
`

// countingLoader serves the placement fixtures' modules by type and counts how
// often each is loaded.
func countingLoader(t *testing.T, loads map[string]int) SubAggregateLoader {
	t.Helper()
	documents := map[string]string{"./data.yaml": placementData, "./inner.yaml": placementInner}
	return func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		loads[typeName]++
		body, ok := documents[typeName]
		if !ok {
			return nil, "", nil, errors.New("no such module " + typeName)
		}
		return mustAggregate(t, body), typeName, nil, nil
	}
}

// placedRelease runs the placement over a Release and returns it with the loads
// it made.
func placedRelease(t *testing.T, releaseYAML string) (*types.Release, *ModuleLoads, map[string]int) {
	t.Helper()
	loads := map[string]int{}
	modules := NewModuleLoads(countingLoader(t, loads))
	release := types.NormalizeRelease(mustRelease(t, releaseYAML)).Release()
	modules.PlaceRoutedComponents(types.Normalize(mustAggregate(t, placementRoot)), release)
	return release, modules, loads
}

// declaredIDs lists the ids a boundary declares on either axis, in order.
func declaredIDs(b types.Boundary) []string {
	var ids []string
	for _, decl := range types.BoundaryComponentDeclarations(b) {
		ids = append(ids, decl.ID)
	}
	return ids
}

// TestPlaceRoutedComponents_NamesABareEntrysComponents proves a routing entry
// that names none of its module's components gains an empty entry for each of
// them, under its local name and in the order the module declares them, so the
// boundary holds them under the ids the graph gives them.
func TestPlaceRoutedComponents_NamesABareEntrysComponents(t *testing.T) {
	release, _, _ := placedRelease(t, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
`)
	east, _ := release.Spec.Boundaries.Get("east")
	data, _ := east.Aggregates.Get("data")
	if got := strings.Join(data.Components.Keys(), ","); got != "proxy,backend" {
		t.Fatalf("the bare entry's components = [%s], want the module's own [proxy,backend]", got)
	}
	for local, comp := range data.Components.All() {
		if comp.Annotations.Len() != 0 || comp.Infra.Len() != 0 || comp.Features.Len() != 0 || comp.Uses.Len() != 0 {
			t.Errorf("placed entry %q is not empty: %+v", local, comp)
		}
	}
	if got := strings.Join(declaredIDs(east), ","); !strings.HasPrefix(got, "data.proxy,data.backend") {
		t.Errorf("the boundary declares [%s], want data.proxy and data.backend first", got)
	}
}

// TestPlaceRoutedComponents_FillsANestedModule proves a module nested inside a
// routed one is placed by the same rule: the outer entry gains a routing entry
// for it, written where the author wrote none, and that entry names its
// components, so the boundary holds them under their nested id.
func TestPlaceRoutedComponents_FillsANestedModule(t *testing.T) {
	release, _, _ := placedRelease(t, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
`)
	east, _ := release.Spec.Boundaries.Get("east")
	data, _ := east.Aggregates.Get("data")
	inner, ok := data.Aggregates.Get("inner")
	if !ok {
		t.Fatalf("no routing entry was written for the nested module")
	}
	if got := strings.Join(inner.Components.Keys(), ","); got != "cache" {
		t.Fatalf("the nested entry's components = [%s], want [cache]", got)
	}
	if got := strings.Join(declaredIDs(east), ","); got != "data.proxy,data.backend,data.inner.cache" {
		t.Errorf("the boundary declares [%s], want [data.proxy,data.backend,data.inner.cache]", got)
	}
}

// TestPlaceRoutedComponents_FillsAPartlyNamedEntry proves a routing entry that
// names some of its module's components gains an empty entry for each of the
// others — they are routed in all the same — placed after the authored entries
// and in the module's own order, while the entry the author wrote keeps its
// place and its body.
func TestPlaceRoutedComponents_FillsAPartlyNamedEntry(t *testing.T) {
	release, _, _ := placedRelease(t, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      aggregates:
        data:
          components:
            backend:
              description: as authored
`)
	east, _ := release.Spec.Boundaries.Get("east")
	data, _ := east.Aggregates.Get("data")
	if got := strings.Join(data.Components.Keys(), ","); got != "backend,proxy" {
		t.Fatalf("the entry naming backend now names [%s], want the authored [backend] then the placed [proxy]", got)
	}
	if proxy, _ := data.Components.Get("proxy"); proxy.Description != "" || proxy.Annotations.Len() != 0 || proxy.Infra.Len() != 0 {
		t.Errorf("the placed entry is not empty: %+v", proxy)
	}
	if got := strings.Join(declaredIDs(east), ","); got != "data.backend,data.proxy,data.inner.cache" {
		t.Errorf("the boundary declares [%s], want [data.backend,data.proxy,data.inner.cache]", got)
	}
}

// TestPlaceRoutedComponents_LeavesWhatTheAuthorNamed proves the placement never
// alters an entry the author wrote: a routing entry naming a component keeps that
// entry's body as written, and a component the boundary names directly under its
// qualified id is not placed a second time on the routed axis.
func TestPlaceRoutedComponents_LeavesWhatTheAuthorNamed(t *testing.T) {
	t.Run("a routing entry naming some components", func(t *testing.T) {
		release, _, _ := placedRelease(t, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      aggregates:
        data:
          components:
            backend:
              description: as authored
`)
		east, _ := release.Spec.Boundaries.Get("east")
		data, _ := east.Aggregates.Get("data")
		if backend, _ := data.Components.Get("backend"); backend.Description != "as authored" {
			t.Errorf("the authored entry was rewritten: %+v", backend)
		}
		if keys := data.Components.Keys(); len(keys) == 0 || keys[0] != "backend" {
			t.Errorf("the authored entry lost its place: %v", keys)
		}
	})
	t.Run("a component named directly beside a bare routing entry", func(t *testing.T) {
		release, _, _ := placedRelease(t, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
      components:
        data.proxy:
          description: named directly
`)
		east, _ := release.Spec.Boundaries.Get("east")
		data, _ := east.Aggregates.Get("data")
		if got := strings.Join(data.Components.Keys(), ","); got != "backend" {
			t.Fatalf("the bare entry names [%s], want [backend] — proxy is already named directly", got)
		}
		seen := map[string]int{}
		for _, id := range declaredIDs(east) {
			seen[id]++
		}
		if seen["data.proxy"] != 1 {
			t.Errorf("data.proxy is declared %d times, want once", seen["data.proxy"])
		}
	})
}

// TestPlaceRoutedComponents_RoutingNothingLoadsNothing proves a Release routing
// no module asks for no walk: nothing is loaded and the Release is untouched.
func TestPlaceRoutedComponents_RoutingNothingLoadsNothing(t *testing.T) {
	release, _, loads := placedRelease(t, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      components:
        data.proxy: {}
`)
	if len(loads) != 0 {
		t.Errorf("a Release routing no module loaded %v", loads)
	}
	east, _ := release.Spec.Boundaries.Get("east")
	if east.Aggregates.Len() != 0 {
		t.Errorf("a Release routing no module gained routing entries: %v", east.Aggregates.Keys())
	}
}

// TestPlaceRoutedComponents_AFailedWalkPlacesNothing proves a composition that
// does not load leaves the Release as written, so the flattening that follows
// meets the failure where it always has.
func TestPlaceRoutedComponents_AFailedWalkPlacesNothing(t *testing.T) {
	modules := NewModuleLoads(func(string, map[string]any) (*types.Aggregate, string, map[string]any, error) {
		return nil, "", nil, errors.New("unavailable")
	})
	release := types.NormalizeRelease(mustRelease(t, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
`)).Release()
	modules.PlaceRoutedComponents(types.Normalize(mustAggregate(t, placementRoot)), release)
	east, _ := release.Spec.Boundaries.Get("east")
	data, _ := east.Aggregates.Get("data")
	if data.Components.Len() != 0 || data.Aggregates.Len() != 0 {
		t.Errorf("a failed walk placed entries: %v / %v", data.Components.Keys(), data.Aggregates.Keys())
	}
}

// TestModuleLoads_LoadsEachModuleOnce proves the placement walk and the
// flattening that follows load each module once between them: flattening takes
// the documents the placement loaded, and composes the same nodes it composes
// with no placement in front of it.
func TestModuleLoads_LoadsEachModuleOnce(t *testing.T) {
	const releaseYAML = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    east:
      directory: ./east
      aggregates:
        data: {}
`
	release, modules, loads := placedRelease(t, releaseYAML)
	normalized := types.NormalizeRelease(release)
	boundaryInfra, _, routedInfra := resolver.WireBoundaryContexts(normalized)
	result, err := Build(BuildConfig{
		ResolveResult: &resolver.ResolveResult{
			Aggregate:            mustAggregate(t, placementRoot),
			Release:              normalized.Release(),
			ReleasePath:          "release.yaml",
			BoundaryInfra:        boundaryInfra,
			RoutedAggregateInfra: routedInfra,
		},
		CatalogLookup:    bindingCatalog(t),
		LoadSubAggregate: modules.Loader(),
	})
	if err != nil {
		t.Fatalf("build over the placed Release: %v", err)
	}
	for _, typeName := range []string{"./data.yaml", "./inner.yaml"} {
		if loads[typeName] != 1 {
			t.Errorf("%s loaded %d times across placement and flattening, want once", typeName, loads[typeName])
		}
	}
	if got := strings.Join(result.Nodes.Keys(), ","); got != "data.proxy,data.backend,data.inner.cache" {
		t.Errorf("composed nodes = [%s], want [data.proxy,data.backend,data.inner.cache]", got)
	}
	for _, id := range []string{"data.proxy", "data.backend", "data.inner.cache"} {
		if bi, ok := boundaryInfra[id]; !ok || bi.BoundaryPath != "east" {
			t.Errorf("%s has no boundary context of its own from the placed entry: %+v", id, bi)
		}
	}
}
