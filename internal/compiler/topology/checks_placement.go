// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// subModuleIsPlaced reports each sub-module the composition brought in whose
// components no boundary holds.
//
// A boundary holds a component two ways and the rule reads both, because either
// one places it: the Release names the component under the id it carries once
// the composition is flattened, or it routes the module in and places every
// component that module contains. The rule therefore fires on placement rather
// than on a spelling — a module whose components are all named individually is
// placed, and reporting it because no routing entry mentions the module would
// refuse an authoring form the language admits and the documentation teaches.
//
// It is keyed per module and not per component. One module placed nowhere is one
// authoring fault with one remedy at one key, so reporting it once names the site
// the author edits; reporting it per component would report one fault many times
// and would name components whose own declarations are correct.
//
// A module contributing no component passes: there is nothing to place, and a
// module a false activation toggle removed contributed nothing because it was
// never composed. A module whose components are only partly placed passes too —
// the Release placed it, and which of its components carry their own entries
// is a separate question this rule does not ask.
//
// The detail is the module's dotted prefix.
var subModuleIsPlaced = constraint.Check[
	constraint.TopologyView,
	constraint.ModuleSubject,
]{
	Sentinel: ErrUnplacedSubModule(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.ModuleSubject,
	) []constraint.Objection {
		contributed := v.ModuleContributedNodes(s.ModulePath())
		if len(contributed) == 0 {
			return nil
		}
		for _, node := range contributed {
			if _, placed := v.NodeBoundaryPlacement(node); placed {
				return nil
			}
		}
		return []constraint.Objection{
			s.Raise(ErrUnplacedSubModule(), s.ModulePath()),
		}
	},
}

// componentIsPlaced reports each component the composition holds that no
// boundary placed.
//
// It reads placement rather than a spelling, exactly as the module rule does: a
// component named under a boundary's own `components` and a component reached by
// routing its module are both placed, and the fact it reads has already
// reconciled the two. So the rule fires only on a component nothing placed by
// either route, which is the one shape that renders outside every boundary the
// Release declared.
//
// It passes a component of a sub-module the Release placed nowhere, and that
// exclusion is what keeps it off the ground the module rule owns. Such a module
// is one authoring fault with one remedy at one key, and the module rule reports
// it once there; reporting it again per component would report one fault many
// times and would name components whose own declarations are correct.
//
// The exclusion is narrow rather than total, and the difference is a whole case
// that would otherwise reach a reader from nothing at all. A module the
// Release placed only in part is placed, so the module rule passes it by
// design; the components of it the Release did not name are then this rule's,
// and each is reported at its own key. An exclusion keyed on "some module
// contributed this" rather than on "no boundary placed that module" would pass
// those components too, and they would render outside every boundary the
// Release declared with nothing saying so — which is the silent build both
// rules exist to end. Dropping the exclusion outright would trade that gap for
// the noise it was written to prevent, so it is narrowed and not removed.
//
// The module's verdict is read here from the facts rather than taken from the
// module rule's answer, because a check reads facts and never another check's
// output. The two facts read are the two that rule composes its own verdict
// from, so the pair cannot come to disagree about what placement means, and
// neither rule depends on which is asked first.
//
// The view names a module's contributed nodes keyed by the module's own dotted
// prefix, while this rule holds a component id and no module, so the candidate
// keys are the id's own dotted prefixes: an id is composed from the prefix of
// the module contributing it and the component's local name, so the module that
// contributed a node is always among them. The answer is then taken from
// membership in the list the view hands back, never from the spelling that
// produced the candidate — so a prefix naming no module, or naming one that
// contributed some other node, cannot exclude a component, which is what keeps a
// component whose own authored name carries a dot from being passed as though a
// module had contributed it.
//
// The walk runs from the longest prefix inward and excludes the component at the
// first module that both contributed it and was placed nowhere. A module nested
// inside another contributes to both keys, so one node sits under several module
// prefixes at once and their verdicts can differ: an inner module placed nowhere
// inside an outer one placed in part is the shape that separates them. Excluding
// at the innermost such module is what leaves that inner module's components to
// the one report the module rule makes at the inner module's own key.
//
// A sibling's placement is what decides the module, never this node's: the
// component the rule was invoked for is already known unplaced by the time the
// walk begins, so it is skipped rather than counted.
//
// The whole predicate is written here rather than delegated, because a rule's
// facts have to be readable in the body that declares them: a helper handed the
// view would move a fact read out of sight of the declaration, and the reader
// that derives placement from what a rule reads could then neither clear it nor
// accuse it.
//
// The detail is the component's flattened id.
var componentIsPlaced = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrUnplacedComponent(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		node := s.NodeID()
		if _, placed := v.NodeBoundaryPlacement(node); placed {
			return nil
		}
		for cut := strings.LastIndex(node, "."); cut > 0; cut = strings.LastIndex(node[:cut], ".") {
			contributed := v.ModuleContributedNodes(node[:cut])
			var holdsNode, modulePlaced bool
			for _, sibling := range contributed {
				if sibling == node {
					holdsNode = true
					continue
				}
				if _, placed := v.NodeBoundaryPlacement(sibling); placed {
					modulePlaced = true
				}
			}
			if holdsNode && !modulePlaced {
				return nil
			}
		}
		return []constraint.Objection{
			s.Raise(ErrUnplacedComponent(), node),
		}
	},
}

// guardSubModulePlacement asks the placement rule of every sub-module the
// composition brought in.
//
// It runs where it does because this is the earliest point holding all of the
// rule's evidence: the module set exists once the composition is flattened, and a
// node's placement is written after that, from the Release's own two axes and
// from the placement a routed module confers. Asked earlier, the rule would read
// an unwritten placement and report every module as unplaced.
//
// The findings are rendered through the one conversion every rule reporting to a
// resolve's callers goes through, so this rule's output is shaped like theirs
// rather than like a second opinion about what a diagnostic is.
//
// A build holding no Release document asks nothing. The rule is about what the
// Release placed, and a build without one declares no boundary at all, so
// every module would be reported for the absence of a document the caller never
// supplied.
func guardSubModulePlacement(
	cancelled constraint.Cancellation,
	cfg BuildConfig,
	view *topologyView,
) ([]*resolver.ResolverError, error) {
	if view == nil || cfg.ResolveResult == nil || cfg.ResolveResult.Release == nil {
		return nil, nil
	}
	objections, err := constraint.RunOnModules(
		cancelled, view, constraint.TopologyView(view), subModuleIsPlaced)
	if err != nil {
		return nil, err
	}
	return resolver.FindingsFromObjections(
		objections, releaseEntityName(cfg.ResolveResult.Release)), nil
}

// guardComponentPlacement asks the placement rule of every component the
// composition holds.
//
// It runs where its sibling does, and for the same reason: a node's placement is
// written from the Release's own two axes and from the placement a routed
// module confers, and the second of those is written only once the composition is
// flattened, because a module's routing entry names no components until its
// document is loaded. Asked earlier, the rule would read a placement the build
// was about to write and report a placed component as unplaced.
//
// The whole node set is asked rather than the module-contributed part of it,
// which is why the rule is not asked by the pass that guards sub-aggregate
// nodes: that pass is bounded to the nodes a module contributed, and a rule over
// placement asked there would answer for part of the composition and say nothing
// about the rest.
//
// The findings are rendered through the one conversion every rule reporting to a
// resolve's callers goes through, so this rule's output is shaped like its
// sibling's rather than like a second opinion about what a diagnostic is.
//
// A build holding no Release document asks nothing, for its sibling's reason:
// a build without one declares no boundary at all, so every component would be
// reported for the absence of a document the caller never supplied.
func guardComponentPlacement(
	cancelled constraint.Cancellation,
	cfg BuildConfig,
	view *topologyView,
) ([]*resolver.ResolverError, error) {
	if view == nil || cfg.ResolveResult == nil || cfg.ResolveResult.Release == nil {
		return nil, nil
	}
	objections, err := constraint.RunOnNodes(
		cancelled, view, constraint.TopologyView(view), componentIsPlaced)
	if err != nil {
		return nil, err
	}
	return resolver.FindingsFromObjections(
		objections, releaseEntityName(cfg.ResolveResult.Release)), nil
}

// boundaryComponentResolves reports each component entry the Release's
// boundaries declare that addresses nothing the composition holds.
//
// It is the over-naming counterpart of the two rules above and is a separate rule
// rather than a branch of either, because it asks a different question of a
// different subject. Those ask of a component the composition holds whether
// anything placed it; this asks of an entry the Release wrote whether anything
// answers to it. Neither implies the other: a Release can name a component that
// does not exist while every component that does exist is properly placed, and it
// can leave a real component unplaced while every entry it wrote resolves.
//
// It reads membership of the flattened set and never the shape of the name. The
// entries this catches are subsystem-qualified in practice — a subsystem
// contributes nothing to a component's id, so an entry written as though it did
// addresses nothing — but a dotted name is not the fault and cannot be: a
// sub-module's prefix legitimately carries a dot, and a rule refusing dotted names
// would refuse the very spelling the routed axis is required to produce.
//
// Both axes a boundary names components on reach it as the id the entry
// addresses, reconciled before the axis is enumerated, so the rule is written
// once and neither axis can be quietly left out.
//
// A component the composition removed because its activation toggle read false
// passes. Switching a component off is an authoring act, and the Release that
// configures it is not expected to be rewritten in the same edit — so refusing
// the entry would make the toggle unusable while any Release named the
// component. The two questions are asked of two facts, because the fact naming a
// flattened node's type reports membership and declines to say why a name is
// absent.
//
// The detail is the id the entry addressed. That is the value to compare against
// the composition, and on the routed axis it is not the name the file spells:
// the file carries the component's local name inside a routing entry and the
// module supplies the rest, so the id appears nowhere the author can read it off.
var boundaryComponentResolves = constraint.Check[
	constraint.TopologyView,
	constraint.BoundaryComponentReferenceSubject,
]{
	Sentinel: ErrUnresolvedBoundaryComponent(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.BoundaryComponentReferenceSubject,
	) []constraint.Objection {
		reference := s.ComponentReference()
		if _, held := v.FlattenedNodeComponentType(reference); held {
			return nil
		}
		if v.ComponentDeactivated(reference) {
			return nil
		}
		return []constraint.Objection{
			s.Raise(ErrUnresolvedBoundaryComponent(), reference),
		}
	},
}

// guardBoundaryComponentResolution asks the resolution rule of every component
// entry the Release's boundaries declare.
//
// It runs where the placement rules beside it run, and for a related but distinct
// reason: those need a node's placement, which is written once the composition is
// flattened, while this needs the flattened node set itself. A sub-module's
// components do not exist until its document is loaded, so an entry addressing one
// of them would be reported as addressing nothing if the rule were asked any
// earlier.
//
// The findings are rendered through the one conversion every rule reporting to a
// resolve's callers goes through, so this rule's output is shaped like its
// neighbours' rather than like a second opinion about what a diagnostic is.
//
// A build holding no Release document asks nothing, for its neighbours'
// reason: a build without one declares no boundary and therefore no entry, so
// there is nothing to resolve.
func guardBoundaryComponentResolution(
	cancelled constraint.Cancellation,
	cfg BuildConfig,
	view *topologyView,
) ([]*resolver.ResolverError, error) {
	if view == nil || cfg.ResolveResult == nil || cfg.ResolveResult.Release == nil {
		return nil, nil
	}
	objections, err := constraint.RunOnBoundaryComponentReferences(
		cancelled, view, constraint.TopologyView(view), boundaryComponentResolves)
	if err != nil {
		return nil, err
	}
	return resolver.FindingsFromObjections(
		objections, releaseEntityName(cfg.ResolveResult.Release)), nil
}
