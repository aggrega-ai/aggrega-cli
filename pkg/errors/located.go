// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package errors

import stderrors "errors"

// located is an error annotated with the authored position its failure is about.
// It is reached only through [Locate] and read only through [LocatedAt].
type located struct {
	err error
	at  SourceLocation
}

// Error returns the annotated error's own text, so the annotation adds nothing to
// any rendering of the chain.
func (l *located) Error() string { return l.err.Error() }

// Unwrap returns the annotated error, so errors.Is and errors.As reach through the
// annotation unchanged.
func (l *located) Unwrap() error { return l.err }

// Locate annotates err with the authored position its failure is about — the
// document an author edits and, where the raising site has it, the key within
// that document — for a surface that places a finding where the author can act on
// it, such as an editor's problem list.
//
// # It renders nothing, and that is why it exists beside a Locator
//
// The annotation's text is exactly the text of the error it annotates. A walk that
// renders a chain — the terminal's multi-line rendering, or [CauseChain]'s single
// line — meets a link whose text the link beneath it already states, and says it
// once. So annotating a failure moves no byte of what any surface prints.
//
// That is the difference from the Locator a [SourceLocation] carries on an
// envelope, and the reason this is not that field. A Locator on an envelope is part
// of the pointer the head of a diagnostic prints, so setting one changes the text
// every terminal and every transcript shows. This carries the same two halves to
// the surfaces that position a finding, and leaves the text alone.
//
// # What it states
//
// at names a document by File — a filesystem path, absolute or relative to the
// workspace — and, where the raising site has it, the dotted key within that
// document by Locator. Unlike an envelope's pointer, File here is never a logical
// identifier: a site holding no document has nothing to state and does not call
// this. Line and Column are not read; a surface holding the parsed document
// resolves the key to a position itself, because only it knows the encoding its
// reader counts columns in.
//
// It returns err unchanged when err is nil or at names no document, so a site that
// may or may not know its document passes what it has without a guard of its own.
func Locate(err error, at SourceLocation) error {
	if err == nil || at.File == "" {
		return err
	}
	return &located{err: err, at: at}
}

// LocatedAt returns the authored position the innermost [Locate] annotation in
// err's chain states, and whether the chain carries one.
//
// The innermost annotation wins because it was made by the site closest to the
// failure, which is the site that knew the most about where it lies: an annotation
// added further out, by a frame that only re-states its cause, can name a document
// but not a finer key than the one beneath it.
//
// It walks the chain the way [CauseChain] does, one cause at a time, and stops at a
// frame carrying several errors at once. A position found down one branch of such
// a frame is a position for that branch's failure, never for the whole, so it is
// not reported as one.
func LocatedAt(err error) (SourceLocation, bool) {
	var found SourceLocation
	ok := false
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		if l, isLocated := cur.(*located); isLocated {
			found, ok = l.at, true
		}
	}
	return found, ok
}
