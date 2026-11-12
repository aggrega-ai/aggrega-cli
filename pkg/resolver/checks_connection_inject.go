// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The rule over what a component's dependency connections deliver into it,
// expressed as a constraint declaration. See checks.go for what a declaration
// here is.
//
// It sits beside the rules over the component's own injection block rather than
// among them, because the surface is a different one: those read the block an
// author wrote on the component, this reads what the connections drawn from it
// deliver. It is declared in this package because the condition it raises is
// registered here.

// connectionInjectEntryKey is the locator tail naming one delivery entry inside
// a connection's block, relative to the connection itself.
//
// It is assembled once rather than at the raise, so the two authoring keys the
// tail names — the injection block and the mechanism inside it — are spelled in
// one place.
func connectionInjectEntryKey(mechanism, deliveryKey string) string {
	return "inject." + mechanism + "." + deliveryKey
}

// ConnectionInjectKeyNotDeliveredByAnotherConnection reports each delivery key
// one of a component's dependency connections delivers that another of that
// component's connections also delivers, under the same mechanism.
//
// A component's injection pipe is one keyed space. Three authoring surfaces
// deliver into it — the component's own injection block, the blocks on the
// entries of its dependency block, and the block on each attachment attached to
// it — and a key two of them deliver under is ambiguous about which value
// arrives. Two of those pairings already halt. This is the third: two
// connections each delivering the same key, where the later silently displaced
// the earlier and the build stayed green. The author resolves it by removing one
// of the two deliveries, or by delivering under different keys.
//
// Presence in both is the whole signal. The rule compares no values and does not
// treat two connections agreeing as acceptable, because the agreement is a
// property of one input rather than of the authoring — the same reading the two
// rules over the other pairings make.
//
// # What it is invoked over, and why the threshold is two
//
// It is invoked once per connection, which is the side each objection is
// attributed to, and asks of every key that connection delivers how many of the
// component's connections deliver it. The fact answers with one entry per
// delivering connection, so its length is the count: one is the ordinary case
// and no finding at all, two or more is the collision. That threshold is what
// separates this rule from the one over the component's own block, which is
// invoked over a key its author already declared once and so reports on an
// answer of one.
//
// Both connections of a colliding pair are subjects, so each raises its own
// finding at its own delivery entry. That is the shape the author needs: neither
// entry is the offender and both are theirs to change, so a single finding would
// have to pick one to point at. It is also the only shape that separates two
// connections sharing ONE socket under two aliases, where the socket names both
// of them identically and only the position tells them apart.
//
// # The keys are the ones the connection DELIVERS, not the ones its author wrote
//
// The two sets differ, and reading the wrong one would refuse legal authoring. A
// delivery entry whose payload cannot be carried as one string per key
// contributes no key to what the graph holds: it reaches no consumer, sets
// nothing, and therefore collides with nothing. So the rule reads the delivery
// the graph stores rather than the payload the author wrote — the same side the
// socket fact answers from, which is what makes the two readings agree by
// construction rather than by care.
//
// # What the detail names
//
// The finding is positioned at the delivery entry inside the connection's block,
// and the entry's own path leads the detail — the framework's rule for a rule
// objecting about a named element inside its subject, which keeps a reader of the
// locator and a reader of the detail from being told two different things. Every
// socket delivering the key follows. They are named in full rather than reduced
// to the other one, because a socket appears once per connection named for it:
// an answer naming one socket twice is what says the two connections share a
// socket, which is precisely the case a reader cannot otherwise see.
var ConnectionInjectKeyNotDeliveredByAnotherConnection = constraint.Check[
	constraint.GraphView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrInjectKeyCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.GraphView,
		s constraint.EdgeSubject,
	) []constraint.Objection {
		delivery := v.PerEdgeInject(s.NodeID(), s.SocketName(), s.AliasName())
		var out []constraint.Objection
		for mechanism, delivered := range delivery.All() {
			for _, deliveryKey := range delivered.Keys() {
				sockets := v.UsesInjectDeliverySockets(s.NodeID(), mechanism, deliveryKey)
				if len(sockets) < 2 {
					continue
				}
				out = append(out, s.RaiseAt(ErrInjectKeyCollision(),
					connectionInjectEntryKey(mechanism, deliveryKey), sockets...))
			}
		}
		return out
	},
}
