// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"sort"
	"strconv"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// buildAggregaLabels produces the aggrega.ai/* resource labels for K8s manifests.
// These are static metadata tags — the compiler has no knowledge of OpenTelemetry;
// downstream tooling (Prometheus, cost allocation, OTel correlation) consumes them.
//
// Only label-safe values live here: Kubernetes label values must match
// (([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])? and be ≤63 chars, so any
// provenance whose value can carry ':' / '/' (recipe URIs, boundary paths) or a
// leading '-' (negative sync waves) is emitted as an annotation by
// buildAggregaAnnotations instead, where values are unconstrained.
func buildAggregaLabels(nodeCtx renderer.NodeRenderContext) map[string]string {
	componentID := nodeCtx.LabelComponentID
	if componentID == "" {
		componentID = nodeCtx.NodeID
	}
	labels := map[string]string{
		"app.kubernetes.io/name":  componentID,
		"aggrega.ai/component-id": componentID,
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
	return labels
}

// buildAggregaAnnotations produces the aggrega.ai/* annotation set for K8s
// manifests. Distinct from buildAggregaLabels: annotations carry data that
// downstream tooling reads but doesn't filter on (ArgoCD wave order, Helm hook
// ordering), plus provenance whose value cannot satisfy the K8s label-value
// charset — the recipe URI (':' and path '/'), the boundary path ('/'), and the
// sync wave (a negative value's leading '-' is an invalid label). The
// argocd.argoproj.io/sync-wave annotation carries the same wave value for ArgoCD.
func buildAggregaAnnotations(nodeCtx renderer.NodeRenderContext) map[string]string {
	annotations := map[string]string{}
	if nodeCtx.BoundaryPath != "" {
		annotations["aggrega.ai/boundary-path"] = nodeCtx.BoundaryPath
	}
	if nodeCtx.RecipeName != "" && nodeCtx.RecipeVersion != "" {
		annotations["aggrega.ai/recipe-uri"] = nodeCtx.RecipeName + ":" + nodeCtx.RecipeVersion
	}
	if nodeCtx.SyncWave != 0 {
		annotations["aggrega.ai/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
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
