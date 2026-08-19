// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	stderrors "errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// objectionsAsHalt renders what a rule raised as the render's own halt, and nil
// when it raised nothing.
//
// Each objection becomes a diagnostic of its own carrying its own position: the
// file the subject was authored in, and the authored key inside it. That is the
// whole reason the findings are not folded into one message — a position is
// something an author can navigate to, and one message naming several positions
// can be navigated to none of them.
//
// One objection is returned as itself rather than as a set of one, so the
// ordinary case reads as the single diagnostic it is. Several are joined under an
// envelope, which is the shape a display surface already renders every member of.
// The envelope wording is the caller's because only the caller knows what its own
// findings are about; everything above it is the same wherever objections are
// turned into a halt.
//
// # Why this is one function and not one per site
//
// The per-objection wrap is a single mechanic: the category a raised objection
// carries, the position it is reported at, and the message an author reads are
// each derived from the objection the same way whatever rule raised it. A second
// copy would agree on the day it was written and be free to drift the moment
// either half gains a term — a severity read, a second locator segment, a change
// to how a detail is rendered — and the drift would surface as two rules
// reporting the same class of fault in two shapes, which is exactly what a reader
// comparing them cannot tell from a deliberate difference.
//
// The category is fixed rather than taken from the caller. Every rule turned into
// a halt here is about what an entity declares, so the fault is in a catalog
// document and the remedy is an edit to one; a caller wanting another category
// would be reporting a different class of fault and owes its own reading rather
// than a parameter here.
func objectionsAsHalt(objections []constraint.Objection, envelope string) error {
	if len(objections) == 0 {
		return nil
	}
	halts := make([]error, 0, len(objections))
	for _, o := range objections {
		halts = append(halts, cerrors.Wrap(
			o.Sentinel(),
			cerrors.CatalogError,
			cerrors.SourceLocation{File: o.SourceFile(), Locator: o.Locator()},
			constraint.ObjectionMessage(o),
		))
	}
	if len(halts) == 1 {
		return halts[0]
	}
	return cerrors.Wrap(
		stderrors.Join(halts...),
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		envelope,
	)
}
