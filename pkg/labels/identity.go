// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package labels

import (
	"strings"
	"unicode/utf8"
)

// reservedScopePrefixes are the openings a composed identity is written behind,
// read from the scopes that write them so that both the composition and whatever
// refuses an authored identity work from one set.
//
// The set exists rather than a single constant because it is the thing a refusal
// has to read: a composed identity opens with one of these followed by the
// encoding's join, and an authored identity spelling that opening would be
// indistinguishable from a composed one. Reading the members off the scopes —
// rather than letting a refusal spell them again — is what keeps the two sides
// from agreeing on the day the refusal is written and drifting afterwards, which
// for an identity space means an authored name silently taking a composed name's
// place. A scope added to the composition is refused here in the same act.
var reservedScopePrefixes = []string{
	BoundaryFeatureScope.prefix,
	AttachmentScope.prefix,
}

// ComponentIdentityDefect names what stops a string from serving as the
// component identity every resource a node emits is stamped with.
//
// An ordinary component's identity is the identity its author wrote, carried
// through unmodified: it is the deployed name of the workload, so nothing writes
// it into a different alphabet. That leaves refusal as the only way such an
// identity can be held to the target's rules, which is why this reports a defect
// rather than returning a repaired value.
type ComponentIdentityDefect int

const (
	// ComponentIdentityUsable is the absence of a defect.
	ComponentIdentityUsable ComponentIdentityDefect = iota

	// ComponentIdentityCharacterNotAdmitted is an identity carrying a character
	// a label value does not admit.
	ComponentIdentityCharacterNotAdmitted

	// ComponentIdentityEdgeNotAlphanumeric is an identity that does not both
	// open and close on a letter or a digit.
	ComponentIdentityEdgeNotAlphanumeric

	// ComponentIdentityReservedOpening is an identity opening exactly as a
	// composed identity does, which would make the two indistinguishable.
	ComponentIdentityReservedOpening
)

// Requirement is the rule the identity failed, phrased as what a name has to do
// rather than as what this one did — so a diagnostic can state it after the name
// and a reader learns what to write instead.
//
// It is empty for [ComponentIdentityUsable], which failed nothing, and for
// [ComponentIdentityReservedOpening], whose requirement names the opening that
// was matched and so belongs to the occurrence rather than to the class.
//
// No requirement here states a LENGTH, and the absence is the design rather than
// an omission. How long a name may be is a property of the target it is written
// for, so it is answered by the guest that renders for that target and reported
// with that target's own reason — not stated here, where a number would be one
// target's limit imposed on every target.
func (d ComponentIdentityDefect) Requirement() string {
	switch d {
	case ComponentIdentityCharacterNotAdmitted:
		return "use only letters, digits, '-', '_' and '.'"
	case ComponentIdentityEdgeNotAlphanumeric:
		return "begin and end with a letter or a digit"
	default:
		return ""
	}
}

// InspectComponentIdentity reports what stops identity from being stamped on the
// resources a component emits, and — for a reserved opening — the opening it
// matched.
//
// Two questions are asked, in a fixed order so that an identity offending
// against both is reported the same way every run, and the order is chosen for
// what it saves the author rather than for tidiness.
//
// The reserved opening is asked FIRST. A name in the reserved namespace has to
// be renamed whatever else is true of it — no edit to its characters or its
// length can make it acceptable — so an author told about a character rule first
// would fix it and then be told to abandon the name anyway. Asking the other way
// round is what costs a round trip. The label rules are asked second, where the
// remedy they name is one the author can actually carry out on the name they
// have.
//
// The rules of the second question are the ones [SanitizeForK8sLabelValue]
// already writes a value into, read from where that function keeps them rather
// than restated, so a validity test and a repair can never come to disagree
// about what is valid. The first reads the reserved openings and the join from
// the composition that writes them, for the same reason.
//
// The empty string is reported usable, because a label value may be empty. No
// authoring surface produces an empty component identity, so this is a statement
// about the label rules rather than a case a build reaches.
func InspectComponentIdentity(identity string) (ComponentIdentityDefect, string) {
	if opening, reserved := reservedOpeningOf(identity); reserved {
		return ComponentIdentityReservedOpening, opening
	}
	return labelValueDefect(identity), ""
}

// labelValueDefect reports how identity fails the alphabet and edge rules of a
// label value, or [ComponentIdentityUsable] where it fails neither.
//
// It asks nothing about LENGTH. How long a name may be belongs to the target the
// name is written for, and this package has no target: the answer travels from
// the guest that renders for one, which is what keeps a single target's number
// from being applied to every target. What is left here is the character set and
// the edge rule, which are properties of the identity alphabet the composition
// itself writes into.
func labelValueDefect(identity string) ComponentIdentityDefect {
	if identity == "" {
		return ComponentIdentityUsable
	}
	for _, r := range identity {
		if !isPermittedLabelChar(r) {
			return ComponentIdentityCharacterNotAdmitted
		}
	}
	first, _ := utf8.DecodeRuneInString(identity)
	last, _ := utf8.DecodeLastRuneInString(identity)
	if !isAlphanumeric(first) || !isAlphanumeric(last) {
		return ComponentIdentityEdgeNotAlphanumeric
	}
	return ComponentIdentityUsable
}

// reservedOpeningOf reports whether identity opens exactly as a composed
// identity does, and which opening it matched.
//
// An opening is a reserved scope prefix written into the identity alphabet
// followed by the encoding's join. Both halves are read from the composition
// rather than spelled here: the prefix is written through the same encoding the
// composition writes it through, so a prefix needing an escape is compared in
// the form it would actually be emitted in, and the join is the one the
// composition places between its parts.
func reservedOpeningOf(identity string) (string, bool) {
	for _, prefix := range reservedScopePrefixes {
		opening := componentIdentityEncoding.Segment(prefix) + componentIdentityJoin
		if strings.HasPrefix(identity, opening) {
			return opening, true
		}
	}
	return "", false
}
