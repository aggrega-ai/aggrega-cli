// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/cognitio/aggrega-cli/internal/compiler/parse"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// NodeBaseContext is the pre-render per-node IR data shared between the
// Step 4b (Component / Feature) and Step 5b (Recipe / FeatureRecipe)
// assertion phases. Carries data available BEFORE the render-time passes
// (provides evaluation, inject hydration); excludes the render-time
// additions to keep the boundary clean.
//
// Step 5b adds Provides + Locals + Recipe on top of NodeBaseContext via
// AssertionContext population. The existing AssembleNodeContext stays
// unchanged at this milestone; future refactor may collapse the duplicate
// IR-graph reads into a shared extraction.
type NodeBaseContext struct {
	NodeID   string
	Values   map[string]any
	Infra    map[string]any
	Uses     map[string]any
	Secrets  map[string]any
	Metadata map[string]any
}

// ExtractNodeBaseContext reads the IR graph at the given nodeID and
// returns the pre-render per-node context. Pure function: same inputs
// produce same outputs; no side effects. Returns a zero-value
// NodeBaseContext if the node is not present in the graph.
//
// Uses is populated from the IR's outbound edges sourced at this node:
// each entry's Socket key maps to a small map carrying {target, alias,
// origin_feature}. Provides values flow only at Step 5b via the
// per-node allProvides; Step 4b's Uses field carries only the resolved
// target identity (no provides values), so a Step 4b assertion
// referencing .uses.<socket>.target works while a Step 4b assertion
// referencing .uses.<socket>.<provides_key> halts with missingkey=error.
func ExtractNodeBaseContext(nodeID string, graph *types.IRGraph) NodeBaseContext {
	out := NodeBaseContext{NodeID: nodeID}
	if graph == nil {
		return out
	}

	values := types.OrderedMap[any]{}
	if raw, ok := graph.Spec.ValuesMapping.Get(nodeID); ok {
		_ = parse.IRPayload(raw, &values)
	}
	out.Values = values.TemplateAlphabeticalMapDeep()

	infra := types.OrderedMap[any]{}
	if raw, ok := graph.Spec.InfraMapping.Get(nodeID); ok {
		_ = parse.IRPayload(raw, &infra)
	}
	out.Infra = infra.TemplateAlphabeticalMapDeep()

	uses := make(map[string]any)
	for _, e := range graph.Spec.Edges {
		if e.Source != nodeID || e.Socket == "" {
			continue
		}
		entry := map[string]any{"target": e.Target}
		if e.Alias != "" {
			entry["alias"] = e.Alias
		}
		if e.OriginFeature != "" {
			entry["origin_feature"] = e.OriginFeature
		}
		uses[e.Socket] = entry
	}
	out.Uses = uses

	secrets := make(map[string]any)
	if sec, ok := graph.Spec.Secrets.Get(nodeID); ok {
		for name, entry := range sec.All() {
			secrets[name] = map[string]any{
				"type":             entry.Type,
				"resolved_version": entry.ResolvedVersion,
				"values":           entry.Values.TemplateAlphabeticalMapDeep(),
			}
		}
	}
	out.Secrets = secrets

	node, ok := graph.Spec.Nodes.Get(nodeID)
	metadata := map[string]any{
		"instance_id":      nodeID,
		"aggregate_name":   graph.Metadata.AggregateName,
		"release_version":  graph.Spec.Provenance.ReleaseVersion,
		"aggregate_version": graph.Spec.Provenance.AggregateVersion,
	}
	if ok {
		metadata["component"] = node.Component
		metadata["resolved_version"] = node.ResolvedVersion
		metadata["boundary_path"] = node.BoundaryPath
	}
	out.Metadata = metadata

	return out
}
