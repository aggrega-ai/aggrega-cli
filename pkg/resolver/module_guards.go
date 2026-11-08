// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RunModuleGuards evaluates, over the document of one sub-module the composition
// imports, the rules [RunGuards] asks of the Aggregate it is handed and that
// nothing else asks of a module's own declarations, and returns one finding per
// violation.
//
// [RunGuards] reads the two authoring documents a build is given, and a module's
// document is not one of them: it is loaded from the catalog only as the
// composition is flattened. Most of the rules that pass asks reach a module's
// components another way — the conformance of what a module component declares,
// the sockets its own attachments and the Release's connections name, the slots
// its attachments fill — each through a reading the flattened composition carries.
// The rules asked here are the ones nothing else reaches: those over a component's
// own injection block, the rule over a literal written in an attachment's own
// injection block, and the socket rule over an attachment a module declares at
// one of its own subsystems. Left unasked, a module could deliver what its
// component type forbids, drop a literal its attachment wrote, or wire a socket
// nobody declared, where the same declaration written in the Aggregate halts the
// build.
//
// They are the SAME declared rules [RunGuards] asks, invoked through the same
// traversals over the same presentation of a document — this one built over the
// module's — so a module's declaration cannot be judged by a different standard
// than the Aggregate's. The module's document is presented on its own: no Release
// is in view, because every rule asked here reads what the module wrote and
// nothing an operator did.
//
// Each finding names the module's own document and the key inside it the author
// wrote, so a module author is sent to the file they edit, at the subsystem or the
// component where the declaration sits — not to the Aggregate that imports the
// module, which wrote none of it.
//
// A component type whose declared contract does not compile is reported here as
// [RunGuards] reports it, ahead of the rules, for a type only a module uses.
//
// parameters is the parameter context the module resolved under, which is what
// its declarations were rendered with.
//
// sourceFile is the file the module's document was read from, in the form the
// composition names it by, and empty where the loader named none. Every finding
// positioned at a key carries it, so a surface placing findings inside an open
// document places these inside the module's document rather than inside the
// Aggregate that imports it, whose keys the locator does not name.
//
// Order: the contract failures, then rule by rule in the order [RunGuards] asks
// them, and within a rule in the order its traversal invoked it — so identical
// input yields a byte-identical sequence. Returns no findings for an absent
// document.
func RunModuleGuards(
	cancelled constraint.Cancellation,
	na types.NormalizedAggregate,
	sourceFile string,
	catalog CatalogLookup,
	parameters map[string]any,
) ([]*ResolverError, error) {
	agg := na.Aggregate()
	if agg == nil {
		return nil, nil
	}
	view := newNormalizedView(na, nil, catalog, parameters)
	errs, err := runModuleRules(cancelled, view, ModuleDocumentEntity(agg))
	if err != nil {
		return nil, err
	}
	// The rules' subjects are built over the module's document alone, which
	// records no file, so the file is the one thing added here — and only beside a
	// locator, because a file with no path inside it would place a finding where no
	// rule positioned it.
	for _, f := range errs {
		if f.Field != "" && f.File == "" {
			f.File = sourceFile
		}
	}
	return append(contractCompileFailures(view), errs...), nil
}

// ModuleDocumentEntity names a sub-module's own document as a finding about a key
// inside it names that document. It is the one spelling, shared by the rules
// [RunModuleGuards] asks and by the checks the composition asks of a module's
// components once flattened, so a reader comparing two findings about one module
// reads the same document named the same way.
func ModuleDocumentEntity(agg *types.Aggregate) string {
	return fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
}

// runModuleRules asks the rules [RunModuleGuards] states, over one module's
// document, each finding naming that document.
//
// Each traversal call names its rule as a literal argument, for the reason
// [runDeclaredRules] states: a rule reached through a parameter is one the
// derivation of which rules a caller invokes cannot attribute.
func runModuleRules(
	cancelled constraint.Cancellation,
	view *normalizedView,
	document string,
) ([]*ResolverError, error) {
	coordinates := newCoordinateSet(view)
	facts := constraint.NormalizedView(view)

	var errs []*ResolverError
	rule := func(objections []constraint.Objection, err error) error {
		if err != nil {
			return err
		}
		errs = append(errs, FindingsFromObjections(objections, document)...)
		return nil
	}

	// Once per component.
	if err := rule(constraint.RunOnNodes(
		cancelled, coordinates, facts, selfInjectBlockAllowed)); err != nil {
		return nil, err
	}

	// Once per delivery mechanism a component's injection block dictates.
	if err := rule(constraint.RunOnNodeMechanisms(
		cancelled, coordinates, coordinates, facts, selfInjectMechanismAllowed)); err != nil {
		return nil, err
	}
	if err := rule(constraint.RunOnNodeMechanisms(
		cancelled, coordinates, coordinates, facts, selfInjectKeyWrittenOnce)); err != nil {
		return nil, err
	}

	// Once per key a component delivers through one mechanism.
	if err := rule(constraint.RunOnNodeMechanismDeliveryKeys(
		cancelled, coordinates, coordinates, coordinates, facts,
		selfInjectReferenceIsPublished)); err != nil {
		return nil, err
	}
	if err := rule(constraint.RunOnNodeMechanismDeliveryKeys(
		cancelled, coordinates, coordinates, coordinates, facts,
		selfInjectKeyNotAlsoOnAConnection)); err != nil {
		return nil, err
	}
	if err := rule(constraint.RunOnNodeMechanismDeliveryKeys(
		cancelled, coordinates, coordinates, coordinates, facts,
		selfInjectKeyNotOnTwoMechanisms)); err != nil {
		return nil, err
	}

	// Once per delivery mechanism of every injection block the module's document
	// writes. Only the literal rule is asked over this axis here: the shape and the
	// ambiguity rules refuse what the Aggregate schema already refuses when a
	// module's document is read, while a literal in an attachment's own block is
	// admitted by the schema and refused by this rule alone.
	if err := rule(constraint.RunOnInjectDeliveries(
		cancelled, coordinates, facts, attachmentInjectEntryNamesAKey)); err != nil {
		return nil, err
	}

	// Once per attachment a subsystem of the module's document carries.
	if err := rule(constraint.RunOnSubsystemAttachments(
		cancelled, coordinates, coordinates, facts, subsystemAttachmentSocketDeclared)); err != nil {
		return nil, err
	}

	return errs, nil
}
