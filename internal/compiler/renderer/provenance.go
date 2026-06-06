// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "strconv"

// buildProvenanceLabels produces the label-safe aggrega.ai/* + standard
// recommended-label set merged into the metadata.labels of every emitted
// artifact whose document carries a metadata block. Only values that
// satisfy the Kubernetes label-value charset live here: component-id, the
// conditional aggregate / environment / compiler-version entries, and the
// app.kubernetes.io/name recommended label. The charset-unsafe provenance
// (recipe-uri, boundary-path, sync-wave) lives in metadata.annotations via
// buildProvenanceAnnotations — the same partition the helm and helmchart
// engines apply (see helm/labels.go buildAggregaLabels/buildAggregaAnnotations).
//
// The artifacts evaluator is the only current consumer; the helm and
// helmchart engines retain their own buildAggregaLabels helper until a
// follow-up unifies the two helpers (the label/annotation partition is now
// identical across both).
func buildProvenanceLabels(state *PhaseBState, nodeCtx NodeRenderContext) map[string]string {
	_ = state
	labels := map[string]string{
		"app.kubernetes.io/name":  nodeCtx.NodeID,
		"aggrega.ai/component-id": nodeCtx.NodeID,
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

// buildProvenanceAnnotations produces the aggrega-managed annotation set
// merged into the metadata.annotations of every emitted artifact whose
// document carries a metadata block. It holds the charset-unsafe
// provenance — values a Kubernetes label cannot represent (recipe-uri's
// ":", boundary-path's "/", a negative sync-wave's leading "-") — so the
// emitted artifact carries valid labels and full-fidelity provenance in
// annotations. The partition mirrors the helm engine's
// buildAggregaAnnotations (helm/labels.go) so a node rendered through both
// the helm engine and the artifacts emitter gets an identical label/
// annotation split. recipe-uri is sourced from recipeURI(state); the
// argocd.argoproj.io/sync-wave annotation is emitted alongside the
// aggrega.ai/sync-wave when the wave is non-zero. Each entry elides on its
// default exactly as the helm engine does.
func buildProvenanceAnnotations(state *PhaseBState, nodeCtx NodeRenderContext) map[string]string {
	annotations := map[string]string{}
	if nodeCtx.BoundaryPath != "" {
		annotations["aggrega.ai/boundary-path"] = nodeCtx.BoundaryPath
	}
	if uri := recipeURI(state); uri != "" {
		annotations["aggrega.ai/recipe-uri"] = uri
	}
	if nodeCtx.SyncWave != 0 {
		annotations["aggrega.ai/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
		annotations["argocd.argoproj.io/sync-wave"] = strconv.Itoa(nodeCtx.SyncWave)
	}
	return annotations
}

// recipeURI assembles the canonical recipe-uri label value from the per-
// node Recipe identity carried on PhaseBState. Returns "" when either name
// or version is missing — partial identity is not labelled.
func recipeURI(state *PhaseBState) string {
	if state == nil || state.RecipeName == "" || state.RecipeVersion == "" {
		return ""
	}
	return state.RecipeName + ":" + state.RecipeVersion
}
