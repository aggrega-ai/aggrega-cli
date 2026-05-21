// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// instancePair pairs a developer-chosen instance name with the authored
// instance. Guards that walk Aggregate component instances iterate over
// the returned slice, which carries every instance reachable from the
// Aggregate's top-level components plus recursive subsystem walks.
type instancePair struct {
	name string
	inst types.ComponentInstance
}

// collectComponentInstances flattens Aggregate.Spec.Components plus all
// ComponentInstances reachable through Subsystems (recursive). Sorted by
// name for deterministic error ordering across runs of the same input.
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
