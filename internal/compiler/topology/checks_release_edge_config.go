// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// releaseEdgeConfigurationReachesAnEdge reports each entry the Release's per-edge
// `uses` surface writes that carries `infra` no connection will ever receive.
//
// The fault is silent in the direction that costs most. An entry carrying `infra`
// says what physical shape the operator wants a connection to take; an entry that
// reached no connection puts that shape nowhere, and the build then succeeds with
// the infrastructure absent from its output. Halting is what makes the operator's
// authoring visible to them, which is the alternative to a build that reports
// success for a document it acted on only in part.
//
// The predicate is exact rather than heuristic. By the time the rule is asked, the
// connection set is final — every surface that draws one has drawn it — so the
// entry either landed on a connection or it did not, and nothing later can change
// the answer.
//
// Three shapes pass, and each is left to the rule that owns it so that a halt here
// cannot stand in for one of them:
//
//   - An entry carrying no `infra` configures nothing, so there is nothing for a
//     connection to receive and no fault to report. An entry that named a target
//     and drew a connection is covered by the same reading rather than by a case
//     of its own: the connection exists, so the entry reached one.
//   - A socket the consuming component's type does not declare belongs to the
//     socket-allowlist rule, which produces the better diagnostic because it can
//     list the sockets that do exist. A component type the catalog holds no entry
//     for declares no socket name at all and therefore passes here too, which is
//     the same deferral: whether the catalog should hold that type is another
//     rule's question.
//   - A consuming component the composition removed because its activation toggle
//     read false takes its whole edge surface with it by design. The entry is
//     inert because the operator switched the component off, not because the entry
//     is wrong.
//
// 🔴 A consuming component the composition does not hold reaches this rule under
// NO document, and that is recorded rather than defended against, because the
// alternative reads as coverage it does not have. The guard inside the flattening
// halts on exactly that shape — an entry carrying configuration, reaching no
// connection, on a component the composition does not hold — and it halts before
// the composition this rule reads exists. So the membership test below is not a
// second report of that class and must not be written as one: it guards the value
// the socket test reads, because a component the composition does not hold has no
// type and asking the catalog what the empty type declares is concluding from an
// answer about nothing. No fixture can drive it, which is why it is one clause
// with the socket test rather than an arm of its own carrying an assertion nothing
// could satisfy.
//
// It reports EVERY offending entry rather than the first, which is the framework's
// accumulation rather than a property of how this is written: the rule is invoked
// once per entry and what each invocation raised is collected, so an author fixing
// a Release with three such entries sees all three in one build.
//
// Its detail leads with the consuming component the entry was keyed under, which
// is what an author looks for first, then the socket and the alias, each
// captioned because the registered wording names neither. The remedy is a property
// of this occurrence rather than of the rule — the entry either wants a target or
// wants removing, and the registered sentence covers the whole condition including
// the arm where the component is not there at all — so it travels as advice.
var releaseEdgeConfigurationReachesAnEdge = constraint.Check[
	constraint.TopologyView,
	constraint.ReleaseEdgeConfigurationSubject,
]{
	Sentinel: ErrUnboundReleaseEdgeConfig(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.TopologyView,
		s constraint.ReleaseEdgeConfigurationSubject,
	) []constraint.Objection {
		node, socket, alias := s.NodeID(), s.SocketName(), s.AliasName()
		infra := v.ReleasePerEdgeInfra(node, socket, alias)
		if infra.Len() == 0 {
			return nil
		}
		if v.PerEdgeConnectionDrawn(node, socket, alias) {
			return nil
		}
		if v.ComponentDeactivated(node) {
			return nil
		}
		// One clause rather than two, because the membership half is not a
		// separate finding here: it guards the value the socket half reads. A
		// component the composition does not hold has no type, and a rule that
		// asked the catalog what the empty type declares would be concluding from
		// an answer about nothing.
		componentType, held := v.FlattenedNodeComponentType(node)
		if !held || !slices.Contains(v.DeclaredSocketNames(componentType), socket) {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrUnboundReleaseEdgeConfig(),
			"Give the entry a target to wire the edge, or remove it: an entry with "+
				"no target configures an edge that must already exist.",
			constraint.Uncaptioned(node),
			constraint.Captioned("on socket", socket),
			constraint.Captioned("under alias", alias),
		)}
	},
}

// guardReleaseEdgeConfiguration asks the reach rule of every entry the Release's
// per-edge `uses` surface writes.
//
// It runs where it does because this is the earliest point holding all of the
// rule's evidence, and two of the three pieces arrive only here. The connection
// set is final once every surface that draws one has drawn it. The consuming
// component's type is knowable for a component a sub-module contributed only once
// that module's document is loaded, which is what flattening does. Asked earlier,
// the rule would read a connection set still being written and report an entry
// that does reach a connection as reaching none.
//
// The findings are rendered through the one conversion every rule reporting to a
// resolve's callers goes through, so this rule's output is shaped like its
// neighbours' rather than like a second opinion about what a diagnostic is.
//
// A build holding no Release asks nothing, for its neighbours' reason: a build
// without one declares no boundary and therefore no entry, so there is nothing to
// ask about.
func guardReleaseEdgeConfiguration(
	cancelled constraint.Cancellation,
	cfg BuildConfig,
	view *topologyView,
) ([]*resolver.ResolverError, error) {
	if view == nil || cfg.ResolveResult == nil || cfg.ResolveResult.Release == nil {
		return nil, nil
	}
	objections, err := constraint.RunOnReleaseEdgeConfigurations(
		cancelled, view, constraint.TopologyView(view), releaseEdgeConfigurationReachesAnEdge)
	if err != nil {
		return nil, err
	}
	return resolver.FindingsFromObjections(
		objections, releaseEntityName(cfg.ResolveResult.Release)), nil
}
