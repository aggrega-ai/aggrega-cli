// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	// collect gathers the run's findings as data alongside the human report, so
	// this surface renders them structurally instead of re-reading them out of the
	// text it printed. It mirrors lintops.Config.Collect, which is where the lint
	// core already carries the same channel, rather than adding a parameter beside
	// the writers: the collector is part of what the caller asks the operation for,
	// and the two shapes agreeing is what keeps the seam one shape.
	//
	// Nil collects nothing and leaves every other behaviour untouched, which is
	// what keeps collection additive — the writer streams are byte-identical
	// whether a collector is threaded or not.
	collect *lintops.FindingCollector
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

// lintStructuredResult returns the run's findings as this tool's structured
// payload, or an untyped nil when the run never reached the point of reporting.
//
// The payload is the lint core's own report type, which is the shape the command
// line's structured output already emits, so a finding's severity, rule identity,
// message and position have one field vocabulary across both surfaces rather than
// one per surface.
//
// A run that failed before any rule executed — an unreadable entity, a linter
// plugin that will not resolve — emits no payload at all. An empty report there
// would claim a clean lint over a lint that never happened, which is a different
// statement from "the rules ran and found nothing".
//
// The nil is returned as an untyped nil deliberately. A typed nil pointer placed
// in an interface is not equal to nil, so returning one would defeat the
// omit-when-absent check at the construction sites and put a null on the wire in
// place of an omitted key.
func lintStructuredResult(collect *lintops.FindingCollector) any {
	if !collect.Reached() {
		return nil
	}
	return collect.Report()
}

var lintToolSpec = toolSpec{
	kinds:   []string{"aggregate", "component", "feature", "featurerecipe", "manifest", "mappinglibrary", "plugin", "recipe", "release", "schemalibrary"},
	summary: "Checks catalog publishing conventions over one entity or a whole Release.",
	description: "Run the publishing-discipline linter over a catalog entity, or validate a Release's resolved " +
		"dependency closure, driving the compiler's own lint operation and writing nothing. Two modes: pass `entity` (a catalog entity YAML " +
		"path) to run the publishing rules — naming shape, category-matches-directory, " +
		"annotation-prefix discipline, and the like — which report at warn severity over local content without " +
		"consulting any registry; or pass `release` (a Release YAML path) to validate every catalog entity the Release " +
		"reaches against its composed schemas. With `pre_publish` (entity mode only) the previous published version is " +
		"fetched and the proposed version bump is verified against the schema-evolution classification, halting on a " +
		"mismatch. `plugin` overrides the workspace-configured linter attachments with a filesystem path. The operation is " +
		"offline-first: a missing artifact fails loudly rather than reaching for a network. Every result carries the " +
		"findings as structured data beside the human summary — each finding's severity, the rule that produced it, its " +
		"message and the entity file it concerns, plus counts by severity, in the same field vocabulary `aggrega lint " +
		"--output json` emits. A finding carries a line and column only when the rule that produced it located the issue " +
		"that precisely; otherwise the file alone locates it. Use it before publishing an " +
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
	// The collector is threaded on every invocation, not only where a caller asked
	// for structured output: this surface has no text-only mode to preserve, so
	// there is no request shape under which the findings would be unwanted, and a
	// conditional would be a second fidelity for one capability to report in.
	collect := &lintops.FindingCollector{}
	req := lintRequest{
		entity:        entity,
		release:       release,
		prePublish:    a.PrePublish,
		plugin:        strings.TrimSpace(a.Plugin),
		workspaceRoot: strings.TrimSpace(a.WorkspaceRoot),
		collect:       collect,
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
		// A path this session may not name is answered with the refusal's own
		// sentence. The label below says the run reported findings, which is what
		// every other failure here means and is not what this one means: nothing
		// was linted, so there is nothing for a caller to go and fix in a document.
		var refused *pathRefusal
		if errors.As(err, &refused) {
			return errorResult(refused.text)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		// A refusal carries the findings too. The run that halted is the one whose
		// findings a caller most needs to read field by field, and reporting them as
		// prose here while reporting them structurally on success would be the same
		// capability answering at two fidelities depending on its verdict.
		return structuredErrorResult(
			explainToolError("Lint reported findings that halt publication.", fmt.Errorf("%s", detail), kind),
			lintStructuredResult(collect))
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
	return textResult(out, lintStructuredResult(collect))
}

// lintInProcess runs the lint operation in-process over the shared lint core,
// resolving the server's tool workspace exactly as the sibling tools do. The
// success summary is written to stdout and diagnostic findings to stderr, kept on
// separate streams so a clean pass still surfaces its advisories. The policy
// resolver passes the workspace value through unchanged — the tool exposes no
// allow/enforce overrides, so the effective policy is always the workspace value.
//
// The request's collector is handed to the core on both modes, so the findings
// this surface renders are the ones the run produced rather than a re-reading of
// the report it printed. Collection is additive: the two writer streams carry the
// same bytes whether a collector is threaded or not, which is what lets the
// adapter's stream-level parity with a direct core call keep holding.
func (s *server) lintInProcess(ctx context.Context, req lintRequest, stdout, stderr io.Writer) error {
	tw, err := s.resolveToolWorkspace(req.workspaceRoot, nil)
	if err != nil {
		return err
	}
	if req.release != "" {
		releasePath, refusal := tw.workspacePath("lint", "release", req.release)
		if refusal != "" {
			return &pathRefusal{text: refusal}
		}
		return lintops.RunRelease(ctx, tw.rw, releasePath, nil, stdout, stderr, req.collect)
	}
	pluginPath := req.plugin
	if pluginPath != "" {
		resolved, refusal := tw.workspacePath("lint", "plugin", pluginPath)
		if refusal != "" {
			return &pathRefusal{text: refusal}
		}
		pluginPath = resolved
	}
	entityPath, refusal := tw.workspacePath("lint", "entity", req.entity)
	if refusal != "" {
		return &pathRefusal{text: refusal}
	}
	return lintops.RunEntity(ctx, lintops.Config{
		EntityPath:     entityPath,
		PrePublish:     req.prePublish,
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      tw.rw,
		Out:            stdout,
		DiagOut:        stderr,
		Collect:        req.collect,
	})
}
