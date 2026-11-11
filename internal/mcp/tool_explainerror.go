// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/explainops"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The pages a pointer names are written as paths inside the documentation tree,
// the form the documentation bundle carries them in, and a pointer names each by
// the address the published documentation serves it at (docAddress) — so a
// reader with no checkout and no cached bundle can open it.

// docEntityDir is the directory holding the per-kind language-reference pages.
// A kind's page is named for the kind's own path segment, so the pointer is
// derived rather than restated per kind.
const docEntityDir = "language-reference/entities/"

// docIndex is the fallback pointer when no kind-specific page applies: the
// language reference itself, named as its section rather than by the file that
// opens it, because the section's own address is where that page is published.
const docIndex = "language-reference"

// docServerReference is the page teaching this server itself: which workspace a
// call is answered in, and the credentials a call spends. A refusal about either
// points here, because no entity kind's page is about them — a refusal of the
// workspace a call resolves included, whichever kind the call concerns
// (explainToolError).
const docServerReference = docIndex + "/cli/aggrega-mcp.md"

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
	return explainDiagnosticAt(message, docPointerFor(kind))
}

// explainDiagnosticAt is explainDiagnostic for a failure whose fix is taught on a
// page no entity kind selects, named outright. The expansion is the same one, so
// a refusal pointing at a page and one pointing at a kind differ in the page they
// name and in nothing else.
func explainDiagnosticAt(message, page string) string {
	message = strings.TrimSpace(message)
	var b strings.Builder
	b.WriteString(message)
	b.WriteString(conditionNote(message))
	b.WriteString("\n\n")
	b.WriteString(docPointerLine(page))
	return b.String()
}

// docPointerLine is the line that closes a piece of guidance by naming the page
// to read, by its published address. It is the one place that line is written,
// so every refusal and every expansion here spells its pointer the same way
// whichever page it names.
func docPointerLine(page string) string {
	return "See: " + docAddress(page)
}

// docAddress returns the address a pointer names a page by: where the published
// documentation serves it, which a reader can open without a checkout or a
// cached bundle. The line closing a piece of guidance and the pointer
// explain-error carries as a field both name a page through it, so the two
// cannot come to name one page in two forms.
func docAddress(page string) string {
	return haltref.PublishedURL(page)
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
	return conditionNoteFor(code)
}

// conditionNoteFor is conditionNote for a short code already in hand, read off a
// failure's chain rather than out of its printed text. A code no condition
// resolves adds nothing, for the reason conditionNote gives.
func conditionNoteFor(shortCode string) string {
	condition, err := haltref.Resolve(shortCode)
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
//
// kind selects the pointer, except for a refusal of the workspace the call
// resolves (toolWorkspaceRefusal), which points at the page teaching which
// workspace a call is answered in whatever kind the call concerns: the fix is to
// the workspace the call named or the server was started over, and no entity
// kind's page teaches that.
func explainToolError(action string, err error, kind string) string {
	if refusesWorkspace(err) {
		return explainToolErrorAt(action, err, docServerReference)
	}
	return explainToolErrorAt(action, err, docPointerFor(kind))
}

// explainToolRefusal is explainToolError for a refusal raised while rendering,
// whose text is the refusal with every cause beneath it rather than its outer
// frame alone, which names only the node being rendered.
//
// That text carries no short code — the code prints at the head of a terminal's
// rendering, not inside any one frame — so the note naming the condition is read
// off the refusal's own chain instead of out of its words. A caller here is
// therefore told the condition's descriptive code exactly as it is for a
// diagnostic whose printed text carries the short code.
func explainToolRefusal(action, message string, refusal error, kind string) string {
	var b strings.Builder
	b.WriteString(action)
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(message))
	if shortCode, ok := cerrors.ShortCodeOf(refusal); ok {
		b.WriteString(conditionNoteFor(shortCode))
	}
	b.WriteString("\n\n")
	b.WriteString(docPointerLine(docPointerFor(kind)))
	return b.String()
}

// explainToolErrorAt is explainToolError for a failure whose fix is taught on a
// page no entity kind selects, named outright.
//
// The note naming the condition is read off the failure's own chain where the
// chain carries one, as the render-refusal path reads it (explainToolRefusal),
// and out of its text otherwise. A failure's text carries no short code where
// the code prints only at the head of a terminal's rendering — a dependency
// resolution that found no entity for a reference is one — and reading the text
// alone would answer such a failure with neither the condition's short code nor
// its descriptive one, which is what a caller branches on.
func explainToolErrorAt(action string, err error, page string) string {
	message := strings.TrimSpace(err.Error())
	note := conditionNote(message)
	if shortCode, ok := cerrors.ShortCodeOf(err); ok {
		note = conditionNoteFor(shortCode)
	}
	var b strings.Builder
	b.WriteString(action)
	b.WriteString("\n\n")
	b.WriteString(message)
	b.WriteString(note)
	b.WriteString("\n\n")
	b.WriteString(docPointerLine(page))
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
// prose, and DocPointer the published address of the language-reference page
// for the kind the diagnostic concerns — where to read the authoring contract
// for the thing that failed, the same address the prose closes with.
//
// Condition, Entries and Reference are present only when the diagnostic named a
// condition something resolves. Condition is what the compiler declares about it;
// Entries are the published entries that teach the authoring fix, in the order the
// documentation itself pairs them with the condition; Reference is where the
// condition is published, for a caller that would rather send its reader to the
// documentation than render prose.
//
// Note is the reason something is absent, and it is an ordinary part of an answer
// rather than a failure. Usually the condition resolved and what is missing is the
// walk-through. It also carries the one refusal that is a fact about the code
// itself — a short code that named a condition once and never names another —
// which arrives with no condition beside it, because there is none left to report.
// Entries and Note are never both present.
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
//
// Condition is nil where nothing resolved, which is an expansion carrying a Note
// and nothing else. The pointer is what carries that distinction, so a reader of
// this type has one thing to test rather than an empty field to interpret — and
// the answer it assembles cannot present a condition whose every value is blank.
type conditionExpansion struct {
	Rendered  string
	Note      string
	Condition *explainErrorCondition
	Entries   []explainErrorEntry
}

// explainCondition returns the shared explanation of the condition the given
// diagnostic names, or nil when the text names none and there is nothing to
// report about the code it does carry.
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
// A composition that resolved nothing still answers where its refusal is a fact
// about the number rather than the absence of one. A short code that named a
// condition once and never names another is exactly that, and it is what a reader
// quoting an old diagnostic needs to be told — so the refusal travels as the
// expansion's note, with no condition beside it. The refusal is recognised by its
// type, never by its wording, which is free to be reworded.
//
// A nil result is the case this surface answers as it always did: the diagnostic
// and the pointer, with nothing asserted about a run of characters no condition is
// registered under, or about a lookup left undetermined. Neither is a fact about
// the diagnostic worth putting in front of a reader. A code that resolves against
// the published reference alone — which is every condition a provider declares for
// itself, since those are registered inside the guest that raises them — is not
// that case and is answered in full.
//
// ctx is the tool call's own; the published pages are read under it.
func (s *server) explainCondition(ctx context.Context, message string) (*conditionExpansion, *rpcError) {
	code, ok := haltref.ShortCodeIn(message)
	if !ok {
		return nil, nil
	}

	explanation, cerr := explainops.Compose(code, func() (explainops.Pages, error) {
		return s.explainPages(ctx)
	})

	// The same test the shared writer applies for "renders nothing", evaluated once
	// and read from here on, so this surface and that one cannot come to disagree
	// about whether there is a condition to report.
	resolved := explanation.Condition.ShortCode != "" || explanation.Condition.Code != ""

	var retired *haltref.RetiredCodeError
	if !resolved && !errors.As(cerr, &retired) {
		return nil, nil
	}

	// Rendered through the shared writer into a buffer rather than composed here,
	// so the block is byte-identical to what the same explanation renders as
	// elsewhere. The two channels are separated the way that writer separates
	// them: the answer on one, the reason anything is missing from it on the other.
	// An explanation that resolved nothing renders as nothing, which is what leaves
	// the note as the whole of that answer.
	var rendered, notes strings.Builder
	if werr := explainops.Write(&rendered, &notes, explanation); werr != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "render the explanation: " + werr.Error()}
	}

	// A composition that resolved the condition and then failed reports both: the
	// condition it did resolve, and why the rest is missing. That is the same
	// pairing a terminal shows, and the reason travels as a note because the
	// condition above it stands — or, where none resolved, because the reason is
	// the answer.
	note := strings.TrimSpace(notes.String())
	if note == "" && cerr != nil {
		note = cerr.Error()
	}

	exp := &conditionExpansion{
		Rendered: strings.TrimSpace(rendered.String()),
		Note:     note,
	}
	if resolved {
		exp.Condition = &explainErrorCondition{
			ShortCode: explanation.Condition.ShortCode,
			Code:      explanation.Condition.Code,
			Message:   explanation.Condition.Message,
		}
	}
	for _, entry := range explanation.Entries {
		exp.Entries = append(exp.Entries, explainErrorEntry{Heading: entry.Heading, Body: entry.Body})
	}
	return exp, nil
}

// explainPages reads the published pages from the copy of the bundle this
// session reads, held to its workspace's own signature check.
//
// It reaches for no registry. The bundle is acquired once at the start of the
// run, ahead of anything that reads it, so an expansion here reads a cache
// somebody else filled rather than putting a registry round-trip behind a
// question — and a session that never obtained the bundle is answered with the
// condition and the reason the walk-through is absent, which is an answer rather
// than a failure.
//
// The coordinates and the cache root are this session's own, not derived a
// second time: a session pinned to a particular bundle explains a code out of
// that bundle and out of the same copy its documentation resources serve.
// Resolving the workspace is this surface's act for the reason every
// documentation read here resolves it — the documentation a session serves is a
// property of the session, not of one call. The signature check runs under ctx,
// the call's own, with what the run stated about the bundle stated on it again.
func (s *server) explainPages(ctx context.Context) (explainops.Pages, error) {
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		return explainops.Pages{}, fmt.Errorf("the published documentation is not available: %w", err)
	}
	ref, version := s.docsBundleCoords(tw)
	var none *docsbundle.NoBundleForVersionError
	if errors.As(s.docsAcquireErr, &none) {
		// The session's acquisition already measured why there is no bundle to
		// read, and that answer names both versions; a read of the cache could
		// only report the version asked for as absent.
		return explainops.Pages{}, explainops.Unavailable(ref, version, s.docsAcquireErr)
	}
	cacheRoot, err := s.docsCacheRoot()
	if err != nil {
		return explainops.Pages{}, explainops.Unavailable(ref, version, err)
	}
	pages, err := explainops.ReadPages(cacheRoot, ref, version, docsbundle.Admission(s.docsDecl.Onto(ctx), tw.cfg, ref, nil))
	if err != nil {
		return explainops.Pages{}, err
	}
	pages.Requested = s.docsRequestedVersion()
	return pages, nil
}

var explainErrorToolSpec = toolSpec{
	// The entity kinds are NOT listed here. The argument schema below carries them
	// as an enum, which is the authority a client composes against, and a second
	// copy in prose is one that drifts the day a kind is added. The definition
	// bytes it frees are what pays for the sentence about the condition, which a
	// caller cannot get from a schema.
	summary: "Expands a diagnostic into the authoring fix it calls for.",
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
	invoke: func(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
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

		expansion, rerr := s.explainCondition(ctx, message)
		if rerr != nil {
			return nil, rerr
		}
		page := docPointerFor(a.Kind)
		if expansion == nil {
			// The diagnostic names no condition anything resolves, so the answer is
			// the diagnostic and where to read the authoring contract — the form
			// every expansion takes before a code adds anything to it.
			guidance := explainDiagnosticAt(message, page)
			return textResult(guidance, explainErrorResult{
				Message:    message,
				Guidance:   guidance,
				DocPointer: docAddress(page),
			})
		}

		result := explainErrorResult{
			Message:    message,
			DocPointer: docAddress(page),
			Condition:  expansion.Condition,
			Entries:    expansion.Entries,
			Note:       expansion.Note,
		}
		// The address is where a condition is published, so it travels with one and
		// not otherwise: an answer carrying a spent number's refusal has no condition
		// to publish an address for, and naming one would send a reader looking for
		// an entry the page does not carry.
		if expansion.Condition != nil {
			result.Reference = haltref.ExplanationURL()
		}
		result.Guidance = explainGuidance(message, expansion, page)
		return textResult(result.Guidance, result)
	},
}

// explainGuidance composes the prose an assistant reads: the diagnostic it sent,
// the shared explanation of the condition that diagnostic named, whatever could
// not be added to that explanation, and where to read the authoring contract for
// the kind it concerns — page, named by its published address as every pointer
// here is (docPointerLine).
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
//
// An absent block is left out rather than joined as an empty one, so an answer
// about a spent number reads as the diagnostic, why the number names nothing now,
// and the pointer — with no blank gap where an explanation would have been.
func explainGuidance(message string, expansion *conditionExpansion, page string) string {
	blocks := []string{message}
	for _, block := range []string{expansion.Rendered, expansion.Note} {
		if block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(append(blocks, docPointerLine(page)), "\n\n")
}
