// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"log"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardCueSchema is the transitional stub during the JSON Schema
// migration. The validator pipeline no longer processes CUE source
// strings; catalog entities authoring Spec.CueSchema do not get
// validated until the schema-authoring phase migrates them to JSON
// Schema 2020-12. When a catalog entity carries Spec.CueSchema,
// this guard logs a deprecation note and returns nil (skip).
//
// When the catalog migrates and entities carry a JSON Schema payload
// instead, the validator pipeline activates against the new payload
// via a future integration. The schema-authoring phase owns the
// catalog-side migration; this guard's stub-skip is the transitional
// behavior during the gap.
func guardCueSchema(agg *types.Aggregate, catalog CatalogLookup, _ map[string]*BoundaryInfra) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}
	instances := collectComponentInstances(agg)
	logged := map[string]bool{}
	for _, pair := range instances {
		comp, ok := catalog.Component(pair.inst.Type)
		if !ok {
			continue
		}
		if comp.Spec.CueSchema != "" && !logged[comp.Metadata.Name] {
			log.Printf("warning: catalog entity %q authors Spec.CueSchema; validation skipped pending JSON Schema migration",
				comp.Metadata.Name)
			logged[comp.Metadata.Name] = true
		}
	}
	return nil
}

// instancePair pairs a developer-chosen instance name with the authored
// instance. Used by guards that walk Aggregate component instances.
type instancePair struct {
	name string
	inst types.ComponentInstance
}

// collectComponentInstances flattens Aggregate.Spec.Components plus all
// ComponentInstances reachable through Subsystems (recursive). Sorted by
// name for deterministic error ordering.
func collectComponentInstances(agg *types.Aggregate) []instancePair {
	var out []instancePair
	for name, inst := range agg.Spec.Components.All() {
		out = append(out, instancePair{name: name, inst: inst})
	}
	collectFromSubsystems(agg.Spec.Subsystems, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func collectFromSubsystems(subs types.OrderedMap[types.Subsystem], out *[]instancePair) {
	for _, sub := range subs.All() {
		for name, inst := range sub.Components.All() {
			*out = append(*out, instancePair{name: name, inst: inst})
		}
		collectFromSubsystems(sub.Subsystems, out)
	}
}
