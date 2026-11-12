// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// annotateWithRefusals decorates a resolution failure with the catalog files the
// supplied view's load refused, so a tolerated skip never hides WHY an entity the
// Release reaches went missing.
//
// A refused file never enters the catalog, so a Release reaching it fails on the
// REFERENCE — a true statement about a document that is correct, naming nothing
// the caller can act on. The refusals the view recorded are what turn that into
// the constraint the author has to fix, and where exactly one of them declares
// the identity the failing reference names, it leads the answer.
//
// The workspace's own account of where it reads local entities travels with the
// refusals, and answers the other half of "nothing resolved": a refused file
// says a document was read and rejected, while the account says which
// directories were read at all. A caller driving the compiler through this
// protocol is usually not at the keyboard the workspace sits at, so the
// directories that were searched are the fact it has least access to.
//
// It is the annotation the command line applies, from the same home, so a caller
// driving the compiler through this protocol reads the same account of what went
// wrong as a reader at a terminal. This surface composes it into the guidance
// text rather than rendering it through a terminal formatter, which is the whole
// of the difference between the two.
//
// A nil view annotates nothing. The one path that produces one is a call whose
// render happened somewhere else, and such a call has no failure of its own to
// annotate. The workspace is taken rather than a rendered note so a call site
// names what it holds and cannot hand over the wrong text. release is the
// Release the view was built for, so the account names the directories that
// Release reads rather than the workspace's where the two differ.
func annotateWithRefusals(err error, view *catalog.LazyLookup, tw *toolWorkspace, release string) error {
	if err == nil || view == nil {
		return err
	}
	note := ""
	if tw != nil {
		note = tw.rw.LocalCatalogAdviceNote(release)
	}
	return compilerops.AnnotateSkipContext(err, view.SkippedEntities(), note)
}

// annotateResolutionWithRefusals is annotateWithRefusals held to the failures a
// skip record can explain.
//
// The guard is the point. A call that runs every gate can fail for reasons a
// refused catalog file has nothing to do with — a drifted lockfile, a recorded
// deprecation marker, a render that stopped — and attaching the refusals to one
// of those would invite a caller to go and fix a file that did not cause it. A
// call whose failure is a closure resolution BY CONSTRUCTION needs no guard and
// uses annotateWithRefusals directly.
func annotateResolutionWithRefusals(err error, view *catalog.LazyLookup, tw *toolWorkspace, release string) error {
	if !compilerops.IsResolutionFailure(err) {
		return err
	}
	return annotateWithRefusals(err, view, tw, release)
}

// refusalKindOr names the entity kind whose authoring contract a reader must
// open: the kind the promoted refusal's own document declared, or fallback where
// nothing was promoted.
//
// Without it the pointer is derived from the failure, and the failure is about a
// REFERENCE — so it names the kind that reference sits in, which is a document
// the author has no reason to edit. A refusal promoted ahead of that failure has
// already established which document is wrong, so the pointer follows it.
func refusalKindOr(err error, fallback string) string {
	var sce *compilerops.SkipContextError
	if !errors.As(err, &sce) || sce == nil {
		return fallback
	}
	refusal, ok := sce.PromotedRefusal()
	if !ok || refusal.Kind == "" {
		return fallback
	}
	return refusal.Kind
}

// lockfileDriftCondition names the condition a tool refuses on when a Release's
// lockfile no longer matches what the Release resolves to. Every tool refusing on
// it states the condition in these words and goes on to what the refusal means
// for that tool, so the build, the validation and the closure description name
// one condition one way.
const lockfileDriftCondition = "the lockfile no longer matches the dependencies this Release resolves to"

// lockfileDriftClearing is what a caller of this protocol is told about clearing
// a lockfile that no longer matches what its Release resolves to. Every tool that
// refuses on that condition ends its sentence with it, so the build, the
// validation and the closure description name one way to clear one condition.
const lockfileDriftClearing = "Resolving the dependencies " +
	"again clears it. Through this protocol that is the pull tool, called with this same 'release' " +
	"path and no 'upgrade_targets': it reconciles the lockfile against what the Release resolves to " +
	"today, which is what this condition needs. The command line the diagnostic names is that same " +
	"operation at a terminal. Where the pull tool completes and this refusal comes back, the two " +
	"calls resolved over different inputs — most often 'catalog_dirs' given to one and not the " +
	"other, or a workspace configuration edited between them — so call both with the same ones."

// lockfileDriftRefusalSummary is the caller-facing sentence for a tool that reads
// a Release without building it and refuses because the Release's lockfile no
// longer matches what it resolves to — the refusal the matching command prints at a
// terminal. outcome says what the tool did not do, as a clause of its own.
func lockfileDriftRefusalSummary(outcome string) string {
	return outcome + ": " + lockfileDriftCondition +
		", and a build of this Release refuses it on the same ground. " + lockfileDriftClearing
}
