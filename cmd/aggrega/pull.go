// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	pullReleasePath       string
	pullCatalogDirs       []string
	pullUpgrade           bool
	pullVerify            bool
	pullIncludePrerelease bool
	pullDetail            bool
	pullQuiet             bool
	pullProgress          bool
)

func newPullCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "pull [<uri>...]",
		Short: "Resolve catalog and source dependencies into the cache and write the per-Release lockfile",
		Long: `Resolve the Release's catalog and source dependencies into the cache and write the per-Release lockfile.

By default, pull reconciles the lockfile against the current dependency resolution while honoring
the pins it already records. A pinned entity is fetched at its pinned version and
verified against its pinned digest; if the registry no longer serves the pinned bytes,
pull halts rather than silently re-pinning. A reference with no pin — newly added, or
removed from the lockfile by hand — is resolved fresh and added. Entities no longer among
the resolved dependencies are pruned. Re-running pull on an unchanged dependency set is a no-op.

Use --upgrade with no positional arguments to discard the existing lockfile and re-resolve
every entity from current registry state, floating each dependency to the highest available
version. Pass one or more entity URIs after --upgrade to upgrade only those: each named pin
is dropped and re-resolved fresh while every other pin — and the recorded external inputs —
is held unchanged. A URI given without --upgrade is a usage error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if pullVerify && pullUpgrade {
				return fmt.Errorf("--verify and --upgrade are mutually exclusive")
			}
			if len(args) > 0 && !pullUpgrade {
				return fmt.Errorf("positional arguments name upgrade targets and require --upgrade")
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pullRun(pullConfig{
				ReleasePath:       pullReleasePath,
				CatalogDirs:       pullCatalogDirs,
				Upgrade:           pullUpgrade,
				UpgradeTargets:    args,
				Verify:            pullVerify,
				IncludePrerelease: pullIncludePrerelease,
				Detail:            pullDetail,
				ws:                rw,
				diag:              buildDiagOptions{quiet: pullQuiet, progress: pullProgress, noColor: globalDisplay.colorOff, plain: globalDisplay.plain},
			})
		},
	}
	c.Flags().StringVar(&pullReleasePath, "release", "", "Path to Release YAML file (required)")
	c.Flags().StringArrayVar(&pullCatalogDirs, "catalog", nil, "Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	c.Flags().BoolVar(&pullUpgrade, "upgrade", false, "Re-resolve dependencies from current registry state, floating each to the highest available version. Bare (no positional URIs) discards the lockfile and re-resolves every entity; with one or more entity URIs, re-resolves only those, holding all other pins and the recorded external inputs unchanged")
	c.Flags().BoolVar(&pullVerify, "verify", false, "Verify the existing lockfile against the current dependency resolution without writing; exits non-zero on drift or missing lockfile")
	c.Flags().BoolVar(&pullIncludePrerelease, "include-prerelease", false, "Reserved for future version-selection logic; currently a no-op")
	c.Flags().BoolVar(&pullDetail, "detail", false, "Print, per resolved entity, what this pull did — fetched fresh from a registry, reused a cache slot, found in a local catalog, or external. Stdout only; not written to the lockfile")
	c.Flags().BoolVar(&pullQuiet, "quiet", false, "Silence the pull-progress surface on stderr. Does not affect stdout. Takes precedence over --progress")
	c.Flags().BoolVar(&pullProgress, "progress", false, "Force the pull-progress surface on even when stderr is not a terminal, printing plain static phase lines. The default is auto: the interactive phase checklist is shown only when stderr is a terminal")
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
	// pullRun prepends them to the workspace catalog_dirs via ResolveCatalogDirs.
	// build's auto-pull integration forwards its own flag dirs so the pull path
	// re-derives the identical stack under the same config.
	CatalogDirs []string
	Upgrade     bool
	// UpgradeTargets names the entity URIs to upgrade when Upgrade is set. Empty
	// with Upgrade set is a full refresh (float every dependency); non-empty is a
	// targeted upgrade — only the named pins are dropped and re-resolved, every
	// other pin and the recorded external inputs are held. Each URI is matched by
	// exact string against a lockfile pin key (the authored bare form, e.g.
	// core/database, not a versioned form); a URI that matches no pin halts.
	UpgradeTargets    []string
	Verify            bool
	IncludePrerelease bool
	Detail            bool

	// ws is the resolved workspace, threaded in by the command's RunE from the
	// shared resolution chokepoint. Nil only when a test invokes pullRun
	// directly; workspaceOrResolve then resolves from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// DiagOut is the sink for the pull's reuse/verify diagnostic notices.
	// Nil defaults to os.Stderr at the top of pullRun (production); tests pass a
	// buffer to capture diagnostics without mutating a process global.
	DiagOut io.Writer

	// Out is the sink for the pull's user-facing status output (the --detail
	// table, the "Verify:" line, and "Pull complete"). Nil defaults to os.Stdout
	// at the top of pullRun (production); tests pass a buffer to capture the
	// output without mutating a process global.
	Out io.Writer

	// puller overrides the production OCI puller; tests inject an in-memory
	// implementation to exercise a remote-library fetch without the network. Nil
	// uses NewDefaultOCIPuller (which is nil under workspace.offline).
	puller catalog.OCIPuller

	// sourceResolver overrides the production source-resolver shim; tests inject
	// a recording/stub implementation to assert the source identity computed from
	// the materialized pillars without a real artifact fetch. Nil uses the shim.
	sourceResolver closure.SourceResolver

	// diag carries the pull-progress surface choices (quiet / progress). The pull
	// command's RunE populates it from the flags; build's auto-pull leaves it
	// quiet so only the build's own surface shows during an implicit pull.
	diag buildDiagOptions
}

// Pull phase indices into the pull progress spine's labels.
const (
	pullPhaseLoadCatalog = iota
	pullPhaseDependencies
	pullPhaseClosure
	pullPhaseLibraries
	pullPhaseValidate
	pullPhaseSources
	pullPhaseLockfile
)

// pullProgressSpine returns the ordered pull phase spine. Pull has no render
// phase (renderIdx -1), so it carries no per-node counter and no plugin-compile
// step — just the phases the closure walk and lockfile write cross.
func pullProgressSpine() progressSpine {
	return progressSpine{
		labels: []string{
			pullPhaseLoadCatalog:  "loading catalog",
			pullPhaseDependencies: "resolving dependencies",
			pullPhaseClosure:      "resolving dependencies",
			pullPhaseLibraries:    "resolving libraries",
			pullPhaseValidate:     "validating",
			pullPhaseSources:      "resolving sources",
			pullPhaseLockfile:     "writing lockfile",
		},
		renderIdx: -1,
	}
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
	if cfg.DiagOut == nil {
		cfg.DiagOut = os.Stderr
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
	progress := newProgressReporter(cfg.DiagOut, diag, pullProgressSpine())
	defer progress.finish()
	releaseDir := filepath.Dir(cfg.ReleasePath)

	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsCfg := rw.Config()

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
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Effective first-win catalog search stack: --catalog flag dirs prepended to
	// the workspace catalog_dirs. Resolved here (not by the caller) so direct pull
	// and build's auto-pull derive the identical stack under the same config.
	dirs := workspaceconfig.ResolveCatalogDirs(cfg.CatalogDirs, wsCfg, ".")
	// Tolerant Pass-1: a file that fails to parse/index is skipped (recorded), not
	// fatal — a broken catalog entity OUTSIDE this Release's closure never fails the
	// pull. A cross-document $ref to a remote library cannot expand at load (Pass-2)
	// because the library is not yet fetched — the closure walk + library discovery
	// below acquire it; closure-scoped Pass-2 (through the pinned lazy lookup) is
	// the validation gate, scoped to what the Release reaches.
	progress.phase(pullPhaseLoadCatalog)
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

	progress.phase(pullPhaseDependencies)
	workspaceRoot := rw.Root()
	puller := cfg.puller
	if puller == nil {
		puller = catalog.NewDefaultOCIPuller(wsCfg)
	}
	// The registry (Tier-3) seam for a bare-name top-level spec.aggregate that the
	// same-directory scan and the catalog both miss: fetch + cache it by identity,
	// pinning its metadata.version (identity), not the OCI tag. Pull-only — build
	// passes a nil puller, so Tier-3 never networks and a registry top-level
	// resolves from the pinned cache through the lazy lookup.
	registryFetch := func(ref string) (resolver.ResolvedAggregate, bool, error) {
		data, version, digest, manifestDigest, found, ferr := catalog.PullAndCacheAggregate(workspaceRoot, cacheDir, puller, ref)
		if ferr != nil || !found {
			return resolver.ResolvedAggregate{}, found, ferr
		}
		agg, perr := resolver.LoadAggregateFromBytes(data, ref)
		if perr != nil {
			return resolver.ResolvedAggregate{}, false, perr
		}
		return resolver.ResolvedAggregate{Aggregate: agg, Bytes: data, ResolvedVersion: version, Digest: digest, ManifestDigest: manifestDigest, FromRegistry: true}, true, nil
	}
	aggRes, err := resolver.ResolveAggregate(releaseDir, rel.Spec.Aggregate, cat.AggregateLookup(), registryFetch)
	if err != nil {
		return fmt.Errorf("load aggregate: %w", err)
	}
	aggregate := aggRes.Aggregate

	lockfilePath := lockfile.PathFor(cfg.ReleasePath)
	// A targeted upgrade names specific entities and holds the rest, so it must
	// keep the prior lockfile (that is what preserves the other pins and the
	// recorded external inputs); only the bare full-refresh upgrade discards it.
	targeted := cfg.Upgrade && len(cfg.UpgradeTargets) > 0
	if cfg.Upgrade && !targeted {
		if err := os.Remove(lockfilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("upgrade: remove existing lockfile: %w", err)
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
			rel.Spec.Aggregate: {ResolvedVersion: aggRes.ResolvedVersion, Digest: aggRes.Digest, ManifestDigest: aggRes.ManifestDigest, ResolutionPath: "tier-3-pull"},
		}
	} else {
		externals = lockfile.ExternalEntitiesFromBytes(rel, aggRes.Bytes, manRes.Bytes)
	}
	// Re-pull reuse-check: when a prior lockfile exists, verify reused
	// cache slots against its pins so a slot that has drifted from its
	// pin is re-fetched and re-pinned from authoritative registry bytes
	// rather than laundered into the new lockfile. Skipped under
	// --verify (read-only — lockfile.Verify covers drift below) and when
	// no prior lockfile is present (first pull, or --upgrade removed
	// it above): there both establish pins from scratch, with nothing to
	// verify against.
	var reuse *catalog.ReuseVerification
	if !cfg.Verify {
		prior, _, lerr := lockfile.LoadLockfile(lockfilePath)
		if targeted {
			// Targeted upgrade holds every non-named pin, so a prior lockfile is
			// required — there must be a pin set to hold. Reducing that set (dropping
			// the named pins) is the whole mechanism: a URI absent from PriorPins
			// floats to its highest-satisfying version, every URI still present is
			// honored, and the untouched lockfile file carries the external inputs
			// forward.
			if lerr != nil || prior == nil {
				return fmt.Errorf("no lockfile to upgrade at %s; run `aggrega pull` first to create one", lockfilePath)
			}
			pins := catalog.PinsFromLockfile(prior)
			for _, target := range cfg.UpgradeTargets {
				if _, ok := pins[target]; !ok {
					return fmt.Errorf("entity %q is not pinned in the lockfile; nothing to upgrade", target)
				}
				delete(pins, target)
			}
			reuse = &catalog.ReuseVerification{
				PriorPins: pins,
				Warn:      func(notice string) { fmt.Fprintf(cfg.DiagOut, "  reuse: %s\n", notice) },
			}
		} else if lerr == nil && prior != nil {
			reuse = &catalog.ReuseVerification{
				PriorPins: catalog.PinsFromLockfile(prior),
				Warn:      func(notice string) { fmt.Fprintf(cfg.DiagOut, "  reuse: %s\n", notice) },
			}
		}
	}
	lookup := cat.ClosureLookupWithReuse(puller, workspaceRoot, cacheDir, externals, registrySeeds, reuse)
	progress.phase(pullPhaseClosure)
	cl, err := closure.ComputeClosure(rel, manifest, aggregate, wsCfg, lookup)
	if err != nil {
		return withSkipContext(fmt.Errorf("resolve: %w", err), skipped)
	}
	// Resolve the workspace provider-socket bindings to their Plugin entities at
	// pull time only. ComputeClosure is config-blind to the provider socket, so
	// build / serve reconstruct these entities from the lockfile rather than from
	// live config — the entity axis stays invariant to a config edit after pull.
	if err := closure.ResolveProviderSocketEntities(&cl, wsCfg.Plugins.Providers, lookup); err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	// pull's determinism-wired lazy lookup: the post-fetch consumers — closure-
	// library discovery, closure-scoped validation, and the source-descent
	// SubAggregateLoader below — resolve the resolved closure directly, with no
	// cache→catalog bridge. REAL puller (pull's job is to fetch + cache); pins come
	// from the freshly-computed closure, because no lockfile exists yet — pull WRITES
	// it — so a Tier-2 hit is verified against the version the walk just resolved.
	lazyView := catalog.NewLazyLookup(workspaceRoot, cacheDir, dirs, puller)
	lazyView.SetPins(catalog.PinsFromClosure(cl))

	// Discover + resolve the cross-document SchemaLibrary axis (ComputeClosure's
	// entity walk does not descend Component/Feature schema bodies), reading the
	// resolved closure through lazyView — the same producer build runs, so a pull
	// and a build of the same Release compute the identical library closure.
	progress.phase(pullPhaseLibraries)
	if err := closure.AugmentClosure(&cl, lazyView, lookup); err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	// Pull-side transitive sub-aggregate descent: pin the closure of every
	// Aggregate reachable through a chain of sub-aggregate references, loading
	// each from the cache the resolution just populated. Lock-time only — the
	// build path reads these pins from the lockfile and never descends.
	aggLoader, _ := lookup.(closure.AggregateLoader)
	if err := closure.DescendSubAggregates(&cl, aggLoader, lookup); err != nil {
		return fmt.Errorf("resolve: sub-aggregate descent: %w", err)
	}
	// Pull-side pipeline-plugin descent, two-pass: Phase A reads each in-closure
	// Recipe / FeatureRecipe's spec.pipeline[].plugin URIs — and its spec.provider
	// binding, resolved by name to the provider-rendering plugin serving it — and pins
	// a registry-resolved plugin ENTITY into the closure (catalog-entity axis), so a
	// registry-published pipeline or provider plugin participates in the deterministic
	// lockfile pin like any other catalog entity. The freshly-pinned plugin entities
	// are then folded into the lazy index by re-applying the closure's pins, so Phase
	// B's entity-body load resolves them; the Phase-B loader is RE-DERIVED after the
	// re-fold (SetPins drops the cached recipe view). Phase B derives each entity's
	// .wasm reference from its identity and pins the ARTIFACT (plugin axis). A
	// Tier-1-local plugin is left out (resolved at build through its local path).
	// Lock-time only — the build path reads plugin pins from the lockfile and never
	// descends.
	// Provider resolution honors the workspace-config plugins.providers selection
	// and records the resolved target → plugin binding for the lockfile, so build
	// selects the engine from the lockfile rather than re-reading the config. On an
	// ambiguous target with no binding the interactive helper (TTY, not --quiet)
	// offers to write the binding; non-interactively the ambiguity halts loudly.
	resolvedBindings := map[string]lockfile.ProviderBinding{}
	providerBindResolver := func(ref string) (types.Plugin, bool, error) {
		bare, _, _ := strings.Cut(ref, ":")
		p, ok, err := lazyView.ProviderPluginForTarget(ref, wsCfg.Plugins.ProviderTargetURIs())
		if errors.Is(err, catalog.ErrAmbiguousProviderPlugin) && providerBindingHelperEligible(cfg) {
			if herr := runProviderBindingHelper(bare, lazyView.ProviderPluginCandidates(bare), rw.Root(), &wsCfg, cfg.DiagOut); herr != nil {
				return types.Plugin{}, false, herr
			}
			p, ok, err = lazyView.ProviderPluginForTarget(ref, wsCfg.Plugins.ProviderTargetURIs())
		}
		if err != nil {
			return types.Plugin{}, false, err
		}
		if ok {
			resolvedBindings[bare] = lockfile.ProviderBinding{Capability: bare, Plugin: p.Coordinate()}
		}
		return p, ok, err
	}
	if err := closure.DescendRecipePluginEntities(&cl, lazyView.ClosureRecipeLoader(), lookup, providerBindResolver); err != nil {
		return fmt.Errorf("resolve: pipeline-plugin entity descent: %w", err)
	}
	lazyView.SetPins(catalog.PinsFromClosure(cl))
	if err := closure.DescendRecipePluginArtifacts(&cl, lazyView.ClosureRecipeLoader(), lookup); err != nil {
		return fmt.Errorf("resolve: pipeline-plugin artifact descent: %w", err)
	}
	// Resolve the mutator-socket attachments to their pinned lockfile records, in
	// authored order. The mutator rewrites the IR and shapes output bytes, so its
	// attachment set / order / export function is recorded for the lockfile and
	// build reconstructs it from there rather than from live config — keeping
	// build output invariant to a plugins.mutator change made after the pull.
	mutatorAttachments, err := resolveMutatorAttachments(wsCfg.Plugins.TransformGovernanceRefs(), lazyView)
	if err != nil {
		return err
	}
	// Cache the verdict-socket (policy / linter) gate plugins so a hermetic build
	// can run them, WITHOUT pinning them in the lockfile. A verdict socket renders
	// an accept/reject verdict without shaping the output bytes, so its plugin is
	// not part of the reproducibility closure (cache-not-pin): the pull fetches and
	// caches its entity + .wasm, and the build resolves it from the cache by URI.
	verdictHTTPClient, _ := workspaceEgressClient(wsCfg)
	if err := cacheVerdictSocketPlugins(context.Background(), wsCfg, lookup, lazyView, workspaceRoot, cacheDir, remoteTargetForWith(verdictHTTPClient), cfg.DiagOut); err != nil {
		return err
	}
	// Re-derive each plugin's host functions from the freshly-cached, digest-
	// verified .wasm and verify they match the machine-owned stamp the publish
	// flow wrote into the entity. Defense-in-depth over the content-digest pin:
	// halts on a divergence the digest alone would not surface (a non-aggrega
	// push, a publish-side stamping bug).
	if err := verifyPluginHostFunctions(context.Background(), &cl, lazyView.ClosureRecipeLoader(), workspaceRoot, cacheDir, cfg.DiagOut); err != nil {
		return fmt.Errorf("verify plugin host functions: %w", err)
	}
	// Refresh the pin set so it covers the descended sub-aggregates the source-
	// descent flatten resolves through lazyView.SubAggregateLoader below, and the
	// providers + pipeline plugins the descents just pinned.
	lazyView.SetPins(catalog.PinsFromClosure(cl))

	// The pull-event tier diagnostic reads the closure's in-memory resolution
	// tier (not persisted in the lockfile) and is unrecoverable after this run.
	// Rendered before the verify/write split so it reflects the pull regardless
	// of mode.
	if cfg.Detail {
		printPullDetail(out, cl)
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
		fmt.Fprintf(out, "Verify: lockfile %s matches current dependency resolution (entities=%d)\n",
			lockfilePath, len(cl.Entities))
		return nil
	}

	// Closure-scoped Pass-2: validate ONLY the entities the Release reaches against
	// the closure's libraries, read through lazyView (the resolved closure, library
	// $refs expanded against the lazily-walked SchemaLibrary set) — the same gate
	// build applies, so a pull and a build of the same Release agree on what is
	// valid. An in-closure schema-invalid entity, or a $ref to a skipped/missing
	// library, fails loudly here; an out-of-closure break is never validated.
	progress.phase(pullPhaseValidate)
	if err := lazyView.ValidateClosureScopedPillars(cl); err != nil {
		return withSkipContext(err, skipped)
	}

	// Per-component source resolution. Run the shared resolve+flatten pass —
	// the same path `aggrega build` uses (pipeline.ResolveAndFlatten) — so each
	// component's source resolves against that component's own cascade-resolved
	// infra (not a flattened Release-wide bag), and the component-to-Recipe join
	// uses the same Manifest mapping the closure walker recorded (lock-time
	// cannot drift from build-time). aggBytes feeds the resolver, which
	// re-parses internally.
	progress.phase(pullPhaseSources)
	aggBytes := aggRes.Bytes
	pullLookup := lazyView.AsCatalogLookup()
	// ResolveAndFlatten scopes the resolved view + parameter map to this
	// callback; resolver.ResolveAndUse purges the parameters after it returns,
	// so the entire source-descent runs while parameters are live and cannot
	// leak them. The descent reads BoundaryInfra and the materialised IR
	// pillars, never the raw parameter map.
	if err := pipeline.ResolveAndFlatten(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     catalog.RelativeToWorkspace(workspaceRoot, aggRes.Path),
		},
		pullLookup,
		lazyView.SubAggregateLoader(),
		func(rr *resolver.ResolveResult, topo *topology.TopologyResult) error {

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
				CatalogLookup:   pullLookup,
				CompilerVersion: provenanceCompilerVersion(),
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
				fr, ok := lazyView.FeatureRecipeByURI(frURI)
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

			var srcResolver closure.SourceResolver = sourceResolverShim{wsCfg: wsCfg}
			if cfg.sourceResolver != nil {
				srcResolver = cfg.sourceResolver
			}
			sourceArtifacts, err := closure.DescendRecipeSources(
				perEntityInfra, cl, lazyView.ClosureRecipeLoader(), srcResolver,
				workspaceRoot, cacheDir,
			)
			if err != nil {
				return fmt.Errorf("source descent: %w", err)
			}
			cl.SourceArtifacts = sourceArtifacts

			// Freeze each writable recipe's intrinsic content pins (source +
			// $import artifact digests) into the recipe file, from the resolved
			// sources — the resolution-time record the build later verifies.
			postFreezeRecipeDigests, err := freezeRecipePins(lazyView, cl.Entities, sourceArtifacts)
			if err != nil {
				return fmt.Errorf("freeze recipe pins: %w", err)
			}

			lf := buildLockfile(cfg.ReleasePath, cl, wsCfg, resolvedBindings, mutatorAttachments, postFreezeRecipeDigests)
			// Carry forward each unchanged pin's recorded deprecation fact from the
			// prior lockfile before verification runs. A warm-cache (Tier-2) re-pull
			// does not re-touch the registry, so it does not re-discover markers; the
			// prior fact holds for a pin whose digest is unchanged. A Tier-3
			// re-discovery below overwrites it with a fresh finding. Without this, a
			// build's honoring auto-pull would silently drop a recorded deprecation
			// and mask a deprecated dependency.
			carryForwardDeprecation(lf, lockfilePath)
			// Carry forward the prior lockfile's external-input pins. These are
			// render-time remote-input pins recorded by build and acquire, not
			// part of the catalog closure buildLockfile materialises, so a pull
			// would otherwise rewrite the lockfile with an empty list and force
			// the next build to re-acquire from the network. A --upgrade pull
			// removes the prior lockfile up front, so this refreshes the pins.
			carryForwardExternalInputs(lf, lockfilePath)
			// Lock-time signature verification: verify each registry-resolved
			// entity's signature against the registry's trust policy, record
			// the verified fact, and cache the bundle for air-gapped re-verify.
			// Pull verifies; build reads the recorded fact and never re-verifies
			// (the AOT boundary). The verify is explicitly requested, so when the
			// workspace is offline a registry-resolved entity's referrer fetch is
			// refused loudly through the egress gate (a nil client) rather than
			// silently passing as unverified; a closure with nothing registry-
			// resolved needs no fetch and is unaffected.
			verifyHTTPClient, _ := workspaceEgressClient(wsCfg)
			if err := verifyClosureSignatures(context.Background(), lf, cl, wsCfg, cacheDir, cfg.DiagOut, remoteTargetForWith(verifyHTTPClient)); err != nil {
				return fmt.Errorf("verify signatures: %w", err)
			}
			progress.phase(pullPhaseLockfile)
			if err := lockfile.WriteLockfilePreservingTimestamp(lockfilePath, lf); err != nil {
				return fmt.Errorf("write lockfile: %w", err)
			}

			// Clear the in-place surface before the stdout summary so the
			// progress lines never linger beside the final output.
			progress.finish()
			fmt.Fprintf(out, "Pull complete: %s (entities=%d, source_artifacts=%d)\n",
				lockfilePath, len(cl.Entities), len(cl.SourceArtifacts))
			return nil
		}); err != nil {
		return err
	}
	return nil
}

// buildLockfile materialises a *lockfile.Lockfile from the closure
// result. Field-for-field copy across the closure / lockfile package
// boundary; both types intentionally mirror the same shape.
func buildLockfile(releasePath string, cl closure.Closure, wsCfg workspaceconfig.Config, bindings map[string]lockfile.ProviderBinding, mutators []lockfile.MutatorAttachment, postFreezeRecipeDigests map[string]string) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     filepath.Base(releasePath),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, b := range bindings {
		lf.Providers = append(lf.Providers, b)
	}
	sort.Slice(lf.Providers, func(i, j int) bool { return lf.Providers[i].Capability < lf.Providers[j].Capability })

	// Author order is semantic for the mutator runner, so the attachments are
	// carried through unsorted (WriteLockfile likewise preserves the order). The
	// copy avoids aliasing the caller's slice.
	lf.Mutators = append([]lockfile.MutatorAttachment(nil), mutators...)

	for _, e := range cl.Entities {
		// A writable recipe's content digest was captured at resolution, before
		// the freeze rewrote the recipe file's intrinsic pins. Overlay the
		// post-freeze content digest (keyed strictly by URI) so the lockfile
		// records the file as the build will re-hash it — otherwise the very
		// next build reports lockfile drift for the recipe the freeze just wrote.
		entDigest := e.Digest
		if pf, ok := postFreezeRecipeDigests[e.URI]; ok {
			entDigest = pf
		}
		lf.Entities = append(lf.Entities, lockfile.Entity{
			URI:             e.URI,
			ResolvedVersion: e.ResolvedVersion,
			Digest:          entDigest,
			ManifestDigest:  e.ManifestDigest,
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

	for _, w := range cl.Plugins {
		lf.Plugins = append(lf.Plugins, lockfile.Plugin{
			URI:             w.URI,
			ResolvedVersion: w.ResolvedVersion,
			Digest:          w.Digest,
			ManifestDigest:  w.ManifestDigest,
			ReferencedFrom:  append([]string(nil), w.ReferencedFrom...),
		})
	}
	sort.Slice(lf.Plugins, func(i, j int) bool { return lf.Plugins[i].URI < lf.Plugins[j].URI })

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
			Kind:           s.Kind,
			URL:            s.URL,
			Chart:          s.Chart,
			PinnedVersion:  s.PinnedVersion,
			Digest:         s.Digest,
			ManifestDigest: s.ManifestDigest,
			Mediatype:      s.Mediatype,
			ResolvedURL:    s.ResolvedURL,
			ReferencedBy:   refs,
		})
	}

	return lf
}

// resolveMutatorAttachments resolves each configured mutator-socket attachment
// to the pinned record the lockfile carries: the plugin's resolved coordinate
// identity, the export override, the attachment's settings object, and a
// content-addressed pin of each declared source file, in authored order. Build
// and serve reconstruct the mutator attachment list from these records rather
// than from live workspace config, so the mutator set / order / export function
// / settings / source content that shapes the rendered output is config-invariant
// after the pull.
//
// Each attachment's Plugin entity resolves through the same pinned lazy view the
// rest of the pull uses; the coordinate is derived through the single
// Plugin.Coordinate rule, the same identity ProviderBinding.Plugin records. A
// ref that does not resolve is a build fault (the closure walk already pinned
// the mutator entity, halting loudly on a missing one), so it halts here rather
// than writing an unresolvable coordinate.
//
// Each declared source path is already anchored to an absolute host path by the
// config layer; the pin records the file's content verbatim, its sha256 digest,
// and its base name only (never the absolute host path — portability and
// determinism), sorted by base name so the pinned wire is deterministic. A read
// failure is a loud pull fault (a misconfigured attachment).
func resolveMutatorAttachments(refs []workspaceconfig.PluginRef, lazyView *catalog.LazyLookup) ([]lockfile.MutatorAttachment, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]lockfile.MutatorAttachment, 0, len(refs))
	for _, ref := range refs {
		if ref.URI == "" {
			continue
		}
		p, ok := lazyView.PluginByURI(ref.URI)
		if !ok {
			return nil, fmt.Errorf("resolve: mutator socket plugin %q not resolvable for lockfile pin", ref.URI)
		}
		att := lockfile.MutatorAttachment{
			Plugin:   p.Coordinate(),
			Function: ref.Function,
		}
		if ref.With != nil {
			att.Settings = ref.With.Settings
			for _, src := range ref.With.Sources {
				content, err := os.ReadFile(src)
				if err != nil {
					return nil, fmt.Errorf("resolve: read mutator socket source %q: %w", src, err)
				}
				att.Sources = append(att.Sources, lockfile.MutatorSourcePin{
					Path:    filepath.Base(src),
					Content: string(content),
					Digest:  digest.ContentDigest(content),
				})
			}
			sort.Slice(att.Sources, func(i, j int) bool { return att.Sources[i].Path < att.Sources[j].Path })
		}
		out = append(out, att)
	}
	return out, nil
}

// cacheVerdictSocketPlugins fetches and caches every verdict-socket (policy /
// linter) gate plugin the effective config attaches, WITHOUT recording any of
// them in the lockfile. A verdict socket renders an accept/reject verdict without
// shaping the output bytes, so its plugin is cached-not-pinned: the pull populates
// the cache slot (entity config blob + OCI manifest, and the .wasm artifact) so a
// hermetic build can resolve and run the gate by URI, but the plugin never enters
// the reproducibility closure and is never drift-gated.
//
// It resolves each gate plugin through the same catalog cascade the pinned
// mutator entity + artifact descent uses (ResolveCatalogEntity for the entity,
// ResolvePlugin for the .wasm), inheriting that path's digest verification. It
// does not append to the closure, so nothing it caches reaches the lockfile.
//
// Trust posture: a freshly cached gate plugin is verified exactly as a pinned
// plugin is at pull — registry signature verification against the resolving
// registry's trust policy AND host-function stamp re-derivation from the cached
// .wasm — through the one fetch-verification home every tier shares
// (verifyFetchedPlugin). Because a verdict gate is cache-not-pin, the verified
// fact is not recorded in the lockfile and never enters the reproducibility
// closure; it is verified all the same, so a tampered or unsigned gate plugin
// halts the pull rather than running unverified at build. A registry gate carries
// a manifest digest and is signature-verified; a local / embedded gate carries
// none and skips signature, exactly as the pinned path skips a non-registry item.
//
// A capability-only / empty-URI attachment carries nothing to fetch and is
// skipped; a plugin that does not resolve from the registry (a local / embedded /
// absent gate the build resolves in place) is skipped rather than treated as a
// closure gap — a verdict gate is not on the pinned axis a missing closure entity
// would halt.
func cacheVerdictSocketPlugins(ctx context.Context, cfg workspaceconfig.Config, lookup closure.Lookup, lazyView *catalog.LazyLookup, workspaceRoot, cacheDir string, targetFor pullverify.TargetFor, diagOut io.Writer) error {
	for _, socket := range cfg.PluginSockets() {
		verdictRefs := socket.RefsByTier(workspaceconfig.SocketTierVerdict)
		if len(verdictRefs) == 0 {
			continue
		}
		for _, ref := range verdictRefs {
			if ref.URI == "" {
				continue
			}
			entRes, err := lookup.ResolveCatalogEntity(ref.URI, "plugin")
			if err != nil {
				return fmt.Errorf("cache %s socket plugin %q: %w", socket.Socket, ref.URI, err)
			}
			if entRes.NotFound {
				continue
			}
			bare, version, _ := strings.Cut(ref.URI, ":")
			if version == "" {
				version = entRes.ResolvedVersion
			}
			artRes, err := lookup.ResolvePlugin(bare, version)
			if err != nil {
				return fmt.Errorf("cache %s socket plugin artifact %q: %w", socket.Socket, ref.URI, err)
			}
			if artRes.NotFound {
				return fmt.Errorf("cache %s socket plugin artifact %q: entity resolved but its .wasm did not: %w",
					socket.Socket, ref.URI, closure.ErrUnresolvableURI)
			}
			if err := verifyCachedVerdictSocketPlugin(ctx, cfg, lazyView, ref.URI, artRes.ManifestDigest, workspaceRoot, cacheDir, targetFor, diagOut); err != nil {
				return fmt.Errorf("verify %s socket plugin %q: %w", socket.Socket, ref.URI, err)
			}
		}
	}
	return nil
}

// verifyCachedVerdictSocketPlugin routes a freshly cached verdict-socket gate
// plugin through the shared fetch-verification home. It binds the plugin's
// host-less bare identity to its registry location for signature referrer
// discovery (the same binding the pinned plugin-entity signature uses) and reads
// the entity stamp + content-verified .wasm from the pull-populated cache (the
// same cache-not-pin read the build resolves the gate through), then hands both
// to verifyFetchedPlugin. The verified fact is discarded: a verdict gate is
// cache-not-pin and never enters the lockfile.
func verifyCachedVerdictSocketPlugin(ctx context.Context, cfg workspaceconfig.Config, lazyView *catalog.LazyLookup, uri, manifestDigest, workspaceRoot, cacheDir string, targetFor pullverify.TargetFor, diagOut io.Writer) error {
	fp := fetchedPlugin{
		URI:            uri,
		ManifestDigest: manifestDigest,
		Kind:           catalog.EntityKindForHint("plugin"),
	}
	// A gate plugin's URI is a host-less bare identity; bind its registry host and
	// path from the workspace registry and the Plugin kind so referrer discovery
	// targets the location it was fetched from.
	location, bound := catalog.BindEntityLocation(cfg.DefaultRegistry, "plugin", uri)
	fp.Host = registryhost.Of(uri)
	if bound {
		fp.Location = location
		fp.Host = registryhost.Of(location)
	}
	// Read the entity stamp and its .wasm digest from the cache-not-pin slot the
	// resolution just populated — the same read the build uses — so the
	// host-function re-derivation runs against the identical bytes the build will.
	// A clean cache miss leaves the host-function inputs zero (skip); only a
	// verification/parse fault or a non-not-found read error halts.
	entity, wasmDigest, ok, err := lazyView.PluginFromCacheByURI(uri)
	if err != nil {
		return err
	}
	if ok {
		artifactRef := entity.ArtifactReference()
		wasmBytes, rerr := oci.ReadCached(workspaceRoot, cacheDir, artifactRef, wasmDigest)
		if rerr != nil && !errors.Is(rerr, oci.ErrFetchNotFound) {
			return fmt.Errorf("read cached .wasm %s: %w", artifactRef, rerr)
		}
		if rerr == nil {
			ent := entity
			fp.Entity = &ent
			fp.EntityURI = uri
			fp.ArtifactURI = artifactRef
			fp.WasmBytes = wasmBytes
		}
	}
	_, err = verifyFetchedPlugin(ctx, fp, cfg, targetFor, diagOut)
	return err
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
