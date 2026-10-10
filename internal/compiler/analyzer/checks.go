// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
// in the Aggregate's own list. The two answer differently for a component a
// sub-aggregate contributes: that component is declared in the sub-aggregate's
// own document, so a rule reading the Aggregate's instance list alone finds
// no instance of that name and then declines to judge it. The flattened set is
// read uniformly and deliberately without a fallback — a node the Aggregate
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
// facts complete rather than at one point serving all of them. The rules reading
// the finalised graph are invoked where that graph and its enumeration are both
// in hand; a rule reading only facts complete once the composition is flattened —
// [AttachmentRequirementWellFormed] is one — is invoked where that flattening's
// own presentation is in hand, which is earlier. That difference is the
// declaration's doing and not a wiring choice: a rule reading fewer facts
// completes sooner, so an author hears about a broken declaration before anything
// downstream reasons about what it declares.
//
// The consequence of the earlier point is worth stating rather than discovering.
// A finding raised before the graph exists halts there, so a document carrying
// one reports it alone and the findings the later rules would have raised for
// that same document are not reached. That is the shape every check before the
// graph already has, and the remedy for a broken declaration is in the catalog
// rather than in the document the later rules are about.
//
// Where a rule evaluates an authored label constraint, the caption on the last
// element of its detail says whether that constraint could not be evaluated or
// was evaluated and came out false. A detail carries fact values and never a
// rendering of them: turning an objection into a message is the framework's job,
// and a rule that formatted one would be doing it twice.

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
		return slotSelectorObjections(s, labelConstraint, v.AttachmentFeatureLabels(fillType), fillType,
			"Correct the selector on the slot's declaration in the component's catalog entity.",
			constraint.Captioned("declared on slot", s.SlotName()))
	},
}

// ConnectionAttachmentSatisfiesSocketSlotSelector reports each attachment a
// connection carries whose Feature does not satisfy the label constraint the
// slot it fills declares on the connection's socket.
//
// It is the slot rule above asked of the other place a slot is declared. A
// component type declares slots of its own, filled on the component, and slots
// on each of its dependency sockets, filled once per connection wired through
// that socket; the constraint is written in one grammar on one kind of
// declaration, and it is evaluated against the labels of the Feature filling the
// slot either way. What differs is how the rule finds the slot: a socket's slot
// is named by the connection the attachment was written on, which the lowering
// carried onto the attachment as data, and never by the attachment's own name,
// which is a composed one no slot carries.
//
// It is invoked once per attachment a node carries and passes every attachment
// no connection carries — the rule above owns those. A connection fills each
// of its slots itself and no scope reaches one, so the attachment the rule is
// asked about is exactly the fill one connection entry wrote, in the document
// whose author named its type; the finding is positioned there.
//
// The finding carries what the rule above carries, plus the socket and the
// connection, because the same slot name may be declared on a socket and on
// the component itself, and one socket's slot is filled separately on each of
// its connections.
var ConnectionAttachmentSatisfiesSocketSlotSelector = constraint.Check[
	constraint.GraphView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrSlotSelectorUnsatisfied(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeAttachmentSubject) []constraint.Objection {
		connection, carried := v.AttachmentConnection(s.NodeID(), s.AttachmentName())
		if !carried {
			return nil
		}
		componentType, present := v.FlattenedNodeComponentType(s.NodeID())
		if !present {
			return nil
		}
		labelConstraint := v.SocketSlotSelector(componentType, connection.Socket, connection.Slot)
		if labelConstraint == "" {
			return nil
		}
		fillType := v.AttachmentResolvedType(s.NodeID(), s.AttachmentName())
		if fillType == "" {
			return nil
		}
		return slotSelectorObjections(s, labelConstraint, v.AttachmentFeatureLabels(fillType), fillType,
			"Correct the selector on the slot's declaration in the socket's featuresSchema on the "+
				"component's catalog entity.",
			constraint.Captioned("declared on slot", connection.Slot),
			constraint.Captioned("of socket", connection.Socket),
			constraint.Captioned("filled on the connection", connection.Alias))
	},
}

// slotSelectorObjections is the comparison both slot rules make once each has
// read its facts: the constraint a slot declares, evaluated against the labels
// of the Feature filling it.
//
// It is handed the values rather than the view, so every fact a rule reads is
// named in the rule's own body, where its point in the compilation is read off.
// Sharing it is what keeps the two slot surfaces from coming to disagree about
// what an unsatisfied or unreadable constraint is and how each is reported. The
// evaluation itself is the one [resolver.SlotSelectorAdmits] makes, which the
// rule over a fill written under another name reads as well, so what a slot's
// constraint accepts has one answer for every rule asking it.
//
// declaredOn names where the slot is declared, after the constraint and before
// the reason; correction is the remedy for a constraint the matcher cannot
// evaluate, which is an edit to that declaration and so differs by surface.
func slotSelectorObjections(
	s constraint.Subject,
	labelConstraint string,
	fillLabels types.OrderedMap[string],
	fillType string,
	correction string,
	declaredOn ...constraint.DetailValue,
) []constraint.Objection {
	detail := append([]constraint.DetailValue{constraint.Uncaptioned(labelConstraint)}, declaredOn...)
	match, err := resolver.SlotSelectorAdmits(labelConstraint, fillLabels)
	if err != nil {
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrSlotSelectorUnsatisfied(),
			correction,
			append(detail, constraint.Captioned("which could not be evaluated:", err.Error()))...,
		)}
	}
	if match {
		return nil
	}
	return []constraint.Objection{s.RaiseWithAdvice(
		ErrSlotSelectorUnsatisfied(),
		"Add the labels the slot's selector requires to the Feature's labels, "+
			"or fill the slot with a Feature that already carries them.",
		append(detail, constraint.Captioned("not satisfied by the labels of the Feature filling it,", fillType))...,
	)}
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
// empty for exactly those builds that rely on sharing.
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
// exactly the build shape that relies on sharing. One entity therefore
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
//
// An unbound slot has no attachment, so no document wrote anything for it and
// the finding is positioned at the component's own attachment block, where a
// fill can be written. Either document can fill it — the Aggregate on the
// component or a subsystem above it, the Release on its entry for the component
// or a boundary above it — so the remedy names both.
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
			"Attach a Feature at that name, in the Aggregate or in the Release, "+
				"on the component or at a scope above it.",
			constraint.Uncaptioned(s.SlotName()),
			constraint.Captioned("declared required by component type", componentType),
		)}
	},
}

// RequiredSocketSlotFilledOnEveryConnection reports, for each connection, every
// slot its socket declares required that no attachment the connection carries
// fills.
//
// A socket's slot is filled per connection: each connection wired through the
// socket carries its own attachments, written on its own entry in the Aggregate
// or in the Release, and no scope reaches one. So the requirement is owed once by
// each connection rather than once by the component, and a socket with two
// connections of which one fills the slot reports the other alone.
//
// It is invoked once per connection the graph holds and reads which slots the
// connection fills from the attachments the node carries, by the connection each
// was written on — the record the lowering carried onto the attachment — and
// never by splitting an attachment's composed name. A connection an attachment
// drew is wired into a socket the attached Feature declares, not one the
// component type declares, so the component's slots are not its to fill and the
// rule passes it.
//
// It names the slot, the socket and the component type declaring it, and the
// connection that leaves it empty, and is positioned at that connection's entry
// in the document that drew it — the one place a fill can be written for it,
// whichever document then writes it.
var RequiredSocketSlotFilledOnEveryConnection = constraint.Check[
	constraint.GraphView,
	constraint.EdgeSubject,
]{
	Sentinel: ErrRequiredSlotUnfilled(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.EdgeSubject) []constraint.Objection {
		node, socket, alias := s.NodeID(), s.SocketName(), s.AliasName()
		if v.PerEdgeOriginFeatureType(node, socket, alias) != "" {
			return nil
		}
		componentType, present := v.FlattenedNodeComponentType(node)
		if !present {
			return nil
		}
		var filled []string
		for _, attachment := range v.MergedAttachmentNames(node) {
			connection, carried := v.AttachmentConnection(node, attachment)
			if carried && connection.Socket == socket && connection.Alias == alias {
				filled = append(filled, connection.Slot)
			}
		}
		var out []constraint.Objection
		for _, slot := range v.SocketDeclaredSlotNames(componentType, socket) {
			if !v.SocketSlotRequired(componentType, socket, slot) || slices.Contains(filled, slot) {
				continue
			}
			out = append(out, s.RaiseWithAdvice(
				ErrRequiredSlotUnfilled(),
				"Attach a Feature under that slot in this connection's features, on its entry in the "+
					"Aggregate or in the Release.",
				constraint.Uncaptioned(slot),
				constraint.Captioned("declared required by socket", socket),
				constraint.Captioned("of component type", componentType),
				constraint.Captioned("left unfilled by the connection", alias),
			))
		}
		return out
	},
}

// ConflictingFeaturesDoNotReachOneComponent reports each pair of Features a
// node's merged attachment set holds where either one names the other in its
// interoperability.conflictsWith list.
//
// # Reach is the cascade's, and nothing here computes it
//
// An attachment reaches every component whose merged set it lands in: the
// component it is written on, and every component inside the subsystem or
// boundary it is written on, nested boundaries included. The attachment cascade
// has already folded every tier into that set by the time this rule is asked, so
// the rule reads the set and walks no scope tree of its own — two Features on
// sibling boundaries that share no component never appear in one node's set, and
// two Features on an empty boundary appear in none. A rule over the authored
// blocks instead would let a declared conflict be bypassed by writing one of the
// two at a boundary.
//
// # Matched by name, whatever the versions
//
// A Feature is identified by its name with any version removed, and each entry of
// a conflictsWith list is read the same way, both through the one home for that
// split. So an entry names every version of the Feature it names, and two
// attachments of one Feature at different versions are one Feature here. The
// comparison is between names the Feature's author declared and the Features the
// node carries; nothing is read out of either name.
//
// # One declaration suffices
//
// A pair conflicts when either side's list names the other, so a Feature
// declaring a conflict halts the build although the other declares nothing. The
// lists are read for every attachment of each Feature the node carries, because
// two versions of one Feature may declare different lists and either declaring
// the conflict is enough.
//
// # What a finding names
//
// It leads with the Feature whose list names the other — the value the
// registered sentence names — and then every place that Feature was attached,
// the Feature it conflicts with and every place that one was attached, and the
// node where the two meet. The places are the ones the cascade recorded while it
// folded, so the rule names where each attachment was written without walking
// anything to find out. One finding is raised per node and pair, in the order the
// merged set first holds each Feature, so a pair meeting on several components
// reports each of them in the order the graph holds its nodes.
//
// Every fact is read in this body, one attachment at a time, and the helpers
// below it are handed the values rather than the view: the view stays in the
// predicate so what the rule reads is visible where the rule is declared.
var ConflictingFeaturesDoNotReachOneComponent = constraint.Check[
	constraint.GraphView,
	constraint.NodeSubject,
]{
	Sentinel: ErrConflictingFeaturesReachOneComponent(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeSubject) []constraint.Objection {
		node := s.NodeID()
		var carried []carriedFeature
		for _, attachment := range v.MergedAttachmentNames(node) {
			resolved := v.AttachmentResolvedType(node, attachment)
			if resolved == "" {
				continue
			}
			carried = carry(carried, featureName(resolved),
				v.FeatureConflictsWith(resolved), v.AttachmentContributions(node, attachment))
		}
		var out []constraint.Objection
		for i := range carried {
			for j := i + 1; j < len(carried); j++ {
				declaring, other, conflict := conflictBetween(carried[i], carried[j])
				if !conflict {
					continue
				}
				detail := []constraint.DetailValue{constraint.Uncaptioned(declaring.name)}
				detail = append(detail, contributionDetail(declaring.places)...)
				detail = append(detail, constraint.Captioned("conflicts with", other.name))
				detail = append(detail, contributionDetail(other.places)...)
				detail = append(detail, constraint.Captioned("on component", node))
				out = append(out, s.RaiseWithAdvice(
					ErrConflictingFeaturesReachOneComponent(),
					"Attach only one of the two where they meet: remove one attachment, or move it to "+
						"a scope that does not hold this component.",
					detail...,
				))
			}
		}
		return out
	},
}

// carriedFeature is one Feature a node's merged attachment set holds, gathered
// across every attachment of the node that resolves to it: the Feature's name
// with any version removed, the names its conflictsWith lists declare — each with
// any version removed, and each once — and every place those attachments were
// contributed from, each once and in the order the cascade recorded them.
type carriedFeature struct {
	name      string
	conflicts []string
	places    []constraint.AttachmentContribution
}

// carry folds one attachment into the Features a node carries, in the order the
// merged set first holds each Feature: an attachment of a Feature already held
// adds its declared names and its places to that Feature's, and one of a Feature
// not yet held starts a new entry.
//
// Two versions of one Feature may declare different lists, and the union is what
// is kept, because either one declaring the conflict is enough.
func carry(
	carried []carriedFeature,
	name string,
	conflictsWith []string,
	places []constraint.AttachmentContribution,
) []carriedFeature {
	index := slices.IndexFunc(carried, func(c carriedFeature) bool { return c.name == name })
	if index < 0 {
		carried = append(carried, carriedFeature{name: name})
		index = len(carried) - 1
	}
	for _, named := range conflictsWith {
		if declared := featureName(named); !slices.Contains(carried[index].conflicts, declared) {
			carried[index].conflicts = append(carried[index].conflicts, declared)
		}
	}
	for _, place := range places {
		if !slices.Contains(carried[index].places, place) {
			carried[index].places = append(carried[index].places, place)
		}
	}
	return carried
}

// conflictBetween reports whether either of two Features a node carries names
// the other in its conflictsWith list, and returns the pair ordered so that the
// first is one whose list does. Where both lists name the other, the Feature the
// merged set holds first leads.
func conflictBetween(first, second carriedFeature) (declaring, other carriedFeature, conflict bool) {
	if slices.Contains(first.conflicts, second.name) {
		return first, second, true
	}
	if slices.Contains(second.conflicts, first.name) {
		return second, first, true
	}
	return carriedFeature{}, carriedFeature{}, false
}

// featureName is a Feature reference with any version removed, which is what
// identifies a Feature to the conflict rule. The split is the shared one, so a
// reference is read here exactly as every other reader of a versioned reference
// reads it.
func featureName(reference string) string {
	name, _, _ := types.SplitVersionedURI(reference)
	return name
}

// contributionDetail spells the places a Feature was contributed from as the
// captioned values a finding carries, in the order they are held.
func contributionDetail(places []constraint.AttachmentContribution) []constraint.DetailValue {
	var detail []constraint.DetailValue
	for _, place := range places {
		detail = append(detail, contributionPlace(place)...)
	}
	return detail
}

// contributionPlace spells one place an attachment was contributed from, in the
// words of the document an author would open to change it. A surface this rule
// does not know is spelled by its token rather than dropped, so a place added to
// the cascade is never silently missing from a finding.
//
// A contribution written on a connection entry is spelled as that entry — the
// connection, its socket and the component it belongs to — because the entry is
// where its author edits it, and the component's own attachment block, which the
// component alone would point at, does not hold it.
func contributionPlace(place constraint.AttachmentContribution) []constraint.DetailValue {
	switch place.Surface {
	case constraint.ContributionAggregateComponent:
		if place.Alias != "" {
			return []constraint.DetailValue{
				constraint.Captioned("attached in the Aggregate on the connection", place.Alias),
				constraint.Captioned("through socket", place.Socket),
				constraint.Captioned("of component", place.Component),
			}
		}
		return []constraint.DetailValue{
			constraint.Captioned("attached in the Aggregate on component", place.Component),
		}
	case constraint.ContributionSubsystem:
		return []constraint.DetailValue{
			constraint.Captioned("attached in the Aggregate on subsystem", place.ScopePath),
		}
	case constraint.ContributionReleaseBoundary:
		return []constraint.DetailValue{
			constraint.Captioned("attached in the Release on boundary", place.ScopePath),
		}
	case constraint.ContributionReleaseComponent:
		detail := []constraint.DetailValue{
			constraint.Captioned("attached in the Release on the entry for component", place.Component),
		}
		if place.Alias != "" {
			detail = []constraint.DetailValue{
				constraint.Captioned("attached in the Release on the connection", place.Alias),
				constraint.Captioned("through socket", place.Socket),
				constraint.Captioned("of the entry for component", place.Component),
			}
		}
		if place.ScopePath != "" {
			detail = append(detail, constraint.Captioned("in boundary", place.ScopePath))
		}
		return detail
	case constraint.ContributionAnnotation:
		detail := []constraint.DetailValue{
			constraint.Captioned("attached by annotation-driven expansion of", place.AnnotationKey),
		}
		if place.AnnotationSource != "" {
			detail = append(detail, constraint.Captioned("declared at", place.AnnotationSource))
		}
		return detail
	default:
		return []constraint.DetailValue{constraint.Captioned("attached on", place.Surface)}
	}
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
// Release addresses a component a composition contributed by the qualified
// identifier the composition gave it, and a Release's attachment names admit
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
// A Release's boundary names and an Aggregate's scope and component names
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
