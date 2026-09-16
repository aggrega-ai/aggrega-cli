// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
)

// listEntitiesArgs is the list-entities tool's argument shape.
//
// There is no argument that selects a subject, and that is the point of the
// tool: it answers what the workspace holds, which is a property of the
// workspace rather than of anything a caller would have to name first. The two
// arguments it does take only say which workspace, and both have defaults.
type listEntitiesArgs struct {
	WorkspaceRoot string   `json:"workspace_root"`
	CatalogDirs   []string `json:"catalog_dirs"`
}

// listEntitiesResult is the list-entities tool's structured payload: the
// workspace the answer was computed over, and every entity in it.
//
// The entities carry the shape a workspace entity is reported under, rather
// than a second description of one assembled here, so the identity a caller
// reads from this tool is the identity every other surface reports the same
// entity under.
//
// Entities is always an array, empty when the workspace declares none, so a
// client iterates without a null check.
type listEntitiesResult struct {
	WorkspaceRoot string                `json:"workspace_root"`
	Count         int                   `json:"count"`
	Entities      []serve.CatalogEntity `json:"entities"`
}

var listEntitiesToolSpec = toolSpec{
	description: "List every Aggrega entity a workspace holds, reading the files and writing nothing. Returns each entity's " +
		"kind, name, apiVersion, description, labels and source file, read verbatim from the document that declares it. " +
		"Call this first in an unfamiliar workspace: it takes no subject, so it is how you obtain the names the other " +
		"tools accept — describe, validate and build refuse a name they cannot resolve rather than answering empty. A " +
		"document counts as an entity when it declares both a kind and a name; foreign YAML declaring no kind is skipped. " +
		"Pair it with list-releases and join the two on kind, name and file.",
	workspaceSelectionNote: "Optional `workspace_root` and `catalog_dirs` override discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack. Features they declare are reported alongside the workspace's own."
    }
  }
}`,
	invoke: listEntitiesInvoke,
}

func listEntitiesInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a listEntitiesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("list-entities", err)
		}
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to list its entities.", err, "component"))
	}

	entities := serve.WorkspaceEntityIndex(ctx, tw.root, tw.newView(nil))
	payload := listEntitiesResult{WorkspaceRoot: tw.root, Count: len(entities), Entities: entities}
	return textResult(renderListEntitiesText(payload), payload)
}

// renderListEntitiesText renders the human-readable entity listing: a heading
// carrying the count, then one line per entity giving the coordinates a caller
// addresses it by.
func renderListEntitiesText(p listEntitiesResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Entities in %s (%d):\n", p.WorkspaceRoot, p.Count)
	for _, e := range p.Entities {
		fmt.Fprintf(&b, "  %s %s", e.Kind, e.Name)
		if e.File != "" {
			fmt.Fprintf(&b, " [%s]", e.File)
		} else {
			fmt.Fprintf(&b, " [%s]", e.Origin)
		}
		if strings.TrimSpace(e.Description) != "" {
			fmt.Fprintf(&b, ": %s", e.Description)
		}
		b.WriteByte('\n')
	}
	if len(p.Entities) > 0 {
		b.WriteString("\nLabels and apiVersion for each entity are in the structured result.")
	}
	return strings.TrimRight(b.String(), "\n")
}

// listReleasesArgs is the list-releases tool's argument shape. Like the entity
// listing it names no subject: which Releases a workspace holds is a property of
// the workspace.
type listReleasesArgs struct {
	WorkspaceRoot string `json:"workspace_root"`
}

// listReleasesResult is the list-releases tool's structured payload: the
// workspace the answer was computed over, and every Release in it, reported
// under the same workspace-relative path the entity listing reports a file
// under so the two answers line up.
type listReleasesResult struct {
	WorkspaceRoot string   `json:"workspace_root"`
	Count         int      `json:"count"`
	Releases      []string `json:"releases"`
}

var listReleasesToolSpec = toolSpec{
	kinds: []string{"release"},
	description: "List every Release a workspace holds, reading the files and writing nothing. Returns each Release's path " +
		"relative to the workspace root — the coordinate list-entities reports a file under, so the two answers line up. " +
		"Membership is decided by the file's own kind header, never by its name: a Release named without a conventional " +
		"prefix is included, and a file merely named like one is not. Call this when you need to name a Release and do " +
		"not know which exist — build, describe and validate each require one named and none can enumerate for you. " +
		"Feed a returned path straight to the `release` argument of describe or build.",
	workspaceSelectionNote: "Optional `workspace_root` overrides discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  }
}`,
	invoke: listReleasesInvoke,
}

func listReleasesInvoke(s *server, _ context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a listReleasesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("list-releases", err)
		}
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to list its Releases.", err, "release"))
	}

	releases := serve.ReleaseCandidates(tw.root)
	payload := listReleasesResult{WorkspaceRoot: tw.root, Count: len(releases), Releases: releases}
	return textResult(renderListReleasesText(payload), payload)
}

// renderListReleasesText renders the human-readable Release listing, and says
// what an empty one means: the walk ran and found none, rather than the question
// having been refused.
func renderListReleasesText(p listReleasesResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Releases in %s (%d):\n", p.WorkspaceRoot, p.Count)
	if p.Count == 0 {
		b.WriteString("  none — no document under this root declares kind: Release")
		return b.String()
	}
	for _, r := range p.Releases {
		fmt.Fprintf(&b, "  %s\n", r)
	}
	return strings.TrimRight(b.String(), "\n")
}
