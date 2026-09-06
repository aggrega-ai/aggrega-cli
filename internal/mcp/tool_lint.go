// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
)

// lintRequest is the in-process lint operation's argument shape, translated from
// the tool's wire arguments. Exactly one of entity or release selects the
// subject.
type lintRequest struct {
	entity        string
	release       string
	prePublish    bool
	plugin        string
	workspaceRoot string
}

// lintRunner runs the lint operation over an in-process request, writing its
// output to the summary (stdout) and diagnostic (stderr) writers. It is the seam
// the lint tool drives: the production value runs the shared lint core in-process
// on the server's resolved workspace, and a test injects a fake that records the
// request.
type lintRunner func(ctx context.Context, req lintRequest, stdout, stderr io.Writer) error

// lintArgs is the lint tool's argument shape. Exactly one of entity or release
// selects the subject.
type lintArgs struct {
	Entity        string `json:"entity"`
	Release       string `json:"release"`
	PrePublish    bool   `json:"pre_publish"`
	Plugin        string `json:"plugin"`
	WorkspaceRoot string `json:"workspace_root"`
}

// lintResultPayload is the lint tool's structured payload: the operation's
// combined output — the success summary plus any advisory findings.
type lintResultPayload struct {
	Output string `json:"output"`
}

var lintToolSpec = toolSpec{
	kinds: []string{"aggregate", "component", "feature", "featurerecipe", "manifest", "mappinglibrary", "plugin", "recipe", "release", "schemalibrary"},
	description: "Run the publishing-discipline linter over a catalog entity, or validate a Release's resolved " +
		"dependency closure, driving the compiler's own lint operation and writing nothing. Two modes: pass `entity` (a catalog entity YAML " +
		"path) to run the publishing rules — naming shape, category-matches-directory, " +
		"annotation-prefix discipline, and the like — which report at warn severity over local content without " +
		"consulting any registry; or pass `release` (a Release YAML path) to validate every catalog entity the Release " +
		"reaches against its composed schemas. With `pre_publish` (entity mode only) the previous published version is " +
		"fetched and the proposed version bump is verified against the schema-evolution classification, halting on a " +
		"mismatch. `plugin` overrides the workspace-configured linter attachments with a filesystem path. The operation is " +
		"offline-first: a missing artifact fails loudly rather than reaching for a network. Use it before publishing an " +
		"entity, or to check a whole Release's dependencies in one pass.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "entity": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a catalog entity YAML to lint against the publishing-discipline rules. Mutually exclusive with release."
    },
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a Release YAML to validate over its resolved dependency closure. Mutually exclusive with entity."
    },
    "pre_publish": {
      "type": "boolean",
      "description": "Fetch the previous published version and verify the proposed version bump against the schema-evolution classification. Only meaningful in entity mode."
    },
    "plugin": {
      "type": "string",
      "description": "Filesystem path to the publishing-linter .wasm artifact, run in place of the workspace-configured linter attachments. Only meaningful in entity mode."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  },
  "anyOf": [
    {"required":["entity"],"not":{"required":["release"]}},
    {"required":["release"],"not":{"required":["entity"]}}
  ]
}`,
	invoke: lintInvoke,
}

func lintInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a lintArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("lint", err)
		}
	}
	entity := strings.TrimSpace(a.Entity)
	release := strings.TrimSpace(a.Release)
	switch {
	case entity != "" && release != "":
		return errorResult("lint takes exactly one of 'entity' or 'release', not both")
	case entity == "" && release == "":
		return errorResult("lint requires either an 'entity' (catalog entity path) or a 'release' (Release path)")
	}

	kind := "component"
	req := lintRequest{
		entity:        entity,
		release:       release,
		prePublish:    a.PrePublish,
		plugin:        strings.TrimSpace(a.Plugin),
		workspaceRoot: strings.TrimSpace(a.WorkspaceRoot),
	}
	if release != "" {
		kind = "release"
	}

	run := s.runLint
	if run == nil {
		run = s.lintInProcess
	}
	var stdout, stderr bytes.Buffer
	if err := run(ctx, req, &stdout, &stderr); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return errorResult(explainToolError("Lint reported findings that halt publication.", fmt.Errorf("%s", detail), kind))
	}

	// The success summary lands on stdout; advisory findings (warnings) land on
	// stderr even on a clean pass, so both are surfaced.
	out := strings.TrimRight(stdout.String(), "\n")
	if advisories := strings.TrimSpace(stderr.String()); advisories != "" {
		if out != "" {
			out += "\n\n"
		}
		out += "Advisories:\n" + advisories
	}
	return textResult(out, lintResultPayload{Output: stdout.String() + stderr.String()})
}

// lintInProcess runs the lint operation in-process over the shared lint core,
// resolving the server's tool workspace exactly as the sibling tools do. The
// success summary is written to stdout and diagnostic findings to stderr, kept on
// separate streams so a clean pass still surfaces its advisories. The policy
// resolver passes the workspace value through unchanged — the tool exposes no
// allow/enforce overrides, so the effective policy is always the workspace value.
func (s *server) lintInProcess(ctx context.Context, req lintRequest, stdout, stderr io.Writer) error {
	tw, err := s.resolveToolWorkspace(req.workspaceRoot, nil)
	if err != nil {
		return err
	}
	if req.release != "" {
		return lintops.RunRelease(tw.rw, tw.absWorkspacePath(req.release), nil, stdout, stderr, nil)
	}
	pluginPath := req.plugin
	if pluginPath != "" {
		pluginPath = tw.absWorkspacePath(pluginPath)
	}
	return lintops.RunEntity(lintops.Config{
		EntityPath:     tw.absWorkspacePath(req.entity),
		PrePublish:     req.prePublish,
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      tw.rw,
		Out:            stdout,
		DiagOut:        stderr,
	})
}
