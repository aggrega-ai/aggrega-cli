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
type attachmentContainerArgs struct {
	GraphNode     string   `json:"graph_node"`
	Release       string   `json:"release"`
	WorkspaceRoot string   `json:"workspace_root"`
	CatalogDirs   []string `json:"catalog_dirs"`
}

// attachmentContainerPayload is the attachment-container tool's structured
// payload: the workspace the answer was computed over, and the answer itself,
// embedded so the keys are the ones every surface reports it under.
type attachmentContainerPayload struct {
	WorkspaceRoot string `json:"workspace_root"`
	serve.AttachmentContainerResult
}

var attachmentContainerToolSpec = toolSpec{
	kinds: []string{"aggregate", "component", "feature"},
	description: "Report where a Feature attachment for a node belongs: which document it is written in, and the container " +
		"inside that document the attachment goes in. Pass `graph_node` — a node id on a compiled graph, as the graph keys " +
		"it — and the `release` whose graph it is. DO NOT ASSEMBLE THIS PATH YOURSELF: a node's id and the scope it sits in " +
		"are each joined with the same separator, so from outside there is no way to tell a separator apart from a " +
		"character in a name, and a path you composed would look right while naming a key nobody wrote. Ask for it. The " +
		"answer's `container` is a list of segments, in the exact shape insertion-point takes, so pass `file` and " +
		"`container` straight to insertion-point to learn where in that document's bytes the text goes. `file` is part of " +
		"the answer rather than something you already know: a node contributed by a sub-aggregate was authored in that " +
		"module's own document, not in the Aggregate the Release names. It ANSWERS AND STOPS: it creates no attachment, " +
		"writes nothing to any file and composes no text — that is yours. Pair it with attach-candidates, which says WHAT " +
		"may attach; this says WHICH CONTAINER it goes in, and insertion-point says WHERE in the file.",
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
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
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
			"compiled graph, so there is no graph to look in without one. Call list-releases to see the Releases " +
			"this workspace declares.")
	}
	if err := ctx.Err(); err != nil {
		return errorResult(explainToolError("Could not report where an attachment belongs.", err, ""))
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError(
			"Could not resolve the workspace to report where an attachment belongs.", err, ""))
	}

	releasePath := tw.absWorkspacePath(a.Release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	// The render is skipped. This reads how the node was reached and discards
	// everything else, and the graph is byte-identical whether or not the render
	// ran, so skipping it keeps a question about an authoring site from loading a
	// rendering plugin to answer itself.
	preview, err := compilerops.PreviewForArtifactFromAvailablePins(
		ctx,
		compilerops.NewPreviewInputs(
			compilerops.FrozenExternalInputs(tw.cacheDir, lf),
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
	result, refusal := serve.AttachmentContainer(preview.PipelineOutcome().Completed().IRGraph, a.GraphNode)
	if refusal != serve.AttachmentContainerReported {
		return errorResult(explainDiagnostic(fmt.Sprintf(
			"No container is reported for %q on the compiled graph of %s: %s.",
			a.GraphNode, a.Release, refusal.Reason()), "release"))
	}

	payload := attachmentContainerPayload{WorkspaceRoot: tw.root, AttachmentContainerResult: result}
	return textResult(renderAttachmentContainerText(payload), payload)
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
	fmt.Fprintf(&b, "An attachment for %s belongs in %s, under %s.\n",
		p.Node, p.File, renderContainerPath(p.Container))
	b.WriteString("  the container path is a list of segments; pass it and the file to insertion-point to learn " +
		"where in that document the text goes\n")
	b.WriteString("  this reports a location and creates nothing: no attachment was made and no file was written")
	return b.String()
}
