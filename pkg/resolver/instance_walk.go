// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// instancePair pairs a developer-chosen instance name with the authored
// instance and its authoring location. It is the single unit yielded by
// [collectComponentInstances], the one complete iterator over an Aggregate's
// component instances — every instance reachable from the top-level components
// plus all (recursive) subsystems. Consumers that previously walked
// `agg.Spec.Components` directly route through the walker so no consumer —
// present or future — can miss subsystem components.
//
// SubsystemPath is the dotted path of the enclosing subsystem ("" for a
// top-level component, "a" for `subsystems.a`, "a.b" for a nested
// `subsystems.a.subsystems.b`). [componentFieldPath] renders it into the
// authoring field locator used in diagnostics, so an error against a subsystem
// component points at `.spec.subsystems.<…>.components[…]`, not a flattened
// `.spec.components[…]`.
type instancePair struct {
	name          string
	inst          types.ComponentInstance
	subsystemPath string
}

// componentFieldPath renders the authoring field locator for a component
// instance at the given dotted subsystem path: `.spec.components["name"]` at the
// top level, `.spec.subsystems.a.components["name"]` in a subsystem, and
// `.spec.subsystems.a.subsystems.b.components["name"]` for nested subsystems —
// matching the dotted-aggregate-path convention used across the resolver guards.
func componentFieldPath(subsystemPath, name string) string {
	if subsystemPath == "" {
		return fmt.Sprintf(".spec.components[%q]", name)
	}
	return fmt.Sprintf(".spec.subsystems.%s.components[%q]", strings.ReplaceAll(subsystemPath, ".", ".subsystems."), name)
}

// collectComponentInstances flattens Aggregate.Spec.Components plus all
// ComponentInstances reachable through Subsystems (recursive), each tagged with
// its dotted subsystem path. Sorted by name for deterministic error ordering
// across runs of the same input (instance names are unique within an Aggregate's
// tree, so the node ID — qualifiedID(prefix, name), not subsystem-scoped — and
// the sort key are unambiguous).
func collectComponentInstances(na types.NormalizedAggregate) []instancePair {
	agg := na.Aggregate()
	if agg == nil {
		return nil
	}
	var out []instancePair
	// Subsystems-only: a top-level components surface has been folded into the
	// synthesized `default` subsystem by [types.Normalize], so iterating
	// subsystems is complete — the defensive root pass is gone. The
	// NormalizedAggregate newtype is what licenses this collapse.
	collectFromSubsystems("", agg.Spec.Subsystems, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func collectFromSubsystems(parentPath string, subs types.OrderedMap[types.Subsystem], out *[]instancePair) {
	for subName, sub := range subs.All() {
		path := subName
		if parentPath != "" {
			path = parentPath + "." + subName
		}
		for name, inst := range sub.Components.All() {
			*out = append(*out, instancePair{name: name, inst: inst, subsystemPath: path})
		}
		collectFromSubsystems(path, sub.Subsystems, out)
	}
}
