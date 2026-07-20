// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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

// validateCatalogWide runs the whole-catalog correctness pass core and renders
// its structured result: it halts loudly on any schema-pillar violation,
// unresolvable or invalid library, extends-resolution failure, location
// collision, or input-kind capability error, checked in that halt-at-first order.
// Advisory and error diagnostics are written to diagOut (os.Stderr in
// production); the final success summary is written to out (os.Stdout in
// production). The count-summary phrasing is composed here at the render seam;
// the core reports each failing category's full ordered detail.
func validateCatalogWide(wsRoot, cacheDir string, dirs []string, diagOut, out io.Writer) error {
	res, err := validateops.ValidateCatalog(wsRoot, cacheDir, dirs)
	if err != nil {
		return fmt.Errorf("catalog validation failed: %w", err)
	}
	if len(res.ExtendsErrs) > 0 {
		for _, e := range res.ExtendsErrs {
			fmt.Fprintf(diagOut, "  extends: %s\n", e)
		}
		return fmt.Errorf("catalog validation failed: %d extends resolution error(s)", len(res.ExtendsErrs))
	}
	if res.OCICollision != nil {
		return fmt.Errorf("catalog validation failed: %w", res.OCICollision)
	}
	if len(res.InputKindErrs) > 0 {
		for _, e := range res.InputKindErrs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
		}
		return fmt.Errorf("catalog validation failed: %d input-kind capability error(s)", len(res.InputKindErrs))
	}
	for _, w := range res.Notices {
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
	if errs := validateops.InputKindCapabilityErrors(conformanceLookup.ProviderPlugin, clRecipes, clFeatureRecipes); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(diagOut, "  input-kind: %s\n", e)
		}
		return fmt.Errorf("release dependency validation failed: %d input-kind capability error(s)", len(errs))
	}

	fmt.Fprintf(out, "release dependency validation passed: %s (schema-pillar + dependency pillars)\n", releasePath)
	return nil
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
