// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// ComponentIdentityIsDeclaredOnce reports each identity in the flattened
// composition that more than one component instance was declared under.
//
// A subsystem contributes nothing to the identity a component carries, so two
// subsystems of one Aggregate may each declare a component under the same name.
// The schema accepts that — they are distinct authored keys in distinct
// containers — and the composition then keys both on one identity, where the
// second replaces the first and one component disappears from the output with
// nothing said about it.
//
// It reads the declarations rather than the node set, and the difference is what
// makes the question answerable. The node set holds one component per identity by
// construction, so a pair is not visible there at all: after the second is
// written there is one entry, exactly as there would have been had the author
// declared one component. The declarations keep both.
//
// It is asked over the flattened composition because that is where a pair of
// declarations can be compared for the identity they take. Asked over one
// authored document instead, it would see the subsystems of that document alone
// and would never be asked about a module's own, which are read only when the
// composition is flattened.
//
// One declaration ends the rule without an objection, which is the ordinary
// answer. So does an identity nothing declared: a name the composition never
// held is another rule's subject, and this one reports only what was declared
// more than once.
var ComponentIdentityIsDeclaredOnce = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrSubsystemComponentIdentityCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.TopologyView, s constraint.NodeSubject) []constraint.Objection {
		declaring := v.NodeDeclaringSubsystems(s.NodeID())
		if len(declaring) < 2 {
			return nil
		}
		// The subsystems travel as one value rather than one detail each. They are
		// the two ends of a single fact — which containers the name was written in
		// — and a reader needs them side by side to see that the pair is the
		// finding.
		return []constraint.Objection{s.RaiseWith(
			ErrSubsystemComponentIdentityCollision(),
			constraint.Uncaptioned(s.NodeID()),
			constraint.Captioned("declared under subsystems", strings.Join(declaring, ", ")),
		)}
	},
}
