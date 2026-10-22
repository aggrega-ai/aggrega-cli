// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// releaseNameEncoding is the segment encoding a Helm release name is built with.
// It is also the encoding [secretVolumeName] writes a slot with, because a pod
// volume name is held to lowercase letters, digits and hyphens, and an encoded
// segment never leaves that set and never ends with a hyphen — so the one
// encoding serves both in-cluster names, and neither carries an alphabet of its
// own.
//
// A release name is an in-cluster identity. It reaches every template as
// .Release.Name and charts routinely name resources after it, so two component
// identities arriving at one release name give two components one set of
// resource names in the cluster. The derivation therefore escapes the
// characters the alphabet does not admit rather than substituting for them:
// distinct identities reach distinct names, and no substitution can make two of
// them meet.
//
// The alphabet is stated here rather than taken from the encoding, because it
// is a property of what Helm accepts in this position rather than of the
// encoding. Helm accepts a release name built from lowercase letters, digits,
// hyphens and dots that begins and ends with a lowercase letter or a digit, up
// to 53 characters. The hyphen is the encoding's own — it opens every escape
// and composes the join — so an alphabet may not admit it. The dot is left out
// even though Helm accepts one: Helm requires a lowercase letter or a digit on
// each side of a dot, so a dot carried through next to a hyphen the encoding
// emitted would leave the grammar the name has to satisfy.
//
// The two conditions on position and length are properties of the identity
// rather than of the encoding, so the encoding does not hold to them and
// [ReleaseNameVerdict] judges both: an identity whose first character is escaped
// begins its name with the hyphen every escape opens with, and one carrying
// escaped characters encodes longer than it was authored.
var releaseNameEncoding = namesegment.NewEncoding('-', isReleaseNameRune)

// isReleaseNameRune reports whether a character is carried through the encoding
// as itself: a lowercase ASCII letter or a digit. Every other character,
// uppercase letters included, is escaped.
func isReleaseNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// releaseName derives a node's Helm release name from the same basis its
// rendered output files are prefixed with, [rendercore.TargetNameBasis].
//
// One source feeds both, so the two are the same name written twice — once as
// authored in the file prefix, once through an injective encoding here — and two
// nodes reach one release name exactly when they reach one prefix. Deriving it
// from a second value would put a lossy name beside a lossless one: the lossless
// one would keep two nodes' files apart while the lossy one quietly gave both a
// single in-cluster identity, and nothing about the files would show it, because
// a release name lives inside the rendered bytes rather than in the name of the
// file carrying them.
//
// The basis is deliberately NOT the component identity the rendered resources
// are LABELLED with. A release name reaches this chart's own templates as
// .Release.Name and charts routinely name their resources after it, so the name
// belongs to whoever wrote the chart; the label says which component the
// resources belong to, which is the compiler's answer and a different question.
// The two coincide for an ordinary component, where the node's own name is both.
// They part for a Feature attachment delivering a chart its author supplied: the
// label becomes the attachment's own composed identity so its resources are
// distinguishable from its host's, while the release name stays the host's,
// because moving it would rename resources the chart's author declared and that
// are already deployed under those names.
func releaseName(nodeCtx rendercore.NodeRenderContext) string {
	return ReleaseNameFor(nodeCtx.NodeID, nodeCtx.DerivedNameBasis)
}

// ReleaseNameFor derives the release name from the two carried identities alone,
// for a caller holding them apart from any render context.
//
// It is the one derivation, and [releaseName] is a reading of it against a render
// context. A caller that answers what this target will name a node's resources —
// before that node is rendered, so that the answer can reach the templates the
// node's own Recipe publishes from — holds the two strings and no context, and a
// second derivation over the same alphabet would agree on the day it was written
// and drift afterwards. The drift would be silent in the worst way: the name a
// chart's templates render against lives inside the rendered bytes, so an author
// publishing an address from a name derived the other way would hand a consumer
// a name for a resource that does not exist, at exit zero.
//
// The basis-over-id fallback is resolved through the shared reading rather than
// spelled here, so this side of the answer cannot part from the render side's.
func ReleaseNameFor(nodeID, derivedNameBasis string) string {
	return releaseNameEncoding.Segment(rendercore.ContributorNameBasis(nodeID, derivedNameBasis))
}

// ReleaseNameKind is what a verdict about a Helm release name calls itself. It
// reaches a reader verbatim in a refusal, so it is written in the target's
// vocabulary — the name a chart's own templates read as .Release.Name — rather
// than in the compiler's.
const ReleaseNameKind = "release name"

// releaseNameMaxLength is the longest string Helm accepts as a release name.
//
// It lives here, beside the derivation, because it is a property of the target
// this package renders for. The compiler holds no such number for any target: it
// asks, and this is the answer for this one.
const releaseNameMaxLength = 53

// ReleaseNameVerdict judges a proposed component identity as the release name
// this provider would derive from it.
//
// Two things can be wrong, and they are reported differently because the
// compiler can act on only one of them. A name that is too long comes back with
// a BOUND — the greatest number of bytes of this identity whose encoding still
// fits — because a shorter identity would be accepted. A name whose FIRST
// character the release-name alphabet refuses comes back with no bound at all:
// every escape opens with a hyphen, so such an identity encodes to a name
// beginning with a hyphen, and Helm requires a lowercase letter or a digit
// there. No shortening reaches that, and offering a bound would send a reader
// off to shorten a name that would still be refused.
//
// Nothing is checked at the END of the name, and that is a property of the
// encoding rather than an omission: an encoded segment never ends with a hyphen,
// so the last character is always one the alphabet admits.
//
// The bound is measured in bytes of the PROPOSED identity, not of the encoded
// name, because bytes of the proposed identity are what the compiler shortens
// by. The two differ by however much escaping the identity costs, which is
// exactly the knowledge that stays on this side of the boundary.
func ReleaseNameVerdict(proposed string) rendercore.DerivedNameVerdict {
	name := releaseNameEncoding.Segment(proposed)
	if proposed != "" && !isReleaseNameRune(rune(name[0])) {
		return rendercore.DerivedNameVerdict{
			Kind:   ReleaseNameKind,
			Usable: false,
			Reason: "a release name begins with a lowercase letter or a digit, and this identity's first character " +
				"is one the release-name alphabet does not admit, so its escape opens the name with a hyphen",
		}
	}
	if len(name) > releaseNameMaxLength {
		bound := longestReleaseNameSource(proposed)
		return rendercore.DerivedNameVerdict{
			Kind:   ReleaseNameKind,
			Usable: false,
			Bound:  &bound,
			Reason: fmt.Sprintf("a release name is at most %d characters, and this identity of %d encodes to %d "+
				"because the release-name alphabet admits neither uppercase letters nor punctuation and escapes each "+
				"of them", releaseNameMaxLength, len(proposed), len(name)),
		}
	}
	return rendercore.DerivedNameVerdict{Kind: ReleaseNameKind, Usable: true}
}

// ServiceNameKind is what a verdict about the name of a Kubernetes Service this
// target emits for a component calls itself. It reaches a reader verbatim in a
// refusal, so it is written in the target's vocabulary.
const ServiceNameKind = "Service name"

// ServiceNameVerdict judges a proposed component identity as the opening of the
// Kubernetes Service names this target emits for that component.
//
// A Service name has to begin with a lowercase letter, which is stricter than
// what a release name may begin with: Helm accepts a release name opening with a
// digit, and the cluster then refuses every Service named after it. Both of this
// target's render paths name a component's Services after its release name — a
// chart routinely names its Services after it, and a synthesized workload's
// Services open with it — so an identity opening with anything but a lowercase
// letter yields Services the cluster refuses at apply time while the build itself
// would stay green. The verdict carries that
// stricter rule here, where it can halt the build instead.
//
// It reads the first character of the release name rather than of the identity as
// proposed, so it judges the one derivation this target names a component's
// resources from rather than a second reading of the same identity. A digit is
// carried through that derivation as itself, and any character the release-name
// alphabet does not admit opens the name with the hyphen every escape begins with;
// both are refused. No bound comes back with a refusal, because no shortening
// changes the first character.
//
// Only the first character is judged. How long a Service name may grow depends
// on the Service key a workload declares and on what a chart appends, neither of
// which a component identity alone determines.
func ServiceNameVerdict(proposed string) rendercore.DerivedNameVerdict {
	name := releaseNameEncoding.Segment(proposed)
	if proposed != "" && !isLowercaseLetter(name[0]) {
		return rendercore.DerivedNameVerdict{
			Kind:   ServiceNameKind,
			Usable: false,
			Reason: fmt.Sprintf("a Kubernetes Service name begins with a lowercase letter, and the Services this "+
				"target names after this identity would begin with %q", string(name[0])),
		}
	}
	return rendercore.DerivedNameVerdict{Kind: ServiceNameKind, Usable: true}
}

// isLowercaseLetter reports whether a byte is a lowercase ASCII letter, the only
// character a Kubernetes Service name may begin with.
func isLowercaseLetter(c byte) bool {
	return c >= 'a' && c <= 'z'
}

// longestReleaseNameSource returns how many bytes of proposed still encode to a
// release name the target accepts.
//
// It measures rather than estimates, because what a byte costs is not uniform:
// an admitted character costs one, a punctuation mark four, and any other
// character nine. An estimate from a ratio would be wrong in both directions —
// too generous on a name of capitals, which would send the compiler round the
// loop again for nothing, and too mean on an ordinary name, which would shorten
// one that did not need it.
//
// The scan walks character boundaries so a cut never lands inside one, and it
// returns the last boundary that fitted. Zero is a real answer: an identity
// whose very first character overruns the limit yields it, and the compiler
// reads a non-positive bound as a name no length reaches.
func longestReleaseNameSource(proposed string) int {
	best := 0
	for i := range proposed {
		if len(releaseNameEncoding.Segment(proposed[:i])) > releaseNameMaxLength {
			break
		}
		best = i
	}
	if len(releaseNameEncoding.Segment(proposed)) <= releaseNameMaxLength {
		best = len(proposed)
	}
	return best
}

// NameBoundOutput assembles this provider's answer to one proposed component
// identity: one verdict per kind of name it derives from that identity.
//
// The release name is this target's own, derived here. The Service name is judged
// on that same derivation, because a component's Services are named after it and
// the cluster holds a Service name to a stricter opening than Helm holds a release
// name. The component-id label value is the identity written onto every manifest
// this provider emits, judged where that label is written rather than restated
// here, so the rule and the write stay one thing.
//
// The release name comes before the Service name, and the order is deliberate: an
// identity whose first character is escaped is refused as both, and the compiler
// reports the earlier of two refusals that carry no bound, so such an identity
// keeps being reported as the release name it cannot be.
//
// A guest reports every kind it derives rather than only the kinds that refused,
// so a reader of a refusal sees what was judged and the host needs no second
// call to learn the rest.
//
// Two limits this provider's target also imposes are deliberately NOT reported
// here, and each is absent for the same reason: neither is derivable from a
// component identity alone. A service port name is a separate field an author
// writes on the workload, so no identity determines it. A scheduled job's name
// is bounded below the general limit because the controller creating each run
// appends a suffix to it — the same reservation this whole exchange implements,
// appearing in the target itself — but whether a node emits one is a property of
// its workload rather than of its identity, and answering as though every node
// did would refuse names that are legal for the workload the node actually
// emits. Reaching them needs the node's resolved workload on the question, which
// is a wider question than this one.
//
// Only the label kind is told whether the identity was authored, and the
// asymmetry is deliberate. Each kind judges the value ITS OWN name is derived
// from, and the values are not the same. The release name is derived here,
// from the proposal, whichever way the proposal was arrived at — so the proposal
// is what this side measures, and handing it an already-written identity would
// put one encoding inside another and spell the name differently from the one a
// recipe author predicts it by. The label value is not derived at all: it IS the
// identity as stamped, which for an authored identity is the proposal written
// into the identity alphabet. So the label kind needs to know which state the
// proposal is in, and the release name and the Service name read from it do not.
func NameBoundOutput(proposed string, authored bool) rendercore.NameBoundOutput {
	return rendercore.NameBoundOutput{
		Names: []rendercore.DerivedNameVerdict{
			ReleaseNameVerdict(proposed),
			ServiceNameVerdict(proposed),
			rendercore.ComponentIDLabelVerdict(proposed, authored),
		},
	}
}

// RenderedNameOutput assembles this provider's answer to the question the host
// asks before any node renders: what name will this target give the node's
// resources. It is the release name for a node whose Recipe declares source the
// author supplied or a workload this provider synthesizes, and no name for a node
// declaring neither.
//
// The two input kinds answer with one name because both render paths name a
// component's resources after it. A chart's own templates read it as
// .Release.Name and routinely name their resources after it; the synthesized path
// names every resource it emits for a component after it too
// ([synthesizedNameBasis]). Both read the one derivation, [ReleaseNameFor], so the
// name reported here and the names emitted cannot part — and an address a Recipe
// publishes from this answer is the name a Service the node emits actually
// carries.
//
// A node declaring neither renders an artifact set alone, and gets no name: the
// resources in an artifact set are named by the artifacts its author wrote, so a
// name reported here would be one none of that node's emitted resources carries.
// No name is the answer rather than an omission.
func RenderedNameOutput(in rendercore.RenderedNameInput) rendercore.RenderedNameOutput {
	if !in.Sourced && !in.Synthesized {
		return rendercore.RenderedNameOutput{}
	}
	return rendercore.RenderedNameOutput{Name: ReleaseNameFor(in.NodeID, in.DerivedNameBasis)}
}
