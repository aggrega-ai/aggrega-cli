// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package labels

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// Unbounded is the bound a caller states when it knows of no limit on a
// composed identity's length: the composition then returns what it composed,
// however long that is.
//
// It exists because the limit belongs to the target a name is written for, and
// this package has no target. A target's limit reaches a composition from the
// guest that renders for that target, so the caller holding that answer supplies
// it here and a caller holding no answer says so rather than inventing one. Any
// non-positive bound reads as this one, since a bound of zero admits only the
// empty identity and no composition has anything to return for it.
const Unbounded = 0

// ComponentIdentityScope is the minting site a composed identity belongs to.
//
// It carries the opening every identity that site composes is written behind, so
// two sites composing from the same authored parts still reach two identities.
// That separation is what a scope is for: without it, an identity built from a
// boundary path and an instance and one built from a host component and an
// instance would occupy a single space and could meet there.
//
// The openings are free choices rather than derived ones, because an identity is
// composed from the parts an author wrote and nothing has to be spelled out in
// the opening for those parts to be told apart again. Short is therefore
// preferred: every character an opening takes is a character the authored parts
// do not get.
type ComponentIdentityScope struct{ prefix string }

var (
	// BoundaryFeatureScope is the scope of the identity every resource a
	// boundary-attached feature emits carries. Its parts are the segments of the
	// boundary path the feature is attached to, outermost first, and the instance
	// name it is attached under.
	BoundaryFeatureScope = ComponentIdentityScope{prefix: "bfr"}

	// AttachmentScope is the scope of the identity every resource a feature
	// attached to one component emits carries. Its parts are the identity of the
	// host component and the instance name the feature is attached under.
	//
	// The host is a part rather than context a reader is expected to supply,
	// because it is what separates one instance name attached to several
	// components: those attachments emit different resources and so need
	// different identities, and an author who names them alike has written
	// nothing wrong.
	AttachmentScope = ComponentIdentityScope{prefix: "cfr"}
)

// identityInstanceReserve is how many characters of a shortened identity the
// last part keeps.
//
// The run is reserved whether that part needs all of it or not, which is what
// makes the budget left for the parts before it a single number rather than an
// expression evaluated per identity: one figure to read, at the cost of at most
// a few characters nothing uses.
const identityInstanceReserve = 12

// identityDigestLength is how many hexadecimal characters of the identity's
// digest a shortened identity carries.
//
// The width is one number, chosen here, and it is chosen ahead of any demand
// for it rather than in answer to one. A demand would announce itself: the
// duplicate check every derived name is declared into halts naming both
// identities the day two of them reach one value. Waiting for it is still the
// wrong trade in this one case, because the width is not a free choice once
// anything carries it — every shortened identity is named after this number, so
// moving it renames all of them, and a rename in a cluster is a
// destroy-and-recreate rather than an update. The cheap moment to fix the width
// is before anything is named with it and the expensive moment is every moment
// after, which is why it is not left for the halt to force.
//
// What the characters buy is a smaller chance that two shortened identities
// reach one value; what they cost is that much less room for the authored
// parts, in exactly the cases where the identity was already being shortened.
// They buy no guarantee. No injective map exists from an unbounded domain into
// a bounded one, so a shortened identity's uniqueness rests on the duplicate
// halt and not on this number: a wider digest moves the odds and leaves the
// kind of guarantee exactly where it was.
const identityDigestLength = 10

// componentIdentityEncoding writes an authored part into the characters a
// Kubernetes label value admits, escaping every character the alphabet refuses
// rather than substituting for it — so two parts differing at all reach two
// written forms.
//
// The admitted alphabet is the ASCII letters and the digits and nothing else,
// which is deliberately narrower than the label-value charset. A label value
// also carries an underscore, a dot and a hyphen, but it has to begin and end
// with a letter or a digit; the hyphen is the encoding's own and no alphabet may
// admit it; and admitting the underscore or the dot would let an authored part
// END with one, leaving a composed value ending on a character the target
// refuses. With the alphanumerics alone, every character a composition emits is
// a letter, a digit or a hyphen, a written part never ends with a hyphen, and a
// composition always opens with its scope — so a composed value is a valid label
// value for every input by construction, rather than by a check that could be
// reached with nothing left to return.
//
// The alphabet is held here while the LENGTH bound is not, and the asymmetry is
// deliberate. A length is one target's limit: holding it here would apply it to
// every target, including the targets that impose none, so a length arrives from
// whichever guest renders. An alphabet narrower than every target's is a different
// thing — not one target's rule imposed on the rest, but the intersection of all of
// them — so a composed value is legal everywhere it can be written without the
// compiler learning what any target admits. That narrowness also protects a target
// whose own derivation folds rather than escapes: an alphabet admitting the dot
// would hand it a name it must fold, and two identities can fold to one. The
// guest-side channel therefore carries lengths and grammars; the alphabet stays here.
var componentIdentityEncoding = namesegment.NewEncoding(isAlphanumeric)

// nodeIdentityEncoding writes a node identity into the alphabet every authored
// name already satisfies: the ASCII letters, the digits and the underscore.
//
// That set is the authoring surface's own, less the hyphen. A component, module
// or subsystem name is held to letters, digits, the underscore and the hyphen
// where it is authored, and the hyphen is the encoding's own — it opens every
// escape and composes the join, so no alphabet may admit it. What the encoding
// does with a hyphen is keep it where it can: an authored hyphen is carried
// through as written wherever the character after it is admitted, which is every
// position an ordinary name puts one in.
//
// So the only character this encoding writes out of an identity the compiler can
// produce is the one that joins a qualified identity together, which no authored
// name may carry. Every ordinary authored name is returned byte for byte, which
// is what keeps a rendered diff readable: the names move only where the identity
// was composed rather than written.
//
// The alphabet is deliberately not any target's. It carries no knowledge of what
// a target admits, and it is not the intersection of the targets either — it is
// the set the authoring surface already holds its own names to, so an identity
// written through it is spelled the way its author would have spelled it. A
// target whose own alphabet is poorer narrows it again on its own side, and a
// target whose derivation folds rather than escapes is protected for the reason
// stated above: this encoding hands it no dot to fold.
var nodeIdentityEncoding = namesegment.NewEncoding(isAuthoredNameRune)

// isAuthoredNameRune reports whether a character is carried through
// [nodeIdentityEncoding] as itself: an ASCII letter, a digit or an underscore.
// The hyphen is absent because it is the encoding's own; every other character,
// the identity join included, is escaped.
func isAuthoredNameRune(r rune) bool {
	return isAlphanumeric(r) || r == '_'
}

// EncodeNodeIdentity writes a node identity into the alphabet every authored
// component name already satisfies, so the identity can be spent as a name on
// any target without the caller knowing what that target admits.
//
// A node one aggregate composes into another carries a QUALIFIED identity: the
// name the composing aggregate gave the inner one, joined to the name the inner
// one gave the node. The character that joins them is reserved out of every
// authored name precisely so that join is unambiguous — which also means it is
// the one character an identity can carry that no name an author wrote ever
// does, and the one character a deployment target is therefore liable to refuse.
//
// This writes that character out as an escape and carries everything else
// through. The encoding is injective, so two identities differing at all reach
// two names and no two components can arrive at one: substituting for the join
// instead would let an identity joining `a` to `b` and one an author wrote as
// `a_b` meet, and a name two components reach identifies neither.
//
// An identity carrying no join comes back as it was written, with two exceptions
// that are both legal to author: a name ending in a hyphen, and a name whose
// hyphen is followed by the character an escape is marked with. Neither can be
// carried through as authored without making an escape ambiguous, so each is
// written out instead.
//
// An empty identity comes back empty. There is nothing to tell apart from
// anything, so it gets no stand-in — a caller with no identity to spell wants no
// name rather than a placeholder that reads like one.
//
// It is NOT for a value composed from several parts — [ComposeComponentIdentity]
// composes one of those, over its own alphabet, and passing its result here would
// escape the join it composed with and spell one identity two ways. Nor is it for
// the identity a graph is keyed by: a caller looking a node up, or naming one in
// a diagnostic, wants the identity as the author can read it.
//
// The derivation is deterministic: one identity always yields one name.
func EncodeNodeIdentity(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	return nodeIdentityEncoding.Segment(nodeID)
}

// NodeIdentityBound returns how many bytes of nodeID still reach a value of at
// most limit bytes once [EncodeNodeIdentity] has written it.
//
// It exists because a caller holding a limit on the WRITTEN value has to state
// its bound in bytes of the identity, which is what an author shortens by. The
// two differ by however much writing the identity out costs, and that cost is
// this package's to know rather than the caller's.
//
// It measures rather than estimates, because what a byte costs is not uniform: a
// character the alphabet carries through costs one, the character joining a
// qualified identity costs three, and a character with no selector of its own
// costs nine. An estimate from a ratio would be wrong in both directions — too
// generous on an identity joined several times, which would send an author back
// to the same wall a second time, and too mean on an ordinary name, which would
// ask them to shorten one that already fitted.
//
// The scan walks character boundaries so a cut never lands inside one, and it
// keeps the LAST boundary that fitted rather than stopping at the first that did
// not. The written length does not rise monotonically with what is written: a
// hyphen an author wrote is carried through only where the character after it is
// one the alphabet admits, so a cut landing immediately after a hyphen writes
// that hyphen out and costs more than a cut one character further on. Stopping
// at the first overrun would report that cut's cost as the wall and hand back a
// smaller number than the encoding requires.
//
// Zero is a real answer, and two things yield it: a limit of zero or less, and an
// identity whose very first character already overruns. A caller reporting zero
// says no length of this identity reaches a value the limit admits.
//
// It is DELIBERATELY not shared with the same measurement a target makes over its
// own alphabet, and the near-duplication is the point rather than an oversight.
// What a byte costs is a property of the alphabet doing the writing, so the
// arithmetic belongs beside the alphabet: one shared measurement would have to be
// told which alphabet to use, and the only caller holding that answer is the one
// that already owns the alphabet. Sharing it would put every alphabet's bound on
// one path, which is the single place a mis-measurement could reach all of them.
func NodeIdentityBound(nodeID string, limit int) int {
	if limit <= 0 {
		return 0
	}
	best := 0
	for i := range nodeID {
		if len(EncodeNodeIdentity(nodeID[:i])) <= limit {
			best = i
		}
	}
	if len(EncodeNodeIdentity(nodeID)) <= limit {
		best = len(nodeID)
	}
	return best
}

// The encoding's own two fixed strings, read out of the encoding rather than
// restated here as literals. A literal copy would agree with the encoding on the
// day it was written and drift afterwards, and the agreement is exactly what
// would hide the drift.
//
// The mark is what the encoding writes for a part with no value at all; its
// selector stands for no character, so no authored part can produce it, which is
// what lets a shortened identity be marked as one. The join is what the encoding
// writes between two parts: assembling two parts whose written forms are known
// leaves precisely the join between them.
var (
	componentIdentityAbsentMark = componentIdentityEncoding.Segment("")
	componentIdentityJoin       = strings.TrimSuffix(
		strings.TrimPrefix(componentIdentityEncoding.Name("", ""), componentIdentityAbsentMark),
		componentIdentityAbsentMark)
)

// identityContextReserve is how many characters of a shortened identity the
// parts before the last one keep: whatever the caller's bound leaves once the
// digest's own space, the scope's opening, the last part's reserved run and the
// joins between the regions are all accounted for.
//
// It is one number per identity rather than an expression evaluated per part,
// which is the whole point of reserving a fixed run for the last part.
//
// 🔴 The join term is computed from the regions actually being joined, and that
// is a correctness property rather than a tidiness one. A fixed multiple is
// correct only for the arity its author had in hand: one more region needs one
// more join, and a budget that did not account for it would hand the earlier
// parts characters the limit does not have — so the composition would emit a
// value LONGER than the target accepts, which is a name the target refuses
// rather than one that merely reads poorly. Every other reserve here is a
// property of the scheme; this one is a property of the caller, so it is the
// caller's arity that decides it.
//
// The scope's opening is measured rather than assumed for the same reason: two
// scopes need not write openings of one length.
func identityContextReserve(scope ComponentIdentityScope, bound, regions int) int {
	joins := regions - 1
	if joins < 0 {
		joins = 0
	}
	return bound -
		len(componentIdentityEncoding.Segment(scope.prefix)) -
		joins*len(componentIdentityJoin) -
		identityInstanceReserve -
		len(componentIdentityAbsentMark) -
		identityDigestLength
}

// ComposeComponentIdentity composes the component identity every resource a
// derived node emits carries, from a scope and the parts an author wrote, in
// order.
//
// The identity is COMPOSED from those parts rather than read out of any string
// the compiler built for its own use, and that is what makes it injective. Each
// part is written into the label alphabet by an encoding that escapes what the
// alphabet refuses instead of folding it away, and the parts are assembled with
// a join no written part can contain. So two identities differing at all — in a
// character the alphabet refuses, in where one part ends and the next begins, in
// how many parts there are — reach two identities, and reading an identity back
// at its joins returns exactly the scope and parts it was built from.
//
// Two identities composed under DIFFERENT scopes can never meet, because an
// identity opens with its scope written through the same encoding and the
// openings differ. An identity composed here can never meet a component identity
// an author wrote either: an authored identity is carried through unmodified,
// and one opening as a composed identity does is refused where it is authored
// rather than repaired here. [InspectComponentIdentity] is that refusal, and it
// reads the same set of openings this composition writes.
//
// The LAST part is the one a reader looks for and the one that discriminates
// most between two identities in one deployment, which is also what makes it the
// part worth keeping when an identity has to be shortened.
//
// The bound is the caller's, in characters, and [Unbounded] states that the
// caller knows of none. It is a parameter rather than a constant here because a
// limit on a name belongs to the target that name is written for, and this
// package has no target: the number reaches a caller from the guest that renders
// for the target, and a caller with no such answer says Unbounded instead of
// standing in for one.
//
// Where the composed identity fits the bound it is returned as it stands, which
// is the case for every ordinary derivation: no digest, no shortening, and a
// hyphen an author wrote still reads as one. An Unbounded composition always
// takes that path.
//
// Where it does not fit, injectivity cannot hold in full, because no injective
// map exists from unbounded parts into a bounded value. What comes back then is
// the identity shortened around a digest of the whole of it, behind a marker no
// authored part can produce — so a shortened identity can never be mistaken for
// one that fitted. Two shortened identities are unlikely to meet, and the
// duplicate check every derived name is declared into halts naming both where
// the two contributors' output can reach one another. Where it cannot — two
// attachments on one component, under a provider that folds its group's output
// into one document and writes each attachment's own resources beside it as
// files of their own — the two are not compared, and what is left there rests
// on the unlikelihood alone.
//
// The derivation is deterministic in both the bound and the parts: one bound and
// one part list always yield one identity, so a caller that asks the same
// question twice gets the same answer twice.
func ComposeComponentIdentity(scope ComponentIdentityScope, bound int, parts ...string) string {
	plain := componentIdentityEncoding.Name(componentIdentityParts(scope, parts)...)
	if bound <= Unbounded || len(plain) <= bound {
		return plain
	}
	return shortenComponentIdentity(plain, scope, bound, parts)
}

// componentIdentityParts orders the segments a composed identity is assembled
// from: the scope's opening, then the authored parts in the order given.
func componentIdentityParts(scope ComponentIdentityScope, parts []string) []string {
	ordered := make([]string, 0, len(parts)+1)
	ordered = append(ordered, scope.prefix)
	return append(ordered, parts...)
}

// shortenComponentIdentity returns the identity that stands in for one longer
// than the caller's bound.
//
// The order of the steps is the safety property, which is why they are written
// out in it rather than folded into one expression. The digest's own space is
// reserved FIRST, before anything is measured; the last part then takes its
// fixed run; the parts before it take what those reserves leave; and the digest
// is appended last. Because every reserve comes out of the bound before any part
// is looked at, no budget here can go negative and every input has an output —
// where working out the remainder first can reach a negative budget, which in
// the obvious implementation cuts a string from the wrong end and returns a
// plausible wrong identity with nothing reporting it.
//
// A bound smaller than the reserves themselves leaves the leading region nothing
// and is not an error: the identity that comes back is the scope, the last part's
// run, and the digest, which is the shortest form this scheme has. It can still
// exceed a very small bound, and it is the caller — holding the target's own
// answer — that decides what to do about that, since this package cannot tell a
// bound no scheme could meet from one it merely failed to meet this pass.
func shortenComponentIdentity(plain string, scope ComponentIdentityScope, bound int, parts []string) string {
	// The last part is kept whole up to its reserve; whatever precedes it shares
	// what is left. An identity composed from a single part has no leading run,
	// and none is emitted for it: an empty region between two joins would read as
	// a part somebody wrote.
	var leading []string
	last := ""
	if n := len(parts); n > 0 {
		leading = parts[:n-1]
		last = parts[n-1]
	}

	// How many regions this identity is assembled from, counted before any budget
	// is worked out because the joins between them come out of that budget: the
	// scope, the leading parts where there are any, the last part, and the digest.
	hasLeading := len(leading) > 0
	regionCount := 3
	if hasLeading {
		regionCount++
	}

	// Reserved first: this run is subtracted from the limit in
	// identityContextReserve before any part below is measured.
	tail := componentIdentityAbsentMark + componentIdentityDigest(plain)
	// Then the last part's fixed run.
	lastRegion := clipWrittenRegion(
		componentIdentityEncoding.Segment(last), identityInstanceReserve)
	// Then whatever the reserves leave, for the parts before it.
	leadingRegion := clipWrittenRegion(
		componentIdentityEncoding.Name(leading...), identityContextReserve(scope, bound, regionCount))

	regions := make([]string, 0, regionCount)
	regions = append(regions, componentIdentityEncoding.Segment(scope.prefix))
	if hasLeading {
		regions = append(regions, leadingRegion)
	}
	return strings.Join(append(regions, lastRegion, tail), componentIdentityJoin)
}

// clipWrittenRegion cuts an already-written region to at most n characters,
// keeping its beginning, and drops any hyphen the cut left at the end.
//
// The beginning is what is kept because that is where an identity
// discriminates: two identities in one deployment share their leading parts far
// more often than they share their leading characters.
//
// The cut is by byte, which is exact here because a written region carries only
// ASCII — an admitted character is an ASCII letter or digit, and every other
// character is written as an escape built from ASCII. The cutset is the join,
// whose character is the only non-alphanumeric one a written region can carry.
// Dropping a trailing one is for readability rather than validity: the digest
// always follows, so the value ends on a hexadecimal character whatever this
// leaves behind.
//
// A negative n keeps nothing. It arises where the caller's bound is smaller than
// the reserves the scheme takes before any part is measured, which is a bound a
// target stated rather than a defect here — so the region is emptied instead of
// the cut being attempted, which on a negative index would stop the build with a
// runtime fault rather than with the target's own answer.
func clipWrittenRegion(region string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(region) > n {
		region = region[:n]
	}
	return strings.TrimRight(region, componentIdentityJoin)
}

// componentIdentityDigest is the discriminator a shortened identity carries: the
// leading hexadecimal characters of the SHA-256 of the composed identity.
//
// It is taken over the composed identity rather than over the parts, because the
// composed form is those parts written out injectively — so two identities
// differing at all differ in what is hashed, including two differing only in
// where one part ends and the next begins. Hashing the parts would need a second
// way of running them together, and a second one is a second thing that can
// disagree with the first.
//
// This is not the content-address spelling stored bytes are named by: that one
// carries its algorithm and is compared verbatim across a distribution
// boundary, while this one only has to tell two identities apart inside one
// build.
func componentIdentityDigest(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])[:identityDigestLength]
}
