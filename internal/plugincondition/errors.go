// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package plugincondition

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// The generic conditions a plugin reaches for.
//
// A plugin that stops a build reports a failure in-band on its socket output: a
// sentence of its own, and the identity of the condition that sentence names.
// The identity is not the plugin's to allocate, and the reason is that a plugin
// author cannot be coordinated with. There is nowhere for a stranger's plugin to
// publish a number — the published correspondence between codes and conditions
// is composed from this module's own declarations, and the self-report the host
// reads at load carries no channel for a condition at all — so a number a plugin
// minted for itself would be one no reader could look up and no registry could
// hold distinct from anybody else's.
//
// So the identity a plugin reports is one the COMPILER declares, and the host
// admits any of them. That buys the single property worth having: every code a
// reader meets resolves, so `aggrega explain` answers for it from the binary's own
// declarations, with no documentation to fetch and no guest to load.
//
// Most plugin failures have no compiler condition that fits, so the compiler
// declares a small GENERIC set naming the KINDS of failure a plugin reports, and a
// plugin reaches for one of those. Each names what happened at the socket and
// nothing about any particular target: that a construct the build declares has no
// expression, that an input could not be resolved to one value, that two produced
// things want one name, that a value has no representation. WHICH construct, which
// input, which name and which value are the plugin's own business, and the plugin
// says so in its own sentence, which the host carries through and prints after the
// condition's.
//
// That division is what keeps every target-specific fact on the far side of the
// plugin boundary while the identity a reader quotes stays one the compiler owns.
//
// The set gains a member when a genuinely new KIND of failure appears, which is
// a change to the compiler. It is not widened to accommodate a particular
// target: a proposed member whose sentence names one is the wrong member rather
// than a missing one, because such a sentence is domain knowledge that has
// crossed back over the boundary this set exists to hold. Nor is it widened to
// cover a failure some compiler condition already names precisely, since a plugin
// with a precise identity available reports less than it knows by reaching here.

// ErrPluginCannotExpressInput indicates that a plugin has no expression for a
// construct the build declares. The construct has no counterpart at all in what
// the plugin emits, so the repair is to stop declaring it against this plugin
// rather than to change a value it carries.
//
// It is one of the conditions a plugin chooses from. Which construct is in the
// plugin's own sentence; this condition says only that there is no expression
// for it.
//
// A guest core RAISES this condition as well as matching it, and it does so by
// wrapping the value this accessor returns rather than by handing an identity to
// the constructor that demands one. The accessor therefore publishes a plain
// error, which is also what keeps the condition visible to the generator that
// composes the published error reference: that generator recognises an accessor
// by its shape, and the shape it recognises returns error.
func ErrPluginCannotExpressInput() error { return errPluginCannotExpressInput }

var errPluginCannotExpressInput = cerrors.Register("plugin-cannot-express-input", cerrors.Meta{
	ShortCode: "E0288",
	Message:   "a plugin has no expression for a construct this build declares",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin decides whether it can express a construct, inside its own module and over " +
		"the input the host handed it, and the host's plugin socket raises the condition on the failure that " +
		"guest returned. The rule runs where no view of a compilation reaches, so there is no view " +
		"for the rule dispatch to invoke a rule over."),
})

// ErrPluginRequiredInputUnresolved indicates that a plugin could not resolve
// something it requires to exactly one value. Zero and many are one failure at
// the plugin's input boundary — it cannot pick — so both arrive here, and the
// plugin's own sentence says which it met.
//
// It is one of the conditions a plugin chooses from.
func ErrPluginRequiredInputUnresolved() error { return errPluginRequiredInputUnresolved }

var errPluginRequiredInputUnresolved = cerrors.Register("plugin-required-input-unresolved", cerrors.Meta{
	ShortCode: "E0289",
	Message:   "a plugin could not resolve something it requires to exactly one value",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin resolves what it requires, inside its own module and over the input the host " +
		"handed it, and the host's plugin socket raises the condition on the failure that guest returned. The " +
		"rule runs where no view of a compilation reaches, so there is no view for the rule dispatch " +
		"to invoke a rule over."),
})

// ErrPluginOutputNameCollision indicates that two things a plugin produced
// occupy one name in its output. It is keyed on the consequence rather than on
// where either name came from: one slot, two things that must go in it, and
// emitting would silently drop one. Whether either name was authored or derived
// is the plugin's own business and is in its sentence.
//
// It is one of the conditions a plugin chooses from.
func ErrPluginOutputNameCollision() error { return errPluginOutputNameCollision }

var errPluginOutputNameCollision = cerrors.Register("plugin-output-name-collision", cerrors.Meta{
	ShortCode: "E0290",
	Message:   "two things a plugin produced occupy one name in its output",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin names the things it produces, inside its own module, and discovers the " +
		"collision there; the host's plugin socket raises the condition on the failure that guest returned. The " +
		"rule runs over the plugin's own output where no view of a compilation reaches, so there is " +
		"no view for the rule dispatch to invoke a rule over."),
})

// ErrPluginValueNotRepresentable indicates that a plugin has no representation
// for a value it was given. It is distinct from having no expression for a
// construct, and the difference is which repair the author needs: here the
// counterpart exists in what the plugin emits and this particular value does not
// fit it, so the value is what changes.
//
// It is one of the conditions a plugin chooses from.
func ErrPluginValueNotRepresentable() error { return errPluginValueNotRepresentable }

var errPluginValueNotRepresentable = cerrors.Register("plugin-value-not-representable", cerrors.Meta{
	ShortCode: "E0291",
	Message:   "a plugin has no representation for a value it was given",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin decides whether a value it was given can be represented, inside its own " +
		"module, and the host's plugin socket raises the condition on the failure that guest returned. The rule " +
		"runs where no view of a compilation reaches, so there is no view for the rule dispatch to " +
		"invoke a rule over."),
})

// ErrPluginFailureUnclassified indicates that a plugin reported a failure that
// none of the declared kinds names. It is the fallback and never a destination:
// a plugin reaching for it where one of the other conditions fits reports less
// than it knows.
//
// Its wording is deliberately "none of the declared kinds names" rather than "a
// plugin reported an error", so a reader who quotes it is told plainly that the
// plugin declined to classify. An over-used fallback is then legible in the wild
// rather than indistinguishable from a correct report.
//
// It is one of the conditions a plugin chooses from, and it is chosen rather
// than fallen into: a report naming no condition at all is a different state,
// and the two stay distinguishable because they answer different questions about
// whether the set is right. A rising count of deliberate fallbacks says the set
// is missing a kind; a rising count of reports naming nothing says plugins are
// not reading the contract.
func ErrPluginFailureUnclassified() error { return errPluginFailureUnclassified }

var errPluginFailureUnclassified = cerrors.Register("plugin-failure-unclassified", cerrors.Meta{
	ShortCode: "E0292",
	Message:   "a plugin reported a failure that none of the declared kinds names",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A guest plugin decides that its failure fits none of the declared kinds, inside its own " +
		"module, and the host's plugin socket raises the condition on the failure that guest returned. The rule " +
		"runs where no view of a compilation reaches, so there is no view for the rule dispatch to " +
		"invoke a rule over."),
})
