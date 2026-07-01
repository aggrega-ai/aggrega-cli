// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
)

var (
	releasePath  string
	catalogDirs  []string
	traceEnabled bool
	buildDetail  bool
	buildQuiet   bool
	buildDebug   bool
)

func newBuildCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "build",
		Short: "Compile Aggregate + Release into Helm artifacts via the 5-step pipeline",
		RunE:  runBuild,
	}
	c.Flags().StringVar(&releasePath, "release", "", "Path to Release YAML file (required)")
	c.Flags().StringArrayVar(&catalogDirs, "catalog", nil, "Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	c.Flags().BoolVar(&traceEnabled, "trace", false, "write per-node intermediate files to .trace/ (sibling to dist/) for post-mortem inspection")
	c.Flags().BoolVar(&buildDetail, "detail", false, "When build performs the implicit auto-pull (no lockfile present, auto_pull enabled), print per resolved entity what that pull did — fetched fresh from a registry, reused a cache slot, found in a local catalog, or external. Stdout only; not persisted. No-op when a lockfile already exists (no pull this build)")
	c.Flags().BoolVar(&buildQuiet, "quiet", false, "Silence the per-phase build-progress line on stderr. Does not affect stdout")
	c.Flags().BoolVar(&buildDebug, "debug", false, "Emit human-readable verbose diagnostics on stderr — per-phase wall-clock timing and entity/closure counts. Wins over --quiet: verbose output always emits. Distinct from --trace, which writes machine-readable per-node intermediate files to .trace/")
	if err := c.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	return c
}

// runBuild is the cobra entry point. It reads the flag-bound package
// globals and delegates to dispatchBuild, which composes catalog loading,
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
func runBuild(cmd *cobra.Command, args []string) error {
	rw, err := workspaceFromContext(cmd.Context())
	if err != nil {
		return err
	}
	return dispatchBuildWithDiag(rw, releasePath, catalogDirs, traceEnabled,
		buildDiagOptions{quiet: buildQuiet, debug: buildDebug}, os.Stderr, os.Stdout)
}

// dispatchBuild is the silent-diagnostics form of dispatchBuildWithDiag: it runs
// with no phase-progress surface and no verbose debug layer. Tests call it
// directly (passing a nil workspace, which resolves from the working directory)
// to avoid racing on flag-bound globals under t.Parallel; the CLI entry point
// calls dispatchBuildWithDiag to thread the operator's --quiet / --debug
// choices. Both share the single implementation below.
func dispatchBuild(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, diagOut, out io.Writer) error {
	return dispatchBuildWithDiag(rw, releasePath, catalogFlagDirs, traceEnabled, buildDiagOptions{}, diagOut, out)
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
func dispatchBuildWithDiag(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, traceEnabled bool, diag buildDiagOptions, diagOut, out io.Writer) error {
	progress := newProgressReporter(diagOut, diag)
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
	cacheDir := wsCfg.ResolvedArtifactCacheDir()

	// Effective first-win catalog search stack: --catalog dirs prepended to the
	// workspace catalog_dirs, defaulting to the current directory.
	dirs := resolveCatalogDirs(catalogFlagDirs, wsCfg, ".")

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
	if buildDetail {
		reportCatalogShadows(cat, diagOut)
	}

	// Ensure the per-Release lockfile exists (auto-pull when missing + permitted),
	// returning whether it pre-existed — drift is verified only for a pre-existing
	// lockfile; a freshly auto-pulled one matches the closure by construction. The
	// flag dirs (not the resolved list) are forwarded so the pull path re-derives
	// the identical stack with the same config.
	progress.phase(phaseLockfile)
	lockfilePreexisted, err := acquireLockfile(rw, releasePath, catalogFlagDirs, wsCfg, buildDetail, diagOut)
	if err != nil {
		return err
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		return fmt.Errorf("load lockfile: %w", err)
	}

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
	// pin, and compute the closure once. lint runs the identical front door, so
	// the sequence lives in releaseClosure and cannot drift between them. The
	// build's manifest-implements consistency halt runs via afterManifest so it
	// still fires before the closure walk — the earliest loud point. An in-closure
	// entity skipped at tolerant load surfaces as an unresolvable URI;
	// withSkipContext replays WHY it was skipped so the failure is never a bare
	// "not found".
	progress.phase(phaseClosure)
	rc, err := releaseClosure(releasePath, lazyView, lf, wsCfg, wsRoot, cacheDir,
		func(manifest *types.Manifest, manifestPath string) error {
			return lazyView.AssertManifestImplementsConsistency(manifest, manifestPath)
		})
	if err != nil {
		return withSkipContext(err, skipped)
	}
	rel := rc.Release
	manifest := rc.Manifest
	aggRes := rc.Aggregate
	aggBytes := aggRes.Bytes
	cl := rc.Closure
	progress.note("closure entities: %d", len(cl.Entities))
	progress.note("schema libraries: %d, mapping libraries: %d", len(cl.SchemaLibraries), len(cl.MappingLibraries))

	// Closure-scoped Pass-2: validate ONLY the entities the Release reaches against
	// the closure's libraries (resolved through the lazy lookup). An in-closure
	// entity that is schema-invalid — or whose $ref names a library skipped (broken)
	// or missing — fails loudly here (the hard in-closure library reject); an
	// out-of-closure break is never validated.
	progress.phase(phaseValidate)
	if err := lazyView.ValidateClosureScopedPillars(cl); err != nil {
		return withSkipContext(err, skipped)
	}

	// Lockfile-drift verification against the freshly-computed closure. Only a
	// pre-existing lockfile is verified; an auto-pulled one matches by construction.
	if lockfilePreexisted {
		if err := lockfile.Verify(lf, cl); err != nil {
			return fmt.Errorf("%w; run `aggrega pull --regenerate %s` to refresh the lockfile against current catalog state",
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

	// Deprecated-entity policy gate: evaluate each closure entity's deprecation
	// status from the retroactive lockfile marker and the author-time
	// metadata.deprecated flag under one build-time path, halting or advising per
	// the workspace build policy. Reads only the lockfile and resolved entity
	// metadata — no registry access (the ahead-of-time boundary).
	if err := enforceClosureDeprecation(cl, lf, lazyView, wsCfg.Build, diagOut); err != nil {
		return err
	}

	// Surface non-fatal schema-pillar-overlap advisories for the build's closure
	// entities (registry-pinned included) — the warning-level sibling of the
	// closure-scoped pillar validation above, over the same closure-entity set.
	for _, w := range lazyView.ClosureLintWarnings(cl) {
		fmt.Fprintf(diagOut, "  notice: %s\n", w)
	}

	// Provider dispatch is the shared enginefactory.Dispatch — the single
	// source of truth both the CLI build and the build-faithful test harness
	// use, so a test renders through exactly the engines a real build does.
	progress.phase(phaseRender)
	relAggPath := catalog.RelativeToWorkspace(wsRoot, aggRes.Path)
	buildLookup := lazyView.AsCatalogLookup()
	buildLookup.ProviderInfraSchema = lazyView.ProviderInfraSchemaResolver(manifest)
	result, err := pipeline.RunPipeline(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     relAggPath,
			WorkspaceConfig:   wsCfg,
			Lockfile:          lf,
			WorkspaceRoot:     wsRoot,
			MappingLibraries:  lazyView.MappingLibraries(),
			CompilerVersion:   provenanceCompilerVersion(),
		},
		buildLookup,
		lazyView.RecipeLookupWithManifest(manifest),
		lazyView.SubAggregateLoader(),
		enginefactory.Dispatch,
		pipeline.Options{
			IncludeTrace:             traceEnabled,
			RecipeResolutionVerifier: lazyView.VerifyRecipeResolution,
			// Per-node render progress. The callback writes only to the progress
			// reporter's diagnostic sink (stderr); stdout carries only the final
			// summary, so rendered output stays byte-deterministic. A no-op
			// reporter (quiet, no debug) ignores the calls.
			NodeProgress: progress.renderNode,
		},
	)
	if err != nil {
		return err
	}

	// Write output. Clean dist/boundaries/ first so stale files from a prior
	// build of a different variant don't leak into this one (GitOps consumers
	// must see exactly what the compiler just produced).
	outDir := filepath.Join("dist")
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

	// Clear the in-place progress line (and, in debug mode, print the timing
	// total) before the stdout summary so the progress surface never lingers
	// beside the final output. The deferred finish is idempotent.
	progress.finish()
	fmt.Fprintf(out, "Build complete: %d files written to %s/\n", len(result.Artifacts), outDir)
	return nil
}

// acquireLockfile ensures the per-Release lockfile exists before the build wires
// its lazy lookup + runs closure-scoped Pass-2, returning whether it PRE-EXISTED.
// A present lockfile is left as-is — drift against it is verified later, against
// the resolved closure. A missing lockfile auto-pulls when workspace.auto_pull is
// true (the default), populating the lockfile + cache in-process; under offline,
// or with auto_pull false, it halts with the pull-first remediation (offline
// ignores auto_pull because the pull itself needs network access).
//
// The lockfile must exist before the lazy lookup is pinned: its pins drive the
// verify-on-read Tier-2 reads through which every closure entity (and the
// libraries a $ref names) resolves.

// lockfileHasRegistryPin reports whether the lockfile pins uri as a registry
// entity — one carrying a real resolved version. A version-less pin marks an
// external (local) top-level; a real version marks one registry-resolved at pull
// that must resolve at build through the lazy lookup's pinned Tier-2 read, not the
// external tier (which would re-pin version "" and drift against the lockfile).
func lockfileHasRegistryPin(lf *lockfile.Lockfile, uri string) bool {
	if lf == nil {
		return false
	}
	for _, e := range lf.Entities {
		if e.URI == uri {
			return e.ResolvedVersion != ""
		}
	}
	return false
}

// acquireLockfile writes any diagnostics (the no-pull --detail note, and the
// auto-pull's reuse/verify notices forwarded through pullConfig.DiagOut) to
// diagOut (os.Stderr in production).
func acquireLockfile(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, wsCfg workspaceconfig.Config, detail bool, diagOut io.Writer) (bool, error) {
	lockfilePath := lockfile.PathFor(releasePath)

	_, statErr := os.Stat(lockfilePath)
	preexisted := statErr == nil
	if statErr != nil && !stderrors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("lockfile stat %s: %w", lockfilePath, statErr)
	}

	// Offline: never network. Build is frozen against whatever lockfile exists;
	// a missing one cannot be filled here.
	if wsCfg.Offline {
		if preexisted {
			return true, nil
		}
		return false, fmt.Errorf("aggrega build: lockfile %s missing and workspace is offline; run `aggrega pull %s` from a connected environment first: %w",
			lockfilePath, releasePath, oci.ErrFetchNotFound)
	}

	// build's auto-pull IS the default (lockfile-honoring) pull — no separate build
	// flag, no float. It honors every existing pin (resolving to the pinned version,
	// verifying the pinned digest, halting on a drifted pin), resolves + adds
	// un-pinned references, refills an empty cache by pin, and prunes stale pins. On
	// a complete, cache-populated lockfile it is a no-op with NO network (every pin a
	// Tier-2 hit) — so the build stays hermetic given a complete lockfile, with
	// auto_pull the fill escape hatch; only `pull --regenerate` floats. There is no
	// absent-vs-partial special case: any incomplete/empty state runs the same
	// honoring pull, and lockfile.Verify (in dispatchBuild) is the post-condition.
	// The build's own resolution runs through the nil-puller pinned lazy lookup,
	// untouched — so build output is unchanged by this fill.
	if wsCfg.AutoPull {
		if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: catalogFlagDirs, Detail: detail, ws: rw, DiagOut: diagOut}); err != nil {
			return false, fmt.Errorf("auto-pull: %w", err)
		}
		return false, nil
	}

	// auto_pull is false: build is frozen against the lockfile as it stands.
	if preexisted {
		if detail {
			fmt.Fprintf(diagOut, "aggrega build --detail: resolution detail is a pull-time surface; lockfile %s already present and auto_pull is off, so no pull ran this build\n", lockfilePath)
		}
		return true, nil
	}
	return false, fmt.Errorf("aggrega build: lockfile %s missing and auto_pull is false; run `aggrega pull %s` first",
		lockfilePath, releasePath)
}

// releaseClosureResult carries the resolved entities and the computed closure
// the shared Release→closure orchestration produces. The Manifest is the
// extends-composed result (not the child's own entries); the Aggregate carries
// its raw bytes + path for the caller's downstream pipeline call.
type releaseClosureResult struct {
	Release   *types.Release
	Manifest  *types.Manifest
	Aggregate resolver.ResolvedAggregate
	Closure   closure.Closure
}

// releaseClosure runs the Release→closure orchestration shared by the build and
// lint front doors: load the Release, resolve + compose the Manifest, resolve
// the Aggregate, gate the external aggregate seed on a registry pin, and compute
// the closure once. Both dispatchBuild and lintRelease call it so the sequence
// lives in one place and cannot drift between them.
//
// The helper ends at the closure: caller-specific setup (the lazyView and its
// pins/integrity policy) is supplied by the caller, and all post-closure work
// (validation, lockfile verification, extends, render) stays in the caller —
// the build and lint post-closure sequences differ deliberately.
//
// afterManifest, when non-nil, runs after the Manifest is composed and before
// the Aggregate resolves. The build injects its manifest-implements consistency
// halt there so that check still fires before the closure walk; lint passes nil.
// lf may be nil (lint tolerates a missing lockfile); the pin gate is skipped
// when it is, leaving the aggregate seeded external exactly as before.
func releaseClosure(
	releasePath string,
	lazyView *catalog.LazyLookup,
	lf *lockfile.Lockfile,
	wsCfg workspaceconfig.Config,
	wsRoot, cacheDir string,
	afterManifest func(manifest *types.Manifest, manifestPath string) error,
) (releaseClosureResult, error) {
	releaseDir := filepath.Dir(releasePath)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return releaseClosureResult{}, fmt.Errorf("load release: %w", err)
	}
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return releaseClosureResult{}, fmt.Errorf("load manifest: %w", err)
	}
	// Compose the Manifest's extends chain before any downstream lookup reads its
	// mappings, so the recipe cascade and resolution-completeness pass operate on
	// the inherited result. A Manifest with no extends is returned unchanged.
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		return releaseClosureResult{}, fmt.Errorf("compose manifest extends: %w", err)
	}
	if afterManifest != nil {
		if err := afterManifest(manifest, manRes.Path); err != nil {
			return releaseClosureResult{}, err
		}
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, lazyView.AggregateLookup(), nil)
	if err != nil {
		return releaseClosureResult{}, fmt.Errorf("load aggregate: %w", err)
	}
	// A registry-pinned top-level resolves through the lazy lookup (Tier-1 / pinned
	// Tier-2); seeding it external — externals are consulted first — would shadow it
	// with a version-less pin and drift. Gate the external seed on the pin; the
	// manifest is always external (it has no registry tier).
	aggExternal := aggRes.Bytes
	if lf != nil && lockfileHasRegistryPin(lf, rel.Spec.Aggregate) {
		aggExternal = nil
	}
	externals := lockfile.ExternalEntitiesFromBytes(rel, aggExternal, manRes.Bytes)

	cl, err := computeBuildClosure(rel, manifest, aggRes.Aggregate, externals, wsCfg, lazyView, wsRoot, cacheDir)
	if err != nil {
		return releaseClosureResult{}, err
	}
	return releaseClosureResult{Release: rel, Manifest: manifest, Aggregate: aggRes, Closure: cl}, nil
}

// computeBuildClosure walks the Release's closure ONCE, hermetically (the lazy
// lookup carries a nil OCIPuller, so Tier 3 never fires at build time), and
// resolves the cross-document SchemaLibrary + MappingLibrary axes the entity walk
// does not descend. The single closure is reused for closure-scoped validation,
// lockfile-drift verification, and closure-scoped extends — so the closure is
// walked once, not once per concern. Discovery runs through the lazy lookup (the
// same producer pull uses), so a remote pinned library resolves from the pinned
// cache and the drift compare is like-for-like with pull.
func computeBuildClosure(rel *types.Release, manifest *types.Manifest, aggregate *types.Aggregate, externals map[string][]byte, wsCfg workspaceconfig.Config, lazyView *catalog.LazyLookup, wsRoot, cacheDir string) (closure.Closure, error) {
	lookup := lazyView.ClosureLookup(nil, wsRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, wsCfg, lookup)
	if err != nil {
		return closure.Closure{}, err
	}
	if err := discoverClosureLibraries(&cl, lazyView, lookup); err != nil {
		return closure.Closure{}, fmt.Errorf("schema-library discovery: %w", err)
	}
	if err := discoverClosureMappingLibraries(&cl, lazyView, lookup); err != nil {
		return closure.Closure{}, fmt.Errorf("mapping-library discovery: %w", err)
	}
	return cl, nil
}

// withSkipContext annotates an in-closure resolution / validation failure with
// the catalog files the TOLERANT Pass-1 load skipped, so a tolerated skip never
// hides WHY an entity the Release reaches went missing. A skipped file whose
// header named the kind+name is reported with that identity; a file too malformed
// to peek is reported by path. No-op when nothing was skipped or err is nil.
func withSkipContext(err error, skipped []catalog.SkippedEntity) error {
	if err == nil || len(skipped) == 0 {
		return err
	}
	var b strings.Builder
	b.WriteString(err.Error())
	b.WriteString("\n  note: catalog files skipped at load (one may be the unresolved in-closure entity):")
	for _, s := range skipped {
		switch {
		case s.Kind != "" && s.Name != "":
			fmt.Fprintf(&b, "\n    - %s %q (%s): %s", s.Kind, s.Name, s.Path, s.Err)
		default:
			fmt.Fprintf(&b, "\n    - %s: %s", s.Path, s.Err)
		}
	}
	return fmt.Errorf("%s", b.String())
}

// writeArtifacts writes the rendered file map to disk under outRoot,
// preserving relative path structure and creating intermediate
// directories as needed. Each entry's relative path is cleaned and
// validated:
//
//   - Empty paths are rejected (defensive guard against renderer
//     producing a zero-key entry).
//   - Path-traversal attempts ("..", or paths beginning with "../")
//     are rejected. The check defends against a malicious or buggy
//     renderer engine emitting a path that escapes outRoot.
//
// On any per-file error (mkdir, write), the function returns the
// wrapped error and stops — partial writes to disk persist; the
// caller is responsible for any cleanup in the partial-write case.
func writeArtifacts(outRoot string, files map[string][]byte) error {
	outRoot = filepath.Clean(outRoot)
	for relPath, content := range files {
		if relPath == "" {
			return fmt.Errorf("empty artifact path")
		}
		relPath = filepath.Clean(relPath)
		if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid artifact path %q", relPath)
		}
		full := filepath.Clean(filepath.Join(outRoot, relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return fmt.Errorf("write %q: %w", full, err)
		}
	}
	return nil
}
