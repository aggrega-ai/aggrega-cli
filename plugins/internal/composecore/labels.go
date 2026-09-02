// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// composeOmittedProvenanceKeys are the provenance keys the shared builders emit
// that compose deliberately does not carry. Compose has no annotation concept and
// its service-label values are unconstrained, so it folds the whole provenance
// set — labels plus annotations — into a single labels map rather than splitting
// on the Kubernetes label-value charset, and drops the keys only a Kubernetes
// runtime consumes. The plain `aggrega.ai/sync-wave` provenance entry is
// retained.
//
// The set itself is the shared one, because the question "which provenance keys
// mean nothing off Kubernetes" has one answer for every such target rather than a
// compose answer and a per-provider answer beside it. The name is kept here
// because it is the vocabulary this package's own assertions read.
var composeOmittedProvenanceKeys = rendercore.KubernetesOnlyProvenanceKeys

// injectAggregaLabelsIntoServices walks the services: map of a YAML-
// decoded compose document and stamps the canonical aggrega.ai/* label
// set onto each service whose origin is recorded in the origins map.
// Used by sourced-mode rendering after the base + valuesMapping deep-
// merge so labels land on services with provenance derived from the
// service's actual emission origin, not from a single shared nodeCtx.
//
// Origin contract. The origins map is keyed by service name; the value
// is the NodeRenderContext that emitted that service. The caller is
// responsible for populating the map with one entry per service whose
// labels should be compiler-managed:
//
//   - Sourced-mode caller (this package's renderSourced): every service
//     in the merged base+overlay maps to the sole consumer nodeCtx —
//     the single-origin case. All services get consumer labels.
//   - A future merge of fragments drawn from several contexts would
//     populate one entry per service pointing at the originating
//     fragment's nodeCtx — services emitted by an FR map to the FR's
//     nodeCtx; services emitted by a regular Recipe map to that
//     Recipe's nodeCtx; services authored deliberately at higher levels
//     map to the appropriate authoring context.
//
// Services NOT present in the origins map are left untouched. That
// skip serves multi-FeatureRecipe composition, where one
// FeatureRecipe's fragment may carry services contributed by an
// upstream FeatureRecipe: the upstream emission step would have
// already populated origins for its services, and the downstream
// injection step skips those, deferring to the authoritative
// provenance set upstream. It awaits a caller that builds the map per
// service; the single-origin builder below is the only one that ships.
//
// The skip is not a way to preserve a catalog author's hand-set
// aggrega.ai/* labels on a base compose service. Single-origin
// rendering maps every service in the merged document to the one node
// context, so no service is absent from origins and the origin-derived
// keys overwrite the author's aggrega.ai/* values. The one repair that
// would change that is reading label presence as a claim of ownership,
// and it is refused: it would have the compiler infer meaning from
// label content. Author-set keys outside the aggrega.ai/* space
// survive regardless, as the merge rule below states.
//
// Existing label keys on a service are merged with — not replaced by —
// the origin-derived labels: author-set non-aggrega.ai/* keys
// (`tier: backend`, etc.) are preserved; origin-derived aggrega.ai/*
// keys overwrite any pre-existing aggrega.ai/* values for the same key.
//
// The function tolerates malformed compose documents silently — sourced-
// mode treats the input as authored YAML, and label injection is a
// metadata enhancement, not a validation gate.
func injectAggregaLabelsIntoServices(doc map[string]any, origins map[string]rendercore.NodeRenderContext) {
	if doc == nil || len(origins) == 0 {
		return
	}
	servicesRaw, ok := doc["services"]
	if !ok {
		return
	}
	services, ok := asStringMap(servicesRaw)
	if !ok {
		return
	}
	for name, svcRaw := range services {
		origin, hasOrigin := origins[name]
		if !hasOrigin {
			// No recorded origin means no provenance to stamp; an
			// upstream contributor's labels stand.
			continue
		}
		svc, ok := asStringMap(svcRaw)
		if !ok {
			continue
		}
		merged := map[string]string{}
		if existing, ok := asStringMap(svc["labels"]); ok {
			for k, v := range existing {
				if s, ok := v.(string); ok {
					merged[k] = s
				}
			}
		}
		for k, v := range buildAggregaLabels(origin) {
			merged[k] = v
		}
		svc["labels"] = merged
		services[name] = svc
	}
	doc["services"] = services
}

// buildSingleOriginMap returns a per-service origins map for the simple
// case where every service in a merged compose document shares one
// nodeCtx — the single-origin scenario for sourced-mode rendering.
// Future multi-origin call sites build the map directly per service.
func buildSingleOriginMap(doc map[string]any, nodeCtx rendercore.NodeRenderContext) map[string]rendercore.NodeRenderContext {
	origins := map[string]rendercore.NodeRenderContext{}
	if doc == nil {
		return origins
	}
	services, ok := asStringMap(doc["services"])
	if !ok {
		return origins
	}
	for name := range services {
		origins[name] = nodeCtx
	}
	return origins
}

// buildAggregaLabels produces the aggrega.ai/* service-level labels for
// compose services. It derives its set from the shared provenance builders
// rather than re-listing keys, so adding a provenance key in one place flows
// to every engine: compose carries the entire provenance set — the
// label-safe entries unioned with the charset-unsafe ones — as a single
// service-labels map, because compose's label values are unconstrained and
// it has no annotation concept. The two Kubernetes-only keys
// (composeOmittedProvenanceKeys) are dropped; the remaining set matches the
// helm and helm-chart engines' provenance for the same node.
func buildAggregaLabels(nodeCtx rendercore.NodeRenderContext) map[string]string {
	return foldComposeProvenanceSet(
		rendercore.BuildAggregaProvenanceLabels(nodeCtx),
		rendercore.BuildAggregaProvenanceAnnotations(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx)),
	)
}

// foldComposeProvenanceSet folds a Kubernetes-shaped provenance split — the
// label-safe half and the charset-unsafe half — into the single service-labels
// map compose emits. Every compose surface reaches the fold through this one
// function, so the services this package synthesizes and merges and the services
// an artifact fragment contributes carry the same key set from the same rule
// rather than from two agreeing copies of it.
//
// The fold itself is the shared one ([rendercore.FoldProvenanceSet]), which every
// target whose metadata surface constrains neither charset nor annotation concept
// reads: the union of the two halves, minus the keys only a Kubernetes runtime
// consumes. Compose adds nothing to it, so this is a name rather than a second
// implementation.
func foldComposeProvenanceSet(labels, annotations map[string]string) map[string]string {
	return rendercore.FoldProvenanceSet(labels, annotations)
}
