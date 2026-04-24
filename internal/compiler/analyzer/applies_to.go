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

// validateAppliesTo checks every Feature attachment against the target node's labels,
// using the Feature's appliesTo / appliesToExpressions / appliesToAnyOf constraints.
// Semantics: AND across the three forms (all constraints must pass); constraint omitted ⇒ vacuously true.
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

// formatAppliesTo produces a short, deterministic string for the error selector field.
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
