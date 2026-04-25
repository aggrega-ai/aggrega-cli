// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"fmt"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

const maxSubAggregateDepth = 10

// flattenResult is the intermediate output of aggregate flattening.
//
// Nodes uses OrderedMap so the Aggregate's author-declared component order
// (and the order of components in any sub-aggregate it imports) survives
// subsystem flattening. Downstream consumers (IR construction → IR JSON
// emission) rely on this author-order propagation per the deterministic-
// ordering contract.
type flattenResult struct {
	Nodes              types.OrderedMap[*TopologyNode]
	FeatureAttachments map[string][]FeatureAttachment
	SecretAttachments  map[string][]SecretAttachment
	ExportedComponents map[string]string
}

// flattenAggregate recursively walks an Aggregate tree, flattening all components
// (including sub-aggregate components) into a flat node map with dot-notation IDs.
// Walks are in author order (OrderedMap iteration).
func flattenAggregate(
	agg *types.Aggregate,
	prefix string,
	subsystemPath string,
	loader SubAggregateLoader,
	visited map[string]bool,
	depth int,
) (*flattenResult, error) {
	if depth > maxSubAggregateDepth {
		return nil, fmt.Errorf("%w: depth %d at prefix %q", ErrMaxDepth, depth, prefix)
	}

	result := &flattenResult{
		Nodes:              types.OrderedMap[*TopologyNode]{},
		FeatureAttachments: make(map[string][]FeatureAttachment),
		SecretAttachments:  make(map[string][]SecretAttachment),
		ExportedComponents: make(map[string]string),
	}

	// Walk top-level components in author order
	for name, comp := range agg.Spec.Components.All() {
		nodeID := qualifiedID(prefix, name)
		result.Nodes.Set(nodeID, &TopologyNode{
			InstanceID:      nodeID,
			ComponentType:   comp.Type,
			Values:          comp.Values,
			SelfInject:      comp.Inject,
			SubsystemPath:   subsystemPath,
			SourceAggregate: prefix,
		})
		collectAttachments(result, nodeID, comp, 0)
	}

	// Walk subsystems in author order (recursive)
	for subName, sub := range agg.Spec.Subsystems.All() {
		subPath := qualifiedID(subsystemPath, subName)
		flattenSubsystem(result, prefix, subPath, sub)
	}

	// Walk sub-aggregates in author order
	if loader != nil {
		for aggName, subAgg := range agg.Spec.Aggregates.All() {
			childPrefix := qualifiedID(prefix, aggName)

			if visited[subAgg.Type] {
				return nil, fmt.Errorf("%w: circular sub-aggregate reference %q at %q", ErrSubAggregateLoad, subAgg.Type, childPrefix)
			}

			childAgg, err := loader(subAgg.Type)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrSubAggregateLoad, err)
			}

			childVisited := copyVisited(visited)
			childVisited[subAgg.Type] = true

			childResult, err := flattenAggregate(childAgg, childPrefix, "", loader, childVisited, depth+1)
			if err != nil {
				return nil, err
			}

			// Merge child results — preserve child's author-order during merge.
			for id, node := range childResult.Nodes.All() {
				result.Nodes.Set(id, node)
			}
			for id, feats := range childResult.FeatureAttachments {
				result.FeatureAttachments[id] = append(result.FeatureAttachments[id], feats...)
			}
			for id, secs := range childResult.SecretAttachments {
				result.SecretAttachments[id] = append(result.SecretAttachments[id], secs...)
			}

			// Rewire exported_components: alias scoped to sub-aggregate prefix
			if childAgg != nil {
				for exportAlias, localRef := range childAgg.Spec.ExportedComponents.All() {
					resolvedID := qualifiedID(childPrefix, localRef)
					result.ExportedComponents[qualifiedID(childPrefix, exportAlias)] = resolvedID
				}
			}
		}
	}

	return result, nil
}

func flattenSubsystem(result *flattenResult, prefix, subsystemPath string, sub types.Subsystem) {
	for name, comp := range sub.Components.All() {
		nodeID := qualifiedID(prefix, name)
		result.Nodes.Set(nodeID, &TopologyNode{
			InstanceID:      nodeID,
			ComponentType:   comp.Type,
			Values:          comp.Values,
			SelfInject:      comp.Inject,
			SubsystemPath:   subsystemPath,
			SourceAggregate: prefix,
		})
		collectAttachments(result, nodeID, comp, 0)
	}

	// Recurse into nested subsystems
	for subName, nested := range sub.Subsystems.All() {
		nestedPath := subsystemPath + "." + subName
		flattenSubsystem(result, prefix, nestedPath, nested)
	}
}

func collectAttachments(result *flattenResult, nodeID string, comp types.ComponentInstance, tier int) {
	// Features (author order)
	for fname, feat := range comp.Features.All() {
		result.FeatureAttachments[nodeID] = append(result.FeatureAttachments[nodeID], FeatureAttachment{
			InstanceName: fname,
			Feature:      feat,
			SourceTier:   tier,
		})
	}

	// Secrets (author order)
	for sname, sec := range comp.Secrets.All() {
		result.SecretAttachments[nodeID] = append(result.SecretAttachments[nodeID], SecretAttachment{
			InstanceName: sname,
			Secret:       sec,
			SourceTier:   tier,
		})
	}
}

func qualifiedID(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func copyVisited(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
