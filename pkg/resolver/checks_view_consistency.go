// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// componentTypeResolvesToACatalogEntry reports each component whose type was
// resolved as a catalog identity and which the catalog this build consults holds
// no entry for.
//
// It is a rule in its own right rather than a condition inside the rules that
// meet it, and expressing it once is what lets every other rule over what a
// component type declares simply end when the entry is absent. Each of those
// reads a name set or a resolved contract that comes back empty for a type
// nobody found exactly as it does for a type declaring nothing, so a rule
// carrying its own classification would be stating this one a second time and
// the copies would answer differently the moment either moved.
//
// Two conditions make the finding, and reading one without the other reports the
// wrong thing in a way nothing catches. Absence alone is not a fault: a type with
// a ./ or ../ prefix names no catalog entity, so this rule passes over it; pull,
// build, and validate or lint over a Release refuse it in dependency resolution
// as a reference no catalog holds, and inspect, which skips that, shows it. Absence
// of a type addressed as an identity is a divergence between what the build
// resolved and what the rules can see, because a genuinely unresolvable reference
// halts before any of this is asked. The two facts are independent — one is of
// the reference as authored, the other of what the catalog holds — so both are read.
//
// It is keyed by the component rather than by the type, so one absent entry is
// reported once per component that names it. The component is the site an author
// can act on; the type alone would leave them looking for which component meant
// it, and a type several components name would be reported once for all of them.
//
// The detail is the type as the component declared it, captioned with the wording
// this condition reports under. That wording is not this rule's own: it is
// [catalogEntryAbsentCaption], the single home the composing guard renders from
// as well, so a reader meeting the condition at either surface is told the same
// thing. The caption is carried rather than left off because a detail value on
// its own says which type the finding is about and nothing about what is wrong
// with it, which names the fault only for a reader who already knows the rule.
var componentTypeResolvesToACatalogEntry = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrGuardEntityViewInconsistency(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeSubject,
	) []constraint.Objection {
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present {
			return nil
		}
		if v.CatalogComponentPresent(componentType) {
			return nil
		}
		if v.ComponentTypeIsLocalPath(componentType) {
			return nil
		}
		return []constraint.Objection{
			s.RaiseWith(
				ErrGuardEntityViewInconsistency(),
				constraint.Captioned(catalogEntryAbsentCaption, componentType),
			),
		}
	},
}
