// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// gatherTargetRenderFacts reads, for each connection a node makes, what that
// connection's target reported, from the record each node's turn left.
//
// One entry per connection, keyed by the alias the connection is held under in the
// node's uses map, in that map's order, so the entries line up one for one with the
// connections a template reads at .context.uses. A target with no record reports
// nothing, which is the same answer as a target whose provider reports nothing.
//
// The records are the ones each node's own turn left, so what a target's attachment
// held for the pass after the walk reported is absent here exactly as it is from a
// caller's entry: a held attachment never reads another held attachment's output.
func gatherTargetRenderFacts(
	uses types.OrderedMap[types.EnrichedEdge],
	recorded map[string]renderFactsRecord,
) types.OrderedMap[renderFactsRecord] {
	var out types.OrderedMap[renderFactsRecord]
	for alias, edge := range uses.All() {
		out.Set(alias, recorded[edge.Target])
	}
	return out
}

// usesWithTargetFacts returns the connections a node makes with each target's render
// facts on its entry, for the context of an attachment whose FeatureRecipe declares
// it reads them.
//
// The map is rebuilt rather than written in place, and the rebuild is the point: an
// ordered map shares its storage with every copy of the value, and the node's own
// map is the one its Recipe rendered against and every other attachment of the node
// is handed — an attachment that does not declare the read must never meet the facts
// there. Every entry carries the facts, empty for a target whose providers reported
// nothing, so a template ranging the connections reads each one's facts the same way.
//
// targets is what gatherTargetRenderFacts read for the same connections; a
// connection it holds no entry for is refused rather than handed a target's facts
// it was never gathered for.
func usesWithTargetFacts(
	uses types.OrderedMap[types.EnrichedEdge],
	targets types.OrderedMap[renderFactsRecord],
) (types.OrderedMap[types.EnrichedEdge], error) {
	var out types.OrderedMap[types.EnrichedEdge]
	if targets.Len() != uses.Len() {
		return out, fmt.Errorf("internal error: the node makes %d connection(s) but %d target report(s) were gathered for them",
			uses.Len(), targets.Len())
	}
	for alias, edge := range uses.All() {
		rec, gathered := targets.Get(alias)
		if !gathered {
			return types.OrderedMap[types.EnrichedEdge]{}, fmt.Errorf(
				"internal error: no target report was gathered for the connection %q", alias)
		}
		facts, err := rec.onContext()
		if err != nil {
			return types.OrderedMap[types.EnrichedEdge]{}, fmt.Errorf("connection %q to %q: %w", alias, edge.Target, err)
		}
		edge.RenderFacts = facts
		out.Set(alias, edge)
	}
	return out, nil
}
