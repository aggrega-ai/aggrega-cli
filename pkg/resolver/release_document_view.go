// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// releaseDocumentView presents one authored Release to a check as named facts:
// everything its boundaries place, with the coordinates that identify each
// placement.
//
// It is the Release's counterpart to [aggregateDocumentView] and
// [manifestDocumentView] and is deliberately the same shape rather than a shape
// of its own: all three satisfy the one generated [constraint.DocumentView], and
// all three take the facts their own document cannot carry from the same
// refusing base. What holds this type to the facts a Release itself carries is
// the obligation asserted at the bottom of this file: a Release fact added to the
// registry stops this type compiling, and a fact carried by any other kind does
// not reach it.
//
// It also supplies the placement axis a traversal walks, so the entry a rule is
// invoked for and the set the fact reports are projected from one fold rather
// than derived twice. Reading either from the Release beside it would be a second
// reconciliation of the two axes a boundary names components on, free to disagree
// with the one the other side took.
//
// Unlike its two neighbours it presents its placement fact with no document key,
// so it has no counterpart to their refusal of an accessor naming another
// document. That fact is the axis itself rather than a lookup within it — the
// same shape the Release's declared-reference fact takes — so there is no key to
// check a caller's argument against. The chain facts a Release also carries do
// take a document key and are refused outright below, so no key reaches a
// comparison there either.
type releaseDocumentView struct {
	// ReleaseDocumentBase supplies the document-stage facts a Release does not
	// carry, and declares nothing for the ones it does — so this type writes
	// bodies for exactly the Release's own facts.
	constraint.ReleaseDocumentBase

	// placements is the fold this value took once, projected by both the fact
	// and the enumerator below.
	placements []constraint.BoundaryPlacementInstance
}

// newReleaseDocumentView presents the Release a caller is holding: the file it
// was read from, and the document itself.
//
// The fold happens here rather than at each accessor because a view projects
// facts already produced: two accessors folding independently would be two
// reconciliations of one document, and the entry a rule is invoked for could then
// differ from the set that rule reads back.
func newReleaseDocumentView(path string, rel *types.Release) releaseDocumentView {
	return releaseDocumentView{placements: boundaryPlacements(path, rel)}
}

// ReleaseBoundaryPlacements returns everything the presented Release's boundaries
// place, as the coordinates identifying each placement, in the order the document
// declares them.
//
// It hands back the coordinates of the same entries the enumerator beside it
// yields, projected from the one fold this value took rather than derived a
// second time, so a rule invoked for an entry and the set it reads back cannot
// come to disagree.
func (v releaseDocumentView) ReleaseBoundaryPlacements() []constraint.BoundaryPlacement {
	if len(v.placements) == 0 {
		return nil
	}
	out := make([]constraint.BoundaryPlacement, 0, len(v.placements))
	for _, placement := range v.placements {
		out = append(out, placement.Placement)
	}
	return out
}

// BoundaryPlacements returns every placement the presented Release's boundaries
// declare, each carrying the site a locator is built from, in the order the
// document declares them.
func (v releaseDocumentView) BoundaryPlacements() []constraint.BoundaryPlacementInstance {
	return v.placements
}

// ReleaseExtendsChainCycleEdge refuses: this view presents one Release document,
// and the fact is read off a walk of the chain that document heads.
//
// Both chain facts refuse here rather than being answered from the document, and
// the ground is that the answer is not in the document. The reference a Release
// declares is one string; whether following it returns to a file already in the
// chain, and how many files the chain reaches, are properties of the walk that
// follows it — and the walk resolves each reference against the Release's own
// directory, which this presentation neither holds nor may reach. Answering from
// the document would report an absence the chain does not have, and a rule cannot
// tell that from a Release that heads no chain at all.
//
// Which presentation does answer them is named rather than left to a reader: the
// walk builds its own, beside the rules it invokes.
func (v releaseDocumentView) ReleaseExtendsChainCycleEdge(
	document string,
) (constraint.ExtendsChainCycleEdge, bool) {
	panic(v.refuseChainFact("ReleaseExtendsChainCycleEdge"))
}

// ReleaseExtendsChainLength refuses, for the reason
// [releaseDocumentView.ReleaseExtendsChainCycleEdge] states.
func (v releaseDocumentView) ReleaseExtendsChainLength(document string) (int, bool) {
	panic(v.refuseChainFact("ReleaseExtendsChainLength"))
}

// ReleaseExtendsChainBoundariesDeclaredBy refuses, for the reason
// [releaseDocumentView.ReleaseExtendsChainCycleEdge] states.
//
// This one would be the easiest of the four to answer wrongly, because the
// document in hand does carry a boundaries block and reading it would produce an
// answer. That answer would be about one Release while the fact is about a
// chain, so a rule reading it would clear a composition it never saw.
func (v releaseDocumentView) ReleaseExtendsChainBoundariesDeclaredBy(
	document string,
) (string, bool) {
	panic(v.refuseChainFact("ReleaseExtendsChainBoundariesDeclaredBy"))
}

// ReleaseExtendsChainComponentsDeclaredBy refuses, for the reason
// [releaseDocumentView.ReleaseExtendsChainBoundariesDeclaredBy] states.
func (v releaseDocumentView) ReleaseExtendsChainComponentsDeclaredBy(
	document string,
) (string, bool) {
	panic(v.refuseChainFact("ReleaseExtendsChainComponentsDeclaredBy"))
}

// ReleaseExtendsChainInheritedLiteralDirectories refuses, for the reason
// [releaseDocumentView.ReleaseExtendsChainCycleEdge] states.
//
// Whether a directory was inherited is not a property of the document that ends
// up carrying it. One Release's boundaries block reads the same whether its
// directories were written there or composed in from the Release it extends, so
// an answer from this presentation would report every directory as authored.
func (v releaseDocumentView) ReleaseExtendsChainInheritedLiteralDirectories(
	document string,
) []string {
	panic(v.refuseChainFact("ReleaseExtendsChainInheritedLiteralDirectories"))
}

// refuseChainFact is what this view says about a chain fact, which it was not
// built to answer.
func (v releaseDocumentView) refuseChainFact(accessor string) string {
	return fmt.Sprintf(
		"releaseDocumentView.%s asked of a view built from one Release document: the fact is "+
			"read from a walk of the chain that document heads, so present that walk",
		accessor)
}

// releaseRoutingKey is the authoring key a Release writes a routing entry under.
//
// It is spelled once, here, because it is used from both directions: the
// reconciliation that resolves a boundary's two component axes puts this key at
// the head of every locator it produces for a routed entry, so reading it back is
// how the walk below learns which axis a declaration came from — and the module
// placements that walk adds are located under the same key. One spelling serving
// both means an axis read off a locator cannot disagree with a locator built
// beside it.
const releaseRoutingKey = "aggregates"

// boundaryPlacements folds everything a Release's boundaries place into the shape
// a traversal over placements enumerates: the boundary's own component entries
// and the components its routing entries name, then the sub-modules it routes,
// then the boundaries nested inside it.
//
// The order is the document's own at every level, so a rule invoked once per
// placement reports in the order an author reads the document — and so the
// placement a conflict names as the earlier one is the earlier one in the file
// rather than whichever a map happened to yield first.
//
// It walks the NORMALIZED Release, so a document that declares its components
// at the top level rather than under named boundaries is covered by the same
// walk: normalization gives such a document a boundary of its own holding exactly
// those entries, and that boundary contributes no scope level, so its entries are
// named at the top-level shape their author wrote.
//
// The two axes a boundary names components on are reconciled by the one traversal
// that reconciles them, which is what supplies both the id an entry addresses and
// the keys the author typed to write it. Reading either map directly would be a
// second reconciliation, free to disagree with the one the flattening itself
// takes — and a reader that took a routing entry's local name for an id would
// report every module-routed Release as placing components that do not exist.
//
// Routed sub-modules are collected from the reading that reports every prefix an
// entry implies, including the modules nested inside it, because routing a module
// into a boundary places every component it contains: a routing entry naming no
// component at all is the shape that carries a whole module in, and it is the
// only record of a placement whose components cannot be enumerated until that
// module's own document is loaded.
func boundaryPlacements(path string, rel *types.Release) []constraint.BoundaryPlacementInstance {
	normalized := types.NormalizeRelease(rel).Release()
	if normalized == nil {
		return nil
	}
	var out []constraint.BoundaryPlacementInstance
	var walk func(scopePath string, b types.Boundary)
	walk = func(scopePath string, b types.Boundary) {
		for _, decl := range types.BoundaryComponentDeclarations(b) {
			out = append(out, componentPlacement(path, scopePath, decl))
		}
		for _, module := range types.BoundaryRoutedModules(b) {
			out = append(out, modulePlacement(path, scopePath, module))
		}
		for name, child := range b.Boundaries.All() {
			walk(constraint.JoinBoundaryScope(scopePath, name, child.Synthesized), child)
		}
	}
	for name, b := range normalized.Spec.Boundaries.All() {
		walk(constraint.JoinBoundaryScope("", name, b.Synthesized), b)
	}
	return out
}

// componentPlacement presents one component declaration as the placement it is
// and the site a locator is built from.
//
// The axis is read off the locator the reconciliation produced rather than
// re-derived from the shape of the id, which is a question the reconciliation has
// already answered and the id cannot: a boundary's own component block admits a
// dotted qualified id, so a name alone says nothing about which block it was
// written in.
//
// The ordinary shape is the second one, and it covers both component axes without
// telling them apart in the locator: the reconciliation reports the keys the
// author typed relative to the boundary, so a boundary's own entry contributes
// its component container and its key while a routing entry contributes the
// routing container, the module, the component container and the local name.
//
// The first shape exists for the one boundary an author did not write. A
// Release that names no boundaries has its entries folded into a boundary
// normalization synthesized, which spells no level, so the keys relative to it
// have nothing to be relative to and naming them alone would produce a path the
// document does not contain. Those entries are named at the top-level site the
// author did write instead. Such a boundary holds no routing entries at all — it
// is built from the Release's own component surface — so its declarations are
// the direct ones and the component travels on the site.
func componentPlacement(
	path string,
	scopePath string,
	decl types.BoundaryComponentDeclaration,
) constraint.BoundaryPlacementInstance {
	axis := constraint.PlacementNamedComponent
	if len(decl.Locator) > 0 && decl.Locator[0] == releaseRoutingKey {
		axis = constraint.PlacementRoutedComponent
	}
	placement := constraint.BoundaryPlacement{
		Document:     constraint.AuthoringDocumentRelease,
		BoundaryPath: scopePath,
		Reference:    decl.ID,
		Axis:         axis,
	}
	if scopePath == "" {
		return constraint.BoundaryPlacementInstance{
			Placement:  placement,
			Site:       constraint.AuthoredSite{Component: decl.ID},
			SourceFile: path,
		}
	}
	return constraint.BoundaryPlacementInstance{
		Placement:  placement,
		Site:       constraint.AuthoredSite{ScopePath: scopePath},
		RoutedKeys: decl.Locator,
		SourceFile: path,
	}
}

// modulePlacement presents one routed sub-module as the placement it is and the
// site a locator is built from.
//
// The keys are the routing entries reaching the module, one routing container and
// one name per level of its dotted prefix, because a nested module is written
// inside the routing entry of the module that holds it. The prefix itself is not
// a key any file contains, so a locator spelling it as one segment would name a
// key that is not there.
//
// A module placement always sits inside a boundary the author named: the boundary
// normalization synthesizes is built from the Release's own component surface
// and holds no routing entries at all. The empty-scope arm is therefore absent
// here rather than omitted, and the locator assembly's own rule covers it anyway —
// a scope it cannot name yields no segments rather than a rootless path.
func modulePlacement(
	path string,
	scopePath string,
	module string,
) constraint.BoundaryPlacementInstance {
	return constraint.BoundaryPlacementInstance{
		Placement: constraint.BoundaryPlacement{
			Document:     constraint.AuthoringDocumentRelease,
			BoundaryPath: scopePath,
			Reference:    module,
			Axis:         constraint.PlacementRoutedModule,
		},
		Site:       constraint.AuthoredSite{ScopePath: scopePath},
		RoutedKeys: routedModuleKeys(module),
		SourceFile: path,
	}
}

// routedModuleKeys renders the authoring key path of a routed sub-module relative
// to the boundary that routes it: one routing container and one name for each
// level of its dotted prefix, because each level is an entry inside the routing
// entry of the module above it.
//
// The prefix is split on the separator the composition joins module levels with,
// which is the same separator [types.JoinQualifiedID] composes one from — the
// reading is the writing's inverse and neither is free to pick another character.
func routedModuleKeys(module string) []string {
	segments := strings.Split(module, moduleLevelSeparator)
	keys := make([]string, 0, len(segments)*2)
	for _, segment := range segments {
		keys = append(keys, releaseRoutingKey, segment)
	}
	return keys
}

// moduleLevelSeparator is what a sub-module's dotted prefix joins its levels
// with, which is what a reading of that prefix splits on.
const moduleLevelSeparator = "."

// The compiler holds the presentation to the contract rather than a comment
// claiming it: a signature drifting from the declared facts stops compiling here.
//
// The per-kind assertion is the half the embedded base cannot supply. Embedding
// makes every document-stage fact compile, so the view assertion alone would
// accept a presentation answering none of them; naming the Release's own
// obligation is what makes a Release fact added to the registry fail here, and on
// no other presentation.
var (
	_ constraint.DocumentView                = releaseDocumentView{}
	_ constraint.ReleaseDocumentFacts        = releaseDocumentView{}
	_ constraint.BoundaryPlacementEnumerator = releaseDocumentView{}
)
