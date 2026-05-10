// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// normalizeUses converts the typed `uses` block authored on a Component
// or Feature into a flat slice of NormalizedEdge — the canonical edge
// representation downstream code reads. The function handles two
// authoring forms; the choice is per-socket, captured at the type level
// by UsesSocketValue:
//
//   - Compact form — string array. `socket: ["db1", "db2"]`. Each entry
//     produces one edge; Alias equals Target.
//   - Verbose form — alias-keyed map. `socket: {"hr-db": {"target":
//     "corporate-hr", "inject": {...}}}`. Each entry produces one edge;
//     the map key becomes Alias and the entry's Target field becomes the
//     edge's Target. Target defaults to the alias when the entry omits
//     it (the typed VerboseUsesEntry.UnmarshalYAML hook also handles the
//     bare-string shorthand `"my-db": "target-name"` and populates Target
//     directly from the scalar).
//
// Resolver-side guarantees from upstream Step 1 mean every socket name
// this function sees is already known to the source entity's
// usesSchema — the resolver enforces socket-allowlist and required-socket
// rules before topology runs. Shape errors (non-array-non-map socket
// values, malformed verbose entries) surface at YAML or JSON unmarshal
// time via the typed wrapper's UnmarshalYAML / UnmarshalJSON hooks; this
// function only sees structurally valid input.
//
// Iteration order:
//
//   - Outer socket loop iterates uses.All() in author order — the
//     OrderedMap preserves YAML authoring order.
//   - Inner verbose-map alias loop iterates val.Verbose.All() in author
//     order. The TestNormalizeUses_VerboseFormPreservesAuthorOrder
//     regression test pins this discipline at the inner alias layer.
//
// The originFeature parameter is non-empty when the `uses` block being
// normalised lives on a Feature (or Secret) rather than directly on a
// Component. Each emitted edge carries the value into its OriginFeature
// field — the per-edge origin marker that keeps Feature-authored edges
// separable from Component-authored edges. IR's IREdge.OriginFeature is
// the downstream surface that consumes it.
func normalizeUses(uses types.OrderedMap[types.UsesSocketValue], sourceNodeID, originFeature string) ([]NormalizedEdge, error) {
	if uses.Len() == 0 {
		return nil, nil
	}

	var edges []NormalizedEdge

	for socket, val := range uses.All() {
		// Compact form: string array
		if len(val.Compact) > 0 {
			for _, target := range val.Compact {
				edges = append(edges, NormalizedEdge{
					Source:        sourceNodeID,
					Target:        target,
					Socket:        socket,
					Alias:         target,
					OriginFeature: originFeature,
				})
			}
			continue
		}

		// Verbose form: alias-keyed map (author order via OrderedMap.All).
		if val.Verbose.Len() > 0 {
			for alias, entry := range val.Verbose.All() {
				edges = append(edges, buildVerboseEdge(sourceNodeID, socket, alias, entry, originFeature))
			}
			continue
		}

		// Both Compact and Verbose empty — zero-value socket value, skip.
	}

	return edges, nil
}

// buildVerboseEdge constructs one NormalizedEdge from a typed verbose-form
// entry. Target defaults to the alias when the entry's Target field is
// empty after unmarshal — matching the schema's documented default for
// the verbose form's omitted-target shape. The entry's Inject pointer
// (nil when absent) carries through into the edge's Inject field directly;
// the InjectBlock's Env / File / Arg / Additional fields populate via
// standard YAML and JSON struct unmarshal of the inject mapping.
func buildVerboseEdge(source, socket, alias string, entry types.VerboseUsesEntry, originFeature string) NormalizedEdge {
	target := entry.Target
	if target == "" {
		target = alias
	}
	return NormalizedEdge{
		Source:        source,
		Target:        target,
		Socket:        socket,
		Alias:         alias,
		Inject:        entry.Inject,
		OriginFeature: originFeature,
	}
}
