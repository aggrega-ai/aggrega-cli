// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package sourceref owns the single rule for reading a Recipe's source
// reference as local or remote, and the single statement of what a local one is
// resolved against. It is a stdlib-only leaf, and that is what makes it the one
// home reachable from every side that asks: the render imports the dependency
// descent and the catalog load precedes both, so a rule living inside any one of
// them would be reachable from that one and re-implemented in the others.
//
// The locality rule is a heuristic over the reference's own text, and the whole
// of what it reads is that text: a reference carrying a recognised transport
// scheme prefix, or naming a repository, is fetched; anything else names a path
// the workspace already holds. Nothing outside the string decides it — no entity,
// no values, no infrastructure, no filesystem — which is why the rule can sit
// this low.
//
// The distinction is load-bearing rather than cosmetic: a reference the workspace
// already holds is never pinned in a Release's lockfile, so both the pass that
// writes the lockfile and the pass that renders against one have to draw the line
// in the same place. Two copies drawing it identically today would be exactly what
// diverges the next time a transport is added.
//
// # Which value the resolution is owed, and why that decides where it runs
//
// A reference written as a template names nothing until it has been evaluated, so
// the directory it is relative to cannot be joined to it while it is still text:
// joining first resolves the template's spelling and leaves the value the author
// actually wrote unresolved. Whether a reference carries a template is therefore
// read here too, beside the locality rule, because the two together decide the
// one question — resolve this reference now, or resolve what it produces later —
// and a surface that could answer one without the other would answer it wrongly
// for exactly the references that differ.
package sourceref

import (
	"path/filepath"
	"strings"
)

// templateOpen is the opening delimiter a source template is written with. The
// source surface fixes the pair rather than inheriting a configurable one, so a
// single spelling decides every reading of whether a reference is still text.
const templateOpen = "{{"

// IsLocal reports whether a source value names a reference the workspace already
// holds rather than one fetched over a transport.
//
// It accepts the two shapes a source field takes. A scalar reference is read
// directly. A structured source is read through its url entry, which is the field
// naming what would be fetched, so a structured source carrying no url — or one
// whose url is not text — reaches the rule with nothing and is not local, the same
// answer a scalar naming nothing gets. Any other shape names nothing this rule can
// place and is not local either.
func IsLocal(source any) bool {
	switch s := source.(type) {
	case string:
		return IsLocalURL(s)
	case map[string]any:
		url, _ := s["url"].(string)
		return IsLocalURL(url)
	default:
		return false
	}
}

// IsLocalURL reports whether a single source reference names something the
// workspace already holds.
//
// An empty reference is NOT local. It names nothing at all, which is a different
// answer from naming something near: reporting it local would hand the empty
// string onward as though it were a path, and the passes that ask this question
// treat a local reference as one they may use verbatim.
func IsLocalURL(ref string) bool {
	if ref == "" {
		return false
	}
	switch {
	case strings.HasPrefix(ref, "oci://"),
		strings.HasPrefix(ref, "https://"),
		strings.HasPrefix(ref, "http://"),
		strings.HasPrefix(ref, "git+ssh://"),
		strings.HasPrefix(ref, "git://"),
		strings.HasPrefix(ref, "ssh://"),
		strings.HasSuffix(ref, ".git"):
		return false
	default:
		return true
	}
}

// IsTemplated reports whether a source reference is still template text rather
// than a value.
//
// It is the question that decides WHEN a local reference is anchored, not
// whether: a reference carrying a template action names nothing yet, so the
// directory it is relative to belongs to the value it produces rather than to the
// spelling that produces it. Anchoring the spelling reads as harmless whenever
// the value happens to be relative, because a directory joined to a relative path
// is the same directory either way — and it silently mangles every value that is
// not, since an absolute path, a transport reference and an empty result are each
// decided by text the anchoring has already prefixed by the time they exist.
//
// The delimiter is the one the source surface evaluates with, so this reading and
// that evaluation cannot come to disagree about which references are text.
func IsTemplated(ref string) bool {
	return strings.Contains(ref, templateOpen)
}

// AnchorToDocument resolves a source reference against the directory of the
// document that declared it, and is the one statement of what a relative source
// is relative TO.
//
// Four references come back exactly as given, and each for its own reason. An
// empty documentPath names no declaring document — an entity resolved from a
// registry lives in a content-addressed slot rather than in a file anybody
// authored, so there is no directory for its reference to be relative to and
// handing the reference back is the honest answer. A reference that is not local
// names something fetched over a transport, and joining it to a directory would
// destroy the reference while leaving a value every later pass reads as local. An
// empty reference names nothing at all, which the locality reading already
// answers. An absolute reference already names where it is.
//
// Everything else is a path the author wrote beside their own document, and it
// reads the same whatever directory the build runs from.
//
// It takes the document rather than the directory so that "the directory that
// declared it" is derived once here instead of at each call site, where a second
// spelling would be free to differ.
func AnchorToDocument(documentPath, ref string) (string, error) {
	if documentPath == "" || !IsLocalURL(ref) || filepath.IsAbs(ref) {
		return ref, nil
	}
	return filepath.Abs(filepath.Join(filepath.Dir(documentPath), ref))
}
