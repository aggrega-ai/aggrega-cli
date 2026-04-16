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

	nodeIDs := make([]string, 0, len(graph.Spec.FeatureValuesMapping))
	for nodeID := range graph.Spec.FeatureValuesMapping {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		node, ok := graph.Spec.Nodes[nodeID]
		if !ok {
			continue
		}

		instances := graph.Spec.FeatureValuesMapping[nodeID]
		instanceNames := make([]string, 0, len(instances))
		for name := range instances {
			instanceNames = append(instanceNames, name)
		}
		sort.Strings(instanceNames)

		for _, instance := range instanceNames {
			entry := instances[instance]
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
	if len(spec.AppliesTo) > 0 {
		keys := make([]string, 0, len(spec.AppliesTo))
		for k := range spec.AppliesTo {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, spec.AppliesTo[k]))
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
