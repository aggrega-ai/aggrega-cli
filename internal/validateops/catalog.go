// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/enginefactory"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// CatalogResult is the structured outcome of whole-catalog validation.
//
// Exactly one failing category is populated on failure, in the halt-at-first
// order the pass applies: ExtendsErrs, then OCICollision, then InputKindErrs.
// Each carries the full ordered diagnostic detail for its category so a caller
// renders it faithfully; the count-summary phrasing is the caller's to compose.
// On a clean pass, Valid is true and Notices carries the advisory lines
// (schema-pillar overlap, narrowing-on-extend) in deterministic order. A load
// failure is returned as the function's error, not in this result.
type CatalogResult struct {
	Valid         bool
	ExtendsErrs   []error
	OCICollision  error
	InputKindErrs []error
	Notices       []string
}

// ValidateCatalog runs the whole-catalog correctness pass over the catalog under
// dirs: it loads every entity and applies the schema-pillar (Pass-2) and library
// resolution/rejection checks, resolves the extends chains, rejects OCI-location
// collisions, and reports provider input-kind capability mismatches. This is the
// catalog-wide counterpart to a build's per-Release closure validation — the home
// where a broken catalog entity is meant to surface.
//
// It halts at the first failing category and reports that category's full detail
// in the result; the categories are checked in order (extends, oci-location,
// input-kind). A catalog that cannot load at all is returned as an error. The
// caller supplies the already-resolved workspace root, cache directory, catalog
// search directories, and the part of them somebody named.
//
// Before anything is loaded, every document under dirs is read and an Aggrega
// document that cannot be loaded halts the pass, naming each such file — see
// unloadableCatalogDocuments. The load on its own passes over a file whose YAML
// does not parse, as a build's load does, so without that walk the pass would
// report a clean catalog over a document it never read.
func ValidateCatalog(wsRoot, cacheDir string, dirs, named []string) (CatalogResult, error) {
	if err := unloadableCatalogDocuments(wsRoot, cacheDir, dirs, named); err != nil {
		return CatalogResult{}, err
	}
	cat, err := catalog.LoadAllWithNamedDirs(wsRoot, cacheDir, named, dirs...)
	if err != nil {
		return CatalogResult{}, err
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		errs := make([]error, len(extErrs))
		for i, e := range extErrs {
			errs[i] = e
		}
		return CatalogResult{ExtendsErrs: errs}, nil
	}
	if err := VerifyNoOCILocationCollision(cat); err != nil {
		return CatalogResult{OCICollision: err}, nil
	}
	catRecipes := make([]types.Recipe, 0, len(cat.Recipes))
	for _, r := range cat.Recipes {
		catRecipes = append(catRecipes, r)
	}
	catFeatureRecipes := make([]types.FeatureRecipe, 0, len(cat.FeatureRecipes))
	for _, fr := range cat.FeatureRecipes {
		catFeatureRecipes = append(catFeatureRecipes, fr)
	}
	if errs := InputKindCapabilityErrors(cat.CatalogLookup().ProviderPlugin, catRecipes, catFeatureRecipes); len(errs) > 0 {
		return CatalogResult{InputKindErrs: errs}, nil
	}
	// Surface the whole-catalog advisory notices accumulated during the eager
	// passes above — schema-pillar overlap (Pass-2) and narrowing-on-extend (from
	// ResolveAllExtends). Informational only: advisories never escalate the exit
	// code. Emission order is deterministic — the accumulator walks sorted keys.
	return CatalogResult{Valid: true, Notices: cat.LintWarnings()}, nil
}

// unloadableCatalogDocuments walks dirs as the catalog's own walk reads them,
// reads every document beneath them once, and returns the halt naming each
// Aggrega document among them that cannot be loaded, or nil when there is none.
// Which documents those are is [catalog.UnloadableDocument]'s answer, the same
// one whole-catalog lint reads at its walk. The walk completes before it
// halts, so every such file is named in one run rather than the first alone. A
// file reached twice — catalog directories may overlap — is read and reported
// once.
//
// A walk that stops early — on a link leading out of a directory's root — has
// not read every document, so it raises nothing of its own and leaves the
// refusal to the load that follows, which walks the same directories, stops on
// the same link and reports it in the words a whole-catalog load always has.
func unloadableCatalogDocuments(wsRoot, cacheDir string, dirs, named []string) error {
	var found []error
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		err := catalog.WalkDocuments(wsRoot, dir, cacheDir, named, func(path string) error {
			abs, aerr := filepath.Abs(path)
			if aerr != nil {
				abs = path
			}
			if _, dup := seen[abs]; dup {
				return nil
			}
			seen[abs] = struct{}{}
			data, rerr := os.ReadFile(path)
			if f := catalog.UnloadableDocument(path, data, rerr, nil); f != nil {
				found = append(found, f)
			}
			return nil
		})
		if err != nil {
			return nil
		}
	}
	return catalog.UnloadableDocumentsHalt(found)
}

// InputKindCapabilityErrors reports each Recipe / FeatureRecipe that declares a
// workload: block (synthesized mode) bound to a provider whose plugin cannot
// synthesize — a guaranteed render-time halt, surfaced here at validation time.
//
// The workload is checked whatever else the same document declares. A Recipe
// pairing a workload with a source or with artifacts still cannot have that
// workload rendered, so another, honourable input kind alongside it does not
// excuse the one the provider cannot honour.
//
// It is an early, non-authoritative detector, and non-authoritative carries two
// consequences that decide what a clean result from it is worth.
//
// A build does not run it. It is reached from the validate path, the editor tier,
// and the assistant-facing catalog and release scopes; no build command reaches
// it. So a clean result here is not a prediction that a build will pass — and it
// does not need to be, because a build refuses this same fault on its own at the
// render-time central input-kind gate, which is the single enforcement chokepoint
// and is reached identically from the Recipe and FeatureRecipe render paths.
//
// And it is silently skipped for any provider it cannot read a capability claim
// for: one whose serving provider Plugin does not resolve in scope, whose
// resolution errors, or whose resolved Plugin carries no contract: block
// (ContractFor ok=false). Skipping is correct in each of those cases — there is no
// capability claim to contradict, and forward-looking catalog scaffolding must not
// be flagged — but a skip and a pass are indistinguishable in the output, so over a
// catalog whose provider Plugin entities declare no contract this reports nothing
// at all, and nothing in the result says so.
//
// The detector reads a provider's input-kind support from the authoring-time
// capabilities claim on the provider-rendering Plugin entity that serves it,
// resolved through providerPlugin — the same provider→Plugin resolution the render
// route follows — and reuses the gate's own message clause
// (renderer.UnsupportedInputKindMessage) so the early and late diagnostics read
// identically. The workload predicate is the gate's own
// (!Spec.Workload.IsEmpty()), so presence is classified identically.
func InputKindCapabilityErrors(providerPlugin func(ref string) (types.Plugin, bool, error), recipes []types.Recipe, featureRecipes []types.FeatureRecipe) []error {
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
			renderer.UnsupportedInputKindMessage(provider, "synthesized", caps)))
	}
	for _, r := range recipes {
		check("Recipe", r.Metadata.Name+":"+r.Metadata.Version, r.Spec.Capability, r.Spec.Workload)
	}
	for _, fr := range featureRecipes {
		check("FeatureRecipe", fr.Metadata.Name+":"+fr.Metadata.Version, fr.Spec.Capability, fr.Spec.Workload)
	}
	// Sort by message so the diagnostic order is deterministic regardless of the
	// caller's iteration order (the whole-catalog mode ranges an unordered map).
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

// VerifyNoOCILocationCollision halts when two DISTINCT catalog identities map to
// the same OCI repository location (the lowercased, identity-derived coordinate
// from OCILocation). Such a collision would silently overwrite one entity's
// published bytes with another's at population time — the loud counterpart to the
// publish-path location encoding. Grouping is by version-independent identity
// (provider/name for recipes, metadata.name otherwise), so multiple versions of
// one identity sharing a location — distinguished by tag — are not a collision.
func VerifyNoOCILocationCollision(cat *catalog.Catalog) error {
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
		add("Recipe", e.Spec.Capability, e.Metadata.Name)
	}
	for _, e := range cat.FeatureRecipes {
		add("FeatureRecipe", e.Spec.Capability, e.Metadata.Name)
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
	return fmt.Errorf("%w:\n  - %s", oci.ErrOCILocationCollision(), strings.Join(collisions, "\n  - "))
}
