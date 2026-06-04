// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	renderCompose "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/compose"
	renderHelm "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	renderHelmChart "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
)

var (
	releasePath  string
	catalogDir   string
	traceEnabled bool
)

func newBuildCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "build",
		Short: "Compile Aggregate + Release into Helm artifacts via the 5-step pipeline",
		RunE:  runBuild,
	}
	c.Flags().StringVar(&releasePath, "release", "", "Path to Release YAML file (required)")
	c.Flags().StringVar(&catalogDir, "catalog", ".", "Directory containing catalog entities")
	c.Flags().BoolVar(&traceEnabled, "trace", false, "write per-node intermediate files to .trace/ (sibling to dist/) for post-mortem inspection")
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
	return dispatchBuild(releasePath, catalogDir, traceEnabled)
}

// dispatchBuild composes the filesystem-touching pre-pipeline steps
// (catalog load, lockfile verify-or-auto-pull, extends resolution,
// deprecation surface, raw Aggregate read, workspace config load) around
// pipeline.RunPipeline, then writes the rendered Artifacts to dist/
// and (when --trace is on) the Trace map to .trace/. The in-memory
// pipeline orchestration lives in internal/compiler/pipeline; tests
// call dispatchBuild directly to avoid racing on flag-bound globals
// under t.Parallel.
func dispatchBuild(releasePath, catalogDir string, traceEnabled bool) error {
	// Load catalog from directory. wsRoot is the workspace the caller
	// invoked the build from; the catalog renders source-file accessors
	// relative to it so the IR's per-node SourceFile records stay
	// byte-identical across developer machines whose workspaces live at
	// different absolute paths.
	wsRoot, _ := os.Getwd()

	// Load workspace config up front: the catalog walk skips the configured
	// cache directory (a resolution artifact, not authored catalog), and the
	// pipeline call below reuses wsCfg.
	wsCfg, err := workspaceconfig.Load(workspaceConfigPath())
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	cacheDir := wsCfg.Workspace.ResolvedCacheDir()

	// Pass-1 catalog load. Determines the local library set (which lockfile-pinned
	// libraries are already authored locally and must NOT be bridged from the
	// cache) and serves the auto-pull closure walk. Pass 1 alone does not expand
	// cross-document $ref pillars, so a reference to a pulled remote library does
	// not fail here — which is exactly why the lockfile + library bridge must run
	// before Pass-2 pillar validation.
	passOne, err := catalog.LoadPassOne(wsRoot, cacheDir, catalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	// Ensure the per-Release lockfile exists (auto-pull when missing + permitted),
	// returning whether it pre-existed — drift is verified only for a pre-existing
	// lockfile; a freshly auto-pulled one matches the closure by construction.
	lockfilePreexisted, err := acquireLockfile(releasePath, catalogDir, wsCfg)
	if err != nil {
		return err
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		return fmt.Errorf("load lockfile: %w", err)
	}

	// Bridge lockfile-pinned remote libraries into the catalog BEFORE Pass-2, so a
	// cross-document $ref to a pulled remote SchemaLibrary (in the cache + pinned,
	// absent from the local walk) resolves at pillar validation. A locally-authored
	// library has a pin but no cache slot, so only NON-LOCAL pins are bridged —
	// reading a local pin would miss the cache. The bridged libraries pass through
	// the same Pass-2 reject gate as local ones (LoadWithExtraLibraries runs Pass-2
	// over the merged set).
	extraLibs := map[string][]byte{}
	for _, pin := range lf.SchemaLibraries {
		if _, local := passOne.SchemaLibraries[pin.URI]; local {
			continue
		}
		data, rerr := catalog.ReadPinnedLibrary(wsRoot, cacheDir, pin.URI, pin.ResolvedVersion, pin.Digest)
		if rerr != nil {
			return fmt.Errorf("bridge pinned SchemaLibrary %s: %w", pin.URI, rerr)
		}
		extraLibs[pin.URI] = data
	}
	extraMappings := map[string][]byte{}
	for _, pin := range lf.MappingLibraries {
		if _, local := passOne.MappingLibraries[pin.URI]; local {
			continue
		}
		data, rerr := catalog.ReadPinnedLibrary(wsRoot, cacheDir, pin.URI, pin.ResolvedVersion, pin.Digest)
		if rerr != nil {
			return fmt.Errorf("bridge pinned MappingLibrary %s: %w", pin.URI, rerr)
		}
		extraMappings[pin.URI] = data
	}
	cat, warnings, err := catalog.LoadWithExtraLibraries(wsRoot, cacheDir, extraLibs, extraMappings, catalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}

	// Lockfile-drift verification against the BRIDGED catalog. The bridged remote
	// libraries resolve at Tier-1 with their pinned content digest (entityBytes
	// carry the exact verified bytes), so the freshly-computed closure is compared
	// faithfully to the lockfile and halts on any (URI, version, digest) drift with
	// ErrLockfileDrift. Hermetic: the closure lookup injects a nil OCIPuller, so
	// Tier 3 never fires at build time. Only a pre-existing lockfile is verified;
	// an auto-pulled one matches by construction.
	if lockfilePreexisted {
		if err := verifyLockfileAgainstClosure(releasePath, catalogDir, lockfile.PathFor(releasePath), wsCfg, cat); err != nil {
			return err
		}
	}

	// Resolve extends inheritance for every Component and Feature so downstream
	// guards/renderer see the fully-merged entity (inherited labels, secretNames,
	// usesSchema, composed schema).
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(os.Stderr, "  extends: %s\n", e)
		}
		return fmt.Errorf("catalog extends: %d resolution error(s)", len(extErrs))
	}

	// Surface non-fatal deprecation notices (e.g. legacy secretNames usage).
	for _, w := range cat.DeprecationWarnings() {
		fmt.Fprintf(os.Stderr, "  deprecation: %s\n", w)
	}

	// Surface non-fatal lint advisories from catalog load (e.g.
	// schema-pillar-overlap warnings).
	for _, w := range cat.LintWarnings() {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}

	// Load Release + derive Aggregate + Manifest paths.
	releaseDir := filepath.Dir(releasePath)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	aggPath := filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml")
	manifestPath := filepath.Join(releaseDir, rel.Spec.Manifest+".yaml")
	manifest, err := resolver.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		return fmt.Errorf("read aggregate: %w", err)
	}

	// engineFactory dispatches recipe.spec.provider to a fresh-per-call
	// renderer.Engine. Three provider names ship:
	//
	//   - "helm" — fresh HelmEngine per call. The empty-string branch
	//     also routes here, preserved as a defensive default; current
	//     Recipe schema requires spec.provider, so empty values do
	//     not reach this dispatch in normal flow.
	//   - "helm-chart" — fresh helmchart.HelmEngine per call.
	//   - "compose" — fresh ComposeEngine per call. Per-(path, provider)
	//     dispatch grouping in the orchestrator (BoundaryAssembler tier)
	//     handles cross-call aggregation; the engine itself is stateless.
	//
	// Unknown providerName wraps renderer.ErrUnknownProvider with the
	// offending name so the upstream RenderAll error surfaces with
	// dispatch failure context intact.
	engineFactory := func(providerName string) (renderer.Engine, error) {
		switch providerName {
		case "helm", "":
			return &renderHelm.HelmEngine{}, nil
		case "helm-chart":
			return &renderHelmChart.HelmEngine{}, nil
		case "compose":
			return &renderCompose.ComposeEngine{}, nil
		default:
			return nil, fmt.Errorf("%w: %q", renderer.ErrUnknownProvider, providerName)
		}
	}

	relAggPath := catalog.RelativeToWorkspace(wsRoot, aggPath)
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
			MappingLibraries:  cat.MappingLibraries,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		engineFactory,
		pipeline.Options{
			IncludeTrace:             traceEnabled,
			RecipeResolutionVerifier: cat.VerifyRecipeResolution,
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

	fmt.Printf("Build complete: %d files written to %s/\n", len(result.Artifacts), outDir)
	return nil
}

// acquireLockfile ensures the per-Release lockfile exists before the build's
// library bridge + Pass-2 run, returning whether it PRE-EXISTED. A present
// lockfile is left as-is — drift against it is verified later, against the
// bridged catalog. A missing lockfile auto-pulls when workspace.auto_pull is true
// (the default), populating the lockfile + cache in-process; under offline, or
// with auto_pull false, it halts with the pull-first remediation (offline ignores
// auto_pull because the pull itself needs network access).
//
// Splitting acquisition from drift-verification is what lets the build load the
// lockfile, bridge the pinned remote libraries it names into the catalog, and
// only then verify drift against the merged catalog: a remote $ref cannot be
// expanded at Pass-2 until its library is bridged, and the library cannot be
// bridged until the lockfile that pins it is acquired.
func acquireLockfile(releasePath, catalogDir string, wsCfg workspaceconfig.Config) (bool, error) {
	lockfilePath := lockfile.PathFor(releasePath)

	_, statErr := os.Stat(lockfilePath)
	switch {
	case statErr == nil:
		return true, nil
	case stderrors.Is(statErr, os.ErrNotExist):
		// fall through to auto-pull / halt decision below
	default:
		return false, fmt.Errorf("lockfile stat %s: %w", lockfilePath, statErr)
	}

	if wsCfg.Workspace.Offline {
		return false, fmt.Errorf("aggrega build: lockfile %s missing and workspace is offline; run `aggrega pull %s` from a connected environment first: %w",
			lockfilePath, releasePath, oci.ErrFetchNotFound)
	}
	if !wsCfg.Workspace.AutoPull {
		return false, fmt.Errorf("aggrega build: lockfile %s missing and auto_pull is false; run `aggrega pull %s` first",
			lockfilePath, releasePath)
	}

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDir: catalogDir}); err != nil {
		return false, fmt.Errorf("auto-pull: %w", err)
	}
	return false, nil
}

// verifyLockfileAgainstClosure loads the per-Release lockfile and
// compares it to a freshly-computed closure (Tier-1 + Tier-2 only —
// the injected OCIPuller is nil so Tier 3 never runs).
func verifyLockfileAgainstClosure(releasePath, catalogDir, lockfilePath string, wsCfg workspaceconfig.Config, cat *catalog.Catalog) error {
	lf, diags, err := lockfile.LoadLockfile(lockfilePath)
	if err != nil {
		return fmt.Errorf("load lockfile %s: %w", lockfilePath, err)
	}
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "  lockfile: %s\n", d.Message)
	}

	releaseDir := filepath.Dir(releasePath)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	manifest, err := resolver.LoadManifest(filepath.Join(releaseDir, rel.Spec.Manifest+".yaml"))
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	aggregate, err := resolver.LoadAggregate(filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml"))
	if err != nil {
		return fmt.Errorf("load aggregate: %w", err)
	}

	workspaceRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("workspace root: %w", err)
	}
	cacheDir := wsCfg.Workspace.ResolvedCacheDir()

	externals := lockfile.LoadExternalEntities(rel,
		filepath.Join(releaseDir, rel.Spec.Aggregate+".yaml"),
		filepath.Join(releaseDir, rel.Spec.Manifest+".yaml"))
	lookup := cat.ClosureLookupWithExternals(nil, workspaceRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, wsCfg, lookup)
	if err != nil {
		return fmt.Errorf("closure: %w", err)
	}
	// Discover + resolve the cross-document SchemaLibrary axis against the BRIDGED
	// catalog (the same shared producer pull's pin flow uses), so the recomputed
	// closure includes the bridged libraries and lockfile.Verify compares like
	// for like — no spurious drift on a remote pinned library.
	if err := discoverClosureLibraries(&cl, cat, lookup); err != nil {
		return fmt.Errorf("closure: schema-library discovery: %w", err)
	}
	if err := discoverClosureMappingLibraries(&cl, cat, lookup); err != nil {
		return fmt.Errorf("closure: mapping-library discovery: %w", err)
	}

	if err := lockfile.Verify(lf, cl); err != nil {
		return fmt.Errorf("%w; run `aggrega pull --regenerate %s` to refresh the lockfile against current catalog state",
			err, releasePath)
	}
	return nil
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
