// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// dispatchedStageAxes is every axis a rule declared over a build's grouping is
// invoked once per: the evaluated assembly paths groups landed at, and the
// providers that assembled at each.
//
// It is named as one set because an invocation needs both together. A value
// satisfying one of them cannot be handed to the traversal at all, and the
// failure a partial value would produce is a rule invoked zero times — which
// reports exactly what a rule that ran and approved reports.
type dispatchedStageAxes interface {
	constraint.AssemblyPathEnumerator
	constraint.AssemblyGroupProviderEnumerator
}

// runDispatchedStageRules invokes the rules declared over how a build grouped its
// rendered nodes, and returns what they raised as the render's own halt.
//
// It runs once every node has been sorted into its group and before any group
// assembles, which is the first point the facts it reads exist and the last point
// at which acting on them still saves work: the providers that met at one path
// and the boundaries behind each group are read from the attribution the grouping
// records, so a reading placed earlier would ask about a grouping still being
// built, and one placed after assembly would run over output the answer was
// always going to discard.
//
// # Why the halt is an operator fault
//
// Every rule here is about where a boundary's own path declaration sent its
// output, and that declaration is the operator's: it is written on the boundary,
// in the deployment document, and the remedy is an edit there. So the finding is
// attributed to the operator rather than to a catalog entity, which is what sends
// a reader to the document they can actually change.
//
// The cancellation signal is the caller's own, so a walk over a large grouping
// stops within one boundary of the caller asking it to.
func runDispatchedStageRules(
	ctx context.Context,
	view constraint.DispatchedView,
	coordinates dispatchedStageAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	raised, err := constraint.RunOnAssemblyPathProviders(
		ctx.Err, coordinates, coordinates, view,
		AssemblyPathServesOneProvider,
	)
	if err != nil {
		return err
	}
	return objectionsAsOpsHalt(
		raised, "assembly paths reached by more than one provider")
}

// objectionsAsOpsHalt renders what a rule about a build's grouping raised as the
// render's halt, attributing the fault to how the deployment was configured.
//
// It is the sibling of [objectionsAsHalt] and of [objectionsAsTranslationHalt],
// and shares the whole of their per-objection rendering; what it does not share
// is the category, which is the one thing the three readings genuinely differ on.
// A rule about what an entity declares sends an author to a catalog document; a
// rule about a marker the host stamped has no such document; a rule about where a
// boundary's output landed sends an operator to the deployment document, which is
// the only place a directory declaration can be changed.
func objectionsAsOpsHalt(objections []constraint.Objection, envelope string) error {
	return objectionsAsCategorisedHalt(objections, cerrors.OpsError, envelope)
}
