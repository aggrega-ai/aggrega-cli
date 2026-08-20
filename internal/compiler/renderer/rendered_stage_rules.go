// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// renderedStageAxes is every axis a rule declared over a render's assembled
// output is invoked once per: the boundaries that fed output, the providers that
// assembled within each, and the paths each pair wrote.
//
// It is named as one set because an invocation needs all three together. A value
// satisfying two of them cannot be handed to the traversal at all, and the
// failure a partial value would produce is a rule invoked zero times — which
// reports exactly what a rule that ran and approved reports.
type renderedStageAxes interface {
	constraint.BoundaryEnumerator
	constraint.ProviderEnumerator
	constraint.EmittedPathEnumerator
}

// runRenderedStageRules invokes the rules declared over what a render assembled,
// and returns what they raised as the render's own halt.
//
// It runs after the assembly has flushed its groups into the result, which is the
// first point the facts it reads exist: the paths a provider wrote within a
// boundary and the bytes standing at one of them are read from the attribution
// the flush records, so a reading placed before it would ask about output that
// had not been attributed yet and would be answered with nothing.
//
// It also runs after the cross-boundary link pass, which is what the one rule
// here needs: a resolved link replaces its own placeholder, so a reading taken
// before resolution would object to every marker the pass was about to remove.
// The flush does not change bytes — it moves each group's files into the result
// under their write prefix — so running after it observes exactly the
// post-resolution bytes.
//
// # Why the halt is a translation fault and not a catalog one
//
// A surviving placeholder is not something an author wrote. The host stamps the
// marker and a provider's link resolution is supposed to replace it, so one left
// standing means resolution never reached it — a fault on the translation side,
// with nothing in any catalog document to correct. That is the category the
// condition is declared with, and it is why this reading renders its objections
// itself rather than through the shared catalog-category rendering: the wording
// and the position are the same mechanic, and only the attribution differs.
//
// The cancellation signal is the caller's own, so a long walk over a large
// output stops within one boundary of the caller asking it to.
func runRenderedStageRules(
	ctx context.Context,
	view constraint.RenderedView,
	coordinates renderedStageAxes,
) error {
	if view == nil || coordinates == nil {
		return nil
	}
	raised, err := constraint.RunOnBoundaryProviderPaths(
		ctx.Err, coordinates, coordinates, coordinates, view,
		CrossBoundaryLinkPlaceholderResolved,
	)
	if err != nil {
		return err
	}
	return objectionsAsTranslationHalt(
		raised, "assembled output retaining unresolved cross-boundary link placeholders")
}

// objectionsAsTranslationHalt renders what a rule about produced output raised as
// the render's halt, attributing the fault to the translation rather than to a
// catalog document.
//
// It is the sibling of [objectionsAsHalt] and shares the whole of that function's
// per-objection rendering; what it does not share is the category, which is the
// one thing the two readings genuinely differ on. A rule about what an entity
// declares sends an author to a document to edit; a rule about a marker the host
// stamped and a provider failed to resolve has no such document, so reporting it
// as a catalog fault would send an author looking through a Recipe that is not at
// fault.
//
// One objection is returned as itself and several are joined under an envelope,
// for the reason the shared rendering gives: a position is something to navigate
// to, and one message naming several of them can be navigated to none.
func objectionsAsTranslationHalt(objections []constraint.Objection, envelope string) error {
	return objectionsAsCategorisedHalt(objections, cerrors.TranslationError, envelope)
}
