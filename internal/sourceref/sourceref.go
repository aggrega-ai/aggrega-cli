// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package sourceref owns the single rule for reading a Recipe's source
// reference as local or remote. It is a stdlib-only leaf, and that is what makes
// it the one home reachable from both sides that ask: the render imports the
// dependency descent, so a rule living inside either package would be reachable
// from one of them and re-implemented in the other.
//
// The rule is a heuristic over the reference's own text, and the whole of what it
// reads is that text: a reference carrying a recognised transport scheme prefix,
// or naming a repository, is fetched; anything else names a path the workspace
// already holds. Nothing outside the string decides it — no entity, no values, no
// infrastructure, no filesystem — which is why the rule can sit this low.
//
// The distinction is load-bearing rather than cosmetic: a reference the workspace
// already holds is never pinned in a Release's lockfile, so both the pass that
// writes the lockfile and the pass that renders against one have to draw the line
// in the same place. Two copies drawing it identically today would be exactly what
// diverges the next time a transport is added.
package sourceref

import "strings"

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
