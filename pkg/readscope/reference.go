// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package readscope

import (
	"fmt"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// ErrReferenceLeadsOutOfWorkspace is the halt raised when a reference a document
// makes to another file — a Release's aggregate, manifest or extends, a Manifest's
// extends, a Release's parameters import — leads out of the directory it must stay
// inside: written as a path that climbs out of it, or as one that a link on the way
// carries out. That directory is the workspace for a document read inside it, and
// the directory of the Release or Manifest itself for one named from outside the
// workspace, on the command line or by the workspace configuration.
//
// Nothing is read from the file it names. The refusal names the reference as it
// is written, the document it is written in wherever the reader resolving it
// holds that document, the directory it is resolved from, where it leads, the
// directory it must stay inside, and the link that carries it out where one does.
//
// Category: OPS_ERROR. Every reference this halt judges is written in a Release or
// a Manifest, so the fix is in one of those two documents. Match with
// errors.Is(err, ErrReferenceLeadsOutOfWorkspace()).
func ErrReferenceLeadsOutOfWorkspace() error { return errReferenceLeadsOutOfWorkspace }

var errReferenceLeadsOutOfWorkspace = cerrors.Register("reference-leads-out-of-workspace", cerrors.Meta{
	ShortCode: "E0324",
	Message:   "a reference a document makes to another file leads out of the directory it must stay inside",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated where the product opens the file a reference " +
		"names — a Release's aggregate, manifest and extends, a Manifest's extends and a Release's parameters " +
		"import — while it works out which documents a compilation reads. That happens ahead of every declared " +
		"stage, and what the rule reads is where a path leads on disk rather than a document a compilation holds."),
})

// Reference is one reference a document makes to another file, as the reader
// resolving it holds it.
type Reference struct {
	// Document is the file the reference is written in. It is empty where the
	// reader resolving the reference holds only the directory it is resolved from:
	// a Release's aggregate and manifest pointers are resolved after the Release's
	// extends chain is folded, so the link that wrote one is no longer known.
	Document string
	// Anchor is the directory of the document the resolution started at — the
	// Release a command or a request names, or the configuration names, or a
	// Manifest named on its own — and it decides the root every reference the
	// resolution reaches is held to, those written in the Releases an extends chain
	// leads to included. Empty holds the reference to the workspace root.
	Anchor string
	// From is the directory the reference is resolved from.
	From string
	// Key is the key the reference is written under, as a dotted path in its
	// document: ".spec.aggregate", ".spec.parameters.$import".
	Key string
	// Value is the reference as written.
	Value string
	// Target is the path the reference resolves to, the file about to be read.
	Target string
}

// CheckReference reports whether the file a reference names may be read: nil when
// it may, and the [ErrReferenceLeadsOutOfWorkspace] halt when it leads out of the
// root it is held to, given the workspace at workspaceRoot.
//
// The root is the one [writescope.RootFor] picks for the reference's Anchor, the
// one answer to which side of the workspace a directory lies on: the workspace
// root where the anchor lies inside the workspace, and the anchor itself where it
// lies outside. A document named from outside the workspace — a Release or a
// Manifest the owner typed on the command line, or the configuration named — is
// therefore a root of its own, its own directory, exactly as a directory typed or
// named outside the workspace is for what the product composes beneath it; no
// workspace is looked for around it. The target is judged against that root by
// [pathscope.ResolvesInside]: the deepest part of it that exists, with its links
// resolved, must be the root or lie beneath it. A reference that climbs out as
// written is refused as one that a link carries out is, unlike a read beneath a
// directory read as a whole ([CheckRead]), which only a link can carry out.
//
// With no workspace root nothing is held: a document read with no workspace around
// it has no boundary to be held to.
func CheckReference(workspaceRoot string, ref Reference) error {
	if workspaceRoot == "" || ref.Target == "" {
		return nil
	}
	root := writescope.RootFor(workspaceRoot, ref.Anchor)
	res, inside := pathscope.ResolvesInside(root, ref.Target)
	if inside {
		return nil
	}
	namedOutside := root != workspaceRoot
	values := []string{fmt.Sprintf("%s: %s", ref.Key, ref.Value)}
	if ref.Document != "" {
		values = append(values, "written in "+absolute(ref.Document))
	}
	values = append(values,
		"resolved from "+absolute(ref.From),
		"which leads to "+absolute(ref.Target),
	)
	if namedOutside {
		values = append(values, fmt.Sprintf("outside %s, the directory of a document named outside the workspace %s",
			absolute(root), absolute(workspaceRoot)))
	} else {
		values = append(values, "outside the workspace "+absolute(root))
	}
	if res.Link != "" {
		values = append(values, fmt.Sprintf("the link %s leads to %s", res.Link, res.LinkLeadsTo))
	}
	return cerrors.Raise(errReferenceLeadsOutOfWorkspace, cerrors.OpsError,
		cerrors.SourceLocation{File: ref.Document, Locator: ref.Key},
		cerrors.ComposeMessage(ErrReferenceLeadsOutOfWorkspace(), values, referenceRemedyFor(absolute(root), namedOutside)), nil)
}

// referenceRemedyFor is what the refusal tells its reader, in terms of what they
// can change, for a reference that must stay inside root — the workspace, or,
// where namedOutside, the directory of a document named from outside it.
//
// It names two remedies because a reference names two kinds of file. An Aggregate
// is an entity a catalog directory holds, so one kept elsewhere is reached by its
// name from a catalog directory the configuration lists; a Manifest, a Release and
// a parameters file are read beside the Release that names them and are never
// looked up by name, so for those the file comes inside, or the document is opened
// from a workspace that holds both.
func referenceRemedyFor(root string, namedOutside bool) string {
	reach := "A document in the workspace reaches only files inside " + root + ": move the file there — or, " +
		"where a link carries the reference out, make the link lead there — or open the workspace at a directory " +
		"holding both documents."
	if namedOutside {
		reach = "A Release or a Manifest named outside the workspace — on the command line, or by the workspace " +
			"configuration — reaches only files inside its own directory " + root + ": move the file there — or, " +
			"where a link carries the reference out, make the link lead there — or name the document from a " +
			"workspace holding both documents."
	}
	return "nothing was read from that file. " + reach + " An Aggregate kept outside that directory is reached " +
		"by its name rather than a path, from a catalog directory the workspace configuration lists"
}
