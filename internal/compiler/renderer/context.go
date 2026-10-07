// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AssembleNodeContext builds the TemplateContext for one node, matching
// template-context-v1alpha1.schema.json. The context is the projection Recipe
// and FeatureRecipe templates are written against — a view assembled in memory
// for the duration of the render and never persisted — so what this function
// puts on it is exactly what a template can read.
//
// It fills the node's identity and topology (instance_id, the name its provider
// reports for the resources it renders for the node, labels, the
// three cascading annotation buckets, the pre-hydrated inject pipe, and the
// enriched uses map keyed by the alias the developer gave each edge), the
// evaluated values and infra dictionaries, the node's own resolved provides, its
// attachments in author order, and the immutable inject_raw record of what the
// developer actually authored.
//
// It does not fill the connections other nodes make TO this one. Those reach only
// the context a Feature attached to the node renders its own resources against
// (see AssembleInboundEdges), so this context — and every byte rendered from it —
// is the same however many nodes connect to the node.
//
// attachmentProvides is the node's attachment bag as ResolveAttachmentProvides
// resolved it, keyed by attachment name. It is folded onto every projected
// attachment here — the node's own under .features, and each one a connection
// carries under that connection in the uses map — so a consumer's read of an
// attachment's provides and the resource that attachment's FeatureRecipe
// generates derive from one evaluation. A nil bag leaves every attachment's
// provides empty.
//
// The reported name is taken from the pass that collected it before any node was
// rendered rather than asked for here, so every surface that reads it reads one
// answer. A node whose provider reports none carries an empty value, and the rule
// declared over authored text refuses a template that reads it in that case.
//
// Several fields are deliberately left empty here and filled by whoever owns
// them, which is the trap for a reader who assumes the returned context is
// final: locals are evaluated ahead of the node's provides and published onto
// the context later, and Release identity is folded in by
// AssembleNodeContextWithRelease. Reading one of those off a freshly assembled
// context yields the zero value rather than an error.
func AssembleNodeContext(
	nodeID string,
	graph *types.IRGraph,
	allProvides NodeProvides,
	declaredProvides NodeDeclaredProvides,
	inject types.InjectPipe,
	injectRaw any,
	renderedNames NodeRenderedNames,
	attachmentProvides map[string]types.OrderedMap[string],
) *types.TemplateContext {
	node, _ := graph.Spec.Nodes.Get(nodeID)

	// Values from values_mapping
	values := types.OrderedMap[any]{}
	if raw, ok := graph.Spec.ValuesMapping.Get(nodeID); ok {
		_ = parse.IRPayload(raw, &values)
	}

	// Infra from infra_mapping
	infra := types.OrderedMap[any]{}
	if raw, ok := graph.Spec.InfraMapping.Get(nodeID); ok {
		_ = parse.IRPayload(raw, &infra)
	}

	// Enriched uses
	uses := AssembleEnrichedUses(nodeID, graph, allProvides, declaredProvides)

	// Annotation buckets
	var annotations *types.AnnotationBuckets
	if node.CatalogAnnotations.Len() > 0 || node.ReleaseAnnotations.Len() > 0 || node.AggregateAnnotations.Len() > 0 {
		annotations = &types.AnnotationBuckets{
			Catalog:   node.CatalogAnnotations,
			Release:   node.ReleaseAnnotations,
			Aggregate: node.AggregateAnnotations,
		}
	}

	// Every attachment the node holds, author-order preserved, projected through
	// one shape with its resolved provides: the node's own onto the `.features`
	// pillar, and each one a connection carries onto that connection's entry in
	// the uses map — and, by the name it takes on the node, beside the pillar for
	// its own FeatureRecipe.
	features, connectionAttachments := projectAttachments(graph.Spec.FeatureValuesMapping, nodeID, attachmentProvides, &uses)

	// Metadata from Release
	var metadata *types.TemplateMetadata
	if graph.Spec.Provenance.ReleaseVersion != "" || graph.Spec.Provenance.AggregateVersion != "" {
		metadata = &types.TemplateMetadata{
			AggregateName: graph.Metadata.AggregateName,
		}
	}

	providesForNode := allProvides[nodeID]

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: encodeNodeIdentity(nodeID),
			// Empty where the node's provider reports no such name, which is the
			// answer rather than an omission: what the compiler built for the node is
			// named after the identity above.
			RenderedName: renderedNames[nodeID],
			Labels:       node.Labels,
			Annotations:  annotations,
			Inject:       inject,
			Uses:         uses,
		},
		Values: values,
		Infra:  infra,
		// Locals starts empty. Evaluation happens earlier, in the Pass-1 preamble
		// ahead of each node's provides (see EvaluateRecipeLocals); publication onto
		// this context happens later, at Phase B.0, which overwrites the field with
		// that node's already-resolved map. The two are separate steps, and the field
		// is empty in between.
		Locals:                types.OrderedMap[string]{},
		Provides:              providesForNode,
		Features:              features,
		Metadata:              metadata,
		InjectRaw:             injectRaw,
		ConnectionAttachments: connectionAttachments,
	}

	return ctx
}

// projectAttachments projects one node's attachments onto the template surface,
// preserving author order. One projection for every attachment family: a
// family-specific copy would be free to lose a pillar, which is how a pillar
// carried in the IR becomes unreadable from a template. Every surface that
// exposes a node's attachments takes its attachment set from here, so which of
// them are the node's own is decided once.
//
// provides is the node's resolved attachment bag, keyed by attachment name; each
// projected attachment carries its own bag, and one with none keeps an empty
// Provides. The bag is read here and never evaluated, so every place an
// attachment is shown holds the one evaluation.
//
// An attachment one of the node's connections carries is projected into the
// second result rather than the first. It is one of the node's attachments for
// everything the graph does with one — its own FeatureRecipe renders against it —
// but it was written on a connection rather than on the node's own block, and a
// template ranging the node's `.features` to find its own attachments by type
// would otherwise be handed a connection's credential as if the node had attached
// it. Whether an entry came from a connection is read off the entry's own mark,
// never out of its name.
//
// Where uses is non-nil, each such attachment is also placed on the entry of the
// connection it was written on, under the slot name its socket declares: the
// entry whose alias and socket are the ones the mark records. The value placed
// there is the one projected value, so the connection view and the name a
// FeatureRecipe finds it by cannot come to carry an attachment in different
// shapes. An attachment whose connection has no entry in uses — no edge was
// projected for it — is shown under no connection.
func projectAttachments(
	bucket types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]],
	nodeID string,
	provides map[string]types.OrderedMap[string],
	uses *types.OrderedMap[types.EnrichedEdge],
) (own, onConnections types.OrderedMap[types.TemplateAttachment]) {
	own = types.OrderedMap[types.TemplateAttachment]{}
	onConnections = types.OrderedMap[types.TemplateAttachment]{}
	entries, ok := bucket.Get(nodeID)
	if !ok {
		return own, onConnections
	}
	for name, entry := range entries.All() {
		projected := types.TemplateAttachment{
			Type:            entry.Type,
			ResolvedVersion: entry.ResolvedVersion,
			Labels:          entry.Labels,
			Values:          entry.Values,
			Infra:           entry.Infra,
			Provides:        provides[name],
		}
		if !entry.OnConnection() {
			own.Set(name, projected)
			continue
		}
		onConnections.Set(name, projected)
		if uses == nil {
			continue
		}
		conn := entry.Connection
		edge, found := uses.Get(conn.Alias)
		if !found || edge.Socket != conn.Socket {
			continue
		}
		edge.Features.Set(conn.Attachment, projected)
		uses.Set(conn.Alias, edge)
	}
	return own, onConnections
}

// AssembleNodeContextWithRelease enriches the context with Release metadata.
func AssembleNodeContextWithRelease(
	ctx *types.TemplateContext,
	release *types.Release,
) {
	if release == nil || ctx.Metadata == nil {
		if release != nil {
			ctx.Metadata = &types.TemplateMetadata{}
		} else {
			return
		}
	}
	ctx.Metadata.ReleaseName = release.Metadata.Name
	ctx.Metadata.ReleaseVersion = release.Metadata.Version
	ctx.Metadata.Environment = release.Metadata.Environment
}
