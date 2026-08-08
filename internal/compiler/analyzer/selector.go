// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// validateSelectors performs the first of the two label-matching
// classes Step 4 runs: the usesSchema-selector check. For each edge in
// the IR graph the function picks a selector string from one of two
// catalog surfaces and evaluates it against the target node's labels.
//
// Selector source. Edges whose IREdge.OriginFeature is non-empty read
// the selector from the originating Feature catalog entity's
// usesSchema entry for the socket; other edges read from the consuming
// Component's usesSchema entry. The branch matches the edge-atomicity
// contract IR carries forward — Feature-authored edges participate in
// the originating Feature's usesSchema, not the consuming Component's.
//
// Skip cases. A usesSchema entry without a selector string is
// vacuously satisfied, and so is a socket with no usesSchema entry at
// all: an author may name a socket the catalog did not declare, and
// such a socket has no catalog entry to carry a selector, so there is
// nothing to evaluate and nothing missing. That is a legitimate
// authoring shape, not a gap — which is why the condition below is
// selector presence and not the edge's declaration marker. Selector
// presence is the question this pass actually asks; keying on the
// marker instead would put a second condition on one question and the
// two could then disagree. Catalog-lookup misses and target-node misses
// also silently continue: the resolver guards validate every component
// and feature reference, so those misses reflect internal invariants
// rather than authoring errors (see [ErrCatalogLookup]).
//
// Coverage, stated plainly because it changed: this check is exhaustive
// over DECLARED sockets. An edge on a socket the catalog did not declare
// is not label-constrained by anything, and what it may carry is bounded
// by the target's declared provides instead.
//
// Output. One [AnalysisError] per violation. Parse errors carry
// "selector parse error" framing; no-match errors carry target /
// labels / selector / socket framing. Errors sort by
// (source, target, socket).
func validateSelectors(ctx context.Context, graph *types.IRGraph, catalogLookup resolver.CatalogLookup) ([]AnalysisError, error) {
	if catalogLookup.Component == nil {
		return nil, nil
	}

	var errs []AnalysisError

	for _, edge := range graph.Spec.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceNode, ok := graph.Spec.Nodes.Get(edge.Source)
		if !ok {
			continue
		}

		// Look up source component to get usesSchema
		catComp, found := catalogLookup.Component(sourceNode.Component)
		if !found {
			continue
		}

		// If the edge originated from a Feature, look up Feature usesSchema instead
		var selectorStr string
		if edge.OriginFeature != "" && catalogLookup.Feature != nil {
			catFeat, fFound := catalogLookup.Feature(edge.OriginFeature)
			if fFound {
				if entry, ok := catFeat.Spec.UsesSchema.Get(edge.Socket); ok {
					selectorStr = entry.Selector
				}
			}
		} else {
			if entry, ok := catComp.Spec.UsesSchema.Get(edge.Socket); ok {
				selectorStr = entry.Selector
			}
		}

		if selectorStr == "" {
			continue
		}

		// Get target node labels
		targetNode, ok := graph.Spec.Nodes.Get(edge.Target)
		if !ok {
			continue
		}

		match, err := selector.MatchString(selectorStr, targetNode.Labels)
		if err != nil {
			errs = append(errs, AnalysisError{
				Source:     edge.Source,
				Target:     edge.Target,
				Socket:     edge.Socket,
				Selector:   selectorStr,
				Message:    fmt.Sprintf("selector parse error: %v", err),
				OriginFile: sourceFileFor(graph, edge.Source),
				Field:      selectorFieldPath(edge.Source, edge.Socket, sourceNode.SubsystemPath),
				Sentinel:   ErrSelectorViolation(),
			})
			continue
		}

		if !match {
			errs = append(errs, AnalysisError{
				Source:   edge.Source,
				Target:   edge.Target,
				Socket:   edge.Socket,
				Selector: selectorStr,
				Message: fmt.Sprintf(
					"target %q labels %v do not match selector %q on socket %q",
					edge.Target, targetNode.Labels, selectorStr, edge.Socket,
				),
				OriginFile: sourceFileFor(graph, edge.Source),
				Field:      selectorFieldPath(edge.Source, edge.Socket, sourceNode.SubsystemPath),
				Sentinel:   ErrSelectorViolation(),
			})
		}
	}

	// Sort for determinism
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].Source != errs[j].Source {
			return errs[i].Source < errs[j].Source
		}
		if errs[i].Target != errs[j].Target {
			return errs[i].Target < errs[j].Target
		}
		return errs[i].Socket < errs[j].Socket
	})

	return errs, nil
}
