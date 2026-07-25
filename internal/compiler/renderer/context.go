// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// AssembleNodeContext builds the full TemplateContext for one node,
// matching template-context-v1alpha1.schema.json.
func AssembleNodeContext(
	nodeID string,
	graph *types.IRGraph,
	allProvides NodeProvides,
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
	uses := AssembleEnrichedUses(nodeID, graph, allProvides)

	// Annotation buckets
	var annotations *types.AnnotationBuckets
	if node.CatalogAnnotations.Len() > 0 || node.ReleaseAnnotations.Len() > 0 || node.AggregateAnnotations.Len() > 0 {
		annotations = &types.AnnotationBuckets{
			Catalog:   node.CatalogAnnotations,
			Release:   node.ReleaseAnnotations,
			Aggregate: node.AggregateAnnotations,
		}
	}

	// Attachments from the feature and credential buckets (author-order
	// preserved). Both buckets project through the same shape, so an attachment
	// is read the same way whichever bucket carries it. Provides is filled in by
	// the orchestrator once the bound FeatureRecipe has been evaluated.
	features := projectAttachments(graph.Spec.FeatureValuesMapping, nodeID)
	secrets := projectAttachments(graph.Spec.Secrets, nodeID)

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
			InstanceID:  nodeID,
			Labels:      node.Labels,
			Annotations: annotations,
			Inject:      inject,
			Uses:        uses,
		},
		Values: values,
		Infra:  infra,
		// Locals starts empty; the orchestrator overwrites it with the resolved
		// Recipe.Spec.Locals map (Phase B preamble, see EvaluateRecipeLocals).
		Locals:    types.OrderedMap[string]{},
		Provides:  providesForNode,
		Features:  features,
		Secrets:   secrets,
		Metadata:  metadata,
		InjectRaw: injectRaw,
	}

	return ctx
}

// projectAttachments projects one node's attachment bucket onto the template
// surface, preserving author order. One projection for every attachment bucket:
// a bucket-specific copy would be free to lose a pillar, which is how a pillar
// carried in the IR becomes unreadable from a template.
func projectAttachments(bucket types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]], nodeID string) types.OrderedMap[types.TemplateAttachment] {
	out := types.OrderedMap[types.TemplateAttachment]{}
	entries, ok := bucket.Get(nodeID)
	if !ok {
		return out
	}
	for name, entry := range entries.All() {
		out.Set(name, types.TemplateAttachment{
			Type:   entry.Type,
			Values: entry.Values,
			Infra:  entry.Infra,
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
