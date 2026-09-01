// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	stderrors "errors"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// GuestFailure is the in-band failure a provider guest reports on a socket
// output whose shape declares the failure channel: the human message, and the
// identifiers of the halting condition that message names.
//
// It exists because the plugin boundary carries values and not Go errors. A
// guest holds a real error chain — a registered condition beneath whatever
// wrapped it — and the socket carries fields, so the guest states what the
// chain says rather than handing the chain over. Every field is optional except
// the message: a chain that names no registered condition carries no
// identifiers, which is a fact about that chain rather than a failure.
//
// The zero value is the success case. A guest that completed sets nothing, and
// the host reads an absent message as success.
type GuestFailure struct {
	// Message is the failure text, flattened from the whole error chain.
	Message string
	// Code is the descriptive, machine-matchable identity of the condition the
	// failure names, or empty where the chain names none.
	Code string
	// ShortCode is the compact identifier of the same condition — the letter E
	// and four digits — or empty where the chain names none.
	ShortCode string
}

// FailureOf derives the in-band failure a guest reports for err, and is the one
// home for that derivation across every guest export that has a failure channel
// to report on.
//
// It is one function rather than a convention because the alternative was
// measured: each guest flattened its own chain, so the identity a registered
// condition carries was discarded at every export and only the sentence's text
// crossed. A second copy of this derivation is a second answer to "what does
// this guest report", and the two agree until one of them is edited.
//
// A nil error yields the zero value, so a caller on its success path needs no
// branch of its own.
func FailureOf(err error) GuestFailure {
	if err == nil {
		return GuestFailure{}
	}
	f := GuestFailure{Message: failureMessage(err)}
	if code, ok := cerrors.CodeOf(err); ok {
		f.Code = code
	}
	if short, ok := cerrors.ShortCodeOf(err); ok {
		f.ShortCode = short
	}
	return f
}

// RenderNodeFailure is the render_node output a guest returns when it could not
// complete: the in-band failure derived from err and nothing else.
//
// A failing render emits no files, and the four constructors here exist so no
// guest spells that envelope for itself. Each guest owns its own exports, so the
// alternative is one copy of this shape per guest per export — and a copy that
// forgot a field would drop the identity for that one export while every other
// export carried it, which is the shape a green proof does not show.
func RenderNodeFailure(err error) RenderNodeOutput {
	f := FailureOf(err)
	return RenderNodeOutput{Error: f.Message, ErrorCode: f.Code, ErrorShortCode: f.ShortCode}
}

// ValidateNodeFailure is the validate_node_input output a guest returns when it
// could not complete the structural check at all, as distinct from completing it
// and reporting findings as diagnostics.
func ValidateNodeFailure(err error) ValidateNodeOutput {
	f := FailureOf(err)
	return ValidateNodeOutput{Error: f.Message, ErrorCode: f.Code, ErrorShortCode: f.ShortCode}
}

// AssembleBoundaryFailure is the assemble_boundary output a guest returns when it
// could not complete: the in-band failure derived from err and no assembled files.
func AssembleBoundaryFailure(err error) AssembleBoundaryOutput {
	f := FailureOf(err)
	return AssembleBoundaryOutput{Error: f.Message, ErrorCode: f.Code, ErrorShortCode: f.ShortCode}
}

// NameBoundFailure is the name_bound output a guest returns when the question
// itself could not be answered, as distinct from answering it with a refusing
// verdict.
func NameBoundFailure(err error) NameBoundOutput {
	f := FailureOf(err)
	return NameBoundOutput{Error: f.Message, ErrorCode: f.Code, ErrorShortCode: f.ShortCode}
}

// LinkPublishFailure is the link_publish output a guest returns when it could not
// publish the link: the in-band failure derived from err, no handle and no wiring.
func LinkPublishFailure(err error) LinkPublishOutput {
	f := FailureOf(err)
	return LinkPublishOutput{Error: f.Message, ErrorCode: f.Code, ErrorShortCode: f.ShortCode}
}

// LinkResolveFailure is the link_resolve output a guest returns when it could not
// resolve the link: the in-band failure derived from err and no wiring.
func LinkResolveFailure(err error) LinkResolveOutput {
	f := FailureOf(err)
	return LinkResolveOutput{Error: f.Message, ErrorCode: f.Code, ErrorShortCode: f.ShortCode}
}

// failureMessage flattens err's chain into the one message that crosses the
// socket: each frame's own text in order, joined by ": ", skipping a frame whose
// text the accumulation already carries.
//
// Flattening at all is forced by the boundary: the rich multi-line rendering
// belongs to the terminal, and some wrapper types keep their cause out of
// Error() and surface it only through Unwrap, so a caller taking err.Error()
// alone reports the outermost description and drops the diagnostic underneath —
// the frame naming the document actually at fault.
//
// What each frame contributes is decided by frameText, and that decision is the
// half a plain flatten gets wrong.
//
// A source pointer already stated is not stated again, which is the same
// one-home rule frameText applies to the category, one level down: two frames of
// one failure routinely point at the same node, and repeating the pointer between
// them says nothing the first statement did not. The containment skip above
// cannot see it, because the two frames' texts differ in their messages even
// where their pointers agree.
func failureMessage(err error) string {
	var segments []string
	statedSource := ""
	for cur := err; cur != nil; cur = stderrors.Unwrap(cur) {
		txt, source := frameText(cur)
		if source != "" && source == statedSource {
			txt = strings.TrimPrefix(txt, source+": ")
		}
		if source != "" {
			statedSource = source
		}
		if txt == "" {
			continue
		}
		redundant := false
		for _, prior := range segments {
			if strings.Contains(prior, txt) {
				redundant = true
				break
			}
		}
		if !redundant {
			segments = append(segments, txt)
		}
	}
	return strings.Join(segments, ": ")
}

// frameText renders one frame's contribution to the crossing message, and
// returns the source pointer it led with so the caller can tell a repeated
// pointer from a new one. A frame that carries no pointer returns an empty
// second value, which leaves whatever pointer was last stated standing.
//
// For a categorised frame the contribution is its source pointer and message
// WITHOUT the category; for anything else it is the frame's own text.
//
// Dropping the category and keeping the pointer is not a formatting preference,
// and the two halves have different reasons. The category is a value the HOST
// composes into the head of the diagnostic it prints for this failure, so a
// frame that spells it into the crossing text gives one value two homes and the
// word arrives twice on one line. The source pointer is occurrence data the host
// may not hold at all — a document inside the packed source, a key within it —
// so dropping it would delete the only locator the reader gets.
//
// The frame is matched by type assertion rather than by errors.As, because the
// question is what THIS frame says and not what its chain contains: errors.As
// would answer for a frame further down and every ordinary wrap would then
// contribute a categorised frame's text.
func frameText(frame error) (text, source string) {
	ce, ok := frame.(*cerrors.CompilerError)
	if !ok {
		return frame.Error(), ""
	}
	if ce == nil {
		return "", ""
	}
	src := ce.Source.String()
	switch {
	case src == "":
		return ce.Message, ""
	case ce.Message == "":
		return src, src
	default:
		return src + ": " + ce.Message, src
	}
}
