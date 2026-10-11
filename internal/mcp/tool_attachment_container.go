// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
)

// attachmentContainerArgs is the attachment-container tool's argument shape.
//
// GraphNode names a node on the compiled graph of a Release, by the id that
// graph keys it under, and Release says which Release's graph to build. Both are
// required: a node id is meaningful only against the graph it is a key of.
//
// There is deliberately no name-addressed form. A workspace document's
// metadata.name names an entity rather than an instance of one, and one entity
// may be instantiated at several places in a single Aggregate, so such a name
// has no one container to report.
//
// Connection names one connection the graph node draws, when the attachment is
// for that connection rather than for the node; its document is required,
// because either document may hold an attachment on a connection the Aggregate
// drew. It is the daemon's request shape for the same thing, so the two surfaces
// accept and refuse the same coordinates.
type attachmentContainerArgs struct {
	GraphNode     string                      `json:"graph_node"`
	Release       string                      `json:"release"`
	Connection    *serve.ConnectionCoordinate `json:"connection"`
	WorkspaceRoot string                      `json:"workspace_root"`
	CatalogDirs   []string                    `json:"catalog_dirs"`
}

// attachmentContainerPayload is the attachment-container tool's structured
// payload: the workspace the answer was computed over, and the answer itself,
// embedded so the keys are the ones every surface reports it under.
//
// Both locations in it are told as the caller may be told them. The workspace
// root is absent, and marked by WorkspaceRootWithheld, where the caller is not
// told where the workspace sits on this host. The answer's file is spelled
// relative to that root where it lies inside the workspace; one outside it is
// left empty in the embedded answer and marked by FileWithheld, which follows the
// answer rather than sitting beside the field, because the answer's keys are the
// shared shape's and stay in that shape's order.
type attachmentContainerPayload struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`
	serve.AttachmentContainerResult
	FileWithheld bool `json:"file_withheld,omitempty"`
}

var attachmentContainerToolSpec = toolSpec{
	kinds:   []string{"aggregate", "component", "feature"},
	summary: "Reports which document and which container a Feature attachment belongs in.",
	description: "Report where a Feature attachment for a node belongs: which document it is written in, and the container " +
		"inside that document the attachment goes in. Pass `graph_node` — a node id on a compiled graph, as the graph keys " +
		"it — and the `release` whose graph it is. DO NOT ASSEMBLE THIS PATH YOURSELF: a node's id and the scope it sits in " +
		"are each joined with the same separator, so from outside there is no way to tell a separator apart from a " +
		"character in a name, and a path you composed would look right while naming a key nobody wrote. Ask for it. The " +
		"answer's `container` is a list of segments, in the exact shape insertion-point takes, so pass `file` and " +
		"`container` straight to insertion-point to learn where in that document's bytes the text goes. `file` is part of " +
		"the answer rather than something you already know: a node contributed by a sub-aggregate was authored in that " +
		"module's own document, not in the Aggregate the Release names. For an attachment on one CONNECTION the node's " +
		"component draws, add `connection` with its `socket`, `alias` and `document` — `aggregate` or `release`, either of " +
		"which may hold one: the answer is then that connection's own features container in that document. In the " +
		"Release it is the per-edge entry under the Release's entry for the node, reported whether or not the Release " +
		"writes that per-edge entry yet, so insertion-point names the keys still to write; a Release writing no entry " +
		"for the node is refused. A connection the Release drew is refused in the Aggregate, which holds no entry for " +
		"it, and one a Feature drew is refused in both, because nothing is attached to it. It ANSWERS AND STOPS: it " +
		"creates no attachment, writes nothing to any file and composes no text — that is yours. Pair it with " +
		"attach-candidates, which says WHAT may attach; this says WHICH CONTAINER it goes in, and insertion-point says " +
		"WHERE in the file.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "graph_node": {
      "type": "string",
      "minLength": 1,
      "description": "A node id on the compiled graph of a Release, as the graph keys it. A node contributed by a sub-aggregate carries a qualified id."
    },
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the Release whose graph carries graph_node, absolute or relative to the workspace root."
    },
    "connection": {
      "type": "object",
      "description": "One connection the graph_node's component draws, when the attachment is for that connection rather than for the node.",
      "properties": {
        "socket": { "type": "string", "minLength": 1, "description": "The dependency socket the connection is wired through." },
        "alias": { "type": "string", "minLength": 1, "description": "The name the connection carries within its socket." },
        "document": { "type": "string", "enum": ["aggregate", "release"], "description": "The document the attachment is written in." }
      },
      "required": ["socket", "alias", "document"]
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to the workspace root the server was launched with, or, where it was launched with none, to discovery from its working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack while the graph is built."
    }
  },
  "required": ["graph_node", "release"]
}`,
	invoke: attachmentContainerInvoke,
}

func attachmentContainerInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a attachmentContainerArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("attachment-container", err)
		}
	}
	if strings.TrimSpace(a.GraphNode) == "" {
		return errorResult("attachment-container requires a 'graph_node': a node id on a compiled graph, as the " +
			"graph keys it. Read the ids from the graph a build or a describe returns, and note that a node " +
			"contributed by a sub-aggregate carries a qualified id.")
	}
	if strings.TrimSpace(a.Release) == "" {
		return errorResult("attachment-container requires a 'release': a node id names a node on a Release's " +
			"compiled graph, so there is no graph to look in without one. Call list-releases with response_format " +
			"detailed to see the Releases this workspace declares.")
	}
	if a.Connection != nil {
		if field, reason := a.Connection.Fault(true); field != "" {
			return errorResult("attachment-container: 'connection." + field + "' " + reason + ".")
		}
	}
	if err := ctx.Err(); err != nil {
		return errorResult(explainToolError("Could not report where an attachment belongs.", err, ""))
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace to report where an attachment belongs.", err, ""))
	}

	releasePath, pathRefused := tw.workspacePath("attachment-container", "release", a.Release)
	if pathRefused != "" {
		return errorResult(pathRefused)
	}
	if tw, err = s.subjectWorkspace(tw, a.Release); err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace to report where an attachment belongs.", err, ""))
	}
	lf, lockRefused := tw.loadReleaseLockfile(releasePath)
	if lockRefused != "" {
		return errorResult(lockRefused)
	}
	view := tw.newView(releasePath, lf)

	// The render is skipped. This reads how the node was reached and discards
	// everything else, and the graph is byte-identical whether or not the render
	// ran, so skipping it keeps a question about an authoring site from loading a
	// rendering plugin to answer itself.
	preview, err := compilerops.PreviewForArtifactFromAvailablePins(
		ctx,
		compilerops.NewPreviewInputs(
			compilerops.PinnedExternalInputs(tw.root, tw.cacheDir, tw.cfg, lf),
			view,
			releasePath,
			tw.root,
			tw.cfg,
			s.serverVersion,
			compilerops.WithLockfile(lf, lf != nil),
		),
		pipeline.Options{SkipRender: true},
	)
	if err != nil {
		return errorResult(explainToolError(
			"Could not build the Release's graph to report where an attachment for a node on it belongs.",
			err, previewFailureKind(preview, err)))
	}

	// The graph comes off the finished payload: the refusal above returned, so the
	// run this reports a container from is one that ended.
	graph := preview.PipelineOutcome().Completed().IRGraph
	var result serve.AttachmentContainerResult
	if a.Connection != nil {
		var release *types.Release
		if preview.Resolved != nil {
			release = preview.Resolved.Release
		}
		var refused serve.ConnectionRefusal
		result, refused = serve.ConnectionAttachmentContainer(tw.root, graph, release, releasePath, a.GraphNode,
			*a.Connection)
		if refused != serve.ConnectionAnswered {
			return errorResult(explainDiagnostic(fmt.Sprintf(
				"No container is reported for the connection %q through socket %q of %q on the compiled graph of %s: %s.",
				a.Connection.Alias, a.Connection.Socket, a.GraphNode, a.Release, refused.Reason()), "release"))
		}
	} else {
		var refusal serve.AttachmentContainerRefusal
		result, refusal = serve.AttachmentContainer(graph, a.GraphNode)
		if refusal != serve.AttachmentContainerReported {
			return errorResult(explainDiagnostic(fmt.Sprintf(
				"No container is reported for %q on the compiled graph of %s: %s.",
				a.GraphNode, a.Release, refusal.Reason()), "release"))
		}
	}

	payload := attachmentContainerPayload{AttachmentContainerResult: result}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	if file, told := tw.reportedLocation(result.File); told {
		payload.File = file
	} else {
		payload.File, payload.FileWithheld = "", true
	}
	return tw.answer(textResult(renderAttachmentContainerText(payload), payload))
}

// renderAttachmentContainerText renders the human-readable answer: the document,
// the container, and what to do with the pair.
//
// The next call is named in the text rather than left to the structured fields,
// because the text block is what an assistant reads when it does not parse the
// result — and this answer is a coordinate for another call rather than
// something to act on by itself.
func renderAttachmentContainerText(p attachmentContainerPayload) string {
	var b strings.Builder
	file := p.File
	if p.FileWithheld {
		file = keptBackLocationWords
	}
	fmt.Fprintf(&b, "An attachment for %s belongs in %s, under %s\n",
		p.Node, file, renderContainerPath(p.Container))
	b.WriteString("  the container path is a list of segments; pass it and the file to insertion-point to learn " +
		"where in that document the text goes\n")
	b.WriteString("  this reports a location and creates nothing: no attachment was made and no file was written")
	return b.String()
}
