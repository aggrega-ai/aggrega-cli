// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// The payload is the lint core's own report, in the shape the command line's
// structured output already emits, so a finding's severity, rule identity,
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
//
// The report is told the way every answer here is: each finding's file is a
// location on this host, and a finding's message and remedy are prose a rule
// composed, so both pass through what the caller may be told of them. The
// report's fields are read here, in one body, so every fact the lint core
// produced is visibly carried onto the wire.
func lintStructuredResult(tw *toolWorkspace, collect *lintops.FindingCollector) any {
	if !collect.Reached() {
		return nil
	}
	report := collect.Report()
	out := lintReportPayload{Findings: make([]lintFindingPayload, 0, len(report.Findings)), Summary: report.Summary}
	for _, f := range report.Findings {
		entry := lintFindingPayload{Finding: f}
		entry.Message = tw.reportedText(f.Message)
		entry.Suggest = tw.reportedText(f.Suggest)
		if f.Location != nil {
			at := &lintLocationPayload{FindingLocation: *f.Location}
			at.File, at.FileWithheld = tw.reportedLocationField(f.Location.File)
			entry.Location = at
		}
		out.Findings = append(out.Findings, entry)
	}
	return out
}

// lintReportPayload is the lint core's report as this surface answers with it.
//
// It carries the report's own findings and summary under the report's own keys,
// in the report's own order, and differs from it in one place: a finding's file
// is told as the caller may be told it, so one the caller is not told is absent
// and marked by the location's file_withheld, which is omitted where nothing was
// kept back. Over the process's own streams nothing is kept back, so what is
// encoded is the report's own bytes.
//
// The finding and its location are embedded rather than restated, and the one
// field this surface spells differently is declared ahead of the embedded
// location, so it takes that field's place in the encoding: the vocabulary is the
// lint core's, which is the one home a finding's field names have.
type lintReportPayload struct {
	Findings []lintFindingPayload  `json:"findings"`
	Summary  lintops.ReportSummary `json:"summary"`
}

// lintFindingPayload is one finding, with its location as this surface tells it.
type lintFindingPayload struct {
	lintops.Finding
	Location *lintLocationPayload `json:"location,omitempty"`
}

// lintLocationPayload is where a finding points, with its file as this surface
// tells it.
type lintLocationPayload struct {
	File         *string `json:"file"`
	FileWithheld bool    `json:"file_withheld,omitempty"`
	lintops.FindingLocation
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
		"short code (the code printed at the head of its advisory row, which `aggrega explain` expands), its message and " +
		"the entity file it concerns, plus counts by severity, in the same field vocabulary `aggrega lint --output json` " +
		"emits. A finding from a rule Aggrega does not declare, one a linter of your own adds, carries no short code, as " +
		"its row prints none. A finding carries a line and column only when the rule that produced it located the issue " +
		"that precisely; otherwise the file alone locates it. Use it before publishing an " +
		"entity, or to check a whole Release's dependencies in one pass.",
	listenerNote: "You did not start this server, so a finding's file is named relative to the workspace root, " +
		"and one outside the workspace carries no file and is marked `file_withheld`; a place on the server's " +
		"host named inside a message is named the same way, or reads " + keptBackLocationWords + ".",
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
      "description": "Optional workspace root override. ` + workspaceRootArgumentRule + `"
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
	err := run(ctx, req, &stdout, &stderr)
	// The run resolves its own workspace behind the runner seam, so the answer is
	// read against the served one — which, over a listener, is the only one a call
	// can reach — and nothing is resolved over the process's own streams.
	tw := s.servedWorkspace()
	if err != nil {
		// A path this session may not name, and a workspace that could not be
		// resolved, are answered with the refusal's own sentence. The label below
		// says the run reported findings, which is what every other failure here
		// means and is not what these mean: nothing was linted, so there is nothing
		// for a caller to go and fix in a document.
		var refused *pathRefusal
		if errors.As(err, &refused) {
			return tw.answer(errorResult(refused.text))
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		// A refusal carries the findings too. The run that halted is the one whose
		// findings a caller most needs to read field by field, and reporting them as
		// prose here while reporting them structurally on success would be the same
		// capability answering at two fidelities depending on its verdict.
		return tw.answer(structuredErrorResult(
			explainToolError("Lint reported findings that halt publication.", fmt.Errorf("%s", detail), kind),
			lintStructuredResult(tw, collect)))
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
	return tw.answer(textResult(out, lintStructuredResult(tw, collect)))
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
		// Nothing was linted, so the failure is carried out as a refusal and
		// answered in its own words rather than under the label for findings.
		return &pathRefusal{text: explainToolError("Could not resolve the workspace to lint in.", err, "")}
	}
	if req.release != "" {
		releasePath, refusal := tw.workspacePath("lint", "release", req.release)
		if refusal != "" {
			return &pathRefusal{text: refusal}
		}
		if tw, err = s.subjectWorkspace(tw, req.release); err != nil {
			return &pathRefusal{text: explainToolError("Could not resolve the workspace to lint in.", err, "")}
		}
		return lintops.RunRelease(ctx, tw.rw, releasePath, nil, nil, stdout, stderr, req.collect)
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
	if tw, err = s.subjectWorkspace(tw, req.entity); err != nil {
		return &pathRefusal{text: explainToolError("Could not resolve the workspace to lint in.", err, "")}
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
