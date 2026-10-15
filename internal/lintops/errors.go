// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The publishing-discipline rules, each declared as a registered halting
// condition.
//
// # The rule identifier IS the descriptive code
//
// A rule already carried a stable identifier before it carried an identity: the
// linter guest reports it on every finding, the structured lint report publishes
// it, and this package dispatches on it. Registering the condition under that
// same string is what keeps the rule one identifier rather than two — a mapping
// between a rule identifier and a separate descriptive code would be a second
// home for the correspondence, and a second home is what drifts when either side
// is renamed.
//
// So [ruleID] reads the identifier off the condition, and nothing in this
// package writes a rule identifier as a literal.
//
// # A condition here names a rule, never a severity
//
// The same rule is reported at the advisory severity over local content and at
// the halting severity at publish time, because the two differ in consequence
// rather than in what was found. The registration declares the wording and the
// codes and says nothing about consequence, which is what lets one condition
// stand behind both readings — see [reportFindings], which decides the
// consequence from the finding the guest returned.
//
// # Each registration declares its own evaluator, as a literal
//
// Every registration below declares its rule evaluated elsewhere, because a
// publishing-discipline rule is evaluated by the publishing-linter guest, or by
// this package's own publish-time verdict, and never by the compiler's rule
// dispatch. The reasons read alike wherever the fact is alike, and that
// repetition is deliberate rather than a shared constant waiting to be
// extracted.
//
// Two reasons hold it, and they pull the same way. The census that reads these
// notes reads them off the declaration's own syntax, so an identifier naming a
// constant is a value it cannot resolve and the declaration would go unread.
// And the fact is per-condition: if one of these rules is later lifted into the
// dispatch, only its own declaration should change, and a shared constant would
// make that edit invisible — every sibling would go on asserting a fact that
// had stopped being true of one of them.
var (
	errSchemaEvolution = cerrors.Register("schema-evolution", cerrors.Meta{
		ShortCode: "E0276",
		Message: "the version being published changes the published predecessor in a way the " +
			"documented evolution rubric objects to",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the difference between the version being published and " +
			"its published predecessor, and this package raises the condition on the finding it returns. " +
			"The rule runs inside the guest over content presented at publication, which the rule " +
			"dispatch neither invokes nor holds a view of."),
	})

	errAnnotationPrefix = cerrors.Register("annotation-prefix", cerrors.Meta{
		ShortCode: "E0277",
		Message:   "an annotation key on the entity uses a prefix the platform reserves",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the entity's annotation keys against the prefixes the " +
			"platform reserves, and this package raises the condition on the finding it returns. The rule " +
			"runs inside the guest over content presented at publication, which the rule dispatch neither " +
			"invokes nor holds a view of."),
	})

	errNameShape = cerrors.Register("name-shape", cerrors.Meta{
		ShortCode: "E0278",
		Message:   "the entity's name does not follow the documented name shape for its kind",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the entity's name against the documented name shape " +
			"for its kind, and this package raises the condition on the finding it returns. The rule runs " +
			"inside the guest over content presented at publication, which the rule dispatch neither " +
			"invokes nor holds a view of."),
	})

	errDynamicRefs = cerrors.Register("dynamic-refs", cerrors.Meta{
		ShortCode: "E0279",
		Message: "a schema in the entity uses a dynamic reference construct that cross-document " +
			"reference resolution cannot follow",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the schemas the entity carries for dynamic reference " +
			"constructs, and this package raises the condition on the finding it returns. The rule runs " +
			"inside the guest over content presented at publication, which the rule dispatch neither " +
			"invokes nor holds a view of."),
	})

	errIDOverrides = cerrors.Register("id-overrides", cerrors.Meta{
		ShortCode: "E0280",
		Message: "a schema in the entity declares an identifier of its own, where the platform " +
			"owns that schema's identity",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the schemas the entity carries for an identifier " +
			"declared where the platform owns the schema's identity, and this package raises the " +
			"condition on the finding it returns. The rule runs inside the guest over content presented " +
			"at publication, which the rule dispatch neither invokes nor holds a view of."),
	})

	errCategoryMatchesDirectory = cerrors.Register("category-matches-directory", cerrors.Meta{
		ShortCode: "E0281",
		Message: "the entity's category label does not match the category directory the entity " +
			"is filed under",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the entity's category label against the category " +
			"directory it is filed under, and this package raises the condition on the finding it " +
			"returns. The rule runs inside the guest over content presented at publication, which the " +
			"rule dispatch neither invokes nor holds a view of."),
	})

	errXAggregaDiscouragement = cerrors.Register("x-aggrega-discouragement", cerrors.Meta{
		ShortCode: "E0282",
		Message: "the entity writes an Aggrega schema extension directly into schema content " +
			"instead of declaring it under usesSchema or featuresSchema",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the schema content the entity carries for an Aggrega " +
			"schema extension written there rather than under the entity's own socket or attachment " +
			"table, and this package raises the condition on the finding it returns. The rule runs inside " +
			"the guest over content presented at publication, which the rule dispatch neither invokes nor " +
			"holds a view of."),
	})

	errPluginDefaultLimits = cerrors.Register("plugin-default-limits", cerrors.Meta{
		ShortCode: "E0283",
		Message: "the Plugin entity declares no runtime resource cap of its own, so the platform " +
			"defaults apply",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the runtime resource caps a Plugin entity declares, " +
			"and this package raises the condition on the finding it returns. The rule runs inside the " +
			"guest over content presented at publication, which the rule dispatch neither invokes nor " +
			"holds a view of."),
	})

	errSecretSlotDeclaresSensitive = cerrors.Register("secret-slot-declares-sensitive", cerrors.Meta{
		ShortCode: "E0284",
		Message: "an attachment slot admits only secret material and does not declare that it " +
			"handles it",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates an attachment slot's selector against what the slot " +
			"declares about handling secret material, and this package raises the condition on the " +
			"finding it returns. The rule runs inside the guest over content presented at publication, " +
			"which the rule dispatch neither invokes nor holds a view of."),
	})

	errProvidesDeclaredByComponent = cerrors.Register("provides-declared-by-component", cerrors.Meta{
		ShortCode: "E0285",
		Message: "the Recipe publishes a provides key the Component it implements does not " +
			"declare, so a consumer wiring that key is refused",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the provides keys a Recipe publishes against the " +
			"Component it implements, and this package raises the condition on the finding it returns. " +
			"The rule runs inside the guest over content presented at publication, which the rule " +
			"dispatch neither invokes nor holds a view of."),
	})

	errMetricsCredentialAnnotationMatchesProvides = cerrors.Register("metrics-credential-annotation-matches-provides", cerrors.Meta{
		ShortCode: "E0346",
		Message: "a Component's annotation stating how its metrics authenticate disagrees with whether it declares the " +
			"provides key saying an instance's metrics need a credential",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates the annotation a Component states how its metrics " +
			"authenticate with against the provides keys its schema declares, and this package raises the condition on " +
			"the finding it returns. The rule runs inside the guest over content presented at publication, which the " +
			"rule dispatch neither invokes nor holds a view of."),
	})

	errConnectionCredentialMatchesTargetKey = cerrors.Register("connection-credential-matches-target-key", cerrors.Meta{
		ShortCode: "E0329",
		Message: "a connection and the target it reaches do not name one key: they write different locators for " +
			"it, or only one of them names one",
		RulePlace: cerrors.RuleEvaluatedElsewhere("The publishing-linter guest evaluates each connection a Release's components draw against " +
			"the key its target holds, reading the Aggregate, the Release and the Components they name, and this " +
			"package raises the condition on the finding it returns. The rule runs inside the guest over the " +
			"authored documents a Release-scoped lint presents, which the rule dispatch neither invokes nor holds " +
			"a view of."),
	})

	errPublishBumpBelowClassification = cerrors.Register("publish-bump-below-classification", cerrors.Meta{
		ShortCode: "E0286",
		Message: "the version bump being published is smaller than the evolution check's " +
			"classification of the change requires",
		RulePlace: cerrors.RuleEvaluatedElsewhere("This package evaluates the rule itself, at publication: it reads the classification the " +
			"evolution rule produced and compares it with the bump between the published predecessor's " +
			"version and the one offered — see [bumpRefusal], which is the host's own verdict rather " +
			"than a finding the guest returned. Which bump was offered is known only here, and " +
			"publication runs no compilation, so there is no view for the rule dispatch to invoke a rule " +
			"over."),
	})
)

// ErrSchemaEvolution fires when the evolution check objects to the difference
// between the published predecessor and the version being published.
func ErrSchemaEvolution() error { return errSchemaEvolution }

// ErrAnnotationPrefix fires when an annotation key on a published entity uses a
// prefix the platform reserves for its own well-known keys.
func ErrAnnotationPrefix() error { return errAnnotationPrefix }

// ErrNameShape fires when a published entity's name does not match the
// documented name shape for its kind.
func ErrNameShape() error { return errNameShape }

// ErrDynamicRefs fires when a schema in a published entity uses a dynamic
// reference construct that cross-document reference resolution cannot follow.
func ErrDynamicRefs() error { return errDynamicRefs }

// ErrIDOverrides fires when a schema in a published entity declares an
// identifier of its own, displacing the identity the platform owns.
func ErrIDOverrides() error { return errIDOverrides }

// ErrCategoryMatchesDirectory fires when a published entity's category label
// and the category directory it is filed under name different categories.
func ErrCategoryMatchesDirectory() error { return errCategoryMatchesDirectory }

// ErrXAggregaDiscouragement fires when a published entity writes an Aggrega
// schema extension directly into schema content rather than declaring it under
// the entity's own socket or attachment table.
func ErrXAggregaDiscouragement() error { return errXAggregaDiscouragement }

// ErrPluginDefaultLimits fires when a published Plugin entity declares neither
// runtime resource cap, so the platform's own defaults govern how it runs.
func ErrPluginDefaultLimits() error { return errPluginDefaultLimits }

// ErrSecretSlotDeclaresSensitive fires when an attachment slot's selector
// admits only secret material while the slot declares no marker recording that
// it handles it.
func ErrSecretSlotDeclaresSensitive() error { return errSecretSlotDeclaresSensitive }

// ErrProvidesDeclaredByComponent fires when a Recipe publishes a provides key
// the Component it implements does not declare.
func ErrProvidesDeclaredByComponent() error { return errProvidesDeclaredByComponent }

// ErrMetricsCredentialAnnotationMatchesProvides fires when a Component's
// annotation stating how its metrics authenticate and its declaration of the
// provides key saying whether an instance's metrics need a credential disagree:
// it states that its metrics take no credential and declares the key, or states
// that they take one and does not.
func ErrMetricsCredentialAnnotationMatchesProvides() error {
	return errMetricsCredentialAnnotationMatchesProvides
}

// ErrConnectionCredentialMatchesTargetKey fires when a connection whose target
// declares the key it requires of its callers and that target do not name one
// key: the two ends write different locators, or only one end names a key.
func ErrConnectionCredentialMatchesTargetKey() error { return errConnectionCredentialMatchesTargetKey }

// ErrPublishBumpBelowClassification fires when the version bump offered for
// publication is smaller than the evolution check's classification requires.
func ErrPublishBumpBelowClassification() error { return errPublishBumpBelowClassification }

// publishingRule is one rule the publishing-discipline pass dispatches: the
// condition that names it, whether it runs only at publish time, and whether it
// reads one entity or a whole Release.
//
// prePublish is set for a rule that needs the previously published bytes to
// compare against. Such a rule cannot run over local content, because in default
// mode there is no registry to read a predecessor from — the constraint is
// structural rather than a policy about when the rule is worth running.
//
// releaseScoped is set for a rule that reads a whole Release rather than one
// entity: the Aggregate the Release deploys, that Release, and the Component each
// of the Aggregate's component instances is an instance of. Such a rule is
// dispatched once per release-scoped run, over those documents, and never per
// entity file — so it is reached only from the pass that names a Release, which
// is the only pass with those documents to hand it.
type publishingRule struct {
	condition     cerrors.Identity
	prePublish    bool
	releaseScoped bool
}

// publishingRules is the rule set this package dispatches, in dispatch order.
//
// It is the single home of that set. The identifiers the guest answers to are
// read off these conditions rather than written out beside them, so a rule
// renamed in its registration is renamed in the dispatch by construction.
//
// LOCKSTEP: the set MUST match the linter guest's dispatch arms. Adding a rule
// means registering a condition above, adding it here, and adding the guest's
// arm. The agreement is guarded by a test that reads the guest's dispatch table,
// so a one-sided addition fails rather than producing a rule that silently never
// runs.
var publishingRules = []publishingRule{
	{condition: errSchemaEvolution, prePublish: true},
	{condition: errAnnotationPrefix},
	{condition: errNameShape},
	{condition: errDynamicRefs},
	{condition: errIDOverrides},
	{condition: errCategoryMatchesDirectory},
	{condition: errXAggregaDiscouragement},
	{condition: errPluginDefaultLimits},
	{condition: errSecretSlotDeclaresSensitive},
	{condition: errProvidesDeclaredByComponent},
	{condition: errMetricsCredentialAnnotationMatchesProvides},
	{condition: errConnectionCredentialMatchesTargetKey, releaseScoped: true},
}

// ruleConditions keys the rule set by the identifier the guest reports on a
// finding, which is the direction a report reads it in: a finding arrives
// carrying an identifier, and the condition behind it is what the report names.
var ruleConditions = indexRuleConditions(publishingRules)

// indexRuleConditions keys the rule set by rule identifier, and panics at
// package initialisation on a set that would leave the lookup ambiguous.
//
// A duplicate cannot arise from the registrations themselves — registering two
// conditions under one descriptive code is refused where they are declared — so
// what this catches is one condition listed twice, which would make the set's
// length disagree with the number of rules dispatched and would dispatch that
// rule twice.
func indexRuleConditions(rules []publishingRule) map[string]cerrors.Identity {
	out := make(map[string]cerrors.Identity, len(rules))
	for _, r := range rules {
		id := ruleID(r.condition)
		if _, dup := out[id]; dup {
			panic("lintops: the publishing rule " + id + " is listed twice, so it would be dispatched twice " +
				"and the guest would answer it twice")
		}
		out[id] = r.condition
	}
	return out
}

// ruleID returns the identifier a rule is dispatched and reported under, read
// off the condition that names the rule.
//
// The identifier is the condition's descriptive code rather than a second string
// declared beside it, so there is one name for a rule and no correspondence to
// keep in step.
func ruleID(condition cerrors.Identity) string { return condition.ErrorCode() }

// ConditionForRule returns the condition a finding's rule identifier names, and
// whether the rule set holds one.
//
// It is the one place that correspondence is read, and every surface reporting
// a finding reads it here rather than keeping a table of its own: a terminal
// row and a structured entry naming different conditions for one finding is
// what a second table would eventually produce, and nothing would fail while
// the two agreed.
//
// A finding can legitimately carry an identifier no condition names: the linter
// socket runs whatever guest the workspace attaches, and a guest of somebody
// else's carries its own rule set. Such a finding is reported without a code
// rather than refused — the finding is the guest's answer and stands whether or
// not this build knows the rule by name.
func ConditionForRule(id string) (cerrors.Identity, bool) {
	condition, ok := ruleConditions[id]
	return condition, ok
}
