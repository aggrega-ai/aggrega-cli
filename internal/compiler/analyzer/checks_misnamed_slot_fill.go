// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"fmt"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// AttachmentFittingAnEmptySlotIsNamedForIt reports each attachment written on a
// component instance under a name its component type declares no slot for,
// whose Feature fits a slot the type declares that nothing binds on that
// instance once the whole attachment cascade has folded.
//
// Declaring slots does not close the attachment surface, so an attachment under
// any other name is legal and stays free. The cost of that openness is one
// shape: a fill meant for a slot, written under the wrong name, becomes a free
// attachment while the slot it was meant for stays empty — and a slot left empty
// that is not required is a build that delivers nothing and says nothing. That
// shape, and no other, is what this rule halts.
//
// Three things narrow it to that shape, and each is a fact rather than a guess.
//
// Where the attachment was written. Only an attachment written on the instance
// itself is judged — the component's own block in the Aggregate, or the
// Release's entry for that one component. An attachment the instance inherits
// from a subsystem or a Release boundary is never judged: attaching one
// Feature once at a scope several consumers share is a supported shape, and on
// every consumer it reaches it sits under a name no slot there need declare. The
// scope is read from the places the cascade recorded each contribution as
// coming from, never from the attachment's name, and an attachment
// annotation-driven expansion produced is not judged either, because its name
// is the one the expansion's binding chose rather than one written on the
// instance. An attachment written on a connection is the connection rules'
// business: a socket's slot set is closed, and those rules refuse any other
// name outright.
//
// Which slots count. A slot counts when its declared label constraint is
// satisfied by the attachment's Feature and nothing binds the slot on the node
// after the cascade. A slot something binds is satisfied, so an extra attachment
// beside it is free; a slot declaring no constraint is fitted by no Feature here,
// because it says nothing about which Feature it is for. Fit is decided by
// [resolver.FeatureFitsSlot], which evaluates the constraint through the same
// evaluation the rule over a slot's own fill makes, so the two cannot come to
// disagree about what a slot's constraint accepts.
//
// What it names. One finding per offending attachment, leading with the name the
// attachment was written under — the value the registered sentence already
// names — then the component instance, the Feature attached, and each empty slot
// that Feature fits, captioned, in the order the component type declares them.
// Where the Feature fits one slot the advice names it; where it fits several,
// every one is named in the detail and the advice says to attach under the one
// it is meant to fill.
//
// It is placed at the finalised graph because that is the earliest point holding
// every fact it reads: which slots a node ends up binding is known only once
// every scope has folded, and where each attachment was contributed from is
// recorded as the cascade folds and is not available before it.
var AttachmentFittingAnEmptySlotIsNamedForIt = constraint.Check[
	constraint.GraphView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: resolver.ErrMisnamedSlotFill(),
	Severity: constraint.SeverityFatal,
	Run: func(v constraint.GraphView, s constraint.NodeAttachmentSubject) []constraint.Objection {
		if _, carried := v.AttachmentConnection(s.NodeID(), s.AttachmentName()); carried {
			return nil
		}
		if !writtenOnTheInstance(v.AttachmentContributions(s.NodeID(), s.AttachmentName())) {
			return nil
		}
		componentType, present := v.FlattenedNodeComponentType(s.NodeID())
		if !present {
			return nil
		}
		declared := v.DeclaredSlotNames(componentType)
		if len(declared) == 0 || slices.Contains(declared, s.AttachmentName()) {
			return nil
		}
		featureType := v.AttachmentResolvedType(s.NodeID(), s.AttachmentName())
		if featureType == "" {
			return nil
		}
		featureLabels := v.AttachmentFeatureLabels(featureType)
		bound := v.MergedAttachmentNames(s.NodeID())
		var emptyFitting []string
		for _, slot := range declared {
			if slices.Contains(bound, slot) {
				continue
			}
			if resolver.FeatureFitsSlot(v.SlotSelector(componentType, slot), featureLabels) {
				emptyFitting = append(emptyFitting, slot)
			}
		}
		if len(emptyFitting) == 0 {
			return nil
		}
		detail := []constraint.DetailValue{
			constraint.Uncaptioned(s.AttachmentName()),
			constraint.Captioned("on component", s.NodeID()),
			constraint.Captioned("attaching the Feature", featureType),
		}
		for i, slot := range emptyFitting {
			caption := "which fits the empty slot"
			if i > 0 {
				caption = "and the empty slot"
			}
			detail = append(detail, constraint.Captioned(caption, slot))
		}
		return []constraint.Objection{s.RaiseWithAdvice(
			resolver.ErrMisnamedSlotFill(),
			misnamedSlotFillAdvice(emptyFitting),
			detail...,
		)}
	},
}

// writtenOnTheInstance reports whether any of an attachment's contributions was
// written on the component instance itself: its own block in the Aggregate, or
// the Release's entry for that one component, and not on a connection entry
// either of those carries.
//
// One such contribution is enough. An attachment both an instance and a scope
// above it write under one name is still a name the instance's author chose.
func writtenOnTheInstance(contributions []constraint.AttachmentContribution) bool {
	for _, c := range contributions {
		if c.Socket != "" || c.Alias != "" {
			continue
		}
		switch c.Surface {
		case constraint.ContributionAggregateComponent, constraint.ContributionReleaseComponent:
			return true
		}
	}
	return false
}

// misnamedSlotFillAdvice is the remedy for one offending attachment: attach it
// under the name of the slot it fits. Where it fits one slot that name is the
// advice; where it fits several, the author is the one who knows which it is
// meant for, and the detail names every candidate.
func misnamedSlotFillAdvice(slots []string) string {
	if len(slots) == 1 {
		return fmt.Sprintf("Attach it as %q to fill the slot.", slots[0])
	}
	return "Attach it under the name of the slot it is meant to fill."
}
