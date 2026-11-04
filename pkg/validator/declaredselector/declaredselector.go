// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package declaredselector checks the label selectors an entity's own document
// declares on its dependency sockets and attachment slots, and reports each one
// the selector grammar refuses.
//
// A selector is otherwise read only where something reaches it: an edge drawn
// through the socket, an attachment filling the slot. So a selector the grammar
// refuses, on a socket nothing wires or a slot nothing fills, would be read by
// nothing at all. Asked here of the document as it is validated, every declared
// selector is read whether or not anything reaches it.
//
// A refusal is reported under the condition a build reports the same selector
// under when it reaches it — selector violation for a socket's selector,
// attachment slot selector unsatisfied for a slot's — in the same words, naming
// the selector, the socket or slot that declares it and the grammar's reason. It
// is filed as a fault in a catalog entity and positioned at the document's file.
//
// A Feature's applicability selectors are a different declaration and are not
// checked here.
//
// # Why the checks are run from this package
//
// The run sits in a package of its own because neither of the two places it
// would otherwise go can hold it. Not the validate package that calls it: the
// constraint framework derives exactly one pipeline package, as the one package
// running checks from which every stage of a compilation is reachable, and the
// validate package reaches every stage through the isolated render it drives, so
// a check run there would give the derivation two answers and it refuses to pick
// one. Not this package's parent: the checks are declared beside the two
// conditions they report, in the compiler's analyzer, and the analyzer already
// depends on the parent through the resolver, so the parent cannot depend on the
// analyzer in turn. Nor the analyzer itself: the framework takes the first
// package, in the order it reads the module's directories, that runs checks at
// the earliest stage as the loader whose callers it measures every surface's
// reach into, and the analyzer is read ahead of the resolver that holds that
// place, so running the checks there would change what the framework measures.
package declaredselector

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// ComponentRefusals returns one error for each selector the Component's own
// document at path declares that the selector grammar refuses — the sockets'
// selectors first, in author order, then the slots' — and nil when the grammar
// reads every one.
//
// comp is the Component as that document declares it, before the chain it
// extends is merged in: a selector its base declares is reported when the base is
// checked, against the base's own file.
func ComponentRefusals(path string, comp *types.Component) []error {
	view := validator.PresentComponentDeclaredSelectors(path, comp)
	objections := constraint.RunOnDocument(path, view, analyzer.ComponentSocketSelectorsAreReadable)
	objections = append(objections,
		constraint.RunOnDocument(path, view, analyzer.ComponentSlotSelectorsAreReadable)...)
	return refusalsFrom(objections)
}

// FeatureRefusals is ComponentRefusals for the Feature's own document at path.
func FeatureRefusals(path string, feat *types.Feature) []error {
	view := validator.PresentFeatureDeclaredSelectors(path, feat)
	objections := constraint.RunOnDocument(path, view, analyzer.FeatureSocketSelectorsAreReadable)
	objections = append(objections,
		constraint.RunOnDocument(path, view, analyzer.FeatureSlotSelectorsAreReadable)...)
	return refusalsFrom(objections)
}

// refusalsFrom renders each objection as the halt a validate surface reports,
// through the one per-objection wrap every surface reporting a raised objection
// uses, filed as a fault in a catalog entity.
//
// The category is the catalog's rather than the one a build files the same
// selector under. A build files a finding under the document whose wiring
// reached the selector; here nothing reached it, and the edit is in the catalog
// entity that declares it — which is where the rule over the same document's
// schema block files its finding too.
func refusalsFrom(objections []constraint.Objection) []error {
	if len(objections) == 0 {
		return nil
	}
	out := make([]error, 0, len(objections))
	for _, o := range objections {
		out = append(out, constraint.ObjectionHalt(o, cerrors.CatalogError))
	}
	return out
}
