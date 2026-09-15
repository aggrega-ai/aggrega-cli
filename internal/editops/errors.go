// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package editops

import (
	"errors"
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrEditRefusedInvalidResult reports that the edit was composed and would have
// left the document in a state its own schema refuses, so nothing was written.
//
// It is the condition that keeps this package inside what an author could type
// by hand. Every other refusal here says a request did not address an edit;
// this one says the request addressed one and the result was not admissible —
// the authoring schema would have refused the same document had it been typed.
// The failure it wraps carries which field was wrong and where, so a reader is
// told what to change rather than only that something was.
//
// It is a registered condition, and the other refusals in this package are not.
// The distinction is the one the registry draws: this names a rule with a code a
// reader quotes and looks up, and it is the rule a caller has to be able to
// match on in order to tell "your request was malformed" from "your request was
// fine and the document would not have been". The refusals below say one
// request's arguments do not address a place an entry can go, which every
// surface carrying this capability already has a channel for.
func ErrEditRefusedInvalidResult() error { return errEditRefusedInvalidResult }

var errEditRefusedInvalidResult = cerrors.Register("edit-refused-invalid-result", cerrors.Meta{
	ShortCode: "E0275",
	Message:   "the edit was refused because the resulting document would not be valid, and nothing was written",
	RulePlace: cerrors.RuleNoStageReaches("A document edit evaluates the rule, as the spliced result is validated against its own " +
		"schema before anything is written and refused when that schema would not accept it. The " +
		"edit shapes one document and compiles nothing, so no declared stage of a compilation holds " +
		"the result being judged."),
})

// The conditions on which no edit is applied.
//
// They are unexported values rather than registered halting conditions. None of
// them halts a build: each says that one request's arguments do not address a
// place an entry can go in one document, which is what every surface carrying
// this capability already has a refusal channel for. Carrying the reason as an
// error value lets each surface report it in its own channel without anything
// acquiring a code it would then have to keep forever.
//
// Each sentence names what the caller must change, in the vocabulary of the
// request rather than of any one surface's spelling of it — so a caller reaching
// this over a protocol is not told to pass a command-line flag it has no way to
// pass.
var (
	errNoWorkspace = errors.New("no workspace was named, so there is no root to resolve the document against")

	errNoFile = errors.New("no document was named: name the file to edit, relative to the root of the workspace")

	errFileOutsideWorkspace = errors.New("the document named is not inside the workspace, and an edit is " +
		"only applied to a document the workspace carries")

	errFileUnreadable = errors.New("the document named could not be read")

	errFileUnparseable = errors.New("the document named is not well-formed YAML, so it has no structure " +
		"to place an entry in")

	errNoDocumentKind = errors.New("the document named declares no kind, so there is no schema to check " +
		"the result of an edit against")

	errUnknownDocumentKind = errors.New("the document named declares a kind that is not one this product " +
		"validates, so there is no schema to check the result of an edit against")

	// Raised through unknownKind, which is what names the admissible set
	// alongside it. Wrapping this value directly would refuse without saying
	// what may be named instead.
	errUnknownKind = errors.New("the edit named is not one this capability applies")

	errNoNodePath = errors.New("no node was named: give the path to the node the edit is about, from the " +
		"document root, as its segments")

	errNoEntryName = errors.New("no name was given for the entry the edit adds")

	errNoSocket = errors.New("no socket was named: wiring an edge needs the socket on the named node that " +
		"the connection is declared inside")

	errValueNotScalar = errors.New("the value given is a mapping or a sequence, and setting a value writes " +
		"a scalar; adding a block is not one of the edits this capability applies")

	errValueNotAReference = errors.New("no reference was given: this edit records the entity the entry " +
		"requests, or the node an edge points at, as a non-empty string")

	errContainerNotAMapping = errors.New("the node path reaches a value the document carries as a scalar " +
		"or a sequence, and an entry cannot be added inside one")

	errContainerFlowStyle = errors.New("the node path reaches a collection written in flow style, and " +
		"adding an entry to one rewrites bytes the author already wrote rather than adding a line")

	errNodeAbsent = errors.New("the node path reaches nothing in the document named")

	errEntryPresentDiffers = errors.New("an entry of that name is already there carrying something else, " +
		"and replacing what an author wrote is not one of the edits this capability applies")
)

// unknownKind refuses an edit outside the admissible set and names the set the
// caller may ask for instead.
//
// A caller that names no edit at all is told what it may name. A caller that
// named one and got it wrong is the caller that most needs the same list, so it
// is told the same thing rather than only that its request was not admissible.
//
// The names are read from the enumeration rather than written here. A list
// written into a refusal is a second statement of the bound, and the two would
// agree until an edit was added to one of them.
//
// It composes the sentence rather than replacing it, so the condition is still
// the one value a caller matches on, and still unregistered: it names no rule a
// reader would look up, and nothing it refuses halts a build.
func unknownKind(named Kind) error {
	return fmt.Errorf("%w: %q; name one of %s", errUnknownKind, string(named), admissibleEdits())
}

// admissibleEdits renders the admissible edits as the fragment a refusal reads
// out, joining all but the last with commas and the last with "or".
//
// A surface composing a refusal of its own renders the same set at its own
// boundary, and that near-duplicate is deliberate: what may never be restated is
// the BOUND, which every one of them reads from the enumeration, while the
// sentence carrying it is each surface's own to write in its own voice.
func admissibleEdits() string {
	kinds := Kinds()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
