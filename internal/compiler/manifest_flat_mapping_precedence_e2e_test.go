// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a flat Manifest mapping wins on
// EXISTENCE rather than on resolvability, identically on both consumption paths.
//
// A Manifest may map one component or feature type twice: once in the flat
// shape and once in the per-provider shape. The flat entry is the author's
// answer for that type, so a flat value naming a Recipe absent from the catalog
// is a fault to report — never a reason to consult the other shape. Two joins
// consume the mapping (the render-time recipe lookup and the URI lookup the
// source-descent walk reads) and they must answer identically, because a
// build's rendered output and the faults the same build reports would otherwise
// describe different recipes.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	aerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const flatPrecedenceFixtureDir = "testdata/fullstack-app"

func flatPrecedenceCatalogPaths() []string {
	return []string{
		flatPrecedenceFixtureDir + "/catalog/base",
		flatPrecedenceFixtureDir + "/catalog/recipes/helm-synthesized-with-artifacts",
	}
}

// loadFlatPrecedenceInputs loads the fixture catalog and one of its Manifests
// through the production loaders, so the joins below are asked about a Manifest
// that came off disk rather than one assembled in Go.
func loadFlatPrecedenceInputs(t *testing.T, manifestFile string) (*catalog.Catalog, *types.Manifest) {
	t.Helper()
	cat, err := catalog.LoadAll("", flatPrecedenceCatalogPaths()...)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	res, err := resolver.ResolveManifest(flatPrecedenceFixtureDir, "./"+manifestFile)
	if err != nil {
		t.Fatalf("load manifest %s: %v", manifestFile, err)
	}
	return cat, res.Manifest
}

// TestFlatMapping_DanglingValueDoesNotReachThePerProviderShape is the
// divergence proof: for a component type carrying BOTH a flat mapping whose
// value names no catalog Recipe and a per-provider mapping whose value names a
// real one, the render-time join must report a miss — the same verdict the URI
// join reports — rather than resolving the per-provider Recipe.
//
// A miss here is not a silent loss: the pre-render resolution-completeness pass
// turns it into the loud fault the sibling test below quotes.
func TestFlatMapping_DanglingValueDoesNotReachThePerProviderShape(t *testing.T) {
	t.Parallel()

	cat, manifest := loadFlatPrecedenceInputs(t, "manifest-dangling-flat-with-per-provider.yaml")

	// Precondition: the fixture really does carry both shapes for this type.
	// Without it a passing assertion below would prove nothing.
	if got := manifest.Spec.Mappings.Legacy.Len(); got == 0 {
		t.Fatal("fixture precondition: the Manifest carries no per-provider mapping, so this test cannot distinguish the two joins")
	}

	const (
		instance = "backend"
		compType = "core/compute/api-server"
	)

	uri, ok, err := catalog.ComponentRecipeURI(manifest, instance, compType)
	if err != nil {
		t.Fatalf("ComponentRecipeURI: %v", err)
	}
	if !ok || !strings.Contains(uri, "absent-from-catalog") {
		t.Fatalf("URI join: want the flat (dangling) value, got uri=%q ok=%v", uri, ok)
	}

	if r, found := cat.RecipeLookupWithManifest(manifest).Recipe(instance, compType); found {
		t.Fatalf("render join resolved %q: a flat mapping wins on existence, so a dangling flat value must not fall back to the per-provider shape", r.Metadata.Name)
	}
}

// TestFlatMapping_DanglingFeatureValueDoesNotReachThePerProviderShape is the
// Feature analogue: the feature join carries the same two-shape cascade and
// therefore owes the same verdict.
func TestFlatMapping_DanglingFeatureValueDoesNotReachThePerProviderShape(t *testing.T) {
	t.Parallel()

	cat, manifest := loadFlatPrecedenceInputs(t, "manifest-dangling-flat-feature-with-per-provider.yaml")

	if got := manifest.Spec.Mappings.Legacy.Len(); got == 0 {
		t.Fatal("fixture precondition: the Manifest carries no per-provider mapping, so this test cannot distinguish the two joins")
	}

	const (
		instance = "cross-namespace-routing"
		featType = "core/k8s-dns-resolver"
	)

	uri, ok, err := catalog.FeatureRecipeURI(manifest, instance, featType)
	if err != nil {
		t.Fatalf("FeatureRecipeURI: %v", err)
	}
	if !ok || !strings.Contains(uri, "absent-from-catalog") {
		t.Fatalf("URI join: want the flat (dangling) value, got uri=%q ok=%v", uri, ok)
	}

	if fr, found := cat.RecipeLookupWithManifest(manifest).FeatureRecipe(instance, featType); found {
		t.Fatalf("feature render join resolved %q: a flat mapping wins on existence, so a dangling flat value must not fall back to the per-provider shape", fr.Metadata.Name)
	}
}

// TestFlatMapping_DanglingValueHaltsTheBuild pins the loud fault a dangling
// flat mapping produces through a real build, and pins that the diagnostic
// names the FLAT value — the one the author wrote — rather than whatever some
// other mapping shape happens to hold.
func TestFlatMapping_DanglingValueHaltsTheBuild(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  flatPrecedenceFixtureDir + "/release-dangling-flat-with-per-provider.yaml",
		CatalogPaths: flatPrecedenceCatalogPaths(),
	})
	if err == nil {
		t.Fatal("expected a build halt for the dangling flat mapping, got nil")
	}
	if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
		t.Fatalf("expected ErrNoRecipeResolved, got: %v", err)
	}
	if chain := aerrors.CauseChain(err); !strings.Contains(chain, "helm-api-server-absent-from-catalog") {
		t.Fatalf("the diagnostic must name the flat mapping value the author wrote; got: %s", chain)
	}
}

// TestFlatMapping_ResolvingValueStillBuilds guards the direction a corpus of
// newly-refused input cannot see: the ordinary Manifest, whose flat mappings
// all resolve, must still build.
func TestFlatMapping_ResolvingValueStillBuilds(t *testing.T) {
	t.Parallel()

	result, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  flatPrecedenceFixtureDir + "/release.yaml",
		CatalogPaths: flatPrecedenceCatalogPaths(),
	})
	if err != nil {
		t.Fatalf("a Manifest whose flat mappings all resolve must still build: %v", err)
	}
	if len(result.Artifacts) == 0 {
		t.Fatal("expected rendered artifacts, got none")
	}
}

// TestPerProviderMapping_StillResolvesWhenNoFlatEntryCovers is the second guard
// in that direction: with no flat entry for the type, nothing outranks the
// per-provider entry, so resolution must still reach it on both joins.
func TestPerProviderMapping_StillResolvesWhenNoFlatEntryCovers(t *testing.T) {
	t.Parallel()

	cat, manifest := loadFlatPrecedenceInputs(t, "manifest-per-provider-only.yaml")

	const (
		instance = "backend"
		compType = "core/compute/api-server"
	)

	uri, ok, err := catalog.ComponentRecipeURI(manifest, instance, compType)
	if err != nil {
		t.Fatalf("ComponentRecipeURI: %v", err)
	}
	if !ok || !strings.Contains(uri, "helm-api-server-synthesized-with-artifacts") {
		t.Fatalf("URI join must reach the per-provider entry when no flat entry covers the type; got uri=%q ok=%v", uri, ok)
	}

	r, found := cat.RecipeLookupWithManifest(manifest).Recipe(instance, compType)
	if !found {
		t.Fatal("render join must reach the per-provider entry when no flat entry covers the type")
	}
	if r.Metadata.Name != "helm-api-server-synthesized-with-artifacts" {
		t.Fatalf("render join resolved %q, want helm-api-server-synthesized-with-artifacts", r.Metadata.Name)
	}

	if _, berr := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  flatPrecedenceFixtureDir + "/release-per-provider-only.yaml",
		CatalogPaths: flatPrecedenceCatalogPaths(),
	}); berr != nil {
		t.Fatalf("a Manifest resolving through the per-provider shape alone must still build: %v", berr)
	}
}
