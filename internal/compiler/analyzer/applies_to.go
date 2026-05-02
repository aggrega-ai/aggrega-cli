// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/selector"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// validateAppliesTo performs the second of the two label-matching
// classes Step 4 runs: the Feature appliesTo check on every attached
// Feature instance. For each (nodeID, featureInstance) pair in the
// IR's FeatureValuesMapping the function looks up the Feature catalog
// entity by type and evaluates the three appliesTo forms against the
// target node's labels.
//
// Semantics. AND across appliesTo, appliesToExpressions, and
// appliesToAnyOf — all three must hold for the attachment to be valid;
// an omitted form is vacuously satisfied. Matching delegates to
// pkg/selector (MatchLabels / MatchExpressions / MatchAnyOf).
//
// Skip cases. Target-node misses and catalog Feature misses silently
// continue (defense-in-depth; Step 1's resolver guards prevent the
// user-facing case — see [ErrCatalogLookup]).
//
// Output. One [AnalysisError] per violation. Source carries the
// nodeID, Target carries the Feature instance name (a structural
// reuse of AnalysisError's edge-shaped fields for the per-feature
// case), Selector carries [formatAppliesTo]'s short summary of the
// constraint. Errors emerge in (nodeID, instance) order via
// pre-sorted iteration; no explicit post-sort.
func validateAppliesTo(graph *types.IRGraph, catalogLookup resolver.CatalogLookup) []AnalysisError {
	if catalogLookup.Feature == nil {
		return nil
	}

	var errs []AnalysisError

	nodeIDs := graph.Spec.FeatureValuesMapping.Keys()
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		node, ok := graph.Spec.Nodes.Get(nodeID)
		if !ok {
			continue
		}

		instances, _ := graph.Spec.FeatureValuesMapping.Get(nodeID)
		instanceNames := instances.Keys()
		sort.Strings(instanceNames)

		for _, instance := range instanceNames {
			entry, _ := instances.Get(instance)
			feat, found := catalogLookup.Feature(entry.Type)
			if !found {
				continue
			}

			if !selector.MatchLabels(feat.Spec.AppliesTo, node.Labels) ||
				!selector.MatchExpressions(feat.Spec.AppliesToExpressions, node.Labels) ||
				!selector.MatchAnyOf(feat.Spec.AppliesToAnyOf, node.Labels) {
				errs = append(errs, AnalysisError{
					Source:   nodeID,
					Target:   instance,
					Selector: formatAppliesTo(feat.Spec),
					Message: fmt.Sprintf(
						"feature %q (type %q) appliesTo constraint not satisfied by node %q labels %v",
						instance, entry.Type, nodeID, node.Labels,
					),
				})
			}
		}
	}

	return errs
}

// formatAppliesTo produces a short, deterministic single-line summary
// of a Feature's appliesTo constraint set for the [AnalysisError]
// Selector field. Format: "appliesTo{k1=v1,k2=v2}" with full
// key/value pairs alphabetical-by-key, optionally followed by
// "appliesToExpressions[N]" and "appliesToAnyOf[N]" carrying counts
// rather than content. The structured-form count-only treatment keeps
// error messages compact when a Feature carries many match-expressions
// or anyOf terms.
func formatAppliesTo(spec types.FeatureSpec) string {
	var parts []string
	if spec.AppliesTo.Len() > 0 {
		keys := spec.AppliesTo.Keys()
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			v, _ := spec.AppliesTo.Get(k)
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
		}
		parts = append(parts, "appliesTo{"+strings.Join(pairs, ",")+"}")
	}
	if len(spec.AppliesToExpressions) > 0 {
		parts = append(parts, fmt.Sprintf("appliesToExpressions[%d]", len(spec.AppliesToExpressions)))
	}
	if len(spec.AppliesToAnyOf) > 0 {
		parts = append(parts, fmt.Sprintf("appliesToAnyOf[%d]", len(spec.AppliesToAnyOf)))
	}
	return strings.Join(parts, " ")
}
