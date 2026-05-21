// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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

// flattenAggregate recursively walks an Aggregate tree, flattening all
// components (including sub-aggregate components) into a single flat
// node map with dot-notation IDs. The dot is reserved as the
// compiler-internal path separator; user-authored instance names cannot
// contain dots, so synthesised dotted IDs are unambiguous. Author order
// is preserved at every level by iterating OrderedMap-backed
// collections — root-topology blocks (`components`, `subsystems`,
// `aggregates`) are YAML maps keyed by user-authored instance names so
// the parser preserves declaration order.
//
// Walk order — three passes over the current Aggregate level, each in
// author order:
//
//  1. Top-level components (agg.Spec.Components). Each becomes a node
//     with the current `prefix` and `subsystemPath`; collectAttachments
//     records its features and secrets.
//  2. Subsystems (agg.Spec.Subsystems). flattenSubsystem walks each
//     subsystem's components and nested sub-subsystems, accumulating
//     dotted subsystemPath segments.
//  3. Sub-aggregate imports (agg.Spec.Aggregates). For each, the loader
//     resolves the type URI to a child Aggregate; the recursive call
//     uses the imported Aggregate's name as the new prefix and merges
//     the child's flattened nodes / attachments / exported components
//     back into the parent's result.
//
// Two cycle / depth disciplines protect the recursion:
//
//   - The `visited` set is keyed by sub-aggregate type URI, copied per
//     branch (copyVisited) so siblings don't poison each other; a
//     repeat type URI on the same branch produces ErrSubAggregateLoad
//     wrapping a "circular" message.
//   - The `depth` counter is bounded by maxSubAggregateDepth (10);
//     exceeding it produces ErrMaxDepth. The depth bound subsumes cycle
//     detection when the visited-set check would have otherwise allowed
//     infinite recursion via type-renaming tricks.
//
// Note on the runtime-graph cycle distinction: flattenAggregate's
// cycle / depth checks operate on the sub-aggregate INCLUSION graph
// (which authoring file embeds which). The runtime `uses` graph
// supports cycles without restriction; it is built later in
// normalizeUses. The two graphs are unrelated and the cycle rules
// differ deliberately — runtime back-edges are a legitimate authoring
// shape (multi-agent loops); inclusion cycles are authoring bugs.
func flattenAggregate(
	agg *types.Aggregate,
	prefix string,
	subsystemPath string,
	loader SubAggregateLoader,
	visited map[string]bool,
	depth int,
) (*flattenResult, error) {
	if depth > maxSubAggregateDepth {
		return nil, cerrors.Wrap(ErrMaxDepth, cerrors.DeveloperError, cerrors.SourceLocation{File: prefix}, fmt.Sprintf("depth %d at prefix %q", depth, prefix))
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
		if existing, exists := result.Nodes.Get(nodeID); exists {
			return nil, cerrors.Wrap(ErrSubAggregateNameCollision, cerrors.DeveloperError, cerrors.SourceLocation{File: prefix}, fmt.Sprintf("id %q (path %q) collides with existing entry from %q", nodeID, prefix, existing.SourceAggregate))
		}
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
		if err := flattenSubsystem(result, prefix, subPath, sub); err != nil {
			return nil, err
		}
	}

	// Walk sub-aggregates in author order
	if loader != nil {
		for aggName, subAgg := range agg.Spec.Aggregates.All() {
			childPrefix := qualifiedID(prefix, aggName)

			if visited[subAgg.Type] {
				return nil, cerrors.Wrap(ErrSubAggregateLoad, cerrors.DeveloperError, cerrors.SourceLocation{File: childPrefix}, fmt.Sprintf("circular sub-aggregate reference %q at %q", subAgg.Type, childPrefix))
			}

			childAgg, err := loader(subAgg.Type)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: childPrefix}, fmt.Sprintf("%v", ErrSubAggregateLoad))
			}

			childVisited := copyVisited(visited)
			childVisited[subAgg.Type] = true

			childResult, err := flattenAggregate(childAgg, childPrefix, "", loader, childVisited, depth+1)
			if err != nil {
				return nil, err
			}

			// Merge child results — preserve child's author-order during merge.
			for id, node := range childResult.Nodes.All() {
				if existing, exists := result.Nodes.Get(id); exists {
					return nil, cerrors.Wrap(ErrSubAggregateNameCollision, cerrors.DeveloperError, cerrors.SourceLocation{File: childPrefix}, fmt.Sprintf("id %q (path %q) collides with existing entry from %q", id, childPrefix, existing.SourceAggregate))
				}
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

// flattenSubsystem walks a single subsystem's components and any nested
// sub-subsystems, appending each component to the parent flattenResult's
// node map and accumulating dotted subsystemPath segments as the recursion
// descends. The subsystemPath ends up on each TopologyNode and feeds IR's
// Tier-3 Onion Model traversal (collectSubsystemFeatures in
// internal/compiler/ir/features.go) — the path string is what tells IR
// which subsystem-level features cascade to the node and in what
// outer-to-inner order.
//
// Subsystems are not gated by the sub-aggregate cycle / depth bounds:
// they are part of the authoring root of the same Aggregate, so the
// only bound is the YAML schema's structural validity. Pathologically
// deep subsystem nesting in a single Aggregate would still reach the
// recursion stack limit, but no architectural anchor enshrines a depth
// bound for subsystem nesting today.
//
// Returns ErrSubAggregateNameCollision (wrapped) if a Set on the flat
// node map would overwrite an existing entry. The collision is impossible
// by current schema (root-level instance names cannot contain dots) but
// the guard defends against future schema loosening or validation bypass.
func flattenSubsystem(result *flattenResult, prefix, subsystemPath string, sub types.Subsystem) error {
	for name, comp := range sub.Components.All() {
		nodeID := qualifiedID(prefix, name)
		if existing, exists := result.Nodes.Get(nodeID); exists {
			return cerrors.Wrap(ErrSubAggregateNameCollision, cerrors.DeveloperError, cerrors.SourceLocation{File: prefix}, fmt.Sprintf("id %q (path %q) collides with existing entry from %q", nodeID, prefix, existing.SourceAggregate))
		}
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
		if err := flattenSubsystem(result, prefix, nestedPath, nested); err != nil {
			return err
		}
	}
	return nil
}

// collectAttachments records a node's feature and secret attachments
// into the per-node slices on flattenResult, preserving author order
// from the Aggregate's component instance. The IR builder reads
// FeatureAttachments[nodeID] and SecretAttachments[nodeID] in this
// order and feeds them through the per-node feature-cascade
// normalisation. Author order is the only ordering applied here; later
// merge passes inside IR may dedupe by instance name with "last wins"
// semantics, but topology surfaces the raw author-attached entries.
//
// The `tier` parameter is currently always 0 from both call sites; the
// SourceTier field on the resulting attachment is therefore always 0
// today. The field exists for future use; SourceTier carries no
// meaningful tier information in the current implementation.
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
