// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/selector"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// validateSelectors checks every edge's usesSchema selector against the target node's labels.
// Every edge is guaranteed to have a usesSchema entry (Step 1 ErrUnknownUsesSocket invariant).
func validateSelectors(graph *types.IRGraph, catalogLookup resolver.CatalogLookup) []AnalysisError {
	if catalogLookup.Component == nil {
		return nil
	}

	var errs []AnalysisError

	for _, edge := range graph.Spec.Edges {
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
				Source:   edge.Source,
				Target:   edge.Target,
				Socket:   edge.Socket,
				Selector: selectorStr,
				Message:  fmt.Sprintf("selector parse error: %v", err),
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

	return errs
}
