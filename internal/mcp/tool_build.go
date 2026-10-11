// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// buildArgs is the build tool's argument shape.
type buildArgs struct {
	Release       string   `json:"release"`
	WorkspaceRoot string   `json:"workspace_root"`
	CatalogDirs   []string `json:"catalog_dirs"`

	// ResponseFormat is a pointer so that a value a caller sent is
	// distinguishable from one it left out. Every value outside the declared set
	// is refused, the blank string among them, and only an absent argument takes
	// the default — so a plain string would make a refused value and an absent one
	// the same bytes at exactly the point the two have to be told apart.
	ResponseFormat *string `json:"response_format"`

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
// writes the lockfile and fills the artifact cache — so "in memory"
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
// call refused before the Release resolved, or one that stopped while resolving
// it — and that is a fact about how far the run got rather than about what this
// shape carries. The readings over a Release's composed extends chain are raised
// the moment it resolves, ahead of every gate that can refuse it, so every later
// refusal carries them. A caller that wants the findings for a Release without
// rendering it reads them from the validate tool instead.
//
// Files reports nothing on a refusal, and its key is NOT a signal a caller may
// branch on: the field carries no omit option, so it is encoded whatever the
// outcome, and an empty list therefore cannot distinguish a build that refused
// from one that ran and emitted nothing. What distinguishes them is the failure
// flag on the envelope, which is the protocol's own discriminator and not
// something this shape restates. Read this field for what was written, and read
// the flag for whether anything was going to be.
//
// Release is the Release's location on this host, spelled as the caller may be
// told it; one the caller is not told is absent and marked by ReleaseWithheld,
// which is omitted where nothing was kept back.
type buildResultPayload struct {
	Release         *string     `json:"release"`
	ReleaseWithheld bool        `json:"release_withheld,omitempty"`
	Files           []buildFile `json:"files"`
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

	// Advisories carries every exit-code-neutral finding the build raised — the
	// same findings, in the same order, a command-line build prints. That order is
	// the terminal's: first a line for each catalog directory the workspace
	// configuration lists for the Release where nothing exists, which the build
	// reads as holding nothing, on a build that does not fill its lockfile (one
	// that fills carries that line among the resolution's diagnostics instead);
	// then the render's own advisories, in the order the render raised them; then
	// the findings the shared preview raised about the Release and what it
	// reaches, in the order it raised them. A terminal prints the last group once
	// the render has returned, although the preview raised it earlier, so this
	// list follows the display rather than the moment of raising.
	// None of them changes the outcome: a build carrying advisories and nothing
	// else still succeeded, and the files above are the files it produced.
	//
	// They are reported for the same reason the deprecation notices are: a caller
	// with no terminal has no other way to receive them, and a finding nobody is
	// told about is a finding that was not made. None of them is ever the reason a
	// build refused — what refused travels in the guidance text, and in Refusal
	// where the render itself refused — and a refusal carries whichever of them the
	// run had raised before it stopped, for the reason given on this type.
	Advisories []buildAdvisory `json:"advisories,omitempty"`

	// Refusal is what the pipeline run refused on, as one entry a caller reads
	// field by field: the document it points at, the key there, the position of
	// that key, the condition's identifiers and the refusal in the compiler's
	// words. It is present on a build the run itself refused — while rendering, or
	// ahead of the render where the graph is composed or a Component's or a
	// Feature's assertions are checked — and absent on every other answer: a build
	// that succeeded, and one refused by an earlier gate, whose refusal is the
	// guidance text's alone.
	Refusal *buildRefusal `json:"refusal,omitempty"`

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

// buildRefusal is the refusal a pipeline run stopped on, as the build tool
// reports it.
//
// Its fields reuse the names this tool family already gives the same facts about
// a finding, so a caller that reads one finding reads this one: severity,
// message, code and key_path as a build advisory carries them, short_code as the
// explain-error tool names a condition's compact code, file and file_withheld as
// every answer here tells a place on the server's host, line and column as a lint
// finding locates itself, and findings as the lint tool names its list of
// positioned findings.
//
// File is the document the refusal points at — the document its own chain names,
// or the Aggregate where it names none — and KeyPath the key there, when the
// refusal named one. Line and Column are that key's position, counted from one
// with the column in the UTF-16 units a session takes when a client negotiates
// none, and are absent where the refusal named no key or the key was not found in
// the document: a reader is told a position or none, never a guessed one.
// Severity is the halting severity, because this is the finding that stopped the
// build. Message is the refusal with every cause beneath it, exactly as the
// compiler raised it, so whatever was withheld from it where it was raised stays
// withheld here.
//
// Findings is present only where the refusal joins several findings — the
// composition of a graph refuses with every violation it found — and holds one
// entry per finding, in the order the refusal states them, each in this same
// shape: its own document, key, position, identifiers and words. The refusal's
// own place is then its first finding's, and its message carries every one.
type buildRefusal struct {
	File         *string        `json:"file"`
	FileWithheld bool           `json:"file_withheld,omitempty"`
	KeyPath      string         `json:"key_path,omitempty"`
	Line         int            `json:"line,omitempty"`
	Column       int            `json:"column,omitempty"`
	Severity     string         `json:"severity"`
	Code         string         `json:"code,omitempty"`
	ShortCode    string         `json:"short_code,omitempty"`
	Message      string         `json:"message"`
	Findings     []buildRefusal `json:"findings,omitempty"`
}

// buildRefusalOf maps the refusal a pipeline run stopped on onto the tool's wire
// shape, telling each file and the places each message names the way the rest of
// this answer tells them.
//
// The findings, their documents and their keys come from the shared preview,
// which reads them off the refusal's own chain for every surface that positions
// it, so this tool and the language server name one place for each finding of one
// refusal. Each key is resolved to a position by the one walk every positioned
// surface resolves a key through.
func (tw *toolWorkspace) buildRefusalOf(refusal compilerops.RenderRefusal) *buildRefusal {
	entry := tw.refusalEntryAt(refusal.Findings[0], refusal.Message, refusal.Err)
	if len(refusal.Findings) > 1 {
		for _, finding := range refusal.Findings {
			found := tw.refusalEntryAt(finding, finding.Message, finding.Err)
			entry.Findings = append(entry.Findings, *found)
		}
	}
	return entry
}

// refusalEntryAt is one entry in the refusal's shape, placed where finding points
// and worded and identified by message and err.
func (tw *toolWorkspace) refusalEntryAt(finding compilerops.RefusalFinding, message string, err error) *buildRefusal {
	entry := &buildRefusal{
		KeyPath:  finding.KeyPath,
		Severity: abi.SeverityHalt,
		Message:  tw.reportedText(message),
	}
	entry.File, entry.FileWithheld = tw.reportedLocationField(finding.Document)
	entry.Code, _ = cerrors.CodeOf(err)
	entry.ShortCode, _ = cerrors.ShortCodeOf(err)
	if finding.KeyPath == "" || len(finding.Content) == 0 {
		return entry
	}
	var doc yaml.Node
	if yerr := yaml.Unmarshal(finding.Content, &doc); yerr != nil {
		return entry
	}
	if span, ok := yamlpos.FieldRange(&doc, yamlpos.BuildLineIndex(finding.Content), finding.KeyPath); ok {
		entry.Line, entry.Column = span.Line, span.Column
	}
	return entry
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

// previewAdvisories maps the shared preview's own classed warnings onto the
// tool's wire shape, in the order the preview raised them.
//
// Two classes are routed elsewhere rather than dropped. A recorded deprecation
// marker has a field of its own, Deprecations. An extends failure detail
// accompanies a halt and names what refused, so it travels in the refusal's
// guidance text, the way the deprecation refusal quotes every recorded marker.
// Every other class is an exit-code-neutral finding, which is what an entry here
// is: the readings over a Release's composed extends chain, the schema-overlap
// advisories over what the Release reaches, and the notices a caller's own
// catalog load handed in. The switch names the two exceptions rather than the
// carried classes, so a class added to the preview later reaches a caller that
// reads no terminal by default instead of being lost on the way to it.
//
// Every carried class takes warn, the severity for a finding that wants
// attention. The composition reading is declared at warning severity by the rule
// that raises it, the overlap advisory is the warning-level sibling of the schema
// validation beside it, and a catalog notice reports a deprecated shape. None of
// them merely records an observation, which is what info is for.
//
// The message is the warning's own text — what a command-line build prints after
// the prefix naming its class — and the code is the registered condition's,
// carried on the warning from where it was raised. A class raised as text
// carries none.
func previewAdvisories(preview *compilerops.PreviewResult) []buildAdvisory {
	if preview == nil {
		return nil
	}
	var out []buildAdvisory
	for _, w := range preview.Warnings {
		switch w.Class {
		case compilerops.WarningDeprecation, compilerops.WarningExtends:
			continue
		}
		out = append(out, buildAdvisory{Severity: abi.SeverityWarn, Message: w.Text, Code: w.Code})
	}
	return out
}

var buildToolSpec = toolSpec{
	kinds:   []string{"release"},
	summary: "Renders a Release into its deployment artifacts, in memory.",
	description: "Compile a Release into its rendered deployment artifacts entirely in memory and return them — the " +
		"render writes no artifact to disk. Pass the Release path as `release` (absolute, or relative to the " +
		"workspace). The tool runs the full compiler pipeline through the provider render step and returns the produced " +
		"files by output-relative path. Use it to see exactly what a Release would emit before committing to a real build, " +
		"or to inspect one component's rendered output while authoring. In the detailed response format each file's rendered " +
		"content is included; the concise format returns just the file paths. A render failure comes back as actionable " +
		"guidance rather than a partial write (route it through explain-error if needed): a refusal the compiler raises " +
		"while compiling the Release reads in the compiler's words with every cause, and travels as a structured " +
		"`refusal` entry naming the document it points at, the key there and that key's position, with each finding " +
		"listed under `findings` where it joins several. `auto_pull` resolves the " +
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
      "description": "Optional workspace root override. Defaults to the workspace root the server was launched with, or, where it was launched with none, to discovery from its working directory."
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
	reachEscalation: buildReach,
	invoke:          buildInvoke,
}

// buildReach reports how far a build would reach: nothing, which is what the
// operation declares, or the workspace where this call would fill the lockfile
// first.
//
// It reads the fill decision from the one composition every surface carrying a
// build reads — the call's own argument and the workspace's own auto_pull
// setting, which are additive — rather than branching on the argument. Branching
// would be a second derivation of the same decision, and it would agree with the
// build right up until one of the two learned about an input the other did not:
// a workspace configured to fill would then let a caller admitted to read alone
// drive a fill, which is precisely the case the class exists to bound.
//
// It resolves the workspace the way the build resolves it, in the same order and
// from the same arguments, so the two answer about one workspace. Where either
// step is unavailable the declared class is what the call reaches, and that is
// structural rather than a guess: a payload this cannot read is one the build
// refuses before running anything, and a workspace this cannot resolve is one the
// build stops at before the fill. Neither case leaves a fill to be admitted.
func buildReach(s *server, args json.RawMessage) opsurface.Reach {
	var a buildArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return opsurface.ReachNone
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return opsurface.ReachNone
	}
	if tw, err = s.subjectWorkspace(tw, a.Release); err != nil {
		return opsurface.ReachNone
	}
	if tw.cfg.FillsLockfile(a.AutoPull) {
		return opsurface.ReachWorkspace
	}
	return opsurface.ReachNone
}

func buildInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a buildArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("build", err)
		}
	}
	// The answer's shape is read from the arguments alone, before the Release is
	// resolved and before the fill below reaches a registry and writes a lockfile.
	// A call this surface is going to refuse for its response format should not pay
	// for a network round trip and a write first, and it should be answered about
	// the argument it got wrong rather than about a workspace it never reached.
	format, formatRefusal := parseResponseFormat("build", a.ResponseFormat, deliveryResponseFormatDefault)
	if formatRefusal != "" {
		return errorResult(formatRefusal)
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
	if tw, err = s.subjectWorkspace(tw, a.Release); err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for the build.", err, "release"))
	}

	// The catalog directories this Release reads, before the fill and the render.
	// `aggrega build` loads every one of them first and stops at one it cannot walk
	// — a directory this call names that does not exist, or one that cannot be read —
	// while the view this tool renders through passes over such a directory and
	// would build from the ones that remain. So the stack is asked first, the way the
	// command line's load asks it, and a refusal there is answered before the fill
	// below writes a lockfile: what a build refuses, it refuses having written
	// nothing. It answers in this tool's refusal shape, naming the Release and no
	// file; nothing had run, so there is no finding to carry.
	if refused := tw.catalogDirsRefusal(releasePath); refused != nil {
		payload := buildResultPayload{Files: []buildFile{}}
		payload.Release, payload.ReleaseWithheld = tw.reportedLocationField(releasePath)
		return tw.answer(structuredErrorResult(catalogDirsGuidance(
			"The build refused before resolving anything: a catalog directory this Release reads cannot be "+
				"walked, and `aggrega build` refuses the same directory. Nothing was resolved, rendered or written.",
			refused), payload))
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
	fills := tw.cfg.FillsLockfile(a.AutoPull)
	// A directory the workspace configuration lists for this Release where nothing
	// exists is named once, as `aggrega build` names it once. A fill is the
	// dependency resolution that writes that line among its own diagnostics, so a
	// build that fills carries it there, and one that does not carries it among its
	// advisories — read off the same decision that runs the fill, so the two cannot
	// disagree about which of them speaks for it.
	var absent []buildAdvisory
	if !fills {
		for _, line := range tw.absentCatalogDirLines(releasePath) {
			absent = append(absent, buildAdvisory{Severity: abi.SeverityWarn, Message: line})
		}
	}
	if fills {
		report, render, fillErr := runDependencyResolution(ctx, s, tw, releasePath,
			pullArgs{Release: a.Release, WorkspaceRoot: a.WorkspaceRoot, CatalogDirs: a.CatalogDirs}, nil)
		if fillErr != nil {
			// The fill refused, so nothing was rendered and there is no render to
			// report on. What the caller is handed is the resolution's own payload,
			// which carries the diagnostics that say where it stopped — the same
			// detail the pull tool hands back for the same refusal.
			refused := buildResultPayload{Files: []buildFile{}, DependencyResolution: &report}
			refused.Release, refused.ReleaseWithheld = tw.reportedLocationField(releasePath)
			return tw.answer(structuredErrorResult(
				explainToolError("The build refused before rendering: it was asked to resolve this Release's "+
					"dependencies first, and the resolution did not complete. Nothing was rendered.",
					fillErr, "release"),
				refused))
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
		// A lockfile that is a link leading out is refused before the render reads
		// it. This return is reached with nothing run where no fill was asked, and
		// after a fill that wrote its lockfile and conserved no render; the second is
		// a build that refused after writing a lockfile, so what the fill resolved
		// travels in the answer as it does on every other refusal.
		lf, lockRefused := tw.loadReleaseLockfile(releasePath)
		if lockRefused != "" {
			refused := buildResultPayload{Files: []buildFile{}, DependencyResolution: fillReport}
			refused.Release, refused.ReleaseWithheld = tw.reportedLocationField(releasePath)
			return tw.answer(structuredErrorResult(lockRefused, refused))
		}
		view := tw.newView(releasePath, lf)
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
		fetchSeam := compilerops.PinnedExternalInputs(tw.root, tw.cacheDir, tw.cfg, lf, tw.rw.CacheWriteRefusal())

		// This tool renders from the pins the workspace carries by the time it gets
		// here — the ones already on disk, or the ones a fill above wrote before its
		// own render stopped. It runs the strict operation either way, drift verify
		// included, because what it publishes is the claim that these are the bytes a
		// build emits: a build refuses a lockfile that has drifted, so rendering from
		// one here would publish that claim exactly where it does not hold.
		//
		// The drift verify stands down on a lockfile this call filled, and that is not a
		// relaxation. The fill already held that lockfile to this same verify, over the
		// closure a build reconstructs, before writing it, and refuses to write one the
		// verify disagrees with; the verify here is there to catch a lockfile that went
		// stale between one run and another, which a file written moments ago by this
		// run, and checked as it was written, cannot have done.
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

	// Every recorded marker is reported whatever the outcome — on a success in the
	// payload, on a refusal quoted in the message as well — because reporting is
	// unconditional and only halting is configured, so choosing a stricter
	// threshold never costs the caller a finding.
	deprecationNotices := previewWarningTexts(preview, compilerops.WarningDeprecation)

	// The findings are assembled BEFORE the verdict is read, and that ordering is
	// the whole point rather than a tidying. Assembled after a success check, every
	// field below would be populated only on the path where the caller needs it
	// least: a run that finished cleanly. The run that refused is the one whose
	// findings a caller most needs to read field by field, because it has to decide
	// what to change, and reporting them as prose there while reporting them
	// structurally on success would be one capability answering at two fidelities
	// depending on its verdict.
	payload := buildResultPayload{}
	payload.Release, payload.ReleaseWithheld = tw.reportedLocationField(releasePath)
	if len(deprecationNotices) > 0 {
		payload.Deprecations = tw.reportedTexts(deprecationNotices)
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
	payload.DeclinedExternalInputs = tw.reportedTexts(declined)
	// The advisories the build raised travel in the answer, so a caller driving the
	// compiler through this surface receives every finding a command-line build
	// prints rather than only the ones that stopped it. They come from the two
	// places the shared preview raises them — the render's advisory channel, read
	// through the accessor that answers on either verdict, and the preview's own
	// classed warnings — joined in the order a terminal prints them. A run that
	// halted raised whatever the phases ahead of it found, and those are exactly
	// the findings a caller has to act on to get past the halt. The lines naming a
	// listed catalog directory where nothing exists lead them, where a command-line
	// build prints them: before it reads the catalog at all.
	payload.Advisories = slices.Concat(absent, buildAdvisories(preview.PipelineOutcome().Advisories()), previewAdvisories(preview))
	// An advisory's message is prose the compiler composed, so it may name a place
	// on this host; it is told the way the rest of this answer is.
	for i := range payload.Advisories {
		payload.Advisories[i].Message = tw.reportedText(payload.Advisories[i].Message)
	}

	if err != nil {
		// A catalog file the load refused never enters the catalog, so a Release
		// reaching it fails on the REFERENCE — a true statement about a document
		// that is correct. Replaying the refusals the view recorded is what turns
		// that into the constraint the author has to fix, and it is the annotation
		// the command line has always applied, through the one home both read.
		annotated := annotateResolutionWithRefusals(err, rendered, tw, releasePath)
		kind := refusalKindOr(annotated, previewFailureKind(preview, err))
		// A refusal raised while rendering is reported in the compiler's words with
		// every cause beneath it, and as an entry naming where it points: its outer
		// frame alone names only the node being rendered, which tells a caller that
		// the render failed and not what to change.
		if refusal, ok := preview.RenderRefusal(); ok {
			payload.Refusal = tw.buildRefusalOf(refusal)
			return tw.answer(structuredErrorResult(
				explainToolRefusal(previewFailureSummary(preview, deprecationNotices), refusal.Message,
					refusal.Err, kind),
				payload))
		}
		return tw.answer(structuredErrorResult(
			explainToolError(previewFailureSummary(preview, deprecationNotices), annotated, kind),
			payload))
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
		return tw.answer(structuredErrorResult(
			"The build resolved this Release's dependencies first, and the render that resolution "+
				"performed did not finish — so there is no complete set of files to report and none is "+
				"reported. Call pull with this same 'release' path to see where the resolution stopped, "+
				"then build again.",
			payload))
	}
	payload.Files = renderedFiles(completed, format == responseFormatDetailed)
	text := renderBuildText(payload)
	return tw.answer(textResult(text, payload))
}

// previewWarningTexts returns the text of each of a preview's warnings of one
// class, in the order the preview raised them. It is how the two classes that do
// not travel as advisories reach the answer: the deprecation notices on their own
// field, and the extends failure details in the refusal they accompany.
func previewWarningTexts(preview *compilerops.PreviewResult, class compilerops.WarningClass) []string {
	if preview == nil {
		return nil
	}
	texts := make([]string, 0, len(preview.Warnings))
	for _, w := range preview.Warnings {
		if w.Class == class {
			texts = append(texts, w.Text)
		}
	}
	return texts
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
	case compilerops.GateRecordedSignatures:
		return "The build refused: no verified signature is recorded for an artifact the workspace's trust " +
			"configuration requires one for, or a recorded signature's cached bundle no longer matches its digest. " +
			"The diagnostic names each artifact, the file that records no signature for it and the settings that " +
			"require its signature. Resolving the dependencies again verifies each artifact and records the fact; " +
			"through this protocol that is the pull tool, called with this same 'release' path, and the command line " +
			"the diagnostic names is that same operation at a terminal."
	case compilerops.GatePinnedIntegrity:
		return "The build refused: a pinned entry in the cache no longer matches the digest the lockfile records for it, " +
			"and no catalog directory holds the pinned bytes. Re-pull the Release to refill the cache from the registry."
	case compilerops.GateLockfileDrift:
		return "The build refused: the lockfile no longer matches the dependencies this Release resolves to, " +
			"so it would have rendered from pins a build would not have used. Resolving the dependencies " +
			"again clears it. Through this protocol that is the pull tool, called with this same 'release' " +
			"path and no 'upgrade_targets': it reconciles the lockfile against what the Release resolves to " +
			"today, which is what this condition needs. The command line the diagnostic names is that same " +
			"operation at a terminal. Where the pull tool completes and this refusal comes back, the two " +
			"calls resolved over different inputs — most often 'catalog_dirs' given to one and not the " +
			"other, or a workspace configuration edited between them — so call both with the same ones."
	case compilerops.GateClosurePillars:
		return "The build refused: an entity this Release reaches does not satisfy its schema."
	case compilerops.GateExtends:
		// The halt itself only counts the failures, so each one is quoted here, the
		// way the deprecation arm quotes every recorded marker: a caller has to know
		// WHICH inheritance to repair, and the guidance is the one place this
		// refusal names it.
		summary := "The build refused: an entity this Release reaches inherits from a parent that could not be resolved."
		if broken := previewWarningTexts(preview, compilerops.WarningExtends); len(broken) > 0 {
			summary += " Every inheritance this build could not resolve:\n  " + strings.Join(broken, "\n  ")
		}
		return summary
	case compilerops.GateAxisReconstruction:
		return "The build could not reconstruct the Release's pinned dependency set from the lockfile."
	case compilerops.GateRender:
		// The run itself refused, and whether it was rendering when it did is a fact
		// the run reports rather than one read off the refusal's words: the
		// composition of the graph and the assertions a Component or a Feature
		// declares refuse before anything renders.
		if !preview.PipelineOutcome().ReachedRender() {
			return "The build refused before rendering anything; nothing was written to disk."
		}
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
// findings for that choice. They are listed in the order the payload carries
// them, which is the order a command-line build prints them, and their absence
// prints nothing.
func renderBuildText(p buildResultPayload) string {
	var b strings.Builder
	release := reportedLocationWords(p.Release)
	if p.DependencyResolution != nil {
		fmt.Fprintf(&b, "Resolved this Release's dependencies into %s first, then rendered.\n",
			reportedLocationWords(p.DependencyResolution.Lockfile))
		for _, line := range p.DependencyResolution.Diagnostics {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Build of %s rendered %d file(s) in memory (no artifact written to disk):\n", release, len(p.Files))
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
			len(p.DeclinedExternalInputs), release)
		for _, url := range p.DeclinedExternalInputs {
			fmt.Fprintf(&b, "  %s\n", url)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
