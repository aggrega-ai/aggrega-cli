// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "strconv"

// buildProvenanceLabels produces the canonical aggrega.ai/* + standard
// recommended-label set merged into the metadata.labels of every emitted
// artifact whose document carries a metadata block. The label set is the
// chapter-16 contract for artifacts emission: component-id, recipe-uri,
// boundary-path, plus the conditional aggregate / environment / compiler-
// version / sync-wave entries the helm engine already emits, plus the
// app.kubernetes.io/name recommended label.
//
// The artifacts evaluator is the only current consumer; the helm and
// helmchart engines retain their own buildAggreraLabels helper until a
// follow-up unifies the two helpers and regenerates affected goldens.
func buildProvenanceLabels(state *PhaseBState, nodeCtx NodeRenderContext) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":  nodeCtx.NodeID,
		"aggrega.ai/component-id": nodeCtx.NodeID,
	}
	if uri := recipeURI(state); uri != "" {
		labels["aggrega.ai/recipe-uri"] = uri
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

// recipeURI assembles the canonical recipe-uri label value from the per-
// node Recipe identity carried on PhaseBState. Returns "" when either name
// or version is missing — partial identity is not labelled.
func recipeURI(state *PhaseBState) string {
	if state == nil || state.RecipeName == "" || state.RecipeVersion == "" {
		return ""
	}
	return state.RecipeName + ":" + state.RecipeVersion
}
