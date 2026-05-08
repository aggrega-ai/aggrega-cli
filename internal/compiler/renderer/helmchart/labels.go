// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"sort"
	"strconv"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
)

// buildAggreraLabels produces the aggrega.ai/* resource labels for K8s manifests.
// These are static metadata tags — the compiler has no knowledge of OpenTelemetry;
// downstream tooling (Prometheus, cost allocation, OTel correlation) consumes them.
func buildAggreraLabels(nodeCtx renderer.NodeRenderContext) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":      nodeCtx.NodeID,
		"aggrega.ai/component-id":     nodeCtx.NodeID,
	}
	if nodeCtx.BoundaryPath != "" {
		labels["aggrega.ai/boundary-path"] = nodeCtx.BoundaryPath
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

// buildAggreraAnnotations produces the aggrega-managed annotation set for
// downstream K8s manifests rendered from this helm-chart sub-chart. API
// parity with the helm provider's same-named function: the helm-chart
// engine itself does not render manifests at compile time (manifest
// rendering happens at downstream `helm install`), so this function is
// available for chart-author-side templates that consume the annotations
// or for fixture-based tests asserting parity. Currently emits
// argocd.argoproj.io/sync-wave when nodeCtx.SyncWave != 0; default-0
// elide preserved.
func buildAggreraAnnotations(nodeCtx renderer.NodeRenderContext) map[string]string {
	annotations := map[string]string{}
	if nodeCtx.SyncWave != 0 {
		annotations["argocd.argoproj.io/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
	}
	return annotations
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
