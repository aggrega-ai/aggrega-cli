// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// This file is the single home for converting what a check raised into the
// failure shape this package's callers already read. It is a new home rather than
// a copy of an existing one, and the distinction matters to whoever migrates the
// next rule here: the conversion the resolver owns renders into that package's
// own finding shape, and it is unreachable from here in any case, because the
// resolver depends on this package and an import back would be a cycle. So the
// second rule migrated into this package calls what is below rather than writing
// its own conversion beside it.

// ruleSubject is what a rule's diagnostic needs in this package's shape and an
// objection cannot carry: which entity the rule was checked against, and the
// identifier the entity's reference documentation heads the rule's entry with.
//
// A check knows neither. It is handed named facts and a subject standing for the
// document, and the entity reference is a property of the load that presented
// that document rather than of anything the check reads. The rule identifier is
// not derivable either: it names what the rule constrains in the words the
// reference documentation uses, which is a different register from the condition
// the rule raises under, so no accessor on an objection could produce it.
type ruleSubject struct {
	// Kind is the entity kind whose rules were checked.
	Kind envelope.Kind

	// Name is the entity's metadata name.
	Name string

	// Version is the entity's metadata version. Optional.
	Version string

	// Rule identifies the violated rule the way the entity's reference
	// documentation names it.
	Rule string
}

// shapeRuleErrorsFrom renders what a check raised into the failure shape this
// package's callers already read: one failure per objection, in the order the
// objections were raised.
//
// Every field is derived and none is re-typed. The sentinel becomes the failure's
// condition, so a caller matching by identity keeps matching. The framework's
// provenance becomes the failure's position, and the advice the occurrence
// carries becomes the advice the message closes with — the same slot every
// hand-written shape rule already puts its remedy in.
//
// It goes through the shape-rule constructor rather than building a failure
// directly, because every rule constraining a catalog entity in this package
// reports through that one constructor and a rule reporting through the framework
// is not a reason for a second shape.
//
// Returns nil when nothing was raised.
//
// # The one bound, enforced rather than documented
//
// A shape-rule description carries no slot for what an occurrence required, and
// that absence is a decision the description states on itself: the requirement is
// the sentence the rule's condition is registered with, and a second copy of it
// travelling beside the failure would be free to drift. So a rule's detail values
// have nowhere to land in this shape, and this refuses an objection carrying any
// rather than dropping them.
//
// Refusing is what keeps the bound visible. A conversion that silently discarded
// them would produce a message missing exactly the values the author has to act
// on, and nothing about the result would say so. The refusal is a panic for the
// reason the constructors below it panic: a detail value reaches here from a check
// written in this package, never from an authored document, so it is a mistake in
// the compiler rather than in anything a user wrote.
//
// A rule with something to say about the occurrence says it in its advice, which
// is where every hand-written rule in this package already puts it. A rule that
// genuinely needs a separate slot is a change to the shape-rule description, taken
// deliberately, rather than a conversion quietly deciding where the values go.
func shapeRuleErrorsFrom(objections []constraint.Objection, subject ruleSubject) []*ValidationError {
	if len(objections) == 0 {
		return nil
	}
	out := make([]*ValidationError, 0, len(objections))
	for _, o := range objections {
		if detail := o.RenderedDetail(); len(detail) > 0 {
			panic(fmt.Sprintf(
				"validator: the rule raising %q supplied the occurrence values [%s], and a shape-rule "+
					"failure carries no slot for them; raise them as the occurrence's advice, or give "+
					"ShapeRuleViolation a slot for them deliberately",
				subject.Rule, strings.Join(detail, ", ")))
		}
		out = append(out, NewShapeRuleError(ShapeRuleViolation{
			Kind:       subject.Kind,
			Name:       subject.Name,
			Version:    subject.Version,
			Path:       positionOf(o),
			Rule:       subject.Rule,
			Suggestion: o.Advice(),
			Err:        o.Sentinel(),
		}))
	}
	return out
}

// positionOf is where a failure rendered from an objection is positioned: the
// authored element the framework named inside the document, or the document's own
// file where it named none.
//
// The two are one decision rather than two, which is why they are read here
// rather than at each caller: a locator is a path inside the file the objection
// carries, so a conversion choosing them independently could position a failure at
// a path belonging to a document it does not name. Both land in the same slot,
// which already carries that dual meaning — a locator inside the document, or the
// file where a rule has only the file.
//
// A rule invoked once per document is always in the second case, because such a
// subject stands for the whole file and there is no authored element inside it to
// name.
func positionOf(o constraint.Objection) string {
	if locator := o.Locator(); locator != "" {
		return locator
	}
	return o.SourceFile()
}
