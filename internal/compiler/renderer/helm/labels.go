// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

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

// buildAggreraAnnotations produces the aggrega.ai/* annotation set for K8s
// manifests. Distinct from buildAggreraLabels: annotations carry data that
// downstream tooling reads but doesn't filter on (ArgoCD wave order, Helm
// hook ordering, etc.). Currently emits argocd.argoproj.io/sync-wave when
// nodeCtx.SyncWave != 0 (default-0 elide); future expansions add more
// annotations as provider-native sequencing primitives surface.
func buildAggreraAnnotations(nodeCtx renderer.NodeRenderContext) map[string]string {
	annotations := map[string]string{}
	if nodeCtx.SyncWave != 0 {
		annotations["argocd.argoproj.io/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
	}
	return annotations
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
