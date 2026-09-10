// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/explainops"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// docEntityDir is the directory holding the per-kind language-reference pages.
// A kind's page is named for the kind's own path segment, so the pointer is
// derived rather than restated per kind.
const docEntityDir = "aggrega-docs/language-reference/entities/"

// docIndex is the fallback pointer when no kind-specific page applies.
const docIndex = "aggrega-docs/language-reference/"

// docPointerFor returns the language-reference page documenting an entity kind, or
// the index when the kind is empty, unrecognized, or has no page of its own.
//
// A diagnostic naming a kind is expanded with this pointer so the reader can look
// up the authoring contract. Falling back to the index for a kind with no page is
// deliberate: naming a page that does not ship would read as a broken reference to
// whoever followed it, which is worse than being sent to the index.
func docPointerFor(kind string) string {
	segment := strings.ToLower(strings.TrimSpace(kind))
	k, ok := entitykind.BySegment(segment)
	if !ok {
		return docIndex
	}
	if e, _ := entitykind.Lookup(k); !e.ReferencePage {
		return docIndex
	}
	return docEntityDir + segment + ".md"
}

// explainDiagnostic expands a diagnostic message into actionable guidance: the
// diagnostic's own text, what the compiler declares about the condition the
// message names where it names one, and a documentation pointer for the surface
// it concerns. kind selects the pointer; an empty kind uses the
// language-reference index.
//
// It invents no error corpus. The message the compiler already produced carries
// the remediation, the pointer only names where to read the authoring contract,
// and the condition note is read from the declarations the compiler itself
// registers rather than composed here — so nothing in the guidance is text this
// package made up about a failure.
func explainDiagnostic(message, kind string) string {
	message = strings.TrimSpace(message)
	var b strings.Builder
	b.WriteString(message)
	b.WriteString(conditionNote(message))
	b.WriteString("\n\nSee: ")
	b.WriteString(docPointerFor(kind))
	return b.String()
}

// conditionNote returns what the compiler declares about the condition a
// diagnostic names, or the empty string when the text carries no code that
// resolves to one.
//
// What it adds is the identifier a caller can BRANCH on. A printed diagnostic
// carries the short code, which is compact enough to sit at the head of a line
// and says nothing on its own; the descriptive code says what the condition is
// and is what a consumer outside Go matches against. A caller handed only the
// printed form would otherwise have to match on the wording of a message, which
// is the one part of a diagnostic that is free to be reworded.
//
// A text carrying no code, or one no condition is registered under, adds nothing
// rather than reporting anything: the guidance is about the diagnostic, and a
// code this does not recognise is not a fact about the diagnostic worth
// asserting.
func conditionNote(message string) string {
	code, ok := haltref.ShortCodeIn(message)
	if !ok {
		return ""
	}
	condition, err := haltref.Resolve(code)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("\n\nThe short code %s names the condition %s. Match on that descriptive code "+
		"rather than on the wording of the message, which is free to be reworded.",
		condition.ShortCode, condition.Code)
}

// explainToolError renders a tool-level failure into the guidance an assistant
// reads to correct the call: a leading action line, the underlying error, and a
// documentation pointer. It is the single route every closure-driven tool
// sends a failure through, so a build error, a validation-load failure, and a
// missing search plugin all reach the caller as guidance rather than an opaque
// code.
func explainToolError(action string, err error, kind string) string {
	var b strings.Builder
	b.WriteString(action)
	b.WriteString("\n\n")
	b.WriteString(explainDiagnostic(err.Error(), kind))
	return b.String()
}

// explainErrorArgs is the explain-error tool's argument shape.
type explainErrorArgs struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

// explainErrorCondition is what the compiler declares about the condition a
// diagnostic named: its two identifiers and the sentence every diagnostic naming
// it opens with.
//
// It is nested rather than flattened onto the result because the result already
// carries a message — the diagnostic the caller sent — and a condition's own
// sentence is a different string. Two keys called the same thing on one object
// would leave a caller guessing which it was reading.
type explainErrorCondition struct {
	ShortCode string `json:"short_code"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

// explainErrorEntry is one published entry documenting the condition: the heading
// that opens it and the prose beneath.
//
// The heading travels beside the body rather than being left for the caller to
// parse back out of it, so a caller rendering the entries as a list has their
// titles without reading markdown.
type explainErrorEntry struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
}

// explainErrorResult is the explain-error tool's structured payload.
//
// Message is the diagnostic as it was sent, Guidance the whole expansion as
// prose, and DocPointer the language-reference page for the kind the diagnostic
// concerns — where to read the authoring contract for the thing that failed.
//
// The rest is present only when the diagnostic named a condition something
// resolves. Condition is what the compiler declares about it; Entries are the
// published entries that teach the authoring fix, in the order the documentation
// itself pairs them with the condition; Reference is where the condition is
// published, for a caller that would rather send its reader to the documentation
// than render prose. Note is the reason the entries are absent and is an ordinary
// part of an answer rather than a failure — the condition resolved, and what is
// missing is the walk-through. Entries and Note are never both present.
type explainErrorResult struct {
	Message    string                 `json:"message"`
	Guidance   string                 `json:"guidance"`
	DocPointer string                 `json:"doc_pointer"`
	Condition  *explainErrorCondition `json:"condition,omitempty"`
	Entries    []explainErrorEntry    `json:"entries,omitempty"`
	Reference  string                 `json:"reference,omitempty"`
	Note       string                 `json:"note,omitempty"`
}

// conditionExpansion is the shared explanation of one condition in the two forms
// this surface publishes it: the prose a reader sees and the fields a caller
// branches on.
//
// Rendered comes from the shared layer rather than from here, which is the whole
// point of the type: the block a caller reads is the same bytes a terminal prints
// for the same code, so the two cannot drift into two explanations of one
// condition.
type conditionExpansion struct {
	Rendered  string
	Note      string
	Condition explainErrorCondition
	Entries   []explainErrorEntry
}

// explainCondition returns the shared explanation of the condition the given
// diagnostic names, or nil when the text names none that anything resolves.
//
// The code is read out of the message because that is what this surface is handed:
// a caller here holds the diagnostic it got back from a build, not a bare code, so
// resolving one from the other is part of answering rather than something to ask
// the caller for.
//
// Composition is the shared layer's, so which condition a code names, which
// entries document it and how a missing walk-through is worded are decided in one
// place for every surface that answers this question. Nothing about the answer is
// decided here — this supplies the workspace and renders what comes back.
//
// A nil result is the case this surface answers as it always did: the diagnostic
// and the pointer, with nothing asserted about a run of characters no condition
// is registered under. A code that resolves against the published reference alone
// — which is every condition a provider declares for itself, since those are
// registered inside the guest that raises them — is not that case and is answered
// in full.
func (s *server) explainCondition(message string) (*conditionExpansion, *rpcError) {
	code, ok := haltref.ShortCodeIn(message)
	if !ok {
		return nil, nil
	}

	explanation, cerr := explainops.Compose(code, s.explainPages)
	if explanation.Condition.ShortCode == "" && explanation.Condition.Code == "" {
		return nil, nil
	}

	// Rendered through the shared writer into a buffer rather than composed here,
	// so the block is byte-identical to what the same explanation renders as
	// elsewhere. The two channels are separated the way that writer separates
	// them: the answer on one, the reason anything is missing from it on the other.
	var rendered, notes strings.Builder
	if werr := explainops.Write(&rendered, &notes, explanation); werr != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "render the explanation: " + werr.Error()}
	}

	// A composition that resolved the condition and then failed reports both: the
	// condition it did resolve, and why the rest is missing. That is the same
	// pairing a terminal shows, and the reason travels as a note because the
	// condition above it stands.
	note := strings.TrimSpace(notes.String())
	if note == "" && cerr != nil {
		note = cerr.Error()
	}

	exp := &conditionExpansion{
		Rendered: strings.TrimSpace(rendered.String()),
		Note:     note,
		Condition: explainErrorCondition{
			ShortCode: explanation.Condition.ShortCode,
			Code:      explanation.Condition.Code,
			Message:   explanation.Condition.Message,
		},
	}
	for _, entry := range explanation.Entries {
		exp.Entries = append(exp.Entries, explainErrorEntry{Heading: entry.Heading, Body: entry.Body})
	}
	return exp, nil
}

// explainPages reads the published pages the session's workspace has cached.
//
// It reaches for nothing. The bundle is acquired once per session at the
// handshake, ahead of anything that reads it, so an expansion here reads a cache
// somebody else filled rather than putting a registry round-trip behind a
// question — and a workspace that never obtained the bundle is answered with the
// condition and the reason the walk-through is absent, which is an answer rather
// than a failure.
//
// The coordinates are this session's own, not derived a second time: a session
// pinned to a particular bundle explains a code out of that bundle and out of the
// same one its documentation resources serve. Resolving the workspace is this
// surface's act for the reason every documentation read here resolves it — the
// documentation a session serves is a property of the session, not of one call.
func (s *server) explainPages() (explainops.Pages, error) {
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		return explainops.Pages{}, fmt.Errorf("the published documentation is not available: %w", err)
	}
	ref, version := s.docsBundleCoords(tw)
	return explainops.ReadPages(tw.root, tw.cacheDir, ref, version)
}

var explainErrorToolSpec = toolSpec{
	// The entity kinds are NOT listed here. The argument schema below carries them
	// as an enum, which is the authority a client composes against, and a second
	// copy in prose is one that drifts the day a kind is added. The definition
	// bytes it frees are what pays for the sentence about the condition, which a
	// caller cannot get from a schema.
	description: "Expand a compiler diagnostic or error message into actionable guidance, writing nothing. " +
		"Pass the exact diagnostic text a validate or build call returned as `message`, and optionally the " +
		"entity kind it concerns as `kind`. The answer carries the diagnostic verbatim and a pointer to the " +
		"language-reference page for that kind; where the diagnostic opens with a halting condition's short " +
		"code, it also carries that condition and the published walk-through of the authoring fix. It does " +
		"not guess at causes beyond what the diagnostic already states; the compiler's own message carries " +
		"the remediation.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "message": {
      "type": "string",
      "minLength": 1,
      "description": "The diagnostic or error text to expand into guidance. Typically copied verbatim from a validate or build result."
    },
    "kind": {
      "type": "string",
      "description": "Optional entity kind the diagnostic concerns (release, aggregate, component, feature, recipe, featurerecipe, manifest, plugin). Selects the language-reference page the guidance points at.",
      "enum": ["aggregate", "component", "feature", "featurerecipe", "manifest", "mappinglibrary", "plugin", "recipe", "release", "schemalibrary", "workload"]
    }
  },
  "required": ["message"]
}`,
	invoke: func(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
		var a explainErrorArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return invalidToolArguments("explain-error", err)
			}
		}
		message := strings.TrimSpace(a.Message)
		if message == "" {
			return errorResult("explain-error requires a non-empty 'message' argument")
		}

		expansion, rerr := s.explainCondition(message)
		if rerr != nil {
			return nil, rerr
		}
		if expansion == nil {
			// The diagnostic names no condition anything resolves, so the answer is
			// the diagnostic and where to read the authoring contract — the form
			// every expansion takes before a code adds anything to it.
			guidance := explainDiagnostic(message, a.Kind)
			return textResult(guidance, explainErrorResult{
				Message:    message,
				Guidance:   guidance,
				DocPointer: docPointerFor(a.Kind),
			})
		}

		result := explainErrorResult{
			Message:    message,
			DocPointer: docPointerFor(a.Kind),
			Condition:  &expansion.Condition,
			Entries:    expansion.Entries,
			Reference:  haltref.ExplanationURL(),
			Note:       expansion.Note,
		}
		result.Guidance = explainGuidance(message, expansion, result.DocPointer)
		return textResult(result.Guidance, result)
	},
}

// explainGuidance composes the prose an assistant reads: the diagnostic it sent,
// the shared explanation of the condition that diagnostic named, whatever could
// not be added to that explanation, and where to read the authoring contract for
// the kind it concerns.
//
// The blocks are joined rather than formatted together, and each is trimmed
// first, so the shared explanation appears in the answer as the bytes the shared
// layer produced. Reformatting it here — rewrapping it, indenting it, folding a
// heading into a sentence — would make this surface's copy a rendering of its own,
// which is the divergence routing through one composition exists to remove.
//
// A missing walk-through is stated rather than left out. Omitting the reason would
// leave a reader with a condition and no explanation and nothing saying an
// explanation exists elsewhere, which reads as though none had ever been written.
func explainGuidance(message string, expansion *conditionExpansion, docPointer string) string {
	blocks := []string{message, expansion.Rendered}
	if expansion.Note != "" {
		blocks = append(blocks, expansion.Note)
	}
	return strings.Join(append(blocks, "See: "+docPointer), "\n\n")
}
