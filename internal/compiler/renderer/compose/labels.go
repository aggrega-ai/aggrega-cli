// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

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
// compose services. Mirrors the helm and helmchart engines' same-named
// helper so the cross-cutting provenance contract holds across all three
// engines: every emitted service carries component-id, recipe-uri (when a
// Recipe identity is attached to the node), and boundary-path. Compiler-
// version, aggregate-name, environment, and sync-wave follow the same
// elide-on-empty rule as the K8s engines.
func buildAggregaLabels(nodeCtx renderer.NodeRenderContext) map[string]string {
	componentID := nodeCtx.LabelComponentID
	if componentID == "" {
		componentID = nodeCtx.NodeID
	}
	labels := map[string]string{
		"aggrega.ai/component-id": componentID,
	}
	if nodeCtx.BoundaryPath != "" {
		labels["aggrega.ai/boundary-path"] = nodeCtx.BoundaryPath
	}
	if nodeCtx.RecipeName != "" && nodeCtx.RecipeVersion != "" {
		labels["aggrega.ai/recipe-uri"] = nodeCtx.RecipeName + ":" + nodeCtx.RecipeVersion
	}
	if nodeCtx.Provenance.CompilerVersion != "" {
		labels["aggrega.ai/compiler-version"] = nodeCtx.Provenance.CompilerVersion
	}
	if nodeCtx.Provenance.AggregateName != "" {
		labels["aggrega.ai/aggregate-name"] = nodeCtx.Provenance.AggregateName
	}
	if nodeCtx.Provenance.Environment != "" {
		labels["aggrega.ai/environment"] = nodeCtx.Provenance.Environment
	}
	if nodeCtx.SyncWave != 0 {
		labels["aggrega.ai/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
	}
	return labels
}
