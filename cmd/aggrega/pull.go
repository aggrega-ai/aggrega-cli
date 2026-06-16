// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	pullReleasePath       string
	pullCatalogDirs       []string
	pullRegenerate        bool
	pullVerify            bool
	pullIncludePrerelease bool
	pullDetail            bool
)

func newPullCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "pull",
		Short: "Resolve catalog and source dependencies into the cache and write the per-Release lockfile",
		RunE: func(cmd *cobra.Command, args []string) error {
			if pullVerify && pullRegenerate {
				return fmt.Errorf("--verify and --regenerate are mutually exclusive")
			}
			return pullRun(pullConfig{
				ReleasePath:       pullReleasePath,
				CatalogDirs:       pullCatalogDirs,
				Regenerate:        pullRegenerate,
				Verify:            pullVerify,
				IncludePrerelease: pullIncludePrerelease,
				Detail:            pullDetail,
			})
		},
	}
	c.Flags().StringVar(&pullReleasePath, "release", "", "Path to Release YAML file (required)")
	c.Flags().StringArrayVar(&pullCatalogDirs, "catalog", nil, "Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	c.Flags().BoolVar(&pullRegenerate, "regenerate", false, "Discard any existing lockfile and rebuild from current registry state")
	c.Flags().BoolVar(&pullVerify, "verify", false, "Verify the existing lockfile against the current closure without writing; exits non-zero on drift or missing lockfile")
	c.Flags().BoolVar(&pullIncludePrerelease, "include-prerelease", false, "Reserved for future version-selection logic; currently a no-op")
	c.Flags().BoolVar(&pullDetail, "detail", false, "Print, per resolved entity, what this pull did — fetched fresh from a registry, reused a cache slot, found in a local catalog, or external. Stdout only; not written to the lockfile")
	if err := c.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	return c
}

// pullConfig is the input contract for the pull workflow. cobra-bound
// flag handling reads the command-line values into a pullConfig
// instance; build's auto-pull integration constructs one directly.
type pullConfig struct {
	ReleasePath string
	// CatalogDirs holds the repeatable --catalog flag directories (UNresolved):
	// pullRun prepends them to the workspace catalog_dirs via resolveCatalogDirs.
	// build's auto-pull integration forwards its own flag dirs so the pull path
	// re-derives the identical stack under the same config.
	CatalogDirs       []string
	Regenerate        bool
	Verify            bool
	IncludePrerelease bool
	Detail            bool

	// puller overrides the production OCI puller; tests inject an in-memory
	// implementation to exercise a remote-library fetch without the network. Nil
	// uses NewDefaultOCIPuller (which is nil under workspace.offline).
	puller catalog.OCIPuller

	// sourceResolver overrides the production source-resolver shim; tests inject
	// a recording/stub implementation to assert the source identity computed from
	// the materialized pillars without a real artifact fetch. Nil uses the shim.
	sourceResolver closure.SourceResolver
}

// pullRun executes the pull workflow against the supplied
// configuration. Loads workspace config + Release + Manifest +
// Aggregate, walks the catalog and source closures, and writes the
// per-Release lockfile.
//
// Returns wrapped errors on workspace-config validation failure,
// Release/Manifest/Aggregate load failure, closure walk failure
// (typically a missing catalog entity surfaced by closure.ErrUnresolvableURI),
// irEntityInfra derives the per-instance source infra for FeatureRecipe / secret
// entities from the IR render-set (graph.Spec) — the exact feature/secret set the
// renderer renders, assembled across all five tiers (Aggregate component/subsystem,
// Release boundary-wide/component, annotation-expanded) at IR-build. Reading the IR
// (rather than the topology or a Release-boundary walk, each short by tiers) is what
// makes the no-using-instance skip in source-descent congruent with rendering.
//
// Each entry is keyed by the Manifest-mapped FeatureRecipe URI — the verbatim
// string the closure records as the source-descent entity — so the returned map
// joins against closure entities. An instance whose type has no Manifest mapping
// carries no source-descent entity and is skipped. Per-instance infra is the
// IR-resolved entry.Infra (the secret entry carries it post-unification), so no
// Release-side join is needed.
//
// scopeOf reports a FeatureRecipe's executionScope by feature type. A
// boundary-scope FR renders once at its boundary from the boundary's own infra,
// but its per-component FeatureValuesMapping entries are Tier-4 folds at the
// wrong granularity for source identity; those entries are skipped here and the
// FR's source is pinned once per (boundaryPath, instance) from the IR
// boundary-feature projection — the same infra the renderer resolves the source
// from, so the lock-time pin and the build-time resolution agree by
// construction. scopeOf returning found=false (FR not resolvable) falls through
// to the per-component pin, preserving prior behavior.
func irEntityInfra(graph *types.IRGraph, manifest *types.Manifest, scopeOf func(instanceName, featType string) (string, bool)) (map[string][]closure.ComponentSourceInfra, error) {
	out := map[string][]closure.ComponentSourceInfra{}
	add := func(nodeID, scope, instance, featType string, entryInfra, entryValues types.OrderedMap[any]) error {
		if featType == "" {
			return nil
		}
		frURI, found, err := catalog.FeatureRecipeURI(manifest, instance, featType)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		var infra map[string]any
		if entryInfra.Len() > 0 {
			infra = entryInfra.TemplateAlphabeticalMapDeep()
		}
		var values map[string]any
		if entryValues.Len() > 0 {
			values = entryValues.TemplateAlphabeticalMapDeep()
		}
		out[frURI] = append(out[frURI],
			closure.ComponentSourceInfra{Component: nodeID + "." + scope + "." + instance, Infra: infra, Values: values})
		return nil
	}
	for nodeID, feats := range graph.Spec.FeatureValuesMapping.All() {
		for instName, entry := range feats.All() {
			if scope, ok := scopeOf(instName, entry.Type); ok && scope == "boundary" {
				continue // pinned once from the boundary projection below
			}
			if err := add(nodeID, "features", instName, entry.Type, entry.Infra, entry.Values); err != nil {
				return nil, err
			}
		}
	}
	for nodeID, secs := range graph.Spec.Secrets.All() {
		for instName, entry := range secs.All() {
			if err := add(nodeID, "secrets", instName, entry.Type, entry.Infra, entry.Values); err != nil {
				return nil, err
			}
		}
	}
	for boundaryPath, feats := range graph.Spec.BoundaryFeatures.All() {
		for instName, bf := range feats.All() {
			if scope, ok := scopeOf(instName, bf.Type); !ok || scope != "boundary" {
				continue // component-scope (or unresolvable): pinned per-component above
			}
			// Boundary-scope features carry no values (Release pillar-split).
			if err := add(boundaryPath, "features", instName, bf.Type, bf.Infra, types.OrderedMap[any]{}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// source descent failure (closure.ErrSourceTemplateAtLockTime), and
// lockfile write failure.
func pullRun(cfg pullConfig) error {
	releaseDir := filepath.Dir(cfg.ReleasePath)

	wsCfg, err := workspaceconfig.Load(workspaceConfigPath())
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	rel, err := resolver.LoadRelease(cfg.ReleasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	manRes, err := resolver.ResolveManifest(releaseDir, rel.Spec.Manifest)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	// Compose the Manifest's extends chain so the closure walk and recipe
	// resolution see the inherited mappings rather than the child's own
	// entries alone. A Manifest with no extends reference is unchanged.
	manifest, err := resolver.ResolveManifestExtends(releaseDir, manRes.Manifest)
	if err != nil {
		return fmt.Errorf("compose manifest extends: %w", err)
	}
	// The aggregate resolve is deferred until after the catalog loads (below) so
	// its identity cascade can reach the catalog tier — pull must resolve the
	// top-level aggregate the same way build does, or a catalog-resident
	// aggregate would build but not pin (lock/build divergence).

	// Workspace-agnostic: pull walks the closure to populate the
	// lockfile and never emits IR, so the catalog's source-file
	// accessors are unused. Pass "" for workspace root. The walk skips
	// the configured cache directory so cached library.yaml files are not
	// ingested as authored Tier-1 catalog.
	cacheDir := wsCfg.Workspace.ResolvedCacheDir()
	// Effective first-win catalog search stack: --catalog flag dirs prepended to
	// the workspace catalog_dirs. Resolved here (not by the caller) so direct pull
	// and build's auto-pull derive the identical stack under the same config.
	dirs := resolveCatalogDirs(cfg.CatalogDirs, wsCfg, ".")
	// Tolerant Pass-1: a file that fails to parse/index is skipped (recorded), not
	// fatal — a broken catalog entity OUTSIDE this Release's closure never fails the
	// pull. A cross-document $ref to a remote library cannot expand at load (Pass-2)
	// because the library is not yet fetched — the closure walk + library discovery
	// below acquire it; closure-scoped Pass-2 (after the bridge) is the validation
	// gate, scoped to what the Release reaches.
	cat, skipped, err := catalog.LoadPassOneTolerant("", cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	// Verify the Manifest's mapping table is consistent before the closure walk,
	// so pull halts on a mismatched mapping with the same Loud Guardrail the
	// build does (lock/build parity).
	if err := cat.AssertManifestImplementsConsistency(manifest, manRes.Path); err != nil {
		return err
	}

	workspaceRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("workspace root: %w", err)
	}
	puller := cfg.puller
	if puller == nil {
		puller = catalog.NewDefaultOCIPuller(wsCfg)
	}
	// The registry (Tier-3) seam for a bare-name top-level spec.aggregate that the
	// same-directory scan and the catalog both miss: fetch + cache it by identity,
	// pinning its metadata.version (identity), not the OCI tag. Pull-only — build
	// passes nil, so Tier-3 never networks and a registry top-level resolves there
	// through the cache→catalog bridge.
	registryFetch := func(ref string) (resolver.ResolvedAggregate, bool, error) {
		data, version, digest, found, ferr := catalog.PullAndCacheAggregate(workspaceRoot, cacheDir, puller, ref)
		if ferr != nil || !found {
			return resolver.ResolvedAggregate{}, found, ferr
		}
		agg, perr := resolver.LoadAggregateFromBytes(data, ref)
		if perr != nil {
			return resolver.ResolvedAggregate{}, false, perr
		}
		return resolver.ResolvedAggregate{Aggregate: agg, Bytes: data, ResolvedVersion: version, Digest: digest, FromRegistry: true}, true, nil
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, cat.AggregateLookup(), registryFetch)
	if err != nil {
		return fmt.Errorf("load aggregate: %w", err)
	}
	aggregate := aggRes.Aggregate

	lockfilePath := lockfile.PathFor(cfg.ReleasePath)
	if cfg.Regenerate {
		if err := os.Remove(lockfilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("regenerate: remove existing lockfile: %w", err)
		}
	}
	if cfg.Verify {
		// Verify-only: a missing lockfile is fatal — the flag
		// presumes one exists. The no-lockfile case is what the
		// default pull (no --verify) addresses.
		if _, statErr := os.Stat(lockfilePath); statErr != nil {
			return fmt.Errorf("verify: lockfile %s not found: %w", lockfilePath, statErr)
		}
	}

	// A registry-resolved top-level is pinned as a real registry entity (its
	// metadata.version + digest), seeded through the registry tier rather than the
	// version-less external tier; a local top-level (same-dir / ./file / catalog)
	// stays external exactly as before.
	var externals map[string][]byte
	var registrySeeds map[string]closure.Resolution
	if aggRes.FromRegistry {
		externals = lockfile.ExternalEntitiesFromBytes(rel, nil, manRes.Bytes)
		registrySeeds = map[string]closure.Resolution{
			rel.Spec.Aggregate: {ResolvedVersion: aggRes.ResolvedVersion, Digest: aggRes.Digest, ResolutionPath: "tier-3-pull"},
		}
	} else {
		externals = lockfile.ExternalEntitiesFromBytes(rel, aggRes.Bytes, manRes.Bytes)
	}
	// Re-pull reuse-check: when a prior lockfile exists, verify reused
	// cache slots against its pins so a slot that has drifted from its
	// pin is re-fetched and re-pinned from authoritative registry bytes
	// rather than laundered into the new lockfile. Skipped under
	// --verify (read-only — lockfile.Verify covers drift below) and when
	// no prior lockfile is present (first pull, or --regenerate removed
	// it above): there both establish pins from scratch, with nothing to
	// verify against.
	var reuse *catalog.ReuseVerification
	if !cfg.Verify {
		if prior, _, lerr := lockfile.LoadLockfile(lockfilePath); lerr == nil && prior != nil {
			reuse = &catalog.ReuseVerification{
				PriorPins: prior.PinsByURI(),
				Warn:      func(notice string) { fmt.Fprintf(os.Stderr, "  reuse: %s\n", notice) },
			}
		}
	}
	lookup := cat.ClosureLookupWithReuse(puller, workspaceRoot, cacheDir, externals, registrySeeds, reuse)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, wsCfg, lookup)
	if err != nil {
		return withSkipContext(fmt.Errorf("closure: %w", err), skipped)
	}
	// Discover + resolve the cross-document SchemaLibrary axis (ComputeClosure's
	// entity walk does not descend Component/Feature schema bodies). Shared with
	// build's drift-verify so both compute the identical library closure.
	if err := discoverClosureLibraries(&cl, cat, lookup); err != nil {
		return fmt.Errorf("closure: schema-library discovery: %w", err)
	}
	if err := discoverClosureMappingLibraries(&cl, cat, lookup); err != nil {
		return fmt.Errorf("closure: mapping-library discovery: %w", err)
	}
	// Pull-side transitive sub-aggregate descent: pin the closure of every
	// Aggregate reachable through a chain of sub-aggregate references, loading
	// each from the cache the resolution just populated. Lock-time only — the
	// build path reads these pins from the lockfile and never descends.
	aggLoader, _ := lookup.(closure.AggregateLoader)
	if err := closure.DescendSubAggregates(&cl, aggLoader, lookup); err != nil {
		return fmt.Errorf("closure: sub-aggregate descent: %w", err)
	}
	// Fold the registry aggregates the closure just pulled+cached into the catalog
	// so pull's own source-descent topology flatten (SubAggregateLoader, below)
	// resolves a registry-only sub-aggregate from cache — the same cache→catalog
	// bridge build runs. The lockfile is written from the closure, not the catalog,
	// so this fold does not perturb the pins.
	pullPins := make([]aggregateBridgePin, 0, len(cl.Entities))
	for _, e := range cl.Entities {
		pullPins = append(pullPins, aggregateBridgePin{URI: e.URI, Version: e.ResolvedVersion, Digest: e.Digest})
	}
	if err := bridgeCachedEntities(cat, workspaceRoot, cacheDir, pullPins); err != nil {
		return err
	}

	// The pull-event tier diagnostic reads the closure's in-memory resolution
	// tier (not persisted in the lockfile) and is unrecoverable after this run.
	// Rendered before the verify/write split so it reflects the pull regardless
	// of mode.
	if cfg.Detail {
		printPullDetail(os.Stdout, cl)
	}

	if cfg.Verify {
		// Verify-only path: load the existing lockfile and check
		// drift against the freshly-computed closure. No disk writes.
		// Source-artifact descent is skipped — lockfile.Verify covers
		// the entity/library/plugin axes; source artifacts have their
		// own digest gate at build-time read.
		lf, _, lerr := lockfile.LoadLockfile(lockfilePath)
		if lerr != nil {
			return fmt.Errorf("verify: load lockfile %s: %w", lockfilePath, lerr)
		}
		if verr := lockfile.Verify(lf, cl); verr != nil {
			return fmt.Errorf("verify: %w", verr)
		}
		fmt.Printf("Verify: lockfile %s matches current closure (entities=%d)\n",
			lockfilePath, len(cl.Entities))
		return nil
	}

	// Bridge the discovered libraries into the catalog before source-descent, so
	// its materialized pillars expand library-$ref-composed defaults exactly as
	// build does. Source-descent reads graph.Spec.{Infra,Values}Mapping (schema
	// defaults applied), and a library-$ref-composed default materializes only once
	// the $ref expands against the SchemaLibraries map; pull and build must compute
	// identical materialized pillars here or the pinned source identity diverges
	// from build (ErrFetchIntegrity). Mirrors dispatchBuild's bridge: a
	// locally-authored library is already in the tolerant Pass-1 catalog (skipped);
	// a remote pinned library is read from the cache. MERGE (not a reload): the
	// libraries are merged into the existing tolerant catalog, so an out-of-closure
	// broken entity is not re-walked-and-aborted; closure-scoped Pass-2 below is the
	// validation gate, scoped to what the Release reaches.
	extraLibs := map[string][]byte{}
	for _, lib := range cl.SchemaLibraries {
		if _, local := cat.SchemaLibraries[lib.URI]; local {
			continue
		}
		data, rerr := catalog.ReadPinnedLibrary(workspaceRoot, cacheDir, lib.URI, lib.ResolvedVersion, lib.Digest)
		if rerr != nil {
			return fmt.Errorf("bridge pinned SchemaLibrary %s: %w", lib.URI, rerr)
		}
		extraLibs[lib.URI] = data
	}
	extraMappings := map[string][]byte{}
	for _, lib := range cl.MappingLibraries {
		if _, local := cat.MappingLibraries[lib.URI]; local {
			continue
		}
		data, rerr := catalog.ReadPinnedLibrary(workspaceRoot, cacheDir, lib.URI, lib.ResolvedVersion, lib.Digest)
		if rerr != nil {
			return fmt.Errorf("bridge pinned MappingLibrary %s: %w", lib.URI, rerr)
		}
		extraMappings[lib.URI] = data
	}
	bridgeWarnings, err := cat.MergeExtraLibraries(extraLibs, extraMappings)
	if err != nil {
		return fmt.Errorf("catalog load (bridged): %w", err)
	}
	for _, w := range bridgeWarnings {
		fmt.Fprintf(os.Stderr, "  %s\n", w)
	}
	// Closure-scoped Pass-2: validate ONLY the entities the Release reaches against
	// the closure's libraries (local + bridged) — the same gate build applies, so a
	// pull and a build of the same Release agree on what is valid. An in-closure
	// schema-invalid entity, or a $ref to a skipped/missing library, fails loudly
	// here; an out-of-closure break is never validated.
	if err := cat.ValidateClosureScopedPillars(cl); err != nil {
		return withSkipContext(err, skipped)
	}

	// Per-component source resolution. Run the shared resolve+flatten pass —
	// the same path `aggrega build` uses (pipeline.ResolveAndFlatten) — so each
	// component's source resolves against that component's own cascade-resolved
	// infra (not a flattened Release-wide bag), and the component-to-Recipe join
	// uses the same Manifest mapping the closure walker recorded (lock-time
	// cannot drift from build-time). aggBytes feeds ResolveFromInputs, which
	// re-parses internally.
	aggBytes := aggRes.Bytes
	pullLookup := cat.CatalogLookup()
	pullLookup.ProviderInfraSchema = cat.ProviderInfraSchemaResolver(manifest)
	rr, topo, err := pipeline.ResolveAndFlatten(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     catalog.RelativeToWorkspace(workspaceRoot, aggRes.Path),
		},
		pullLookup,
		cat.SubAggregateLoader(),
	)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	defer resolver.PurgeParameters(&rr.Parameters)

	// Key per-instance infra by the Manifest-mapped entity URI — the same
	// verbatim string the closure walker records as the source-descent entity
	// (catalog.ComponentRecipeURI / FeatureRecipeURI resolve it flat-shape-first,
	// legacy-fallback), so this map joins against closure entities regardless of
	// short-form vs full-path mapping authoring. An instance with no Manifest
	// mapping carries no source-descent entity and is skipped.
	perEntityInfra := map[string][]closure.ComponentSourceInfra{}

	// Feature / secret instances: derive from the IR render-set, not a Release
	// walk. The renderer renders graph.Spec (the IR), whose feature/secret set is
	// assembled across all five tiers (Aggregate component/subsystem, Release
	// boundary-wide/component, annotation-expanded) at IR-build — a Release-boundary
	// walk and the topology are each short by tiers, so the using-set must come from
	// the IR for the no-using-instance skip to be congruent with rendering. Building
	// the IR here is acyclic (it consumes topology + resolve + catalog and copies
	// the recipe source pointer, never a fetched artifact); pull thus inherits the
	// IR-build failure surface. Per-instance infra is the IR-resolved entry.Infra
	// (Part B made secrets carry it), so no Release-side join is needed.
	graph, _, err := ir.BuildIR(ir.BuildIRConfig{
		Topology:        topo,
		ResolveResult:   rr,
		CatalogLookup:   cat.CatalogLookup(),
		CompilerVersion: "0.1.0",
	})
	if err != nil {
		return fmt.Errorf("ir: %w", err)
	}
	executionScopeOf := func(instanceName, featType string) (string, bool) {
		// Scope classification (name tier wins over type): a miss OR a
		// version-ambiguous mapping falls through to the per-component pin,
		// preserving prior behavior. The loud ambiguity halt belongs to the
		// source-pin joins below (irEntityInfra for features, ComponentRecipeURI
		// for components), where a wrong pick would mis-pin a fetched source.
		frURI, ok, _ := catalog.FeatureRecipeURI(manifest, instanceName, featType)
		if !ok {
			return "", false
		}
		fr, ok := cat.FeatureRecipeByURI(frURI)
		if !ok {
			return "", false
		}
		return fr.Spec.ExecutionScope, true
	}
	featInfra, err := irEntityInfra(graph, manifest, executionScopeOf)
	if err != nil {
		return fmt.Errorf("feature source-descent mapping: %w", err)
	}
	for frURI, comps := range featInfra {
		perEntityInfra[frURI] = append(perEntityInfra[frURI], comps...)
	}

	// Component instances: the component->Recipe join comes from the flattened
	// topology; the per-component source-eval values and infra come from the
	// MATERIALIZED IR pillars (graph.Spec.ValuesMapping / InfraMapping — schema
	// defaults applied), the same pillars the renderer resolves the source
	// against, so a value/infra-driven source identity pins identically at lock
	// and build. Runs after BuildIR so the materialized pillars exist.
	for _, node := range topo.Nodes.All() {
		recURI, found, err := catalog.ComponentRecipeURI(manifest, node.InstanceID, node.ComponentType)
		if err != nil {
			return fmt.Errorf("component source-descent mapping for %q: %w", node.ComponentType, err)
		}
		if !found {
			continue
		}
		perEntityInfra[recURI] = append(perEntityInfra[recURI],
			closure.ComponentSourceInfra{
				Component: node.InstanceID,
				Infra:     parse.NodePayloadMap(graph.Spec.InfraMapping, node.InstanceID),
				Values:    parse.NodePayloadMap(graph.Spec.ValuesMapping, node.InstanceID),
			})
	}

	var srcResolver closure.SourceResolver = sourceResolverShim{}
	if cfg.sourceResolver != nil {
		srcResolver = cfg.sourceResolver
	}
	sourceArtifacts, err := closure.DescendRecipeSources(
		perEntityInfra, cl, cat.ClosureRecipeLoader(), srcResolver,
		workspaceRoot, cacheDir,
	)
	if err != nil {
		return fmt.Errorf("source descent: %w", err)
	}
	cl.SourceArtifacts = sourceArtifacts

	lf := buildLockfile(cfg.ReleasePath, cl, wsCfg)
	if err := lockfile.WriteLockfile(lockfilePath, lf); err != nil {
		return fmt.Errorf("write lockfile: %w", err)
	}

	fmt.Printf("Pull complete: %s (entities=%d, source_artifacts=%d)\n",
		lockfilePath, len(cl.Entities), len(cl.SourceArtifacts))
	return nil
}

// workspaceConfigPath returns the conventional path of the
// per-workspace configuration file in the current working directory.
// Returns ".aggrega-config.yaml" — Load tolerates absence.
func workspaceConfigPath() string {
	return ".aggrega-config.yaml"
}

// buildLockfile materialises a *lockfile.Lockfile from the closure
// result. Field-for-field copy across the closure / lockfile package
// boundary; both types intentionally mirror the same shape.
func buildLockfile(releasePath string, cl closure.Closure, wsCfg workspaceconfig.Config) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     filepath.Base(releasePath),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, e := range cl.Entities {
		lf.Entities = append(lf.Entities, lockfile.Entity{
			URI:             e.URI,
			ResolvedVersion: e.ResolvedVersion,
			Digest:          e.Digest,
			ReferencedFrom:  append([]string(nil), e.ReferencedFrom...),
		})
	}
	sort.Slice(lf.Entities, func(i, j int) bool { return lf.Entities[i].URI < lf.Entities[j].URI })

	for _, l := range cl.SchemaLibraries {
		lf.SchemaLibraries = append(lf.SchemaLibraries, lockfile.SchemaLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.SchemaLibraries, func(i, j int) bool { return lf.SchemaLibraries[i].URI < lf.SchemaLibraries[j].URI })

	for _, l := range cl.MappingLibraries {
		lf.MappingLibraries = append(lf.MappingLibraries, lockfile.MappingLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.MappingLibraries, func(i, j int) bool { return lf.MappingLibraries[i].URI < lf.MappingLibraries[j].URI })

	for _, w := range cl.WasmPlugins {
		lf.WasmPlugins = append(lf.WasmPlugins, lockfile.WasmPlugin{
			URI:             w.URI,
			ResolvedVersion: w.ResolvedVersion,
			Digest:          w.Digest,
			ReferencedFrom:  append([]string(nil), w.ReferencedFrom...),
		})
	}
	sort.Slice(lf.WasmPlugins, func(i, j int) bool { return lf.WasmPlugins[i].URI < lf.WasmPlugins[j].URI })

	for _, s := range cl.SourceArtifacts {
		refs := make([]lockfile.SourceArtifactReference, 0, len(s.ReferencedBy))
		for _, r := range s.ReferencedBy {
			refs = append(refs, lockfile.SourceArtifactReference{
				Entity:       r.Entity,
				URI:          r.URI,
				Field:        r.Field,
				RequestedRef: r.RequestedRef,
			})
		}
		lf.SourceArtifacts = append(lf.SourceArtifacts, lockfile.SourceArtifact{
			Kind:          s.Kind,
			URL:           s.URL,
			PinnedVersion: s.PinnedVersion,
			Digest:        s.Digest,
			Mediatype:     s.Mediatype,
			ReferencedBy:  refs,
		})
	}

	return lf
}

func copyConsumers(in []closure.LibraryConsumer) []lockfile.LibraryConsumer {
	out := make([]lockfile.LibraryConsumer, 0, len(in))
	for _, c := range in {
		out = append(out, lockfile.LibraryConsumer{
			ConsumerURI: c.ConsumerURI,
			Fragments:   append([]string(nil), c.Fragments...),
		})
	}
	return out
}
