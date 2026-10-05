// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// This file is the one reading of what a refusal LEADS with — one failure, or
// several joined — shared by every surface that reports a refusal finding by
// finding. The command line's diagnostic rendering reads it to decide which
// failures it prints as diagnostics of their own, each under its own head and
// location, and [PreviewResult.RenderRefusal] reads it to decide which findings a
// positioned surface places, each where its own chain points. One reading is what
// keeps a terminal and an editor from disagreeing about how many findings one
// refusal states: two readings would agree on every chain carrying no join and part
// company on the first one that does.

// JoinedErrors returns the errors a frame carries when it carries several at
// once, and reports whether it does.
//
// It is the one place the multi-error shape is recognised. The shape is a
// method returning a slice rather than a single error, and every reader asks for
// it through this function rather than spelling the assertion out again. Two
// spellings of the question agree on every chain carrying no wrapping frame, so a
// reader asking it of the outermost frame alone and a reader asking it of a whole
// chain are indistinguishable until a wrap appears — at which point the narrower
// one hands a wrapped join to a branch that matches a single element and reports
// it as the whole failure. The agreement is what makes a second spelling worth
// removing rather than keeping in step.
func JoinedErrors(err error) ([]error, bool) {
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return nil, false
	}
	return multi.Unwrap(), true
}

// FrameKind says what one frame of an error chain is, in the only terms a walk
// over a refusal cares about: whether the command line's diagnostic rendering
// renders that frame on terms of its own, and if so as which kind of frame.
//
// It exists so that a walk which passes through frames can tell the frames it may
// pass through from the frames it may not. Passing through a frame nothing claims
// loses nothing, because nothing below would have rendered it differently.
// Passing through a frame that is claimed loses exactly what its rendering would
// have said.
type FrameKind int

const (
	// FrameOpaque is a frame nothing claims — a wrap adding context, or any error
	// rendered no differently from the text it already carries.
	FrameOpaque FrameKind = iota
	// FrameMulti is a frame carrying several errors at once.
	FrameMulti
	// FrameEnvelope is a *cerrors.CompilerError.
	FrameEnvelope
	// FrameClaimed is a frame rendered on terms of its own other than an envelope
	// or a join: the skip-context decoration, and each structured domain type the
	// rendering converts into an envelope. A walk looking for something further
	// down must stop at one of these rather than pass through it, because the text
	// it contributes is reachable only through the rendering that claims it.
	FrameClaimed
)

// ClassifyFrame reports which kind the single frame err is, looking at that
// frame alone and never at what it wraps.
//
// 🔴 The claimed set is the command line's detection branches, one for one: the
// decoration and each structured domain type that rendering converts before it
// prints. A branch added there without a kind added here is a branch a walk is
// free to step over, so the two are changed together.
func ClassifyFrame(err error) FrameKind {
	if _, ok := JoinedErrors(err); ok {
		return FrameMulti
	}
	if _, ok := err.(*cerrors.CompilerError); ok {
		return FrameEnvelope
	}
	switch err.(type) {
	case *SkipContextError,
		*resolver.ResolverError,
		*validator.WorkloadValidationError,
		*validator.ValidationError,
		*analyzer.AnalysisError:
		return FrameClaimed
	}
	return FrameOpaque
}

// LeadingFrame walks the single-Unwrap spine rooted at err and returns the first
// frame that is claimed, together with the kind it was classified as. It reports
// FrameOpaque and a nil frame when the spine reaches no such frame at all.
//
// It answers "what does this chain lead with, once the wraps above it are
// discounted": a refusal leading with a join states each joined failure as a
// finding of its own, and every other refusal states one. Stopping at the first
// claimed frame is what keeps the answer honest: a join sitting beneath an
// envelope or beneath a decoration is that frame's detail rather than the failure
// the reader leads with, and the frame claiming it is reported with the join
// beneath it.
//
// It is not errors.As, which searches the whole error tree including the branches
// of a multi-error, and so would report a frame reachable only by descending into
// one of several joined elements — a frame that is one finding's detail rather
// than anything the chain leads with.
func LeadingFrame(err error) (error, FrameKind) {
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		if kind := ClassifyFrame(cur); kind != FrameOpaque {
			return cur, kind
		}
	}
	return nil, FrameOpaque
}

// leadingFailures returns the failures err states as findings of their own, in the
// order it joins them: each element of a join err leads with, recursively — an
// element that itself leads with a join contributes its own elements — and err
// alone otherwise. It never returns an empty slice for a non-nil error.
//
// It is the reading the command line's rendering makes of the same chain when it
// prints a refusal: each failure returned here is one diagnostic there, under a
// head of its own.
func leadingFailures(err error) []error {
	frame, kind := LeadingFrame(err)
	if kind != FrameMulti {
		return []error{err}
	}
	parts, _ := JoinedErrors(frame)
	var out []error
	for _, part := range parts {
		if part == nil {
			continue
		}
		out = append(out, leadingFailures(part)...)
	}
	if len(out) == 0 {
		return []error{err}
	}
	return out
}
