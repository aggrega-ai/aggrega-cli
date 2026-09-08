// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// buildArgs is the build tool's argument shape.
type buildArgs struct {
	Release        string   `json:"release"`
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	ResponseFormat string   `json:"response_format"`
}

// buildFile is one rendered artifact in the build result: its output-relative
// path and, in the detailed form, its rendered content.
type buildFile struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

// buildResultPayload is the build tool's structured payload. The render happens
// entirely in memory: Files are the artifacts the pipeline produced, and
// nothing is written to disk.
//
// # It is returned on a refusal too
//
// A build that refused answers in this same shape, with the failure flagged on
// the result envelope rather than by a field here. Whatever findings the run
// held when it stopped are populated exactly as they would be on a success,
// because a refusal is not a reason to drop to a lower fidelity and the caller
// that has to act programmatically is the one holding the refusal. That is why
// the fields below are filled before the verdict is read rather than after it.
//
// What a refusal HOLDS is narrower than what a success holds, and the difference
// belongs to the producers rather than to this shape. The recorded deprecation
// markers and the declined external references accumulate alongside the render
// rather than inside its result, so they survive the verdict either way. The
// advisories survive it too: a run that stopped hands back the findings the
// phases ahead of the halt raised, so a caller acting on a refusal receives the
// same entries a command-line build printed before it stopped. A refusal reports
// none of them only where the run stopped before any phase had raised one — a
// call that never reached the compiler, or one that stopped at closure
// resolution — and that is a fact about how far the run got rather than about
// what this shape carries. A caller that wants the findings for a Release
// without rendering it reads them from the validate tool instead.
//
// Files reports nothing on a refusal, and its key is NOT a signal a caller may
// branch on: the field carries no omit option, so it is encoded whatever the
// outcome, and an empty list therefore cannot distinguish a build that refused
// from one that ran and emitted nothing. What distinguishes them is the failure
// flag on the envelope, which is the protocol's own discriminator and not
// something this shape restates. Read this field for what was written, and read
// the flag for whether anything was going to be.
type buildResultPayload struct {
	Release string      `json:"release"`
	Files   []buildFile `json:"files"`
	// Deprecations names each pinned reference carrying a recorded deprecation
	// marker, from the shared report every surface reads. It is present whatever
	// the outcome, for the same reason the command line prints the notices on a
	// build that succeeded: reporting is unconditional and only halting is
	// configured, so raising the halt threshold never reduces what the caller is
	// told.
	Deprecations []string `json:"deprecations,omitempty"`

	// DeclinedExternalInputs names each external reference the render asked for
	// that the lockfile does not pin. Such a reference is refused rather than
	// fetched, so the emitted files stop short of the artifacts the render would
	// otherwise produce, and a command-line build meeting the same reference halts
	// on it. Naming them is what lets a caller tell an incomplete render from an
	// accurate one; without it the difference is invisible in the returned files.
	DeclinedExternalInputs []string `json:"declined_external_inputs,omitempty"`

	// Advisories carries every exit-code-neutral finding the build raised, in the
	// order it raised them — the same findings, in the same order, a command-line
	// build prints. None of them changes the outcome: a build carrying advisories
	// and nothing else still succeeded, and the files above are the files it
	// produced.
	//
	// They are reported for the same reason the deprecation notices are: a caller
	// with no terminal has no other way to receive them, and a finding nobody is
	// told about is a finding that was not made. None of them is ever the reason a
	// build refused — what refused travels in the guidance text instead — and a
	// refusal carries whichever of them the run had raised before it stopped, for
	// the reason given on this type.
	Advisories []buildAdvisory `json:"advisories,omitempty"`
}

// buildAdvisory is one exit-code-neutral finding a build raised.
//
// Severity distinguishes a finding that wants attention from one that merely
// records an observation; neither affects the build's outcome. Code is a stable
// string a caller can match on, present when the finding's class carries one.
// KeyPath is the path to the offending key in the authored document, present when
// the finding names one — a finding about the shape of the deployed topology
// rather than about a line of authoring names no key, and its message says what it
// concerns instead.
type buildAdvisory struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
	KeyPath  string `json:"key_path,omitempty"`
}

// buildAdvisories maps the advisories a run raised onto the tool's wire shape,
// preserving their order.
//
// The message is rendered through the compiler's own advisory formatter — the one
// formatter every surface that displays this channel reads — so an advisory reads
// identically here, in an editor, and on a terminal.
func buildAdvisories(advisories []renderer.Diagnostic) []buildAdvisory {
	if len(advisories) == 0 {
		return nil
	}
	out := make([]buildAdvisory, 0, len(advisories))
	for _, d := range advisories {
		entry := buildAdvisory{
			Severity: d.Severity,
			Message:  renderer.RenderDiagnosticText(d),
			Code:     d.Code,
		}
		if d.Location != nil {
			entry.KeyPath = d.Location.KeyPath
		}
		out = append(out, entry)
	}
	return out
}

var buildToolSpec = toolSpec{
	kinds: []string{"release"},
	description: "Compile a Release into its rendered deployment artifacts entirely in memory and return them — a " +
		"preview render that writes nothing to disk. Pass the Release path as `release` (absolute, or relative to the " +
		"workspace). The tool runs the full compiler pipeline through the provider render step and returns the produced " +
		"files by output-relative path. Use it to see exactly what a Release would emit before committing to a real build, " +
		"or to inspect one component's rendered output while authoring. In the detailed response format each file's rendered " +
		"content is included; the concise format returns just the file paths. A render failure comes back as actionable " +
		"guidance (route it through explain-error if needed) rather than a partial write. Optional `workspace_root` and " +
		"`catalog_dirs` override workspace and catalog discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the Release YAML to build, absolute or relative to the workspace root."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "detailed returns each rendered file's content; concise returns only the file paths. Defaults to detailed."
    }
  },
  "required": ["release"]
}`,
	invoke: buildInvoke,
}

func buildInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a buildArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("build", err)
		}
	}
	if strings.TrimSpace(a.Release) == "" {
		return errorResult("build requires a 'release' path")
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the build.", err, "release"))
	}
	releasePath := tw.absWorkspacePath(a.Release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	// The render-time external-input seam, frozen — the same posture and the same
	// builder the editor's inspection renders through. It is FROZEN rather than
	// acquiring because this call is an in-memory render: a seam with acquisition
	// on would let a tool call reach the network and write into the
	// content-addressed store as a side effect of what the caller asked to be a
	// preview that writes nothing.
	//
	// A pinned reference is therefore served from the store — which is what makes
	// these bytes the bytes a build emits — and an unpinned one is declined and
	// named in the answer rather than quietly rendering a not-available envelope
	// the caller cannot tell from real content.
	fetchSeam := compilerops.FrozenExternalInputs(tw.cacheDir, lf)

	// This tool renders from the pins the workspace already carries. It neither
	// requires a lockfile nor fills one — the two other artifact-producing
	// surfaces do one or the other before they render — so it runs the operation
	// shaped for that contract: every gate that judges the closure or the entities
	// is on, and the drift verify, whose question presupposes a lockfile the
	// operation asked for, is not this operation's to ask.
	preview, err := compilerops.PreviewForArtifactFromAvailablePins(
		ctx,
		compilerops.NewPreviewInputs(
			fetchSeam,
			view,
			releasePath,
			tw.root,
			tw.cfg,
			s.serverVersion,
			compilerops.WithLockfile(lf, lf != nil),
		),
		pipeline.Options{},
	)

	// Every recorded marker is reported whatever the outcome, so choosing a
	// stricter threshold never costs the caller a finding.
	deprecationNotices := previewDeprecationNotices(preview)

	// The findings are assembled BEFORE the verdict is read, and that ordering is
	// the whole point rather than a tidying. Assembled after a success check, every
	// field below would be populated only on the path where the caller needs it
	// least: a run that finished cleanly. The run that refused is the one whose
	// findings a caller most needs to read field by field, because it has to decide
	// what to change, and reporting them as prose there while reporting them
	// structurally on success would be one capability answering at two fidelities
	// depending on its verdict.
	payload := buildResultPayload{Release: releasePath}
	if len(deprecationNotices) > 0 {
		payload.Deprecations = deprecationNotices
	}
	// A declined reference is carried in the answer whether or not the render
	// survived it. The decline is owed because a reference was declined, not
	// because the run happened to end badly: a guest that renders around the
	// refusal produces output that stops short of what a build would emit, and a
	// caller cannot tell that from an accurate render unless it is told. A decline
	// also HALTS the render in the ordinary case, so the refusal is where this field
	// earns its keep and the success is the incidental path for it.
	payload.DeclinedExternalInputs = fetchSeam.Declined()
	// The advisories the build raised travel in the answer, so a caller driving the
	// compiler through this surface receives every finding a command-line build
	// prints rather than only the ones that stopped it. Read through the accessor
	// that answers on either verdict — a run that halted raised whatever the phases
	// ahead of it found, and those are exactly the findings a caller has to act on
	// to get past the halt.
	payload.Advisories = buildAdvisories(preview.PipelineOutcome().Advisories())

	if err != nil {
		return structuredErrorResult(
			explainToolError(previewFailureSummary(preview, deprecationNotices), err, previewFailureKind(preview, err)),
			payload)
	}

	// The rendered set comes off the finished payload: the refusal above returned,
	// so the run whose files this reports is one that ended.
	detail := parseResponseFormat(a.ResponseFormat) == responseFormatDetailed
	payload.Files = renderedFiles(preview.PipelineOutcome().Completed(), detail)
	text := renderBuildText(payload)
	return textResult(text, payload)
}

// previewDeprecationNotices pulls the recorded-marker notices out of a preview's
// warnings. They are reported whatever the outcome — on a success they ride in
// the payload, and on a refusal they are quoted in the message — because
// reporting is unconditional and only halting is configured, so raising the halt
// threshold never reduces what the caller is told.
func previewDeprecationNotices(preview *compilerops.PreviewResult) []string {
	if preview == nil {
		return nil
	}
	notices := make([]string, 0, len(preview.Warnings))
	for _, w := range preview.Warnings {
		if w.Class == compilerops.WarningDeprecation {
			notices = append(notices, w.Text)
		}
	}
	return notices
}

// previewFailureSummary is the caller-facing sentence for a refused build,
// chosen by the gate that refused rather than by matching the failure's text.
// Each one names what the build did and what the reader is looking at, because
// an agent consuming this has only the sentence to route on.
func previewFailureSummary(preview *compilerops.PreviewResult, deprecationNotices []string) string {
	gate := compilerops.GateNone
	if preview != nil {
		gate = preview.FailedGate
	}
	switch gate {
	case compilerops.GateManifestConsistency:
		return "The build refused: the Manifest maps a type onto a recipe that does not implement it."
	case compilerops.GateDeprecation:
		return "The build refused: this Release pins a deprecated artifact at or above the configured halt severity. " +
			"Every recorded marker in this Release:\n  " + strings.Join(deprecationNotices, "\n  ")
	case compilerops.GatePinnedIntegrity:
		return "The build refused: a pinned entry in the cache no longer matches the digest the lockfile records for it. " +
			"Re-pull the Release to refill the cache from the registry."
	case compilerops.GateLockfileDrift:
		return "The build refused: the lockfile no longer matches the dependencies this Release resolves to."
	case compilerops.GateClosurePillars:
		return "The build refused: an entity this Release reaches does not satisfy its schema."
	case compilerops.GateExtends:
		return "The build refused: an entity this Release reaches inherits from a parent that could not be resolved."
	case compilerops.GateAxisReconstruction:
		return "The build could not reconstruct the Release's pinned dependency set from the lockfile."
	case compilerops.GateRender:
		return "The build failed during rendering; nothing was written to disk."
	default:
		return "The build could not resolve the Release closure."
	}
}

// previewFailureKind names the document class a refusal is about, so the caller
// is pointed at the file it has to edit. The closure-resolution case keeps the
// per-stage attribution the resolution itself produces, which is finer than
// anything this switch could reconstruct.
func previewFailureKind(preview *compilerops.PreviewResult, err error) string {
	gate := compilerops.GateNone
	if preview != nil {
		gate = preview.FailedGate
	}
	switch gate {
	case compilerops.GateManifestConsistency:
		return "manifest"
	case compilerops.GateClosureResolution:
		var cerr *compilerops.ClosureError
		if errors.As(err, &cerr) {
			return closureStageKind(cerr.Stage)
		}
		return "release"
	default:
		return "release"
	}
}

// renderedFiles converts the pipeline's rendered artifact map into the ordered
// file list the build result carries. The paths are sorted so the result is
// deterministic across runs regardless of the map's iteration order; content is
// included only in the detailed form.
func renderedFiles(result *pipeline.Result, includeContent bool) []buildFile {
	paths := make([]string, 0, len(result.Artifacts))
	for p := range result.Artifacts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	files := make([]buildFile, 0, len(paths))
	for _, p := range paths {
		f := buildFile{Path: p}
		if includeContent {
			f.Content = string(result.Artifacts[p])
		}
		files = append(files, f)
	}
	return files
}

// renderBuildText renders the human-readable build summary: the file count and
// each rendered path, one per line, followed by any external reference the
// render had to decline.
//
// The declined references are spelled out rather than counted, and each carries
// the remedy, because the caller has to know WHICH reference is missing to
// supply it. Their absence prints nothing, so a fully-pinned render reads exactly
// as it did before.
//
// The advisories print for the same reason and in the same shape: a caller
// reading the text form rather than the structured one must not receive fewer
// findings for that choice. They are listed in the order the build raised them,
// which is the order a command-line build prints them, and their absence prints
// nothing.
func renderBuildText(p buildResultPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Build of %s rendered %d file(s) in memory (nothing written to disk):\n", p.Release, len(p.Files))
	for _, f := range p.Files {
		fmt.Fprintf(&b, "  %s\n", f.Path)
	}
	if len(p.Advisories) > 0 {
		fmt.Fprintf(&b, "\nThe build raised %d advisory finding(s). None of them changed the outcome:\n", len(p.Advisories))
		for _, a := range p.Advisories {
			fmt.Fprintf(&b, "  %s: %s\n", a.Severity, a.Message)
		}
	}
	if len(p.DeclinedExternalInputs) > 0 {
		fmt.Fprintf(&b, "\nThis render declined %d external reference(s) the lockfile does not pin. "+
			"The files above stop short of what a build would emit; run `aggrega pull --release %s` to pin them:\n",
			len(p.DeclinedExternalInputs), p.Release)
		for _, url := range p.DeclinedExternalInputs {
			fmt.Fprintf(&b, "  %s\n", url)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
