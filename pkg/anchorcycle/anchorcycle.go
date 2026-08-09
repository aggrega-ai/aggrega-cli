// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package anchorcycle detects a YAML anchor that is aliased somewhere inside
// its own content. Such a document denotes an infinite tree: expanding the
// alias reproduces the anchor, which contains the alias again, so no walk over
// the expanded form can terminate.
//
// It is the single home for that decision. Every surface that parses YAML into
// a node tree and then follows alias links — the entity envelope, the
// compact-form schema compiler, a provider's fragment reader — checks here
// before walking, so the rule cannot drift between them and a walker carries no
// check of its own.
//
// A parse alone is safe: parsing a node tree does not expand aliases, so a
// recursive document parses cleanly and only fails when something walks it. The
// check therefore belongs immediately after a parse, on the near side of the
// walk.
//
// It refuses recursion, never depth. A legitimately deep document parses and
// walks normally; only an anchor reached while the walk is still inside it is
// refused. Any depth limit large enough to be safe would still be large enough
// to exhaust the stack, and small enough to reject documents an author may
// legitimately write.
//
// This is a distinct rule from the reference-expansion cycle detector at
// pkg/refcycle, and the two are deliberately not unified: that one compares
// canonical string keys across documents for `$ref` and `$import` expansion,
// while this one compares node identity within one parsed document. They detect
// different things by different means and share only the shape of the word
// "cycle". They do describe the same failure class to a reader, so a caller that
// needs a sentinel reports both through the same one rather than teaching an
// author two names for one condition. That sentinel is refcycle.ErrCycle,
// which the compact-form surface re-exports as compactform.ErrCircularRef; its
// declaration documents both conditions from the reporting side. A caller with
// no sentinel of its own — a provider's fragment reader — prefixes Explain with
// its own locator and reports the refusal directly.
package anchorcycle

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Explain renders the refusal an author is shown when Find reports an anchor,
// naming what is wrong and what to change. A caller prefixes whatever locates
// the document for its own surface — a file path, an entity and field — and
// adds nothing else, so an author meets one wording for one condition wherever
// it is met.
//
// It names the remedy rather than only the fault, because the fault is visible
// in the text the author already has and the remedy is not: an alias may point
// at content declared outside the anchor, or the content may be written out in
// full at both places.
func Explain(anchor string) string {
	return fmt.Sprintf(
		"anchor &%s is aliased by *%s inside its own content, so the document expands "+
			"without end; point that alias at content declared outside &%s, or write the "+
			"content out in full", anchor, anchor, anchor)
}

// Find reports an anchor that is aliased somewhere inside its own content, and
// whether one exists at all. The returned name is the anchor an author has to
// break.
//
// The walk is over the document's own nodes, following alias links as edges,
// and it colours each node once it is finished. That is what keeps it linear in
// the size of the text: a document may name one anchor from very many places
// without any recursion at all, and its expanded form is then exponentially
// larger than the text — so a check that walked the expansion would exhaust the
// same resources it exists to protect. Only an anchor reached while the walk is
// still inside it counts, which is the difference between refusing recursion
// and refusing depth.
//
// A nil root reports no anchor.
func Find(root *yaml.Node) (anchor string, found bool) {
	inside := map[*yaml.Node]bool{}
	finished := map[*yaml.Node]bool{}

	var walk func(*yaml.Node) (string, bool)
	walk = func(n *yaml.Node) (string, bool) {
		if n == nil || finished[n] {
			return "", false
		}
		if inside[n] {
			// Reached while still descending through it. The anchor is what
			// the author has to break, so it is what gets named; an alias
			// cannot resolve to an unanchored node, and the alias's own text
			// is the same word, so the fallback is only ever a formality.
			if n.Anchor != "" {
				return n.Anchor, true
			}
			return n.Value, true
		}
		inside[n] = true
		if n.Alias != nil {
			if a, ok := walk(n.Alias); ok {
				return a, true
			}
		}
		for _, c := range n.Content {
			if a, ok := walk(c); ok {
				return a, true
			}
		}
		delete(inside, n)
		finished[n] = true
		return "", false
	}
	return walk(root)
}
