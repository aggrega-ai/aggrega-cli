// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

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
