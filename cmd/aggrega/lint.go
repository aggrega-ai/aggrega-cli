// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// lint command flags. Cobra populates these globals from the
// invocation; lintRun reads them through lintConfig for testability.
var (
	lintEntityPath  string
	lintReleasePath string
	lintPrePublish  bool
	lintPluginPath  string

	// Tri-state policy override flags. Both
	// `allow_*` (6) and `enforce_*` (2) families get paired CLI
	// flags. resolvePolicy decides effective value per cobra
	// Flags().Changed() state.
	lintAllowDynamicRefs          bool
	lintNoAllowDynamicRefs        bool
	lintAllowIDOverrides          bool
	lintNoAllowIDOverrides        bool
	lintAllowDirectXAggregaExt    bool
	lintNoAllowDirectXAggregaExt  bool
	lintAllowAggregaAIPrefix      bool
	lintNoAllowAggregaAIPrefix    bool
	lintAllowNonstandardNames     bool
	lintNoAllowNonstandardNames   bool
	lintAllowCategoryMismatches   bool
	lintNoAllowCategoryMismatches bool
	lintEnforceMonotonicNarrowing bool
	lintNoEnforceMonotonic        bool
	lintEnforceImmutability       bool
	lintNoEnforceImmutability     bool
)

func newLintCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "lint [entity-path]",
		Short: "Validate the whole catalog, or run the publishing-linter over one entity",
		Long: `aggrega lint has two modes, selected by whether an entity path is given.

Without an entity argument, lint validates the WHOLE catalog: it loads
every catalog entity and runs the schema-pillar (Pass-2) and library
resolution/rejection checks — the same correctness validation a build
applies, but over the entire catalog rather than one Release's closure.
This is the place a broken catalog entity is meant to surface (CI,
pre-publish); a build validates only the Release it processes.

With an entity path, lint runs the publishing-linter rule set against
that entity. In default mode the rules run at warning severity over
local content without consulting any registry. With --pre-publish the
previous registry version is fetched and the schema-evolution
classification is compared against the proposed version bump; mismatches
halt. Per-workspace policy flags from .aggrega-config.yaml's
lint.publishing_discipline block can be overridden per invocation via
the --allow-*/--no-allow-* and --enforce-*/--no-enforce-* flag pairs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := lintEntityPath
			if path == "" && len(args) > 0 {
				path = args[0]
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			if lintReleasePath != "" {
				// Release-closure mode is mutually exclusive with a positional /
				// --entity target — they select different closures.
				if path != "" {
					return fmt.Errorf("OPS_ERROR: --release is mutually exclusive with an entity argument; pass one or the other")
				}
				return lintRelease(rw, lintReleasePath, os.Stderr, os.Stdout)
			}
			if path == "" {
				// No entity argument → whole-catalog validation.
				return lintCatalogWideFromWorkspace(rw)
			}
			return lintRun(lintConfig{
				EntityPath:     path,
				PrePublish:     lintPrePublish,
				PluginPath:     lintPluginPath,
				PolicyResolver: makeResolverFromCobra(cmd),
				ws:             rw,
			})
		},
	}
	c.Flags().StringVar(&lintReleasePath, "release", "", "Path to a Release YAML to lint over its resolved closure (closure-scoped schema-pillar overlap + pillar validation; mutually exclusive with an entity argument)")
	c.Flags().StringVar(&lintEntityPath, "entity", "", "Path to the entity YAML to lint (alternatively pass as the positional arg)")
	c.Flags().BoolVar(&lintPrePublish, "pre-publish", false, "Fetch the previous registry version and verify the proposed bump against the schema-evolution classification")
	c.Flags().StringVar(&lintPluginPath, "plugin", "", "Filesystem path to the publishing-linter .wasm artifact (overrides workspace plugins.linter[0].uri)")

	// Tri-state allow_* flags
	c.Flags().BoolVar(&lintAllowDynamicRefs, "allow-dynamic-refs", false, "Force allow_dynamic_refs=true for this invocation")
	c.Flags().BoolVar(&lintNoAllowDynamicRefs, "no-allow-dynamic-refs", false, "Force allow_dynamic_refs=false for this invocation")
	c.Flags().BoolVar(&lintAllowIDOverrides, "allow-id-overrides", false, "Force allow_id_overrides=true for this invocation")
	c.Flags().BoolVar(&lintNoAllowIDOverrides, "no-allow-id-overrides", false, "Force allow_id_overrides=false for this invocation")
	c.Flags().BoolVar(&lintAllowDirectXAggregaExt, "allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=true")
	c.Flags().BoolVar(&lintNoAllowDirectXAggregaExt, "no-allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=false")
	c.Flags().BoolVar(&lintAllowAggregaAIPrefix, "allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=true")
	c.Flags().BoolVar(&lintNoAllowAggregaAIPrefix, "no-allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=false")
	c.Flags().BoolVar(&lintAllowNonstandardNames, "allow-nonstandard-names", false, "Force allow_nonstandard_names=true")
	c.Flags().BoolVar(&lintNoAllowNonstandardNames, "no-allow-nonstandard-names", false, "Force allow_nonstandard_names=false")
	c.Flags().BoolVar(&lintAllowCategoryMismatches, "allow-category-mismatches", false, "Force allow_category_mismatches=true")
	c.Flags().BoolVar(&lintNoAllowCategoryMismatches, "no-allow-category-mismatches", false, "Force allow_category_mismatches=false")

	// Tri-state enforce_* flags
	c.Flags().BoolVar(&lintEnforceMonotonicNarrowing, "enforce-monotonic-narrowing", false, "Force enforce_monotonic_narrowing=true")
	c.Flags().BoolVar(&lintNoEnforceMonotonic, "no-enforce-monotonic-narrowing", false, "Force enforce_monotonic_narrowing=false")
	c.Flags().BoolVar(&lintEnforceImmutability, "enforce-immutability", false, "Force enforce_immutability=true")
	c.Flags().BoolVar(&lintNoEnforceImmutability, "no-enforce-immutability", false, "Force enforce_immutability=false")

	return c
}

// lintCatalogWideFromWorkspace resolves the workspace's catalog directories and
// cache from .aggrega-config.yaml, then runs the whole-catalog validation pass.
// Catalog dirs default to the workspace root when none are configured (mirroring
// the daemon's catalog resolution).
func lintCatalogWideFromWorkspace(rw *workspaceconfig.ResolvedWorkspace) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Whole-catalog lint enumerates an entire catalog tree. With no
	// catalog_dirs configured it must NOT fall back to scanning the workspace
	// root — that pulls in unrelated trees (a subrepo's testdata/ fixtures) and
	// surfaces as a misleading duplicate-identity dump. Require an explicit
	// catalog_dirs and say so plainly. (Release-scoped commands resolve a
	// specific closure, not a whole-tree enumeration, so they keep a working-
	// directory default; the footgun is specific to whole-catalog enumeration.)
	if len(wsCfg.Workspace.CatalogDirs) == 0 {
		return fmt.Errorf("OPS_ERROR: whole-catalog lint needs catalog directories to scan; set `workspace.catalog_dirs` in .aggrega-config.yaml (or lint a specific Release with --release)")
	}
	dirs := resolveCatalogDirs(nil, wsCfg, wsRoot)
	return lintCatalogWide(wsRoot, cacheDir, dirs, os.Stderr, os.Stdout)
}

// lintCatalogWide runs the whole-catalog correctness validation: it loads every
// catalog entity under dirs and applies the schema-pillar (Pass-2) and library
// resolution/rejection checks (LoadAllWithCacheDir), then resolves the extends
// chains. This is the catalog-wide counterpart to a build's per-Release closure
// validation — the home where a broken catalog entity is meant to surface (CI,
// pre-publish). It halts loudly on any schema-pillar violation, unresolvable or
// invalid library, or extends-resolution failure. Advisory and error
// diagnostics are written to diagOut (os.Stderr in production); the final
// success summary is written to out (os.Stdout in production).
func lintCatalogWide(wsRoot, cacheDir string, dirs []string, diagOut, out io.Writer) error {
	cat, err := catalog.LoadAllWithCacheDir(wsRoot, cacheDir, dirs...)
	if err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(diagOut, "  extends: %s\n", e)
		}
		return fmt.Errorf("catalog validation failed: %d extends resolution error(s)", len(extErrs))
	}
	if err := verifyNoOCILocationCollision(cat); err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	catRecipes := make([]types.Recipe, 0, len(cat.Recipes))
	for _, r := range cat.Recipes {
		catRecipes = append(catRecipes, r)
	}
	catFeatureRecipes := make([]types.FeatureRecipe, 0, len(cat.FeatureRecipes))
	for _, fr := range cat.FeatureRecipes {
		catFeatureRecipes = append(catFeatureRecipes, fr)
	}
	if errs := inputKindCapabilityErrors(catRecipes, catFeatureRecipes); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
		}
		return fmt.Errorf("catalog validation failed: %d input-kind capability error(s)", len(errs))
	}
	// Surface the whole-catalog advisory notices accumulated during the eager
	// passes above — schema-pillar overlap (Pass-2) and narrowing-on-extend (from
	// ResolveAllExtends). Informational only: advisories never escalate the exit
	// code, which stays 0 here (errors already returned above). Emission order is
	// deterministic — lintWarnings is accumulated over sorted-key walks.
	for _, w := range cat.LintWarnings() {
		fmt.Fprintf(diagOut, "  notice: %s\n", w)
	}
	fmt.Fprintln(out, "catalog validation passed (schema-pillar + library + extends + oci-location)")
	return nil
}

// lintRelease runs the closure-scoped lint mode: it computes a Release's resolved
// closure through the SAME build machinery the compiler uses (computeBuildClosure
// over the lazy lookup — never a lint-local fork, which would re-open the
// build/lint view divergence) and validates + advises over exactly the entities
// that Release reaches.
//
// Closure-scoped pillar VALIDATION errors fail the lint (non-zero exit), aligning
// with whole-catalog lint's error contract; schema-pillar OVERLAP advisories are
// informational `notice:` lines that never change the exit code, matching the
// build's own advisory treatment. No render runs — this is validation + advice,
// not emission.
//
// The lockfile pins the closure when present (the build's pinned view); absent, the
// closure resolves through Tier-1 (local catalog). lint never triggers a network
// pull — a registry-pinned closure that needs a lockfile surfaces as an honest
// unresolvable-URI error rather than a silent fetch.
//
// Advisory (`notice:`) and error diagnostics are written to diagOut (os.Stderr
// in production); the final success summary is written to out (os.Stdout in
// production).
func lintRelease(rw *workspaceconfig.ResolvedWorkspace, releasePath string, diagOut, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := resolveCatalogDirs(nil, wsCfg, wsRoot)

	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lf, _, lfErr := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if lfErr == nil {
		lazyView.SetPins(catalog.PinsFromLockfile(lf))
	}

	// Shared Release→closure orchestration — the SAME front door the build runs
	// (releaseClosure in build.go), so lint and build compute the identical closure
	// by construction. lint runs no manifest-implements consistency halt (afterManifest
	// nil) and tolerates a missing lockfile: pass the lockfile only when it loaded, so
	// the registry-pin external-seed gate matches the build's exactly.
	var lockForClosure *lockfile.Lockfile
	if lfErr == nil {
		lockForClosure = lf
	}
	rc, err := releaseClosure(releasePath, lazyView, lockForClosure, wsCfg, wsRoot, cacheDir, nil)
	if err != nil {
		return err
	}
	rel := rc.Release
	manifest := rc.Manifest
	aggRes := rc.Aggregate
	cl := rc.Closure

	// Pillar-validation errors fail the lint; resolve extends first so composed
	// (parent+child) schemas validate, matching the build.
	if extErrs := lazyView.ResolveClosureExtends(cl); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(diagOut, "  extends: %s\n", e)
		}
		return fmt.Errorf("release closure validation failed: %d extends resolution error(s)", len(extErrs))
	}
	if err := lazyView.ValidateClosureScopedPillars(cl); err != nil {
		return fmt.Errorf("release closure validation failed: %w", err)
	}

	// Authored-value conformance: validate the Release's resolved (post-parameter-
	// interpolation) values against the closure's composed schemas, via the build's
	// SHARED Step-1 validation (ResolveAndValidate — the same guard suite the build
	// runs pre-topology, no flatten/render). A real conformance violation is an
	// error (non-zero exit), distinct from the advisories below. The composed
	// EffectiveSchema (extends parent+child) flows in through the lookup, so an
	// inherited-pillar value is validated against the inherited constraint.
	conformanceLookup := lazyView.AsCatalogLookup()
	conformanceLookup.ProviderInfraSchema = lazyView.ProviderInfraSchemaResolver(manifest)
	verr := pipeline.ResolveAndValidate(context.Background(), pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggRes.Bytes,
		AggregatePath:     catalog.RelativeToWorkspace(wsRoot, aggRes.Path),
	}, conformanceLookup, func(*resolver.ResolveResult) error {
		// Validation IS the Step-1 work (annotation + guard suite) that runs
		// inside ResolveAndValidate; lint needs nothing further from the
		// resolved view, and the parameter purge is owned by the resolver.
		return nil
	})
	if verr != nil {
		return fmt.Errorf("release value conformance failed: %w", verr)
	}

	// Schema-pillar overlap + narrowing-on-extend advisories — informational, never
	// change the exit code (the compiler does not police narrow-vs-relax). The
	// narrowings were computed by ResolveClosureExtends above (same composition as
	// the effective schema), not recomputed here.
	for _, w := range lazyView.ClosureLintWarnings(cl) {
		fmt.Fprintf(diagOut, "  notice: %s\n", w)
	}
	for _, w := range lazyView.ClosureNarrowingWarnings() {
		fmt.Fprintf(diagOut, "  notice: %s\n", w)
	}
	if errs := inputKindCapabilityErrors(closureRecipes(lazyView, cl)); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
		}
		return fmt.Errorf("release closure validation failed: %d input-kind capability error(s)", len(errs))
	}

	fmt.Fprintf(out, "release closure validation passed: %s (schema-pillar + closure pillars)\n", releasePath)
	return nil
}

// inputKindCapabilityErrors reports each Recipe / FeatureRecipe that declares a
// workload: block (synthesized mode) bound to a KNOWN provider whose engine
// cannot synthesize — a guaranteed render-time halt, surfaced here at lint time.
// It is an early, non-authoritative detector: the render-time central input-kind
// gate remains the single enforcement chokepoint. The detector reads engine
// capabilities through enginefactory.CapabilitiesFor — the same Dispatch the gate
// uses — so the two cannot disagree, and it reuses the gate's own message clause
// (renderer.UnsupportedInputKindMessage) so the early and late diagnostics read
// identically. A provider whose name binds to no shipped engine (CapabilitiesFor
// ok=false — unknown, unimplemented terraform/cloudformation, or eval) is skipped:
// there is no synthesizing engine to contradict and forward-looking catalog
// scaffolding must not be flagged. The workload predicate is the gate's own
// (!Spec.Workload.IsEmpty()), so presence is classified identically.
func inputKindCapabilityErrors(recipes []types.Recipe, featureRecipes []types.FeatureRecipe) []error {
	var errs []error
	check := func(kind, identity, provider string, workload types.WorkloadTemplate) {
		if workload.IsEmpty() {
			return
		}
		caps, ok := enginefactory.CapabilitiesFor(provider)
		if !ok || caps.Synthesized {
			return
		}
		errs = append(errs, fmt.Errorf("%s %q: %s", kind, identity,
			renderer.UnsupportedInputKindMessage(provider, "synthesised", caps)))
	}
	for _, r := range recipes {
		check("Recipe", r.Metadata.Name+":"+r.Metadata.Version, r.Spec.Provider, r.Spec.Workload)
	}
	for _, fr := range featureRecipes {
		check("FeatureRecipe", fr.Metadata.Name+":"+fr.Metadata.Version, fr.Spec.Provider, fr.Spec.Workload)
	}
	// Sort by message so the diagnostic order is deterministic regardless of the
	// caller's iteration order (the whole-catalog mode ranges an unordered map).
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

// closureRecipes resolves the Recipe and FeatureRecipe entities a Release's
// closure reaches, from the closure's recorded kind-hints, for the closure-scoped
// lint detector. Entities the closure classified as neither (KindHint empty or
// another kind) are not recipe-shaped and are skipped; an entry that fails to
// resolve through the lazy view is likewise skipped (the render-time gate remains
// the authoritative backstop).
func closureRecipes(lazyView *catalog.LazyLookup, cl closure.Closure) ([]types.Recipe, []types.FeatureRecipe) {
	var recipes []types.Recipe
	var featureRecipes []types.FeatureRecipe
	for _, e := range cl.Entities {
		switch e.KindHint {
		case "recipe":
			if r, ok := lazyView.RecipeByURI(e.URI); ok {
				recipes = append(recipes, r)
			}
		case "feature_recipe":
			if fr, ok := lazyView.FeatureRecipeByURI(e.URI); ok {
				featureRecipes = append(featureRecipes, fr)
			}
		}
	}
	return recipes, featureRecipes
}

// verifyNoOCILocationCollision halts when two DISTINCT catalog identities map to
// the same OCI repository location (the lowercased, identity-derived coordinate
// from ociLocation). Such a collision would silently overwrite one entity's
// published bytes with another's at population time — the loud counterpart to the
// publish-path location encoding. Grouping is by version-independent identity
// (provider/name for recipes, metadata.name otherwise), so multiple versions of
// one identity sharing a location — distinguished by tag — are not a collision.
func verifyNoOCILocationCollision(cat *catalog.Catalog) error {
	byLoc := map[string]map[string]struct{}{}
	add := func(kind, provider, name string) {
		loc := catalog.OCILocation(kind, provider, name)
		ident := name
		if provider != "" {
			ident = provider + "/" + name
		}
		if byLoc[loc] == nil {
			byLoc[loc] = map[string]struct{}{}
		}
		byLoc[loc][ident] = struct{}{}
	}
	for _, e := range cat.Components {
		add("Component", "", e.Metadata.Name)
	}
	for _, e := range cat.Features {
		add("Feature", "", e.Metadata.Name)
	}
	for _, e := range cat.Providers {
		add("Provider", "", e.Metadata.Name)
	}
	for _, e := range cat.Aggregates {
		add("Aggregate", "", e.Metadata.Name)
	}
	for _, e := range cat.Recipes {
		add("Recipe", e.Spec.Provider, e.Metadata.Name)
	}
	for _, e := range cat.FeatureRecipes {
		add("FeatureRecipe", e.Spec.Provider, e.Metadata.Name)
	}
	for _, e := range cat.SchemaLibraries {
		add("SchemaLibrary", "", e.Metadata.Name)
	}
	for _, e := range cat.MappingLibraries {
		add("MappingLibrary", "", e.Metadata.Name)
	}

	var collisions []string
	for loc, idents := range byLoc {
		if len(idents) <= 1 {
			continue
		}
		names := make([]string, 0, len(idents))
		for id := range idents {
			names = append(names, id)
		}
		sort.Strings(names)
		collisions = append(collisions, fmt.Sprintf("%s <- %s", loc, strings.Join(names, ", ")))
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return fmt.Errorf("%w:\n  - %s", oci.ErrOCILocationCollision, strings.Join(collisions, "\n  - "))
}

// lintConfig is the test-injectable input contract. Tests construct
// it directly; production cobra populates it from the flag globals
// + a PolicyResolver closure built from the cobra Flags().Changed
// state. FetchTarget overrides registry construction for end-to-end
// tests (in-memory oras.ReadOnlyTarget), mirroring push's PushTarget.
type lintConfig struct {
	EntityPath     string
	PrePublish     bool
	PluginPath     string
	PolicyResolver func(flagBase string, wsValue bool) bool
	FetchTarget    oras.ReadOnlyTarget

	// ws is the resolved workspace, threaded in by the command's RunE. Nil only
	// when a test invokes lintRun directly; workspaceOrResolve then resolves
	// from the working directory.
	ws *workspaceconfig.ResolvedWorkspace

	// DiagOut is the sink for lint's diagnostic output (the first-publication
	// note, the pre-release-only warning, the not-resolvable-parent note, and the
	// findings report). Nil defaults to os.Stderr at the top of lintRun
	// (production); tests pass a buffer to capture diagnostics without mutating a
	// process global.
	DiagOut io.Writer
}

// makeResolverFromCobra returns a closure that decides effective
// policy values per cobra Flags().Changed() state. Precedence:
// explicit --no-X > explicit --X > workspace value.
func makeResolverFromCobra(cmd *cobra.Command) func(string, bool) bool {
	return func(flagBase string, wsValue bool) bool {
		if cmd.Flags().Changed("no-" + flagBase) {
			return false
		}
		if cmd.Flags().Changed(flagBase) {
			return true
		}
		return wsValue
	}
}

// lintEntityHeader is the minimum YAML envelope lint parses out of
// the proposed entity file: kind, metadata.name, metadata.version,
// metadata.labels, and spec.extends. Mirrors pushEntityHeader's
// shape and extends it for narrowing-rule parent resolution.
type lintEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string            `yaml:"name"`
		Version string            `yaml:"version"`
		Labels  map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Extends  string `yaml:"extends"`
		Provider string `yaml:"provider"`
	} `yaml:"spec"`
}

// defaultRuleIDs returns the rule set to invoke per mode. Default
// mode runs all structural rules. --pre-publish prepends
// schema-evolution (which needs previous bytes).
//
// LOCKSTEP: the slice MUST match the plugin's lib.rs dispatcher
// arms. Adding a rule requires updating both this
// slice AND aggrega-cli/wasm-plugins/publishing-linter/src/lib.rs.
func defaultRuleIDs(prePublish bool) []string {
	rules := []string{
		"annotation-prefix",
		"name-shape",
		"narrowing",
		"dynamic-refs",
		"id-overrides",
		"category-matches-directory",
		"x-aggrega-discouragement",
	}
	if prePublish {
		return append([]string{"schema-evolution"}, rules...)
	}
	return rules
}

func lintRun(cfg lintConfig) error {
	if cfg.DiagOut == nil {
		cfg.DiagOut = os.Stderr
	}
	if cfg.EntityPath == "" {
		return fmt.Errorf("aggrega lint: entity path required (pass as positional arg or via --entity)")
	}
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsCfg := rw.Config()

	pluginPath, err := resolvePluginPath(cfg.PluginPath, wsCfg)
	if err != nil {
		return err
	}

	proposedBytes, err := os.ReadFile(cfg.EntityPath)
	if err != nil {
		return fmt.Errorf("aggrega lint: read %s: %w", cfg.EntityPath, err)
	}
	proposedJSON, header, err := yamlToLinterJSON(proposedBytes)
	if err != nil {
		return fmt.Errorf("aggrega lint: parse %s: %w", cfg.EntityPath, err)
	}

	resolver := cfg.PolicyResolver
	if resolver == nil {
		resolver = func(_ string, ws bool) bool { return ws }
	}
	policies := effectivePolicies(wsCfg.Lint.PublishingDiscipline, resolver)

	var (
		previousJSON     json.RawMessage
		previousVersion  string
		firstPublication bool
	)
	if cfg.PrePublish {
		previousJSON, previousVersion, err = fetchPreviousRegistryVersion(
			context.Background(), cfg.FetchTarget, wsCfg, header, cfg.DiagOut,
		)
		if err != nil {
			return fmt.Errorf("aggrega lint: fetch previous: %w", err)
		}
		firstPublication = previousVersion == ""
	}

	parentJSON, _ := resolveParent(header, wsCfg, cfg.EntityPath, cfg.DiagOut)

	inputs := buildRuleInputs(header.Kind, proposedJSON, previousJSON, parentJSON, policies, cfg.EntityPath, cfg.PrePublish)
	rawFindings, err := validator.DispatchPublishingLinterBatch(pluginPath, inputs)
	if err != nil {
		return fmt.Errorf("aggrega lint: dispatch: %w", err)
	}

	aggregated := aggregateFindings(rawFindings)
	if !cfg.PrePublish {
		aggregated = downgradeToWarning(aggregated)
	}

	if firstPublication {
		fmt.Fprintf(cfg.DiagOut,
			"first publication of %q; no version-bump verification performed\n",
			header.Metadata.Name)
		aggregated = downgradeFirstPublicationToWarning(aggregated)
	}

	// Bump-vs-classification check runs BEFORE printing findings so
	// the schema-evolution finding can be downgraded to warning when
	// the bump satisfies the classification. The rule body emits the
	// classification at Error severity (it's the rule's only output
	// channel); whether that classification IS a violation depends on
	// the proposed bump direction — which only the CLI knows. When
	// the bump satisfies, the finding is informational; when it
	// doesn't, the CLI surfaces its own explicit error.
	if cfg.PrePublish && !firstPublication {
		verdict := classificationFromFindings(aggregated)
		if verdict != "" {
			proposedBump := bumpDirection(previousVersion, header.Metadata.Version)
			if !proposedBumpSatisfies(proposedBump, verdict) {
				printFindings(aggregated, cfg.DiagOut)
				return fmt.Errorf(
					"aggrega lint --pre-publish: schema-evolution requires %s bump; proposed %s -> %s is %s",
					verdict, previousVersion, header.Metadata.Version, proposedBump,
				)
			}
			// Bump satisfies the classification — downgrade the
			// schema-evolution finding to warning so it doesn't trip
			// the generic-error halt below.
			aggregated = downgradeRuleToWarning(aggregated, "schema-evolution")
		}
	}

	halted := printFindings(aggregated, cfg.DiagOut)
	if halted {
		return fmt.Errorf("aggrega lint: %d error finding(s)", countErrors(aggregated))
	}
	return nil
}

// downgradeRuleToWarning maps Error → Warning for findings whose
// RuleID matches the given id. Used for the schema-evolution rule
// when the proposed bump satisfies its classification verdict; the
// rule emits Error severity to convey the classification, but the
// CLI's halt-on-error logic requires downgrading once the bump is
// verified compatible.
func downgradeRuleToWarning(findings []validator.LinterFinding, ruleID string) []validator.LinterFinding {
	out := make([]validator.LinterFinding, len(findings))
	for i, f := range findings {
		if f.RuleID == ruleID && f.Severity == "error" {
			f.Severity = "warning"
		}
		out[i] = f
	}
	return out
}

// resolvePluginPath returns the filesystem path of the publishing-linter Wasm
// plugin. It is the linter's per-socket URI feeder: the --plugin flag is the
// explicit override, and plugins.linter[0].uri from the resolved workspace
// config is the URI source. The generic URI→path resolution — local-path
// handling and the uniform OCI-not-yet-wired rejection — lives once in
// pkg/pluginpath and is shared with every other plugin socket.
func resolvePluginPath(cliFlag string, wsCfg workspaceconfig.Config) (string, error) {
	uri := ""
	if len(wsCfg.Plugins.Linter) > 0 {
		uri = wsCfg.Plugins.Linter[0].URI
	}
	return pluginpath.Resolve(uri, cliFlag)
}

// looksLikeOCIURI returns true when the URI looks like an OCI
// reference. Heuristic: oci:// scheme, or hub.aggrega.ai host, or
// <host>/<path>:<tag> shape with a slash before the colon.
// yamlToLinterJSON converts the proposed entity YAML to JSON and
// extracts the metadata envelope. Returns the JSON bytes the plugin
// consumes plus the header struct the host inspects for kind, name,
// version, labels, extends.
func yamlToLinterJSON(yamlBytes []byte) (json.RawMessage, lintEntityHeader, error) {
	var raw interface{}
	if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("yaml unmarshal: %w", err)
	}
	cleaned := convertYAMLMaps(raw)
	jsonBytes, err := json.Marshal(cleaned)
	if err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("json marshal: %w", err)
	}
	var header lintEntityHeader
	if err := yaml.Unmarshal(yamlBytes, &header); err != nil {
		return nil, lintEntityHeader{}, fmt.Errorf("yaml header unmarshal: %w", err)
	}
	return jsonBytes, header, nil
}

// convertYAMLMaps recursively converts map[interface{}]interface{}
// (gopkg.in/yaml.v3 default for nested maps) to map[string]interface{}
// so json.Marshal accepts the value.
func convertYAMLMaps(v interface{}) interface{} {
	switch x := v.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[fmt.Sprintf("%v", k)] = convertYAMLMaps(val)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[k] = convertYAMLMaps(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, val := range x {
			out[i] = convertYAMLMaps(val)
		}
		return out
	default:
		return v
	}
}

// effectivePolicies merges workspace policy values with CLI tri-state
// overrides. resolver is called per flag-base name to determine
// effective value.
func effectivePolicies(ws workspaceconfig.PublishingDisciplineConfig, resolve func(string, bool) bool) json.RawMessage {
	exemptions := ws.NarrowingExemptions
	if exemptions == nil {
		exemptions = []workspaceconfig.NarrowingExemption{}
	}
	policies := map[string]any{
		"enforce_monotonic_narrowing":       resolve("enforce-monotonic-narrowing", ws.EnforceMonotonicNarrowing),
		"enforce_immutability":              resolve("enforce-immutability", ws.EnforceImmutability),
		"narrowing_exemptions":              exemptions,
		"allow_dynamic_refs":                resolve("allow-dynamic-refs", ws.AllowDynamicRefs),
		"allow_id_overrides":                resolve("allow-id-overrides", ws.AllowIDOverrides),
		"allow_direct_x_aggrega_extensions": resolve("allow-direct-x-aggrega-extensions", ws.AllowDirectXAggregaExtensions),
		"allow_aggrega_ai_prefix":           resolve("allow-aggrega-ai-prefix", ws.AllowAggregaAIPrefix),
		"allow_nonstandard_names":           resolve("allow-nonstandard-names", ws.AllowNonstandardNames),
		"allow_category_mismatches":         resolve("allow-category-mismatches", ws.AllowCategoryMismatches),
	}
	out, _ := json.Marshal(policies)
	return out
}

// fetchPreviousRegistryVersion lists tags on the entity's registry
// reference, picks the latest non-pre-release, and pulls its bytes.
// Returns ("", "", nil) when no qualifying tag exists (first
// publication. The pre-release-only warning is written to diagOut (os.Stderr
// in production).
func fetchPreviousRegistryVersion(
	ctx context.Context,
	overrideTarget oras.ReadOnlyTarget,
	wsCfg workspaceconfig.Config,
	header lintEntityHeader,
	diagOut io.Writer,
) (json.RawMessage, string, error) {
	target := overrideTarget
	if target == nil {
		reference := buildPushReference(wsCfg.DefaultRegistry, header.Kind, header.Spec.Provider, header.Metadata.Name)
		repo, err := oci.NewRemoteRepository(reference)
		if err != nil {
			return nil, "", fmt.Errorf("construct repo: %w", err)
		}
		target = repo
	}
	tags, err := oci.ListTags(ctx, target)
	if err != nil {
		return nil, "", fmt.Errorf("list tags: %w", err)
	}
	latest := oci.SelectLatestVersion(tags, false /* includePreRelease */)
	if latest == "" {
		// Distinguish "no previous version exists" (true first publication)
		// from "tags exist but all are pre-release" (different shape;
		// operator should know).
		if oci.HasAnySemverTag(tags) {
			fmt.Fprintf(diagOut,
				"warning: registry has only pre-release tags for this entity; "+
					"treating as first publication. Promote a pre-release "+
					"to a stable version to enable bump-vs-classification verification.\n")
		}
		return nil, "", nil
	}
	pull, err := oci.Pull(ctx, target, latest)
	if err != nil {
		return nil, "", fmt.Errorf("pull %s: %w", latest, err)
	}
	previousJSON, _, err := yamlToLinterJSON(pull.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse previous %s: %w", latest, err)
	}
	return previousJSON, latest, nil
}

// resolveParent walks workspace catalog_dirs to find the parent
// entity named in header.Spec.Extends. Only catalog-dir
// resolution is performed today; OCI parent fetch defers. Returns nil
// silently when extends is empty or the parent isn't locally
// resolvable — the narrowing rule short-circuits on nil parent. The
// not-locally-resolvable note is written to diagOut (os.Stderr in production).
func resolveParent(header lintEntityHeader, wsCfg workspaceconfig.Config, entityPath string, diagOut io.Writer) (json.RawMessage, error) {
	if header.Spec.Extends == "" {
		return nil, nil
	}
	candidates := wsCfg.Workspace.CatalogDirs
	if len(candidates) == 0 {
		// No catalog dirs configured; fall back to the entity file's
		// own directory (development convenience for testing).
		candidates = []string{filepath.Dir(entityPath)}
	}
	// Strip any version suffix from the extends URI; resolution is
	// path-based, not version-pinned at this layer.
	name := header.Spec.Extends
	if idx := strings.LastIndex(name, ":"); idx >= 0 {
		// Only strip if the bit after the colon looks like a version.
		if _, err := semver.NewVersion(name[idx+1:]); err == nil {
			name = name[:idx]
		}
	}
	kindLower := strings.ToLower(header.Kind)
	for _, dir := range candidates {
		candidate := filepath.Join(dir, kindLower, name+".yaml")
		bytes, err := os.ReadFile(candidate)
		if err == nil {
			parentJSON, _, err := yamlToLinterJSON(bytes)
			if err != nil {
				return nil, fmt.Errorf("parse parent %s: %w", candidate, err)
			}
			return parentJSON, nil
		}
	}
	fmt.Fprintf(diagOut, "narrowing rule: parent %q not locally resolvable; rule skipped\n", header.Spec.Extends)
	return nil, nil
}

// buildRuleInputs builds one LinterInput per rule the CLI dispatches
// to. The schema-evolution rule (only in --pre-publish) gets the
// previous bytes; the narrowing rule gets parent; category-matches-
// directory gets source_path. Other rules get the same shared
// proposed bytes + policies.
func buildRuleInputs(
	kind string,
	proposed, previous, parent, policies json.RawMessage,
	entityPath string,
	prePublish bool,
) []validator.LinterInput {
	rules := defaultRuleIDs(prePublish)
	inputs := make([]validator.LinterInput, 0, len(rules))
	for _, r := range rules {
		input := validator.LinterInput{
			RuleID:     r,
			EntityKind: kind,
			Proposed:   proposed,
			Policies:   policies,
		}
		switch r {
		case "schema-evolution":
			input.Previous = previous
		case "narrowing":
			input.Parent = parent
		case "category-matches-directory":
			input.SourcePath = entityPath
		}
		inputs = append(inputs, input)
	}
	return inputs
}

// aggregateFindings flattens per-rule [][]LinterFinding slices into
// a single ordered list, preserving rule-iteration order.
func aggregateFindings(perRule [][]validator.LinterFinding) []validator.LinterFinding {
	var all []validator.LinterFinding
	for _, findings := range perRule {
		all = append(all, findings...)
	}
	return all
}

// downgradeToWarning maps every error finding to a warning. Default
// mode (no --pre-publish) treats every rule's output as informational.
func downgradeToWarning(findings []validator.LinterFinding) []validator.LinterFinding {
	out := make([]validator.LinterFinding, len(findings))
	for i, f := range findings {
		if f.Severity == "error" {
			f.Severity = "warning"
		}
		out[i] = f
	}
	return out
}

// downgradeFirstPublicationToWarning maps the schema-evolution
// rule's classification finding to warning severity for the first-
// publication case. Other rules' findings stay at
// whatever severity they had.
func downgradeFirstPublicationToWarning(findings []validator.LinterFinding) []validator.LinterFinding {
	out := make([]validator.LinterFinding, len(findings))
	for i, f := range findings {
		if f.RuleID == "schema-evolution" && f.Severity == "error" {
			f.Severity = "warning"
		}
		out[i] = f
	}
	return out
}

// printFindings writes each finding to diagOut (os.Stderr in production) in a
// stable format and returns true when at least one finding is severity=error.
func printFindings(findings []validator.LinterFinding, diagOut io.Writer) bool {
	halted := false
	for _, f := range findings {
		if f.Severity == "error" {
			halted = true
		}
		fmt.Fprintf(diagOut, "[%s] %s: %s\n", f.Severity, f.RuleID, f.Message)
	}
	return halted
}

func countErrors(findings []validator.LinterFinding) int {
	n := 0
	for _, f := range findings {
		if f.Severity == "error" {
			n++
		}
	}
	return n
}

// classificationFromFindings extracts the schema-evolution rule's
// classification verdict. Returns "" when no schema-evolution
// finding exists. Multi-finding edge case: HIGHEST classification
// wins (MAJOR > MINOR > PATCH).
func classificationFromFindings(findings []validator.LinterFinding) string {
	rank := map[string]int{"PATCH": 0, "MINOR": 1, "MAJOR": 2}
	highest := ""
	for _, f := range findings {
		if f.RuleID != "schema-evolution" {
			continue
		}
		if f.Classification == "" {
			continue
		}
		if highest == "" || rank[f.Classification] > rank[highest] {
			highest = f.Classification
		}
	}
	return highest
}

// proposedBumpSatisfies returns true when proposed >= required.
// Over-bump is allowed: MAJOR-required can satisfy with MAJOR;
// MINOR-required can satisfy with MINOR or MAJOR; PATCH-required
// can satisfy with any.
func proposedBumpSatisfies(proposed, required string) bool {
	rank := map[string]int{"PATCH": 0, "MINOR": 1, "MAJOR": 2}
	return rank[proposed] >= rank[required]
}

// bumpDirection compares two semver versions and returns PATCH,
// MINOR, or MAJOR. When the versions are equal or the previous is
// unparseable, returns PATCH (best-effort).
func bumpDirection(previous, proposed string) string {
	pv, _ := semver.NewVersion(previous)
	sv, _ := semver.NewVersion(proposed)
	if pv == nil || sv == nil {
		return "PATCH"
	}
	if sv.Major() > pv.Major() {
		return "MAJOR"
	}
	if sv.Minor() > pv.Minor() {
		return "MINOR"
	}
	return "PATCH"
}
