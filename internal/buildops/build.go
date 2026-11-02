// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package buildops

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// The external-input store is what the build injects as the wasm
// loader's fetch seam; the assertion keeps the two packages' contracts
// aligned at compile time.
var _ wasm.FetchService = (*externalinputs.Service)(nil)

// The decline recorder is injected in the store's place, so it owes the same
// contract. It is declared against an interface of its own package rather than
// against the loader's, so nothing else would notice the two drifting apart —
// which is what this assertion is for.
var _ wasm.FetchService = (*externalinputs.DeclineRecorder)(nil)

// Phase names a stage of the build orchestration a caller's progress surface can
// display. The values are the stages a caller can meaningfully show; they carry
// no ordering authority over the run, and a caller is free to map them onto a
// surface of its own with more or fewer rows.
type Phase int

const (
	// PhaseLoadCatalog is the tolerant catalog load.
	PhaseLoadCatalog Phase = iota
	// PhaseLockfile is the lockfile step: verifying one is present, or filling
	// one in-process when the automatic pull is on.
	PhaseLockfile
	// PhaseClosure is the dependency resolution the preview performs.
	PhaseClosure
	// PhaseValidate is the closure-scoped validation the preview performs.
	PhaseValidate
	// PhaseInheritance is the inheritance resolution the preview performs.
	PhaseInheritance
	// PhaseRender is the provider render.
	PhaseRender
)

// Reporter carries a caller's diagnostic surface into the run. Every field is
// optional and a nil field is a no-op, so a surface with no terminal supplies
// none of them.
//
// The advisory fields hand the caller the material rather than pre-formatted
// text: the classes and the records are the same for every surface, and how they
// read is each surface's own choice. A caller that renders them writes them in
// its own voice; a caller that reports advisories some other way ignores them
// and loses nothing, because none of them reaches a rendered byte.
type Reporter struct {
	// Phase fires as the run enters each stage.
	Phase func(Phase)
	// Note carries a count or a tier observation that is cheap at a stage
	// boundary, for a caller running a verbose layer.
	Note func(format string, args ...any)
	// RenderNode fires once per node as the render loop completes it: the
	// 1-based count rendered so far, the graph's node count, and the node just
	// finished.
	RenderNode func(done, total int, nodeID string)
	// PluginCompileBegin fires once with the number of provider guests the eager
	// compile pass will compile, before the render loop.
	PluginCompileBegin func(total int)
	// PluginCompileGuestDone fires as each guest finishes compiling, with the
	// running completed count.
	PluginCompileGuestDone func(done int)
	// PluginCompileRunning fires when a guest is observed to actually compile
	// rather than being served from the cache.
	PluginCompileRunning func()
	// CatalogShadows is handed the loaded catalog when the resolution detail is
	// requested and more than one catalog directory was searched, so a caller can
	// report which entities a higher-precedence directory shadowed.
	CatalogShadows func(cat *catalog.Catalog)
	// Advisories is handed the preview's warnings — the extends notices, the
	// catalog lint notices and the deprecation notices — in the order the preview
	// raised them, whether or not the preview then failed. A gate that halted
	// still found everything ahead of it, and a reader who loses those findings
	// has to re-run to see them.
	Advisories func(preview *compilerops.PreviewResult)
	// DryRunDivergence is handed the external references a dry run declined for
	// want of a pin, so a caller can name which references the render could not
	// reach and what pins them. It fires only for a dry run that declined
	// something.
	DryRunDivergence func(declined []string, releasePath string)
}

func (r Reporter) phase(p Phase) {
	if r.Phase != nil {
		r.Phase(p)
	}
}

func (r Reporter) note(format string, args ...any) {
	if r.Note != nil {
		r.Note(format, args...)
	}
}

// irGraph hands the run's graph to a caller that asked for one. A caller that
// asked for none is silent and reports no error, which is what keeps the
// callback its own request rather than needing a switch beside it.
func (o RunOptions) irGraph(graph *types.IRGraph) error {
	if o.IRGraph == nil {
		return nil
	}
	return o.IRGraph(graph)
}

// Pull runs the dependency fill the build's automatic-pull step performs:
// resolving the Release's dependencies into the lockfile and cache in-process.
// upgrade makes that fill a full refresh; detail asks it for the per-entity
// resolution report. The two writers are the fill's diagnostic stream and its
// status stream, in that order.
//
// The build never implements it. Filling the lockfile is the pull operation's
// own job, so the surface that owns the pull supplies it, and a build reached
// from a surface that has no pull to offer refuses the fill rather than
// performing a partial one of its own.
//
// # Why it takes a render option set, and why the WHOLE set travels
//
// Discovering which external inputs a Release consumes is itself a render, so a
// fill renders the Release the build was going to render anyway. render is the
// option set that render runs under — the same value the build would have used —
// handed over whole rather than as a chosen subset, so a knob added to the
// pipeline reaches the fill's render without a second list here having to be
// widened to admit it. ctx carries the render knobs that ride on a context
// rather than on that struct, for the same reason: the fill's render is the
// build's render, so it runs under the build's own context.
//
// # Why it hands a render back
//
// A fill that has rendered the Release has computed exactly what the build needs
// to write. Returning it lets the build present that result instead of computing
// it a second time, which is what makes one build one render.
//
// The contract is the render this fill FINISHED, and nil where it did not finish
// one — a fill that was not reached, that stood down, or whose render stopped
// part way. Nil is therefore not an error signal and not a request: it is the
// absence of a render, and a build that receives it renders for itself. The
// supplier answers the question from the render's own verdict rather than from a
// report beside it, so there is no second reading to disagree with the first.
type Pull func(ctx context.Context, upgrade, detail bool, render pipeline.Options, diagOut, out io.Writer) (*compilerops.PreviewResult, error)

// DefaultTraceDir is the name of the directory a traced build writes to when it
// is told nothing else. Like the default output directory's name it is composed
// beneath the workspace root the build resolved ([DefaultTraceDirIn]), and it
// sits beside that directory rather than under it so the rendered tree stays
// purely deployable.
const DefaultTraceDir = ".trace"

// DefaultTraceDirIn returns the default trace directory beneath root: where a
// traced build writes when nothing names a trace directory of its own. It is
// composed the way [artifactout.DefaultOutputDirIn] composes the output
// directory, beneath the same root, so the two stay siblings wherever the build
// was run from.
func DefaultTraceDirIn(root string) string {
	return filepath.Join(root, DefaultTraceDir)
}

// RunOptions carries the build knobs that are not diagnostics. The zero value is
// the plain build shape: rendered output to the default output directory.
//
// A build never renders in order to acquire, and that is not a knob here or
// anywhere else. Acquisition happens at the pull step, which pins every external
// input a Release's render consumes. A build that renders for itself is held to
// the closure the lockfile records, so an unpinned reference halts naming it;
// and a build whose pull step ran presents that step's render rather than
// performing one, so the acquiring render is still the pull's and still the only
// one there is.
type RunOptions struct {
	// DisablePluginCache skips the persistent on-disk plugin compile cache
	// for this build, compiling guests fresh in an in-memory cache that is
	// discarded when the build ends. It changes only compilation cost, not
	// rendered output.
	DisablePluginCache bool
	// VerifyDeterminism renders every node twice and compares the emitted
	// bytes, halting the build on any divergence. It changes only whether the
	// determinism check runs, not the rendered output of a deterministic build.
	VerifyDeterminism bool
	// AutoPull runs the pull step before the build (the same in-process fill the
	// workspace auto_pull setting performs), turning it on for a single build.
	// Effective when this flag OR workspace auto_pull is set.
	AutoPull bool
	// Detail prints, per resolved entity, what the auto-pull fill did, and reports
	// which entities a higher-precedence catalog directory shadowed. Display-only:
	// it changes only what is written, never the rendered output or the lockfile.
	// The resolution report goes to Out, which is what the corresponding flag's
	// help promises — the per-entity rows, and the note saying so when no fill
	// ran. The shadow report is a catalog-precedence warning rather than
	// resolution detail, and stays on the diagnostic stream with the other
	// warnings.
	Detail bool
	// Upgrade makes the auto-pull step a full refresh: the pull discards the
	// existing pin set and re-resolves every dependency, floating each to the
	// highest available version rather than honoring existing pins. It implies the
	// auto-pull step — requesting it turns the pull step on when neither the flag
	// nor workspace auto_pull is set.
	Upgrade bool
	// TraceUnredacted writes the declared-sensitive values into the trace files
	// rather than the text that stands in for them, and stamps each of those
	// files with a header saying so. It is meaningful only alongside the trace
	// itself: requesting it without the trace halts, because a security-relevant
	// switch that quietly does nothing is the worst of the available outcomes.
	// It reaches the trace and nothing else — the inspect layouts and the
	// diagnostic bodies are identical in both states.
	TraceUnredacted bool
	// SuppressUnsupportedFieldWarnings silences the unsupported-field warning class on
	// the render-warning surface — the per-field notices a lower-fidelity target
	// emits for authored fields it cannot honor. Display-only; other render
	// warnings still surface and the rendered output is unchanged.
	SuppressUnsupportedFieldWarnings bool
	// DryRun reports the render instead of persisting it: each artifact goes out
	// as path-and-content on the same stream the build summary uses, and the
	// output directory is neither cleaned nor written. The external-input closure
	// needs no separate freeze here — no build's render acquires — so a dry run is
	// inert in the same way an ordinary build is, and the flags that fill
	// dependencies are refused alongside it rather than silently ignored.
	DryRun bool
	// OutDir overrides the rendered-artifact output directory; empty renders to
	// the default output directory beneath the workspace root the build resolved
	// ([artifactout.DefaultOutputDirIn]), whatever folder the build was run from.
	OutDir string
	// TraceDir overrides the directory a traced build writes its per-node
	// intermediate files to; empty writes them to .trace beneath the workspace
	// root ([DefaultTraceDirIn]), beside the default output directory. It says
	// only where a trace goes: whether one is written at all is the caller's
	// TraceEnabled, and the directory is cleared and rewritten exactly as .trace
	// is.
	TraceDir string
	// IRGraph, when non-nil, is handed the graph this run resolved, once, after
	// the render succeeded and before anything else the run writes to its status
	// stream. Nil means the caller did not ask for it — the presence of the
	// callback IS the request, so there is no second switch beside it that could
	// disagree with it.
	//
	// It hands over the graph rather than bytes, for the same reason the advisory
	// reports hand over their records: how a graph is serialized, and whether the
	// values an author declared sensitive are withheld from it, belong to the
	// surface that writes it. Serializing one here would be a second emission
	// beside the one the surfaces already share.
	//
	// It is the one callback here that returns an error, because it is the one
	// whose failure is a failed write to the caller's own stream: a run that could
	// not hand back the document it was asked for has not done what was asked, and
	// swallowing that would leave a truncated document behind a successful build.
	IRGraph func(graph *types.IRGraph) error

	// PolicyInputDir, when non-empty, is the directory the payload each
	// governance role hands a policy is written to, one JSON file per role (or per
	// attachment, where a role's attachments receive different payloads), by the
	// names [pipeline.PolicyInputs.Files] composes. The files are written whether
	// the run finished or stopped, so a run that stopped part way writes the roles
	// it reached and names the ones it did not. Asking for them changes no artifact
	// and no verdict, and it combines with DryRun: a dry run asked for them writes
	// them and nothing else.
	PolicyInputDir string
}

// Config is the build operation's input contract. A caller assembles it from
// whatever state it holds — flag values, a tool request — and hands it to Run.
type Config struct {
	// Workspace is the resolved workspace the caller threads in. Required: Run
	// refuses a nil rather than resolving one of its own, because resolving here
	// would make the result depend on a directory no caller named.
	Workspace *workspaceconfig.ResolvedWorkspace

	// ReleasePath is the path of the Release to build.
	ReleasePath string

	// CatalogDirs holds the repeatable catalog-directory flag values
	// (UNresolved): Run prepends them to the workspace catalog_dirs, and forwards
	// the same unresolved list to Pull so the fill re-derives the identical stack
	// under the same config.
	CatalogDirs []string

	// TraceEnabled writes the per-node intermediate files the pipeline collects.
	TraceEnabled bool

	// CompilerVersion is the build identity stamped into IR provenance. It is
	// supplied rather than derived here, because the value names the binary the
	// caller is running and a layer this far from the entry point cannot know
	// which that is.
	CompilerVersion string

	// Options carries the build knobs that are not diagnostics.
	Options RunOptions

	// Report carries the caller's progress surface and advisory voice. The zero
	// value reports nothing.
	Report Reporter

	// Pull performs the dependency fill the automatic-pull step needs, and hands
	// back the render that fill performed. Nil refuses a run that would have to
	// fill, rather than performing a partial fill of its own.
	Pull Pull

	// DiagOut is the sink for the run's per-step diagnostics. Nil defaults to
	// os.Stderr at the top of Run; callers pass a buffer to capture diagnostics
	// without mutating a process global.
	DiagOut io.Writer

	// Display is where the build's render shows what it raises as it raises it.
	// A caller that also fills the lockfile through Pull hands the fill the same
	// value — a pipeline.OperationDisplay — so an advisory both renders raise is
	// shown once for the command. Nil shows on a display of the build's own over
	// DiagOut.
	Display io.Writer

	// Out is the sink for the run's user-facing status output — the resolution
	// report the detail knob asks for, and the dry run's artifact report. Nil
	// defaults to os.Stdout at the top of Run.
	Out io.Writer
}

// Result is what a completed build reports back. The rendered artifacts are
// already written (or, for a dry run, already reported), so what remains is the
// sentence describing what happened, which the caller writes in its own voice
// and on its own stream.
type Result struct {
	// Summary is the one-line completion sentence: how many files were written
	// and where, and how many files the directories written into hold beside them
	// that this build did not write, where there are any — or, for a dry run, how
	// many would have been written.
	Summary string
}

// Run composes the filesystem-touching steps (catalog load, lockfile
// verify-or-fill, external-input gate, artifact write, trace write) around
// compilerops.Preview, which every rendering surface is a transport over.
//
// # One build, one render
//
// The render is performed once per build and no more, and which side performs it
// follows from what the run did rather than from anything a caller sets. A build
// whose lockfile step filled the lockfile already has a render of this Release —
// discovering which external inputs a Release consumes is itself a render — so
// the steps below present that one. A build that filled nothing renders here.
// Everything from the completed payload onward is the same either way, which is
// what makes this operation a presentation layer over a render rather than a
// second place a Release is rendered.
//
// Per-step diagnostics are written to cfg.DiagOut; the run's status output goes
// to cfg.Out. Neither carries the completion summary: that is returned, so the
// caller places it on whichever stream and in whichever layout its own surface
// uses.
//
// # The caller's context, and what ending it does
//
// ctx is the caller's own, and it reaches every step below that can wait on
// something outside the process: the dependency fill, the entity fetches the
// closure walk performs, and the guest calls the render dispatches. Ending it
// ends those — a fetch in flight is abandoned and the run unwinds — so a caller
// whose reader asked to stop has a way to make the stop reach the work rather
// than only the process. A caller with no cancellation of its own passes a
// background context and the behaviour is what it was.
//
// A build writes its artifacts in one step, after the render has finished, so a
// run that ends part way leaves the output directory exactly as it found it: no
// half-written artifact set is presented as a finished one.
func Run(ctx context.Context, cfg Config) (res Result, err error) {
	// Already asked to stop: do none of the work. The check is here rather than
	// only at the steps that wait, because everything below this line reads the
	// file system, and a run whose reader has already withdrawn it should not
	// begin one. It halts naming the withdrawal, so the outcome is a stated
	// reason rather than a build that silently did nothing.
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("aggrega build: %w", err)
	}

	diagOut := cfg.DiagOut
	if diagOut == nil {
		diagOut = os.Stderr
	}
	display := cfg.Display
	if display == nil {
		display = pipeline.NewOperationDisplay(diagOut)
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	run := cfg.Options
	report := cfg.Report
	releasePath := cfg.ReleasePath

	// Flag-pairing halt, before any work: an unredacted trace with no trace to
	// write is a no-op, and a no-op is the one outcome a switch about withheld
	// values must not have — an operator who asked for the values and got a build
	// that silently ignored them has no signal that they are still withheld. The
	// check sits here rather than in a command entry point so it covers every
	// caller of this operation, not only the command line.
	if run.TraceUnredacted && !cfg.TraceEnabled {
		return Result{}, fmt.Errorf("aggrega build: --trace-unredacted needs --trace; there is no trace to write without it. Pass both to write an unredacted trace, or neither")
	}

	// A dry run and a flag that writes cannot both be honored, so the pairing is
	// refused here rather than resolved silently either way: ignoring the write
	// would make a requested fill vanish, and honoring it would break the one
	// promise the dry run makes. Each message names the flag the user actually
	// typed, which is why --upgrade is tested before the promotion below folds it
	// into --auto-pull — a reader told their --auto-pull conflicts, when what they
	// passed was --upgrade, has to guess the connection.
	if run.DryRun {
		switch {
		case run.Upgrade:
			return Result{}, fmt.Errorf("aggrega build: --dry-run conflicts with --upgrade; a dry run writes nothing and --upgrade re-resolves every dependency and rewrites the lockfile. Run `aggrega pull --upgrade --release %s` as its own step, then `aggrega build --dry-run --release %s`", releasePath, releasePath)
		case run.AutoPull:
			return Result{}, fmt.Errorf("aggrega build: --dry-run conflicts with --auto-pull; a dry run writes nothing and --auto-pull fills the lockfile. Run `aggrega pull --release %s` as its own step, then `aggrega build --dry-run --release %s`", releasePath, releasePath)
		case cfg.TraceEnabled:
			return Result{}, fmt.Errorf("aggrega build: --dry-run conflicts with --trace; a dry run writes nothing and --trace writes per-node files to .trace/. Drop --trace to preview the render, or drop --dry-run to write a trace")
		}
	}

	// wsRoot is the resolved workspace root; the catalog renders source-file
	// accessors relative to it so the IR's per-node SourceFile records stay
	// byte-identical across developer machines whose workspaces live at
	// different absolute paths.
	rw, err := requireWorkspace(cfg.Workspace)
	if err != nil {
		return Result{}, err
	}
	wsRoot := rw.Root()

	// The governance payloads, when asked for. The collector rides on the render
	// options assembled below, so whichever render this build presents — its own,
	// or the one its dependency fill performed — records into it. They are written
	// as the run returns, on every path from here: a run that stopped part way
	// writes the roles it reached and names the ones it did not, which is the
	// moment an author most needs to see what a policy would have been handed.
	//
	// A failed write fails a build that otherwise finished, because the files were
	// asked for and are not there; on a build that already failed it is reported
	// beside that failure rather than replacing it.
	var policyInputs *pipeline.PolicyInputs
	if run.PolicyInputDir != "" {
		policyInputs = pipeline.NewPolicyInputs()
		defer func() {
			werr := writePolicyInputs(wsRoot, run.PolicyInputDir, policyInputs, diagOut)
			if werr == nil {
				return
			}
			if err == nil {
				res, err = Result{}, werr
				return
			}
			fmt.Fprintf(diagOut, "  policy input: %v\n", werr)
		}()
	}

	// Workspace config drives the cache directory (the catalog walk skips it as
	// a resolution artifact, not authored catalog) and is reused by the pipeline
	// call below.
	wsCfg := rw.Config()

	// Reproducibility guardrail: a build-critical binding supplied only by the
	// machine's global config (and absent from this workspace's tree) makes the
	// build non-reproducible for anyone who lacks that global file. Warn — never
	// halt, never change the exit code — naming each global-only binding and the
	// command that pins them into the workspace. The provenance resolve failing
	// must not affect the build, so any error here is swallowed advisory-side.
	if bindings, gerr := rw.GlobalOnlyBuildCritical(false); gerr == nil && len(bindings) > 0 {
		var b strings.Builder
		for i, bind := range bindings {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s %s", bind.Field, bind.Identity)
		}
		fmt.Fprintf(diagOut, "warning: this build depends on configuration present only in your global config and absent from this workspace (%s); run `aggrega config capture` to pin it into the workspace for a reproducible build\n", b.String())
	}

	// --upgrade drives a full-refresh pull, so requesting it turns the pull step
	// on: when neither --auto-pull nor workspace auto_pull is set, --upgrade
	// implies the auto-pull step (a full refresh runs before the build) rather
	// than erroring.
	if run.Upgrade {
		run.AutoPull = true
	}

	cacheDir := wsCfg.ResolvedArtifactCacheDir()

	// Effective first-win catalog search stack for this Release: the flag dirs
	// prepended to the directories the workspace configuration gives it, with
	// the workspace's conventional catalog directory appended last when it
	// exists. Its named part travels beside it to every reader of it, unjudged.
	// A configured directory that is not a directory is refused here, before the
	// catalog is read or the lockfile filled, as the pull refuses it.
	dirs, err := rw.ResolveCatalogDirs(releasePath, cfg.CatalogDirs)
	if err != nil {
		return Result{}, err
	}
	namedDirs, err := rw.NamedCatalogDirs(releasePath, cfg.CatalogDirs)
	if err != nil {
		return Result{}, err
	}

	// Tolerant Pass-1 catalog load. A file that fails to read, parse, or index is
	// SKIPPED (recorded) rather than aborting the walk — so a broken catalog entity
	// OUTSIDE this Release's closure never fails the build. The closure walk +
	// closure-scoped Pass-2 below validate exactly what the Release reaches;
	// catalog-wide validation is `aggrega lint`'s job. Pass 1 does not expand
	// cross-document $ref pillars, so a reference to a not-yet-resolved remote
	// library does not fail here.
	report.phase(PhaseLoadCatalog)
	cat, skipped, err := catalog.LoadPassOneTolerantWithNamedDirs(wsRoot, cacheDir, namedDirs, dirs...)
	if err != nil {
		return Result{}, fmt.Errorf("catalog load: %w", err)
	}
	report.note("catalog search dirs: %d", len(dirs))
	// The count is of files the load refused while indexing them. A file of a kind
	// no catalog holds is in the record too, so a failure it explains can name it,
	// but it was never a candidate for the catalog, and counting it would report
	// as refused a file every load passes over.
	refused := 0
	for _, s := range skipped {
		if !s.KindNotHeld() {
			refused++
		}
	}
	if refused > 0 {
		report.note("catalog files skipped at load: %d", refused)
	}

	// When more than one catalog directory is searched, report which entities a
	// higher-precedence directory shadowed (detail only); the override is never
	// silent.
	if run.Detail && report.CatalogShadows != nil {
		report.CatalogShadows(cat)
	}

	// The render knobs, assembled ONCE and above the lockfile step, because the
	// step below may render this Release: the fill discovers which external inputs
	// a Release consumes by rendering it, and that render is this build's render.
	// Assembling them here is what lets the same option set drive whichever render
	// happens, so a knob added to the pipeline reaches both without a second
	// assembly having to be widened to admit it.
	//
	// Neither depends on anything the lockfile step produces: every member below
	// reads the caller's own knobs and this run's report callbacks, and nothing
	// reads the lockfile, the catalog or the closure.
	//
	// The context carries the render knobs that ride on a context rather than on
	// the option struct; the external-input seam is installed by the shared entry
	// point, from the service handed to it.
	//
	// It is derived from the caller's own rather than started fresh, which is what
	// makes ending that one end the work below: a context minted here would carry
	// the knobs and nothing else, leaving every waiting step unreachable from
	// outside.
	buildCtx := wasm.ContextWithDisablePluginCache(ctx, run.DisablePluginCache)
	buildCtx = wasm.ContextWithVerifyDeterminism(buildCtx, run.VerifyDeterminism)
	renderOptions := pipeline.Options{
		IncludeTrace:    cfg.TraceEnabled,
		TraceUnredacted: run.TraceUnredacted,
		// Per-node render progress. The callback reaches only the caller's
		// diagnostic surface; the rendered output is byte-deterministic
		// regardless of whether one is attached. A caller that attached none
		// discards the calls.
		NodeProgress: func(done, total int, nodeID string) {
			if report.RenderNode != nil {
				report.RenderNode(done, total, nodeID)
			}
		},
		// Eager plugin-compilation phase callbacks. Begin fires once with the
		// number of provider guests to compile before the render loop; Running
		// fires when a guest is observed to actually compile; GuestDone fires as
		// each finishes. All three reach only the caller's diagnostic surface, so
		// the rendered output stays byte-deterministic. They are set
		// unconditionally, because a set Begin is what makes the pipeline run the
		// eager compile pass so a guest compilation is attributed rather than
		// buried in the first node's render — leaving it unset for a caller with
		// no progress surface would change when guests compile, not merely what is
		// displayed.
		PluginCompileBegin: func(total int) {
			if report.PluginCompileBegin != nil {
				report.PluginCompileBegin(total)
			}
		},
		PluginCompileGuestDone: func(done int) {
			if report.PluginCompileGuestDone != nil {
				report.PluginCompileGuestDone(done)
			}
		},
		PluginCompileRunning: func() {
			if report.PluginCompileRunning != nil {
				report.PluginCompileRunning()
			}
		},
		SuppressUnsupportedFieldWarnings: run.SuppressUnsupportedFieldWarnings,
		PolicyInputs:                     policyInputs,
	}

	// Ensure the per-Release lockfile exists (filled when the pull step is asked
	// for, else required), returning whether it pre-existed — drift is verified
	// here only for a pre-existing lockfile, because the fill held the one it wrote
	// to this same verify before writing it and refuses to write one it disagrees
	// with — and the render the fill performed, where it performed one.
	report.phase(PhaseLockfile)
	lockfilePreexisted, filled, err := ensureLockfile(buildCtx, cfg, wsCfg, run, renderOptions, diagOut, out)
	if err != nil {
		return Result{}, err
	}
	lf, _, err := lockfile.Load(wsRoot, lockfile.PathFor(releasePath))
	if err != nil {
		return Result{}, fmt.Errorf("load lockfile: %w", err)
	}

	// The render, or the render that already happened.
	//
	// A fill renders this Release to find out which external inputs it consumes,
	// so a build that filled its own lockfile already holds a render of what it
	// was about to render. It presents that one; it renders only where there was
	// none to inherit. The presence of the render IS the decision — a fact about
	// what this run did, which cannot be read two ways and which no dial can
	// contradict. There is deliberately nothing here asking whether to reuse it:
	// a switch of that shape would be a second answer to a question the render
	// itself already answers by existing.
	//
	// declined is the references this build's own render was refused, and it is
	// empty on the presenting path for the same reason it is a fact rather than a
	// setting: a build that rendered nothing refused nothing. A dry run never
	// presents — it refuses the flags that fill and stands the workspace setting
	// down — so the note below is reached from the rendering path alone.
	preview := filled
	var declined []string
	if preview == nil {
		// External-input gate for render-time guest fetches, and it is the same seam
		// every other rendering surface attaches: the pinned posture behind the
		// decline recorder, taken from the one composition that describes it. A build
		// renders the closure the lockfile records — a pinned URL is served from the
		// content-addressed store (refilled by its pinned digest if the blob has gone
		// missing, which cannot change what the render sees) and an unpinned one is
		// refused loudly, naming it. Acquisition belongs to the pull step, so there is
		// nothing for a build to merge back afterwards and no posture for it to choose.
		// A refill writes the cache, so in a folder that is no workspace it declines
		// with the resolution's own refusal instead.
		fetchSeam := compilerops.PinnedExternalInputs(wsRoot, cacheDir, wsCfg, lf, rw.CacheWriteRefusal())

		// The build's determinism-wired lazy lookup: EVERY catalog consumer this run
		// has — guards, triad aggregate resolution, manifest-implements gate,
		// closure computation + closure-scoped validation + extends, renderer,
		// topology — resolves the resolved closure through this. No cache→catalog
		// bridge: the lazy lookup reads registry-pulled entities directly from the
		// pinned Tier-2 cache. Nil puller: Tier-3 is disabled, so the build never
		// networks. SetPins threads the lockfile pins so a Tier-2 cache hit is
		// verified-on-read and a miss is a loud halt rather than a silent skip —
		// byte-identical from the lockfile.
		lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil, namedDirs...)
		lazyView.SetPins(catalog.PinsFromLockfile(lf))
		lazyView.WriteNoticesTo(diagOut)

		// The preview computation itself — the gate sequence and the render — runs in
		// the shared entry point every rendering surface is a transport over, so the
		// command line, the editor's build tool and the daemon cannot come to disagree
		// about which gates run or in what order. What stays here is what genuinely
		// belongs to this operation: the knobs, the progress reporting, and the writes.
		//
		// A build emits a deployable artifact, so every gate is on and every
		// divergence halts. It holds the Release to its lockfile, which the phase
		// above has already ensured exists.
		preview, err = compilerops.PreviewForArtifact(
			buildCtx,
			compilerops.NewPreviewInputs(
				fetchSeam,
				lazyView,
				releasePath,
				wsRoot,
				wsCfg,
				cfg.CompilerVersion,
				compilerops.WithLockfile(lf, lockfilePreexisted),
				// The tolerant Pass-1 load's deprecation notices are this operation's to
				// produce (no other surface loads a whole catalog) and the entry point's
				// to emit, so the advisory sequence has one order.
				compilerops.WithCatalogWarnings(cat.DeprecationWarnings()),
				compilerops.WithProgress(func(p compilerops.PreviewPhase) { report.phase(previewPhase(p)) }),
				compilerops.WithDisplay(display),
			),
			renderOptions,
		)
		declined = fetchSeam.Declined()
	}
	// The warnings are reported before the failure is returned, because a gate that
	// halted still found everything ahead of it and a reader who loses those
	// findings has to re-run to see them.
	if report.Advisories != nil {
		report.Advisories(preview)
	}
	// The divergence note sits beside the warnings and is not conditioned on the
	// failure: a decline halts the render, so it is the failure path in practice,
	// but the note is owed whenever a reference was declined rather than whenever
	// the run happened to end badly.
	if run.DryRun && len(declined) > 0 && report.DryRunDivergence != nil {
		report.DryRunDivergence(declined, releasePath)
	}
	if err != nil {
		// Only a resolution or validation failure can be explained by a file the
		// tolerant load refused, or by where local entities are read from, so only
		// those are annotated: a deprecation halt carrying a note about skipped
		// catalog files would name bystanders as the cause. The annotation is
		// composed here rather than handed in by the caller, so every surface that
		// runs this build reports the same account of the same failure.
		if compilerops.IsResolutionFailure(err) {
			return Result{}, compilerops.AnnotateSkipContext(err, skipped, rw.LocalCatalogAdviceNote(releasePath))
		}
		return Result{}, err
	}
	cl := preview.Resolved.Closure
	report.note("closure entities: %d", len(cl.Entities))
	report.note("schema libraries: %d, mapping libraries: %d, workload libraries: %d", len(cl.SchemaLibraries), len(cl.MappingLibraries), len(cl.WorkloadLibraries))
	// The run finished — the failure above returned — so this reports from the
	// finished payload. A command-line build writes artifacts and hands back the
	// graph it rendered from, and a run that stopped has neither.
	result := preview.PipelineOutcome().Completed()
	// The presented render is the one case where the verdict comes from outside
	// this operation: a build that rendered for itself has already returned on the
	// failure above, while a presented render is only as good as the promise the
	// surface supplying the fill made about it. A partial render reaching here
	// would be written out as though it were whole — an artifact set missing the
	// nodes the render never reached — so it is refused instead, on the same terms
	// every other wiring error is.
	if result == nil {
		return Result{}, fmt.Errorf("internal: the dependency fill handed back a render that did not finish — only a finished render can be presented")
	}

	// The graph this build resolved, handed back before anything else reaches the
	// status stream — ahead of the dry run's artifact report and ahead of the
	// completion summary the caller writes when Run returns. A build already holds
	// the graph it rendered from, so handing it back costs no second resolution
	// and cannot disagree with the artifacts written below.
	if err := run.irGraph(result.IRGraph); err != nil {
		return Result{}, err
	}

	// The defaults are composed beneath the workspace root this build resolved, so
	// a build writes the same directories wherever it was run from — the root
	// itself, a folder beneath it, or a folder elsewhere that named the root — and
	// names them in full. Where the resolution found no workspace and fell back to
	// the folder it started from, that folder is the root, and the build writes
	// there.
	outDir := run.OutDir
	if outDir == "" {
		outDir = artifactout.DefaultOutputDirIn(wsRoot)
	}
	traceDir := run.TraceDir
	if traceDir == "" {
		traceDir = DefaultTraceDirIn(wsRoot)
	}

	// What is written in each directory is held to the root the directory it is
	// composed in picks: for the defaults that is the workspace root itself, and
	// for a directory a caller named, the workspace root where it lies inside the
	// workspace and that directory where it lies outside it. The decision is the
	// one every composed write asks, not this build's.
	outRoot := writescope.RootFor(wsRoot, filepath.Dir(outDir))
	traceRoot := writescope.RootFor(wsRoot, filepath.Dir(traceDir))

	// The trace directory is cleared and written below the output, so it is held
	// to its root before the output is written: a trace directory leading out of
	// it halts the build before anything of it lands on disk, rather than after the
	// artifacts have been replaced.
	if cfg.TraceEnabled && len(result.Trace) > 0 {
		if err := writescope.CheckDelete(traceRoot, traceDir); err != nil {
			return Result{}, fmt.Errorf("write trace: %w", err)
		}
	}

	// A dry run reports the render and leaves the output directory alone —
	// including the clean, which is itself a write: a dry run that emptied
	// boundaries/ and wrote nothing back would have destroyed the previous build's
	// output in order to show what the next one would contain.
	var summary string
	if run.DryRun {
		// The judgement the write would make is made here too, so a dry run halts
		// where the build would rather than reporting a write that would not happen.
		if err := artifactout.CheckBuildOutput(outRoot, outDir, result.Artifacts); err != nil {
			return Result{}, fmt.Errorf("write output: %w", err)
		}
		if err := artifactout.WriteDryRunReport(out, outDir, result.Artifacts); err != nil {
			return Result{}, fmt.Errorf("write output: %w", err)
		}
		summary = fmt.Sprintf("Dry run: %d files would be written to %s; nothing was written", len(result.Artifacts), outDir)
		if run.PolicyInputDir != "" {
			// The one write a dry run makes is the one it was asked for, and the
			// summary says so rather than claiming nothing was written.
			summary = fmt.Sprintf("Dry run: %d files would be written to %s; only the policy inputs were written", len(result.Artifacts), outDir)
		}
	} else {
		// Write output. The boundaries subtree from a prior build of a different
		// variant is cleared as part of the write, so GitOps consumers see exactly
		// what the compiler just produced.
		unwritten, werr := artifactout.WriteBuildOutput(outRoot, outDir, result.Artifacts)
		if werr != nil {
			return Result{}, fmt.Errorf("write output: %w", werr)
		}
		summary = fmt.Sprintf("Build completed: %d files written to %s", len(result.Artifacts), outDir)
		// A file the write found beside its own and did not produce is left where it
		// is and told once, in the sentence every surface performing a build uses,
		// and the summary counts it: a directory mixing this build's files with
		// others reads as one build's output unless something says otherwise.
		if len(unwritten) > 0 {
			fmt.Fprintf(diagOut, "  warning: %s\n", artifactout.UnwrittenAdvisory(unwritten))
			summary += fmt.Sprintf(", beside %d file(s) this build did not write", len(unwritten))
		}
	}

	// Trace surface — write the collected per-step intermediate state
	// to .trace/ (sibling to dist/, never under). The sibling layout
	// keeps dist/ purely deployable: no flag-conditional contents, no
	// apply-tool walking surprises. Skip when the trace map is empty
	// so an empty-trace fixture run does not clobber a pre-existing
	// .trace/ from an earlier traced build.
	//
	// This carries no dry-run guard, and deliberately so: the refusal above is the
	// guard, and a second one here would be a copy that agrees with it today. What
	// keeps the two coupled is a test asserting the pairing refuses, so removing
	// the refusal fails loudly rather than quietly re-opening this write.
	if cfg.TraceEnabled && len(result.Trace) > 0 {
		if err := artifactout.ReplaceAll(traceRoot, traceDir, result.Trace); err != nil {
			return Result{}, fmt.Errorf("write trace: %w", err)
		}
	}

	// Nothing to merge back into the lockfile, on either path. The external-input
	// section is written by the pull step alone: a build that rendered for itself
	// acquired nothing to merge, and a build presenting the fill's render is
	// holding a render whose acquisitions the fill already wrote before it
	// returned. That is what makes this operation a read of the closure rather
	// than an extension of it — whichever lockfile the presented or performed
	// render was held to is the lockfile this operation leaves behind.

	return Result{Summary: summary}, nil
}

// writePolicyInputs writes the governance payloads a run recorded into dir and
// prints, on diagOut, the account [WritePolicyInputs] returns.
func writePolicyInputs(wsRoot, dir string, collected *pipeline.PolicyInputs, diagOut io.Writer) error {
	lines, err := WritePolicyInputs(wsRoot, dir, collected)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Fprintf(diagOut, "  %s\n", line)
	}
	return nil
}

// WritePolicyInputs writes the governance payloads a run recorded into dir and
// returns its account of them, one line each: how many files were written and
// where, then each role that wrote nothing or less than its attachments, with the
// reason. It is the one write of those files and the one wording of that account,
// so every surface that offers the payloads writes and reports them alike.
//
// The files replace an earlier export of the same naming and nothing else the
// directory holds, so a role whose attachments changed between two runs leaves
// no file of the earlier one behind. Where dir is held is the one answer every
// composed write asks: inside the workspace at wsRoot it is held to that root,
// and a directory named outside it is its own root. A relative dir is resolved
// against the working directory.
func WritePolicyInputs(wsRoot, dir string, collected *pipeline.PolicyInputs) ([]string, error) {
	files := collected.Files()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("write policy inputs: %w", err)
	}
	root := writescope.RootFor(wsRoot, filepath.Dir(abs))
	if err := artifactout.ReplaceNamed(root, abs, files.Files, pipeline.IsPolicyInputFileName); err != nil {
		return nil, fmt.Errorf("write policy inputs: %w", err)
	}
	lines := []string{fmt.Sprintf("policy input: %d file(s) written to %s", len(files.Files), abs)}
	for _, gap := range files.Missing {
		lines = append(lines, fmt.Sprintf("policy input: no file for %s: %s", gap.Role, gap.Reason))
	}
	return lines, nil
}

// requireWorkspace returns the workspace a caller threaded in from its own
// resolution chokepoint. A nil workspace is a wiring error — the call was
// reached without any caller having resolved one — so it is reported rather than
// repaired. Resolving here from the current directory would make the result
// depend on a directory no caller named, which is exactly the dependency routing
// every surface through one resolution point exists to remove.
func requireWorkspace(ws *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if ws == nil {
		return nil, fmt.Errorf("internal: no resolved workspace supplied — the caller must resolve one and pass it in")
	}
	return ws, nil
}

// previewPhase maps a stage of the shared preview sequence onto this layer's
// phase vocabulary. The two are deliberately separate: this layer also carries
// stages the preview knows nothing about (the catalog load and the lockfile step
// happen before the preview begins), so a shared enumeration would have to name
// stages only one side has.
func previewPhase(p compilerops.PreviewPhase) Phase {
	switch p {
	case compilerops.PhaseClosure:
		return PhaseClosure
	case compilerops.PhaseValidate:
		return PhaseValidate
	case compilerops.PhaseInheritance:
		return PhaseInheritance
	case compilerops.PhaseRender:
		return PhaseRender
	}
	return PhaseClosure
}

// ensureLockfile ensures the per-Release lockfile exists before the build wires
// its lazy lookup + runs closure-scoped Pass-2, returning whether it PRE-EXISTED
// and the render the fill performed where one ran.
//
// The render comes back because the fill's way of finding out which external
// inputs a Release consumes is to render it, so a fill leaves a render of this
// Release behind. Handing it up is what lets the build present it rather than
// compute the same thing again. It is nil wherever no fill ran, and nil is
// exactly what it means — there is no render to present — so the caller renders
// for itself and nothing has to be consulted to find that out.
//
// Build is read-only by default: a present lockfile is left as-is — drift
// against it is verified later against the resolved closure — and a missing one
// halts with the pull-first remediation, writing no lockfile or source pins.
// When the pull step is requested — the caller's AutoPull knob or workspace
// auto_pull — the fill runs in-process before the build proceeds whether or not
// one is present, honoring existing pins and resolving what has none; Upgrade
// makes it a full refresh, floating every dependency to the highest satisfying
// version instead.
//
// The offline posture adds no rule of its own here. A fill under it is the same
// pull, and that pull reads only what the machine already holds — the catalog
// directories and the cache — so a Release those satisfy is filled and built,
// and a reference neither holds halts the pull naming it. Upgrade, whose whole
// job is to ask a registry what it offers now, is refused by that pull before it
// resolves anything. Deciding offline a second time here would make this build
// refuse a fill the pull, the daemon's build and the tool protocol's build all
// run.
//
// DryRun suppresses the fill outright: the fill writes, and a dry run writes
// nothing. It is read here rather than inferred from AutoPull because the two
// are independent — the fill also runs from the workspace setting, which no flag
// refusal reaches — so a dry run that only refused the flag would still fill in a
// workspace configured to.
//
// The lockfile must exist before the lazy lookup is pinned: its pins drive the
// verify-on-read Tier-2 reads through which every closure entity (and the
// libraries a $ref names) resolves.
//
// The two sinks are kept apart, because the resolution detail is promised on the
// status stream. out carries that report: the no-pull note written here, and the
// fill's per-entity rows and completion line forwarded through the fill's own
// status writer. diagOut carries the fill's reuse/verify notices and progress
// surface.
func ensureLockfile(ctx context.Context, cfg Config, wsCfg workspaceconfig.Config, run RunOptions, render pipeline.Options, diagOut, out io.Writer) (bool, *compilerops.PreviewResult, error) {
	releasePath := cfg.ReleasePath
	lockfilePath := lockfile.PathFor(releasePath)

	_, statErr := os.Stat(lockfilePath)
	preexisted := statErr == nil
	if statErr != nil && !stderrors.Is(statErr, os.ErrNotExist) {
		return false, nil, fmt.Errorf("lockfile stat %s: %w", lockfilePath, statErr)
	}

	// build's auto-pull is the opt-in fill: it runs when the caller's AutoPull knob
	// or workspace.auto_pull is set. That composition is not made here — it is read
	// from the workspace config, which owns one of the two inputs and is the one
	// home every build-carrying surface asks, so this build and a build driven
	// through the daemon or the tool protocol cannot come to rank the two
	// differently. By default (upgrade off) it is lockfile-honoring
	// — it honors every existing pin (resolving to the pinned version, verifying the
	// pinned digest, halting on a drifted pin), resolves + adds un-pinned references,
	// refills an empty cache by pin, and prunes stale pins; on a complete,
	// cache-populated lockfile it is a no-op that fetches nothing (every pin a Tier-2 hit),
	// so the build stays hermetic given a complete lockfile. With upgrade the fill
	// is a full refresh: it discards the existing pin set and re-resolves every
	// dependency, floating each to the highest satisfying version. There is no
	// absent-vs-partial special case: any incomplete/empty state runs the same fill,
	// and lockfile.Verify (in the preview's gate sequence) is the post-condition,
	// which the fill runs itself over the lockfile it composes before writing it. The
	// build's own resolution runs through the nil-puller pinned lazy lookup, untouched
	// — so build output is unchanged by this fill.
	//
	// A dry run turns the fill off however it was asked for. Asking through the
	// knobs is refused before the build starts; this leg is the workspace setting,
	// which a dry run overrides for the one invocation rather than refusing to run
	// at all — a workspace that fills by default would otherwise have no way to
	// preview a render. A missing lockfile then halts with the same pull-first
	// remediation the read-only default gives, which is the honest outcome: there
	// is nothing to render against and filling one would be a write.
	if fillRuns(wsCfg, run) {
		if cfg.Pull == nil {
			return false, nil, fmt.Errorf("internal: no dependency fill supplied — a build that fills the lockfile needs the caller's pull operation")
		}
		// The fill runs under this build's own context and option set, so the render
		// it performs to discover external inputs is the render this build would
		// have performed — and comes back so the build can present it.
		filled, err := cfg.Pull(ctx, run.Upgrade, run.Detail, render, diagOut, out)
		if err != nil {
			return false, nil, fmt.Errorf("auto-pull: %w", err)
		}
		return false, filled, nil
	}

	// Read-only default: the build is frozen against the lockfile as it stands and
	// never writes source pins. A present lockfile is used as-is; a missing one halts
	// with the pull-first remediation.
	if preexisted {
		if run.Detail {
			// The reason no detail follows is that no pull step was ASKED for, not
			// that a lockfile happens to be present: a pull step runs whether or not
			// one is, so naming the file as the cause would misdescribe the one case
			// this note is printed in.
			fmt.Fprintf(out, "aggrega build --detail: resolution detail is a pull-time surface; no pull step ran this build, so there is nothing to report. Pass --auto-pull (or enable auto_pull) to fill %s during the build and report what the fill did\n", lockfilePath)
		}
		return true, nil, nil
	}
	return false, nil, fmt.Errorf("aggrega build: lockfile %s missing; run `aggrega pull --release %s` first to create it, or pass --auto-pull (or enable auto_pull) to fill it during the build",
		lockfilePath, releasePath)
}

// fillRuns reports whether this build's lockfile step runs the dependency fill.
// The answer is the workspace configuration's FillsLockfile — the decision the
// daemon's build and the tool protocol's build read as well — with the one rule
// this command adds on top: a dry run writes nothing, so it runs no fill however
// the fill was asked for. It reads no offline posture, because the pull the fill
// runs enforces that posture itself. It is read by the lockfile step that runs
// the fill and by every earlier step that has to know whether the fill will speak
// for it.
func fillRuns(wsCfg workspaceconfig.Config, run RunOptions) bool {
	return !run.DryRun && wsCfg.FillsLockfile(run.AutoPull)
}
