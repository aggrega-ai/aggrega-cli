// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"sort"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// buildAggregaLabels produces the label-safe aggrega.ai/* resource labels for
// K8s manifests, delegating to the shared provenance builder so the helm,
// helm-chart, and artifacts surfaces emit one identical partition with the same
// sanitised component-id. The compiler has no knowledge of OpenTelemetry;
// downstream tooling (Prometheus, cost allocation, OTel correlation) consumes them.
func buildAggregaLabels(nodeCtx renderer.NodeRenderContext) map[string]string {
	return renderer.BuildAggregaProvenanceLabels(nodeCtx)
}

// buildAggregaAnnotations produces the charset-unsafe aggrega.ai/* annotation
// set (recipe-uri, boundary-path, sync-wave + argocd.argoproj.io/sync-wave),
// delegating to the shared provenance builder. The recipe-uri is assembled from
// this node's Recipe identity.
func buildAggregaAnnotations(nodeCtx renderer.NodeRenderContext) map[string]string {
	return renderer.BuildAggregaProvenanceAnnotations(nodeCtx, renderer.ProvenanceRecipeURIFromNodeCtx(nodeCtx))
}

// helmHookWeight returns the stringified SyncWave value to inject as a
// helm.sh/hook-weight annotation on Helm hook resources. Returns "" when
// no hook-weight should be emitted (SyncWave == 0). Hook detection happens
// at injection time (per-manifest scan for an existing helm.sh/hook
// annotation set by the chart author).
func helmHookWeight(nodeCtx renderer.NodeRenderContext) string {
	if nodeCtx.SyncWave == 0 {
		return ""
	}
	return strconv.Itoa(nodeCtx.SyncWave)
}

// sortedLabelKeys returns map keys sorted for deterministic YAML output.
func sortedLabelKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
