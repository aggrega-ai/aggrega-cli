// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// ArtifactAxis enumerates the artifacts one node projects, so a rule declared
// over a single artifact can be invoked once per artifact a build carries.
//
// # The coordinate is the output name, never the authored key
//
// A Recipe's artifacts block is keyed by whatever the author chose, and only one
// of the three ways an entry can be written makes that key the name of an output.
// An entry written inline resolves to its own key. An entry supplied by an
// imported file resolves to that file's base name, which nothing requires to
// match the key it was written under. An entry supplied by a library bundle
// expands into one artifact per file the bundle holds, each named by the bundle's
// own filename — so one authored key becomes many outputs and is itself the name
// of none of them.
//
// Every artifact fact is keyed by the output name, so an axis enumerating
// authored keys would ask those facts about names they hold nothing for. The
// answer would be an empty template and an empty byte string, from which a rule
// collects nothing and raises nothing — and a rule that raises nothing reads
// exactly like a rule that ran and approved. For a bundle every expanded file
// would be missed outright. Enumerating output names is what makes the axis and
// the facts one set.
//
// # It reads what the render recorded, and must not be changed to resolve again
//
// The names come from what the artifacts walk retained as it ran, never from a
// second walk over the authored block. That is deliberate and load-bearing: the
// walk is the one place an import is read from disk and a bundle is expanded, so
// resolving either a second time here would read the same file twice and could
// answer differently each time. Matching the walk by construction is not the same
// as agreeing with it, and only the first survives a change to how an entry
// resolves. An axis rebuilt to re-resolve imports would look simpler and would
// reintroduce exactly that divergence.
type ArtifactAxis struct {
	// names answers one node's output names in the order the walk reached them.
	names func(node string) []string

	// sourceFile labels the file a node's artifact entries were written in.
	//
	// It is supplied rather than read from what the render retained, because it
	// is not one of the facts the retention carries: it positions a finding for
	// an author instead of answering a question a rule asks. An absent label
	// leaves a finding unpositioned rather than mispositioned.
	sourceFile func(node string) string
}

// Artifacts returns the output names of the artifacts one node projects, in the
// order the render walked them — the Recipe's own entry order, and within an
// entry that consumed a library bundle, the order that bundle declares its files.
//
// A node the render did not reach enumerates nothing, which is the same answer a
// node whose Recipe declares no artifacts gives.
func (a *ArtifactAxis) Artifacts(node string) []constraint.AuthoredCoordinate {
	if a == nil || a.names == nil {
		return nil
	}
	names := a.names(node)
	if len(names) == 0 {
		return nil
	}
	var file string
	if a.sourceFile != nil {
		file = a.sourceFile(node)
	}
	out := make([]constraint.AuthoredCoordinate, 0, len(names))
	for _, name := range names {
		out = append(out, constraint.AuthoredCoordinate{Value: name, SourceFile: file})
	}
	return out
}

// HydratedArtifactAxis enumerates every artifact whose text the render recorded
// before evaluating it — the set a Recipe's own entries resolved to.
//
// This is the axis for a rule that admits or refuses what an author wrote. Such a
// rule has to be invoked for an entry whose bytes are never produced, because an
// entry it refuses is exactly one that renders to nothing: an axis built from
// what a render successfully projected could not present it.
//
// sourceFile labels the file a node's entries were written in, for positioning a
// finding; it may be nil.
//
// It returns nil when the render retained no such state, so a caller checks for
// nil rather than holding an axis that enumerates nothing for every node. An axis
// answering emptily would leave every rule invoked over it silent, and silence is
// indistinguishable from approval.
func (r *RenderResult) HydratedArtifactAxis(sourceFile func(node string) string) *ArtifactAxis {
	if r == nil || r.hydrated == nil {
		return nil
	}
	retention := r.hydrated
	return &ArtifactAxis{
		names:      retention.artifactNames,
		sourceFile: sourceFile,
	}
}

// RenderedArtifactAxis enumerates the artifacts a render actually projected.
//
// It is a narrower set than the one above and the difference is the point of
// having both: an entry that evaluates to nothing is skipped rather than emitted,
// and an entry refused before evaluation never reaches this set at all. So this is
// the axis for a rule about what a build produced, where presenting an entry whose
// bytes were never written would have the rule ask about content that does not
// exist. Neither axis is a copy of the other and unifying them would silently
// widen one rule's reach or narrow the other's.
//
// It delegates to the fact naming that set rather than reading the retention
// beside it, so a rule is invoked for exactly the artifacts it can then read.
// Two readings of one set are free to disagree with nothing comparing them; one
// reading cannot.
//
// sourceFile labels the file a node's entries were written in, for positioning a
// finding; it may be nil.
//
// It returns nil when the render published no presentation to read that fact
// from, for the reason the sibling above returns nil.
func (r *RenderResult) RenderedArtifactAxis(sourceFile func(node string) string) *ArtifactAxis {
	if r == nil || r.RenderedView == nil {
		return nil
	}
	view := r.RenderedView
	return &ArtifactAxis{
		names:      view.ArtifactProjectionNameSet,
		sourceFile: sourceFile,
	}
}

var _ constraint.ArtifactEnumerator = (*ArtifactAxis)(nil)
