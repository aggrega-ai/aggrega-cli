// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// nodeInputKind names the one input kind a node's declared inputs classify to:
// the scope a provider's annotation support claim is declared at for that kind,
// and the user-facing word every diagnostic about that kind spells. The two
// travel together deliberately. A provider's support claim is declared per input
// kind, so a reader of the claim needs the scope; an author reading a diagnostic
// needs the word. Carrying them as one value is what stops the capability gate
// and the annotation advisory from describing one input kind in two
// vocabularies.
type nodeInputKind struct {
	// Scope is the scope name a provider's annotation support claim declares
	// this input kind at.
	Scope string
	// Display is the input kind's user-facing name, spelled as every diagnostic
	// naming an input kind spells it.
	Display string
}

// classifyNodeInputKind is the one home of the source > workload > artifacts
// precedence: a node declaring a source or a workload is sourced or synthesised
// even when it ALSO declares artifacts (those ride through as composed artifacts),
// and only a node with neither is artifacts-only. ok is false for a node
// declaring none of the three, which classifies to no input kind at all.
//
// It is also the one home of the pairing every reader needs — an input kind's
// support-claim scope and the word a diagnostic spells it with — which is why
// declaredNodeInputKinds builds each of its entries by asking this function
// rather than naming a scope and a word directly. A second set of literals would
// agree on the day it was written and would be free to drift afterwards, which is
// the whole reason this is a function rather than a switch at each reader.
func classifyNodeInputKind(hasSource, hasWorkload, hasArtifacts bool) (nodeInputKind, bool) {
	switch {
	case hasSource:
		return nodeInputKind{Scope: types.WellKnownAnnotationScopeSourced, Display: "sourced"}, true
	case hasWorkload:
		return nodeInputKind{Scope: types.WellKnownAnnotationScopeSynthesized, Display: "synthesised"}, true
	case hasArtifacts:
		return nodeInputKind{Scope: types.WellKnownAnnotationScopeArtifactsStandalone, Display: "artifacts-only"}, true
	}
	return nodeInputKind{}, false
}

// declaredNodeInputKinds names every input kind a node's declared inputs commit
// it to, in source → workload → artifacts order. It is the plural counterpart of
// classifyNodeInputKind, and the two answer different questions: that one resolves
// a node to the single kind its mode is NAMED after, while this one enumerates
// every kind the node actually declares, because a node's output is the sum of its
// declared kinds rather than the one the precedence picked.
//
// Both readers that ask a per-kind question of a node read this one: the
// capability gate, which needs a provider able to render every declared kind, and
// the annotation-support advisory, which needs to know whether ANY declared kind's
// annotation scope covers an authored key. The two questions are different and the
// set is the same, because what makes a kind a member here is that the node
// genuinely renders through it.
//
// artifacts contributes a kind only when it is the sole declaration. Alongside a
// source or a workload it rides through as composed artifacts, which the
// standalone-artifacts capability does not govern — the same bound the precedence
// encodes, stated here as the condition it is. That bound is a property of what
// the kind MEANS rather than a convenience for one reader, which is why it holds
// for the advisory too: a provider declares annotation support at the
// standalone-artifacts scope, whose own meaning is a node whose whole output is
// imported artifact documents, so such a declaration says nothing about a node
// that also renders a source or a workload.
//
// Each kind is obtained by asking classifyNodeInputKind what a node declaring
// that input ALONE classifies to, rather than by naming the kinds directly. That
// keeps the scope name and the user-facing word for an input kind in exactly one
// place: a second set of literals here would agree on the day it was written and
// would be free to drift from the classifier afterwards.
func declaredNodeInputKinds(hasSource, hasWorkload, hasArtifacts bool) []nodeInputKind {
	kinds := make([]nodeInputKind, 0, 3)
	for _, alone := range [3][3]bool{
		{hasSource, false, false},
		{false, hasWorkload, false},
		{false, false, hasArtifacts && !hasSource && !hasWorkload},
	} {
		if kind, ok := classifyNodeInputKind(alone[0], alone[1], alone[2]); ok {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// claimedBy reports whether a provider's render-behavior contract claims to
// render this input kind. An input kind the contract does not name reports true,
// so a kind this classification does not model never fabricates a refusal.
func (k nodeInputKind) claimedBy(caps ProviderContract) bool {
	switch k.Scope {
	case types.WellKnownAnnotationScopeSourced:
		return caps.Sourced
	case types.WellKnownAnnotationScopeSynthesized:
		return caps.Synthesized
	case types.WellKnownAnnotationScopeArtifactsStandalone:
		return caps.ArtifactsStandalone
	}
	return true
}

// warnUnhonorableAnnotations advises on every platform annotation authored for the
// scope a render's output lands in that the render's resolved target's own support
// claim covers for NONE of the input kinds that render declares — the annotations
// that reach no consumer and change nothing. It emits into the structured advisory
// sink and returns nothing: this is a degradation, never a refusal, because a
// boundary annotation legitimately cascades across components rendering through
// heterogeneous targets and refusing the build would reject a correct deployment.
//
// It is asked once per RENDER rather than once per node. A node's own Recipe and
// each feature attached to that node name their own capability and declare their
// own inputs, and each emits its own documents — so each is judged on the target
// and the declared kinds that produced its own output, and one authored annotation
// may be honoured in one of them while reaching nothing in another.
//
// The honour question is asked once per declared kind and an advisory is raised
// only when every answer is no. A render's declared kinds compose by addition and
// their outputs are added together, so a target with a slot for the key on one
// declared kind DOES act on it, whatever the other kinds are — and this advisory
// says the value was ignored, so asking about one kind in a render that emits two
// produces a statement contradicted by the same build's own emitted bytes, whose
// remedy is to delete an annotation that is in fact working. Raising only on a
// unanimous no is what keeps the sentence true.
//
// The predicate answering per scope is unchanged and is asked repeatedly rather
// than taught about sets: what a provider claims is declared per scope, so a claim
// covering a node is a claim covering one of the scopes that node renders at, and
// that is a question about one scope at a time.
//
// Only keys under the platform's own reserved prefix are considered. A
// sub-prefixed key is authored vocabulary the compiler passes through by design,
// so advising on one would be wrong and would bury the real advisories in noise.
// The prefix test is the whole membership test and there is deliberately no list
// of key names beside it: a second register of platform keys would be free to
// drift from the reservation, and it would suppress the case an author most needs
// told about, which is a mistyped key that reaches nothing and says nothing.
//
// Annotations are read in the order the deployment authored them, from the
// ordered source rather than from the render context's flattened copy, so the
// advisory stream is byte-stable across runs of identical input.
//
// It reads the support claim off the entity the given lookup resolves for the
// full provider binding — version pin included — so the claim consulted is the
// one carried by the plugin behind the engine actually rendering this node. It
// emits nothing when there is no lookup, no resolving entity, no sink, no
// annotations, or no declared input kind: each of those leaves nothing to compare
// and nothing honest to report.
func warnUnhonorableAnnotations(
	nodeID, providerRef, providerName string,
	kinds []nodeInputKind,
	annotations types.OrderedMap[string],
	providerPlugin func(ref string) (types.Plugin, bool, error),
	sink func(Diagnostic),
) {
	if sink == nil || providerPlugin == nil || annotations.Len() == 0 || len(kinds) == 0 {
		return
	}
	plugin, ok, err := providerPlugin(providerRef)
	if err != nil || !ok {
		return
	}
	// Assembled once for the whole annotation walk: every advisory this node
	// raises names the same set of declared kinds, because the set is a property
	// of the node rather than of the key being reported.
	displays := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		displays = append(displays, kind.Display)
	}
	for _, key := range annotations.Keys() {
		if !strings.HasPrefix(key, types.ReservedLabelPrefixAggregaAI) {
			continue
		}
		if honouredByAnyKind(plugin, key, kinds) {
			continue
		}
		d := UnhonorableAnnotationDiagnostic(providerName, key, displays...)
		d.Location = &DiagnosticLocation{URI: nodeID, Kind: providerName}
		sink(d)
	}
}

// honouredByAnyKind reports whether a provider's support claim covers key at the
// scope of at least one of the input kinds a node declares. Any is the right
// quantifier and not a leniency: the declared kinds' outputs are added together,
// so a claim covering one of them means the target does act on the key somewhere
// in this node's output, and reporting it as ignored would be false.
func honouredByAnyKind(plugin types.Plugin, key string, kinds []nodeInputKind) bool {
	for _, kind := range kinds {
		if types.WellKnownAnnotationHonoured(plugin, key, kind.Scope) {
			return true
		}
	}
	return false
}
