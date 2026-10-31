// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ReleaseEdgeConfigurations folds the entries a Release's per-edge `uses`
// surface writes into the shape a traversal over them enumerates: each entry's
// coordinates, the site it was written at, and the file it was written in.
//
// It is the one fold of that surface, and every stage enumerating it reads it
// here — the reading of the two authoring documents, where a rule over what an
// entry writes is asked once its facts are complete, and the composition, where
// a rule needing the flattened set is asked. One fold is what lets a finding
// raised at either stage name the same entry the same way: the boundary it sits
// in and the keys the author typed to reach the component.
//
// The walk is [ReleaseEdgeEntries], which is what keeps this axis and the guards
// over the same surface agreeing on which entries a Release declares and in what
// order. It supplies the boundary each entry sits in and the keys reaching the
// consuming component, so nothing here re-derives an authoring site the walk
// already settled.
//
// An entry in the boundary normalization synthesized names the top-level shape
// its author wrote: that boundary spells no level, so the keys relative to it
// have nothing to be relative to and the component travels on the site instead —
// the same division the boundary-component axis draws. Such a boundary holds no
// routing entries, so its entries are always the direct ones.
//
// sourceFile is the workspace-relative Release document, carried on every entry
// so a finding about one names the file it is a path inside; it is empty where
// the caller holds no file for the Release, and every entry then carries none.
func ReleaseEdgeConfigurations(
	rel *types.Release,
	sourceFile string,
) []constraint.ReleaseEdgeConfigurationInstance {
	entries := ReleaseEdgeEntries(rel)
	if len(entries) == 0 {
		return nil
	}
	out := make([]constraint.ReleaseEdgeConfigurationInstance, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Instance(sourceFile))
	}
	return out
}

// Instance is the entry in the shape a traversal over the Release's per-edge
// surface enumerates it, through the one spelling of its coordinates and site —
// so a finding about the entry raised outside a rule names the entry exactly as
// a rule over it does.
func (e ReleaseEdgeEntry) Instance(sourceFile string) constraint.ReleaseEdgeConfigurationInstance {
	return releaseEdgeConfigurationInstance(
		e.NodeID, e.BoundaryScopePath, e.ComponentKeys, e.Socket, e.Alias, sourceFile)
}

// Site returns where the Release wrote the component entry: the site, and the
// keys reaching the entry's own block from it — the same division a per-edge
// entry under it takes, so the component entry and an entry written inside it
// are located by one rule.
func (c ReleaseComponentEntry) Site() (constraint.AuthoredSite, []string) {
	return releaseComponentEntrySite(c.NodeID, c.BoundaryScopePath, c.ComponentKeys)
}

// releaseComponentEntrySite is the one spelling of where a Release component
// entry sits: in the boundary normalization synthesized, the component on the
// site itself, since that boundary spells no level the keys could be relative
// to; in every other boundary, the boundary on the site and the keys reaching the
// entry after it.
func releaseComponentEntrySite(
	node, boundaryScopePath string,
	componentKeys []string,
) (constraint.AuthoredSite, []string) {
	if boundaryScopePath == "" {
		return constraint.AuthoredSite{Component: node}, nil
	}
	return constraint.AuthoredSite{ScopePath: boundaryScopePath}, componentKeys
}

// ReleaseConnectionEntry returns the per-edge entry a Release writes, or would
// write, for one connection of one component: the coordinates and the site the
// fold above gives an entry the Release does write, built from the Release's own
// component entry for the node. The second return is false where the Release
// writes no component entry for the node at all, so there is no entry of its own
// for a per-edge one to sit under.
//
// It answers whether or not the Release writes an entry for that connection yet,
// and that is its point: where a new per-edge entry belongs is a fact about the
// component entry it goes under, and it is spelled exactly as the entry would be
// once written, so a reader placing one and a rule later judging it name the same
// keys.
func ReleaseConnectionEntry(
	rel *types.Release,
	sourceFile, node, socket, alias string,
) (constraint.ReleaseEdgeConfigurationInstance, bool) {
	for _, c := range ReleaseComponentEntries(rel) {
		if c.NodeID != node {
			continue
		}
		return releaseEdgeConfigurationInstance(
			c.NodeID, c.BoundaryScopePath, c.ComponentKeys, socket, alias, sourceFile), true
	}
	return constraint.ReleaseEdgeConfigurationInstance{}, false
}

// releaseEdgeConfigurationInstance is the one spelling of a per-edge entry's
// coordinates and site, for an entry written and for one about to be.
func releaseEdgeConfigurationInstance(
	node, boundaryScopePath string,
	componentKeys []string,
	socket, alias, sourceFile string,
) constraint.ReleaseEdgeConfigurationInstance {
	instance := constraint.ReleaseEdgeConfigurationInstance{
		Configuration: constraint.ReleaseEdgeConfiguration{
			Document:     constraint.AuthoringDocumentRelease,
			BoundaryPath: boundaryScopePath,
			Node:         node,
			Socket:       socket,
			Alias:        alias,
		},
		SourceFile: sourceFile,
	}
	instance.Site, instance.ComponentKeys = releaseComponentEntrySite(node, boundaryScopePath, componentKeys)
	return instance
}
