// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package standalonekind narrows the entity-kind vocabulary to the kinds an
// author writes as a document of their own.
//
// The vocabulary answers which kinds exist. This answers a second question about
// each of them: whether a document of that kind is a file somebody writes, or
// content that lives inside another kind's document. The two are not
// distinguishable from a schema. A kind whose content is embedded fixes its
// apiVersion and its kind exactly as a standalone kind does, so that the content
// can be validated where it sits, and fixing them is what makes the content
// identifiable rather than what makes it addressable. What separates the two
// cases is where the content is written, which is a property of the kind that is
// documented and decided rather than computed.
//
// It has one home because the daemon offers kinds to an author on three surfaces
// — the set it reports on the wire, the set it completes in a `kind:` field, and
// the set whose buffers it validates — and every one of them wants this
// narrowing rather than the vocabulary. Read separately, a surface offering a
// kind that no author writes and a surface declining to offer one that they do
// look the same from the outside: nothing fails, and the author is either handed
// a value nothing accepts or not handed the one they wanted.
package standalonekind

import "github.com/aggrega-ai/aggrega-cli/pkg/entitykind"

// embedded names every kind whose content is written inside another kind's
// document, each with the reason beside it.
//
// It is written out rather than derived, and that is deliberate: no mechanical
// source separates these from the kinds an author writes standalone, so there is
// nothing to derive from. Membership of the offered set stays derived from the
// vocabulary — this list is the only thing stated by hand, so the set it narrows
// still follows the vocabulary and a kind added there still arrives with no edit
// to any offering surface.
//
// A name here that the vocabulary no longer declares, and a declared kind that
// reaches neither this list nor the decided roll-call the daemon's tests hold,
// are both reported rather than left to pass in silence.
var embedded = []entitykind.Kind{
	// Workload content is written inside a Recipe or a FeatureRecipe, in its
	// `workload:` field — either inline or from a file that field imports — so
	// there is no workload document to address on its own. The apiVersion and
	// kind the content carries name it for validation where it sits; they do not
	// make it independently addressable. Offering it to an author as something to
	// draft would offer a document the product has no place for.
	entitykind.Workload,
}

// Kinds returns every kind an author writes as a document of their own, in the
// vocabulary's own declared order. The returned slice is freshly allocated, so a
// caller may reorder or filter it.
func Kinds() []entitykind.Kind {
	excluded := make(map[entitykind.Kind]bool, len(embedded))
	for _, k := range embedded {
		excluded[k] = true
	}
	declared := entitykind.Kinds()
	out := make([]entitykind.Kind, 0, len(declared))
	for _, k := range declared {
		if !excluded[k] {
			out = append(out, k)
		}
	}
	return out
}
