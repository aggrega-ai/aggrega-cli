// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// insertionPointArgs is the insertion-point tool's argument shape.
//
// File names the document, relative to the workspace root or by absolute path,
// as every other path argument on this surface is named.
//
// Container is the structural path to the container an entry goes inside, from
// the document root. It is the caller's statement of where such an entry belongs
// in a document of this kind; what this tool answers is where, in this
// particular document, that place is.
//
// PositionEncoding names the units the reported character offset is measured in.
// It is an argument rather than a fixed choice because this protocol runs no
// handshake to negotiate one, and a caller applying the position into its own
// buffer measures that buffer in units of its own. The default matches the one a
// session takes when a client negotiates nothing, so the two surfaces answer a
// caller that says nothing identically.
type insertionPointArgs struct {
	File             string   `json:"file"`
	Container        []string `json:"container"`
	PositionEncoding string   `json:"position_encoding"`
	WorkspaceRoot    string   `json:"workspace_root"`
}

// insertionPointPayload is the insertion-point tool's structured payload: the
// workspace the answer was computed over, and the answer itself, embedded so the
// keys are the ones every surface reports it under. The workspace root is absent,
// and marked by WorkspaceRootWithheld, where the caller is not told where the
// workspace sits on this host; the answer's own file is already named relative to
// that root.
type insertionPointPayload struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`
	serve.InsertionPointResult
}

var insertionPointToolSpec = toolSpec{
	summary: "Locates where a new entry belongs in an authored document.",
	description: "Locate where a new entry under a named container belongs in an authored document, so you can apply an " +
		"edit without parsing the file yourself. Pass `file` and `container` — the structural path from the document root " +
		"to the container the entry goes inside, such as the components map, a component's features map, or a dependency " +
		"socket. THE CONTAINER PATH IS YOURS TO SUPPLY: this reads the document's structure and never guesses where " +
		"something belongs from what it is called, what type it carries or what it appears to mean, so you say which key, " +
		"and it says where that key's entries go in THIS document. The answer is a zero-width `position`, the columns to " +
		"indent at, the nesting step measured from the document's own indentation, and `missingContainerKeys` — the " +
		"container keys the document does not have yet and that you must write before the entry, outermost first. " +
		"`entryShape` reports whether the container holds a mapping or a sequence, and reports `unknown` where the " +
		"container is not there yet or holds nothing, because the document has not said. Where " +
		"`precededByLineBreak` is set, begin your text with a line break: the position sits at the end of the line the " +
		"entry follows, which is in range whether or not the file ends in a newline and disturbs no byte already written. " +
		"It ANSWERS AND STOPS: it writes nothing to any file, composes no entry text and applies no edit — that is yours. " +
		"Pair it with attach-candidates or connection-candidates, which say WHAT may attach; this says WHERE it goes.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "file": {
      "type": "string",
      "minLength": 1,
      "description": "The document to locate a position in, relative to the workspace root or absolute. It must lie inside the workspace."
    },
    "container": {
      "type": "array",
      "minItems": 1,
      "items": {"type": "string", "minLength": 1},
      "description": "The structural path from the document root to the container the entry goes inside: each element a mapping key, or a sequence position written in decimal."
    },
    "position_encoding": {
      "type": "string",
      "enum": ["utf-8", "utf-16", "utf-32"],
      "description": "Units the reported character offset is measured in. Defaults to utf-16, which is what a session takes when a client negotiates nothing."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to the workspace root the server was launched with, or, where it was launched with none, to discovery from its working directory."
    }
  },
  "required": ["file", "container"]
}`,
	invoke: insertionPointInvoke,
}

func insertionPointInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a insertionPointArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("insertion-point", err)
		}
	}
	if strings.TrimSpace(a.File) == "" {
		return errorResult("insertion-point requires a 'file': the document to locate a position in, relative " +
			"to the workspace root or absolute. Call list-entities with response_format detailed to see the " +
			"documents this workspace declares.")
	}
	if len(a.Container) == 0 {
		return errorResult("insertion-point requires a 'container': the structural path from the document root " +
			"to the container the entry goes inside, each element a mapping key or a decimal sequence position. " +
			"Nothing infers that path for you — it is the shape you are authoring, and this tool answers where " +
			"that shape lives in this particular document.")
	}
	enc, ok := insertionPositionEncoding(a.PositionEncoding)
	if !ok {
		return errorResult(fmt.Sprintf("insertion-point does not report positions in %q. Pass one of utf-8, "+
			"utf-16 or utf-32, or omit position_encoding for utf-16.", a.PositionEncoding))
	}
	if err := ctx.Err(); err != nil {
		return errorResult(explainToolError("Could not locate an insertion point.", err, ""))
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to locate an insertion point in.", err, ""))
	}

	// The document is resolved here, through the one helper, before the operation
	// that reads it is reached. That operation confines the file to the root as
	// well — it must, since it is reached from another surface too — but a caller
	// refused there is answered in that operation's vocabulary rather than in this
	// surface's, and over a listener the sentence a caller needs is the one naming
	// the workspace this server was started over.
	file, refusal := tw.workspacePath("insertion-point", "file", a.File)
	if refusal != "" {
		return errorResult(refusal)
	}
	if tw, err = s.subjectWorkspace(tw, a.File); err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to locate an insertion point in.", err, ""))
	}

	result, err := serve.InsertionPoint(tw.root, file, a.Container, enc)
	if err != nil {
		return errorResult(explainDiagnostic(fmt.Sprintf(
			"No insertion point can be reported for %s in %q: %s", renderContainerPath(a.Container), a.File,
			err.Error()), ""))
	}
	payload := insertionPointPayload{InsertionPointResult: result}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderInsertionPointText(payload), payload))
}

// insertionPositionEncoding reads the encoding a call named, and reports whether
// it named one this surface reports positions in. An absent value takes the
// default a session takes when a client negotiates nothing, so a caller that
// says nothing is answered identically on both surfaces.
//
// An unrecognised value is refused rather than silently defaulted: a caller
// applying UTF-16 offsets into a buffer it measures in bytes corrupts a document
// on the first character outside ASCII, and a silent default is exactly what
// would hide that until then.
func insertionPositionEncoding(named string) (yamlpos.PositionEncoding, bool) {
	switch strings.ToLower(strings.TrimSpace(named)) {
	case "":
		return yamlpos.PositionEncodingUTF16, true
	case string(yamlpos.PositionEncodingUTF8):
		return yamlpos.PositionEncodingUTF8, true
	case string(yamlpos.PositionEncodingUTF16):
		return yamlpos.PositionEncodingUTF16, true
	case string(yamlpos.PositionEncodingUTF32):
		return yamlpos.PositionEncodingUTF32, true
	default:
		return "", false
	}
}

// renderContainerPath renders a container path as the dotted breadcrumb a reader
// recognises, for the refusals and the readable rendering.
func renderContainerPath(container []string) string {
	if len(container) == 0 {
		return "an empty container path"
	}
	return strings.Join(container, ".")
}

// renderInsertionPointText renders the human-readable answer: where the text
// goes, what has to be created on the way, and at which columns.
//
// The missing keys are stated in the text and not left to the structured field
// alone, because the text block is what an assistant reads when it does not
// parse the result — and a position applied without the containers it needs
// produces a document that no longer parses, which is the one failure this
// answer must not read as absent.
func renderInsertionPointText(p insertionPointPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Insertion point for %s in %s:\n", renderContainerPath(p.Container), p.File)
	fmt.Fprintf(&b, "  position: line %d, character %d (%s)\n",
		p.Position.Line, p.Position.Character, p.PositionEncoding)
	if p.PrecededByLineBreak {
		b.WriteString("  begin the text you write with a line break\n")
	} else {
		b.WriteString("  the document carries nothing yet; write from the start of the file\n")
	}
	if len(p.MissingContainerKeys) > 0 {
		indent := p.EntryIndent - len(p.MissingContainerKeys)*p.IndentStep
		b.WriteString("  write these container keys first, outermost first:\n")
		for i, key := range p.MissingContainerKeys {
			fmt.Fprintf(&b, "      %s: at column %d\n", key, indent+i*p.IndentStep)
		}
	} else {
		b.WriteString("  the container is already there; write the entry only\n")
	}
	fmt.Fprintf(&b, "  entry at column %d, shape %s\n", p.EntryIndent, p.EntryShape)
	if p.IndentStepMeasured {
		fmt.Fprintf(&b, "  nesting step %d, measured from this document\n", p.IndentStep)
	} else {
		fmt.Fprintf(&b, "  nesting step %d, this surface's fallback: the document exhibits no nesting to measure\n",
			p.IndentStep)
	}
	return b.String()
}
