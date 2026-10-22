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
// at which acting on them still saves work: the providers that met at one path,
// the boundaries behind each group, and what each group's elected engine claimed
// are read from the attribution and the election the grouping records, so a reading
// placed earlier would ask about a grouping still being built, and one placed after
// assembly would run over output the answer was always going to discard.
//
// # Why the attribution differs between the rules and is not one category
//
// The rules here do not all report the same kind of fault, and the category is
// what sends a reader to the right place. A rule about where a boundary's own path
// declaration sent its output is the operator's: the declaration is written on the
// boundary, in the Release document, and the remedy is an edit there. A rule
// about a provider whose serving plugin does not offer the assembly that provider
// claims is not — there is nothing in the Release document to correct, and the
// remedy is to serve the provider through a plugin that offers both, which makes
// it a fault of the translation. So each reading is rendered under its own
// attribution rather than under one that would be wrong for some of them.
//
// Two readings sharing a category are still two readings, because the envelope a
// halt opens with names what was read. One envelope covering both would have to
// describe neither precisely, and the envelope is the first line an author sees.
//
// Each reading runs in the order declared below, and the first to raise anything
// halts the build. That is the same shape a single reading has when it raises
// objections about more than one group: the build stops at the first fault it can
// name.
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
		AssemblyPathNamesADirectoryBeneathTheOutputRoot,
	)
	if err != nil {
		return err
	}
	if halt := objectionsAsOpsHalt(
		raised, "boundary output directories not beneath the output root",
	); halt != nil {
		return halt
	}
	raised, err = constraint.RunOnAssemblyPathProviders(
		ctx.Err, coordinates, coordinates, view,
		AssemblyPathServesOneProvider,
	)
	if err != nil {
		return err
	}
	if halt := objectionsAsOpsHalt(
		raised, "assembly paths reached by more than one provider",
	); halt != nil {
		return halt
	}
	raised, err = constraint.RunOnAssemblyPathProviders(
		ctx.Err, coordinates, coordinates, view,
		AssemblyGroupProviderHasItsDeclaredAssembler,
	)
	if err != nil {
		return err
	}
	if halt := objectionsAsTranslationHalt(
		raised, "dispatch groups whose provider offers no boundary assembler",
	); halt != nil {
		return halt
	}
	raised, err = constraint.RunOnAssemblyPathProviders(
		ctx.Err, coordinates, coordinates, view,
		AssemblyGroupBoundariesAgreeOnAssemblyName,
	)
	if err != nil {
		return err
	}
	if halt := objectionsAsTranslationHalt(
		raised, "merged dispatch groups whose boundaries disagree on the assembly name",
	); halt != nil {
		return halt
	}
	raised, err = constraint.RunOnAssemblyPathProviders(
		ctx.Err, coordinates, coordinates, view,
		AssemblyGroupDerivesEachNameOnce,
	)
	if err != nil {
		return err
	}
	if halt := objectionsAsTranslationHalt(
		raised, "dispatch groups whose contributors derive one name more than once in one key space",
	); halt != nil {
		return halt
	}
	raised, err = constraint.RunOnAssemblyPathProviders(
		ctx.Err, coordinates, coordinates, view,
		AssemblyGroupPlacesEachContributorInADirectoryOfItsOwn,
	)
	if err != nil {
		return err
	}
	return objectionsAsOpsHalt(
		raised, "boundary-level Feature instances whose name is not a directory name")
}

// objectionsAsOpsHalt renders what a rule about a build's grouping raised as the
// render's halt, attributing the fault to how the Release was configured.
//
// It is the sibling of [objectionsAsHalt] and of [objectionsAsTranslationHalt],
// and shares the whole of their per-objection rendering; what it does not share
// is the category, which is the one thing the three of them genuinely differ on.
// A rule about what an entity declares sends an author to a catalog document; a
// rule about a marker the host stamped has no such document; a rule about where a
// boundary's output landed sends an operator to the Release document, which is
// the only place a directory declaration can be changed — and so does a rule
// about what an attachment a boundary declares delivered, since that attachment
// is written there and nowhere else.
func objectionsAsOpsHalt(objections []constraint.Objection, envelope string) error {
	return objectionsAsCategorisedHalt(objections, cerrors.OpsError, envelope)
}
