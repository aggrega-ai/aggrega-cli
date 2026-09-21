// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The two rules in this file are about the component at the far end of a
// dependency connection, and they are a pair: one reports a target no component
// answers to, the other a target a false `active` toggle removed. They are
// declared here because the conditions they report are registered here, and they
// read the finalised graph because that is the earliest presentation carrying
// every fact their predicates need — the target a connection resolved to after
// export aliases are rewritten and after every authoring surface has drawn.
//
// Splitting one question across two declarations is what the conditions
// themselves require: an author whose target vanished because they switched a
// component off needs a different sentence from one whose target is a typo, and
// a rule can name one condition. The two predicates are complementary by
// construction — each ends without objecting on the case the other reports — so
// one absent target produces exactly one finding.
//
// Each reads the SAME three facts and composes them; none is re-derived. The
// resolved target is keyed by the connection, and membership of the flattened
// set and removal by a toggle are both keyed by the node the target names. The
// membership fact deliberately does not separate "never contributed" from
// "contributed and then removed", so the removal fact beside it is what draws
// that line — which is why both rules ask both.

// ConnectionTargetIsAComponent reports each dependency connection whose resolved
// target is not a component the composition holds and was not removed by an
// activation toggle.
//
// It is invoked once per connection whichever surface drew it, so a connection a
// component's own dependency block wrote, one an attachment projected and one the
// Release wired are all asked. That breadth is the rule's point: a target that
// answers to nothing leaves the consumer's delivery silently empty, and which
// document drew the connection changes nothing about that.
//
// A target the composition removed ends this rule without an objection. The
// removal is reported by [ConnectionTargetIsActive], whose sentence names the
// toggle, and reporting both would tell an author two things about one target.
var ConnectionTargetIsAComponent = constraint.Check[
	constraint.GraphView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrDanglingUsesTarget(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.EdgeSubject) []constraint.Objection {
		target := v.PerEdgeResolvedTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if _, held := v.FlattenedNodeComponentType(target); held {
			return nil
		}
		if v.ComponentDeactivated(target) {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrDanglingUsesTarget(),
			"Point the target at a component the Aggregate declares, or declare a component under that name.",
			constraint.Uncaptioned(target),
			constraint.Captioned("wired into socket", s.SocketName()),
		)}
	},
}

// ConnectionTargetIsActive reports each dependency connection whose resolved
// target the composition removed because its `active` toggle read false.
//
// It is the sibling of [ConnectionTargetIsAComponent] over the other half of one
// absence, and it exists separately so the author is told the target was switched
// off rather than that it does not exist — the remedy is a different edit. A
// target the composition never held ends this rule without an objection, for the
// reason its sibling's documentation gives.
//
// It is invoked once per connection whichever surface drew it, so switching a
// component off while some other document still depends on it halts the build
// however that dependency was written.
var ConnectionTargetIsActive = constraint.Check[
	constraint.GraphView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrUsesPrunedComponent(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.EdgeSubject) []constraint.Objection {
		target := v.PerEdgeResolvedTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if _, held := v.FlattenedNodeComponentType(target); held {
			return nil
		}
		if !v.ComponentDeactivated(target) {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrUsesPrunedComponent(),
			"Keep the target's `active` toggle true, or drop the dependency on it.",
			constraint.Uncaptioned(target),
			constraint.Captioned("wired into socket", s.SocketName()),
		)}
	},
}
