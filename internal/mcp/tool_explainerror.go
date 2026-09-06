// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// explainErrorResult is the explain-error tool's structured payload.
type explainErrorResult struct {
	Message    string `json:"message"`
	Guidance   string `json:"guidance"`
	DocPointer string `json:"doc_pointer"`
}

var explainErrorToolSpec = toolSpec{
	description: "Expand a compiler diagnostic or error message into actionable, human-readable guidance, " +
		"writing nothing. " +
		"Pass the exact diagnostic text you got back from a validate or build call as `message`, and " +
		"optionally the entity kind it concerns (release, aggregate, component, feature, recipe, manifest, " +
		"and so on) as `kind`. The tool returns the diagnostic verbatim plus a pointer to the language-reference " +
		"page for that kind, so you can read the authoring contract and fix the input. It does not guess at " +
		"causes beyond what the diagnostic already states; the compiler's own message carries the remediation.",
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
		if strings.TrimSpace(a.Message) == "" {
			return errorResult("explain-error requires a non-empty 'message' argument")
		}
		guidance := explainDiagnostic(a.Message, a.Kind)
		return textResult(guidance, explainErrorResult{
			Message:    strings.TrimSpace(a.Message),
			Guidance:   guidance,
			DocPointer: docPointerFor(a.Kind),
		})
	},
}
