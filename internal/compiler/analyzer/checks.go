// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
)

// This file holds this package's rules expressed as constraint declarations:
// each is a value naming the sentinel it enforces, what a violation does to the
// build, the facts it reads, and what it is invoked once per. Declared that
// way, a rule's point in the compilation follows from the facts it reads rather
// than from where a call happens to sit, and the provenance its diagnostic
// carries is supplied to it rather than assembled in its body.
//
// Every rule this package enforces now reaches that shape, because every input
// their predicates read is a declared fact: a socket's and a slot's declared
// label constraint, a Feature entity's applicability surfaces and its own
// labels, a node's finalised label set, an edge's resolved target and
// originating Feature type, the presence mandates a component type declares,
// and the node's merged attachment set after the whole attachment cascade.
//
// Two functions beside this file are deliberately absent from it. One
// enumerates the graph's acyclic paths and one assembles the payload handed to
// the policy socket; both compute material the build carries forward rather
// than deciding whether an input is admissible, so neither has a violation to
// report or a sentinel to name. Reading graph state is not what makes something
// a rule — being able to stop or warn a build is — so the discriminator is the
// absence of a predicate rather than the presence of graph access.
//
// The rules are declared here and the hand-written functions beside this file
// still run. That is what a reader comparing the two should expect rather than
// read as duplication left behind: a declaration is invoked where something
// presents a view over the compilation products its stage names, and nothing
// presents one at the stages these rules declare. The declaration is what makes
// the placement computable; the invocation arrives with the change that supplies
// the view these rules need, and the hand-written form is removed by the change
// that no longer needs it.
//
// That is a statement about these declarations and not about the framework. A
// view over an authored document is presented elsewhere, and a rule declared
// there is invoked through it, so a reader must not read the paragraph above as
// saying no view exists anywhere.
//
// Where a rule evaluates an authored label constraint, the last element of its
// detail is why that constraint could not be evaluated at all, and its absence
// means the constraint was evaluated and came out false. A detail carries fact
// values and never a rendering of them: turning an objection into a message is
// the framework's job, and a rule that formatted one would be doing it twice.

// EdgeTargetSatisfiesSocketSelector reports each edge whose target does not
// satisfy the label constraint declared on the socket the edge wires.
//
// The constraint comes from one of two declaration surfaces, and the edge's own
// resolved origin is what picks between them: an edge drawn from an
// attachment's dependency block participates in the attached Feature's socket
// declarations, while an edge drawn from a component's own block participates
// in the component type's. Reading the origin rather than inferring it from the
// socket name is what keeps the two surfaces apart.
//
// An empty constraint is one answer and not two. A socket declared without a
// constraint and a socket the catalog declares no entry for both leave the
// target unconstrained, so both end the rule, and the rule never has to ask
// which of the two it is looking at. That is deliberate: keying on whether the
// catalog declared the socket would put a second condition on one question, and
// the two could then disagree.
//
// It evaluates the constraint against the labels of the node the edge resolves
// to — the resolved target and not the authored one, because an author may
// leave a target implicit and the rule is about the node at the far end either
// way. A target naming no instance ends the rule: an absent node carries no
// label set, and treating its emptiness as one would turn an internal
// inconsistency into an authoring diagnostic.
var EdgeTargetSatisfiesSocketSelector = constraint.Check[
	constraint.GraphView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrSelectorViolation(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.EdgeSubject) []constraint.Objection {
		var labelConstraint string
		if origin := v.PerEdgeOriginFeatureType(s.NodeID(), s.SocketName(), s.AliasName()); origin != "" {
			labelConstraint = v.AttachmentSocketSelector(origin, s.SocketName())
		} else {
			componentType, instanced := v.InstanceComponentType(s.NodeID())
			if !instanced {
				return nil
			}
			labelConstraint = v.SocketSelector(componentType, s.SocketName())
		}
		if labelConstraint == "" {
			return nil
		}
		target := v.PerEdgeResolvedTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if _, instanced := v.InstanceComponentType(target); !instanced {
			return nil
		}
		match, err := selector.MatchString(labelConstraint, v.NodeLabels(target))
		if err != nil {
			return []constraint.Objection{s.Raise(
				ErrSelectorViolation(), labelConstraint, target, err.Error(),
			)}
		}
		if match {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrSelectorViolation(), labelConstraint, target)}
	},
}

// AttachmentSatisfiesFeatureApplicability reports each attachment whose node
// does not satisfy the applicability the attached Feature declares.
//
// A Feature declares applicability on two surfaces and both must hold. The
// single constraint's requirements hold together; the alternatives hold
// alternatively, any one of them being enough. An omitted surface is vacuously
// satisfied, which is why the rule evaluates both unconditionally rather than
// branching on presence: a constraint with no requirements admits every label
// set, and an empty set of alternatives admits every label set too.
//
// The type is read as the attachment's resolved type rather than the authored
// one, because an attachment's type can be contributed at a scope above the
// node, and an attachment resolving to no catalog entity ends the rule — there
// is no declaration to evaluate.
var AttachmentSatisfiesFeatureApplicability = constraint.Check[
	constraint.GraphView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrSelectorViolation(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeAttachmentSubject) []constraint.Objection {
		featureType := v.AttachmentResolvedType(s.NodeID(), s.AttachmentName())
		if featureType == "" {
			return nil
		}
		labels := v.NodeLabels(s.NodeID())
		appliesTo := v.FeatureAppliesTo(featureType)
		allHold, allErr := selector.MatchString(appliesTo, labels)
		anyHolds, anyErr := selector.MatchAnyOf(v.FeatureAppliesToAnyOf(featureType), labels)
		if unevaluable := firstNonNil(allErr, anyErr); unevaluable != nil {
			return []constraint.Objection{s.Raise(
				ErrSelectorViolation(), featureType, appliesTo, unevaluable.Error(),
			)}
		}
		if allHold && anyHolds {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrSelectorViolation(), featureType, appliesTo)}
	},
}

// SlotFillSatisfiesSlotSelector reports each filled attachment slot whose fill
// does not satisfy the label constraint the slot declares.
//
// A slot is a binding point and not a dependency socket, so its constraint is
// evaluated against the labels of the Feature filling it rather than against
// the labels of any node an edge points at. A slot declaring no constraint
// accepts any fill.
//
// An attachment binds a slot by carrying the slot's name, so the rule
// establishes the binding by looking for the slot's name among the node's
// merged attachment names before reading anything keyed by an attachment. An
// unbound slot is not this rule's business — whether a slot may be left unbound
// is the required-slot rule's question, and answering it here would report one
// question's finding with the other's evidence.
var SlotFillSatisfiesSlotSelector = constraint.Check[
	constraint.GraphView,
	constraint.NodeSlotSubject,
]{
	Sentinel: ErrSlotSelectorUnsatisfied(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSlotSubject) []constraint.Objection {
		componentType, instanced := v.InstanceComponentType(s.NodeID())
		if !instanced {
			return nil
		}
		labelConstraint := v.SlotSelector(componentType, s.SlotName())
		if labelConstraint == "" {
			return nil
		}
		// The attachment coordinate below is the slot's name only because the
		// merged set was found to hold an attachment of that name.
		if !slices.Contains(v.MergedAttachmentNames(s.NodeID()), s.SlotName()) {
			return nil
		}
		fillType := v.AttachmentResolvedType(s.NodeID(), s.SlotName())
		if fillType == "" {
			return nil
		}
		match, err := selector.MatchString(labelConstraint, v.AttachmentFeatureLabels(fillType))
		if err != nil {
			return []constraint.Objection{s.Raise(
				ErrSlotSelectorUnsatisfied(), labelConstraint, fillType, err.Error(),
			)}
		}
		if match {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrSlotSelectorUnsatisfied(), labelConstraint, fillType,
		)}
	},
}

// RequiredAttachmentTypeAttached reports each presence mandate a node's
// component type declares that no attachment on the node satisfies.
//
// A mandate demands that some attachment of a named type be present, under any
// name, with at-least-one cardinality. Presence is all the rule tests: it does
// not ask which scope supplied the attachment, whether a slot declared the
// name, or how many attachments of the type are present.
//
// The satisfying set is read from the node's merged attachment names and each
// name's resolved type, which is why the rule cannot be answered from the
// authored block alone. An attachment contributed at a scope the node inherits
// from binds a name on every node beneath that scope, so the authored block is
// empty for exactly those deployments that rely on sharing.
//
// A mandate whose declaration cannot be evaluated is skipped here rather than
// counted as unsatisfied, and it is reported by
// [AttachmentRequirementWellFormed] instead. A broken declaration must never
// read as a satisfied requirement, and it must not read as an absent
// attachment either: the two have different remediations.
var RequiredAttachmentTypeAttached = constraint.Check[
	constraint.GraphView,
	constraint.NodeSubject,
]{
	Sentinel: resolver.ErrMissingRequiredFeature(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSubject) []constraint.Objection {
		componentType, instanced := v.InstanceComponentType(s.NodeID())
		if !instanced {
			return nil
		}
		mandates := v.RequiredAttachmentMandates(componentType)
		if len(mandates) == 0 {
			return nil
		}
		var attached []string
		for _, attachment := range v.MergedAttachmentNames(s.NodeID()) {
			if resolved := v.AttachmentResolvedType(s.NodeID(), attachment); resolved != "" {
				attached = append(attached, resolved)
			}
		}
		var out []constraint.Objection
		for _, mandate := range mandates {
			if mandate.Malformed != "" || slices.Contains(attached, mandate.Type) {
				continue
			}
			out = append(out, s.Raise(
				resolver.ErrMissingRequiredFeature(), mandate.Key, mandate.Type, mandate.Label,
			))
		}
		return out
	},
}

// AttachmentRequirementWellFormed reports each presence mandate a node's
// component type declares whose declaration cannot be evaluated — an
// annotation naming the mandate family with no label after it, or one whose
// value is not a Feature type reference.
//
// It is a separate rule from [RequiredAttachmentTypeAttached] rather than a
// branch inside it, because the two carry different sentinels and a sentinel is
// the identity a caller matches on. The remediations differ too: correct the
// catalog entity, versus attach the Feature.
//
// The whole of its predicate is the mandate set a component type declares,
// which is complete as soon as the catalog is reachable — so it reads the facts
// of an earlier point in the compilation than its sibling, whose satisfying set
// needs the whole attachment cascade folded. A rule declaring an earlier view
// is still readable at every later point, so this places the rule no later than
// it has to be rather than moving where it may run.
var AttachmentRequirementWellFormed = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeSubject,
]{
	Sentinel: ErrMalformedAttachmentRequirement(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.NormalizedView, s constraint.NodeSubject) []constraint.Objection {
		componentType, instanced := v.InstanceComponentType(s.NodeID())
		if !instanced {
			return nil
		}
		var out []constraint.Objection
		for _, mandate := range v.RequiredAttachmentMandates(componentType) {
			if mandate.Malformed == "" {
				continue
			}
			out = append(out, s.Raise(
				ErrMalformedAttachmentRequirement(), mandate.Key, mandate.Malformed,
			))
		}
		return out
	},
}

// RequiredAttachmentSlotBound reports each attachment slot a node's component
// type declares required that no attachment binds.
//
// The test is name presence and nothing more. It does not ask which scope
// supplied the attachment, what the fill's type is, or what the material means
// — the slot's declared label constraint is the surface that governs which
// Feature may bind it, checked separately, and cardinality is fixed at one by a
// name binding one slot.
//
// It reads the merged attachment names rather than the authored block, and that
// is what makes the rule honest rather than a convenience: an attachment
// contributed once at a scope the node inherits from binds the slot for every
// component beneath that scope, so a rule over the authored block would reject
// exactly the deployment shape that relies on sharing. One entity therefore
// serves both the per-consumer fill and the shared attachment, and the
// requirement holds for both.
//
// A slot's requirement is not the presence mandate
// [RequiredAttachmentTypeAttached] enforces. A mandate demands some attachment
// of a named type at any name; a slot demands one named binding point be bound.
var RequiredAttachmentSlotBound = constraint.Check[
	constraint.GraphView,
	constraint.NodeSlotSubject,
]{
	Sentinel: ErrRequiredSlotUnfilled(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSlotSubject) []constraint.Objection {
		componentType, instanced := v.InstanceComponentType(s.NodeID())
		if !instanced {
			return nil
		}
		if !v.SlotRequired(componentType, s.SlotName()) {
			return nil
		}
		if slices.Contains(v.MergedAttachmentNames(s.NodeID()), s.SlotName()) {
			return nil
		}
		return []constraint.Objection{s.Raise(ErrRequiredSlotUnfilled(), componentType)}
	},
}

// OverriddenAnnotationAdvisory reports each leaf of an attachment's
// configuration where an explicitly authored value took precedence over one an
// annotation supplied. The author may be unaware of the suppression, because the
// annotation's contribution no longer appears in the rendered output. The
// emitted output is not wrong, so the rule advises and the build finishes.
//
// It reads one fact — the per-leaf provenance of an attachment's configuration —
// and keys it entirely by the coordinates of the subject it was invoked for. That
// fact completes only once the whole attachment cascade and annotation-driven
// expansion have been folded, which is what places the rule where it runs: the
// per-leaf provenance record is the only evidence that a suppression happened,
// and it does not exist earlier.
//
// It fires wherever an explicit value displaced an annotation-supplied one,
// whether or not the two would have produced different output. The per-leaf
// merge overwrites the suppressed value, so no comparison is available to make
// and the provenance record is the whole of the actionable signal.
var OverriddenAnnotationAdvisory = constraint.Check[
	constraint.GraphView,
	constraint.NodeAttachmentPillarLeafSubject,
]{
	Sentinel: ErrOverriddenAnnotation(),
	Severity: constraint.SeverityWarning,
	Run: func(
		v constraint.GraphView,
		s constraint.NodeAttachmentPillarLeafSubject,
	) []constraint.Objection {
		leaf := v.PerLeafAttachmentProvenance(
			s.NodeID(), s.AttachmentName(), s.PillarName(), s.LeafName(),
		)
		if !leaf.DisplacesAnnotation {
			return nil
		}
		return []constraint.Objection{s.Raise(
			ErrOverriddenAnnotation(),
			leaf.DisplacedAnnotationKey,
			leaf.DisplacedAnnotationSource,
		)}
	},
}

// firstNonNil is the first non-nil error of two, or nil when neither is set. It
// exists because a rule evaluating two independent constraint surfaces reports
// one reason rather than both: the first surface that could not be evaluated is
// what the author has to fix before the rule can say anything about the second.
func firstNonNil(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
