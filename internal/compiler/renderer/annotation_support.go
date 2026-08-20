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
// Both the capability gate and the annotation-support advisory read it, so the
// precedence is stated once. A second copy would agree on the day it was written
// and would be free to drift afterwards, which is the whole reason this is a
// function rather than a switch at each reader.
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

// warnUnhonorableAnnotations advises on every platform annotation authored on a
// node that the resolved target's own support claim does not cover for that
// node's input kind — the annotations that reach no consumer and change nothing.
// It emits into the structured advisory sink and returns nothing: this is a
// degradation, never a refusal, because a boundary annotation legitimately
// cascades across components rendering through heterogeneous targets and
// refusing the build would reject a correct deployment.
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
// annotations, or no input kind: each of those leaves nothing to compare and
// nothing honest to report.
func warnUnhonorableAnnotations(
	nodeID, providerRef, providerName string,
	kind nodeInputKind,
	annotations types.OrderedMap[string],
	providerPlugin func(ref string) (types.Plugin, bool, error),
	sink func(Diagnostic),
) {
	if sink == nil || providerPlugin == nil || annotations.Len() == 0 || kind.Scope == "" {
		return
	}
	plugin, ok, err := providerPlugin(providerRef)
	if err != nil || !ok {
		return
	}
	for _, key := range annotations.Keys() {
		if !strings.HasPrefix(key, types.ReservedLabelPrefixAggregaAI) {
			continue
		}
		if types.WellKnownAnnotationHonoured(plugin, key, kind.Scope) {
			continue
		}
		d := UnhonorableAnnotationDiagnostic(providerName, key, kind.Display)
		d.Location = &DiagnosticLocation{URI: nodeID, Kind: providerName}
		sink(d)
	}
}
