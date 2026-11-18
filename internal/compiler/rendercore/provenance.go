// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
)

// ProvenanceMetadataKey is the key a template resource's own metadata carries the
// build provenance under, for a target whose resources hold such a member: a
// CloudFormation resource's Metadata attribute and an ARM template resource's
// metadata object.
//
// It is one key rather than the provenance entries written flat, because that
// member is a shared space: a template language may reserve names of its own
// there (CloudFormation reserves the AWS:: family) and other tools write their
// own, so a block under one reserved-prefix key collides with none of them and a
// merge into an author's own metadata touches exactly one name. The
// `aggrega.ai/` prefix is the reserved platform-injection keyspace, which is what
// makes the key unavailable to an author and therefore safe to claim.
const ProvenanceMetadataKey = "aggrega.ai/provenance"

// ProvenanceComponentIDKey is the provenance key the component identity
// ([ProvenanceComponentID]) is recorded under, in the labels
// [BuildAggregaProvenanceLabels] builds and in every set folded from them.
const ProvenanceComponentIDKey = "aggrega.ai/component-id"

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
// ways. That alphabet is the lowercase letters and the digits, with every other
// character — a capital included — written out as an escape, because a surface
// naming the workload a Feature attachment synthesizes spends this value as a
// resource name and a resource name admits no capital. So the value is returned
// as carried, and it is already both a label value and a name a cluster accepts.
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
// answer different questions: the identity says which FeatureRecipe (FR)
// attachment emitted a resource, so an attachment's sourced resources are told
// apart from its host's, while the name an author's own templates render against
// stays the host's. One value cannot do both, since keeping the release name the
// host's would keep the identity the host's too.
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

// fittedProvenanceEntry is one compiler-owned provenance entry whose source value
// is not a label value by construction: its key and the value as the build holds
// it.
type fittedProvenanceEntry struct {
	key   string
	value string
}

// fittedProvenanceEntries returns the compiler-owned provenance entries whose
// value is written into a label only in the form a label admits. Each is a
// string the build carries from an authored or reported source that does not
// hold it to the label rules: a Release's environment is any string, an
// Aggregate's name may hold `/`, a version may hold a `+` or, for a development
// build, parentheses.
//
// The returned slice IS the list of those entries, and no comment anywhere
// enumerates them: a prose copy beside the structure would go stale silently the
// day an entry is added. Both builders read this one list, so the entries whose
// label value is fitted and the entries whose verbatim value moves to an
// annotation cannot come to differ.
func fittedProvenanceEntries(nodeCtx NodeRenderContext) []fittedProvenanceEntry {
	return []fittedProvenanceEntry{
		{key: "aggrega.ai/provider", value: nodeCtx.ProviderName},
		{key: "aggrega.ai/provider-version", value: nodeCtx.ProviderVersion},
		{key: "aggrega.ai/compiler-version", value: nodeCtx.Provenance.CompilerVersion},
		{key: "aggrega.ai/aggregate-name", value: nodeCtx.Provenance.AggregateName},
		{key: "aggrega.ai/environment", value: nodeCtx.Provenance.Environment},
	}
}

// fitProvenanceLabelValue returns v unchanged when it already satisfies the
// Kubernetes label-value rules, and [labels.SanitizeForK8sLabelValue] of v
// otherwise.
//
// The rules come from two homes, each the one that already holds them. The
// length, [labelValueMaxLength], is a Kubernetes target's limit and stays here
// beside the writer of every label value. The character and edge rules are read
// through [labels.SatisfiesLabelValueCharacterRules], from the package that holds
// the repair, so the test and the repair cannot disagree about what is valid.
// The repair branch cuts at the sanitiser's own limit, the same number held
// privately in that package; this function's unit test fails if the two part.
//
// The unchanged branch is the point of the function rather than a shortcut: the
// sanitiser collapses runs of hyphens, which a valid label value may carry, so
// sanitising unconditionally would rename a value that needed no repair. A caller
// tells whether v was rewritten by comparing the result with v. The result is ""
// when v is empty, and when v holds no character the sanitiser keeps.
func fitProvenanceLabelValue(v string) string {
	if len(v) <= labelValueMaxLength && labels.SatisfiesLabelValueCharacterRules(v) {
		return v
	}
	return labels.SanitizeForK8sLabelValue(v)
}

// BuildAggregaProvenanceLabels produces the label-safe `aggrega.ai/*` + standard
// recommended-label set for every emitted manifest whose document carries a
// metadata block. Every value here satisfies the Kubernetes label-value rules:
// the component-id (both `app.kubernetes.io/name` and `aggrega.ai/component-id`),
// which is a label value by construction, and each compiler-owned entry whose
// source value is not a label value by construction, read from
// [fittedProvenanceEntries].
//
// Each of those entries carries its value as written when the value already
// satisfies the label rules, and otherwise the value [fitProvenanceLabelValue]
// rewrites it into — so an Aggregate named `imported/flow` is labelled
// `imported-flow`. A rewritten entry's verbatim value is carried under the same
// key by [BuildAggregaProvenanceAnnotations], so the build records the exact name
// whenever the label cannot. An entry whose value holds no character a label
// keeps is not written as a label at all, and its verbatim value is carried in
// the annotation the same way. The provenance a label never carries (recipe-uri,
// boundary-path, sync-wave) lives in annotations via
// [BuildAggregaProvenanceAnnotations].
//
// This is the single implementation shared by the helm and helm-chart engines
// and the artifacts emitter — one charset-split + component-id source, so the
// partition and every label value are identical across all three. A target
// with no label-value rules and no annotation concept folds the two halves
// through [FoldProvenanceSet], where the annotation's verbatim value replaces a
// rewritten label value under the shared key.
//
// The returned set is the compiler-owned provenance keys plus the recipe's
// author-owned output labels (nodeCtx.OutputLabels) — both the keys written
// literally on the recipe and the ones it promotes from the node's component
// labels. Output labels are carried as authored, values included. All of them
// are disjoint from the provenance keys by construction: load-time validation
// applies one reserved-namespace test to both blocks and rejects any key under
// the aggrega.ai/ or app.kubernetes.io/ prefixes, so the merge overwrites
// nothing. Every emitting surface sorts keys, so merge order is not observable.
func BuildAggregaProvenanceLabels(nodeCtx NodeRenderContext) map[string]string {
	componentID := ProvenanceComponentID(nodeCtx)
	out := map[string]string{
		"app.kubernetes.io/name": componentID,
		ProvenanceComponentIDKey: componentID,
	}
	for _, e := range fittedProvenanceEntries(nodeCtx) {
		if e.value == "" {
			continue
		}
		if fitted := fitProvenanceLabelValue(e.value); fitted != "" {
			out[e.key] = fitted
		}
	}
	for k, v := range nodeCtx.OutputLabels {
		out[k] = v
	}
	return out
}

// BuildAggregaProvenanceAnnotations produces the `aggrega.ai/*` annotation set —
// provenance a Kubernetes label cannot represent — so an emitted manifest
// carries valid labels and full-fidelity provenance in annotations. It holds the
// values no label carries (recipe-uri's ":", boundary-path's "/", a negative
// sync-wave's leading "-"), and, under the same key as its label, the verbatim
// value of each compiler-owned entry whose source value is not a label value by
// construction ([fittedProvenanceEntries]) and whose label value
// [BuildAggregaProvenanceLabels] had to rewrite or omit. An entry whose value is
// already a label value is carried by the label alone, so a build whose values
// all satisfy the label rules gains no annotation from them.
//
// recipeURI is passed in so the helper stays engine-agnostic: the helm and
// helm-chart engines assemble it from nodeCtx via [ProvenanceRecipeURIFromNodeCtx];
// the artifacts emitter assembles it from PhaseBState via [recipeURI]. An empty
// recipeURI elides (partial identity is not stamped). The
// argocd.argoproj.io/sync-wave annotation carries the same wave value when the
// wave is non-zero. Each entry elides on its default.
func BuildAggregaProvenanceAnnotations(nodeCtx NodeRenderContext, recipeURI string) map[string]string {
	annotations := map[string]string{}
	for _, e := range fittedProvenanceEntries(nodeCtx) {
		if e.value != "" && fitProvenanceLabelValue(e.value) != e.value {
			annotations[e.key] = e.value
		}
	}
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
// The annotations are applied second, and the order is the contract rather than
// an accident: where a label's value had to be rewritten to satisfy the label
// rules, the annotation half carries the verbatim value under the same key, so
// the folded set records the value as the build holds it.
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
// key literals of its own. Only `aggrega.ai/*` keys are kept: the
// Kubernetes-runtime-specific keys those builders also produce carry no meaning
// in a Terraform config and the fold drops them, and the prefix leaves out the
// recipe's author-owned output labels. The retained set is the
// cross-provider identity: provider, provider-version, component-id,
// compiler-version, aggregate-name, environment, boundary-path, recipe-uri, and
// sync-wave, each present only when its source value is set. The two halves are
// combined by [FoldProvenanceSet], so the value recorded under each key is the
// one that fold's contract names.
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
	fields := FoldProvenanceSet(
		BuildAggregaProvenanceLabels(nodeCtx),
		BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI),
	)

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
