// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// releaseNameEncoding is the segment encoding a Helm release name is built with.
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
// rather than of the encoding, and nothing here enforces either: an identity
// whose first character is escaped begins its name with the hyphen every escape
// opens with, and an identity carrying escaped characters encodes longer than
// it was authored.
var releaseNameEncoding = namesegment.NewEncoding(isReleaseNameRune)

// isReleaseNameRune reports whether a character is carried through the encoding
// as itself: a lowercase ASCII letter or a digit. Every other character,
// uppercase letters included, is escaped.
func isReleaseNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// releaseName derives a node's Helm release name from the same component
// identity its rendered output files are prefixed with,
// [rendercore.ProvenanceComponentID].
//
// One source feeds both, so the two are the same identity written twice — once
// as authored in the file prefix, once through an injective encoding here — and
// two nodes reach one release name exactly when they reach one prefix. Deriving
// it from a second value would put a lossy name beside a lossless one: the
// lossless one would keep two nodes' files apart while the lossy one quietly
// gave both a single in-cluster identity, and nothing about the files would
// show it, because a release name lives inside the rendered bytes rather than
// in the name of the file carrying them.
func releaseName(nodeCtx rendercore.NodeRenderContext) string {
	return releaseNameEncoding.Segment(rendercore.ProvenanceComponentID(nodeCtx))
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
// Two kinds are reported. The release name is this target's own, derived here.
// The component-id label value is the identity written onto every manifest this
// provider emits, judged where that label is written rather than restated here,
// so the rule and the write stay one thing.
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
func NameBoundOutput(proposed string) rendercore.NameBoundOutput {
	return rendercore.NameBoundOutput{
		Names: []rendercore.DerivedNameVerdict{
			ReleaseNameVerdict(proposed),
			rendercore.ComponentIDLabelVerdict(proposed),
		},
	}
}
