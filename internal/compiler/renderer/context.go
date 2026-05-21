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

	// Features from feature_values_mapping (author-order preserved)
	features := types.OrderedMap[types.TemplateFeature]{}
	if fvm, ok := graph.Spec.FeatureValuesMapping.Get(nodeID); ok && fvm.Len() > 0 {
		for name, entry := range fvm.All() {
			features.Set(name, types.TemplateFeature{
				Type:   entry.Type,
				Values: entry.Values,
				Infra:  entry.Infra,
			})
		}
	}

	// Secrets from secrets mapping (author-order preserved)
	secrets := types.OrderedMap[types.TemplateSecret]{}
	if sec, ok := graph.Spec.Secrets.Get(nodeID); ok && sec.Len() > 0 {
		for name, entry := range sec.All() {
			secrets.Set(name, types.TemplateSecret{
				Type:   entry.Type,
				Values: entry.Values,
			})
		}
	}

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
