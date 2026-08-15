// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// subModuleIsPlaced reports each sub-module the composition brought in whose
// components no boundary holds.
//
// A boundary holds a component two ways and the rule reads both, because either
// one places it: the deployment names the component under the id it carries once
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
// the deployment placed it, and which of its components carry their own entries
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

// guardSubModulePlacement asks the placement rule of every sub-module the
// composition brought in.
//
// It runs where it does because this is the earliest point holding all of the
// rule's evidence: the module set exists once the composition is flattened, and a
// node's placement is written after that, from the deployment's own two axes and
// from the placement a routed module confers. Asked earlier, the rule would read
// an unwritten placement and report every module as unplaced.
//
// The findings are rendered through the one conversion every rule reporting to a
// resolve's callers goes through, so this rule's output is shaped like theirs
// rather than like a second opinion about what a diagnostic is.
//
// A build holding no deployment document asks nothing. The rule is about what the
// deployment placed, and a build without one declares no boundary at all, so
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
	entity := "Release"
	if name := cfg.ResolveResult.Release.Metadata.Name; name != "" {
		entity = "Release " + name
	}
	return resolver.FindingsFromObjections(objections, entity), nil
}
