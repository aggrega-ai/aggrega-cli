// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// composeOmittedProvenanceKeys are the two provenance keys the shared
// builders emit that compose deliberately does not carry. Compose has no
// annotation concept and its service-label values are unconstrained, so it
// folds the whole provenance set — labels plus annotations — into a single
// labels map rather than splitting on the Kubernetes label-value charset.
// The two Kubernetes-only keys are dropped: `app.kubernetes.io/name` is a
// K8s recommended label with no compose meaning, and
// `argocd.argoproj.io/sync-wave` is an Argo CD ordering hint that compose
// does not honour. The plain `aggrega.ai/sync-wave` provenance entry is
// retained.
var composeOmittedProvenanceKeys = []string{
	"app.kubernetes.io/name",
	"argocd.argoproj.io/sync-wave",
}

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
//   - Future Release-level merger of cross-context fragments would
//     populate one entry per service pointing at the originating
//     fragment's nodeCtx — services emitted by an FR map to the FR's
//     nodeCtx; services emitted by a regular Recipe map to that
//     Recipe's nodeCtx; services authored deliberately at higher levels
//     map to the appropriate authoring context.
//
// Services NOT present in the origins map are left untouched. This is
// load-bearing for two future-proofing cases:
//
//  1. Catalog authors that deliberately set aggrega.ai/* labels on
//     specific services in a base compose file. Treating "absent from
//     origins" as "author-managed, do not stamp" preserves the
//     author's intent without the compiler having to use label-
//     presence as a heuristic for ownership.
//  2. Multi-FR composition where one FR's fragment may carry services
//     contributed by an upstream FR. The upstream FR's emission step
//     would have already populated origins for its services; the
//     downstream FR's injection step skips those, deferring to the
//     authoritative provenance set upstream.
//
// Existing label keys on a service are merged with — not replaced by —
// the origin-derived labels: author-set non-aggrega.ai/* keys
// (`tier: backend`, etc.) are preserved; origin-derived aggrega.ai/*
// keys overwrite any pre-existing aggrega.ai/* values for the same key.
//
// The function tolerates malformed compose documents silently — sourced-
// mode treats the input as authored YAML, and label injection is a
// metadata enhancement, not a validation gate.
func injectAggregaLabelsIntoServices(doc map[string]any, origins map[string]renderer.NodeRenderContext) {
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
			// Service not registered in origins map: author-managed;
			// preserve as-is.
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
func buildSingleOriginMap(doc map[string]any, nodeCtx renderer.NodeRenderContext) map[string]renderer.NodeRenderContext {
	origins := map[string]renderer.NodeRenderContext{}
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
func buildAggregaLabels(nodeCtx renderer.NodeRenderContext) map[string]string {
	labels := renderer.BuildAggregaProvenanceLabels(nodeCtx)
	for k, v := range renderer.BuildAggregaProvenanceAnnotations(nodeCtx, renderer.ProvenanceRecipeURIFromNodeCtx(nodeCtx)) {
		labels[k] = v
	}
	for _, k := range composeOmittedProvenanceKeys {
		delete(labels, k)
	}
	return labels
}
