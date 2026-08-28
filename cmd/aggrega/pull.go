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
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
}

func newPullCmd() *cobra.Command {
	opts := &pullFlags{}
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
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pullRun(pullConfig{
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
	c.Flags().BoolVar(&opts.upgrade, "upgrade", false, "Re-resolve dependencies from current registry state, floating each to the highest available version. Bare (no positional URIs) discards the lockfile and re-resolves every entity; with one or more entity URIs, re-resolves only those, holding all other pins and the recorded external inputs unchanged")
	c.Flags().BoolVar(&opts.verify, "verify", false, "Verify the existing lockfile against the current dependency resolution without writing; exits non-zero on drift or missing lockfile. Also reports the compiler and core-plugin versions the lockfile records — reported for audit and never compared against this binary, so a toolchain difference is never drift")
	c.Flags().BoolVar(&opts.includePrerelease, "include-prerelease", false, "Reserved for future version-selection logic; currently a no-op")
	c.Flags().BoolVar(&opts.detail, "detail", false, "Print, per resolved entity, what this pull did — fetched fresh from a registry, reused a cache slot, found in a local catalog, served from inside the compiler binary, or external. Stdout only; not written to the lockfile")
	c.Flags().BoolVar(&opts.quiet, "quiet", false, "Silence the pull-progress surface on stderr. Does not affect stdout. Takes precedence over --progress")
	c.Flags().BoolVar(&opts.progress, "progress", false, "Force the pull-progress surface on even when stderr is not a terminal, printing plain static phase lines. The default is auto: the interactive phase checklist is shown only when stderr is a terminal")
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
	// shared resolution chokepoint. Required: workspaceOrResolve refuses a nil, so
	// a test invoking pullRun directly declares the workspace it resolves.
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
	// builds the production puller, which is nil when the workspace is offline:
	// the network-egress gate refuses before any dial, so no registry tier exists
	// and unresolved references surface as NotFound rather than as a fetch error.
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

// irEntityInfra derives the per-instance source infra for FeatureRecipe
// entities from the IR render-set (graph.Spec) — the exact attachment set the
// renderer renders, assembled across all five tiers (Aggregate component/subsystem,
// Release boundary-wide/component, annotation-expanded) at IR-build. Reading the IR
// (rather than the topology or a Release-boundary walk, each short by tiers) is what
// makes the no-using-instance skip in source-descent congruent with rendering.
//
// Each entry is keyed by the Manifest-mapped FeatureRecipe URI — the verbatim
// string the closure records as the source-descent entity — so the returned map
// joins against closure entities. An instance whose type has no Manifest mapping
// carries no source-descent entity and is skipped. Per-instance infra is the
// IR-resolved entry.Infra, so no Release-side join is needed.
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

// pullRun executes the pull workflow against the supplied
// configuration. Loads workspace config + Release + Manifest +
// Aggregate, walks the catalog and source closures, and writes the
// per-Release lockfile.
//
// Returns wrapped errors on workspace-config validation failure,
// Release/Manifest/Aggregate load failure, closure walk failure
// (typically a missing catalog entity surfaced by closure.ErrUnresolvableURI),
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

	// pull walks the closure to populate the lockfile and never emits IR, so the
	// catalog's source-file accessors go unused. The resolved workspace root is
	// still supplied, because it is the anchor the configured cache directory is
	// written against: with it, the walk steps over cached library.yaml files
	// rather than ingesting them as authored Tier-1 catalog no matter which
	// directory pull is invoked from.
	workspaceRoot := rw.Root()
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
	cat, skipped, err := catalog.LoadPassOneTolerant(workspaceRoot, cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	// Verify the Manifest's mapping table is consistent before the closure walk,
	// so pull halts on a mismatched mapping with the same Loud Guardrail the
	// build does (lock/build parity).
	if err := cat.AssertManifestImplementsConsistency(manifest, manRes.Path, cat.ComponentAncestry); err != nil {
		return err
	}

	progress.phase(pullPhaseDependencies)
	puller := cfg.puller
	if puller == nil {
		// The discovery step consults the local registry index so a version recorded
		// withdrawn is not selected by a reference that did not name it, and every
		// skip is announced on the same writer the deprecation notices use. A
		// workspace with no index built gets a nil screen and an unscreened
		// discovery step.
		puller = catalog.NewOCIPullerWithRetractionScreen(wsCfg, newRetractionScreen(rw, wsCfg.DefaultRegistry, cfg.DiagOut))
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
			rel.Spec.Aggregate: {ResolvedVersion: aggRes.ResolvedVersion, Digest: aggRes.Digest, ManifestDigest: aggRes.ManifestDigest, ResolutionPath: closure.ResolutionPathRegistry},
		}
	} else {
		externals = lockfile.ExternalEntitiesFromBytes(rel, aggRes.Bytes, manRes.Bytes)
	}
	// The prior lockfile is read exactly once — by this one decision, which performs
	// that read and hands the value back — and every later consumer reads what it
	// returned rather than the file: the reuse verification handed to the closure
	// lookup below, and the deprecation and external-input carry-forwards at the end
	// of the pull. Which pins are honored, and whether the prior lockfile is read at
	// all, is that decision's to make: a bare --upgrade floats every pin and carries
	// forward neither the recorded deprecations nor the external inputs, and holding
	// the read together with the policy that governs it is what keeps the three
	// consumers from disagreeing about it.
	//
	// The file on disk is left untouched by the decision. It is replaced only by the
	// single write at the end of a successful pull, so a refresh that cannot complete
	// leaves the previous pins in place rather than destroying pins it never replaced.
	//
	// The invocation the decision reads is named once here and handed to both of
	// its consumers — the reuse decision itself, and the verdict-plugin trust
	// record written at the end, which has to know whether this run re-resolves a
	// given attachment. A second composite literal would be a second answer to
	// "what kind of pull is this", and the two could then disagree.
	//
	// Not every identity a pull re-resolves is pinned. A plugin attached in a role
	// that only returns an accept-or-reject verdict shapes no rendered output, so its
	// identity is recorded beside the lockfile rather than pinned in it — and a
	// named target checked against the pin set alone would be refused before the run
	// could reach the record it governs. The attachments the workspace configures,
	// and the identities that record already holds, are therefore handed in as
	// targets the decision accepts without a pin behind them.
	//
	// The record is read here rather than only at the two places that compare and
	// write it, because a target naming a plugin the configuration no longer
	// attaches has to be accepted for the run to be able to drop its entry — and the
	// only place that target can come from is the record. A record that exists and
	// does not satisfy its schema halts the pull at this point, which is the same
	// fail-closed reading every other consumer of the file takes.
	recordedTrust, _, err := pluginstrust.LoadOrStart(pluginstrust.PathFor(cfg.ReleasePath))
	if err != nil {
		return err
	}
	pinMode := catalog.PinReuseMode{Upgrade: cfg.Upgrade, UpgradeTargets: cfg.UpgradeTargets, VerifyOnly: cfg.Verify}
	pinReuse, err := catalog.PinReuseFromLockfile(lockfilePath, cfg.ReleasePath, pinMode,
		verdictUpgradeTargets(wsCfg, recordedTrust),
		func(notice string) { fmt.Fprintf(cfg.DiagOut, "  reuse: %s\n", notice) })
	if err != nil {
		return err
	}
	lookup := cat.ClosureLookupWithReuse(puller, workspaceRoot, cacheDir, externals, registrySeeds, pinReuse.Verification)
	progress.phase(pullPhaseClosure)
	// Reference shape is judged before the walk, for the same reason the shared
	// closure resolver judges it: a mapping value the reference grammar cannot
	// express matches no entity, so the walk would report a recipe that is in the
	// catalog as absent. This path computes its own closure rather than going
	// through the shared resolver, so it carries the check itself.
	if faults := catalog.ManifestRecipeReferenceFaults(manifest); len(faults) > 0 {
		return withSkipContext(faults[0], skipped)
	}
	cl, err := closure.ComputeClosure(rel, manifest, types.Normalize(aggregate), wsCfg, lookup)
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
	// Transitive sub-aggregate descent: pin the closure of every Aggregate
	// reachable through a chain of sub-aggregate references, loading each from the
	// cache the resolution just populated. This is where the pins are WRITTEN; a
	// build or a serve drift check re-derives the same axis hermetically from the
	// pinned cache, so the two sides must reach the identical set.
	aggLoader, aggErr := closure.RequireAggregateLoader(lookup)
	if aggErr != nil {
		return aggErr
	}
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
		if errors.Is(err, catalog.ErrAmbiguousProviderPlugin()) && providerBindingHelperEligible(cfg) {
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
	// Resolve the transform-role governance attachments (label-transform /
	// annotation-transform) to their pinned lockfile records, in authored order. A
	// transform role shapes output bytes, so its attachment set / order / role /
	// export function is recorded for the lockfile and build reconstructs it from
	// there rather than from live config — keeping build output invariant to a
	// plugins.governance transform change made after the pull.
	mutatorAttachments, err := resolveMutatorAttachments(wsCfg.Plugins.TransformGovernanceRefs(), lazyView)
	if err != nil {
		return err
	}
	// Record the verdict-role attachments' external rule-input digests for the
	// lockfile. This is provenance, not a pin: the verdict plugin is cache-not-pin
	// and reads its rule inputs live, so only the digest is recorded — enough to
	// audit the inputs that shaped a verdict and to surface a changed rule file as
	// a lockfile diff on the next pull.
	verdictInputs, err := resolveVerdictInputs(wsCfg.VerdictAttachments())
	if err != nil {
		return err
	}
	// Cache the verdict-socket (policy / linter) gate plugins so a hermetic build
	// can run them, WITHOUT pinning them in the lockfile. A verdict socket renders
	// an accept/reject verdict without shaping the output bytes, so its plugin is
	// not part of the reproducibility closure (cache-not-pin): the pull fetches and
	// caches its entity + .wasm, and the build resolves it from the cache by URI.
	verdictHTTPClient, _ := workspaceEgressClient(wsCfg)
	// The identities the caching walk resolved are collected here and recorded
	// further down, after the verify-only branch has had its chance to return.
	// Collecting and recording are deliberately separate steps: the caching walk
	// runs on every pull, including a verify-only one, and a verify-only pull
	// writes nothing at all.
	verdictResolutions, err := cacheVerdictSocketPlugins(context.Background(), wsCfg, lookup, lazyView, workspaceRoot, cacheDir, remoteTargetForWith(verdictHTTPClient), cfg.DiagOut)
	if err != nil {
		return err
	}
	// Hold each resolved identity to the one recorded as trusted for it. This sits
	// ABOVE the verify-only return below on purpose: comparing needs no write, so
	// the mode that exists to report whether the committed files still describe
	// what resolves today covers the record as well as the lockfile. It also sits
	// as early as the identities exist, so a disagreement stops the run before it
	// does further work on a resolution it is not going to accept.
	if err := holdVerdictPluginsToTheirRecord(cfg.ReleasePath, verdictResolutions, pinMode); err != nil {
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
		// Report the toolchain the lockfile records. Verify is the surface a user
		// reaches to find out what a lockfile says, and it has the file loaded
		// already, so the stamp is read back here rather than through a command of
		// its own. It is reported and never compared against this binary: the
		// toolchain is not part of the pinned closure, and checking it would pin the
		// compiler by proxy.
		printLockfileToolchain(out, lf.Toolchain)
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
	// callback, so the entire source-descent runs while parameters are live
	// and nothing here can hold them past it. The descent reads BoundaryInfra
	// and the materialised IR pillars, never the raw parameter map.
	if err := pipeline.ResolveAndFlatten(
		context.Background(),
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			AggregatePath:     catalog.RelativeToWorkspace(workspaceRoot, aggRes.Path),
		},
		pullLookup,
		lazyView.RecipeLookupWithManifest(manifest).Recipe,
		lazyView.SubAggregateLoader(),
		func(rr *resolver.ResolveResult, topo *topology.TopologyResult) error {

			// Key per-instance infra by the Manifest-mapped entity URI — the same
			// verbatim string the closure walker records as the source-descent entity
			// (catalog.ComponentRecipeURI / FeatureRecipeURI return the mapping value
			// verbatim), so this map joins against closure entities regardless of
			// short-form vs full-path mapping authoring. An instance with no Manifest
			// mapping carries no source-descent entity and is skipped.
			perEntityInfra := map[string][]closure.ComponentSourceInfra{}

			// Attachment instances: derive from the IR render-set, not a Release walk.
			// The renderer renders graph.Spec (the IR), whose attachment set is
			// assembled across all five tiers (Aggregate component/subsystem, Release
			// boundary-wide/component, annotation-expanded) at IR-build — a Release-boundary
			// walk and the topology are each short by tiers, so the using-set must come from
			// the IR for the no-using-instance skip to be congruent with rendering. Building
			// the IR here is acyclic (it consumes topology + resolve + catalog and copies
			// the recipe source pointer, never a fetched artifact); pull thus inherits the
			// IR-build failure surface. Per-instance infra is the IR-resolved entry.Infra,
			// so no Release-side join is needed.
			graph, _, _, err := ir.BuildIR(ir.BuildIRConfig{
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

			lf := buildLockfile(cfg.ReleasePath, cl, wsCfg, resolvedBindings, mutatorAttachments, verdictInputs, postFreezeRecipeDigests)
			// Carry forward each unchanged pin's recorded deprecation fact from the
			// prior lockfile before verification runs. A warm-cache (Tier-2) re-pull
			// does not re-touch the registry, so it does not re-discover markers; the
			// prior fact holds for a pin whose digest is unchanged. A Tier-3
			// re-discovery below overwrites it with a fresh finding. Without this, a
			// build's honoring auto-pull would silently drop a recorded deprecation
			// and mask a deprecated dependency.
			carryForwardDeprecation(lf, pinReuse.Prior)
			// Carry forward the prior lockfile's external-input pins. These are
			// render-time remote-input pins recorded by build and acquire, not
			// part of the catalog closure buildLockfile materialises, so a pull
			// would otherwise rewrite the lockfile with an empty list and force
			// the next build to re-acquire from the network. A bare --upgrade is a
			// full refresh and reads no prior lockfile, so this carries nothing and
			// the pins refresh on the next build.
			carryForwardExternalInputs(lf, pinReuse.Prior)
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
			// Record the identity resolved for each verdict-returning attachment,
			// beside the lockfile just written, and drop any entry the configuration
			// no longer accounts for when this run asked for that. This runs only
			// here, past the verify-only return above, so a verify-only pull neither
			// records nor drops anything.
			//
			// The attachment set goes in alongside the resolutions because the two
			// answer different halves of one question. The resolutions say what was
			// acquired, which is what gets recorded; the attachments say what the
			// configuration still names, which is what keeps an entry from being
			// dropped — and a detached plugin is precisely the case with an entry and
			// no resolution.
			trustedCount, err := recordVerdictPluginTrust(cfg.ReleasePath, wsCfg.VerdictAttachments(), verdictResolutions, pinMode, cfg.DiagOut)
			if err != nil {
				return err
			}

			// Clear the in-place surface before the stdout summary so the
			// progress lines never linger beside the final output.
			progress.finish()
			fmt.Fprintf(out, "Pull complete: %s (entities=%d, source_artifacts=%d%s)\n",
				lockfilePath, len(cl.Entities), len(cl.SourceArtifacts), trustedPluginsSummary(trustedCount))
			return nil
		}); err != nil {
		return err
	}
	return nil
}

// buildLockfile materialises a *lockfile.Lockfile from the closure
// result. Field-for-field copy across the closure / lockfile package
// boundary; both types intentionally mirror the same shape.
func buildLockfile(releasePath string, cl closure.Closure, wsCfg workspaceconfig.Config, bindings map[string]lockfile.ProviderBinding, mutators []lockfile.MutatorAttachment, verdictInputs []lockfile.VerdictInput, postFreezeRecipeDigests map[string]string) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     filepath.Base(releasePath),
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		// Records which compiler and which core plugins produced the file.
		// Versions only, so it stays identical across machines that resolved the
		// same closure with the same compiler; advisory, so nothing compares it.
		Toolchain: toolchainStamp(),
	}

	for _, b := range bindings {
		lf.Providers = append(lf.Providers, b)
	}
	sort.Slice(lf.Providers, func(i, j int) bool { return lf.Providers[i].Capability < lf.Providers[j].Capability })

	// Author order is semantic for the transform runner, so the attachments are
	// carried through unsorted (WriteLockfile likewise preserves the order). The
	// copy avoids aliasing the caller's slice.
	lf.Mutators = append([]lockfile.MutatorAttachment(nil), mutators...)

	// Verdict-input digest records are provenance (WriteLockfile canonicalises
	// their order), so they are carried through with only an aliasing-safe copy.
	lf.VerdictInputs = append([]lockfile.VerdictInput(nil), verdictInputs...)

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
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.SchemaLibraries, func(i, j int) bool { return lf.SchemaLibraries[i].URI < lf.SchemaLibraries[j].URI })

	for _, l := range cl.MappingLibraries {
		lf.MappingLibraries = append(lf.MappingLibraries, lockfile.MappingLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.MappingLibraries, func(i, j int) bool { return lf.MappingLibraries[i].URI < lf.MappingLibraries[j].URI })

	for _, l := range cl.WorkloadLibraries {
		lf.WorkloadLibraries = append(lf.WorkloadLibraries, lockfile.WorkloadLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.WorkloadLibraries, func(i, j int) bool { return lf.WorkloadLibraries[i].URI < lf.WorkloadLibraries[j].URI })

	for _, l := range cl.ArtifactsLibraries {
		lf.ArtifactsLibraries = append(lf.ArtifactsLibraries, lockfile.ArtifactsLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
			Consumers:       copyConsumers(l.Consumers),
		})
	}
	sort.Slice(lf.ArtifactsLibraries, func(i, j int) bool { return lf.ArtifactsLibraries[i].URI < lf.ArtifactsLibraries[j].URI })

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

// resolveMutatorAttachments resolves each configured transform-role governance
// attachment (label-transform / annotation-transform) to the pinned record the
// lockfile carries: the plugin's resolved coordinate identity, the transform
// role, the export override, the attachment's settings object, and a
// content-addressed pin of each declared source file, in authored order. Build
// and serve reconstruct the attachment list from these records rather than from
// live workspace config, so the set / order / role / export function / settings /
// source content that shapes the rendered output is config-invariant after the
// pull.
//
// Each attachment's Plugin entity resolves through the same pinned lazy view the
// rest of the pull uses; the coordinate is derived through the single
// Plugin.Coordinate rule, the same identity ProviderBinding.Plugin records. A
// ref that does not resolve is a build fault (the closure walk already pinned
// the transform-role governance entity, halting loudly on a missing one), so it halts here rather
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
			return nil, fmt.Errorf("resolve: transform-role governance plugin %q not resolvable for lockfile pin", ref.URI)
		}
		att := lockfile.MutatorAttachment{
			Plugin:   p.Coordinate(),
			Function: ref.Function,
			// Pin the transform role verbatim so the build reconstructs the
			// attachment at the runner it was authored for. The role is recorded on
			// every attachment (never inferred at reconstruction), so the pinned
			// dispatch cannot diverge from the authored one.
			Role: string(ref.Role),
		}
		if ref.With != nil {
			att.Settings = ref.With.Settings
			for _, src := range ref.With.Sources {
				content, err := os.ReadFile(src)
				if err != nil {
					return nil, fmt.Errorf("resolve: read transform-role governance source %q: %w", src, err)
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

// resolveVerdictInputs records, for each verdict-role governance attachment in
// the workspace's verdict attachment set, the content digest of every external
// rule input it reads at compile time — a Kyverno / Rego file supplied through
// the attachment's with.sources. Unlike a transform attachment this is
// PROVENANCE, not a build input: the verdict plugin is cache-not-pin and reads
// its rule inputs LIVE, so the lockfile records only the digest (never the
// content), so the inputs that shaped an accept/reject decision are auditable
// and a changed rule file surfaces as a lockfile diff on the next pull. An
// attachment that declares no sources contributes no entry. Digests are recorded
// in source base-name order for a deterministic record.
//
// The verdict plugin is cache-not-pin and so is absent from the entities /
// plugins sections; the record therefore identifies the attachment by its
// authored URI (self-standing provenance), not by a resolved coordinate that
// would join into those sections.
func resolveVerdictInputs(atts []workspaceconfig.VerdictAttachment) ([]lockfile.VerdictInput, error) {
	if len(atts) == 0 {
		return nil, nil
	}
	var out []lockfile.VerdictInput
	for _, att := range atts {
		// The record identifies an attachment by (plugin, role), so it can carry only
		// an attachment that HAS a role: a linter attachment is a verdict attachment
		// and is read from the same set, but it has no role to key by, so its rule
		// inputs are not recorded here. The narrowing is on what the record can
		// identify, never a second answer to which attachments are verdict
		// attachments. It reads the socket rather than the role string, because the
		// governance socket is the only one whose verdict membership is decided by a
		// role — a role authored onto any other socket's attachment resolves to none
		// of the roles this record can key by.
		if att.Socket != workspaceconfig.SocketGovernance {
			continue
		}
		ref := att.Ref
		if ref.With == nil || len(ref.With.Sources) == 0 {
			continue
		}
		vi := lockfile.VerdictInput{
			Plugin: ref.URI,
			Role:   string(ref.Role),
		}
		for _, src := range ref.With.Sources {
			content, err := os.ReadFile(src)
			if err != nil {
				return nil, fmt.Errorf("resolve: read verdict socket source %q: %w", src, err)
			}
			vi.Sources = append(vi.Sources, lockfile.VerdictSourcePin{
				Path:   filepath.Base(src),
				Digest: digest.ContentDigest(content),
			})
		}
		sort.Slice(vi.Sources, func(i, j int) bool { return vi.Sources[i].Path < vi.Sources[j].Path })
		out = append(out, vi)
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
// A capability-only / empty-URI attachment carries nothing to fetch and nothing to
// identify, so the verdict attachment set omits it; an attachment whose ENTITY does
// not resolve is skipped rather than treated as a closure gap — a verdict gate is
// not on the pinned axis a missing closure entity would halt.
//
// The skip covers the entity axis only, and the artifact axis deliberately does
// not share it: an attachment whose entity resolves while its `.wasm` does not
// HALTS. That is the configured-but-unloadable gate, and a gate an operator
// attached which no build can load is exactly the case that must fail loudly
// rather than pass with the gate quietly absent.
//
// It returns the identity each attachment's plugin resolved to, in attachment
// order, for the caller to record beside the lockfile. Returning them rather than
// recording them here is what keeps a verify-only pull from writing: the caching
// walk runs on every pull, and only the writing pull reaches the recording step.
// An attachment whose plugin is not in the content cache yields no identity — the
// same clean-miss skip the verification below takes.
func cacheVerdictSocketPlugins(ctx context.Context, cfg workspaceconfig.Config, lookup closure.Lookup, lazyView *catalog.LazyLookup, workspaceRoot, cacheDir string, targetFor pullverify.TargetFor, diagOut io.Writer) ([]verdictPluginResolution, error) {
	var resolutions []verdictPluginResolution
	for _, att := range cfg.VerdictAttachments() {
		ref := att.Ref
		entRes, err := lookup.ResolveCatalogEntity(ref.URI, "plugin")
		if err != nil {
			return nil, fmt.Errorf("cache %s socket plugin %q: %w", att.Socket, ref.URI, err)
		}
		if entRes.NotFound {
			continue
		}
		bare, version := splitAttachmentRef(ref.URI)
		if version == "" {
			version = entRes.ResolvedVersion
		}
		artRes, err := lookup.ResolvePlugin(bare, version)
		if err != nil {
			return nil, fmt.Errorf("cache %s socket plugin artifact %q: %w", att.Socket, ref.URI, err)
		}
		if artRes.NotFound {
			return nil, fmt.Errorf("cache %s socket plugin artifact %q: entity resolved but its .wasm did not: %w",
				att.Socket, ref.URI, closure.ErrUnresolvableURI())
		}
		resolved, err := verifyCachedVerdictSocketPlugin(ctx, cfg, lazyView, ref.URI, artRes.ManifestDigest, workspaceRoot, cacheDir, targetFor, diagOut)
		if err != nil {
			return nil, fmt.Errorf("verify %s socket plugin %q: %w", att.Socket, ref.URI, err)
		}
		if resolved.Coordinate == "" {
			continue
		}
		// The authored reference minus any version suffix is carried alongside the
		// resolved identity because a targeted upgrade names that form, not the
		// identity coordinate: the two coincide for a bare-name attachment and
		// diverge for a location-shaped one.
		resolved.BareURI = bare
		resolutions = append(resolutions, resolved)
	}
	return resolutions, nil
}

// verifyCachedVerdictSocketPlugin routes a freshly cached verdict-socket gate
// plugin through the shared fetch-verification home. It binds the plugin's
// host-less bare identity to its registry location for signature referrer
// discovery (the same binding the pinned plugin-entity signature uses) and reads
// the entity stamp + content-verified .wasm from the pull-populated cache (the
// same cache-not-pin read the build resolves the gate through), then hands both
// to verifyFetchedPlugin. The verified fact is discarded: a verdict gate is
// cache-not-pin and never enters the lockfile.
//
// It also returns the identity that one cache read reports — the plugin's identity
// coordinate, the version its entity declares, and the content digest of its .wasm
// layer. That triple comes from THIS read and no other, because the same read is
// what a later resolution of the same plugin performs: a second derivation of the
// digest from the manifest descriptor, or of the version from the entity
// resolution, would let the recorded identity and the resolved one disagree while
// both looked right. A clean cache miss returns the zero value, whose empty
// coordinate is the caller's signal that there was nothing to identify.
func verifyCachedVerdictSocketPlugin(ctx context.Context, cfg workspaceconfig.Config, lazyView *catalog.LazyLookup, uri, manifestDigest, workspaceRoot, cacheDir string, targetFor pullverify.TargetFor, diagOut io.Writer) (verdictPluginResolution, error) {
	var identity verdictPluginResolution
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
		return verdictPluginResolution{}, err
	}
	if ok {
		artifactRef := entity.ArtifactReference()
		identity = verdictPluginResolution{
			Coordinate: artifactRef,
			Version:    entity.Metadata.Version,
			Digest:     wasmDigest,
		}
		wasmBytes, rerr := oci.ReadCached(workspaceRoot, cacheDir, artifactRef, wasmDigest)
		if rerr != nil && !errors.Is(rerr, oci.ErrFetchNotFound()) {
			return verdictPluginResolution{}, fmt.Errorf("read cached .wasm %s: %w", artifactRef, rerr)
		}
		if rerr == nil {
			ent := entity
			fp.Entity = &ent
			fp.EntityURI = uri
			fp.ArtifactURI = artifactRef
			fp.WasmBytes = wasmBytes
		}
	}
	if _, err = verifyFetchedPlugin(ctx, fp, cfg, targetFor, diagOut); err != nil {
		return verdictPluginResolution{}, err
	}
	// Withdrawal discovery runs at this same registry-touching moment, over the same
	// bound location the signature verification above just used. It runs only where
	// something was identified: with no identity there is no record to carry a
	// withdrawal, so discovering one would have nowhere to go.
	if identity.Coordinate != "" {
		rec, derr := discoverVerdictPluginDeprecation(ctx, cfg, pullverify.Entity{
			URI:            fp.URI,
			ManifestDigest: fp.ManifestDigest,
			Host:           fp.Host,
			Location:       fp.Location,
			Kind:           fp.Kind,
		}, targetFor, diagOut)
		if derr != nil {
			return verdictPluginResolution{}, derr
		}
		if rec != nil {
			identity.Deprecation = &pluginstrust.Deprecation{
				Message:  rec.Message,
				Severity: rec.Severity,
			}
		}
	}
	return identity, nil
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
