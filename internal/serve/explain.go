// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/explainops"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
)

// explainErrorParams is the request shape for `aggrega/explainError`.
//
// Code is either of the identifiers a condition carries: the short code a
// diagnostic's bracketed head prints, or the descriptive code a structured
// diagnostic carries as a key of its own. One parameter takes both because a
// caller holding one has no way to obtain the other without the answer this
// request gives, and a surface that took only one would be silent for whoever
// was handed the other.
type explainErrorParams struct {
	Code string `json:"code"`
}

// explainErrorEntry is one published entry documenting the condition: the heading
// that opens it and the prose beneath.
//
// The heading travels beside the body rather than being left for the caller to
// parse back out of it, because a client rendering a list of entries wants their
// titles without reading markdown, and a client that re-derived them would be
// deciding for itself what opens an entry.
type explainErrorEntry struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// explainErrorResult is the `aggrega/explainError` response.
//
// ShortCode, Code and Message are what the compiler declares about the condition:
// its two identifiers and the sentence every diagnostic naming it opens with. They
// are always present on an answer, because they come from a declaration rather
// than from a read — which is what lets this answer at all in a workspace with no
// cached documentation.
//
// Entries are the published entries that teach the authoring fix, in the order the
// page's own correspondence names them. Reference is where the condition is
// published, so a client that would rather send its reader to the documentation
// than render the prose has the address without composing one.
//
// Note is the reason the entries are absent, and it is an ordinary part of an
// answer rather than a failure: the condition resolved, and what is missing is the
// walk-through. A workspace with no cached documentation and a condition the page
// does not document yet are the two cases, and each says which it is. Entries and
// Note are never both present.
type explainErrorResult struct {
	ShortCode string              `json:"shortCode"`
	Code      string              `json:"code"`
	Message   string              `json:"message"`
	Entries   []explainErrorEntry `json:"entries,omitempty"`
	Reference string              `json:"reference"`
	Note      string              `json:"note,omitempty"`
}

// dispatchExplainError answers with what the compiler declares about the
// condition a code names, and the published entries that teach the authoring fix.
//
// It is the expansion of a code the diagnostics on this wire carry, and it is a
// request of its own rather than something riding on each diagnostic. A
// diagnostic is emitted on every edit, so an expansion travelling on each one
// would republish the same paragraphs on every keystroke to a reader who has
// usually already read them; a client that wants the prose asks once. The
// diagnostic carries the quotable code and the protocol's own pointer, and this
// is how a client that cannot follow that pointer itself obtains the text behind
// it.
//
// The answer is composed by the shared layer every surface answering this
// question composes through, so a code explained here and the same code explained
// at a terminal are one answer in two renderings rather than two answers that
// agree today. Nothing about which condition a code names, which entries document
// it, or how a refusal is worded is decided in this file.
//
// It reaches the documentation bundle, acquiring it when the workspace has no
// copy, and it is admitted on the rule every reaching method here runs under: a
// caller that sent it has asked for the expansion. Nothing is written to the
// workspace, and nothing is prompted for — a bundle that cannot be acquired
// leaves the condition answered and states why the prose is missing.
//
// A code nothing resolves is refused as an invalid-params envelope, because what
// needs correcting is the request rather than the workspace: the code a caller
// sent names no condition, or names a number that is spent, and both are answered
// by sending a different code.
func (s *Server) dispatchExplainError(ctx context.Context, req *jsonRPCRequest) error {
	var params explainErrorParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	code := strings.TrimSpace(params.Code)
	if code == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"explainError requires a `code`: the short code a diagnostic's head prints, or the "+
				"descriptive code it carries as a key of its own")
	}

	// Acquisition warnings go to the daemon's own log stream rather than onto the
	// answer. They report on how the documentation was obtained, which is not what
	// the caller asked about, and a client rendering an explanation has nowhere to
	// put them; the reason the prose is MISSING is a different thing and travels on
	// the answer as a note.
	warn := func(notice string) { fmt.Fprintf(s.stderr, "  explainError: %s\n", notice) }
	explanation, err := explainops.Compose(code, func() (explainops.Pages, error) {
		return explainops.LoadPages(ctx, s.wsConfig, s.workspaceRoot, s.compilerVersion, warn)
	})
	if err != nil {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, err.Error())
	}

	result := explainErrorResult{
		ShortCode: explanation.Condition.ShortCode,
		Code:      explanation.Condition.Code,
		Message:   explanation.Condition.Message,
		Reference: haltref.ExplanationURL(),
	}
	for _, entry := range explanation.Entries {
		result.Entries = append(result.Entries, explainErrorEntry{Heading: entry.Heading, Body: entry.Body})
	}
	if explanation.Note != nil {
		result.Note = explanation.Note.Error()
	}
	return s.writeResult(ctx, req.ID, result)
}
