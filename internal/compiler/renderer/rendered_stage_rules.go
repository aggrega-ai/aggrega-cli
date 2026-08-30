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
// It also runs after the cross-boundary link pass, which is what the placeholder
// rule here needs: a resolved link replaces its own placeholder, so a reading taken
// before resolution would object to every marker the pass was about to remove.
// The flush does not change bytes — it moves each group's files into the result
// under their write prefix — so running after it observes exactly the
// post-resolution bytes.
//
// # The two rules are asked in order, and the order is the reason
//
// The collision reading comes first and its halt is returned before the
// placeholder reading is asked at all. Two contributors meeting on one path leave
// one of their two files out of the assembly, and a placeholder standing in the
// file that was left out is a consequence of the collision rather than a finding
// of its own — so asking the placeholder rule first would report the symptom and
// leave the cause unmentioned.
//
// # The two halts carry different attributions
//
// A collision is a fault in what two catalog documents declare: two Recipes chose
// output names that meet, and the remedy is an edit to one of them. A surviving
// placeholder is not something an author wrote — the host stamps the marker and a
// provider's link resolution is supposed to replace it, so one left standing means
// resolution never reached it, a fault on the translation side with nothing in any
// catalog document to correct. So the two readings render through the two
// category-bearing entries rather than through one: the wording and the position
// are the same mechanic, and only the attribution differs.
//
// # The two absent-presentation conditions are DIFFERENT questions
//
// The collision reading HALTS, so it cannot treat an absent presentation as an
// ordinary outcome the way an advisory can. The presentation it reads extends one
// the render's CALLER supplies, and the chain is cumulative — so the two cases are
// separated rather than collapsed.
//
// A caller that supplied NO presentation gets no reading. That caller assembled a
// render's input directly, which is asking for a render rather than for a build.
//
// 🔴 That is a real bound and it is stated rather than implied: such a caller gets
// no collision reading, so two contributors meeting on one output path assemble for
// it instead of halting, and whichever of their two files sorts first by contributor
// is the one it receives. The build path supplies the presentation, so the bound
// does not reach a build; it reaches exactly the callers that assemble a render's
// input themselves.
//
// A presentation absent BENEATH a present caller presentation is not an outcome, it
// is a chain that broke inside this package: the attribution, the presentation and
// the enumeration are completed together, unconditionally, at one site. It halts
// rather than returning nothing, because the two are indistinguishable to any reader
// downstream and silence is indistinguishable from approval. Its wording says the
// fault is the compiler's rather than the author's, because no category attributes a
// fault to the compiler reaching a check without its inputs.
//
// The cancellation signal is the caller's own, so a long walk over a large
// output stops within one boundary of the caller asking it to.
func runRenderedStageRules(
	ctx context.Context,
	callerPresentationSupplied bool,
	view constraint.RenderedView,
	coordinates renderedStageAxes,
) error {
	if coordinates == nil {
		return nil
	}
	if view == nil {
		if !callerPresentationSupplied {
			return nil
		}
		return cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{},
			"the assembled output could not be checked for two contributors writing one output "+
				"path — internal error: the check's inputs were incomplete. This is a fault in the "+
				"compiler rather than in the catalog, so there is nothing to correct in any document",
			nil, nil)
	}
	collided, err := constraint.RunOnBoundaryProviderPaths(
		ctx.Err, coordinates, coordinates, coordinates, view,
		AssembledPathWrittenByOneContributor,
	)
	if err != nil {
		return err
	}
	if halt := objectionsAsHalt(
		collided, "assembled output paths written by more than one contributor",
	); halt != nil {
		return halt
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
