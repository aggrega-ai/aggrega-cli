// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The reading over what a bound Recipe publishes, expressed as a constraint
// declaration. See checks.go for what a declaration here is.
//
// It sits in its own file rather than beside the near-miss pair because it is not
// a near miss: those two pair a name against a set of declared names and report a
// resemblance, while this one reports an exact absence from a declared contract.
// Grouping them would suggest they share a predicate, and a reader looking for the
// edit-distance reading would find a rule that has none.

// publishedProvidesKeyIsDeclared reports each key a node's bound Recipe publishes
// that the node's component type does not declare in its output contract.
//
// Publishing more than the Component declares is legal and stays legal: the
// provides contract is subset-required and superset-permitted, so a Recipe that
// covers its Component's contract and emits extra keys besides has fulfilled it.
// The rule is therefore advisory, and it could not be anything else — refusing the
// superset would refuse authoring the contract admits.
//
// What it exists to name is a consequence invisible from either side of the edge
// it affects. A consumer reading across a `uses` edge sees the target's DECLARED
// contract, so an undeclared key is never delivered, and the consuming template
// that reads it renders an empty value rather than failing. The condition is
// reported here, at its cause — on the entry that published the key — because that
// is where it is nameable: at the consumer the key is a template reference the
// compiler cannot attribute to a target, and a diagnostic that guessed which
// target was meant would be worse than none. So the empty value appears at the
// consumer and the explanation at the producer, deliberately not co-located, and
// both appear in one build's output.
//
// The fix is one of two and which one is right is the author's call: declare the
// key on the Component when consumers are meant to read it, or leave it undeclared
// when it is the Recipe's own working state — in which case `locals` is where such
// state belongs.
//
// It reads three facts. Keyed by the node it was invoked for: the component type
// the instance declares. Keyed by that component type: whether the catalog this
// build resolves answers for it, and the keys its contract declares its
// realization will publish. The published side is not read as a fact at all — it
// is the axis the rule is invoked once per, so the key in hand IS the published
// key, and a fact restating the set would be a second reading of the enumeration
// that supplied the subject.
//
// A component type the catalog view does not answer for is passed over. No
// declaration was read for it, which is not the same as a declaration of nothing,
// and reporting every published key of such a node would turn a catalog the view
// could not answer into an advisory about the author's Recipe.
//
// Its detail leads with the published key, which is the value the rule's own
// wording is about and so carries no caption. The component type whose contract
// lacks it and the instance the Recipe was bound to follow, each captioned,
// because the registered wording names neither and the locator names the Recipe's
// own entry rather than either of them. The instance is what tells two findings
// apart where one Recipe serves several instances of one type: the entry at fault
// is written once, so those findings share a locator and differ only here.
var publishedProvidesKeyIsDeclared = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeProvidesKeySubject,
]{
	Sentinel: ErrProvidesKeyNotDeclared(),
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeProvidesKeySubject,
	) []constraint.Objection {
		componentType, present := v.InstanceComponentType(s.NodeID())
		if !present || !v.CatalogComponentPresent(componentType) {
			return nil
		}
		if slices.Contains(v.DeclaredProvidesKeys(componentType), s.ProvidesKey()) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(ErrProvidesKeyNotDeclared(),
			constraint.Uncaptioned(s.ProvidesKey()),
			constraint.Captioned("on component type", componentType),
			constraint.Captioned("bound to component instance", s.NodeID()))}
	},
}
