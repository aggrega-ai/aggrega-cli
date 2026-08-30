// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import "strings"

// PillarRootValues and PillarRootInfra are the prefixes a pillar-leaf
// coordinate leads with: the pillar's own authored name, then the separator. A
// coordinate is one of these roots followed by the flat-dotted path
// FlattenLeaves produces for the datum, so "values.credentials.password" names
// the leaf an author wrote at credentials.password inside values.
//
// They are declared beside the flattener that produces the rest of the
// coordinate, and beside SplitPillarCoordinate which reads one back, so a
// surface that writes a coordinate and a surface that parses one cannot come to
// spell it differently. A surface holding its own copy of the prefix would
// agree on the day it was written, and that agreement is what would hide a
// later divergence.
const (
	PillarRootValues = "values."
	PillarRootInfra  = "infra."
)

// SplitPillarCoordinate splits a pillar-leaf coordinate into the pillar's name
// and the flat-dotted path beneath it. It reports false for a string that is
// not a coordinate of a pillar this spelling covers — including the bare pillar
// name with no path under it, which names no leaf.
//
// Only values and infra are covered, because those are the two pillars whose
// data a node authors and a display shows under this spelling. A provides key
// belongs to the entity that publishes it and is addressed through the edge
// that carries it, not through a coordinate on the consumer.
func SplitPillarCoordinate(coordinate string) (pillar, path string, ok bool) {
	if rest, cut := strings.CutPrefix(coordinate, PillarRootValues); cut && rest != "" {
		return strings.TrimSuffix(PillarRootValues, "."), rest, true
	}
	if rest, cut := strings.CutPrefix(coordinate, PillarRootInfra); cut && rest != "" {
		return strings.TrimSuffix(PillarRootInfra, "."), rest, true
	}
	return "", "", false
}

// PathCoveredBy reports whether a flat-dotted leaf path is covered by a set of
// declared paths: it is covered when the set names the path itself, or names
// any object that contains it.
//
// The containing objects are walked as well as the leaf, and that walk is the
// whole content of this rule. A declaration may land on an interior property
// whose children hold the authored data — a declaration at credentials above an
// authored credentials.password — and matching only the exact path would leave
// those children uncovered while the parent read as covered. A leaf beneath a
// declared object is named by that declaration, so covering it claims nothing
// the author did not write.
//
// Matching is segment-wise rather than textual: dbPasswordExtra is not covered
// by dbPassword, because the walk removes whole segments rather than trimming
// characters.
//
// The path spelling is FlattenLeaves', which is the spelling every declared set
// in this module is keyed by; a mismatch there would make a nested declaration
// unfindable from a leaf path. An empty path names no leaf and is covered by
// nothing. The set is walked to a boolean and never iterated for emission, so
// nothing about its order can reach a rendered byte.
func PathCoveredBy(path string, declared map[string]bool) bool {
	if len(declared) == 0 || path == "" {
		return false
	}
	for p := path; p != ""; p = containingPath(p) {
		if declared[p] {
			return true
		}
	}
	return false
}

// containingPath returns the dotted path of the object containing the named
// leaf, and the empty string when the leaf sits at the root.
func containingPath(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return path[:i]
}
