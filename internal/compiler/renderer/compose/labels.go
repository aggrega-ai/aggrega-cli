// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"strconv"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
)

// injectAggregaLabelsIntoServices walks the services: map of a YAML-decoded
// compose document and merges the canonical aggrega.ai/* label set into
// each service's labels: map. Used by sourced-mode rendering after the
// base + valuesMapping deep-merge so labels land on services regardless
// of whether the base file or the overlay declared them. Existing label
// keys on a service are preserved; aggrega.ai/* keys are overwritten with
// the values derived from nodeCtx.
//
// The function tolerates malformed compose documents silently — sourced-
// mode treats the input as authored YAML, and label injection is a
// metadata enhancement, not a validation gate.
func injectAggregaLabelsIntoServices(doc map[string]interface{}, nodeCtx renderer.NodeRenderContext) {
	if doc == nil {
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
	provLabels := buildAggregaLabels(nodeCtx)
	for name, svcRaw := range services {
		svc, ok := asStringMap(svcRaw)
		if !ok {
			continue
		}
		labels := map[string]string{}
		if existing, ok := asStringMap(svc["labels"]); ok {
			for k, v := range existing {
				if s, ok := v.(string); ok {
					labels[k] = s
				}
			}
		}
		for k, v := range provLabels {
			labels[k] = v
		}
		svc["labels"] = labels
		services[name] = svc
	}
	doc["services"] = services
}

// buildAggregaLabels produces the aggrega.ai/* service-level labels for
// compose services. Mirrors the helm and helmchart engines' same-named
// helper so the cross-cutting provenance contract holds across all three
// engines: every emitted service carries component-id, recipe-uri (when a
// Recipe identity is attached to the node), and boundary-path. Compiler-
// version, aggregate-name, environment, and sync-wave follow the same
// elide-on-empty rule as the K8s engines.
func buildAggregaLabels(nodeCtx renderer.NodeRenderContext) map[string]string {
	labels := map[string]string{
		"aggrega.ai/component-id": nodeCtx.NodeID,
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
