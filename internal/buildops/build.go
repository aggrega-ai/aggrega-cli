// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package buildops

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
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
	// want of a pin, so a caller can say that the render stopped short of the
	// bytes a real build would have produced. It fires only for a dry run that
	// declined something.
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
type Pull func(upgrade, detail bool, diagOut, out io.Writer) error

// RunOptions carries the build knobs that are not diagnostics. The zero value is
// the plain build shape: acquisition on, rendered output to the default output
// directory, no acquisition report.
type RunOptions struct {
	// NoAcquireInputs freezes the external-input closure: a render that
	// requests an unpinned external URL halts with a diagnostic instead
	// of fetching and pinning it.
	NoAcquireInputs bool
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
	// output directory is neither cleaned nor written. It also freezes the
	// external-input closure,
	// which is what keeps the run inert rather than merely output-less — a
	// render that met an unpinned external reference would otherwise fetch it
	// and write a blob into the content-addressed store on its way to producing
	// buffers nobody persists. The flags that fill dependencies are refused
	// alongside it rather than silently ignored.
	DryRun bool
	// OutDir overrides the rendered-artifact output directory; empty
	// renders to the default output directory. The acquisition operation points
	// it at a discard directory so an acquisition pass never touches the real
	// output directory.
	OutDir string
	// OnAcquired, when non-nil, runs after a successful build with the
	// external-input records this run newly pinned (empty when the run
	// pinned nothing). The acquisition operation reports through it.
	OnAcquired func([]lockfile.ExternalInput)
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

	// Pull performs the dependency fill the automatic-pull step needs. Nil
	// refuses a run that would have to fill, rather than performing a partial
	// fill of its own.
	Pull Pull

	// AnnotateResolutionFailure, when non-nil, is handed a resolution or
	// validation failure together with the catalog files the tolerant load
	// refused, and returns the failure the caller wants reported. Only a
	// resolution or validation failure can be explained by a refused file, so
	// only those reach it: a deprecation halt carrying a note about skipped
	// catalog files would name bystanders as the cause. Nil leaves every failure
	// exactly as raised.
	AnnotateResolutionFailure func(err error, skipped []catalog.SkippedEntity) error

	// DiagOut is the sink for the run's per-step diagnostics. Nil defaults to
	// os.Stderr at the top of Run; callers pass a buffer to capture diagnostics
	// without mutating a process global.
	DiagOut io.Writer

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
	// and where, or — for a dry run — how many would have been.
	Summary string
}

// Run composes the filesystem-touching steps (catalog load, lockfile
// verify-or-fill, external-input gate, artifact write, trace write, external-input
// merge) around compilerops.Preview, which every rendering surface is a transport
// over.
//
// Per-step diagnostics are written to cfg.DiagOut; the run's status output goes
// to cfg.Out. Neither carries the completion summary: that is returned, so the
// caller places it on whichever stream and in whichever layout its own surface
// uses.
func Run(cfg Config) (Result, error) {
	diagOut := cfg.DiagOut
	if diagOut == nil {
		diagOut = os.Stderr
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
		// The frozen-input behaviour is adopted, not reimplemented: the same switch
		// --no-acquire-inputs sets is the one thing that makes a dry run inert, and
		// a second frozen-input semantic would be free to drift from it.
		run.NoAcquireInputs = true
	}

	// wsRoot is the resolved workspace root; the catalog renders source-file
	// accessors relative to it so the IR's per-node SourceFile records stay
	// byte-identical across developer machines whose workspaces live at
	// different absolute paths.
	rw, err := requireWorkspace(cfg.Workspace)
	if err != nil {
		return Result{}, fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()

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

	// Effective first-win catalog search stack: the flag dirs prepended to the
	// workspace catalog_dirs, defaulting to the current directory.
	dirs := workspaceconfig.ResolveCatalogDirs(cfg.CatalogDirs, wsCfg, ".")

	// Tolerant Pass-1 catalog load. A file that fails to read, parse, or index is
	// SKIPPED (recorded) rather than aborting the walk — so a broken catalog entity
	// OUTSIDE this Release's closure never fails the build. The closure walk +
	// closure-scoped Pass-2 below validate exactly what the Release reaches;
	// catalog-wide validation is `aggrega lint`'s job. Pass 1 does not expand
	// cross-document $ref pillars, so a reference to a not-yet-resolved remote
	// library does not fail here.
	report.phase(PhaseLoadCatalog)
	cat, skipped, err := catalog.LoadPassOneTolerant(wsRoot, cacheDir, dirs...)
	if err != nil {
		return Result{}, fmt.Errorf("catalog load: %w", err)
	}
	report.note("catalog search dirs: %d", len(dirs))
	if len(skipped) > 0 {
		report.note("catalog files skipped at load: %d", len(skipped))
	}

	// When more than one catalog directory is searched, report which entities a
	// higher-precedence directory shadowed (detail only); the override is never
	// silent.
	if run.Detail && report.CatalogShadows != nil {
		report.CatalogShadows(cat)
	}

	// Ensure the per-Release lockfile exists (filled when the pull step is asked
	// for, else required), returning whether it pre-existed — drift is verified
	// only for a pre-existing lockfile; a filled one matches the closure already.
	report.phase(PhaseLockfile)
	lockfilePreexisted, err := ensureLockfile(cfg, wsCfg, run, diagOut, out)
	if err != nil {
		return Result{}, err
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		return Result{}, fmt.Errorf("load lockfile: %w", err)
	}

	// External-input gate for render-time guest fetches: the service serves
	// every aggrega_fetch_external_input call under the pin discipline —
	// pinned URLs from the content-addressed store, unpinned URLs fetched
	// and pinned when acquisition is on (the default), refused loudly when
	// the closure is frozen. Seeded with the lockfile's pinned view;
	// newly-acquired pins merge back after a successful build below.
	extInputs := newExternalInputsService(cacheDir, lf, run.NoAcquireInputs, wsCfg)

	// The seam the render sees is the service behind a recorder, so a reference
	// declined for want of a pin is remembered by the host that declined it. The
	// service itself is still held directly, because the acquisition records the
	// lockfile write below reads are the service's own and the recorder does not
	// carry them.
	fetchSeam := externalinputs.NewDeclineRecorder(extInputs)

	// The build's determinism-wired lazy lookup: EVERY catalog consumer this run
	// has — guards, triad aggregate resolution, manifest-implements gate,
	// closure computation + closure-scoped validation + extends, renderer,
	// topology — resolves the resolved closure through this. No cache→catalog
	// bridge: the lazy lookup reads registry-pulled entities directly from the
	// pinned Tier-2 cache. Nil puller: Tier-3 is disabled, so the build never
	// networks. SetPins threads the lockfile pins so a Tier-2 cache hit is
	// verified-on-read and a miss is a loud halt rather than a silent skip —
	// byte-identical from the lockfile.
	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lazyView.SetPins(catalog.PinsFromLockfile(lf))

	// The preview computation itself — the gate sequence and the render — runs in
	// the shared entry point every rendering surface is a transport over, so the
	// command line, the editor's build tool and the daemon cannot come to disagree
	// about which gates run or in what order. What stays here is what genuinely
	// belongs to this operation: the knobs, the progress reporting, and the writes.
	//
	// The context carries this run's own render knobs; the external-input seam
	// is installed by the entry point, from the service handed to it below.
	buildCtx := wasm.ContextWithDisablePluginCache(context.Background(), run.DisablePluginCache)
	buildCtx = wasm.ContextWithVerifyDeterminism(buildCtx, run.VerifyDeterminism)

	// A build emits a deployable artifact, so every gate is on and every
	// divergence halts. It holds the Release to its lockfile, which the phase
	// above has already ensured exists.
	preview, err := compilerops.PreviewForArtifact(
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
		),
		pipeline.Options{
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
		},
	)
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
	if declined := fetchSeam.Declined(); run.DryRun && len(declined) > 0 && report.DryRunDivergence != nil {
		report.DryRunDivergence(declined, releasePath)
	}
	if err != nil {
		// Only a resolution or validation failure can be explained by a file the
		// tolerant load refused, so only those are annotated: a deprecation halt
		// carrying a note about skipped catalog files would name bystanders as the
		// cause.
		if compilerops.IsResolutionFailure(err) && cfg.AnnotateResolutionFailure != nil {
			return Result{}, cfg.AnnotateResolutionFailure(err, skipped)
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

	// The graph this build resolved, handed back before anything else reaches the
	// status stream — ahead of the dry run's artifact report and ahead of the
	// completion summary the caller writes when Run returns. A build already holds
	// the graph it rendered from, so handing it back costs no second resolution
	// and cannot disagree with the artifacts written below.
	if err := run.irGraph(result.IRGraph); err != nil {
		return Result{}, err
	}

	outDir := run.OutDir
	if outDir == "" {
		outDir = artifactout.DefaultOutputDir
	}

	// A dry run reports the render and leaves the output directory alone —
	// including the clean, which is itself a write: a dry run that emptied
	// boundaries/ and wrote nothing back would have destroyed the previous build's
	// output in order to show what the next one would contain.
	var summary string
	if run.DryRun {
		artifactout.WriteDryRunReport(out, outDir, result.Artifacts)
		summary = fmt.Sprintf("Dry run: %d files would be written to %s/; nothing was written", len(result.Artifacts), outDir)
	} else {
		// Write output. The boundaries subtree from a prior build of a different
		// variant is cleared as part of the write, so GitOps consumers see exactly
		// what the compiler just produced.
		if err := artifactout.WriteBuildOutput(outDir, result.Artifacts); err != nil {
			return Result{}, fmt.Errorf("write output: %w", err)
		}
		summary = fmt.Sprintf("Build completed: %d files written to %s/", len(result.Artifacts), outDir)
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
		if err := os.RemoveAll(".trace"); err != nil {
			return Result{}, fmt.Errorf("clean .trace: %w", err)
		}
		if err := artifactout.WriteAll(".trace", result.Trace); err != nil {
			return Result{}, fmt.Errorf("write trace: %w", err)
		}
	}

	// Persist newly-pinned external inputs into the lockfile after the
	// build succeeded. The service's full record view is the canonical
	// URL-deduplicated merge of the lockfile's pinned view with this run's
	// acquisitions; the canonical writer keeps the file byte-deterministic.
	// A run that pinned nothing leaves the lockfile untouched.
	//
	// A dry run needs no guard here, and this is the stronger case rather than the
	// same argument again: acquisition is off, so the service refuses an unpinned
	// reference instead of pinning one, and the acquired set is empty by
	// construction. The write is unreachable because nothing can have been
	// acquired — not because a flag was checked.
	acquired := extInputs.AcquiredRecords()
	if len(acquired) > 0 {
		lf.ExternalInputs = extInputs.Records()
		if err := lockfile.WriteLockfilePreservingTimestamp(lockfile.PathFor(releasePath), lf); err != nil {
			return Result{}, fmt.Errorf("write lockfile external inputs: %w", err)
		}
	}
	if run.OnAcquired != nil {
		run.OnAcquired(acquired)
	}

	return Result{Summary: summary}, nil
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

// newExternalInputsService constructs the per-build external-input gate over
// the workspace artifact cache's inputs store, seeded with the lockfile's
// pinned view. Acquisition is on unless the caller froze the closure. The HTTP
// client comes from the network-egress gate; it is nil for an offline workspace,
// so pinned inputs are still served from the cache but any fetch (acquisition or
// a missing-blob refill) refuses loudly — offline dominates acquire
// mechanically, without a separate consult.
func newExternalInputsService(cacheDir string, lf *lockfile.Lockfile, noAcquire bool, wsCfg workspaceconfig.Config) *externalinputs.Service {
	httpClient, _ := egress.HTTPClient(wsCfg)
	return externalinputs.NewService(externalinputs.StoreRoot(cacheDir), !noAcquire, httpClient, lf.ExternalInputs)
}

// ensureLockfile ensures the per-Release lockfile exists before the build wires
// its lazy lookup + runs closure-scoped Pass-2, returning whether it PRE-EXISTED.
// Build is read-only by default: a present lockfile is left as-is — drift
// against it is verified later against the resolved closure — and a missing one
// halts with the pull-first remediation, writing no lockfile or source pins.
// When the pull step is requested — the caller's AutoPull knob or workspace
// auto_pull — the fill runs in-process before the build proceeds whether or not
// one is present, honoring existing pins and resolving what has none; Upgrade
// makes it a full refresh, floating every dependency to the highest satisfying
// version instead. Under offline a missing lockfile always halts, regardless of
// the pull step, because the fill itself needs network access.
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
func ensureLockfile(cfg Config, wsCfg workspaceconfig.Config, run RunOptions, diagOut, out io.Writer) (bool, error) {
	releasePath := cfg.ReleasePath
	lockfilePath := lockfile.PathFor(releasePath)

	_, statErr := os.Stat(lockfilePath)
	preexisted := statErr == nil
	if statErr != nil && !stderrors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("lockfile stat %s: %w", lockfilePath, statErr)
	}

	// Offline: never network. Build is frozen against whatever lockfile exists;
	// a missing one cannot be filled here. The offline decision is read from the
	// egress gate (the sole home of that decision), not from the config field
	// directly, so this pre-flight and the actual fill cannot diverge.
	if _, egErr := egress.RoundTripper(wsCfg); stderrors.Is(egErr, egress.ErrOfflineEgress()) {
		if preexisted {
			return true, nil
		}
		return false, fmt.Errorf("aggrega build: lockfile %s missing and workspace is offline; run `aggrega pull --release %s` from a connected environment first: %w",
			lockfilePath, releasePath, oci.ErrFetchNotFound())
	}

	// build's auto-pull is the opt-in fill: it runs when the caller's AutoPull knob
	// or workspace.auto_pull is set. By default (upgrade off) it is lockfile-honoring
	// — it honors every existing pin (resolving to the pinned version, verifying the
	// pinned digest, halting on a drifted pin), resolves + adds un-pinned references,
	// refills an empty cache by pin, and prunes stale pins; on a complete,
	// cache-populated lockfile it is a no-op with NO network (every pin a Tier-2 hit),
	// so the build stays hermetic given a complete lockfile. With upgrade the fill
	// is a full refresh: it discards the existing pin set and re-resolves every
	// dependency, floating each to the highest satisfying version. There is no
	// absent-vs-partial special case: any incomplete/empty state runs the same fill,
	// and lockfile.Verify (in the preview's gate sequence) is the post-condition. The
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
	if !run.DryRun && (run.AutoPull || wsCfg.AutoPull) {
		if cfg.Pull == nil {
			return false, fmt.Errorf("internal: no dependency fill supplied — a build that fills the lockfile needs the caller's pull operation")
		}
		if err := cfg.Pull(run.Upgrade, run.Detail, diagOut, out); err != nil {
			return false, fmt.Errorf("auto-pull: %w", err)
		}
		return false, nil
	}

	// Read-only default: the build is frozen against the lockfile as it stands and
	// never writes source pins. A present lockfile is used as-is; a missing one halts
	// with the pull-first remediation.
	if preexisted {
		if run.Detail {
			fmt.Fprintf(out, "aggrega build --detail: resolution detail is a pull-time surface; lockfile %s already present, so no pull ran this build\n", lockfilePath)
		}
		return true, nil
	}
	return false, fmt.Errorf("aggrega build: lockfile %s missing; run `aggrega pull --release %s` first to create it, or pass --auto-pull (or enable auto_pull) to fill it during the build",
		lockfilePath, releasePath)
}
