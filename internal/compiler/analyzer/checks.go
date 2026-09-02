// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
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
// Where a rule here needs the component type of a node, it reads the type the
// node carries in the flattened set rather than the type an instance declares
// in the application's own list. The two answer differently for a component a
// sub-aggregate contributes: that component is declared in the sub-aggregate's
// own document, so a rule reading the application's instance list alone finds
// no instance of that name and then declines to judge it. The flattened set is
// read uniformly and deliberately without a fallback — a node the application
// declared directly and a node a composition contributed carry their type the
// same way there, so one read serves both and there is no second path that
// could drift from the first.
//
// Two functions beside this file are deliberately absent from it. One
// enumerates the graph's acyclic paths and one assembles the payload handed to
// the policy socket; both compute material the build carries forward rather
// than deciding whether an input is admissible, so neither has a violation to
// report or a sentinel to name. Reading graph state is not what makes something
// a rule — being able to stop or warn a build is — so the discriminator is the
// absence of a predicate rather than the presence of graph access.
//
// Every rule here is invoked, and each is invoked at the point its own declared
// facts complete rather than at one point serving all of them. Five read the
// finalised graph and are invoked where that graph and its enumeration are both
// in hand; one — [AttachmentRequirementWellFormed] — reads only facts complete
// once the composition is flattened, and is invoked where that flattening's own
// presentation is in hand, which is earlier. That difference is the declaration's
// doing and not a wiring choice: a rule reading fewer facts completes sooner, so
// an author hears about a broken declaration before anything downstream reasons
// about what it declares.
//
// The consequence of the earlier point is worth stating rather than discovering.
// A finding raised before the graph exists halts there, so a document carrying
// one reports it alone and the findings the later rules would have raised for
// that same document are not reached. That is the shape every check before the
// graph already has, and the remedy for a broken declaration is in the catalog
// rather than in the document the later rules are about.
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
// way. A target naming no node ends the rule: an absent node carries no label
// set, and treating its emptiness as one would turn an internal inconsistency
// into an authoring diagnostic.
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
			componentType, present := v.FlattenedNodeComponentType(s.NodeID())
			if !present {
				return nil
			}
			labelConstraint = v.SocketSelector(componentType, s.SocketName())
		}
		if labelConstraint == "" {
			return nil
		}
		target := v.PerEdgeResolvedTarget(s.NodeID(), s.SocketName(), s.AliasName())
		if _, present := v.FlattenedNodeComponentType(target); !present {
			return nil
		}
		match, err := selector.MatchString(labelConstraint, v.NodeLabels(target))
		if err != nil {
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrSelectorViolation(),
				"Correct the selector on the socket's declaration in the component type's usesSchema.",
				constraint.Uncaptioned(labelConstraint),
				constraint.Captioned("declared on socket", s.SocketName()),
				constraint.Captioned("which could not be evaluated:", err.Error()),
			)}
		}
		if match {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrSelectorViolation(),
			"Add the labels the selector requires to the target's component type, "+
				"or wire the socket to a component that already carries them.",
			constraint.Uncaptioned(labelConstraint),
			constraint.Captioned("declared on socket", s.SocketName()),
			constraint.Captioned("not satisfied by the labels of", target),
		)}
	},
}

// AttachmentSatisfiesFeatureApplicability reports each attachment whose node
// does not satisfy the applicability the attached Feature declares.
//
// A Feature declares applicability on two surfaces, and whether a declared
// pair admits a label set is not decided here: the rule hands both surfaces to
// [selector.MatchApplicability], which is the one home for that composition
// and states its semantics. Delegating is the point of this rule rather than
// an indirection over it — every surface asking whether a Feature's
// applicability admits a label set reaches the same answer for the same input
// by construction, so what a build decides and what a read-only query reports
// cannot drift apart. Re-deriving the composition here would agree with that
// home on the inputs anyone thought to try and would reintroduce the drift
// silently everywhere else.
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
		applies, unevaluable := selector.MatchApplicability(
			appliesTo, v.FeatureAppliesToAnyOf(featureType), labels,
		)
		if unevaluable != nil {
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrSelectorViolation(),
				"Correct the appliesTo selector on the Feature's catalog entity.",
				constraint.Uncaptioned(appliesTo),
				constraint.Captioned("declared by Feature", featureType),
				constraint.Captioned("which could not be evaluated:", unevaluable.Error()),
			)}
		}
		if applies {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrSelectorViolation(),
			"Add the labels the Feature's appliesTo requires to this component's type, "+
				"or attach a Feature whose applicability the component already satisfies.",
			constraint.Uncaptioned(appliesTo),
			constraint.Captioned("declared by Feature", featureType),
			constraint.Captioned("not satisfied by the labels of", s.NodeID()),
		)}
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
		componentType, present := v.FlattenedNodeComponentType(s.NodeID())
		if !present {
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
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrSlotSelectorUnsatisfied(),
				"Correct the selector on the slot's declaration in the component's catalog entity.",
				constraint.Uncaptioned(labelConstraint),
				constraint.Captioned("declared on slot", s.SlotName()),
				constraint.Captioned("which could not be evaluated:", err.Error()),
			)}
		}
		if match {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrSlotSelectorUnsatisfied(),
			"Add the labels the slot's selector requires to the Feature's labels, "+
				"or fill the slot with a Feature that already carries them.",
			constraint.Uncaptioned(labelConstraint),
			constraint.Captioned("declared on slot", s.SlotName()),
			constraint.Captioned("not satisfied by the labels of the Feature filling it,", fillType),
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
//
// It names the component type declaring the mandate as well as the type
// demanded and the label the mandate carries, which is the shape its socket and
// slot analogues already take. The declaring type is what tells an author which
// declaration is making the demand, and the surfaces that render a finding as
// text do not all carry the locator that would otherwise say so — so without it
// two components demanding different types would still read alike wherever the
// demanded type happened to match.
var RequiredAttachmentTypeAttached = constraint.Check[
	constraint.GraphView,
	constraint.NodeSubject,
]{
	Sentinel: resolver.ErrMissingRequiredFeature(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSubject) []constraint.Objection {
		componentType, present := v.FlattenedNodeComponentType(s.NodeID())
		if !present {
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
			out = append(out, s.RaiseWith(
				resolver.ErrMissingRequiredFeature(),
				constraint.Uncaptioned(mandate.Type),
				constraint.Captioned("required by component type", componentType),
				constraint.Captioned("under the requirement label", mandate.Label),
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
// Its predicate is the mandate set a component type declares, which is complete
// as soon as the catalog is reachable, asked for the type the node carries once
// the composition is flattened. So it reads the facts of an earlier point in
// the compilation than its sibling, whose satisfying set additionally needs the
// whole attachment cascade folded, and of no earlier a point than the flattened
// node set it reads a type from. A rule declaring an earlier view is still
// readable at every later point, so this places the rule no later than it has
// to be rather than moving where it may run.
//
// It names the component type carrying the broken declaration as well as the
// annotation key and why that key cannot be evaluated. The type is what says
// which catalog entity to correct, which the locator cannot: the locator names
// the component instance that uses the type, and the instance is not where the
// remedy is. The key is named separately from the type because one arm of the
// fault is a key with no label at all, where nothing else identifies it.
var AttachmentRequirementWellFormed = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrMalformedAttachmentRequirement(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.TopologyView, s constraint.NodeSubject) []constraint.Objection {
		componentType, present := v.FlattenedNodeComponentType(s.NodeID())
		if !present {
			return nil
		}
		var out []constraint.Objection
		for _, mandate := range v.RequiredAttachmentMandates(componentType) {
			if mandate.Malformed == "" {
				continue
			}
			out = append(out, s.RaiseWithAdvice(
				ErrMalformedAttachmentRequirement(),
				"Correct that annotation on the component type's catalog entity.",
				constraint.Uncaptioned(mandate.Key),
				constraint.Captioned("declared by component type", componentType),
				constraint.Captioned("which cannot be evaluated because", mandate.Malformed),
			))
		}
		return out
	},
}

// ComponentIdentityIsUsable reports each node whose identity cannot be the name
// its emitted resources are stamped with.
//
// Every resource a node emits carries the node's identity, and for an ordinary
// component that identity is the one its author wrote, carried through with
// nothing done to it. So the target's rules for such a name are not something a
// derivation can absorb: either the identity satisfies them or the build refuses
// it. That asymmetry is deliberate — the identity is the deployed name of the
// workload, so writing it into the target's alphabet would rename every resource
// in every existing output for no gain.
//
// The identity it reads is the flattened one, which is the value actually
// stamped. A component a module contributes carries an identity assembled from
// the module's name and its own, so two names that are each acceptable alone can
// join into one that is not, and only the flattened set holds the result. That
// is why the rule reads the node identifier rather than the authored key, while
// the subject's locator still points the author at the key they wrote.
//
// Two classes report here. An identity that is not a valid label value cannot be
// stamped anywhere. An identity opening as a composed identity does could be
// stamped, but would be indistinguishable from one the compiler built for a
// boundary-attached feature — which is what makes those openings a reserved
// namespace on the authoring surface rather than a convention. Both the rules of
// the first and the openings of the second are read from the one place that
// holds them, so neither is restated here and adding an opening there is enough
// to make this rule refuse it.
//
// Each occurrence names the rule its identity failed. The registered sentence
// carries the remedy, which is one remedy for both classes — rename the
// component — and the occurrence carries which rule the new name has to satisfy,
// because that is the half the classes differ on.
//
// It reads no fact from its view, which makes it the one rule here whose
// declaration is not derived from what its predicate reads — and a reader must
// not take its declared view as evidence of a fact dependency, because there is
// none. Its whole input is the identity its subject carries, which is why the
// declaration below states that in its own terms rather than leaving the empty
// fact requirement to be read as an oversight.
//
// What places it is the enumeration it is invoked over. It has to be asked once
// per node of the flattened composition, because that is the only point at which
// a module-contributed component's qualified identity exists. Asked over an
// earlier enumeration it would read authored keys and silently stop covering the
// case it exists for: two names each acceptable alone that join into an identity
// that is not.
//
// The view it declares is the stage it is invoked at rather than the earliest one
// that would satisfy an empty fact requirement. With no facts read, every stage
// satisfies the declaration equally, so the choice is between two true answers —
// and this one keeps the framework's placement model reporting where the rule
// actually runs. Declaring the earliest instead moves the package the model
// treats as holding the earliest rule site, which reaches a generated table and
// an unrelated coverage assertion; that is the worse answer, because it is wrong
// about something a reader depends on.
var ComponentIdentityIsUsable = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrUnusableComponentIdentity(),
	Severity: constraint.SeverityFatal,
	SubjectOnly: "reads NodeID, the flattened identity the subject carries. " +
		"An identity belongs to the node itself rather than to any stage of the " +
		"compilation, so no view produces it and there is no fact to narrow to.",
	Run: func(_ constraint.TopologyView, s constraint.NodeSubject) []constraint.Objection {
		identity := s.NodeID()
		defect, opening := labels.InspectComponentIdentity(identity)
		switch defect {
		case labels.ComponentIdentityUsable:
			return nil
		case labels.ComponentIdentityReservedOpening:
			// The opening is named rather than described. It is the only part of the
			// identity the author has to change, and an author told that a name is
			// reserved without being told how much of it is cannot act on that.
			return []constraint.Objection{s.RaiseWith(
				ErrUnusableComponentIdentity(),
				constraint.Uncaptioned(identity),
				constraint.Captioned("which must not open with the reserved", opening),
			)}
		default:
			return []constraint.Objection{s.RaiseWith(
				ErrUnusableComponentIdentity(),
				constraint.Uncaptioned(identity),
				constraint.Captioned("which must", defect.Requirement()),
			)}
		}
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
//
// It names the slot as well as the component type declaring it, which is the
// shape its socket analogue already takes. Naming only the type would leave two
// findings about two different slots on two instances of one type reading as the
// same sentence, and the surfaces that render a finding as text do not all carry
// the locator that separates them — so the slot's name is what makes each
// finding say which binding point is unbound.
var RequiredAttachmentSlotBound = constraint.Check[
	constraint.GraphView,
	constraint.NodeSlotSubject,
]{
	Sentinel: ErrRequiredSlotUnfilled(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSlotSubject) []constraint.Objection {
		componentType, present := v.FlattenedNodeComponentType(s.NodeID())
		if !present {
			return nil
		}
		if !v.SlotRequired(componentType, s.SlotName()) {
			return nil
		}
		if slices.Contains(v.MergedAttachmentNames(s.NodeID()), s.SlotName()) {
			return nil
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrRequiredSlotUnfilled(),
			"Attach a Feature at that name, on the component or at a scope above it.",
			constraint.Uncaptioned(s.SlotName()),
			constraint.Captioned("declared required by component type", componentType),
		)}
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
//
// # What the detail names, and why the coordinates are in it
//
// The suppressed annotation stands bare, because the registered wording already
// says an overridden annotation contribution is what the finding is about.
// Everything after it is captioned, since an annotation key, an attachment name
// and a configuration key are bare or dotted words alike: raised bare they arrive
// as a row of quoted strings with nothing saying which is which.
//
// The coordinates are there because a locator cannot always carry them. A
// deployment addresses a component a composition contributed by the qualified
// identifier the composition gave it, and a deployment's attachment names admit
// the separator the locator dialect divides its segments with — so on those
// surfaces the position stops at the block above the offending key and gives up
// everything written under it at once. Naming what it gave up is what turns a
// finding about a block into a finding about a key. They are named whether or not
// the position actually gave them up: the rule cannot see which case it is in,
// and a reader told the same thing twice is better served than one told it never.
//
// # What the rule names, and what it leaves to the position
//
// The component key is not a coordinate this rule is invoked at, so it is read as
// a declared fact rather than off the subject. The distinction is not procedural:
// the authored key an entry sits under is a different value from the node's
// identifier wherever the two surfaces spell one component differently, so a rule
// substituting the identifier would name a component that is not the editable
// site — and on an entry written scope-wide it would name one where the author
// wrote none. It is therefore raised only where the entry has one, which is what
// keeps a scope-wide finding from acquiring a component it never had.
//
// With that fact read, the finding names every coordinate a position can give up.
// A deployment's boundary names and an application's scope and component names
// admit no separator, so a position truncated at all is truncated at the
// components block or at the attachments block, and the component, the
// attachment, the pillar and the key are exactly what lies below those two. So
// what remains unnamed is nothing: a reader is handed either a whole position or
// a truncated one plus every coordinate under it.
//
// The annotation's own site comes last, after the coordinates rather than among
// them. It is a whole position in itself and the longest value the finding
// carries, so a reader scanning for the short coordinates would have to read past
// it; and set before them, its caption reads as though the coordinates were where
// the annotation was declared, which is the one thing they are not. It is not the
// component key by another name either — it names where the DISPLACED annotation
// was written, which is the scope it was inherited from whenever it was not
// written on the component that displaced it.
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
		detail := []constraint.DetailValue{constraint.Uncaptioned(leaf.DisplacedAnnotationKey)}
		if component := v.PerLeafAttachmentAuthoredComponent(
			s.NodeID(), s.AttachmentName(), s.PillarName(), s.LeafName(),
		); component != "" {
			detail = append(detail, constraint.Captioned("component", component))
		}
		detail = append(detail,
			constraint.Captioned("attachment", s.AttachmentName()),
			constraint.Captioned("pillar", s.PillarName()),
			constraint.Captioned("key", s.LeafName()),
			constraint.Captioned("annotation declared at", leaf.DisplacedAnnotationSource),
		)
		return []constraint.Objection{s.RaiseWith(ErrOverriddenAnnotation(), detail...)}
	},
}
