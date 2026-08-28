// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import "slices"

// RootKeyFault classifies what a reference into a context root whose keys become
// readable progressively cannot legitimately read at the point the referencing
// template is evaluated.
//
// The two faulting values are separate because the two mistakes have separate
// fixes, and a classification collapsing them would leave every reader to
// re-derive the distinction — or to report one remedy for both, which tells an
// author who mistyped a key to reorder a block that is already in the right
// order.
type RootKeyFault int

const (
	// RootKeyAdmitted is every reference the root's own contract permits: a key
	// already readable, a bare reference to the root itself, and any reference
	// anchored somewhere else.
	RootKeyAdmitted RootKeyFault = iota

	// RootKeyUnresolved is a reference to a key the root declares and has not
	// made readable yet. It is an ORDERING fault: the definition exists and sits
	// below its reader.
	RootKeyUnresolved

	// RootKeyUndeclared is a reference to a key the root does not declare at
	// all. It is a NAMING fault: no ordering makes it readable, so the key
	// itself is what has to change.
	RootKeyUndeclared
)

// RootKeyReference is the first offending reference a template makes into one
// context root, with the fault it carries.
//
// Path is the reference spelled the way a diagnostic quotes it back to the
// author, and Key is the root's own key it names — the two are separate because a
// diagnostic states both: the expression the author wrote, and the key that
// expression cannot reach. Both are empty when the fault is [RootKeyAdmitted].
type RootKeyReference struct {
	Fault RootKeyFault
	Path  string
	Key   string
}

// ClassifyRootKeyReference is the ONE decision behind every refusal of a
// reference into a context root whose keys become readable progressively. Every
// surface that admits or refuses such a reference routes through it, so no two
// surfaces can come to disagree about what is legal.
//
// It lives here rather than beside any one of those surfaces because the surfaces
// sit in packages that cannot reach each other: the render path and the
// lock-time closure walk both resolve such a root, and the render path already
// imports the closure walk — so a decision held in either of them is unreachable
// from the other by construction. This package is the one both already import.
//
// # What it reads, and what that makes it
//
// resolved is the root's keys already readable when this template runs; declared
// is every key the root names. Neither carries a value: the decision is about
// reference NAMES and nothing about it inspects what a key resolved to. So the
// classification is a pure function of (the authored text, the readable key set,
// the declared key set) — which is what lets one surface decide it while
// evaluating and another decide it from declarations alone, with the same answer.
//
// The two sets are what separate the two faults, and the separation is the point.
// A surface with no ordering fault to report — one whose whole root is readable
// before any reference runs — passes the same set as both arguments, which
// collapses the classification to the naming fault alone.
//
// # It reads the parse tree, never the text
//
// The reference set comes from [CollectRootRefs], the under-collecting walk every
// reference check that admits or refuses authoring is built on. So the root's name
// appearing inside a string literal or a comment is not a reference, and a key
// read inside a rebound range body names a field of the element rather than a key
// of this root. The polarity is not a parameter and must not become one: the
// over-collecting walk beside it would attribute references the template language
// permits, and refusing those refuses legal authoring.
//
// A malformed template yields no tree and no fault. A parse failure is the
// evaluator's diagnostic to report, and naming a reference fault for one would
// describe the author's mistake wrongly.
//
// A bare reference to the root is admitted, matching the whole-root admission
// every scope applies: what a template does with the root once it holds it is the
// evaluator's business.
func ClassifyRootKeyReference(
	text, leftDelim, rightDelim, root string,
	resolved, declared []string,
) RootKeyReference {
	if text == "" {
		return RootKeyReference{}
	}
	tree, err := ParseTree(text, leftDelim, rightDelim)
	if err != nil {
		return RootKeyReference{}
	}
	for _, ref := range CollectRootRefs(tree) {
		if ref.Segs[0] != root || len(ref.Segs) < 2 {
			continue
		}
		key := ref.Segs[1]
		if slices.Contains(resolved, key) {
			continue
		}
		fault := RootKeyUndeclared
		if slices.Contains(declared, key) {
			fault = RootKeyUnresolved
		}
		return RootKeyReference{Fault: fault, Path: ref.Path, Key: key}
	}
	return RootKeyReference{}
}
