// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// connectionAtPositionToolName is the tool's advertised name, held as a
// constant because the refusals below name it and a second spelling of it
// would be free to drift from the one the curated table gives.
const connectionAtPositionToolName = "connection-at-position"

// connectionAtPositionArgs is the connection-at-position tool's argument shape.
//
// File names the document, relative to the workspace root or by absolute path,
// as the insertion-point tool names one, and it is read from disk the way that
// tool reads it. Position is the place in it, and a pointer so a call that left
// it out is refused rather than read as the document's first character.
//
// PositionEncoding names the units the position's character offset is measured
// in, for the reason the insertion-point tool takes one: this protocol runs no
// handshake to negotiate an encoding, and a caller measures its own buffer in
// units of its own. The default is the one a session takes when a client
// negotiates nothing, so the two surfaces read a caller that says nothing alike.
//
// Release names the Release whose compiled graph records the connections, because
// a graph node and a connection exist only on one.
type connectionAtPositionArgs struct {
	File             string        `json:"file"`
	Position         *toolPosition `json:"position"`
	PositionEncoding string        `json:"position_encoding"`
	Release          string        `json:"release"`
	WorkspaceRoot    string        `json:"workspace_root"`
	CatalogDirs      []string      `json:"catalog_dirs"`
}

// toolPosition is a position as a call sends it. Both halves are pointers so a
// position missing one of them is refused, as its declared schema refuses it,
// rather than read as a zero the caller never sent.
type toolPosition struct {
	Line      *int `json:"line"`
	Character *int `json:"character"`
}

// position reads the call's position, and reports whether it names a place: both
// halves sent, and neither below zero.
func (p *toolPosition) position() (yamlpos.Position, bool) {
	if p == nil || p.Line == nil || p.Character == nil || *p.Line < 0 || *p.Character < 0 {
		return yamlpos.Position{}, false
	}
	return yamlpos.Position{Line: *p.Line, Character: *p.Character}, true
}

// connectionAtPositionPayload is the tool's structured payload: the workspace the
// answer was computed over, and the answer itself, embedded so the keys are the
// ones every surface reports it under. The workspace root is absent, and marked
// by WorkspaceRootWithheld, where the caller is not told where the workspace sits
// on this host; the answer names no file.
type connectionAtPositionPayload struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`
	serve.ConnectionAtPositionResult
}

var connectionAtPositionToolSpec = toolSpec{
	kinds:   []string{"aggregate", "release"},
	summary: "Reports which connection's entry a position in an Aggregate or a Release falls inside.",
	description: "Report which connection's entry a position in an Aggregate or a Release falls inside, so you can ask " +
		"about attaching to that connection without reading the document's structure yourself. Pass `file`, the " +
		"document; `position`, a `line` and a `character` both counted from zero; and the `release` whose compiled " +
		"graph records the connections — a connection exists only on a compiled graph, so the Release is required " +
		"even for a position in the Aggregate. The answer is `graphNode` and `connection` — its `socket`, its `alias` " +
		"and the `document` the position was read in — exactly the coordinates attach-candidates and " +
		"attachment-container take, so pass them on unchanged; or, where the position is on no entry an attachment " +
		"is written in, `noConnectionReason` saying why. A position anywhere inside an entry — on its alias, its " +
		"target or its attachments — answers that entry's connection; a position on a socket's own key, above the " +
		"entries under it, answers none. The entries are the ones the compiled graph records: a connection a " +
		"component draws, at its alias-keyed entry in the document the component was authored in, and every per-edge " +
		"entry the Release writes. IT DOES NOT READ MEANING: whether a key is a connection is never guessed from what " +
		"it is called. Pass `position_encoding` to say which units `character` counts in; utf-16 when omitted. It " +
		"ANSWERS AND STOPS: it writes nothing to any file.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "file": {
      "type": "string",
      "minLength": 1,
      "description": "The Aggregate or Release document the position is in, relative to the workspace root or absolute. It must lie inside the workspace."
    },
    "position": {
      "type": "object",
      "description": "The place in the document: a line and a character, both counted from zero.",
      "properties": {
        "line": { "type": "integer", "minimum": 0, "description": "The line, counted from zero." },
        "character": { "type": "integer", "minimum": 0, "description": "The offset within the line, counted from zero, in the units position_encoding names." }
      },
      "required": ["line", "character"]
    },
    "position_encoding": {
      "type": "string",
      "enum": ["utf-8", "utf-16", "utf-32"],
      "description": "Units the position's character offset is measured in. Defaults to utf-16, which is what a session takes when a client negotiates nothing."
    },
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the Release whose compiled graph records the connections, absolute or relative to the workspace root."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. ` + workspaceRootArgumentRule + `"
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories (absolute, or relative to the workspace root), prepended (first-match-wins) to the workspace catalog search stack while the graph is built."
    }
  },
  "required": ["file", "position", "release"]
}`,
	invoke: connectionAtPositionInvoke,
}

func connectionAtPositionInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a connectionAtPositionArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(connectionAtPositionToolName, err)
		}
	}
	if strings.TrimSpace(a.File) == "" {
		return errorResult(connectionAtPositionToolName + " requires a 'file': the Aggregate or Release document the " +
			"position is in, relative to the workspace root or absolute. Call list-entities with response_format " +
			"detailed to see the documents this workspace declares.")
	}
	pos, placed := a.Position.position()
	if !placed {
		return errorResult(connectionAtPositionToolName + " requires a 'position': the place in the document, as a " +
			"'line' and a 'character', both sent and both counted from zero.")
	}
	if strings.TrimSpace(a.Release) == "" {
		return errorResult(connectionAtPositionToolName + " requires a 'release': a connection exists only on a " +
			"Release's compiled graph, so there is no connection to report without one, even for a position in the " +
			"Aggregate. Call list-releases with response_format detailed to see the Releases this workspace declares.")
	}
	enc, ok := insertionPositionEncoding(a.PositionEncoding)
	if !ok {
		return errorResult(fmt.Sprintf("%s does not read positions in %q. Pass one of utf-8, utf-16 or utf-32, or "+
			"omit position_encoding for utf-16.", connectionAtPositionToolName, a.PositionEncoding))
	}
	if err := ctx.Err(); err != nil {
		return errorResult(explainToolError("Could not report the connection at a position.", err, ""))
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace to report the connection at a position in.", err, ""))
	}

	// The document is resolved here, through the one helper, before the operation
	// that reads it is reached, as the insertion-point tool resolves it: the
	// operation confines the file to the root as well, but a caller refused there is
	// answered in that operation's vocabulary rather than in this surface's.
	file, refusal := tw.workspacePath(connectionAtPositionToolName, "file", a.File)
	if refusal != "" {
		return errorResult(refusal)
	}
	releasePath, refusal := tw.workspacePath(connectionAtPositionToolName, "release", a.Release)
	if refusal != "" {
		return errorResult(refusal)
	}
	// Both arguments name a path, and the call is about the Release: the document
	// is read in that Release's graph.
	if tw, err = s.subjectWorkspace(tw, a.Release); err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace to report the connection at a position in.", err, ""))
	}
	lf, lockPins, refusal := tw.loadReleaseLockfile(releasePath)
	if refusal != "" {
		return errorResult(refusal)
	}
	view := tw.newView(releasePath, lockPins)

	// The render is skipped. This reads where the graph records each connection's
	// entry and discards everything else, and the graph is byte-identical whether
	// or not the render ran.
	preview, err := compilerops.PreviewForArtifactFromAvailablePins(
		ctx,
		compilerops.NewPreviewInputs(
			compilerops.PinnedExternalInputs(tw.root, tw.cacheDir, tw.cfg, lf, tw.rw.CacheWriteRefusal()),
			view,
			releasePath,
			tw.root,
			tw.cfg,
			s.serverVersion,
			compilerops.WithLockfile(lf, lf != nil),
			compilerops.WithDisplay(s.runDisplay()),
		),
		pipeline.Options{SkipRender: true},
	)
	if err != nil {
		return errorResult(explainToolError(
			"Could not build the Release's graph to report the connection at a position in a document.",
			err, previewFailureKind(preview, err)))
	}

	// The graph comes off the finished payload: the refusal above returned, so the
	// run this reads the connections from is one that ended.
	graph := preview.PipelineOutcome().Completed().IRGraph
	var release *types.Release
	if preview.Resolved != nil {
		release = preview.Resolved.Release
	}
	result, err := serve.ConnectionAtPosition(tw.root, graph, release, releasePath, file, pos, enc)
	if err != nil {
		return errorResult(explainDiagnostic(fmt.Sprintf("No connection can be reported at a position in %q: %s",
			a.File, err.Error()), ""))
	}

	payload := connectionAtPositionPayload{ConnectionAtPositionResult: result}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderConnectionAtPositionText(a.File, pos, enc, payload), payload))
}

// renderConnectionAtPositionText renders the human-readable answer: the
// connection the position falls on and what to do with it, or why there is none.
//
// The reason is stated in words from the one home of that wording, and the next
// calls are named, because the text block is what an assistant reads when it
// does not parse the structured answer — and a bare "no connection" read without
// its reason is indistinguishable from a position nothing was looked for at.
func renderConnectionAtPositionText(file string, pos yamlpos.Position, enc yamlpos.PositionEncoding,
	p connectionAtPositionPayload) string {
	var b strings.Builder
	where := fmt.Sprintf("line %d, character %d (%s) of %s", pos.Line, pos.Character, enc, file)
	if p.Connection == nil {
		fmt.Fprintf(&b, "No connection is reported at %s: %s.\n", where, p.NoConnectionReason.Sentence())
		b.WriteString("  this reports and changes nothing: no file was written")
		return b.String()
	}
	fmt.Fprintf(&b, "The entry at %s writes the connection %s through socket %s of %s, in the %s\n",
		where, p.Connection.Alias, p.Connection.Socket, p.GraphNode, p.Connection.Document)
	b.WriteString("  pass graph_node, the same release and this connection to attach-candidates for the slots it " +
		"declares, and to attachment-container for where an attachment on it goes\n")
	b.WriteString("  this reports and changes nothing: no file was written")
	return b.String()
}
