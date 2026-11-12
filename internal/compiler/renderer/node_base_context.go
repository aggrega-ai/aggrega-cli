// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// NodeBaseContext is the pre-render per-node IR data shared between the
// Step 4b (Component / Feature) and Step 5b (Recipe / FeatureRecipe)
// assertion phases. Carries data available BEFORE the render-time passes
// (provides evaluation, inject hydration); excludes the render-time
// additions to keep the boundary clean.
//
// Step 5b adds Provides + Locals + Recipe on top of NodeBaseContext via
// AssertionContext population. The node's connections are read through the
// assembly the render context's uses map is built by; its values, infra and
// metadata are read from the graph here and by AssembleNodeContext alike.
type NodeBaseContext struct {
	NodeID   string
	Values   map[string]any
	Infra    map[string]any
	Uses     map[string]any
	Features map[string]any
	Metadata map[string]any

	// connections is the node's connections as the render context projects them,
	// keyed by alias: what each Uses entry reads the target's labels, the
	// connection's values and its attachments from, and what the declarations
	// withholding those attachments from a failure's text are read over.
	connections types.OrderedMap[types.EnrichedEdge]

	// connectionsMarked is, by alias, the leaves of each connection's own values
	// and infra the graph's edge marks — what withholds a connection's
	// configuration from a failure's text, read off the same graph the entries are.
	connectionsMarked map[string]redact.ConnectionKeys
}

// ExtractNodeBaseContext reads the IR graph at the given nodeID and
// returns the pre-render per-node context. Pure function: same inputs
// produce same outputs; no side effects. Returns a zero-value
// NodeBaseContext if the node is not present in the graph.
//
// Uses is populated from the IR's outbound edges sourced at this node, in
// the shape the author writes under `uses:`: one entry per socket, and under
// it one entry per connection on that socket, keyed by the connection's
// alias. Each connection's entry carries target, alias and, only where an
// attachment drew the connection, origin_feature — its identity — and three
// facts about it, under the names a Recipe template reads them by on
// .context.uses.<alias>: labels, the TARGET's labels; values, the values
// written on the connection; and features, the attachments written on it,
// keyed by the slot each fills. The three are read off the projection
// AssembleEnrichedUses and projectAttachments build for the render context,
// never derived here, so an assertion and a template cannot disagree about
// them. Each is present on every entry, empty where the connection carries
// none, as every pillar of this surface is. A socket carrying several
// connections therefore shows every one of them, and a socket-presence check
// (`hasKey .uses "<socket>"`) reads the socket key alone, whatever the count.
//
// Every edge on a built graph carries an alias: a list entry's alias is its
// target, a keyed entry's is the key the author wrote, and an empty one is
// refused before any assertion runs — the authoring schemas refuse an empty
// key and the target-existence rule an empty list entry. No two connections of
// a built node share an alias, on one socket or on two: the build refuses such
// a pair once the graph is final, before any assertion runs, so every
// connection the node holds has an entry of its own here.
//
// Provides values flow only at Step 5b via the per-node allProvides; this view
// carries nothing a connection's target published, so an assertion
// referencing .uses.<socket>.<alias>.target works while one referencing
// .uses.<socket>.<alias>.<provides_key> halts with missingkey=error. An
// attachment, the node's own or a connection's, is read in one shape — its
// type, resolved version and values — because its FeatureRecipe has not
// resolved its provides when a Component assertion runs.
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

	// The connections and the node's own attachments, taken from the one
	// projection the render context takes them from: a connection's labels and
	// values from AssembleEnrichedUses, and every attachment from
	// projectAttachments, which places one written on a connection under that
	// connection and leaves it out of the node's own — so the two surfaces cannot
	// disagree about which attachments are the node's, nor about what a
	// connection carries. Nothing resolved later is asked for: no provides is
	// carried into this view.
	projection := AssembleEnrichedUses(nodeID, graph, nil, nil)
	own, _ := projectAttachments(graph.Spec.FeatureValuesMapping, nodeID, nil, &projection)
	out.connections = projection
	out.connectionsMarked = connectionsWithheld(nodeID, graph)

	uses := make(map[string]any)
	for _, e := range graph.Spec.Edges {
		if e.Source != nodeID || e.Socket == "" {
			continue
		}
		onSocket, held := uses[e.Socket].(map[string]any)
		if !held {
			onSocket = make(map[string]any)
			uses[e.Socket] = onSocket
		}
		entry := map[string]any{"target": e.Target, "alias": e.Alias}
		if e.OriginFeature != "" {
			entry["origin_feature"] = e.OriginFeature
		}
		// A connection the projection holds no entry for — its target is not a
		// node of the graph — reads its three facts empty, as one carrying none.
		projected, _ := projection.Get(connectionAlias(e))
		if projected.Socket != e.Socket {
			projected = types.EnrichedEdge{}
		}
		entry["labels"] = projected.Labels.TemplateAlphabeticalMap()
		entry["values"] = projected.Values.TemplateAlphabeticalMapDeep()
		entry["features"] = assertionAttachments(projected.Features)
		onSocket[e.Alias] = entry
	}
	out.Uses = uses
	out.Features = assertionAttachments(own)

	node, ok := graph.Spec.Nodes.Get(nodeID)
	metadata := map[string]any{
		"instance_id":       encodeNodeIdentity(nodeID),
		"aggregate_name":    graph.Metadata.AggregateName,
		"release_version":   graph.Spec.Provenance.ReleaseVersion,
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

// assertionAttachments returns attachments in the one shape this surface reads
// every attachment in, the node's own and a connection's alike — its type, its
// resolved version and its own values — keyed as the render context keys them:
// the node's own by instance name, a connection's by slot.
func assertionAttachments(attachments types.OrderedMap[types.TemplateAttachment]) map[string]any {
	out := make(map[string]any, attachments.Len())
	for name, attachment := range attachments.All() {
		out[name] = map[string]any{
			"type":             attachment.Type,
			"resolved_version": attachment.ResolvedVersion,
			"values":           attachment.Values.TemplateAlphabeticalMapDeep(),
		}
	}
	return out
}

// withConnectionDeclarations returns keys widened by what the Feature of each
// attachment written on one of the node's connections declares sensitive, so an
// assertion quoting such a leaf at .uses.<socket>.<alias>.features.<slot> reports
// without it, exactly as a Recipe template quoting it at
// .context.uses.<alias>.features.<slot> does.
//
// The declarations are read by usesWithheld over the projection this view's
// entries were read from — the reading a render context's connections are
// withheld by, through the same Feature reader — and placed under each
// connection's socket, which is where this view shows a connection.
//
// The connection's own values are widened the same way: an assertion quoting a
// leaf the edge's marking names at .uses.<socket>.<alias>.values reports without
// it, as a Recipe template quoting it at .context.uses.<alias>.values does. A
// connection whose attachments declare nothing and whose configuration the edge
// marks nothing of leaves keys as they were.
func (b NodeBaseContext) withConnectionDeclarations(keys redact.Keys, feature func(featureType string) resolver.SensitiveLeaves) redact.Keys {
	declared := usesWithheld(b.connections, nil, feature).Attachments
	if len(declared) == 0 && len(b.connectionsMarked) == 0 {
		return keys
	}
	sockets := make(map[string]string, len(declared)+len(b.connectionsMarked))
	for alias := range declared {
		edge, _ := b.connections.Get(alias)
		sockets[alias] = edge.Socket
	}
	for alias := range b.connectionsMarked {
		edge, _ := b.connections.Get(alias)
		sockets[alias] = edge.Socket
	}
	if len(declared) > 0 {
		keys.UsesAttachments = declared
	}
	if len(b.connectionsMarked) > 0 {
		keys.UsesConnections = b.connectionsMarked
	}
	keys.ConnectionSockets = sockets
	return keys
}
