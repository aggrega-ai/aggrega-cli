// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// validateCatalogWideFromWorkspace resolves the workspace's catalog directories
// and cache, then runs the whole-catalog correctness pass. Catalog directories
// come from the repeatable --catalog flag prepended to the workspace config's
// catalog_dirs; at least one source must be set — a whole-tree enumeration with
// no configured directory is refused rather than silently scanning the working
// directory, which would pull in unrelated trees (a subrepo's testdata fixtures)
// and surface as a misleading duplicate-identity dump. Release-scoped correctness
// resolves a specific closure rather than a whole-tree enumeration, so it keeps a
// working-directory default; the footgun is specific to whole-catalog scanning.
func validateCatalogWideFromWorkspace(rw *workspaceconfig.ResolvedWorkspace, catalogFlagDirs []string) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	if len(catalogFlagDirs) == 0 && len(wsCfg.Workspace.CatalogDirs) == 0 {
		return fmt.Errorf("OPS_ERROR: whole-catalog validation needs catalog directories to scan; pass --catalog or set `workspace.catalog_dirs` in .aggrega-config.yaml (or validate a specific Release with --release)")
	}
	dirs := resolveCatalogDirs(catalogFlagDirs, wsCfg, wsRoot)
	return validateCatalogWide(wsRoot, cacheDir, dirs, os.Stderr, os.Stdout)
}

// validateCatalogWide runs the whole-catalog correctness pass: it loads every
// catalog entity under dirs and applies the schema-pillar (Pass-2) and library
// resolution/rejection checks, resolves the extends chains, rejects OCI-location
// collisions, and reports provider input-kind capability mismatches. This is the
// catalog-wide counterpart to a build's per-Release closure validation — the home
// where a broken catalog entity is meant to surface. It halts loudly on any
// schema-pillar violation, unresolvable or invalid library, extends-resolution
// failure, location collision, or input-kind capability error. Advisory and error
// diagnostics are written to diagOut (os.Stderr in production); the final success
// summary is written to out (os.Stdout in production).
func validateCatalogWide(wsRoot, cacheDir string, dirs []string, diagOut, out io.Writer) error {
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
	if errs := inputKindCapabilityErrors(cat.CatalogLookup().ProviderPlugin, catRecipes, catFeatureRecipes); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
		}
		return fmt.Errorf("catalog validation failed: %d input-kind capability error(s)", len(errs))
	}
	// Surface the whole-catalog advisory notices accumulated during the eager
	// passes above — schema-pillar overlap (Pass-2) and narrowing-on-extend (from
	// ResolveAllExtends). Informational only: advisories never escalate the exit
	// code, which stays 0 here (errors already returned above). Emission order is
	// deterministic — the accumulator walks sorted keys.
	for _, w := range cat.LintWarnings() {
		fmt.Fprintf(diagOut, "  notice: %s\n", w)
	}
	fmt.Fprintln(out, "catalog validation passed (schema-pillar + library + extends + oci-location)")
	return nil
}

// validateRelease runs the release-scoped correctness pass: it computes a
// Release's resolved closure through the SAME build machinery the compiler uses
// (releaseClosure over the lazy lookup — never a local fork, which would re-open
// the build/validate view divergence) and validates over exactly the entities
// that Release reaches: closure-scoped extends resolution, closure-scoped
// schema-pillar validation, and pre-render authored-value conformance via the
// build's shared Step-1 guard suite.
//
// Closure-scoped pillar VALIDATION errors fail the check (non-zero exit);
// schema-pillar OVERLAP advisories are informational `notice:` lines that never
// change the exit code, matching the build's own advisory treatment. No render
// runs — this is validation, not emission.
//
// The lockfile pins the closure when present (the build's pinned view); absent,
// the closure resolves through Tier-1 (local catalog). It never triggers a
// network pull — a registry-pinned closure that needs a lockfile surfaces as an
// honest unresolvable-URI error rather than a silent fetch.
//
// Advisory (`notice:`) and error diagnostics are written to diagOut (os.Stderr in
// production); the final success summary is written to out (os.Stdout in
// production).
func validateRelease(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, diagOut, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := resolveCatalogDirs(catalogFlagDirs, wsCfg, wsRoot)

	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lf, _, lfErr := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if lfErr == nil {
		lazyView.SetPins(catalog.PinsFromLockfile(lf))
	}

	// Shared Release→closure orchestration — the SAME front door the build runs
	// (releaseClosure in build.go), so this and build compute the identical closure
	// by construction. No manifest-implements consistency halt (afterManifest nil);
	// a missing lockfile is tolerated: pass the lockfile only when it loaded, so the
	// registry-pin external-seed gate matches the build's exactly.
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

	// Pillar-validation errors fail the check; resolve extends first so composed
	// (parent+child) schemas validate, matching the build.
	if extErrs := lazyView.ResolveClosureExtends(cl); len(extErrs) > 0 {
		for _, e := range extErrs {
			fmt.Fprintf(diagOut, "  extends: %s\n", e)
		}
		return fmt.Errorf("release dependency validation failed: %d extends resolution error(s)", len(extErrs))
	}
	if err := lazyView.ValidateClosureScopedPillars(cl); err != nil {
		return fmt.Errorf("release dependency validation failed: %w", err)
	}

	// Authored-value conformance: validate the Release's resolved (post-parameter-
	// interpolation) values against the closure's composed schemas, via the build's
	// SHARED Step-1 validation (ResolveAndValidate — the same guard suite the build
	// runs pre-topology, no flatten/render). A real conformance violation is an
	// error (non-zero exit), distinct from the advisories below. The composed
	// EffectiveSchema (extends parent+child) flows in through the lookup, so an
	// inherited-pillar value is validated against the inherited constraint.
	conformanceLookup := lazyView.AsCatalogLookup()
	verr := pipeline.ResolveAndValidate(context.Background(), pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggRes.Bytes,
		AggregatePath:     catalog.RelativeToWorkspace(wsRoot, aggRes.Path),
	}, conformanceLookup, func(*resolver.ResolveResult) error {
		// Validation IS the Step-1 work (annotation + guard suite) that runs inside
		// ResolveAndValidate; nothing further is needed from the resolved view, and
		// the parameter purge is owned by the resolver.
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
	clRecipes, clFeatureRecipes := closureRecipes(lazyView, cl)
	if errs := inputKindCapabilityErrors(conformanceLookup.ProviderPlugin, clRecipes, clFeatureRecipes); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
		}
		return fmt.Errorf("release dependency validation failed: %d input-kind capability error(s)", len(errs))
	}

	fmt.Fprintf(out, "release dependency validation passed: %s (schema-pillar + dependency pillars)\n", releasePath)
	return nil
}

// inputKindCapabilityErrors reports each Recipe / FeatureRecipe that declares a
// workload: block (synthesized mode) bound to a provider whose plugin cannot
// synthesize — a guaranteed render-time halt, surfaced here at validation time. It
// is an early, non-authoritative detector: the render-time central input-kind gate
// remains the single enforcement chokepoint. The detector reads a provider's
// input-kind support from the authoring-time capabilities claim on the
// provider-rendering Plugin entity that serves it, resolved through providerPlugin
// — the same provider→Plugin resolution the render route follows — and reuses the
// gate's own message clause (renderer.UnsupportedInputKindMessage) so the early and
// late diagnostics read identically. A provider whose serving Plugin does not
// resolve in scope, or whose Plugin carries no capabilities claim (ContractFor
// ok=false), is skipped: there is no capability claim to contradict and
// forward-looking catalog scaffolding must not be flagged. The workload predicate
// is the gate's own (!Spec.Workload.IsEmpty()), so presence is classified
// identically.
func inputKindCapabilityErrors(providerPlugin func(ref string) (types.Plugin, bool, error), recipes []types.Recipe, featureRecipes []types.FeatureRecipe) []error {
	var errs []error
	check := func(kind, identity, provider string, workload types.WorkloadTemplate) {
		if workload.IsEmpty() {
			return
		}
		caps, ok := enginefactory.ContractFor(providerPlugin, provider)
		if !ok || caps.Synthesized {
			return
		}
		errs = append(errs, fmt.Errorf("%s %q: %s", kind, identity,
			renderer.UnsupportedInputKindMessage(provider, "synthesised", caps)))
	}
	for _, r := range recipes {
		check("Recipe", r.Metadata.Name+":"+r.Metadata.Version, r.Spec.Target, r.Spec.Workload)
	}
	for _, fr := range featureRecipes {
		check("FeatureRecipe", fr.Metadata.Name+":"+fr.Metadata.Version, fr.Spec.Target, fr.Spec.Workload)
	}
	// Sort by message so the diagnostic order is deterministic regardless of the
	// caller's iteration order (the whole-catalog mode ranges an unordered map).
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

// closureRecipes resolves the Recipe and FeatureRecipe entities a Release's
// closure reaches, from the closure's recorded kind-hints, for the closure-scoped
// input-kind detector. Entities the closure classified as neither (KindHint empty
// or another kind) are not recipe-shaped and are skipped; an entry that fails to
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
// from OCILocation). Such a collision would silently overwrite one entity's
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
	for _, e := range cat.Aggregates {
		add("Aggregate", "", e.Metadata.Name)
	}
	for _, e := range cat.Recipes {
		add("Recipe", e.Spec.Target, e.Metadata.Name)
	}
	for _, e := range cat.FeatureRecipes {
		add("FeatureRecipe", e.Spec.Target, e.Metadata.Name)
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
