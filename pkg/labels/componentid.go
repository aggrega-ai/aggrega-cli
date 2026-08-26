// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package labels

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// k8sLabelValueMaxLength is the longest string Kubernetes accepts as a label
// value. It is what bounds a composed identity from above, and so the only
// reason a composition ever needs shortening.
const k8sLabelValueMaxLength = 63

// boundaryFeatureIDPrefix opens the identity of every resource a
// boundary-attached feature emits, so such an identity is recognisable at a
// glance and is never mistaken for a component's own name.
//
// It is a free choice rather than a derived one: the identity is composed from
// the parts an author wrote, so nothing has to be spelled out here for those
// parts to be told apart again. Short is therefore preferred, because every
// character it takes is a character the authored parts do not get.
const boundaryFeatureIDPrefix = "bfr"

// boundaryFeatureInstanceReserve is how many characters of a shortened identity
// the feature instance keeps.
//
// The run is reserved whether the instance needs all of it or not, which is what
// makes the budget left for the boundary path a single number rather than an
// expression evaluated per identity: one figure to read, at the cost of at most
// a few characters nothing uses.
const boundaryFeatureInstanceReserve = 12

// boundaryFeatureDigestLength is how many hexadecimal characters of the
// identity's digest a shortened identity carries.
//
// Six is what ships. The width is configurable in principle — eight or ten
// characters would each make it less likely still that two shortened identities
// reach one value — and it is deliberately NOT built ahead of demand: two more
// characters come out of a budget that is already scarce, and nothing measured
// says the extra room is needed. What will say so is the duplicate check every
// derived name is declared into. It halts naming both identities the day two of
// them do reach one value, so the demand announces itself instead of having to
// be guessed at now, and widening the digest is the answer at that point.
const boundaryFeatureDigestLength = 6

// boundaryFeatureIDEncoding writes an authored part into the characters a
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
// composition always opens with the prefix — so a composed value is a valid
// label value for every input by construction, rather than by a check that
// could be reached with nothing left to return.
var boundaryFeatureIDEncoding = namesegment.NewEncoding(isAlphanumeric)

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
	boundaryFeatureIDAbsentMark = boundaryFeatureIDEncoding.Segment("")
	boundaryFeatureIDJoin       = strings.TrimSuffix(
		strings.TrimPrefix(boundaryFeatureIDEncoding.Name("", ""), boundaryFeatureIDAbsentMark),
		boundaryFeatureIDAbsentMark)
)

// boundaryFeatureContextReserve is how many characters of a shortened identity
// the boundary path keeps: whatever the limit leaves once the digest's own
// space, the prefix, the instance's reserved run and the joins between them are
// all accounted for.
//
// It is one number rather than an expression evaluated per identity, which is
// the whole point of reserving a fixed run for the instance. The three joins are
// the worst case — prefix to path, path to instance, instance to digest — so an
// identity carrying no boundary path simply comes out shorter than the limit
// rather than finding this budget wrong.
var boundaryFeatureContextReserve = k8sLabelValueMaxLength -
	len(boundaryFeatureIDEncoding.Segment(boundaryFeatureIDPrefix)) -
	3*len(boundaryFeatureIDJoin) -
	boundaryFeatureInstanceReserve -
	len(boundaryFeatureIDAbsentMark) -
	boundaryFeatureDigestLength

// BoundaryFeatureComponentID composes the component identity every resource a
// boundary-attached feature emits carries, from the parts an author wrote: the
// instance name the feature is attached under, and the segments of the path of
// the boundary it is attached to, outermost first and empty for a feature
// attached at the top.
//
// The identity is COMPOSED from those parts rather than read out of any string
// the compiler built for its own use, and that is what makes it injective. Each
// part is written into the label alphabet by an encoding that escapes what the
// alphabet refuses instead of folding it away, and the parts are assembled with
// a join no written part can contain. So two attachments differing at all — in a
// character the alphabet refuses, in where one part ends and the next begins, in
// how deep the boundary path runs — reach two identities, and reading an
// identity back at its joins returns exactly the parts it was built from.
//
// Where the composed identity fits the label-value limit it is returned as it
// stands, which is the case for every ordinary attachment: no digest, no
// shortening, and a hyphen an author wrote still reads as one.
//
// Where it does not fit, injectivity cannot hold in full, because no injective
// map exists from unbounded parts into a bounded value. What comes back then is
// the identity shortened around a digest of the whole of it, behind a marker no
// authored part can produce — so a shortened identity can never be mistaken for
// one that fitted. Two shortened identities are unlikely to meet, and the
// duplicate check every derived name is declared into halts naming both if two
// of them do, so nothing here rests on the unlikelihood alone.
func BoundaryFeatureComponentID(instance string, boundaryPath []string) string {
	plain := boundaryFeatureIDEncoding.Name(boundaryFeatureIDParts(instance, boundaryPath)...)
	if len(plain) <= k8sLabelValueMaxLength {
		return plain
	}
	return shortenBoundaryFeatureComponentID(plain, instance, boundaryPath)
}

// boundaryFeatureIDParts orders the parts a composed identity is assembled
// from: the prefix, then the boundary path outermost first, then the instance.
//
// The instance goes last because it is the part a reader looks for and the part
// that discriminates most between two attachments in one deployment, which is
// also what makes it the part worth keeping when an identity has to be
// shortened.
func boundaryFeatureIDParts(instance string, boundaryPath []string) []string {
	parts := make([]string, 0, len(boundaryPath)+2)
	parts = append(parts, boundaryFeatureIDPrefix)
	parts = append(parts, boundaryPath...)
	return append(parts, instance)
}

// shortenBoundaryFeatureComponentID returns the identity that stands in for one
// too long to be a label value.
//
// The order of the steps is the safety property, which is why they are written
// out in it rather than folded into one expression. The digest's own space is
// reserved FIRST, before anything is measured; the instance then takes its fixed
// run; the boundary path takes what those reserves leave; and the digest is
// appended last. Because every reserve comes out of the limit before any part is
// looked at, no budget here can go negative and every input has an output —
// where working out the remainder first can reach a negative budget, which in
// the obvious implementation cuts a string from the wrong end and returns a
// plausible wrong identity with nothing reporting it.
func shortenBoundaryFeatureComponentID(plain, instance string, boundaryPath []string) string {
	// Reserved first: this run is subtracted from the limit in
	// boundaryFeatureContextReserve before any part below is measured.
	tail := boundaryFeatureIDAbsentMark + boundaryFeatureIDDigest(plain)
	// Then the instance's fixed run.
	instanceRegion := clipWrittenRegion(
		boundaryFeatureIDEncoding.Segment(instance), boundaryFeatureInstanceReserve)
	// Then whatever the reserves leave, for the boundary path.
	contextRegion := clipWrittenRegion(
		boundaryFeatureIDEncoding.Name(boundaryPath...), boundaryFeatureContextReserve)

	regions := make([]string, 0, 4)
	regions = append(regions, boundaryFeatureIDEncoding.Segment(boundaryFeatureIDPrefix))
	// A feature attached at the top has no boundary path, and so no region for
	// one: an empty run between two joins would read as a part somebody wrote.
	if len(boundaryPath) > 0 {
		regions = append(regions, contextRegion)
	}
	return strings.Join(append(regions, instanceRegion, tail), boundaryFeatureIDJoin)
}

// clipWrittenRegion cuts an already-written region to at most n characters,
// keeping its beginning, and drops any hyphen the cut left at the end.
//
// The beginning is what is kept because that is where an identity
// discriminates: two attachments in one deployment share their outer boundary
// segments far more often than they share their leading characters.
//
// The cut is by byte, which is exact here because a written region carries only
// ASCII — an admitted character is an ASCII letter or digit, and every other
// character is written as an escape built from ASCII. The cutset is the join,
// whose character is the only non-alphanumeric one a written region can carry.
// Dropping a trailing one is for readability rather than validity: the digest
// always follows, so the value ends on a hexadecimal character whatever this
// leaves behind.
func clipWrittenRegion(region string, n int) string {
	if len(region) > n {
		region = region[:n]
	}
	return strings.TrimRight(region, boundaryFeatureIDJoin)
}

// boundaryFeatureIDDigest is the discriminator a shortened identity carries: the
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
func boundaryFeatureIDDigest(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])[:boundaryFeatureDigestLength]
}
