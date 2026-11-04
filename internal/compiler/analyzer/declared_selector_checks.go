// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
)

// The rules in this file ask one question of an entity's own document: whether
// each label selector it declares on a dependency socket or an attachment slot is
// one the selector grammar reads. They ask it of the document as it is checked,
// one entity at a time, rather than of a compilation.
//
// That is the whole of what they add. The rules over the finalised graph evaluate
// a selector only where something reaches it — an edge drawn through the socket,
// an attachment filling the slot — so a selector the grammar refuses on a socket
// nothing wires, or on a slot nothing fills, is otherwise read by no rule at all
// and can be carried by an entity indefinitely. Asked of the document, every
// declared selector is read whether or not anything reaches it.
//
// They report under the conditions the graph rules report an unreadable selector
// under, with the same values in the same order and a remedy naming the
// declaration to correct, because it is the same fault: a selector the grammar
// refuses can never be satisfied, and a build reaching it halts on it. So an
// author meets one finding in one set of words whichever check reached the
// selector first. The reading itself is
// selector.Parse, the grammar's one home, which is also what the graph rules'
// evaluation runs before it compares any label.
//
// A Component's rules and a Feature's are declared apart because each kind's
// declarations are a fact of that kind's own document. A Feature's applicability
// selectors are not read here: they are a different declaration, read wherever an
// attachment of the Feature is evaluated.

// The remedies an unreadable selector carries, one per place a selector is
// declared. Each names the edit — the declaration to correct — so it differs by
// place and not by which rule found the fault: the graph rules and the document
// rules beside them read these same values.
const (
	componentSocketSelectorCorrection = "Correct the selector on the socket's declaration in the component type's usesSchema."
	componentSlotSelectorCorrection   = "Correct the selector on the slot's declaration in the component's catalog entity."
	socketSlotSelectorCorrection      = "Correct the selector on the slot's declaration in the socket's featuresSchema on the " +
		"component's catalog entity."
	featureSocketSelectorCorrection = "Correct the selector on the socket's declaration in the Feature's usesSchema."
	featureSlotSelectorCorrection   = "Correct the selector on the slot's declaration in the Feature's catalog entity."
)

// The captions the selector rules put ahead of a declared place and ahead of the
// reason a selector could not be read. They are the words a reader matches one
// finding against another by, so every rule raising over a declared selector
// reads them from here rather than spelling them again.
const (
	captionDeclaredOnSocket    = "declared on socket"
	captionDeclaredOnSlot      = "declared on slot"
	captionOfSocket            = "of socket"
	captionCouldNotBeEvaluated = "which could not be evaluated:"
)

// ComponentSocketSelectorsAreReadable reports each dependency socket a
// Component's own document declares whose selector the selector grammar refuses.
//
// It reports under [ErrSelectorViolation], naming the selector, the socket and the
// grammar's reason, as [EdgeTargetSatisfiesSocketSelector] reports the same
// selector when an edge reaches it. A socket declaring no selector constrains
// nothing and is not reported.
var ComponentSocketSelectorsAreReadable = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrSelectorViolation(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.DocumentView, s constraint.DocumentSubject) []constraint.Objection {
		return unreadableSelectorObjections(s, ErrSelectorViolation(),
			v.ComponentSocketSelectors(s.SourceFile()),
			func(constraint.DeclaredSelector) string { return componentSocketSelectorCorrection })
	},
}

// ComponentSlotSelectorsAreReadable reports each attachment slot a Component's
// own document declares — on itself, or on one of its dependency sockets — whose
// selector the selector grammar refuses.
//
// It reports under [ErrSlotSelectorUnsatisfied], naming the selector, the slot,
// the socket for a slot a socket declares, and the grammar's reason, as
// [SlotFillSatisfiesSlotSelector] and [ConnectionAttachmentSatisfiesSocketSlotSelector]
// report the same selector when a fill reaches it.
var ComponentSlotSelectorsAreReadable = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrSlotSelectorUnsatisfied(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.DocumentView, s constraint.DocumentSubject) []constraint.Objection {
		return unreadableSelectorObjections(s, ErrSlotSelectorUnsatisfied(),
			v.ComponentSlotSelectors(s.SourceFile()),
			func(d constraint.DeclaredSelector) string {
				if d.Socket != "" {
					return socketSlotSelectorCorrection
				}
				return componentSlotSelectorCorrection
			})
	},
}

// FeatureSocketSelectorsAreReadable reports each dependency socket a Feature's own
// document declares whose selector the selector grammar refuses, on the terms
// [ComponentSocketSelectorsAreReadable] reports a Component's.
var FeatureSocketSelectorsAreReadable = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrSelectorViolation(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.DocumentView, s constraint.DocumentSubject) []constraint.Objection {
		return unreadableSelectorObjections(s, ErrSelectorViolation(),
			v.FeatureSocketSelectors(s.SourceFile()),
			func(constraint.DeclaredSelector) string { return featureSocketSelectorCorrection })
	},
}

// FeatureSlotSelectorsAreReadable reports each attachment slot a Feature's own
// document declares whose selector the selector grammar refuses, on the terms
// [ComponentSlotSelectorsAreReadable] reports a Component's.
var FeatureSlotSelectorsAreReadable = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentSubject,
]{
	Sentinel: ErrSlotSelectorUnsatisfied(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.DocumentView, s constraint.DocumentSubject) []constraint.Objection {
		return unreadableSelectorObjections(s, ErrSlotSelectorUnsatisfied(),
			v.FeatureSlotSelectors(s.SourceFile()),
			func(constraint.DeclaredSelector) string { return featureSlotSelectorCorrection })
	},
}

// unreadableSelectorObjections is the reading every rule in this file makes once
// it has its declarations: each selector handed to the grammar, and one objection
// raised under sentinel for each the grammar refuses, in the order the
// declarations are given.
//
// The finding for each is [unreadableSelectorObjection]'s, the one the graph
// rules raise for the same selector, with the place read off the declaration:
// the slot where the declaration is a slot, and the socket — as the place it is
// declared on, or as the socket a slot sits in. correction is the remedy for one
// declaration, which is an edit to that declaration and so differs by the place
// it is written.
//
// An empty selector is read like any other and the grammar admits it, so a
// declaration writing none raises nothing.
func unreadableSelectorObjections(
	s constraint.Subject,
	sentinel error,
	declared []constraint.DeclaredSelector,
	correction func(constraint.DeclaredSelector) string,
) []constraint.Objection {
	var out []constraint.Objection
	for _, d := range declared {
		_, err := selector.Parse(d.Selector)
		if err == nil {
			continue
		}
		var place []constraint.DetailValue
		switch {
		case d.Slot != "" && d.Socket != "":
			place = []constraint.DetailValue{
				constraint.Captioned(captionDeclaredOnSlot, d.Slot),
				constraint.Captioned(captionOfSocket, d.Socket),
			}
		case d.Slot != "":
			place = []constraint.DetailValue{constraint.Captioned(captionDeclaredOnSlot, d.Slot)}
		default:
			place = []constraint.DetailValue{constraint.Captioned(captionDeclaredOnSocket, d.Socket)}
		}
		out = append(out, unreadableSelectorObjection(s, sentinel, d.Selector, err, correction(d), place...))
	}
	return out
}

// unreadableSelectorObjection is the one finding a selector the grammar refuses is
// reported with, by every rule that reads one: the selector, uncaptioned because
// the registered sentence already names it; then where it is declared, as the
// caller names it; then the grammar's reason; with correction as the remedy.
//
// The graph rules and the document rules all raise through it, so the values a
// reader sees, and their order, cannot come to differ between a build that
// reaches a selector and a validation that reads it before anything does.
// declaredAt is the caller's because only the caller knows the place — a socket,
// a slot, a socket's slot, or one of those filled on a named connection.
func unreadableSelectorObjection(
	s constraint.Subject,
	sentinel error,
	selectorText string,
	parseErr error,
	correction string,
	declaredAt ...constraint.DetailValue,
) constraint.Objection {
	detail := make([]constraint.DetailValue, 0, len(declaredAt)+2)
	detail = append(detail, constraint.Uncaptioned(selectorText))
	detail = append(detail, declaredAt...)
	detail = append(detail, constraint.Captioned(captionCouldNotBeEvaluated, parseErr.Error()))
	return s.RaiseWithAdvice(sentinel, correction, detail...)
}
