// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
)

// buildFlags carries the `build` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values. It is distinct from buildops.RunOptions and buildDiagOptions, which
// carry the post-parse knobs the dispatch itself consumes.
type buildFlags struct {
	releasePath             string
	catalogDirs             []string
	traceEnabled            bool
	traceUnredacted         bool
	detail                  bool
	quiet                   bool
	progress                bool
	debug                   bool
	disablePluginCache      bool
	verifyDeterminism       bool
	autoPull                bool
	upgrade                 bool
	noUnsupportedFieldWarns bool
	dryRun                  bool
	irGraph                 bool
	unredacted              bool
	assumeYes               bool
}

// Every command that renders offers the unsupported-field warning filter, so the
// flag's name and help text are declared once here and shared by each
// registration — two literals would be free to drift apart.
const (
	flagNoUnsupportedFieldWarnings = "no-unsupported-field-warnings"

	helpNoUnsupportedFieldWarnings = "Silence the unsupported-field warning class: the notices a target raises for something you authored that it cannot honor — a Workload field it cannot express (as a lower-fidelity target such as compose does), or an aggrega.ai/ annotation it does not act on for the node's input kind. Other render warnings still surface. Display-only; does not change the rendered output"
)

// helpTraceUnredacted names the replacement text by calling for it rather than
// spelling it, so the help a user reads and the text the trace actually writes
// cannot diverge.
var helpTraceUnredacted = "Write the declared-sensitive values into the .trace/ files instead of the " +
	redact.Placeholder() +
	" text that stands in for them, and stamp every file the trace writes with a header saying so. Requires --trace. It reaches .trace/ and nothing else: rendered artifacts, aggrega inspect output and diagnostic text are identical with and without it. Invocation-only — there is no workspace-config setting for it, so it can never be left on for a later build"

func newBuildCmd() *cobra.Command {
	opts := &buildFlags{}
	c := &cobra.Command{
		Use:   "build",
		Short: "Compile an Aggregate and Release into provider-native artifacts",
		Long: `Compile an Aggregate and Release pair into provider-native artifacts under each
boundary's output directory.

The Release is the single entry point: it names the Aggregate and the Manifest,
declares the boundaries, and routes each component to a provider. What a
provider emits is the provider's own shape — Helm manifests, a redistributable
Helm chart, docker-compose YAML — so the output directory holds whatever the
providers this Release routes to produce.

Build is read-only by default and needs a lockfile. Run "aggrega pull" first to
create one, or pass --auto-pull to fill it in-process for this build.

Given identical input, build writes byte-identical output, which is what makes
the rendered tree reviewable as a diff.`,
		Example: `  # Build a Release that already has a lockfile
  aggrega build --release releases/production.yaml

  # First build in a fresh clone: fill the lockfile in-process, then build
  aggrega build --release releases/production.yaml --auto-pull

  # Build against a project-local catalog alongside the configured ones
  aggrega build --release releases/production.yaml --catalog ./catalog

  # Confirm the providers render deterministically
  aggrega build --release releases/production.yaml --verify-determinism`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(cmd, args, opts)
		},
	}
	c.Flags().StringVar(&opts.releasePath, "release", "", helpReleasePath)
	c.Flags().StringArrayVar(&opts.catalogDirs, "catalog", nil, helpCatalogDir)
	c.Flags().BoolVar(&opts.traceEnabled, "trace", false, "write per-node intermediate files to .trace/ (sibling to dist/) for post-mortem inspection")
	c.Flags().BoolVar(&opts.traceUnredacted, "trace-unredacted", false, helpTraceUnredacted)
	c.Flags().BoolVar(&opts.detail, "detail", false, "Build is read-only by default and prints no resolution detail. When the pull step runs — because --auto-pull was passed or the workspace enables auto_pull — build fills the lockfile in-process whether or not one is already present, and this flag prints per resolved entity what that fill did: fetched fresh from a registry, reused a cache slot, found in a local catalog, served from inside the compiler binary, or external (the Release's own Aggregate or Manifest, read from the file or entry the Release names). When no pull step runs, it prints one line saying so instead of the per-entity rows. Stdout only; not persisted")
	c.Flags().BoolVar(&opts.quiet, "quiet", false, "Silence the build-progress surface on stderr. Does not affect stdout. Takes precedence over --progress")
	c.Flags().BoolVar(&opts.progress, "progress", false, "Force the build-progress surface on even when stderr is not a terminal, printing plain static phase lines. The default is auto: the interactive phase checklist is shown only when stderr is a terminal. --quiet and --debug both override --progress")
	c.Flags().BoolVar(&opts.debug, "debug", false, "Emit human-readable verbose diagnostics on stderr — per-phase wall-clock timing and entity/dependency counts. Wins over --quiet: verbose output always emits. Distinct from --trace, which writes machine-readable per-node intermediate files to .trace/")
	c.Flags().BoolVar(&opts.noUnsupportedFieldWarns, flagNoUnsupportedFieldWarnings, false, helpNoUnsupportedFieldWarnings)
	c.Flags().BoolVar(&opts.disablePluginCache, "disable-plugin-cache", false, "Skip the persistent on-disk plugin compile cache for this build and compile plugin guests fresh in an in-memory cache that is discarded when the build ends. Off by default, which reuses the shared on-disk cache across builds. Does not change rendered output")
	c.Flags().BoolVar(&opts.verifyDeterminism, "verify-determinism", false, "Render every node twice and compare the emitted bytes, halting the build if any node's two renders differ. Off by default. Use it to confirm a provider renders deterministically — identical input yields byte-identical output — which the GitOps-diff and byte-identical-output guarantees rely on")
	c.Flags().BoolVar(&opts.autoPull, "auto-pull", false, "Run the pull step before building: resolve the Release's dependencies into the lockfile and cache in-process, then build against them. This is the same in-process fill the workspace auto_pull setting performs, turned on for a single build without editing workspace config. On its own it is an incremental fill — existing pins are honored, and references with no pin are resolved and added")
	c.Flags().BoolVar(&opts.upgrade, "upgrade", false, "Make the auto-pull step a full refresh: discard the existing lockfile and re-resolve every dependency from current registry state, floating each to the highest available version instead of honoring existing pins. Implies --auto-pull — on its own it runs a full-refresh pull before the build. It takes no target, so it cannot be narrowed to one dependency: to adopt or drop a single pin, run `aggrega pull --upgrade --release <release> <uri>` instead, which holds every other pin unchanged. Because it drops every recorded pin it asks for confirmation first; --yes answers in advance, and on a non-interactive stdin it refuses rather than waiting for an answer nobody can give")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Render the Release and print what it would emit instead of writing it: each artifact's would-be path and its content go to stdout, and the output directory and the lockfile are left untouched. External inputs behave exactly as they do in an ordinary build — pinned inputs are served from the content-addressed store (and refilled by digest when the blob is missing), and a render that requests an unpinned external URL halts naming it — so what a dry run shows is what a build would write. Refuses to combine with --auto-pull, --upgrade or --trace, each of which writes — run the dependency fill, or a traced build, as its own step")
	c.Flags().BoolVar(&opts.irGraph, flagIRGraph, false, helpIRGraph+". The artifacts are still written and the summary is still printed; the graph is written ahead of both")
	c.Flags().BoolVar(&opts.unredacted, flagUnredacted, false, helpUnredacted+" Reaches the graph --"+flagIRGraph+" writes and nothing else, so it requires --"+flagIRGraph+"; the .trace/ files take --trace-unredacted instead")
	c.Flags().BoolVar(&opts.assumeYes, "yes", false, "Answer the --upgrade confirmation in advance, for non-interactive use. It reaches that one prompt and nothing else: a build that does not refresh asks nothing, so passing it changes no behaviour there")
	if err := c.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	return c
}

// runBuild is the cobra entry point. It reads the command's own flag state and
// delegates to dispatchBuildWithDiag, which drives the shared build
// orchestration in internal/buildops and renders its progress, advisories and
// completion summary in this command's voice.
func runBuild(cmd *cobra.Command, args []string, opts *buildFlags) error {
	// A flag-pairing fault is answered before anything is asked or touched,
	// the confirmation below included: a question put ahead of a refusal of the
	// same invocation is a question whose answer is thrown away.
	if err := refuseUnpairedUnredacted(opts.irGraph, opts.unredacted); err != nil {
		return err
	}
	// Build's --upgrade takes no target, so every use of it is the destructive
	// form the confirmation guards — the same act `aggrega pull --upgrade` bare
	// performs, and gated on the same terms. The dry-run leg is not a second copy
	// of the flag-conflict rule (which is the shared layer's and stays there): a
	// dry run performs no refresh, so there is simply nothing here to confirm, and
	// asking before reporting that the two flags conflict would put the question
	// ahead of the answer.
	if opts.upgrade && !opts.dryRun {
		if err := confirmDestructiveRefresh(cmd.OutOrStdout(), cmd.InOrStdin(), "aggrega build", opts.assumeYes); err != nil {
			return err
		}
	}
	rw, err := workspaceFromContext(cmd.Context())
	if err != nil {
		return err
	}
	// The graph shares the stream the build's own output already uses, named once
	// here so the two cannot come to write to different places.
	out := os.Stdout
	return dispatchBuildWithDiag(cmd.Context(), rw, opts.releasePath, opts.catalogDirs, opts.traceEnabled,
		buildDiagOptions{quiet: opts.quiet, debug: opts.debug, progress: opts.progress, noColor: globalDisplay.colorOff, plain: globalDisplay.plain},
		buildRunOptions(opts, out), os.Stderr, out)
}

// buildRunOptions composes the shared build layer's knobs from this command's
// flag state, including the graph hand-back when the graph was asked for.
//
// The graph goes to out, the stream the build's own output goes to, and through
// the one writer every command that offers the document shares. Whether it is
// withheld is the reader's --unredacted, passed through as set: the values an
// author declared sensitive are withheld unless that flag asks for them. The
// .trace/ files carry a reveal of their own, which reaches them and nothing else.
//
// It is a function of the flag state rather than inline in runBuild so a test
// can drive the same composition the command performs — the flag reaching the
// writer is the wiring under test, and a test that assembled the hand-back
// itself would pass while the command ignored the flag.
func buildRunOptions(opts *buildFlags, out io.Writer) buildops.RunOptions {
	run := buildops.RunOptions{Detail: opts.detail, DisablePluginCache: opts.disablePluginCache, VerifyDeterminism: opts.verifyDeterminism, AutoPull: opts.autoPull, Upgrade: opts.upgrade, SuppressUnsupportedFieldWarnings: opts.noUnsupportedFieldWarns, TraceUnredacted: opts.traceUnredacted, DryRun: opts.dryRun}
	if opts.irGraph {
		unredacted := opts.unredacted
		run.IRGraph = func(graph *types.IRGraph) error {
			return writeIRGraph(out, "build", graph, unredacted)
		}
	}
	return run
}

// dispatchBuild is the silent-diagnostics form of dispatchBuildWithDiag: it runs
// with no phase-progress surface and no verbose debug layer. Tests call it
// directly, passing the workspace and inputs they declared, so a test body reads
// no state it did not supply and stays race-free under t.Parallel; the CLI entry
// point calls dispatchBuildWithDiag to thread the operator's --quiet / --debug
// choices. Both share the single implementation below.
func dispatchBuild(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, diagOut, out io.Writer) error {
	return dispatchBuildWithDiag(ctx, rw, releasePath, catalogFlagDirs, traceEnabled, buildDiagOptions{}, buildops.RunOptions{}, diagOut, out)
}

// dispatchBuildWithDiag runs the shared build orchestration and renders it as
// this command. What the shared layer owns is the work: the catalog load, the
// lockfile step, the external-input gate, the gated preview, and the artifact,
// trace and lockfile writes. What stays here is what a terminal surface owes —
// the phase checklist, the advisory voice, the failure annotation, and the
// completion summary's layout.
//
// Per-step diagnostics are written to diagOut (os.Stderr in production); the
// final success summary is written to out (os.Stdout in production). Tests pass
// buffers (or os.Stderr/os.Stdout) to capture or route the output without
// mutating a process global.
//
// diag carries the operator's stderr-diagnostics choices. The per-phase progress
// surface and the verbose debug layer both write to diagOut only; stdout carries
// only the final success summary, so the compiler's byte-deterministic rendered
// output is invariant with respect to the diagnostic shape.
//
// run carries the non-diagnostic dispatch knobs: the external-input acquisition
// switch, the output-directory override, the acquired-pin callback the acquire
// command reports through, and the graph hand-back a caller that asked for the
// document supplies. The zero value is the plain build shape.
//
// ctx is the run's own context, taken from the command rather than started here,
// so a reader who asks the process to stop ends the build's work and not merely
// the process. It reaches both the cache probe below and the shared build layer.
func dispatchBuildWithDiag(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, diag buildDiagOptions, run buildops.RunOptions, diagOut, out io.Writer) error {
	// Wall-clock start of the whole build run, sourced independently of the
	// progress reporter (whose clock is unset in silent mode and only spans the
	// progress phases) so the summary's "Build time" spans the entire dispatch.
	buildStart := time.Now()
	// Cold/warm plugin-compile-cache guess, up front: it picks the progress bar's
	// weight profile only. The visible "Plugin compilation" step is revealed
	// separately, from whether a guest is observed to actually compile during the
	// build, so a stale-cache rebuild that recompiles still shows it. This guess
	// is detected before the build runs so the bar's calibration is fixed for
	// the whole run. --disable-plugin-cache forces the cold profile.
	coldCache := wasm.PluginCompileCacheCold(wasm.ContextWithDisablePluginCache(ctx, run.DisablePluginCache))
	progress := newProgressReporter(diagOut, diag, buildProgressSpine(coldCache))
	defer progress.finish()

	result, err := buildops.Run(ctx, buildDispatchConfig(rw, releasePath, catalogFlagDirs, traceEnabled, run, progress, diagOut, out))
	if err != nil {
		return err
	}

	// Clear the in-place progress line (and, in debug mode, print the timing
	// total) before the stdout summary so the progress surface never lingers
	// beside the final output. The deferred finish is idempotent.
	progress.finish()
	fmt.Fprintf(out, "%s\n", formatTotalBuildTimeLine(formatPhaseElapsed(time.Since(buildStart))))
	fmt.Fprintln(out, result.Summary)
	return nil
}

// buildDispatchConfig assembles the shared build layer's input contract from
// this command's state: the flag-derived knobs, the two output sinks, this
// binary's own build identity, the pull the auto-pull step runs, and the
// progress and advisory rendering the terminal surface owns.
//
// progress may be nil, which reports nothing — the shape the acquisition command
// uses, whose own report is the only thing a reader is meant to see.
func buildDispatchConfig(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, run buildops.RunOptions, progress *progressReporter, diagOut, out io.Writer) buildops.Config {
	return buildops.Config{
		Workspace:       rw,
		ReleasePath:     releasePath,
		CatalogDirs:     catalogFlagDirs,
		TraceEnabled:    traceEnabled,
		CompilerVersion: provenanceCompilerVersion(),
		Options:         run,
		Report: buildops.Reporter{
			Phase:                  func(p buildops.Phase) { progress.phase(buildPhaseStep(p)) },
			Note:                   progress.note,
			RenderNode:             progress.renderNode,
			PluginCompileBegin:     progress.pluginCompileBegin,
			PluginCompileGuestDone: progress.pluginCompileGuestDone,
			PluginCompileRunning:   progress.pluginCompileRunning,
			CatalogShadows:         func(cat *catalog.Catalog) { reportCatalogShadows(cat, diagOut) },
			Advisories:             func(preview *compilerops.PreviewResult) { writePreviewWarnings(preview, diagOut) },
			DryRunDivergence: func(declined []string, release string) {
				writeDryRunDivergenceNote(declined, release, diagOut)
			},
		},
		// The auto-pull step runs the pull command's own path verbatim, with the
		// progress surface silenced so only the build's own surface shows during an
		// implicit pull. The flag dirs (not the resolved list) are forwarded so the
		// pull re-derives the identical stack with the same config.
		//
		// The fill discovers and pins the external inputs, because the build's own
		// render will not: a build renders the closure the lockfile records. Finding
		// out which remote references a Release consumes is itself a render, so the
		// fill renders the Release the build was about to render — under the build's
		// own context and option set, which arrive here and travel straight through.
		// That render comes back and the build presents it, so a build that fills its
		// own lockfile still renders the Release exactly once.
		Pull: func(ctx context.Context, upgrade, detail bool, render pipeline.Options, pullDiagOut, pullOut io.Writer) (*compilerops.PreviewResult, error) {
			res, err := pullRunWithResult(ctx, pullConfig{ReleasePath: releasePath, CatalogDirs: catalogFlagDirs, Upgrade: upgrade, Detail: detail, ws: rw, DiagOut: pullDiagOut, Out: pullOut, renderOptions: render, diag: buildDiagOptions{quiet: true}})
			if err != nil {
				return nil, err
			}
			// Only a render that FINISHED is handed over. A discovery that stopped
			// part way holds findings worth reporting and no artifacts, and a build
			// handed one would write out a tree missing whatever the render never
			// reached. The question is asked of the render's own verdict rather than
			// of the resolution's separate report of what stopped it, so there is one
			// answer rather than two that agree today; and the accessor is safe on a
			// resolution that never reached a discovery at all, which answers the same
			// way for the same reason — there is no render.
			//
			// Handing back nothing leaves the build to render for itself, which is
			// what it does today on that path and what it must keep doing: the pins
			// the fill did land are on disk, and a reference the fill never reached is
			// still unpinned, so the build's own render halts naming it.
			if res.DiscoveryRender.PipelineOutcome().Completed() == nil {
				return nil, nil
			}
			return res.DiscoveryRender, nil
		},
		// The workspace's account of where it reads local entities travels with the
		// refusals, because the two explain different halves of "nothing resolved":
		// a refused file says a document was read and rejected, and the account says
		// which directories were read at all. The hook is handed a closure rather
		// than the shared function itself so the account comes from the workspace
		// this command resolved, which is the one the reader is standing in.
		AnnotateResolutionFailure: func(err error, skipped []catalog.SkippedEntity) error {
			return compilerops.AnnotateSkipContext(err, skipped, rw.LocalCatalogAdviceNote(releasePath))
		},
		DiagOut: diagOut,
		Out:     out,
	}
}

// buildPhaseStep maps a stage of the shared build orchestration onto this
// command's progress spine. The two are deliberately separate vocabularies: the
// spine carries layout the shared layer knows nothing about, and the shared
// layer names stages a surface is free to draw as one row or several.
func buildPhaseStep(p buildops.Phase) int {
	switch p {
	case buildops.PhaseLoadCatalog:
		return phaseLoadCatalog
	case buildops.PhaseLockfile:
		return phaseLockfile
	case buildops.PhaseClosure:
		return phaseClosure
	case buildops.PhaseValidate:
		return phaseValidate
	case buildops.PhaseInheritance:
		return phaseInheritance
	case buildops.PhaseRender:
		return phaseRender
	}
	return phaseClosure
}

// writePreviewWarnings renders the preview's advisories in this command's voice,
// in the order the preview raised them. Each class gets the prefix the terminal
// reader already recognises for it; the classes exist so the rendering is a
// choice made here rather than text the shared layer pre-formatted for one
// surface's terminal.
//
// A nil result writes nothing: a preview that failed before it could raise
// anything has nothing to say.
func writePreviewWarnings(preview *compilerops.PreviewResult, diagOut io.Writer) {
	if preview == nil {
		return
	}
	for _, w := range preview.Warnings {
		switch w.Class {
		case compilerops.WarningExtends:
			fmt.Fprintf(diagOut, "  extends: %s\n", w.Text)
		case compilerops.WarningCatalogLint:
			fmt.Fprintf(diagOut, "  notice: %s\n", w.Text)
		case compilerops.WarningReleaseComposition:
			fmt.Fprintf(diagOut, "  release composition: %s\n", w.Text)
		case compilerops.WarningCatalog:
			fmt.Fprintf(diagOut, "  catalog: %s\n", w.Text)
		default:
			fmt.Fprintf(diagOut, "  deprecation: %s\n", w.Text)
		}
	}
}

// writeDryRunDivergenceNote names every external reference a dry run declined
// for want of a pin, so the reader sees the whole set rather than only the one
// the render stopped on. The references are named from the record of what was
// declined, not read out of a diagnostic.
//
// The note goes to the diagnostic stream beside the failure rather than being
// wrapped around it, because the diagnostic formatter renders a failure from the
// outermost categorised envelope it can reach and discards the frames above it;
// text wrapped on here would reach the reader only while the failure happened to
// carry no envelope.
//
// It is reached only for a dry run that declined something. An ordinary build
// meeting the same reference halts on it identically — no build's render
// acquires — so this adds the full set and the remedy rather than reporting a
// difference between the two.
func writeDryRunDivergenceNote(declined []string, releasePath string, diagOut io.Writer) {
	if len(declined) == 0 {
		return
	}
	fmt.Fprint(diagOut, "  note: the lockfile does not pin every external reference this render requested, so the following were declined and the render stopped short of the artifacts it would otherwise emit:\n")
	for _, url := range declined {
		fmt.Fprintf(diagOut, "    - %s\n", url)
	}
	fmt.Fprintf(diagOut, "  Run `aggrega pull --release %s` to pin the inputs the render needs, then run the dry run again\n", releasePath)
}

// formatTotalBuildTimeLine renders the successful-build summary's total line with
// its value right-aligned so the value's last character sits in the same right-
// hand column the per-phase checklist elapsed times occupy — the two share a
// right edge. The column is the checklist row body width plus its elapsed field
// width, so the alignment tracks the checklist layout rather than a fixed number.
// A value long enough to reach that column falls back to a single separating
// space rather than a negative pad.
func formatTotalBuildTimeLine(value string) string {
	const label = "Total build time:"
	pad := rowBodyColumn + elapsedFieldWidth - len(label) - len(value)
	if pad < 1 {
		pad = 1
	}
	return label + strings.Repeat(" ", pad) + value
}
