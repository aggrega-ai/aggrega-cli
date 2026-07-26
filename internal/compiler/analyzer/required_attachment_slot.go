// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// validateRequiredAttachmentSlots enforces every attachment slot a node's
// Component catalog entity declares `required: true` against the node's final
// attachment set: the slot's name must be bound by some attachment.
//
// This is the single home of the rule "a required attachment slot is unbound".
// It runs over the finalised graph because the slot is satisfied by the node's
// merged attachment set after the whole cascade — every tier, every scope, plus
// attachments produced by annotation-driven expansion. That is not an
// implementation convenience: a credential attached once at a subsystem,
// boundary or Release scope binds the slot for every component under that
// scope, and the authored component-scope block a deployment leaves empty in
// that shape is exactly what a check over the authored document would reject.
// One entity therefore serves both the per-consumer fill and the shared
// attachment, and the requirement holds for both.
//
// The test is name presence and nothing more. It does not care which scope
// supplied the attachment, what the fill's type is, or what the material means
// — the slot's `selector` is the surface that constrains which Feature may
// bind it, checked separately, and cardinality is fixed at one by the
// attachment set being a map keyed by instance name.
//
// A slot's `required` is NOT the presence mandate the sibling pass enforces,
// and the two are deliberately separate checks over the same resolved set. A
// mandate demands SOME attachment of a named type at any name; a slot demands
// one NAMED binding point be bound. Merging them would answer one question with
// the other's evidence.
//
// Nodes iterate in sorted node order and slots in the catalog entity's author
// order, so the emitted violation set is byte-identical across runs of the same
// input. A component absent from the catalog is skipped: Step 1's guards
// already reject an unresolvable component reference.
func validateRequiredAttachmentSlots(ctx context.Context, graph *types.IRGraph, catalogLookup resolver.CatalogLookup) ([]AnalysisError, error) {
	var errs []AnalysisError

	if catalogLookup.Component == nil {
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
		comp, found := catalogLookup.Component(node.Component)
		if !found {
			continue
		}
		if comp.Spec.FeaturesSchema.Len() == 0 {
			continue
		}

		attachments, _ := graph.Spec.FeatureValuesMapping.Get(nodeID)
		field := componentInstanceFieldPath(nodeID, node.SubsystemPath, node.SynthesizedSubsystem)
		origin := sourceFileFor(graph, nodeID)

		for slotName, slot := range comp.Spec.FeaturesSchema.All() {
			if !slot.Required {
				continue
			}
			if _, bound := attachments.Get(slotName); bound {
				continue
			}
			errs = append(errs, AnalysisError{
				Kind:   AnalysisErrorAttachmentRequirement,
				Source: nodeID, Target: slotName,
				OriginFile: origin,
				Field:      field,
				Message: fmt.Sprintf(
					"component type %q declares the attachment slot %q as required, but no attachment binds it; "+
						"attach a Feature under that name on the component, or at a scope it inherits from",
					node.Component, slotName),
				Sentinel: ErrRequiredSlotUnfilled,
			})
		}
	}
	return errs, nil
}
