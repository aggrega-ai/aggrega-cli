// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
)

// ProvenanceComponentID returns the Kubernetes-safe component identity for a
// node — what a resource this node emits BELONGS to. That is the value
// provenance records and the value a selector queries, and it is the single
// source of the app.kubernetes.io/name and aggrega.ai/component-id values across
// every emitting surface, so an FR-emitted artifact never stamps a
// charset-invalid component identity.
//
// It is the LabelComponentID when set — every resource an FR attachment emits
// has it populated, whatever input kind delivered them, so an attachment's
// resources are distinguishable from its host component's by label alone —
// falling back to the node's own identity, written through
// [labels.EncodeNodeIdentity], for ordinary component nodes.
//
// The fallback ENCODES rather than passes through, and that is the whole of why
// it is a call rather than a field read. A node one aggregate composes into
// another carries a qualified identity, joining the composing aggregate's name
// for the inner one to the inner one's name for the node; the character that
// joins them is reserved out of every authored name, so it is the one character
// an identity can carry that a target is liable to refuse in a name. A resource
// this value names would carry it verbatim, and a consumer handed the same string
// as an address would dial something else, because that character separates a
// service from its namespace. Encoding writes it out and carries every ordinary
// authored name through unchanged.
//
// A NodeID reaching here is already known to be usable: a build whose
// composition holds an identity that could not be stamped — one that is not a
// valid label value once encoded, or one opening as a composed identity does —
// halts before any of this is reached. The encoding is what makes a qualified
// identity part of that space rather than an exception to it.
//
// LabelComponentID is NOT encoded again, and must not be: it is already composed
// through an encoding of its own, over its own alphabet, and encoding it a second
// time would write out the join it was composed with and spell one identity two
// ways.
//
// It is NOT the value a target derives its own names from. That is
// [TargetNameBasis], and reading this one in its place is the mistake the two
// exist to prevent: it would rename the resources an attachment author's own
// chart declared.
func ProvenanceComponentID(nodeCtx NodeRenderContext) string {
	if nodeCtx.LabelComponentID != "" {
		return nodeCtx.LabelComponentID
	}
	return labels.EncodeNodeIdentity(nodeCtx.NodeID)
}

// TargetNameBasis returns the identity a target derives its OWN names from — a
// Helm release name, which a chart an author wrote renders against and which
// names the workload that target synthesizes for a component, and the prefix on
// the files a source render emits. It is the DerivedNameBasis when set, falling
// back to NodeID.
//
// It sits beside [ProvenanceComponentID] rather than inside it because the two
// answer different questions, and a single value answering both is what made an
// FR attachment's sourced resources indistinguishable from its host's: the
// identity had to stay the host's in order to keep the release name the host's.
// Separating them lets the identity say which attachment emitted a resource
// while the name an author's own templates render against stays put.
//
// The fallback is what keeps the split cheap. An ordinary component's NodeID is
// its deployed name and reaches here unmodified; a per-component FR attachment
// leaves DerivedNameBasis empty, so its author's chart renders against the host
// component's name. Only a node whose NodeID a target's name alphabet refuses
// carries the basis explicitly, and a boundary-scope attachment is that case: its
// NodeID is a dispatch marker whose first character the release-name alphabet
// does not admit, so every escape would open the name with a hyphen and no
// shortening would repair it.
//
// Deliberately NOT read by a surface that names a workload an FR attachment
// synthesizes, or a standalone artifact set. Those have no author templates, so
// nothing renders against their names and they are named after the identity —
// which is what keeps an FR's synthesized workload from colliding with its host's
// own. Reading this there would fall back to the host's NodeID and reintroduce
// exactly that collision. A component's own synthesized workload may be named
// from it, as the Helm release name names it: for a component the basis is its
// own identity, so no other node's name is borrowed.
//
// It also does NOT encode, where [ProvenanceComponentID] does, and the asymmetry
// is deliberate rather than an oversight — encoding here would be a defect, not a
// tidying. Each of the two things read from this value already answers the
// question for itself: a target deriving a name from it narrows it through that
// target's OWN alphabet, which escapes whatever that target refuses, so encoding
// first would put one escape inside another and spell the name differently from
// the identity a recipe author predicts it by; and the prefix on the files a
// render emits is a filename, a position the identity join is legal in. So the
// value is passed as authored, once, and every reader that needs a narrower
// alphabet applies its own.
func TargetNameBasis(nodeCtx NodeRenderContext) string {
	return ContributorNameBasis(nodeCtx.NodeID, nodeCtx.DerivedNameBasis)
}

// ContributorNameBasis resolves the basis-over-id fallback from the two carried
// strings, for a reader holding them apart from any node context.
//
// It is the one implementation of that fallback, and [TargetNameBasis] is a
// reading of it against a render context. The assemble side needs the same answer
// and holds no render context: a contributor reaching an assembler arrives as the
// two carried values, and the identity the assembler derives its names from has to
// be the identity the render derived its declarations from. Two spellings of the
// fallback would agree while both were being written and would stop agreeing at
// whichever of them was changed — and the two answers meeting is the whole of what
// makes a declared name and an assembled name comparable, so a divergence there is
// silent on both sides.
//
// Which of the two carried identities a caller passes is the caller's own answer
// and is not decided here: a surface naming a resource an author's own source
// declared and a surface naming one the assembler minted read different carries,
// for the reasons stated on [TargetNameBasis] and [ProvenanceComponentID].
func ContributorNameBasis(nodeID, derivedNameBasis string) string {
	if derivedNameBasis != "" {
		return derivedNameBasis
	}
	return nodeID
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

// KubernetesOnlyProvenanceKeys are the keys the shared builders emit that only a
// Kubernetes runtime consumes: `app.kubernetes.io/name` is the Kubernetes
// recommended label, and `argocd.argoproj.io/sync-wave` is a GitOps controller's
// ordering hint. Neither names anything on a target that does not deploy through
// Kubernetes, so a target of that kind drops both. The plain
// `aggrega.ai/sync-wave` provenance entry is not among them and is retained: it
// records the wave the node declared, which is provenance rather than an
// instruction to a controller.
var KubernetesOnlyProvenanceKeys = []string{
	"app.kubernetes.io/name",
	"argocd.argoproj.io/sync-wave",
}

// FoldProvenanceSet folds the Kubernetes-shaped provenance split — the label-safe
// half from [BuildAggregaProvenanceLabels] and the charset-unsafe half from
// [BuildAggregaProvenanceAnnotations] — into the single key/value set a target
// with no charset constraint and no annotation concept carries, dropping
// [KubernetesOnlyProvenanceKeys].
//
// The split exists because a Kubernetes label value is charset-constrained while
// an annotation value is not. A target whose own metadata surface constrains
// neither has nothing to split on, so it carries the union — and every such
// target carries the same union, which is why the fold has one home rather than
// one copy per provider. Adding a key to either shared builder therefore reaches
// all of them at once.
//
// The result is a fresh map because a caller does not necessarily own its inputs:
// an artifacts placer is handed the key sets the host computed for one projection
// entry, and folding in place would leave one entry's stamp on the next entry of
// the same node.
func FoldProvenanceSet(labels, annotations map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+len(annotations))
	for k, v := range labels {
		out[k] = v
	}
	for k, v := range annotations {
		out[k] = v
	}
	for _, k := range KubernetesOnlyProvenanceKeys {
		delete(out, k)
	}
	return out
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
