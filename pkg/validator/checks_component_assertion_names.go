// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// assertionNameCollisionAdvice is what an author does about one assertion name
// carrying two different rules along an extends chain.
//
// It is the occurrence's advice rather than part of the condition's registered
// sentence, and it is named here rather than written inline so the rule's remedy
// has one spelling.
const assertionNameCollisionAdvice = "rename the extending entity's assertion; an extending entity's " +
	"assertions add to the base's and never replace one"

// assertionNameCollisionAdviceNaming is that remedy with the entity the name is
// inherited from named ahead of it.
//
// The two values travel in the advice rather than as the occurrence's detail
// because the failure shape this rule is rendered into carries no slot for a
// rule's detail values, and every rule already rendered into it says what the
// occurrence found in the same sentence as what to do about it.
//
// Naming the base is what the remedy needs to be actionable: an author handed a
// collision without it has to walk the chain to find which entity the name came
// from before knowing whose rule the rename is preserving.
func assertionNameCollisionAdviceNaming(base, assertion string) string {
	return fmt.Sprintf(
		"assertion %q is already declared by the base Component %q, carrying a different rule; %s",
		assertion, base, assertionNameCollisionAdvice)
}

// entityAssertionNamesCarryOneRule refuses an entity that reuses an inherited
// assertion name to declare a different rule.
//
// An assertion name is a stable rule identifier the failure diagnostic reports,
// so two rules under one name leave a reader unable to say which rule spoke. The
// alternatives are both worse: letting the extending entity's rule win drops a
// base's architectural invariant at exactly the moment a wrapper is introduced,
// and keeping both under disambiguated names destroys the one property the
// identifier has. A refusal costs the author one rename.
//
// A name restated under a byte-identical rule is not a collision. The rule that
// survives such a restatement is the same rule, so the chain declares one
// assertion there and the presented names carry it once.
//
// It reads two facts complete once one authored document has been parsed and
// envelope-checked, which is the point its declared view names: how many rules
// the chain carries under a name, and which entity that name is declared by
// nearest the chain root. Both reach the presentation already resolved across
// the chain the entity extends, so the rule reads two answers rather than
// re-walking anything.
//
// It is invoked once per distinct assertion name rather than once per
// declaration of one, because the violation is a property of the name: an author
// handed the same finding twice for one name has one rename to make either way.
var entityAssertionNamesCarryOneRule = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentAssertionSubject,
]{
	Sentinel: ErrAssertionNameCollision(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentAssertionSubject,
	) []constraint.Objection {
		declared := 0
		for _, name := range v.EntityAssertionNameSet(s.SourceFile()) {
			if name == s.AssertionName() {
				declared++
			}
		}
		if declared < 2 {
			return nil
		}
		// The bare remedy is what a presentation answering no declaring entity
		// gets. The two facts are read off one presentation, so a name it
		// counted twice is a name it declares and the base is there to be
		// named; where one ever disagreed with itself, advice that stops short
		// stays true while advice naming a base nothing supplied would not.
		advice := assertionNameCollisionAdvice
		if base, named := v.EntityAssertionDeclaringEntity(
			s.SourceFile(), s.AssertionName(),
		); named {
			advice = assertionNameCollisionAdviceNaming(base, s.AssertionName())
		}
		return []constraint.Objection{
			s.RaiseWithAdvice(ErrAssertionNameCollision(), advice),
		}
	},
}
