// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"sort"
	"strconv"
	"strings"
)

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
//
// The returned set is the compiler-owned provenance keys plus the recipe's
// author-owned output labels (nodeCtx.OutputLabels) — both the keys written
// literally on the recipe and the ones it promotes from the node's component
// labels. All of them are disjoint from the provenance keys by construction:
// load-time validation applies one reserved-namespace test to both blocks and
// rejects any key under the aggrega.ai/ or app.kubernetes.io/ prefixes, so the
// merge overwrites nothing. Every emitting surface sorts keys, so merge order is
// not observable.
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
	for k, v := range nodeCtx.OutputLabels {
		labels[k] = v
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

// FormatTerraformProvenanceComment renders a node's build provenance as a block
// of HCL (`#`) comment lines suitable for prepending to a Terraform config file
// or emitting as a standalone provenance file. It is the single provenance
// formatter for the terraform provider: the host artifacts path and the guest
// sourced path both call it, so the stamped identity can never diverge between
// the two emission surfaces.
//
// Every key and value is drawn from the shared [BuildAggregaProvenanceLabels]
// and [BuildAggregaProvenanceAnnotations] builders — the same source the
// Kubernetes label and annotation surfaces use — so the formatter re-lists no
// key literals of its own. Only `aggrega.ai/*` keys are kept; the
// Kubernetes-runtime-specific keys those builders also produce
// (app.kubernetes.io/name, argocd.argoproj.io/sync-wave) carry no meaning in a
// Terraform config and are filtered out by the prefix. The retained set is the
// cross-provider identity: provider, provider-version, component-id,
// compiler-version, aggregate-name, environment, boundary-path, recipe-uri, and
// sync-wave, each present only when its source value is set.
//
// recipeURI is passed through to the annotation builder exactly as the
// annotation surface receives it — the host artifacts path sources it from its per-node
// Recipe identity, the guest sourced path from the node's Recipe identity via
// [ProvenanceRecipeURIFromNodeCtx].
//
// Line order is a stable alphabetical sort of the retained keys. Provenance
// keys carry no author-order semantics (they mirror the sorted-key YAML label
// surface), so the sort is the canonical deterministic order rather than a
// stand-in for lost ordering: given identical input the block is byte-identical
// across runs, with no wall-clock or external state read (Deterministic Output).
func FormatTerraformProvenanceComment(nodeCtx NodeRenderContext, recipeURI string) []byte {
	fields := map[string]string{}
	for k, v := range BuildAggregaProvenanceLabels(nodeCtx) {
		fields[k] = v
	}
	for k, v := range BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI) {
		fields[k] = v
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		if strings.HasPrefix(k, "aggrega.ai/") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var b bytes.Buffer
	b.WriteString("# Aggrega build provenance\n")
	for _, k := range keys {
		b.WriteString("# ")
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(fields[k])
		b.WriteByte('\n')
	}
	return b.Bytes()
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
