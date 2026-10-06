// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a Manifest mapping wins on
// EXISTENCE rather than on resolvability, identically on both consumption paths.
//
// A mapping entry is the author's answer for that type, so a value naming a
// Recipe absent from the catalog is a fault to report — never a reason to
// resolve the type some other way. Two joins consume the mapping (the
// render-time recipe lookup and the URI lookup the source-descent walk reads)
// and they must answer identically, because a build's rendered output and the
// faults the same build reports would otherwise describe different recipes.
//
// The same fixture set also pins the other half of the mapping contract:
// `components` and `features` are the whole key surface, and a Manifest
// authoring any other key under `mappings:` is refused at load.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

const mappingPrecedenceFixtureDir = "testdata/fullstack-app"

func mappingPrecedenceCatalogPaths() []string {
	return []string{
		mappingPrecedenceFixtureDir + "/catalog/base",
		mappingPrecedenceFixtureDir + "/catalog/recipes/helm-synthesized-with-artifacts",
	}
}

// loadMappingPrecedenceInputs loads the fixture catalog and one of its Manifests
// through the production loaders, so the joins below are asked about a Manifest
// that came off disk rather than one assembled in Go.
func loadMappingPrecedenceInputs(t *testing.T, manifestFile string) (*catalog.Catalog, *types.Manifest) {
	t.Helper()
	cat, err := catalog.LoadAll("", mappingPrecedenceCatalogPaths()...)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	res, err := resolver.ResolveManifest("", mappingPrecedenceFixtureDir, "", "./"+manifestFile)
	if err != nil {
		t.Fatalf("load manifest %s: %v", manifestFile, err)
	}
	return cat, res.Manifest
}

// TestMapping_DanglingComponentValueIsAMissOnBothJoins pins the agreement
// between the two joins: for a component type whose mapping value names no
// catalog Recipe, the URI join returns that value verbatim (so the fault names
// what the author wrote) and the render-time join reports a MISS rather than
// resolving the type by any other route.
//
// The catalog here really does hold a Recipe whose spec.implements matches the
// type, which is what makes the miss meaningful: resolution is mapping-only.
//
// A miss here is not a silent loss. The tests in this file load the catalog
// eagerly and run no dependency-closure walk, so the pre-render
// resolution-completeness pass is what turns the miss into the loud fault the
// sibling test below quotes. A surface that resolves the Release's dependency
// closure first stops earlier, in that resolution, on the URI no catalog entity
// matches — so the fault a surface reports for this fixture is the one its own
// entry point reaches first.
func TestMapping_DanglingComponentValueIsAMissOnBothJoins(t *testing.T) {
	t.Parallel()

	cat, manifest := loadMappingPrecedenceInputs(t, "manifest-dangling-component-mapping.yaml")

	const (
		instance = "backend"
		compType = "core/compute/api-server"
	)

	// Precondition: the catalog must hold a Recipe implementing this type, or a
	// passing miss below would prove nothing about mapping-only resolution.
	if _, found := cat.RecipeByURI("helm-api-server-synthesized-with-artifacts:1.0.0"); !found {
		t.Fatal("fixture precondition: the catalog holds no Recipe for this type, so a miss cannot distinguish mapping-only resolution from an empty catalog")
	}

	uri, ok, err := catalog.ComponentRecipeURI(manifest, instance, compType)
	if err != nil {
		t.Fatalf("ComponentRecipeURI: %v", err)
	}
	if !ok || !strings.Contains(uri, "absent-from-catalog") {
		t.Fatalf("URI join: want the dangling mapping value verbatim, got uri=%q ok=%v", uri, ok)
	}

	if r, found := cat.RecipeLookupWithManifest(manifest).Recipe(instance, compType); found {
		t.Fatalf("render join resolved %q: a mapping wins on existence, so an unresolvable value must be a miss", r.Metadata.Name)
	}
}

// TestMapping_DanglingFeatureValueIsAMissOnBothJoins is the Feature analogue:
// the feature join carries the same contract and therefore owes the same
// verdict.
func TestMapping_DanglingFeatureValueIsAMissOnBothJoins(t *testing.T) {
	t.Parallel()

	cat, manifest := loadMappingPrecedenceInputs(t, "manifest-dangling-feature-mapping.yaml")

	const (
		instance = "cross-namespace-routing"
		featType = "core/k8s-dns-resolver"
	)

	if _, found := cat.FeatureRecipeByURI("k8s-dns-patcher:1.0.0"); !found {
		t.Fatal("fixture precondition: the catalog holds no FeatureRecipe for this type, so a miss cannot distinguish mapping-only resolution from an empty catalog")
	}

	uri, ok, err := catalog.FeatureRecipeURI(manifest, instance, featType)
	if err != nil {
		t.Fatalf("FeatureRecipeURI: %v", err)
	}
	if !ok || !strings.Contains(uri, "absent-from-catalog") {
		t.Fatalf("URI join: want the dangling mapping value verbatim, got uri=%q ok=%v", uri, ok)
	}

	if fr, found := cat.RecipeLookupWithManifest(manifest).FeatureRecipe(instance, featType); found {
		t.Fatalf("feature render join resolved %q: a mapping wins on existence, so an unresolvable value must be a miss", fr.Metadata.Name)
	}
}

// TestMapping_DanglingValueHaltsTheBuild pins the loud fault a dangling mapping
// produces through a real build, and pins that the diagnostic names the mapping
// value the author wrote.
func TestMapping_DanglingValueHaltsTheBuild(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  mappingPrecedenceFixtureDir + "/release-dangling-component-mapping.yaml",
		CatalogPaths: mappingPrecedenceCatalogPaths(),
	})
	if err == nil {
		t.Fatal("expected a build halt for the dangling mapping, got nil")
	}
	if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
		t.Fatalf("expected ErrNoRecipeResolved, got: %v", err)
	}
	if chain := aerrors.CauseChain(err); !strings.Contains(chain, "helm-api-server-absent-from-catalog") {
		t.Fatalf("the diagnostic must name the mapping value the author wrote; got: %s", chain)
	}
}

// TestMapping_ResolvingValueStillBuilds guards the direction a corpus of
// newly-refused input cannot see: the ordinary Manifest, whose mappings all
// resolve, must still build.
func TestMapping_ResolvingValueStillBuilds(t *testing.T) {
	t.Parallel()

	result, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  mappingPrecedenceFixtureDir + "/release.yaml",
		CatalogPaths: mappingPrecedenceCatalogPaths(),
	})
	if err != nil {
		t.Fatalf("a Manifest whose mappings all resolve must still build: %v", err)
	}
	if len(result.Artifacts) == 0 {
		t.Fatal("expected rendered artifacts, got none")
	}
}

// TestMapping_UnknownKeyIsRefusedAtLoad pins the key surface end-to-end, from a
// fixture on disk through the production loader: `components` and `features`
// are the whole mapping surface, and any other key under `mappings:` halts.
//
// It asserts three things rather than just the refusal. The error carries the
// envelope sentinel, because that sentinel is what the loader gates on — an
// error that failed to carry it would be treated as success at the call site
// and the refusal would not happen. The diagnostic names the key the author
// wrote, so the reader knows which line to open. And it names the accepted
// keys, so the reader is told what to write instead: a refusal that only says
// "no" leaves the author guessing.
func TestMapping_UnknownKeyIsRefusedAtLoad(t *testing.T) {
	t.Parallel()

	_, err := resolver.ResolveManifest("", mappingPrecedenceFixtureDir, "", "./manifest-unknown-mapping-key.yaml")
	if err == nil {
		t.Fatal("expected the Manifest carrying an unknown mappings key to be refused")
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Fatalf("the refusal must carry the sentinel the loader gates on; got %v", err)
	}
	got := err.Error()
	for _, want := range []string{`"helm"`, "components", "features"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not name %s; got %q", want, got)
		}
	}
}
