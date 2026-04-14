// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// AssembleNodeContext builds the full TemplateContext for one node,
// matching template-context-v1alpha1.schema.json.
func AssembleNodeContext(
	nodeID string,
	graph *types.IRGraph,
	allProvides NodeProvides,
	inject types.InjectPipe,
	injectRaw interface{},
) *types.TemplateContext {
	node := graph.Spec.Nodes[nodeID]

	// Values from values_mapping
	var values map[string]interface{}
	if raw, ok := graph.Spec.ValuesMapping[nodeID]; ok {
		_ = json.Unmarshal(raw, &values)
	}

	// Infra from infra_mapping
	var infra map[string]interface{}
	if raw, ok := graph.Spec.InfraMapping[nodeID]; ok {
		_ = json.Unmarshal(raw, &infra)
	}

	// Enriched uses
	uses := AssembleEnrichedUses(nodeID, graph, allProvides)

	// Annotation buckets
	var annotations *types.AnnotationBuckets
	if node.CatalogAnnotations != nil || node.ReleaseAnnotations != nil || node.AggregateAnnotations != nil {
		annotations = &types.AnnotationBuckets{
			Catalog:   node.CatalogAnnotations,
			Release:   node.ReleaseAnnotations,
			Aggregate: node.AggregateAnnotations,
		}
	}

	// Features from feature_values_mapping
	var features map[string]types.TemplateFeature
	if fvm, ok := graph.Spec.FeatureValuesMapping[nodeID]; ok && len(fvm) > 0 {
		features = make(map[string]types.TemplateFeature, len(fvm))
		for name, entry := range fvm {
			features[name] = types.TemplateFeature{
				Type:   entry.Type,
				Values: entry.Values,
				Infra:  entry.Infra,
			}
		}
	}

	// Secrets from secrets mapping
	var secrets map[string]types.TemplateSecret
	if sec, ok := graph.Spec.Secrets[nodeID]; ok && len(sec) > 0 {
		secrets = make(map[string]types.TemplateSecret, len(sec))
		for name, entry := range sec {
			secrets[name] = types.TemplateSecret{
				Type:   entry.Type,
				Values: entry.Values,
			}
		}
	}

	// Metadata from Release
	var metadata *types.TemplateMetadata
	if graph.Spec.Provenance.ReleaseVersion != "" || graph.Spec.Provenance.AggregateVersion != "" {
		metadata = &types.TemplateMetadata{
			AggregateName: graph.Metadata.AppName,
		}
	}

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID:  nodeID,
			Labels:      node.Labels,
			Annotations: annotations,
			Inject:      inject,
			Uses:        uses,
		},
		Values:   ensureMap(values),
		Infra:    ensureMap(infra),
		Outputs:  ensureStringMap(allProvides[nodeID]),
		Features: features,
		Secrets:  secrets,
		Metadata: metadata,
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
