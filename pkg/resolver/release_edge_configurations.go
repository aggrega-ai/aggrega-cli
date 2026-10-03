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
		instance := constraint.ReleaseEdgeConfigurationInstance{
			Configuration: constraint.ReleaseEdgeConfiguration{
				Document:     constraint.AuthoringDocumentRelease,
				BoundaryPath: e.BoundaryScopePath,
				Node:         e.NodeID,
				Socket:       e.Socket,
				Alias:        e.Alias,
			},
			SourceFile: sourceFile,
		}
		if e.BoundaryScopePath == "" {
			instance.Site = constraint.AuthoredSite{Component: e.NodeID}
		} else {
			instance.Site = constraint.AuthoredSite{ScopePath: e.BoundaryScopePath}
			instance.ComponentKeys = e.ComponentKeys
		}
		out = append(out, instance)
	}
	return out
}
