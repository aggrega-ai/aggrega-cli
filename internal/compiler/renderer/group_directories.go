// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// groupDirectory is what one dispatch group received from the path declarations
// that sent contributions to it, and which of its contributors it places under a
// directory of their own.
type groupDirectory struct {
	// pathDeclarations is what each contribution's boundary path declaration
	// evaluated to, deduplicated, in the order the group first received each.
	pathDeclarations []constraint.BoundaryPathDeclaration

	// contributorDirectories are the contributors the group places under a
	// directory of their own, in sorted key order, each with its segment.
	contributorDirectories []constraint.ContributorDirectory
}

// groupDirectories is the render's retained record of what each dispatch group's
// path declarations evaluated to and which of its contributors it places under a
// directory of their own.
//
// It is a retention rather than a read of the render's working state because that
// state is a loop-local: what each group received is dropped when the render
// returns, and the presentation of the grouping outlives it. A reader asking what
// a group's declarations evaluated to has this and nothing else.
//
// It is keyed by the group rather than by a boundary or a node, because the group
// is what both questions are asked of: a sub-directory is evaluated once for each
// contribution, a boundary-scope attachment's contribution is evaluated as well as
// each component's, and which contributors receive a directory of their own
// depends on how the group as a whole is laid out.
type groupDirectories struct {
	// byGroup is looked up by group and never walked, so it holds no order to
	// keep: every reader arrives with a path and a provider in hand. The entries
	// inside each record carry their own order.
	byGroup map[dispatchKey]groupDirectory
}

// newGroupDirectories records, for each group, what its path declarations
// evaluated to and which of its contributors it places under a directory of
// their own, at the point every contributor has been sorted into its group.
//
// It is taken here because this is the first point both are complete and the last
// point at which acting on them still saves work: no group has been bound to an
// assembler, and nothing has been written. The layout read is the one the group's
// finalization reads ([contributorsGivenADirectory]), and each segment is the one
// that finalization composes ([nodeOutputSegment]), so the rules reading this
// record read exactly the directories the build would create.
//
// release is the name of the Release the groups' boundaries are declared in, which
// a finding about a declaration or a contributor names.
func newGroupDirectories(
	groups map[dispatchKey]*pendingDispatch,
	keys []dispatchKey,
	release string,
) *groupDirectories {
	out := &groupDirectories{byGroup: make(map[dispatchKey]groupDirectory, len(keys))}
	for _, key := range keys {
		group := groups[key]
		if group == nil {
			continue
		}
		var record groupDirectory
		for _, d := range group.pathDeclarations {
			record.pathDeclarations = append(record.pathDeclarations, constraint.BoundaryPathDeclaration{
				Release:      release,
				Boundary:     d.boundary,
				Directory:    d.directory,
				SubDirectory: d.subDirectory,
			})
		}
		for _, nodeID := range contributorsGivenADirectory(group, key.Path) {
			producer := group.producerOf(nodeID)
			record.contributorDirectories = append(record.contributorDirectories, constraint.ContributorDirectory{
				Release:          release,
				Contributor:      producer.name,
				ContributorKind:  producer.kind,
				ContributorScope: producer.scope,
				Segment:          nodeOutputSegment(producer),
			})
		}
		out.byGroup[key] = record
	}
	return out
}

// groupBoundaryPathDeclarations returns what each contribution's boundary path
// declaration evaluated to for one dispatch group, in the order the group first
// received each.
//
// It is unexported for the reason the group's other readers are: its only reader
// is in this package — the presentation of what the grouping holds. A render that
// recorded nothing answers nothing, which is the same answer a group nothing was
// recorded for gets.
func (r *RenderResult) groupBoundaryPathDeclarations(
	assemblyPath, provider string,
) []constraint.BoundaryPathDeclaration {
	if r.directories == nil {
		return nil
	}
	return r.directories.byGroup[dispatchKey{Path: assemblyPath, Provider: provider}].pathDeclarations
}

// groupContributorDirectories returns the contributors one dispatch group places
// under a directory of their own, in sorted key order, each with its segment.
//
// A render that recorded nothing answers nothing, which is the same answer a group
// placing no contributor under a directory of its own gets.
func (r *RenderResult) groupContributorDirectories(
	assemblyPath, provider string,
) []constraint.ContributorDirectory {
	if r.directories == nil {
		return nil
	}
	return r.directories.byGroup[dispatchKey{Path: assemblyPath, Provider: provider}].contributorDirectories
}
