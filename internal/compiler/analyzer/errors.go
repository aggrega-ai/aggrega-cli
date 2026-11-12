// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package analyzer

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrSelectorViolation is the ABI-stable sentinel for the two label-matching
// rules Step 4 enforces: the label constraint a dependency socket declares over
// what may satisfy it, and the applicability a Feature declares over what it
// may attach to. Each violation is recorded as an
// [AnalysisError] entry on [AnalysisResult].Errors with this sentinel
// in its Sentinel field; [AnalysisError.ToCompilerError] wraps it as
// the CompilerError's inner error, so the joined build error is
// errors.Is-recoverable at the pipeline boundary.
//
// The two rules share this one sentinel, which is why it has more than one
// emission site: both are declared — [EdgeTargetSatisfiesSocketSelector] and
// [AttachmentSatisfiesFeatureApplicability] — and both raise it through the
// framework. So a caller matching on identity alone learns that a label
// constraint went unsatisfied and not which surface declared it; the locator on
// the violation is what separates the two.
//
// Two further rules raise it over one entity's own document rather than over a
// compilation: [ComponentSocketSelectorsAreReadable] and
// [FeatureSocketSelectorsAreReadable] report a socket's selector the selector
// grammar refuses, whether or not anything wires the socket, with the values the
// socket rule above reports the same selector with when an edge reaches it and a
// remedy naming the declaration to correct.
func ErrSelectorViolation() error { return errSelectorViolation }

var errSelectorViolation = cerrors.Register("selector-violation", cerrors.Meta{
	ShortCode: "E0007",
	Message:   "selector violation",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrSlotSelectorUnsatisfied is the ABI-stable sentinel for an attachment
// slot whose declared `selector` is not satisfied by the labels of the
// Feature attached to fill it — the slot analog of a usesSchema socket
// selector violation, evaluated against the attached Feature's labels at
// Step 4.
//
// Two of its emission sites raise it through the framework over the presentation
// of the finalised graph, one per place a slot is declared:
// [SlotFillSatisfiesSlotSelector] for a component type's own slot, and
// [ConnectionAttachmentSatisfiesSocketSlotSelector] for a slot a dependency
// socket declares, filled per connection. The two are one condition on two
// declaration surfaces, and the second's finding names the socket and the
// connection besides. That is why it separates a slot-fill violation from the
// two rules sharing [ErrSelectorViolation]: a caller matching on identity alone
// already knows a slot's constraint went unsatisfied, and the locator says which
// slot.
//
// Two more raise it over one entity's own document rather than over a
// compilation: [ComponentSlotSelectorsAreReadable] and
// [FeatureSlotSelectorsAreReadable] report a slot's selector the selector grammar
// refuses, whether or not anything fills the slot. They report only that outcome,
// since no fill is in hand to fail the constraint, with the values the graph
// rules report it with and a remedy naming the declaration to correct.
//
// The rule reports both an unsatisfied constraint and one the label matcher
// cannot evaluate at all, under this one identity. Both carry the same number of
// detail values — the constraint, the slot declaring it, and one value naming
// what went wrong with it — so the count separates nothing. What tells them apart
// is the caption on that third value, which names the reason the constraint could
// not be evaluated in the one case and the Feature whose labels failed it in the
// other, and the advice the finding carries, which is a different edit for each:
// correct the declaration, or give the fill the labels the declaration asks for.
func ErrSlotSelectorUnsatisfied() error { return errSlotSelectorUnsatisfied }

var errSlotSelectorUnsatisfied = cerrors.Register("slot-selector-unsatisfied", cerrors.Meta{
	ShortCode: "E0008",
	Message:   "attachment slot selector unsatisfied",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrRequiredSlotUnfilled is the ABI-stable sentinel for an attachment
// slot a Component declares `required: true` that the node's final
// attachment set leaves unbound — the slot analog of a missing required
// uses socket, evaluated over the resolved set rather than over an authored
// block.
//
// It has two emission sites, both raising it through the framework over the
// presentation of the finalised graph. [RequiredAttachmentSlotBound] reports a
// component type's own slot; there the stage is what makes the rule honest: an
// attachment contributed at a subsystem, boundary or Release scope binds the
// slot for every component under that scope, so a slot is satisfied whether an
// author fills it per-consumer or attaches once at a scope the consumers share.
// Only the post-cascade set can see both shapes, and the check is a
// name-presence test over it — it never inspects what the fill means.
// [RequiredSocketSlotFilledOnEveryConnection] reports a slot a dependency socket
// declares, which each connection on the socket fills for itself, so it is owed
// once per connection and its finding names the socket and the connection that
// leaves the slot empty.
//
// A finding leads with the slot's own name — the value the registered sentence
// already names — and carries the component type declaring it captioned after
// that. The pair rather than the type alone is what makes two findings about two
// slots on one instance say which binding point each is about, because the
// surfaces that render a finding as a sentence do not carry the locator that
// would otherwise separate them; and the caption is what tells a reader which of
// the two quoted values is which, since a slot name and a component type are
// both bare or dotted words.
//
// It is deliberately distinct from ErrMissingRequiredFeature, which reports
// an unsatisfied presence mandate. A mandate demands that SOME attachment
// of a named type be present, at any name, with at-least-one cardinality; a
// slot's `required` demands that ONE NAMED binding point be bound. Two
// questions with two remediations, so two sentinels.
//
// It is equally distinct from ErrSlotSelectorUnsatisfied, which reports a fill
// that fails the constraint the same slot declares. That question presupposes a
// fill; this one is whether there is one at all.
func ErrRequiredSlotUnfilled() error { return errRequiredSlotUnfilled }

var errRequiredSlotUnfilled = cerrors.Register("required-slot-unfilled", cerrors.Meta{
	ShortCode: "E0006",
	Message:   "required attachment slot unfilled",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrConflictingFeaturesReachOneComponent is the ABI-stable sentinel for a
// component whose merged attachment set holds two Features one of which names
// the other in its interoperability.conflictsWith list.
//
// It has one emission site, [ConflictingFeaturesDoNotReachOneComponent], which
// raises it through the framework over the presentation of the finalised graph.
// The stage is what makes the rule a rule about reach: an attachment written on
// a subsystem or a boundary lands in the merged set of every component that
// scope holds, so a boundary's attachment and a component's own meet only in the
// post-cascade set. The test compares the names a Feature's author declared
// against the Features a component carries, and reads no meaning into either.
//
// A finding leads with the Feature whose list names the other — the value the
// registered sentence names — then carries, captioned, every place that Feature
// was attached, the Feature it conflicts with and every place that one was
// attached, and the component where the two meet. One finding is raised per
// component and pair, so a pair meeting on several components reports each.
func ErrConflictingFeaturesReachOneComponent() error {
	return errConflictingFeaturesReachOneComponent
}

var errConflictingFeaturesReachOneComponent = cerrors.Register("conflicting-features-reach-one-component", cerrors.Meta{
	ShortCode: "E0308",
	Message:   "a Feature reaches a component alongside a Feature it declares a conflict with",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrPolicyViolation is the ABI-stable sentinel for a Step-4
// policy socket verdict that fails: a plugin result whose
// pass flag is false. Each failed rule is recorded as an
// [AnalysisError] entry (Kind [AnalysisErrorPolicy]) on
// [AnalysisResult].Errors with this sentinel in its Sentinel field;
// [AnalysisError.ToCompilerError] wraps it as the CompilerError's
// inner error, so the joined build error is errors.Is-recoverable at
// the pipeline boundary. Policy never mutates — a failed rule halts
// the build (the Invariant-4 "reject, never silently mutate" Loud
// Guardrail); the message the plugin supplied is surfaced verbatim.
func ErrPolicyViolation() error { return errPolicyViolation }

var errPolicyViolation = cerrors.Register("policy-violation", cerrors.Meta{
	ShortCode: "E0005",
	Message:   "policy violation",
	RulePlace: cerrors.RuleEvaluatedElsewhere("A plugin attached at the policy socket decides the rule, inside its own module and over the " +
		"graph the host handed it, and this package raises the condition on the failing verdict that " +
		"guest returned, surfacing the plugin's own message verbatim. The rule the plugin applied is " +
		"its own and runs where no view of a compilation reaches, so there is no view for the rule " +
		"dispatch to invoke a rule over."),
})

// ErrMalformedAttachmentRequirement is the ABI-stable sentinel for a
// presence mandate whose declaration cannot be evaluated: a
// features.aggrega.ai/requiredType.<label> annotation with no label, or
// whose value is not a Feature type URI. It is deliberately distinct from
// the missing-required-feature sentinel, which reports a well-formed
// requirement that no attachment satisfies. The two have different
// remediations — correct the Component catalog entity, versus attach the
// feature — and an annotation value is an opaque string that no structural
// validation upstream has checked, so a broken declaration must announce
// itself as broken rather than as unsatisfied.
//
// It has one emission site, [AttachmentRequirementWellFormed], which raises it
// through the framework. Its detail leads with the annotation key — the
// declaration the registered sentence names — then carries, captioned, the
// component type whose declaration is broken and why that key cannot be
// evaluated: the type is what names the entity to correct, which the locator
// cannot, since the locator names the component instance that uses the type.
//
// A finding carrying it is raised earlier than one carrying its sibling — while
// the composition is flattened rather than over the finalised graph — so it
// reports as a resolve-shaped finding rather than through this package's own
// violation shape. The sentinel is the same either way, which is what keeps a
// caller matching on identity matching.
func ErrMalformedAttachmentRequirement() error { return errMalformedAttachmentRequirement }

var errMalformedAttachmentRequirement = cerrors.Register("malformed-attachment-requirement", cerrors.Meta{
	ShortCode: "E0003",
	Message:   "malformed attachment requirement",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrOverriddenAnnotation is the ABI-stable sentinel for a leaf of an
// attachment's configuration where an explicitly authored value took
// precedence over one an annotation supplied. It is advisory: the emitted
// output is not wrong, so the finding is reported and the build finishes.
//
// It names a suppression rather than a conflict, and the distinction is what
// makes the rule report every displacement rather than only the ones that
// changed something. The per-leaf merge overwrites the annotation-supplied
// value with the explicit one, so the suppressed value is not recoverable
// downstream and no comparison between the two is available to make; the
// provenance record is the whole of the evidence.
func ErrOverriddenAnnotation() error { return errOverriddenAnnotation }

var errOverriddenAnnotation = cerrors.Register("overridden-annotation", cerrors.Meta{
	ShortCode: "E0004",
	Message:   "annotation contribution overridden by an explicit value",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// ErrUnusableComponentIdentity is the ABI-stable sentinel for a component whose
// identity cannot be the name its emitted resources are stamped with.
//
// An ordinary component's identity reaches its output unmodified — it is the
// deployed name of the workload, so nothing rewrites it into the target's own
// alphabet, and there is no encoding step that could absorb a name the target
// refuses. Refusal is therefore the only way the identity can be held to the
// rules the name has to satisfy, which is why this condition exists at all.
//
// It has one emission site, [ComponentIdentityIsUsable], which raises it through
// the framework over the presentation of the flattened composition. The stage is
// what makes the rule cover what it claims to: a component a module contributes
// carries a qualified identity assembled from the module's name and its own, and
// only the flattened set holds the identity that is actually stamped.
//
// Two classes report under this one identity. One is an identity that is not a
// valid label value — too long, carrying a character the target does not admit,
// or not opening and closing on a letter or a digit. The other is an identity
// opening exactly as a composed identity does, which would leave an authored
// name indistinguishable from one the compiler built for a boundary-attached
// feature.
//
// One sentinel covers both because the remedy is one remedy: rename the
// component. What differs between the classes is only which rule the new name
// has to satisfy, and that travels as the occurrence's own detail rather than as
// a second registered sentence — so the registered wording states the remedy
// that is true for every occurrence and states no rule it would be wrong about.
func ErrUnusableComponentIdentity() error { return errUnusableComponentIdentity }

var errUnusableComponentIdentity = cerrors.Register("unusable-component-identity", cerrors.Meta{
	ShortCode: "E0262",
	Message: "component identity cannot be the name its emitted resources are stamped with: " +
		"rename the component",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})
