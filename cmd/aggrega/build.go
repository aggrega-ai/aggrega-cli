// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
)

// The external-input store is what the build injects as the wasm
// loader's fetch seam; the assertion keeps the two packages' contracts
// aligned at compile time.
var _ wasm.FetchService = (*externalinputs.Service)(nil)

// buildFlags carries the `build` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values. It is distinct from buildRunOptions and buildDiagOptions below, which
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
	noAcquireInputs         bool
	disablePluginCache      bool
	verifyDeterminism       bool
	autoPull                bool
	upgrade                 bool
	noUnsupportedFieldWarns bool
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
		Short: "Compile Aggregate + Release into Helm artifacts via the 5-step pipeline",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuild(cmd, args, opts)
		},
	}
	c.Flags().StringVar(&opts.releasePath, "release", "", "Path to Release YAML file (required)")
	c.Flags().StringArrayVar(&opts.catalogDirs, "catalog", nil, "Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	c.Flags().BoolVar(&opts.traceEnabled, "trace", false, "write per-node intermediate files to .trace/ (sibling to dist/) for post-mortem inspection")
	c.Flags().BoolVar(&opts.traceUnredacted, "trace-unredacted", false, helpTraceUnredacted)
	c.Flags().BoolVar(&opts.detail, "detail", false, "Build is read-only by default and prints no resolution detail. When workspace auto_pull is enabled and no lockfile is present, build fills the lockfile in-process; this flag then prints per resolved entity what that fill did — fetched fresh from a registry, reused a cache slot, found in a local catalog, served from inside the compiler binary, or external. Stdout only; not persisted. No-op when a lockfile already exists (no fill this build)")
	c.Flags().BoolVar(&opts.quiet, "quiet", false, "Silence the build-progress surface on stderr. Does not affect stdout. Takes precedence over --progress")
	c.Flags().BoolVar(&opts.progress, "progress", false, "Force the build-progress surface on even when stderr is not a terminal, printing plain static phase lines. The default is auto: the interactive phase checklist is shown only when stderr is a terminal. --quiet and --debug both override --progress")
	c.Flags().BoolVar(&opts.debug, "debug", false, "Emit human-readable verbose diagnostics on stderr — per-phase wall-clock timing and entity/dependency counts. Wins over --quiet: verbose output always emits. Distinct from --trace, which writes machine-readable per-node intermediate files to .trace/")
	c.Flags().BoolVar(&opts.noAcquireInputs, "no-acquire-inputs", false, "Freeze the external-input set: a render that requests an unpinned external URL halts with a diagnostic instead of fetching and pinning it. Pinned inputs are still served from the content-addressed store (and refilled by digest when the blob is missing)")
	c.Flags().BoolVar(&opts.noUnsupportedFieldWarns, flagNoUnsupportedFieldWarnings, false, helpNoUnsupportedFieldWarnings)
	c.Flags().BoolVar(&opts.disablePluginCache, "disable-plugin-cache", false, "Skip the persistent on-disk plugin compile cache for this build and compile plugin guests fresh in an in-memory cache that is discarded when the build ends. Off by default, which reuses the shared on-disk cache across builds. Does not change rendered output")
	c.Flags().BoolVar(&opts.verifyDeterminism, "verify-determinism", false, "Render every node twice and compare the emitted bytes, halting the build if any node's two renders differ. Off by default. Use it to confirm a provider renders deterministically — identical input yields byte-identical output — which the GitOps-diff and byte-identical-output guarantees rely on")
	c.Flags().BoolVar(&opts.autoPull, "auto-pull", false, "Run the pull step before building: resolve the Release's dependencies into the lockfile and cache in-process, then build against them. This is the same in-process fill the workspace auto_pull setting performs, turned on for a single build without editing workspace config. On its own it is an incremental fill — existing pins are honored, and references with no pin are resolved and added")
	c.Flags().BoolVar(&opts.upgrade, "upgrade", false, "Make the auto-pull step a full refresh: discard the existing lockfile and re-resolve every dependency from current registry state, floating each to the highest available version instead of honoring existing pins. Implies --auto-pull — on its own it runs a full-refresh pull before the build")
	if err := c.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	return c
}

// runBuild is the cobra entry point. It reads the command's own flag state and
// delegates to dispatchBuild, which composes catalog loading,
// lockfile verification, and dist/ writing around the in-memory pipeline
// callable (pipeline.RunPipeline). Per-step diagnostic output (extends
// errors, deprecation warnings, guard violations, selector violations,
// render warnings) writes to stderr inline; the returned error is a
// summary suitable for cobra display.
//
// On success, dist/boundaries/ is cleaned (stale files from prior
// builds don't leak into this one, preserving GitOps byte-identity)
// and the rendered files are written to dist/. A summary line writes
// to stdout.
func runBuild(cmd *cobra.Command, args []string, opts *buildFlags) error {
	rw, err := workspaceFromContext(cmd.Context())
	if err != nil {
		return err
	}
	return dispatchBuildWithDiag(rw, opts.releasePath, opts.catalogDirs, opts.traceEnabled,
		buildDiagOptions{quiet: opts.quiet, debug: opts.debug, progress: opts.progress, noColor: globalDisplay.colorOff, plain: globalDisplay.plain},
		buildRunOptions{detail: opts.detail, noAcquireInputs: opts.noAcquireInputs, disablePluginCache: opts.disablePluginCache, verifyDeterminism: opts.verifyDeterminism, autoPull: opts.autoPull, upgrade: opts.upgrade, suppressUnsupportedFieldWarnings: opts.noUnsupportedFieldWarns, traceUnredacted: opts.traceUnredacted}, os.Stderr, os.Stdout)
}

// buildRunOptions carries the build-dispatch knobs that are not stderr
// diagnostics. The zero value is the plain `aggrega build` shape:
// acquisition on, rendered output to dist/, no acquisition report.
type buildRunOptions struct {
	// noAcquireInputs freezes the external-input closure: a render that
	// requests an unpinned external URL halts with a diagnostic instead
	// of fetching and pinning it.
	noAcquireInputs bool
	// disablePluginCache skips the persistent on-disk plugin compile cache
	// for this build, compiling guests fresh in an in-memory cache that is
	// discarded when the build ends. It changes only compilation cost, not
	// rendered output.
	disablePluginCache bool
	// verifyDeterminism renders every node twice and compares the emitted
	// bytes, halting the build on any divergence. It changes only whether the
	// determinism check runs, not the rendered output of a deterministic build.
	verifyDeterminism bool
	// autoPull runs the pull step before the build (the same in-process fill the
	// workspace auto_pull setting performs), turning it on for a single build.
	// Effective when this flag OR workspace auto_pull is set.
	autoPull bool
	// detail prints, per resolved entity, what the auto-pull fill did, and reports
	// which entities a higher-precedence catalog directory shadowed. Display-only:
	// it changes only what is written, never the rendered output or the lockfile.
	// The resolution report goes to stdout, which is what the flag's help promises
	// — the per-entity rows, and the note saying so when no fill ran. The shadow
	// report is a catalog-precedence warning rather than resolution detail, and
	// stays on the diagnostic stream with the other warnings.
	detail bool
	// upgrade makes the auto-pull step a full refresh: the pull discards the
	// existing pin set and re-resolves every dependency, floating each to the
	// highest available version rather than honoring existing pins. It implies the
	// auto-pull step — requesting it turns the pull step on when neither the flag
	// nor workspace auto_pull is set.
	upgrade bool
	// traceUnredacted writes the declared-sensitive values into the trace files
	// rather than the text that stands in for them, and stamps each of those
	// files with a header saying so. It is meaningful only alongside the trace
	// itself: requesting it without the trace halts, because a security-relevant
	// switch that quietly does nothing is the worst of the available outcomes.
	// It reaches the trace and nothing else — the inspect layouts and the
	// diagnostic bodies are identical in both states.
	traceUnredacted bool
	// suppressUnsupportedFieldWarnings silences the unsupported-field warning class on
	// the render-warning surface — the per-field notices a lower-fidelity target
	// emits for authored fields it cannot honor. Display-only; other render
	// warnings still surface and the rendered output is unchanged.
	suppressUnsupportedFieldWarnings bool
	// outDir overrides the rendered-artifact output directory; empty
	// renders to dist/. The acquire command points it at a discard
	// directory so an acquisition pass never touches dist/.
	outDir string
	// onAcquired, when non-nil, runs after a successful build with the
	// external-input records this run newly pinned (empty when the run
	// pinned nothing). The acquire command reports through it.
	onAcquired func([]lockfile.ExternalInput)
}

// dispatchBuild is the silent-diagnostics form of dispatchBuildWithDiag: it runs
// with no phase-progress surface and no verbose debug layer. Tests call it
// directly, passing the workspace and inputs they declared, so a test body reads
// no state it did not supply and stays race-free under t.Parallel; the CLI entry
// point calls dispatchBuildWithDiag to thread the operator's --quiet / --debug
// choices. Both share the single implementation below.
func dispatchBuild(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, diagOut, out io.Writer) error {
	return dispatchBuildWithDiag(rw, releasePath, catalogFlagDirs, traceEnabled, buildDiagOptions{}, buildRunOptions{}, diagOut, out)
}

// dispatchBuildWithDiag composes the filesystem-touching pre-pipeline steps
// (catalog load, lockfile verify-or-auto-pull, extends resolution, deprecation
// surface, raw Aggregate read, workspace config load) around
// pipeline.RunPipeline, then writes the rendered Artifacts to dist/ and (when
// --trace is on) the Trace map to .trace/. The in-memory pipeline orchestration
// lives in internal/compiler/pipeline.
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
// switch, the output-directory override, and the acquired-pin callback the
// acquire command reports through. The zero value is the plain build shape.
func dispatchBuildWithDiag(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, diag buildDiagOptions, run buildRunOptions, diagOut, out io.Writer) error {
	// Flag-pairing halt, before any work: an unredacted trace with no trace to
	// write is a no-op, and a no-op is the one outcome a switch about withheld
	// values must not have — an operator who asked for the values and got a build
	// that silently ignored them has no signal that they are still withheld. The
	// check sits here rather than in the cobra entry point so it covers every
	// caller of this dispatch, not only the command line.
	if run.traceUnredacted && !traceEnabled {
		return fmt.Errorf("aggrega build: --trace-unredacted needs --trace; there is no trace to write without it. Pass both to write an unredacted trace, or neither")
	}

	// Wall-clock start of the whole build run, sourced independently of the
	// progress reporter (whose clock is unset in silent mode and only spans the
	// progress phases) so the summary's "Build time" spans the entire dispatch.
	buildStart := time.Now()
	// Cold/warm plugin-compile-cache guess, up front: it picks the progress bar's
	// weight profile only. The visible "Plugin compilation" step is revealed
	// separately, from whether a guest is observed to actually compile during the
	// build, so a stale-cache rebuild that recompiles still shows it. This guess
	// is detected before the pipeline runs so the bar's calibration is fixed for
	// the whole run. --disable-plugin-cache forces the cold profile.
	coldCache := wasm.PluginCompileCacheCold(wasm.ContextWithDisablePluginCache(context.Background(), run.disablePluginCache))
	progress := newProgressReporter(diagOut, diag, buildProgressSpine(coldCache))
	defer progress.finish()
	// wsRoot is the resolved workspace root; the catalog renders source-file
	// accessors relative to it so the IR's per-node SourceFile records stay
	// byte-identical across developer machines whose workspaces live at
	// different absolute paths.
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
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
	if run.upgrade {
		run.autoPull = true
	}

	cacheDir := wsCfg.ResolvedArtifactCacheDir()

	// Effective first-win catalog search stack: --catalog dirs prepended to the
	// workspace catalog_dirs, defaulting to the current directory.
	dirs := workspaceconfig.ResolveCatalogDirs(catalogFlagDirs, wsCfg, ".")

	// Tolerant Pass-1 catalog load. A file that fails to read, parse, or index is
	// SKIPPED (recorded) rather than aborting the walk — so a broken catalog entity
	// OUTSIDE this Release's closure never fails the build. The closure walk +
	// closure-scoped Pass-2 below validate exactly what the Release reaches;
	// catalog-wide validation is `aggrega lint`'s job. Pass 1 does not expand
	// cross-document $ref pillars, so a reference to a not-yet-resolved remote
	// library does not fail here.
	progress.phase(phaseLoadCatalog)
	cat, skipped, err := catalog.LoadPassOneTolerant(wsRoot, cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	progress.note("catalog search dirs: %d", len(dirs))
	if len(skipped) > 0 {
		progress.note("catalog files skipped at load: %d", len(skipped))
	}

	// When more than one catalog directory is searched, report which entities a
	// higher-precedence directory shadowed (--detail only); the override is never
	// silent.
	if run.detail {
		reportCatalogShadows(cat, diagOut)
	}

	// Ensure the per-Release lockfile exists (auto-pull when missing + permitted),
	// returning whether it pre-existed — drift is verified only for a pre-existing
	// lockfile; a freshly auto-pulled one matches the closure by construction. The
	// flag dirs (not the resolved list) are forwarded so the pull path re-derives
	// the identical stack with the same config.
	progress.phase(phaseLockfile)
	lockfilePreexisted, err := acquireLockfile(rw, releasePath, catalogFlagDirs, wsCfg, run.autoPull, run.upgrade, run.detail, diagOut, out)
	if err != nil {
		return err
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		return fmt.Errorf("load lockfile: %w", err)
	}

	// External-input gate for render-time guest fetches: the service serves
	// every aggrega_fetch_external_input call under the pin discipline —
	// pinned URLs from the content-addressed store, unpinned URLs fetched
	// and pinned when acquisition is on (the default), refused loudly when
	// the closure is frozen (--no-acquire-inputs). Seeded with the
	// lockfile's pinned view; newly-acquired pins merge back after a
	// successful build below.
	extInputs := newExternalInputsService(cacheDir, lf, run.noAcquireInputs, wsCfg)

	// The build's determinism-wired lazy lookup: EVERY dispatchBuild catalog
	// consumer — guards, triad aggregate resolution, manifest-implements gate,
	// closure computation + closure-scoped validation + extends, renderer,
	// topology — resolves the resolved closure through this. No cache→catalog
	// bridge: the lazy lookup reads registry-pulled entities directly from the
	// pinned Tier-2 cache. Nil puller: Tier-3 is disabled, so the build never
	// networks. SetPins threads the lockfile pins so a Tier-2 cache hit is
	// verified-on-read and a miss is a loud halt rather than a silent skip —
	// byte-identical from the lockfile.
	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lazyView.SetPins(catalog.PinsFromLockfile(lf))
	// Loud integrity gate: a pinned cache slot that drifted from its lockfile pin
	// halts here with the integrity diagnostic, before the closure walk — so a
	// tampered/drifted cache surfaces as "re-pull", not a misleading downstream
	// "not resolvable". (Serve keeps its resilient skip; the build, a deploy gate,
	// halts.)
	if err := lazyView.VerifyPinnedIntegrity(); err != nil {
		return fmt.Errorf("lockfile integrity: %w", err)
	}

	// Shared Release→closure orchestration: load the Release, resolve + compose
	// the Manifest, resolve the Aggregate, gate the external seed on a registry
	// pin, and compute + augment the closure once. Every front door runs this same
	// shared resolver, so the sequence cannot drift between them. The resolver runs
	// the manifest-implements consistency gate itself, before the closure walk —
	// the earliest loud point — and hands back its findings; the halt below is this
	// command's verdict on them. Augment folds the cross-document library axes so
	// the closure matches the one drift is verified against. An in-closure entity
	// skipped at tolerant load surfaces as an unresolvable URI; withSkipContext
	// replays WHY it was skipped so the failure is never a bare "not found".
	progress.phase(phaseClosure)
	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            lazyView,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		return withSkipContext(cerr, skipped)
	}
	// A build emits a deployable artifact, so a mapping the catalog contradicts is
	// a halt: rendering a component through a recipe that does not implement its
	// type would produce an artifact nobody asked for. The findings are joined so
	// every inconsistent mapping is reported at once rather than one per re-run.
	if err := stderrors.Join(resolved.ManifestConsistencyFindings...); err != nil {
		return err
	}
	rel := resolved.Release
	manifest := resolved.Manifest
	aggBytes := resolved.AggBytes
	cl := resolved.Closure
	progress.note("closure entities: %d", len(cl.Entities))
	progress.note("schema libraries: %d, mapping libraries: %d, workload libraries: %d", len(cl.SchemaLibraries), len(cl.MappingLibraries), len(cl.WorkloadLibraries))

	// Closure-scoped Pass-2: validate ONLY the entities the Release reaches against
	// the closure's libraries (resolved through the lazy lookup). An in-closure
	// entity that is schema-invalid — or whose $ref names a library skipped (broken)
	// or missing — fails loudly here (the hard in-closure library reject); an
	// out-of-closure break is never validated.
	progress.phase(phaseValidate)
	if err := lazyView.ValidateClosureScopedPillars(cl); err != nil {
		return withSkipContext(err, skipped)
	}

	// Reconstruct the sub-aggregate and pipeline-plugin axes hermetically (the
	// closure Lookup carries a nil OCIPuller, so no registry fetch) so the drift
	// check below compares the lockfile against a closure carrying the same
	// sub-aggregate and plugin entries the pull pinned — ComputeClosure leaves the
	// plugin axis and the transitive sub-aggregate axis empty. Provider bindings
	// resolve from the LOCKFILE, never the workspace config, so a plugins.providers
	// change after the pull does not move the reconstructed closure. Phase B's
	// re-fold shifts the lazy view's pin set to the augmented closure to make the
	// just-pinned plugin entities visible; the pins are restored to the lockfile
	// view afterward so the render path below resolves exactly as before.
	closureLookup := resolved.Lookup
	reconAggLoader, aggErr := closure.RequireAggregateLoader(closureLookup)
	if aggErr != nil {
		return withSkipContext(aggErr, skipped)
	}
	reconRecipeLoader, recipeErr := closure.RequireRecipeLoader(closureLookup)
	if recipeErr != nil {
		return withSkipContext(recipeErr, skipped)
	}
	reconBindings := lockfile.ProviderBindings(lf)
	reconResolver := func(ref string) (types.Plugin, bool, error) {
		return lazyView.ProviderPluginForTarget(ref, reconBindings)
	}
	reconRefold := func() (closure.RecipeLoader, error) {
		lazyView.SetPins(catalog.PinsFromClosure(cl))
		return lazyView.ClosureRecipeLoader(), nil
	}
	if err := closure.ReconstructClosureAxes(&cl, reconAggLoader, reconRecipeLoader, closureLookup, reconResolver, reconRefold); err != nil {
		return withSkipContext(err, skipped)
	}
	lazyView.SetPins(catalog.PinsFromLockfile(lf))

	// Reconstruct the output-shaping workspace-socket plugin entities and their
	// .wasm from the LOCKFILE (provider bindings + mutator attachments), config-
	// invariant: these sockets are pull-time choices recorded only in the lockfile,
	// so ComputeClosure cannot re-derive them hermetically without re-reading config.
	// The socket lookup honors the lockfile pins so a pinned socket .wasm resolves
	// from the content-addressed cache offline; the entity + artifact are re-read
	// (re-hashed) so a drifted or tampered pin still surfaces as drift.
	socketEntityURIs := lockfile.WorkspaceSocketEntityURIs(lf)
	if len(socketEntityURIs) > 0 {
		socketLookup := lazyView.ClosureLookupWithPins(nil, wsRoot, cacheDir, nil, catalog.PinsFromLockfile(lf))
		socketRecipeLoader, socketErr := closure.RequireRecipeLoader(socketLookup)
		if socketErr != nil {
			return withSkipContext(socketErr, skipped)
		}
		if err := closure.ReconstructWorkspaceSocketEntities(&cl, socketEntityURIs, socketRecipeLoader, socketLookup); err != nil {
			return withSkipContext(err, skipped)
		}
	}

	// Lockfile-drift verification against the freshly-computed closure. Only a
	// pre-existing lockfile is verified; an auto-pulled one matches by construction.
	if lockfilePreexisted {
		if err := lockfile.Verify(lf, cl); err != nil {
			return fmt.Errorf("%w; run `aggrega pull --upgrade --release %s` to refresh the lockfile against current catalog state",
				err, releasePath)
		}
	}

	// Resolve extends inheritance for the closure's Components and Features so the
	// pipeline sees fully-merged entities. Closure-scoped: an out-of-closure broken
	// extends never fails the build; an in-closure broken parent does.
	progress.phase(phaseInheritance)
	if extErrs := lazyView.ResolveClosureExtends(cl); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(diagOut, "  extends: %s\n", e)
		}
		return withSkipContext(fmt.Errorf("catalog extends: %d resolution error(s)", len(extErrs)), skipped)
	}

	// Surface non-fatal deprecation notices about deprecated catalog shapes.
	for _, w := range cat.DeprecationWarnings() {
		fmt.Fprintf(diagOut, "  deprecation: %s\n", w)
	}

	// Deprecated-entity policy gate: report every pinned reference carrying a
	// retroactive lockfile marker, halting or advising per the workspace build
	// policy. The report is the shared one every surface reads; the verdict is this
	// verb's, because a build produces an artifact. Reads only the lockfile — no
	// registry access (the ahead-of-time boundary).
	if err := enforceLockfileDeprecation(lf, wsCfg.Build, diagOut); err != nil {
		return err
	}

	// The same gate over the other store. A plugin attached at a verdict role — a
	// linter, or governance at ir-verdict / render-verdict — shapes no rendered byte,
	// so it is recorded beside the lockfile rather than pinned in it and has no row
	// the gate above could read. Without this, a withdrawn gate plugin is fetched,
	// cached and run with nothing said. Reads only the record: no registry access.
	trustRecord, _, trustErr := pluginstrust.LoadOrStart(pluginstrust.PathFor(releasePath))
	if trustErr != nil {
		return trustErr
	}
	if err := enforceTrustDeprecation(trustRecord, wsCfg.Build, diagOut); err != nil {
		return err
	}

	// Surface non-fatal schema-pillar-overlap advisories for the build's closure
	// entities (registry-pinned included) — the warning-level sibling of the
	// closure-scoped pillar validation above, over the same closure-entity set.
	for _, w := range lazyView.ClosureLintWarnings(cl) {
		fmt.Fprintf(diagOut, "  notice: %s\n", w)
	}

	// Provider rendering routes through the pipeline's catalog-driven Wasm
	// dispatch — the single path both the CLI build and the build-faithful test
	// harness use, so a test renders through exactly the plugins a real build does.
	progress.phase(phaseRender)
	relAggPath := catalog.RelativeToWorkspace(wsRoot, resolved.AggregatePath)
	buildLookup := lazyView.AsCatalogLookup()
	// Provider resolution reads the resolved bindings from the LOCKFILE, never the
	// workspace config — so build output is invariant to a plugins.providers change
	// made after the pull (a change is adopted only on the next pull). A federated
	// catalog with more than one plugin serving a target is disambiguated by the
	// pinned binding; without a binding an ambiguous target halts loudly at render.
	lockfileBindings := lockfile.ProviderBindings(lf)
	buildLookup.ProviderPlugin = func(ref string) (types.Plugin, bool, error) {
		return lazyView.ProviderPluginForTarget(ref, lockfileBindings)
	}
	// The pipeline context carries the external-input service to the
	// renderer's plugin-load sites, where each loader picks it up as its
	// fetch seam.
	buildCtx := wasm.ContextWithExternalInputs(context.Background(), extInputs)
	buildCtx = wasm.ContextWithDisablePluginCache(buildCtx, run.disablePluginCache)
	buildCtx = wasm.ContextWithVerifyDeterminism(buildCtx, run.verifyDeterminism)
	pipelineInputs := pipeline.Inputs{
		Release:            rel,
		Manifest:           manifest,
		AggregateRawBytes:  aggBytes,
		AggregatePath:      relAggPath,
		WorkspaceConfig:    wsCfg,
		Lockfile:           lf,
		WorkspaceRoot:      wsRoot,
		MappingLibraries:   lazyView.MappingLibraries(),
		WorkloadLibraries:  lazyView.WorkloadLibraries(),
		ArtifactsLibraries: lazyView.ArtifactsLibraries(),
		CompilerVersion:    provenanceCompilerVersion(),
	}
	// Compose the governance attachment list the pipeline dispatches and the
	// digests recorded as trusted for the verdict attachments, through the one
	// composition every build surface calls — the command line here, and the
	// daemon and the editor's build tool through BuildToBuffer. Both halves arrive
	// together because a surface that composed the attachments and omitted the
	// recorded digests would run every configured gate while holding none of them
	// to the identity that was reviewed, and would do it silently: an unsupplied
	// digest map is an empty one, and an empty one means nothing was recorded.
	// ApplyGovernanceComposition documents what each half reconstructs and why.
	if err := compilerops.ApplyGovernanceComposition(&pipelineInputs, lazyView, releasePath); err != nil {
		return err
	}
	result, err := pipeline.RunPipeline(
		buildCtx,
		pipelineInputs,
		buildLookup,
		lazyView.RecipeLookupWithManifest(manifest),
		lazyView.SubAggregateLoader(),
		pipeline.Options{
			IncludeTrace:             traceEnabled,
			TraceUnredacted:          run.traceUnredacted,
			RecipeResolutionVerifier: lazyView.VerifyRecipeResolution,
			// Per-node render progress. The callback writes only to the progress
			// reporter's diagnostic sink (stderr); stdout carries only the final
			// summary, so rendered output stays byte-deterministic. A no-op
			// reporter (quiet, no debug) ignores the calls.
			NodeProgress: progress.renderNode,
			// Eager plugin-compilation phase callbacks for the "Plugin compilation"
			// checklist step. Begin fires once with the number of provider guests to
			// compile before the render loop; Running fires when a guest is observed
			// to actually compile (revealing the step); GuestDone fires as each
			// finishes. All write only to the progress reporter's diagnostic sink, so
			// stdout stays byte-deterministic. A silent reporter ignores the calls;
			// when Begin is set, the pipeline runs the eager compile pass so a guest
			// compilation is attributed rather than buried in the first node's render.
			PluginCompileBegin:               progress.pluginCompileBegin,
			PluginCompileGuestDone:           progress.pluginCompileGuestDone,
			PluginCompileRunning:             progress.pluginCompileRunning,
			SuppressUnsupportedFieldWarnings: run.suppressUnsupportedFieldWarnings,
		},
	)
	if err != nil {
		return err
	}

	// Write output. Clean dist/boundaries/ first so stale files from a prior
	// build of a different variant don't leak into this one (GitOps consumers
	// must see exactly what the compiler just produced).
	outDir := run.outDir
	if outDir == "" {
		outDir = filepath.Join("dist")
	}
	if err := os.RemoveAll(filepath.Join(outDir, "boundaries")); err != nil {
		return fmt.Errorf("clean %s/boundaries: %w", outDir, err)
	}
	if err := writeArtifacts(outDir, result.Artifacts); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	// Trace surface — write the collected per-step intermediate state
	// to .trace/ (sibling to dist/, never under). The sibling layout
	// keeps dist/ purely deployable: no flag-conditional contents, no
	// apply-tool walking surprises. Skip when the trace map is empty
	// so an empty-trace fixture run does not clobber a pre-existing
	// .trace/ from an earlier traced build.
	if traceEnabled && len(result.Trace) > 0 {
		if err := os.RemoveAll(".trace"); err != nil {
			return fmt.Errorf("clean .trace: %w", err)
		}
		if err := writeArtifacts(".trace", result.Trace); err != nil {
			return fmt.Errorf("write trace: %w", err)
		}
	}

	// Persist newly-pinned external inputs into the lockfile after the
	// build succeeded. The service's full record view is the canonical
	// URL-deduplicated merge of the lockfile's pinned view with this run's
	// acquisitions; the canonical writer keeps the file byte-deterministic.
	// A run that pinned nothing leaves the lockfile untouched.
	acquired := extInputs.AcquiredRecords()
	if len(acquired) > 0 {
		lf.ExternalInputs = extInputs.Records()
		if err := lockfile.WriteLockfilePreservingTimestamp(lockfile.PathFor(releasePath), lf); err != nil {
			return fmt.Errorf("write lockfile external inputs: %w", err)
		}
	}
	if run.onAcquired != nil {
		run.onAcquired(acquired)
	}

	// Clear the in-place progress line (and, in debug mode, print the timing
	// total) before the stdout summary so the progress surface never lingers
	// beside the final output. The deferred finish is idempotent.
	progress.finish()
	fmt.Fprintf(out, "%s\n", formatTotalBuildTimeLine(formatPhaseElapsed(time.Since(buildStart))))
	fmt.Fprintf(out, "Build completed: %d files written to %s/\n", len(result.Artifacts), outDir)
	return nil
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

// newExternalInputsService constructs the per-build external-input gate over
// the workspace artifact cache's inputs store, seeded with the lockfile's
// pinned view. Acquisition is on unless the operator froze the closure
// (--no-acquire-inputs). The HTTP client comes from the network-egress gate; it
// is nil for an offline workspace, so pinned inputs are still served from the
// cache but any fetch (acquisition or a missing-blob refill) refuses loudly —
// offline dominates acquire mechanically, without a separate consult.
func newExternalInputsService(cacheDir string, lf *lockfile.Lockfile, noAcquire bool, wsCfg workspaceconfig.Config) *externalinputs.Service {
	httpClient, _ := egress.HTTPClient(wsCfg)
	return externalinputs.NewService(externalinputs.StoreRoot(cacheDir), !noAcquire, httpClient, lf.ExternalInputs)
}

// acquireLockfile ensures the per-Release lockfile exists before the build wires
// its lazy lookup + runs closure-scoped Pass-2, returning whether it PRE-EXISTED.
// A present lockfile is left as-is — drift against it is verified later, against
// the resolved closure. Build is read-only by default: a missing lockfile halts
// with the pull-first remediation and no lockfile or source pins are written.
// When the pull step is requested — the --auto-pull flag (autoPull) or workspace
// auto_pull — a missing lockfile is instead filled in-process (populating the
// lockfile + cache) before the build proceeds; upgrade makes that fill a full
// refresh (float every dependency to the highest satisfying version rather than
// honoring existing pins). Under offline a missing lockfile always halts,
// regardless of the pull step, because the fill itself needs network access.
//
// The lockfile must exist before the lazy lookup is pinned: its pins drive the
// verify-on-read Tier-2 reads through which every closure entity (and the
// libraries a $ref names) resolves.
//
// The two sinks are kept apart, because --detail's own help promises the
// resolution report on stdout. out carries that report: the no-pull note written
// here, and the fill's per-entity rows and completion line forwarded through
// pullConfig.Out. diagOut carries the fill's reuse/verify notices and progress
// surface, forwarded through pullConfig.DiagOut. In production out is os.Stdout
// and diagOut is os.Stderr.
func acquireLockfile(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, wsCfg workspaceconfig.Config, autoPull, upgrade, detail bool, diagOut, out io.Writer) (bool, error) {
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

	// build's auto-pull is the opt-in fill: it runs when the --auto-pull flag or
	// workspace.auto_pull is set. By default (upgrade off) it is lockfile-honoring
	// — it honors every existing pin (resolving to the pinned version, verifying the
	// pinned digest, halting on a drifted pin), resolves + adds un-pinned references,
	// refills an empty cache by pin, and prunes stale pins; on a complete,
	// cache-populated lockfile it is a no-op with NO network (every pin a Tier-2 hit),
	// so the build stays hermetic given a complete lockfile. With upgrade the fill
	// is a full refresh: it discards the existing pin set and re-resolves every
	// dependency, floating each to the highest satisfying version. There is no
	// absent-vs-partial special case: any incomplete/empty state runs the same fill,
	// and lockfile.Verify (in dispatchBuild) is the post-condition. The build's own
	// resolution runs through the nil-puller pinned lazy lookup, untouched — so build
	// output is unchanged by this fill.
	if autoPull || wsCfg.AutoPull {
		if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catalogFlagDirs, Upgrade: upgrade, Detail: detail, ws: rw, DiagOut: diagOut, Out: out, diag: buildDiagOptions{quiet: true}}); err != nil {
			return false, fmt.Errorf("auto-pull: %w", err)
		}
		return false, nil
	}

	// Read-only default: the build is frozen against the lockfile as it stands and
	// never writes source pins. A present lockfile is used as-is; a missing one halts
	// with the pull-first remediation.
	if preexisted {
		if detail {
			fmt.Fprintf(out, "aggrega build --detail: resolution detail is a pull-time surface; lockfile %s already present, so no pull ran this build\n", lockfilePath)
		}
		return true, nil
	}
	return false, fmt.Errorf("aggrega build: lockfile %s missing; run `aggrega pull --release %s` first to create it, or pass --auto-pull (or enable auto_pull) to fill it during the build",
		lockfilePath, releasePath)
}

// skipContextError is a resolution or validation failure carrying the catalog
// files the tolerant load refused. It DECORATES the failure rather than
// replacing it: Unwrap returns the failure itself, so errors.Is and errors.As
// still reach the condition the failure raised and a registered condition's
// short code still survives to the reader.
//
// It is a named type rather than an fmt wrap because the diagnostic formatter
// renders a failure from the outermost categorised envelope it can reach and
// discards every frame above it. A note appended by an fmt wrap therefore
// reaches the reader only while the failure happens to carry no envelope, and
// disappears the moment one is introduced beneath it — silently, on the exact
// path where the note is the only text naming what the author wrote wrong. A
// named type gives the formatter something to detect, so the note reaches the
// reader whatever shape the failure it decorates has.
//
// promoted is the refused file's own error when the record singled out exactly
// one refusal as the reason this failure happened, and nil otherwise. It is
// what the reader meets first: an author whose document is missing a required
// field is told that, not told that a reference to the entity that document
// would have provided could not be bound. The failure it explains is still
// reported, inside the note, because it is the context that says why the
// document was needed at all.
type skipContextError struct {
	err      error
	promoted error
	note     string
}

// Error renders the failure the reader is meant to meet first, followed by the
// note. That is the promoted refusal where one was singled out, and the
// decorated failure otherwise; the other of the two is inside the note either
// way, so nothing is dropped.
func (e *skipContextError) Error() string {
	if e.promoted != nil {
		return cerrors.CauseChain(e.promoted) + e.note
	}
	return e.err.Error() + e.note
}

// Unwrap returns the decorated failure, so errors.Is and errors.As traverse
// into it and the sentinel the failure raised stays matchable at the boundary.
// Promotion changes what the reader meets first; it does not change what a
// caller matches on.
func (e *skipContextError) Unwrap() error { return e.err }

// withSkipContext annotates an in-closure resolution / validation failure with
// the catalog files the load refused, so a tolerated skip never hides WHY an
// entity the Release reaches went missing. A skipped file whose header named the
// kind+name is reported with that identity; a file too malformed to peek is
// reported by path. No-op when nothing was skipped or err is nil.
//
// CAUSE BEFORE CONSEQUENCE, where the cause can be named exactly. A refused
// file never enters the catalog, so what fails is the attempt to resolve a
// reference to what that file declared — a consequence of the refusal. Where the
// record singles out exactly one refusal declaring the identity the failing
// reference names, that refusal leads the report and the resolution failure
// moves into the note beneath it. Where it cannot — no refusal declares that
// identity, or more than one does — the report is exactly what it would be with
// no record at all: the failure leads, and every refusal is listed beneath it
// for the reader to weigh. Naming the wrong file as the cause would be worse
// than naming none, because a reader acts on it.
//
// The note states the distinction the tolerant load draws, because the reader
// meeting the note is the one who has to act on it: a refused file OUTSIDE this
// Release's closure is tolerated and never fails a build, so a failure that is
// only listed alongside refusals is explained only by a refusal naming something
// this Release reaches. That is stated rather than left implicit — a reader who
// does not know which class is which cannot tell a cause from a bystander. Where
// a refusal IS promoted the distinction is settled rather than stated: the
// promoted file declares the identity the Release reached, which is what made it
// promotable.
//
// The failure stays in the returned error's chain rather than being rendered
// into text, so a registered condition's identity survives to the reader and
// errors.Is still matches it at the command boundary — promoted or not.
//
// Each reason is rendered as its whole cause chain, not just its outermost
// frame: the outermost frame typically says only which step refused the file
// ("unmarshal Component"), while the frame beneath it is the one naming the
// constraint the file violated — the only part an author can act on.
func withSkipContext(err error, skipped []catalog.SkippedEntity) error {
	if err == nil || len(skipped) == 0 {
		return err
	}
	var b strings.Builder
	cause, promoted := refusalExplaining(err, skipped)
	if promoted {
		b.WriteString("\n  note: this file was skipped at load, so the catalog holds nothing under the identity it declares.")
		b.WriteString("\n  This Release reaches that identity, so what failed is the attempt to resolve the reference to it:")
		fmt.Fprintf(&b, "\n    %s", indentContinuations(cerrors.CauseChain(err), "    "))
		if rest := refusalsBesides(skipped, cause); len(rest) > 0 {
			b.WriteString("\n  note: other catalog files were skipped at load too. A refusal outside this Release's closure is")
			b.WriteString("\n  tolerated and never fails a build:")
			writeRefusals(&b, rest)
		}
		return &skipContextError{err: err, promoted: cause.Err, note: b.String()}
	}
	b.WriteString("\n  note: catalog files skipped at load. A refusal outside this Release's closure is tolerated and never fails a build,")
	b.WriteString("\n  so this failure is explained only by a refusal naming something this Release reaches:")
	writeRefusals(&b, skipped)
	return &skipContextError{err: err, note: b.String()}
}

// refusalExplaining reports the one refused catalog file that explains err, and
// whether exactly one does.
//
// It reads the reference off the failure rather than out of its rendered text.
// A resolution failure carries the reference it could not resolve as a field, so
// the match is against the identity the refused document declares for itself —
// an identity match, not a search for a name inside a message.
//
// A failure carrying no reference at all cannot be explained by any single
// refusal, so it reports false: a validation failure over an entity that DID
// load names no missing entity, and blaming a refusal for it would be a guess.
//
// A matched refusal carrying no reason of its own is declined too. There would
// be nothing to lead the report with, and leading with a note whose subject is
// absent reads worse than the report it replaced.
func refusalExplaining(err error, skipped []catalog.SkippedEntity) (catalog.SkippedEntity, bool) {
	var unresolved *closure.UnresolvedEntityError
	if !stderrors.As(err, &unresolved) || unresolved == nil {
		return catalog.SkippedEntity{}, false
	}
	cause, ok := catalog.SoleRefusalFor(skipped, unresolved.URI, unresolved.KindHint)
	if !ok || cause.Err == nil {
		return catalog.SkippedEntity{}, false
	}
	return cause, true
}

// refusalsBesides returns the refusals other than the promoted one, keeping the
// record's order. The promoted file is already the head of the report, so
// repeating it in the note would say the same thing twice.
func refusalsBesides(skipped []catalog.SkippedEntity, promoted catalog.SkippedEntity) []catalog.SkippedEntity {
	rest := make([]catalog.SkippedEntity, 0, len(skipped))
	for _, s := range skipped {
		if s.Path == promoted.Path {
			continue
		}
		rest = append(rest, s)
	}
	return rest
}

// indentContinuations prefixes every line of s after the first with indent,
// preserving whatever indentation those lines already carry. A failure's own
// text can be several lines — a remediation sentence sits on its own — and
// placing such a block under a note means its continuation lines have to move
// with it, or they read as belonging to the level above.
func indentContinuations(s, indent string) string {
	return strings.ReplaceAll(s, "\n", "\n"+indent)
}

// writeRefusals renders one bullet per refused file: its declared identity where
// the header yielded one, its path otherwise, then the condition it raised.
func writeRefusals(b *strings.Builder, skipped []catalog.SkippedEntity) {
	for _, s := range skipped {
		reason := cerrors.CauseChain(s.Err)
		if code, ok := cerrors.ShortCodeOf(s.Err); ok {
			reason = shortCodeInHead(reason, code)
		}
		switch {
		case s.Kind != "" && s.Name != "":
			fmt.Fprintf(b, "\n    - %s %q (%s): %s", s.Kind, s.Name, s.Path, reason)
		default:
			fmt.Fprintf(b, "\n    - %s: %s", s.Path, reason)
		}
	}
}

// shortCodeInHead puts a condition's short code inside the bracketed head a
// rendered diagnostic already opens with, beside the category — the one
// position the error reference teaches a code appears in, so a refused file
// reported inside a note names its condition exactly as it would when it is
// the whole diagnostic.
//
// A reason that opens with no bracketed head is returned unchanged rather than
// gaining a second, differently-shaped presentation of the same identifier. So
// is one whose head already carries two tokens: the head is category-then-code
// and nothing else, so a space inside it means the code is already there and
// the function is idempotent.
func shortCodeInHead(reason, code string) string {
	if !strings.HasPrefix(reason, "[") {
		return reason
	}
	end := strings.Index(reason, "]")
	if end < 0 || strings.Contains(reason[:end], " ") {
		return reason
	}
	return reason[:end] + " " + code + reason[end:]
}

// writeArtifacts writes the rendered file map to disk under outRoot,
// preserving relative path structure and creating intermediate
// directories as needed. Each entry's name is validated:
//
//   - Empty names are rejected (defensive guard against renderer
//     producing a zero-key entry).
//   - Names that escape outRoot are rejected. The check defends
//     against a malicious or buggy renderer engine emitting a name
//     that lands outside the output root.
//
// An artifact name is a slash-separated logical name rather than a
// path in the host's own spelling, so containment is judged in slash
// arithmetic — identically on every platform — and the name is
// converted to a host path only to write it. Judging it as a host
// path instead would make a backslash a separator on one platform and
// an ordinary character on another, so the same rendered output would
// be accepted on one machine and refused on the next.
//
// Entries are written in sorted name order. Go randomises map
// iteration, so writing in range order would emit the same build's
// files in a different sequence on every run: the bytes on disk match,
// but the order of the mkdir and write calls that produced them does
// not, and a consumer watching the tree as it fills — or a filesystem
// recording creation order — sees a different build each time.
//
// On any per-file error (mkdir, write), the function returns the
// wrapped error and stops — partial writes to disk persist; the
// caller is responsible for any cleanup in the partial-write case.
// Sorting also makes that partial prefix reproducible: the same
// failing input stops after the same set of files.
func writeArtifacts(outRoot string, files map[string][]byte) error {
	outRoot = filepath.Clean(outRoot)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("empty artifact path")
		}
		if !pathscope.IsLocalSlash(name) {
			return fmt.Errorf("invalid artifact path %q (path escapes the output root)", name)
		}
		full := filepath.Join(outRoot, filepath.FromSlash(path.Clean(name)))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, files[name], 0o644); err != nil {
			return fmt.Errorf("write %q: %w", full, err)
		}
	}
	return nil
}
