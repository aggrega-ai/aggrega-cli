// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// It is the annotation the command line applies, from the same home, so a caller
// driving the compiler through this protocol reads the same account of what went
// wrong as a reader at a terminal. This surface composes it into the guidance
// text rather than rendering it through a terminal formatter, which is the whole
// of the difference between the two.
//
// A nil view annotates nothing. The one path that produces one is a call whose
// render happened somewhere else, and such a call has no failure of its own to
// annotate.
func annotateWithRefusals(err error, view *catalog.LazyLookup) error {
	if err == nil || view == nil {
		return err
	}
	return compilerops.AnnotateSkipContext(err, view.SkippedEntities())
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
func annotateResolutionWithRefusals(err error, view *catalog.LazyLookup) error {
	if !compilerops.IsResolutionFailure(err) {
		return err
	}
	return annotateWithRefusals(err, view)
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
