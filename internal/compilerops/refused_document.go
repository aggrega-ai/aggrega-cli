// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// RefusedDocument states why the catalog refused one document as a single
// categorised frame: the document is the source, and the document's own fault
// is the message.
//
// A refusal reaches a reader as a chain of frames, and every one of them is
// about the same document: the step that read it ("unmarshal Feature"), then
// the constraint it violates, each carrying the category and the document's
// path again. Rendered frame by frame, the one fault an author has to fix is
// printed as several nested copies of itself. This is the refusal said once, and
// it is the one home of that shape, so a terminal and a single-line message read
// the same fault in the same words.
//
// path is the document as the surface names it — a workspace-relative path where
// the surface reports those, the path the catalog read otherwise — because the
// spelling of a reported path is the surface's rule and not this function's.
//
// The frame carries no chain a formatter would walk: the fault is already in its
// message, and a frame beneath it would print that fault a second time. The
// refusal is still what errors.Is and errors.As reach through it, so a
// registered condition's short code reaches the head and a caller matching the
// condition the refusal raised still matches it.
func RefusedDocument(path string, refusal error) *cerrors.CompilerError {
	fault := documentFault(refusal)
	return cerrors.Wrap(&statedFault{fault: fault, refusal: refusal}, categoryOf(refusal),
		cerrors.SourceLocation{File: path}, fault)
}

// RefusedDocumentLine renders RefusedDocument on one line, with the refusal's
// short code in its bracketed head: `[CATALOG_ERROR E0215] <path>: <fault>`.
//
// It is the shape a message that has no formatter of its own carries, which is
// why the short code is placed here rather than left to a formatter that would
// not run.
func RefusedDocumentLine(path string, refusal error) string {
	return refusalReason(RefusedDocument(path, refusal))
}

// RefusedReferenceLine is the one-line message for a reference that resolved to
// no entity because the one document declaring what it names was refused: that
// refusal first, as RefusedDocumentLine states it, then why the refusal explains
// the reference resolving to nothing, then the reference itself.
//
// It is the single-line counterpart of the note AnnotateSkipContext writes for a
// promoted refusal, for a surface whose answer is one line, so every such surface
// says it in the same words.
func RefusedReferenceLine(path string, refusal error, reference string) string {
	return fmt.Sprintf("%s; note: %s, and %q resolved to no entity", RefusedDocumentLine(path, refusal), RefusedDocumentNote, reference)
}

// RefusedCatalogDocument reports the one document a strict catalog load stopped
// on, as a refusal record holding that document's path and the refusal the load
// raised for it, and whether the failure is one.
//
// A strict load reports a per-file refusal beneath a frame naming the directory
// it was walking, so the refusal is the first categorised frame beneath that one
// and its source names the document. A failure that is not about one document —
// a directory the walk could not read, the collected findings of the pass that
// validates every entity's schema — has no such frame, or has one naming no
// document, and is reported false so the caller renders it as it always has.
//
// The walk follows single-cause links only. A frame carrying several causes at
// once is several failures, and descending into one of them would single out a
// document the failure as a whole is not about.
func RefusedCatalogDocument(err error) (catalog.SkippedEntity, bool) {
	var load *cerrors.CompilerError
	if !stderrors.As(err, &load) || load == nil {
		return catalog.SkippedEntity{}, false
	}
	for cur := load.Unwrap(); cur != nil; cur = stderrors.Unwrap(cur) {
		doc, ok := cur.(*cerrors.CompilerError)
		if !ok || doc == nil {
			continue
		}
		path := doc.Source().File
		if path == "" || path == load.Source().File {
			return catalog.SkippedEntity{}, false
		}
		return catalog.SkippedEntity{Path: path, Err: cur}, true
	}
	return catalog.SkippedEntity{}, false
}

// documentFault renders a refusal as the document's own fault, naming no
// document: the caller names it once, as the frame's source.
//
// A schema violation is rendered from the violation itself, without the path it
// was met at, because it is complete on its own — it names the kind, the
// condition, each violated key with its line and column, and the advice — and
// the frames above it only say which step ran the check. Every other refusal is
// rendered as its chain of messages, each frame contributing its own sentence
// rather than its category and path, and a sentence an earlier one already
// carries is not said twice.
//
// A wrapping frame that is not categorised contributes its text only where
// nothing categorised sits beneath it. Its text is the flattened rendering of
// everything beneath it, so above a categorised frame it would carry that
// frame's category and path back into the fault this exists to state without
// them.
func documentFault(err error) string {
	var violation *envelope.Error
	if stderrors.As(err, &violation) && violation != nil {
		unplaced := *violation
		unplaced.Path = ""
		return unplaced.Error()
	}
	var segments []string
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		var txt string
		if ce, ok := cur.(*cerrors.CompilerError); ok {
			txt = ce.Message()
		} else if !categorisedBeneath(cur) {
			txt = cur.Error()
		}
		if txt == "" || saidAlready(segments, txt) {
			continue
		}
		segments = append(segments, txt)
	}
	return strings.Join(segments, ": ")
}

// categorisedBeneath reports whether a categorised frame sits anywhere beneath
// err on its single-cause spine.
func categorisedBeneath(err error) bool {
	for cur := stderrors.Unwrap(err); cur != nil; cur = stderrors.Unwrap(cur) {
		if _, ok := cur.(*cerrors.CompilerError); ok {
			return true
		}
	}
	return false
}

// saidAlready reports whether txt is already part of a segment rendered before
// it, so a frame that only re-states its cause is not said twice.
func saidAlready(segments []string, txt string) bool {
	for _, prior := range segments {
		if strings.Contains(prior, txt) {
			return true
		}
	}
	return false
}

// categoryOf returns the category the refusal was filed under, read off its
// outermost categorised frame, or the empty Category where it carries none.
func categoryOf(err error) cerrors.Category {
	var ce *cerrors.CompilerError
	if stderrors.As(err, &ce) && ce != nil {
		return ce.Category()
	}
	return ""
}

// statedFault is what a RefusedDocument frame wraps: the fault its message
// already states, and the refusal it was stated from.
//
// It reports the refusal to errors.Is and errors.As and hides it from
// errors.Unwrap, and the split is the point. A formatter walks a frame's chain by
// single unwrapping and prints every categorised frame it meets, so a chain it
// could walk would print the refusal's frames beneath the frame that already
// states them. Matching reaches through Is and As, so what a caller matches on —
// the condition the refusal raised, its short code, the violation's own type — is
// exactly what it was before the refusal was stated once.
//
// Its text is the fault, which the frame's message already carries, so a walk
// that does reach it as the tail of the chain has nothing new to print.
type statedFault struct {
	fault   string
	refusal error
}

func (s *statedFault) Error() string { return s.fault }

// Is reports whether the refusal matches target.
func (s *statedFault) Is(target error) bool { return stderrors.Is(s.refusal, target) }

// As finds the first error in the refusal's chain that matches target.
func (s *statedFault) As(target any) bool { return stderrors.As(s.refusal, target) }
