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
// It fills the node's identity and topology (instance_id, labels, the three
// cascading annotation buckets, the pre-hydrated inject pipe, and the enriched
// uses map keyed by the alias the developer gave each edge), the evaluated
// values and infra dictionaries, the node's own resolved provides, its
// attachments in author order, and the immutable inject_raw record of what the
// developer actually authored.
//
// Several fields are deliberately left empty here and filled by whoever owns
// them, which is the trap for a reader who assumes the returned context is
// final: locals are evaluated ahead of the node's provides and published onto
// the context later, each attachment's resolved provides arrive once its bound
// FeatureRecipe has been evaluated, and Release identity is folded in by
// AssembleNodeContextWithRelease. Reading one of those off a freshly assembled
// context yields the zero value rather than an error.
func AssembleNodeContext(
	nodeID string,
	graph *types.IRGraph,
	allProvides NodeProvides,
	declaredProvides NodeDeclaredProvides,
	inject types.InjectPipe,
	injectRaw any,
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
	// one shape onto one pillar. Provides is filled in by the orchestrator once
	// the bound FeatureRecipe has been evaluated.
	features := projectAttachments(graph.Spec.FeatureValuesMapping, nodeID)

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
			InstanceID:  encodeNodeIdentity(nodeID),
			Labels:      node.Labels,
			Annotations: annotations,
			Inject:      inject,
			Uses:        uses,
		},
		Values: values,
		Infra:  infra,
		// Locals starts empty. Evaluation happens earlier, in the Pass-1 preamble
		// ahead of each node's provides (see EvaluateRecipeLocals); publication onto
		// this context happens later, at Phase B.0, which overwrites the field with
		// that node's already-resolved map. The two are separate steps, and the field
		// is empty in between.
		Locals:    types.OrderedMap[string]{},
		Provides:  providesForNode,
		Features:  features,
		Metadata:  metadata,
		InjectRaw: injectRaw,
	}

	return ctx
}

// projectAttachments projects one node's attachments onto the template surface,
// preserving author order. One projection for every attachment family: a
// family-specific copy would be free to lose a pillar, which is how a pillar
// carried in the IR becomes unreadable from a template.
func projectAttachments(bucket types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]], nodeID string) types.OrderedMap[types.TemplateAttachment] {
	out := types.OrderedMap[types.TemplateAttachment]{}
	entries, ok := bucket.Get(nodeID)
	if !ok {
		return out
	}
	for name, entry := range entries.All() {
		out.Set(name, types.TemplateAttachment{
			Type:            entry.Type,
			ResolvedVersion: entry.ResolvedVersion,
			Labels:          entry.Labels,
			Values:          entry.Values,
			Infra:           entry.Infra,
		})
	}
	return out
}

// applyAttachmentProvides folds each attachment's resolved provides bag onto the
// projected attachment. Iteration runs over the projection's author order rather
// than the resolved-bag map so the write order is deterministic; an attachment
// with no resolved bag keeps its empty Provides.
func applyAttachmentProvides(attachments *types.OrderedMap[types.TemplateAttachment], provides map[string]types.OrderedMap[string]) {
	if len(provides) == 0 {
		return
	}
	for _, name := range attachments.Keys() {
		bag, ok := provides[name]
		if !ok {
			continue
		}
		att, _ := attachments.Get(name)
		att.Provides = bag
		attachments.Set(name, att)
	}
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
