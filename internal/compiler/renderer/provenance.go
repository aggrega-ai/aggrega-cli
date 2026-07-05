// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "strconv"

// ProvenanceComponentID returns the Kubernetes-safe component identity for a
// node — the sanitised LabelComponentID when set (FR-emitted resources whose
// internal NodeID carries marker syntax such as `<boundary-fr>:bridge@cluster`
// have it populated and sanitised), falling back to NodeID for ordinary
// component nodes whose NodeID is already label-valid. It is the single source
// of the app.kubernetes.io/name and aggrega.ai/component-id values across every
// emitting surface, so an FR-emitted artifact never stamps a charset-invalid
// component identity.
func ProvenanceComponentID(nodeCtx NodeRenderContext) string {
	if nodeCtx.LabelComponentID != "" {
		return nodeCtx.LabelComponentID
	}
	return nodeCtx.NodeID
}

// BuildAggregaProvenanceLabels produces the label-safe `aggrega.ai/*` + standard
// recommended-label set for every emitted manifest whose document carries a
// metadata block. Only values that satisfy the Kubernetes label-value charset
// live here: the sanitised component-id (both `app.kubernetes.io/name` and
// `aggrega.ai/component-id`), the resolved provider name / provider-version, and
// the conditional aggregate-name / environment /
// compiler-version entries. The charset-unsafe provenance (recipe-uri,
// boundary-path, sync-wave) lives in annotations via
// [BuildAggregaProvenanceAnnotations].
//
// This is the single implementation shared by the helm and helm-chart engines
// and the artifacts emitter — one charset-split + sanitised-component-id source,
// so the partition and the component-id value are identical across all three.
// (The compose engine keeps the whole set as service labels: its label values
// are unconstrained and it has no annotation concept.)
func BuildAggregaProvenanceLabels(nodeCtx NodeRenderContext) map[string]string {
	componentID := ProvenanceComponentID(nodeCtx)
	labels := map[string]string{
		"app.kubernetes.io/name":  componentID,
		"aggrega.ai/component-id": componentID,
	}
	if nodeCtx.ProviderName != "" {
		labels["aggrega.ai/provider"] = nodeCtx.ProviderName
	}
	if nodeCtx.ProviderVersion != "" {
		labels["aggrega.ai/provider-version"] = nodeCtx.ProviderVersion
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

// BuildAggregaProvenanceAnnotations produces the charset-unsafe `aggrega.ai/*`
// annotation set — values a Kubernetes label cannot represent (recipe-uri's
// ":", boundary-path's "/", a negative sync-wave's leading "-") — so an emitted
// manifest carries valid labels and full-fidelity provenance in annotations.
// recipeURI is passed in so the helper stays engine-agnostic: the helm and
// helm-chart engines assemble it from nodeCtx via [ProvenanceRecipeURIFromNodeCtx];
// the artifacts emitter assembles it from PhaseBState via [recipeURI]. An empty
// recipeURI elides (partial identity is not stamped). The
// argocd.argoproj.io/sync-wave annotation carries the same wave value when the
// wave is non-zero. Each entry elides on its default.
func BuildAggregaProvenanceAnnotations(nodeCtx NodeRenderContext, recipeURI string) map[string]string {
	annotations := map[string]string{}
	if nodeCtx.BoundaryPath != "" {
		annotations["aggrega.ai/boundary-path"] = nodeCtx.BoundaryPath
	}
	if recipeURI != "" {
		annotations["aggrega.ai/recipe-uri"] = recipeURI
	}
	if nodeCtx.SyncWave != 0 {
		annotations["aggrega.ai/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
		annotations["argocd.argoproj.io/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
	}
	return annotations
}

// ProvenanceRecipeURIFromNodeCtx assembles the recipe-uri annotation value from
// a node's Recipe identity (the helm / helm-chart source). Returns "" when
// either half is missing — partial identity is not stamped.
func ProvenanceRecipeURIFromNodeCtx(nodeCtx NodeRenderContext) string {
	if nodeCtx.RecipeName == "" || nodeCtx.RecipeVersion == "" {
		return ""
	}
	return nodeCtx.RecipeName + ":" + nodeCtx.RecipeVersion
}

// buildProvenanceLabels is the artifacts-emitter entry point to the shared
// label builder; state is accepted for call-site symmetry and is unused (the
// label-safe set derives entirely from nodeCtx).
func buildProvenanceLabels(state *PhaseBState, nodeCtx NodeRenderContext) map[string]string {
	_ = state
	return BuildAggregaProvenanceLabels(nodeCtx)
}

// buildProvenanceAnnotations is the artifacts-emitter entry point to the shared
// annotation builder; it sources recipe-uri from PhaseBState (the artifacts
// path's recipe identity).
func buildProvenanceAnnotations(state *PhaseBState, nodeCtx NodeRenderContext) map[string]string {
	return BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))
}

// recipeURI assembles the canonical recipe-uri value from the per-node Recipe
// identity carried on PhaseBState. Returns "" when either name or version is
// missing — partial identity is not labelled.
func recipeURI(state *PhaseBState) string {
	if state == nil || state.RecipeName == "" || state.RecipeVersion == "" {
		return ""
	}
	return state.RecipeName + ":" + state.RecipeVersion
}
