// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SlotSelectorAdmits evaluates the label constraint an attachment slot declares
// against the labels of a Feature, and reports whether that Feature may fill the
// slot. An empty constraint admits every Feature. The error is the constraint's
// own: one the label matcher cannot parse is reported rather than read as either
// answer.
//
// It is the one evaluation every rule over a slot's constraint makes — whether
// the Feature filling a slot is admitted by it, and whether a Feature attached
// under another name is evidently meant for it — so those rules cannot come to
// disagree about what a slot's constraint accepts. A slot is a binding point, so
// its constraint is evaluated against the labels of the Feature that would bind
// it and never against the labels of a node.
func SlotSelectorAdmits(labelConstraint string, featureLabels types.OrderedMap[string]) (bool, error) {
	return selector.MatchString(labelConstraint, featureLabels)
}

// FeatureFitsSlot reports whether a Feature carrying featureLabels is evidently
// meant for a slot declaring labelConstraint: the slot declares a constraint, and
// the Feature satisfies it.
//
// It is a narrower question than whether the slot would admit the Feature, and
// the difference is the slot declaring no constraint. Such a slot admits any
// fill, but it says nothing about which Feature it is for, so no Feature fits it
// here — otherwise every attachment beside an empty unconstrained slot would read
// as a fill written under the wrong name. A constraint the label matcher cannot
// evaluate is fitted by nothing for the same reason: it is no evidence of what
// the slot is for, and the rule over a slot's own fill is the one that reports it.
func FeatureFitsSlot(labelConstraint string, featureLabels types.OrderedMap[string]) bool {
	if labelConstraint == "" {
		return false
	}
	fits, err := SlotSelectorAdmits(labelConstraint, featureLabels)
	return err == nil && fits
}
