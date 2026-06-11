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
	catalogDir   string
	traceEnabled bool
	buildDetail  bool
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
	c.Flags().BoolVar(&buildDetail, "detail", false, "When build performs the implicit auto-pull (no lockfile present, auto_pull enabled), print per resolved entity what that pull did — fetched fresh from a registry, reused a cache slot, found in a local catalog, or external. Stdout only; not persisted. No-op when a lockfile already exists (no pull this build)")
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

	// Tolerant Pass-1 catalog load. A file that fails to read, parse, or index is
	// SKIPPED (recorded) rather than aborting the walk — so a broken catalog entity
	// OUTSIDE this Release's closure never fails the build. The closure walk +
	// closure-scoped Pass-2 below validate exactly what the Release reaches;
	// catalog-wide validation is `aggrega lint`'s job. Pass 1 does not expand
	// cross-document $ref pillars, so a reference to a not-yet-bridged remote
	// library does not fail here.
	cat, skipped, err := catalog.LoadPassOneTolerant(wsRoot, cacheDir, catalogDir)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}

	// Ensure the per-Release lockfile exists (auto-pull when missing + permitted),
	// returning whether it pre-existed — drift is verified only for a pre-existing
	// lockfile; a freshly auto-pulled one matches the closure by construction.
	lockfilePreexisted, err := acquireLockfile(releasePath, catalogDir, wsCfg, buildDetail)
	if err != nil {
		return err
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		return fmt.Errorf("load lockfile: %w", err)
	}

	// Bridge lockfile-pinned remote libraries into the catalog (merge only — the
	// authoritative gate is the closure-scoped Pass-2 below, not a whole-catalog
	// pass). A locally-authored library has a pin but no cache slot, so only
	// NON-LOCAL pins are bridged. ReadPinnedLibrary verifies each against its pin
	// and HALTS on an integrity mismatch, so a tampered in-closure library is loud,
	// never silently dropped.
	extraLibs := map[string][]byte{}
	for _, pin := range lf.SchemaLibraries {
		if _, local := cat.SchemaLibraries[pin.URI]; local {
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
		if _, local := cat.MappingLibraries[pin.URI]; local {
			continue
		}
		data, rerr := catalog.ReadPinnedLibrary(wsRoot, cacheDir, pin.URI, pin.ResolvedVersion, pin.Digest)
		if rerr != nil {
			return fmt.Errorf("bridge pinned MappingLibrary %s: %w", pin.URI, rerr)
		}
		extraMappings[pin.URI] = data
	}
	bridgeWarnings, err := cat.MergeExtraLibraries(extraLibs, extraMappings)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	for _, w := range bridgeWarnings {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}

	// Bridge lockfile-pinned, cache-backed aggregates into the catalog so the
	// triad resolver and the topology builder resolve a registry-pulled
	// aggregate at build, from the cache, hermetically (no build-time network).
	// The discriminator is build-machine-local catalog presence: read every
	// lockfile entity's verified cache bytes; an absent slot (ErrFetchNotFound)
	// is a catalog-resident entity (read from the local catalog, not the cache)
	// and is skipped; a digest mismatch halts. MergeExtraAggregates dispatches
	// by kind — folding only Aggregates, local-wins on a name collision — so the
	// dropped per-entry resolution tier is never consulted.
	buildPins := make([]aggregateBridgePin, 0, len(lf.Entities))
	for _, ent := range lf.Entities {
		buildPins = append(buildPins, aggregateBridgePin{URI: ent.URI, Version: ent.ResolvedVersion, Digest: ent.Digest})
	}
	if err := bridgeCachedAggregates(cat, wsRoot, cacheDir, buildPins); err != nil {
		return err
	}

	// Load Release + derive Aggregate + Manifest paths. Loaded before the closure
	// walk (which they feed) and reused by the pipeline below.
	releaseDir := filepath.Dir(releasePath)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	// Compose the Manifest's extends chain before any downstream lookup reads
	// its mappings, so the recipe cascade and resolution-completeness pass
	// operate on the inherited result rather than the child's own entries
	// alone. A Manifest with no extends reference is returned unchanged.
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		return fmt.Errorf("compose manifest extends: %w", err)
	}
	// Verify the Manifest's mapping table is consistent — each mapped type
	// resolves to a recipe whose spec.implements matches — before any IR build.
	// A mismatch is a Loud Guardrail halt here, the earliest loud point.
	if err := cat.AssertManifestImplementsConsistency(manifest, manRes.Path); err != nil {
		return err
	}
	// Build never networks: the registry tier (Tier-3) is nil here, so a
	// registry-published top-level resolves only through the cache→catalog bridge
	// (folded into cat.Aggregates above) via catLookup.
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, cat.AggregateLookup(), nil)
	if err != nil {
		return fmt.Errorf("load aggregate: %w", err)
	}
	aggregate := aggRes.Aggregate
	aggBytes := aggRes.Bytes
	// A registry-pinned top-level (a real resolved version in the lockfile)
	// resolves via the bridge + tier-1; seeding it external — externals are
	// consulted before the catalog — would shadow the bridge with a version-less
	// pin and drift. Gate the external seed on the pin; the manifest is always
	// external (it has no registry tier).
	aggExternal := aggRes.Bytes
	if lockfileHasRegistryPin(lf, rel.Spec.Aggregate) {
		aggExternal = nil
	}
	externals := lockfile.ExternalEntitiesFromBytes(rel, aggExternal, manRes.Bytes)

	// Compute the Release's closure ONCE — hermetic (nil puller, Tier-3 disabled),
	// against the bridged catalog. Reused for closure-scoped validation, the
	// lockfile-drift check, and extends resolution. Out-of-closure entities are
	// never resolved here; an in-closure entity that was skipped at tolerant load
	// surfaces as an unresolvable URI, and withSkipContext replays WHY it was
	// skipped so the failure is never a bare "not found".
	cl, err := computeBuildClosure(rel, manifest, aggregate, externals, wsCfg, cat, wsRoot, cacheDir)
	if err != nil {
		return withSkipContext(fmt.Errorf("closure: %w", err), skipped)
	}

	// Closure-scoped Pass-2: validate ONLY the entities the Release reaches against
	// the closure's libraries (local + bridged). An in-closure entity that is
	// schema-invalid — or whose $ref names a library skipped (broken) or missing —
	// fails loudly here (the hard in-closure library reject); an out-of-closure
	// break is never validated.
	if err := cat.ValidateClosureScopedPillars(cl); err != nil {
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
	if extErrs := cat.ResolveClosureExtends(cl); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(os.Stderr, "  extends: %s\n", e)
		}
		return withSkipContext(fmt.Errorf("catalog extends: %d resolution error(s)", len(extErrs)), skipped)
	}

	// Surface non-fatal deprecation notices about deprecated catalog shapes.
	for _, w := range cat.DeprecationWarnings() {
		fmt.Fprintf(os.Stderr, "  deprecation: %s\n", w)
	}

	// Surface non-fatal advisory notices — schema-pillar-overlap lint and the
	// signal-only narrowing-on-extend notices (a child tightening an inherited
	// constraint is reported, never halted — narrowing enforcement is the
	// opt-in publishing linter's job).
	for _, w := range cat.LintWarnings() {
		fmt.Fprintf(os.Stderr, "  notice: %s\n", w)
	}

	// Provider dispatch is the shared enginefactory.Dispatch — the single
	// source of truth both the CLI build and the build-faithful test harness
	// use, so a test renders through exactly the engines a real build does.
	relAggPath := catalog.RelativeToWorkspace(wsRoot, aggRes.Path)
	buildLookup := cat.CatalogLookup()
	buildLookup.ProviderInfraSchema = cat.ProviderInfraSchemaResolver(manifest)
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
		buildLookup,
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		enginefactory.Dispatch,
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
// aggregateBridgePin is the minimal pin shape the cache→catalog aggregate
// bridge needs — satisfied by a committed lockfile.Entity (build) and an
// in-memory closure.ResolvedRef (pull).
type aggregateBridgePin struct {
	URI     string
	Version string
	Digest  string
}

// bridgeCachedAggregates folds lockfile-pinned, cache-present Aggregates into the
// catalog so the triad resolver and the topology SubAggregateLoader resolve a
// registry-pulled aggregate from the cache. Shared by build (over the committed
// lockfile) and pull (over the freshly-computed closure, so pull's own
// source-descent topology flatten can resolve a registry-only sub it just
// pinned). An absent cache slot is a catalog-resident entity, skipped; a digest
// mismatch halts; MergeExtraAggregates folds only the Aggregate kind, local-wins
// on a name collision.
func bridgeCachedAggregates(cat *catalog.Catalog, wsRoot, cacheDir string, pins []aggregateBridgePin) error {
	extra := map[string][]byte{}
	for _, p := range pins {
		data, err := catalog.ReadPinnedEntity(wsRoot, cacheDir, p.URI, p.Version, p.Digest)
		if stderrors.Is(err, oci.ErrFetchNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("bridge pinned entity %s: %w", p.URI, err)
		}
		extra[p.URI] = data
	}
	warns, err := cat.MergeExtraAggregates(extra)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}
	return nil
}

// lockfileHasRegistryPin reports whether the lockfile pins uri as a registry
// entity — one carrying a real resolved version. A version-less pin marks an
// external (local) top-level; a real version marks one registry-resolved at pull
// that must resolve at build via the cache→catalog bridge, not the external tier
// (which would re-pin version "" and drift against the lockfile).
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

func acquireLockfile(releasePath, catalogDir string, wsCfg workspaceconfig.Config, detail bool) (bool, error) {
	lockfilePath := lockfile.PathFor(releasePath)

	_, statErr := os.Stat(lockfilePath)
	switch {
	case statErr == nil:
		// The lockfile already exists, so build does not pull this run. The
		// resolution diagnostic is a pull-time surface; with no pull there is
		// nothing to report, and re-deriving a tier the committed lockfile does
		// not carry would fabricate one. Note the absence rather than print
		// nothing, so --detail does not look silently broken.
		if detail {
			fmt.Fprintf(os.Stderr, "aggrega build --detail: resolution detail is a pull-time surface; lockfile %s already present, so no pull ran this build\n", lockfilePath)
		}
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

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDir: catalogDir, Detail: detail}); err != nil {
		return false, fmt.Errorf("auto-pull: %w", err)
	}
	return false, nil
}

// computeBuildClosure walks the Release's closure ONCE, hermetically (nil
// OCIPuller, so Tier 3 never fires at build time), against the bridged catalog,
// and resolves the cross-document SchemaLibrary + MappingLibrary axes the entity
// walk does not descend. The single closure is reused for closure-scoped
// validation, lockfile-drift verification, and closure-scoped extends — so the
// catalog is walked once, not once per concern. Discovery runs against the
// bridged catalog (the same producer pull uses) so a remote pinned library
// resolves at Tier-1 and the drift compare is like-for-like.
func computeBuildClosure(rel *types.Release, manifest *types.Manifest, aggregate *types.Aggregate, externals map[string][]byte, wsCfg workspaceconfig.Config, cat *catalog.Catalog, wsRoot, cacheDir string) (closure.Closure, error) {
	lookup := cat.ClosureLookupWithExternals(nil, wsRoot, cacheDir, externals)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, wsCfg, lookup)
	if err != nil {
		return closure.Closure{}, err
	}
	if err := discoverClosureLibraries(&cl, cat, lookup); err != nil {
		return closure.Closure{}, fmt.Errorf("schema-library discovery: %w", err)
	}
	if err := discoverClosureMappingLibraries(&cl, cat, lookup); err != nil {
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
