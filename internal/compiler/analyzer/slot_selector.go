// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// validateSlotSelectors runs the attachment-slot selector check at Step 4 — the
// slot analog of validateSelectors (usesSchema) and validateAppliesTo (Feature
// appliesTo), reusing pkg/selector: when a filled slot declares a `selector`,
// the attached Feature's labels must satisfy it, or the fill halts with
// ErrSlotSelectorUnsatisfied.
//
// The check is a label-set evaluation only. It reads the selector the entity
// declared and the labels the attached Feature carries, and reports whether one
// admits the other; it never inspects what the slot or the fill means.
//
// It walks every attachment the node holds and checks the ones whose instance
// name matches a declared slot, so a slot's selector constrains its fill
// whatever that fill produces. An attachment whose name matches no slot is a
// free attachment and is not selector-constrained.
//
// Catalog misses skip silently (Step 1's resolver guards cover the user-facing
// case), mirroring validateAppliesTo.
func validateSlotSelectors(ctx context.Context, graph *types.IRGraph, catalogLookup resolver.CatalogLookup) ([]AnalysisError, error) {
	var errs []AnalysisError

	if catalogLookup.Component == nil || catalogLookup.Feature == nil {
		return errs, nil
	}

	nodeIDs := graph.Spec.Nodes.Keys()
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		node, ok := graph.Spec.Nodes.Get(nodeID)
		if !ok {
			continue
		}
		attachments, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
		if !ok || attachments.Len() == 0 {
			continue
		}
		comp, found := catalogLookup.Component(node.Component)
		if !found {
			continue
		}
		slotNames := attachments.Keys()
		sort.Strings(slotNames)
		for _, slotName := range slotNames {
			slot, slotOK := comp.Spec.FeaturesSchema.Get(slotName)
			if !slotOK || slot.Selector == "" {
				continue
			}
			entry, _ := attachments.Get(slotName)
			feat, featFound := catalogLookup.Feature(entry.Type)
			if !featFound {
				continue
			}
			match, perr := selector.MatchString(slot.Selector, feat.Metadata.Labels)
			if perr != nil {
				errs = append(errs, AnalysisError{
					Source: nodeID, Target: slotName, Selector: slot.Selector,
					Message:  fmt.Sprintf("attachment slot %q selector parse error: %v", slotName, perr),
					Sentinel: ErrSlotSelectorUnsatisfied,
				})
				continue
			}
			if !match {
				errs = append(errs, AnalysisError{
					Source: nodeID, Target: slotName, Selector: slot.Selector,
					Message: fmt.Sprintf("attachment slot %q on component %q: attached Feature %q labels do not satisfy the slot selector %q",
						slotName, node.Component, entry.Type, slot.Selector),
					Sentinel: ErrSlotSelectorUnsatisfied,
				})
			}
		}
	}
	return errs, nil
}
