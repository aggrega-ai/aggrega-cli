// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The rule in this file is about who owns a connection's target. It is declared
// here because the condition it reports is registered here, and it reads the
// flattened composition because that is the earliest presentation carrying both
// authored targets keyed by the same connection: the one the drawing document
// wrote, whichever surface that was, and the one the Release's entry names.
// The second of the two is complete a stage earlier and is answered here by the
// presentation this one extends, so no later stage is needed to hold the pair.
//
// Both sides are read as they were WRITTEN and neither as what the connection
// resolved to. A connection may name a module's published alias rather than a
// component id, and resolving that alias replaces the resolved target with the
// id the module publishes it for — so a comparison against the resolved target
// would report a disagreement between two documents that named the very same
// alias, and would report it on input the build accepts.
//
// 🔴 The presentation this rule declares and the suite that invokes it are one
// stage apart, and that gap is deliberate rather than a mismatch to close. A
// rule is walked over an axis, and the flattened composition enumerates
// modules, nodes, node sockets, the components a Release's boundaries name and
// the connections a component's own block wrote — not those an attachment or
// the Release drew. The finalised graph carries the earliest enumeration of every
// connection, so that is where this rule is invoked from.
// Moving the invocation to the stage the declaration names would leave nothing
// to walk it over, and narrowing the declaration to the suite that invokes it
// would claim the rule needs a later presentation than its facts do.

// ReleaseKeepsTheDeclaredConnectionTarget reports each dependency connection
// whose Release entry names a target differing from the one the document that
// drew the connection wrote.
//
// A Release may configure a connection the Aggregate declared, and it may
// wire one into a socket the architecture declared and left unbound; it may not
// redirect a connection that already exists, because the architecture is the
// Aggregate's to declare. An entry naming the same target, or naming none at
// all, is a configuration and ends this rule without an objection.
//
// It is invoked once per connection whichever surface drew it, so a connection a
// component's own dependency block wrote, one an attachment projected and one
// the Release itself wired are all asked. A connection the Release wired
// names its own target on both sides of the comparison and so never objects,
// which is what keeps the rule about redirection rather than about authorship.
//
// The connection keeps the target its drawing document gave it whether or not
// this rule objects: the pass that reads the Release's edge surface leaves a
// conflicting entry's target and its infrastructure alone, so a redirection that
// is refused is never half-applied.
var ReleaseKeepsTheDeclaredConnectionTarget = constraint.Check[
	constraint.TopologyView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrReleaseEdgeRetarget(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.TopologyView, s constraint.EdgeSubject) []constraint.Objection {
		named := v.ReleasePerEdgeTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if named == "" {
			return nil
		}
		declared := v.PerEdgeAuthoredTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if named == declared {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrReleaseEdgeRetarget(),
			"Drop the target from the Release entry to configure the connection as it was declared, or change the target where the connection is declared.",
			constraint.Captioned("declared target", declared),
			constraint.Captioned("target the Release names", named),
			constraint.Captioned("wired into socket", s.SocketName()),
		)}
	},
}
