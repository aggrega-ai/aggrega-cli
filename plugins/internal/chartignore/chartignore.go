// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package chartignore is the chart ecosystem's declaration of the files that
// never ship beside a chart, shared by the two guests that read a chart source —
// the one rendering it into manifests and the one redistributing it — so both
// declare one set and a chart travels the same way whichever of them serves it.
//
// It is data and nothing else: the host composes it with the author's own ignore
// file when an entity is pinned, records the result, and matches it; no guest
// applies it itself.
package chartignore

// Patterns are the chart ecosystem's own ignore patterns, scoped to the root of
// the chart source: the version-control directories a scaffolded chart's own
// ignore file lists, and the hidden files under the chart's templates/ directory,
// which the chart loader skips before reading any ignore file.
func Patterns() []string {
	return []string{".git/", ".hg/", ".svn/", ".bzr/", "templates/.?*"}
}

// Files names the chart ecosystem's own ignore file, whose lines a chart author
// writes to leave files out of a packaged chart. Wherever a source holds one, its
// lines refine the patterns above for the directory holding it.
func Files() []string {
	return []string{".helmignore"}
}
