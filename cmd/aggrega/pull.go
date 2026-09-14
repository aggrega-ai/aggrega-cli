// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// pullFlags carries the `pull` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values. It is distinct from pullConfig, which is the input contract pullRun
// reads.
type pullFlags struct {
	releasePath       string
	catalogDirs       []string
	upgrade           bool
	verify            bool
	includePrerelease bool
	detail            bool
	quiet             bool
	progress          bool
	assumeYes         bool
}

func newPullCmd() *cobra.Command {
	opts := &pullFlags{}
	c := &cobra.Command{
		Use:   "pull [<uri>...]",
		Short: "Resolve catalog and source dependencies into the cache and write the per-Release lockfile",
		Long: `Resolve the Release's catalog and source dependencies into the cache and write the per-Release lockfile.

By default, pull reconciles the lockfile against the current dependency resolution while honoring
the pins it already records. That pin discipline governs what a registry or the content cache
serves: such an entity is fetched at its pinned version and verified against its pinned digest,
and if the registry no longer serves the pinned bytes, pull halts rather than silently re-pinning.
An entity a local catalog directory supplies is resolved from that directory instead, at the
version its file declares, and its recorded pin is not consulted — so editing a pin by hand does
not hold a locally-supplied entity at that version, and the reconcile rewrites the entry to match
the file. A reference with no pin — newly added, or removed from the lockfile by hand — is
resolved fresh and added. Entities no longer among the resolved dependencies are pruned.
Re-running pull on an unchanged dependency set is a no-op.

Use --upgrade with no positional arguments to discard the existing lockfile and re-resolve
every entity from current registry state, floating each dependency to the highest available
version. That form drops every recorded pin, so it asks for confirmation first; --yes answers in
advance, and on a non-interactive stdin it refuses rather than waiting for an answer nobody can
give. Pass one or more entity URIs after --upgrade to upgrade only those: each named pin
is dropped and re-resolved fresh while every other pin — and the recorded external inputs —
is held unchanged. A named target may also be a plugin that returns an accept-or-reject verdict;
such a plugin carries no lockfile pin, and its identity is recorded beside the lockfile instead.
Naming one the workspace still attaches adopts the identity it resolves to. Naming one the
workspace no longer attaches drops its recorded identity — the one way to clear an entry that
outlived the attachment it was recorded for. A bare --upgrade does both for every plugin at once.
A URI given without --upgrade is a usage error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.verify && opts.upgrade {
				return fmt.Errorf("--verify and --upgrade are mutually exclusive")
			}
			if len(args) > 0 && !opts.upgrade {
				return fmt.Errorf("positional arguments name upgrade targets and require --upgrade")
			}
			// Only the bare form is confirmed: naming targets holds every pin not
			// named, so there is nothing here for a person to lose. The gate sits
			// ahead of the workspace resolution because what it guards is the
			// operator's intent, which no resolved state can change.
			if opts.upgrade && len(args) == 0 {
				if err := confirmDestructiveRefresh(cmd.OutOrStdout(), cmd.InOrStdin(), "aggrega pull", opts.assumeYes); err != nil {
					return err
				}
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pullRun(cmd.Context(), pullConfig{
				ReleasePath:       opts.releasePath,
				CatalogDirs:       opts.catalogDirs,
				Upgrade:           opts.upgrade,
				UpgradeTargets:    args,
				Verify:            opts.verify,
				IncludePrerelease: opts.includePrerelease,
				Detail:            opts.detail,
				ws:                rw,
				diag:              buildDiagOptions{quiet: opts.quiet, progress: opts.progress, noColor: globalDisplay.colorOff, plain: globalDisplay.plain},
			})
		},
	}
	c.Flags().StringVar(&opts.releasePath, "release", "", "Path to Release YAML file (required)")
	c.Flags().StringArrayVar(&opts.catalogDirs, "catalog", nil, "Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	c.Flags().BoolVar(&opts.upgrade, "upgrade", false, "Re-resolve dependencies from current registry state, floating each to the highest available version. Bare (no positional URIs) discards the lockfile and re-resolves every entity, and asks for confirmation first because it drops every recorded pin; with one or more entity URIs, re-resolves only those, holding all other pins and the recorded external inputs unchanged, and asks nothing (mutually exclusive with --verify)")
	c.Flags().BoolVar(&opts.verify, "verify", false, "Verify the existing lockfile against the current dependency resolution without writing; exits non-zero on drift or missing lockfile. Also reports the compiler and core-plugin versions the lockfile records — reported for audit and never compared against this binary, so a toolchain difference is never drift (mutually exclusive with --upgrade)")
	c.Flags().BoolVar(&opts.includePrerelease, "include-prerelease", false, "Not yet implemented: accepted and ignored, so a script that passes it keeps working when it starts having an effect")
	c.Flags().BoolVar(&opts.detail, "detail", false, "Print, per resolved entity, what this pull did — fetched fresh from a registry, reused a cache slot, found in a local catalog, served from inside the compiler binary, or external. Stdout only; not written to the lockfile")
	c.Flags().BoolVar(&opts.quiet, "quiet", false, "Silence the pull-progress surface on stderr. Does not affect stdout. Takes precedence over --progress")
	c.Flags().BoolVar(&opts.progress, "progress", false, "Force the pull-progress surface on even when stderr is not a terminal, printing plain static phase lines. The default is auto: the interactive phase checklist is shown only when stderr is a terminal")
	c.Flags().BoolVar(&opts.assumeYes, "yes", false, "Answer the bare --upgrade confirmation in advance, for non-interactive use. It reaches that one prompt and nothing else: every other form of pull asks nothing, so passing it changes no behaviour there")
	if err := c.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	return c
}

// pullConfig is the input contract for this command's dependency-resolution
// workflow. cobra-bound flag handling reads the command-line values into a
// pullConfig instance; build's automatic-fill integration constructs one
// directly.
//
// It is the command's own shape and stops at the command's edge: the resolution
// itself is internal/pullops, whose Config is what pullRun assembles from this.
// The two are separate because they answer different questions — this one carries
// flag values and a terminal, that one carries the inputs the operation needs
// however it was reached.
type pullConfig struct {
	ReleasePath string
	// CatalogDirs holds the repeatable --catalog flag directories (UNresolved):
	// the resolution prepends them to the workspace catalog_dirs via
	// ResolveCatalogDirs. build's automatic-fill integration forwards its own flag
	// dirs so the resolution re-derives the identical stack under the same config.
	CatalogDirs []string
	Upgrade     bool
	// UpgradeTargets names the entity URIs to upgrade when Upgrade is set. Empty
	// with Upgrade set is a full refresh (float every dependency); non-empty is a
	// targeted upgrade — only the named pins are dropped and re-resolved, every
	// other pin and the recorded external inputs are held. Each URI is matched by
	// exact string, in the authored bare form (e.g. core/database, not a versioned
	// form), against a lockfile pin key OR against a configured attachment whose
	// plugin carries no pin because its identity is recorded beside the lockfile
	// instead; a URI matching neither halts. The second case reduces no pin — there
	// is none to drop — and re-resolves the attachment it names.
	UpgradeTargets    []string
	Verify            bool
	IncludePrerelease bool
	Detail            bool

	// ws is the resolved workspace, threaded in by the command's RunE from the
	// shared resolution chokepoint. Required: the shared layer refuses a nil, so a
	// test invoking pullRun directly declares the workspace it resolves.
	ws *workspaceconfig.ResolvedWorkspace

	// DiagOut is the sink for the resolution's reuse/verify diagnostic notices.
	// Nil defaults to os.Stderr where the resolution is driven (production); tests
	// pass a buffer to capture diagnostics without mutating a process global.
	DiagOut io.Writer

	// Out is the sink for this command's user-facing status output (the --detail
	// table, the "Verify:" line, and "Pull complete"). Nil defaults to os.Stdout
	// where the resolution is driven (production); tests pass a buffer to capture
	// the output without mutating a process global.
	Out io.Writer

	// puller overrides the production OCI puller; tests inject an in-memory
	// implementation to exercise a remote-library fetch without the network. Nil
	// builds the production puller, which is nil when the workspace is offline:
	// the network-egress gate refuses before any dial, so no registry tier exists
	// and unresolved references surface as NotFound rather than as a fetch error.
	puller catalog.OCIPuller

	// sourceResolver overrides the production source-resolver shim; tests inject
	// a recording/stub implementation to assert the source identity computed from
	// the materialized pillars without a real artifact fetch. Nil uses the shim.
	sourceResolver closure.SourceResolver

	// diag carries the pull-progress surface choices (quiet / progress). The pull
	// command's RunE populates it from the flags; build's automatic fill leaves it
	// quiet so only the build's own surface shows during an implicit resolution.
	diag buildDiagOptions

	// renderOptions is the option set the external-input discovery render runs
	// under. It is unexported and has no flag behind it because this command has
	// none to offer: a person running the resolution on its own asks for a
	// lockfile, not for a rendered tree, so its discovery render carries the zero
	// value and every knob is off. Build's automatic fill is what fills it — with
	// the very option set that build would have rendered under, so the discovery
	// render and the build's render are one render under one set of knobs.
	renderOptions pipeline.Options
}

// Pull phase indices into the pull progress spine's labels.
const (
	pullPhaseLoadCatalog = iota
	pullPhaseDependencies
	pullPhaseClosure
	pullPhaseLibraries
	pullPhaseValidate
	pullPhaseSources
	pullPhaseExternalInputs
	pullPhaseLockfile
)

// pullProgressSpine returns the ordered pull phase spine.
//
// It carries no per-node counter and no plugin-compile step (renderIdx -1),
// which is a statement about this surface rather than about the operation: the
// resolution does render, to find out which external inputs the Release consumes,
// but that render's artifacts are discarded and its rows would put a per-node
// counter in the middle of a spine whose last step is the lockfile write. The
// external-input step therefore draws as one row like the others.
func pullProgressSpine() progressSpine {
	return progressSpine{
		labels: []string{
			pullPhaseLoadCatalog:    "loading catalog",
			pullPhaseDependencies:   "resolving dependencies",
			pullPhaseClosure:        "resolving dependencies",
			pullPhaseLibraries:      "resolving libraries",
			pullPhaseValidate:       "validating",
			pullPhaseSources:        "resolving sources",
			pullPhaseExternalInputs: "pinning external inputs",
			pullPhaseLockfile:       "writing lockfile",
		},
		renderIdx: -1,
	}
}

// pullPhaseStep maps a stage of the shared resolution onto this command's
// progress spine. The two are deliberately separate vocabularies: the spine
// carries layout the shared layer knows nothing about, and the shared layer
// names stages a surface is free to draw as one row or several.
func pullPhaseStep(p pullops.Phase) int {
	switch p {
	case pullops.PhaseLoadCatalog:
		return pullPhaseLoadCatalog
	case pullops.PhaseDependencies:
		return pullPhaseDependencies
	case pullops.PhaseClosure:
		return pullPhaseClosure
	case pullops.PhaseLibraries:
		return pullPhaseLibraries
	case pullops.PhaseValidate:
		return pullPhaseValidate
	case pullops.PhaseSources:
		return pullPhaseSources
	case pullops.PhaseExternalInputs:
		return pullPhaseExternalInputs
	case pullops.PhaseLockfile:
		return pullPhaseLockfile
	}
	return pullPhaseLoadCatalog
}

// pullRun drives the shared dependency resolution and reports what it did,
// discarding what the resolution computed. It is the form every caller that
// wants the resolution performed rather than examined uses.
//
// ctx is the caller's and is carried into every step that leaves the process, so
// a caller that withdraws stops the resolution rather than only the reply. This
// form differs from pullRunWithResult on one axis only — the result — and
// carries no render knobs of its own, so a caller whose own render knobs must
// reach the discovery render hands its context to that form instead.
func pullRun(ctx context.Context, cfg pullConfig) error {
	_, err := pullRunWithResult(ctx, cfg)
	return err
}

// pullRunWithResult drives the shared dependency resolution from the command
// line, reports what it did on this surface's own streams, and hands back what
// the resolution computed.
//
// What stays here is what belongs to a surface a person drives: the progress
// spine, the interactive fork the resolution cannot settle from its inputs, the
// skipped-catalog annotation, this binary's identity, and the report. The
// resolution itself — the artifact-store client, the egress gate, the trust
// surface and the lockfile write — is internal/pullops, which every surface runs.
//
// ctx is the caller's. The resolution carries it into every step that leaves the
// process, and it also carries the render knobs that ride on a context rather
// than on an option struct — so a caller whose own render knobs must reach the
// discovery render hands its context in here rather than letting this layer
// invent one.
func pullRunWithResult(ctx context.Context, cfg pullConfig) (pullops.Result, error) {
	diagOut := cfg.DiagOut
	if diagOut == nil {
		diagOut = os.Stderr
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	// Verify is a quick read-only check with an early return, so it carries no
	// progress surface; the default and --upgrade paths walk the full spine.
	diag := cfg.diag
	if cfg.Verify {
		diag.quiet = true
	}
	progress := newProgressReporter(diagOut, diag, pullProgressSpine())
	defer progress.finish()

	opts := pullops.Config{
		Workspace:                 cfg.ws,
		ReleasePath:               cfg.ReleasePath,
		CatalogDirs:               cfg.CatalogDirs,
		Upgrade:                   cfg.Upgrade,
		UpgradeTargets:            cfg.UpgradeTargets,
		Verify:                    cfg.Verify,
		IncludePrerelease:         cfg.IncludePrerelease,
		CompilerVersion:           provenanceCompilerVersion(),
		Toolchain:                 toolchainStamp(),
		DiagOut:                   diagOut,
		AnnotateResolutionFailure: withSkipContext,
		Puller:                    cfg.puller,
		SourceResolver:            cfg.sourceResolver,
		RenderOptions:             cfg.renderOptions,
		Report: pullops.Reporter{
			Phase: func(p pullops.Phase) { progress.phase(pullPhaseStep(p)) },
		},
	}
	// The per-entity resolution report is requested by attaching the printer, so
	// the presence of the callback IS the request and there is no second switch
	// beside it that could disagree with it. It prints where the resolution
	// reaches it, which keeps its rows in the same place relative to the
	// diagnostics as they have always been.
	if cfg.Detail {
		opts.Report.Resolved = func(cl closure.Closure) { printPullDetail(out, cl) }
	}
	// The interactive fork is attached only where a person can answer it: stderr is
	// a terminal, stdin is interactive, and the progress surface is not silenced.
	// Left unattached — a piped invocation, a --quiet one, or any caller with
	// nobody watching — an ambiguously-served capability halts loudly instead, so
	// the run never hangs waiting on input.
	if providerBindingHelperEligible(cfg) {
		opts.ResolveProviderAmbiguity = runProviderBindingHelper
	}

	// The resolution takes the caller's context so a caller that withdraws stops
	// the work rather than only the reply. What reaches it depends on the route
	// in: the pull command hands over the context its command body inherited,
	// which ends when the process is asked to stop, a build's auto-pull hands over
	// the same thing from its own command body, and a request on the daemon or a
	// tool invocation supplies its own request context.
	res, err := pullops.Run(ctx, opts)
	if err != nil {
		return pullops.Result{}, err
	}

	if res.Verified {
		fmt.Fprintf(out, "Verify: lockfile %s matches current dependency resolution (entities=%d)\n",
			res.LockfilePath, len(res.Closure.Entities))
		// Report the toolchain the lockfile records. Verify is the surface a user
		// reaches to find out what a lockfile says, so the stamp is reported here
		// rather than through a command of its own. It is reported and never
		// compared against this binary: the toolchain is not part of the pinned
		// closure, and checking it would pin the compiler by proxy.
		printLockfileToolchain(out, res.Toolchain)
		return res, nil
	}

	// Clear the in-place surface before the stdout summary so the progress lines
	// never linger beside the final output.
	progress.finish()
	fmt.Fprintf(out, "Pull complete: %s (entities=%d, source_artifacts=%d, external_inputs=%d%s)\n",
		res.LockfilePath, len(res.Closure.Entities), len(res.Closure.SourceArtifacts), res.ExternalInputs,
		trustedPluginsSummary(res.TrustedPlugins))
	// A completion line that says nothing about a discovery that did not finish
	// would read as a complete pin set, which is the one thing this run cannot
	// promise. The detail — what stopped, what landed, what a build will do about
	// it — is already on the diagnostic stream, so what belongs beside the
	// completion is the fact that the two streams disagree about completeness.
	if res.ExternalInputDiscovery != nil {
		fmt.Fprintf(out, "External inputs were not fully discovered: dependency pins are complete, external-input pins may not be (see the diagnostics above)\n")
	}
	return res, nil
}

// trustedPluginsSummary renders the trailing field this command's completion
// line carries when plugin identities were recorded, and nothing at all when
// none were.
//
// Nothing rather than a zero: a workspace that attaches no verdict-returning
// plugin has no such identities to have, so reporting `=0` would answer a
// question that was never asked of that workspace.
//
// It is a fragment of one sentence on one surface, so it lives with the sentence
// rather than in the shared layer. A surface reporting the same fact in a
// structured payload reads the count off the result and renders nothing.
func trustedPluginsSummary(recorded int) string {
	if recorded == 0 {
		return ""
	}
	return fmt.Sprintf(", trusted_plugins=%d", recorded)
}
