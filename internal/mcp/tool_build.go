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
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// buildArgs is the build tool's argument shape.
type buildArgs struct {
	Release        string   `json:"release"`
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	ResponseFormat string   `json:"response_format"`

	// AutoPull resolves the Release's dependencies into the lockfile before the
	// render, as this build's first step: the same resolution the pull tool
	// performs, so a workspace filled by a build and one filled by that call are
	// filled identically. It reaches a registry and writes the lockfile, which
	// nothing else about this tool does.
	//
	// It ranks exactly as the same choice ranks on every other surface carrying a
	// build, because all of them read one composition: this argument and the
	// workspace's own auto_pull setting are additive, and either turns the fill on.
	// So absent — the zero value, which is what an argument nobody sent decodes to —
	// the render is held to the lockfile as it stands, UNLESS the workspace is
	// configured to fill, where it fills as it would for any other build there.
	// What this argument cannot do is turn off a fill the workspace asked for.
	//
	// Resolving is where an external input is acquired. The resolution renders this
	// Release to discover which remote references its render consumes, fetches
	// them, and records them in the same lockfile write that carries the dependency
	// pins — so a reference this Release needs is pinned after a fill and, without
	// one, is declined by the render and named in the answer.
	//
	// A build that fills renders this Release exactly once. That discovery render
	// is a render of this Release under the posture a build renders in, so the
	// build is answered from it rather than performing a second one.
	AutoPull bool `json:"auto_pull"`
}

// buildFile is one rendered artifact in the build result: its output-relative
// path and, in the detailed form, its rendered content.
type buildFile struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

// buildResultPayload is the build tool's structured payload. The render happens
// entirely in memory: Files are the artifacts the pipeline produced, and no
// artifact is written to disk.
//
// The render is the whole of what writes nothing. A build asked to resolve its
// dependencies first performs that resolution before rendering, and that step
// writes the lockfile and fills the content-addressed store — so "in memory"
// describes where the ARTIFACTS go, which is what a caller reading this shape is
// choosing between, and never a claim that the call as a whole left the
// workspace untouched. DependencyResolution below is present exactly when it
// did.
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

	// DependencyResolution is what this build's own fill step resolved, in the
	// shape the pull tool answers with, and it is absent on a build that did not
	// fill. A caller that asked for the fill asked for a resolution as part of this
	// call, so the facts that resolution computed are owed here rather than only on
	// the call that does nothing else — which the caller would otherwise have to
	// send a second time to find out what its own build just pinned. Its
	// diagnostics field carries the resolution's own account of itself line by
	// line, which is where a caller with no terminal reads what the fill did.
	//
	// It is the pull tool's shape rather than one of this tool's own because it
	// reports the same operation: a caller that has read one resolution here can
	// read every other, and two shapes would be two spellings of one fact.
	DependencyResolution *pullResultPayload `json:"dependency_resolution,omitempty"`
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
	description: "Compile a Release into its rendered deployment artifacts entirely in memory and return them — the " +
		"render writes no artifact to disk. Pass the Release path as `release` (absolute, or relative to the " +
		"workspace). The tool runs the full compiler pipeline through the provider render step and returns the produced " +
		"files by output-relative path. Use it to see exactly what a Release would emit before committing to a real build, " +
		"or to inspect one component's rendered output while authoring. In the detailed response format each file's rendered " +
		"content is included; the concise format returns just the file paths. A render failure comes back as actionable " +
		"guidance (route it through explain-error if needed) rather than a partial write. `auto_pull` resolves the " +
		"Release's dependencies into the lockfile first, which reaches a registry and writes.",
	workspaceSelectionNote: "Optional `workspace_root` and `catalog_dirs` override workspace and catalog discovery.",
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
    },
    "auto_pull": {
      "type": "boolean",
      "description": "Resolve the Release's dependencies into the lockfile before rendering. Reaches a registry and writes the lockfile; the render still writes no artifact. Defaults to false. The workspace auto_pull setting turns the same step on for every build, and this adds to it rather than overriding it."
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
	releasePath, refusal := tw.workspacePath("build", "release", a.Release)
	if refusal != "" {
		return errorResult(refusal)
	}

	// The fill, before anything reads the lockfile, because what it writes is what
	// the render then resolves against. Whether it runs is composed in the workspace
	// config, which owns one of the two inputs — the argument turns it on for this
	// call, the workspace's auto_pull setting turns it on for every build there, and
	// either is enough. Reading the composition from that one home rather than
	// branching on the argument is what keeps this surface ranking the two exactly as
	// the command line and the daemon rank them.
	//
	// The resolution is the pull tool's own, run with no upgrade targets and no
	// verify, so it reconciles: recorded pins are honoured and only unpinned
	// references are resolved fresh. A build that resolved some other way would be
	// free to fill a workspace differently from the tool a caller uses to fill it.
	var fillReport *pullResultPayload
	// The render the fill conserved, where it conserved one. Nil is not an error
	// signal and not a request: it is the absence of a render, and the step below
	// performs one for itself when it arrives that way.
	var filled *compilerops.PreviewResult
	lockfileFilled := false
	if tw.cfg.FillsLockfile(a.AutoPull) {
		report, render, fillErr := runDependencyResolution(ctx, s, tw, releasePath,
			pullArgs{Release: a.Release, WorkspaceRoot: a.WorkspaceRoot, CatalogDirs: a.CatalogDirs}, nil)
		if fillErr != nil {
			// The fill refused, so nothing was rendered and there is no render to
			// report on. What the caller is handed is the resolution's own payload,
			// which carries the diagnostics that say where it stopped — the same
			// detail the pull tool hands back for the same refusal.
			return structuredErrorResult(
				explainToolError("The build refused before rendering: it was asked to resolve this Release's "+
					"dependencies first, and the resolution did not complete. Nothing was rendered.",
					fillErr, "release"),
				buildResultPayload{Release: releasePath, Files: []buildFile{}, DependencyResolution: &report})
		}
		fillReport = &report
		filled = render
		// Recorded as a fact about what just happened rather than read back off the
		// argument, because what the render needs to know is that a lockfile WAS
		// written and not that one was asked for. The two part on the refusal above,
		// and a render told the wrong one would stand down a drift verify that had
		// something to check.
		//
		// It is recorded separately from the render above and not derived from it,
		// because a fill whose discovery render stopped part way wrote its lockfile
		// and conserved no render: this is true there and the render is nil.
		lockfileFilled = true
	}

	// The render, or the render that already happened.
	//
	// A fill renders this Release to find out which external inputs it consumes, so
	// a build whose fill ran already holds a render of what it was about to render —
	// performed under this same artifact posture, through this same computation,
	// against the closure that fill pinned. It presents that one, and renders only
	// where there was none to inherit.
	//
	// The PRESENCE of that render is the decision. It is a fact about what this
	// call did, not a setting: there is deliberately nothing here asking whether to
	// reuse it, because a switch of that shape would be a second answer to a
	// question the render itself already answers by existing.
	//
	// declined is the references this tool's OWN render was refused, and it stays
	// empty on the presenting path for the same reason it is a fact rather than a
	// setting: a call that rendered nothing here refused nothing here, and the fill
	// that did render acquired rather than declined.
	// err is the workspace resolution's, already returned on above and therefore
	// nil here; the render assigns into it rather than shadowing it, so the single
	// failure branch below reads one variable whichever path produced it.
	preview := filled
	var declined []string
	// The view this call rendered through, held so the failure branch below can
	// read the catalog files its load refused. It stays nil on the presenting
	// path, where the render happened inside the fill and this call's own failure
	// branch is unreachable.
	var rendered *catalog.LazyLookup
	if preview == nil {
		lf := loadReleaseLockfile(releasePath)
		view := tw.newView(lf)
		rendered = view

		// The render-time external-input seam — the same posture and the same builder
		// the editor's inspection and the command-line build render through. It is the
		// PINNED posture rather than the acquiring one because the render emits bytes
		// rather than acquiring content: a seam with acquisition on would widen the
		// closure as a side effect of rendering it, so what a caller got back would
		// depend on what the render happened to reach for. Repairing a missing store
		// blob by a digest the lockfile already records is not that widening, and it is
		// what keeps these bytes the bytes a build emits rather than a decline a build
		// would not have raised.
		//
		// Acquisition belongs to the fill, which is why this render owes none of it. A
		// fill renders with acquisition ON precisely to discover which remote
		// references this Release consumes, fetches them, and pins them in the lockfile
		// it writes — so what a fill changes is which references are pinned by the time
		// a render runs, never what a render may reach. A reference no fill pinned is
		// declined here exactly as it is on a call that never filled.
		//
		// A pinned reference is therefore served from the store, and an unpinned one is
		// declined and named in the answer rather than quietly rendering a
		// not-available envelope the caller cannot tell from real content.
		//
		// Serving a pinned reference from the store is HALF of what makes these bytes
		// the bytes a build emits. The other half is that the pins served from still
		// describe the closure the Release resolves to, which the operation below
		// verifies: reading pinned bytes out of the store says nothing about whether
		// they were the right bytes to read.
		fetchSeam := compilerops.PinnedExternalInputs(tw.root, tw.cacheDir, tw.cfg, lf)

		// This tool renders from the pins the workspace carries by the time it gets
		// here — the ones already on disk, or the ones a fill above wrote before its
		// own render stopped. It runs the strict operation either way, drift verify
		// included, because what it publishes is the claim that these are the bytes a
		// build emits: a build refuses a lockfile that has drifted, so rendering from
		// one here would publish that claim exactly where it does not hold.
		//
		// The drift verify stands down on a lockfile this call filled, and that is not a
		// relaxation. A resolution and the closure it was derived from cannot disagree,
		// so comparing them would hold a set against itself; the verify is there to
		// catch a lockfile that went stale between one run and another, which a file
		// written moments ago by this run cannot have done.
		//
		// The absent case pays nothing for that either, and it needs no condition of its
		// own: the drift verify reads the lockfile from the inputs and stands down when
		// there is none, so a Release carrying no lockfile and no fill renders as it
		// always did. What is refused is the single input on which this surface would
		// otherwise assert something false — a lockfile that exists, was not written by
		// this call, and no longer matches.
		preview, err = compilerops.PreviewForArtifact(
			ctx,
			compilerops.NewPreviewInputs(
				fetchSeam,
				view,
				releasePath,
				tw.root,
				tw.cfg,
				s.serverVersion,
				compilerops.WithLockfile(lf, lf != nil && !lockfileFilled),
			),
			pipeline.Options{},
		)
		declined = fetchSeam.Declined()
	}

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
	// What the fill resolved travels on every outcome for the same reason the
	// findings below do: a caller that asked for a fill is owed what the fill did
	// whether or not the render that followed it survived, and a build that refused
	// AFTER writing a lockfile is exactly the case where the caller most needs to
	// know that the write happened.
	payload.DependencyResolution = fillReport
	// A declined reference is carried in the answer whether or not the render
	// survived it. The decline is owed because a reference was declined, not
	// because the run happened to end badly: a guest that renders around the
	// refusal produces output that stops short of what a build would emit, and a
	// caller cannot tell that from an accurate render unless it is told. A decline
	// also HALTS the render in the ordinary case, so the refusal is where this field
	// earns its keep and the success is the incidental path for it.
	payload.DeclinedExternalInputs = declined
	// The advisories the build raised travel in the answer, so a caller driving the
	// compiler through this surface receives every finding a command-line build
	// prints rather than only the ones that stopped it. Read through the accessor
	// that answers on either verdict — a run that halted raised whatever the phases
	// ahead of it found, and those are exactly the findings a caller has to act on
	// to get past the halt.
	payload.Advisories = buildAdvisories(preview.PipelineOutcome().Advisories())

	if err != nil {
		// A catalog file the load refused never enters the catalog, so a Release
		// reaching it fails on the REFERENCE — a true statement about a document
		// that is correct. Replaying the refusals the view recorded is what turns
		// that into the constraint the author has to fix, and it is the annotation
		// the command line has always applied, through the one home both read.
		annotated := annotateResolutionWithRefusals(err, rendered)
		return structuredErrorResult(
			explainToolError(previewFailureSummary(preview, deprecationNotices), annotated,
				refusalKindOr(annotated, previewFailureKind(preview, err))),
			payload)
	}

	// The rendered set comes off the finished payload. A render performed here has
	// already returned on the refusal above, so a payload missing at this point
	// means a presented render did not finish — the one case where the verdict
	// comes from outside this call, and only as good as the promise the fill made
	// about it. Reporting a partial set would name an artifact list stopping short
	// of the nodes the render never reached, which reads exactly like a complete
	// one, so it is refused instead. The fill declines to hand over an unfinished
	// render, so this is the guard behind that promise rather than the only thing
	// keeping it.
	completed := preview.PipelineOutcome().Completed()
	if completed == nil {
		return structuredErrorResult(
			"The build resolved this Release's dependencies first, and the render that resolution "+
				"performed did not finish — so there is no complete set of files to report and none is "+
				"reported. Call pull with this same 'release' path to see where the resolution stopped, "+
				"then build again.",
			payload)
	}
	detail := parseResponseFormat(a.ResponseFormat) == responseFormatDetailed
	payload.Files = renderedFiles(completed, detail)
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
//
// The drift arm additionally names how to CLEAR the refusal from here, and that
// split is deliberate rather than a duplication: the condition is single-sourced
// in the shared preview and reaches every surface identically, while the
// invocation that clears it is per-surface. A caller here receives both, since
// the shared diagnostic travels in the same guidance and names the command line
// — so the sentence says the two are one operation at two invocation sites,
// rather than leaving an agent to choose between what look like rival
// instructions.
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
		return "The build refused: the lockfile no longer matches the dependencies this Release resolves to, " +
			"so it would have rendered from pins a build would not have used. Resolving the dependencies " +
			"again clears it. Through this protocol that is the pull tool, called with this same 'release' " +
			"path and no 'upgrade_targets': it reconciles the lockfile against what the Release resolves to " +
			"today, which is what this condition needs. The command line the diagnostic names is that same " +
			"operation at a terminal, asked additionally to re-resolve every pin to the highest available " +
			"version — more than this refusal requires, and it clears it too."
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
// That remedy names both invocation sites — the pull tool on this protocol, and
// the same operation at a terminal — because a caller reaching this surface can
// invoke the first without leaving the protocol, so naming only the command line
// would point it somewhere it cannot go. The two are one operation and the
// sentence says so, rather than leaving an agent to choose between what look like
// rival instructions.
//
// The advisories print for the same reason and in the same shape: a caller
// reading the text form rather than the structured one must not receive fewer
// findings for that choice. They are listed in the order the build raised them,
// which is the order a command-line build prints them, and their absence prints
// nothing.
func renderBuildText(p buildResultPayload) string {
	var b strings.Builder
	if p.DependencyResolution != nil {
		fmt.Fprintf(&b, "Resolved this Release's dependencies into %s first, then rendered.\n",
			p.DependencyResolution.Lockfile)
		for _, line := range p.DependencyResolution.Diagnostics {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Build of %s rendered %d file(s) in memory (no artifact written to disk):\n", p.Release, len(p.Files))
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
			"The files above stop short of what a build would emit; run `aggrega pull --release %s` to pin them. "+
			"Through this protocol that is the pull tool, called with this same 'release' path — one operation "+
			"at two invocation sites:\n",
			len(p.DeclinedExternalInputs), p.Release)
		for _, url := range p.DeclinedExternalInputs {
			fmt.Fprintf(&b, "  %s\n", url)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
