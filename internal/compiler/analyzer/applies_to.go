// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// validateAppliesTo performs the second of the two label-matching
// classes Step 4 runs: the Feature appliesTo check on every attached
// Feature instance. For each (nodeID, featureInstance) pair in the
// IR's FeatureValuesMapping the function looks up the Feature catalog
// entity by type and evaluates the two appliesTo surfaces against the
// target node's labels.
//
// Semantics. AND across appliesTo (a string-form selector; requirements
// inside the string AND together) and appliesToAnyOf (OR across items) —
// both must hold for the attachment to be valid; an omitted surface is
// vacuously satisfied (an empty selector string parses to zero
// requirements; an empty items list matches everything). Matching
// delegates to pkg/selector (MatchString / MatchAnyOf).
//
// A selector that fails to parse produces its own [AnalysisError] with a
// "selector parse error" message — the same convention the usesSchema
// selector validator uses — rather than being swallowed or conflated
// with a label mismatch.
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
func validateAppliesTo(ctx context.Context, graph *types.IRGraph, catalogLookup resolver.CatalogLookup) ([]AnalysisError, error) {
	if catalogLookup.Feature == nil {
		return nil, nil
	}

	var errs []AnalysisError

	nodeIDs := graph.Spec.FeatureValuesMapping.Keys()
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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

			strMatch, strErr := selector.MatchString(feat.Spec.AppliesTo, node.Labels)
			anyMatch, anyErr := selector.MatchAnyOf(feat.Spec.AppliesToAnyOf, node.Labels)
			if parseErr := strErr; parseErr != nil || anyErr != nil {
				if parseErr == nil {
					parseErr = anyErr
				}
				errs = append(errs, AnalysisError{
					Source:     nodeID,
					Target:     instance,
					Selector:   formatAppliesTo(feat.Spec),
					Message:    fmt.Sprintf("selector parse error: %v", parseErr),
					OriginFile: sourceFileFor(graph, nodeID),
					Field:      appliesToFieldPath(nodeID, instance, node.SubsystemPath),
					Sentinel:   ErrSelectorViolation(),
				})
				continue
			}

			if !strMatch || !anyMatch {
				errs = append(errs, AnalysisError{
					Source:   nodeID,
					Target:   instance,
					Selector: formatAppliesTo(feat.Spec),
					Message: fmt.Sprintf(
						"feature %q (type %q) appliesTo constraint not satisfied by node %q labels %v",
						instance, entry.Type, nodeID, node.Labels,
					),
					OriginFile: sourceFileFor(graph, nodeID),
					Field:      appliesToFieldPath(nodeID, instance, node.SubsystemPath),
					Sentinel:   ErrSelectorViolation(),
				})
			}
		}
	}

	return errs, nil
}

// formatAppliesTo produces a short, deterministic single-line summary
// of a Feature's appliesTo constraint set for the [AnalysisError]
// Selector field. Format: the appliesTo selector string verbatim in
// quotes ("appliesTo:\"tier in (a, b)\"" — it is already a compact
// single line), optionally followed by "appliesToAnyOf[N]" carrying the
// item count rather than content. The count-only anyOf treatment keeps
// error messages compact when a Feature carries many disjuncts.
func formatAppliesTo(spec types.FeatureSpec) string {
	var parts []string
	if spec.AppliesTo != "" {
		parts = append(parts, fmt.Sprintf("appliesTo:%q", spec.AppliesTo))
	}
	if len(spec.AppliesToAnyOf) > 0 {
		parts = append(parts, fmt.Sprintf("appliesToAnyOf[%d]", len(spec.AppliesToAnyOf)))
	}
	return strings.Join(parts, " ")
}
