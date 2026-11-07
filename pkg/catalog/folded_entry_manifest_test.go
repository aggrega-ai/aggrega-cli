// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestAFoldedEntryReportsTheManifestItsReadHeld holds a lazy view pinned to its
// lockfile — the construction a build resolves through — to describing every
// entry its fold placed from a cache slot with the digest of the manifest the
// fold's verifying read held the entry's bytes to, as every other read of the
// content cache describes what it served. It is the manifest a recorded
// signature on the entry's row is held to having been made for, so an entry
// reported without one would leave that row's signature held to the re-hash alone.
//
// Every kind the fold places is a subject. One pin records no manifest digest of
// its own, and the entry is still reported under the manifest the read held: the
// digest is derived from the bytes on disk, never carried in from the pin. An
// entry authored in a catalog directory is the control, reported with none.
func TestAFoldedEntryReportsTheManifestItsReadHeld(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	dir := filepath.Join(ws, "catalog")
	writeProvenanceFile(t, filepath.Join(dir, "recipe", "compose", "authored-db", "recipe.yaml"), []byte(provenanceAuthoredRecipeYAML))

	const (
		pluginURI    = "pipeline-step/pulled-step:1.0.0"
		recipeURI    = "compose/mariadb:1.0.0"
		frURI        = "compose/dotenv-secret:1.0.0"
		aggregateURI = "example/app:1.0.0"
		schemaURI    = "DbSizing:1.0.0"
		mappingURI   = "BaseMappings:1.0.0"
		workloadURI  = "BaseWorkloads:1.0.0"
		artifactsURI = "PinnedArtifacts:1.0.0"
	)
	pins := Pins{
		{Kind: "Plugin", URI: pluginURI}:             writePinnedSlot(t, ws, cacheDir, pluginURI, oci.MediaTypePlugin, []byte(provenancePulledPluginYAML)),
		{Kind: "Recipe", URI: recipeURI}:             writePinnedSlot(t, ws, cacheDir, recipeURI, oci.MediaTypeRecipe, recipeBundle(t, bridgeRecipeYAML)),
		{Kind: "FeatureRecipe", URI: frURI}:          writePinnedSlot(t, ws, cacheDir, frURI, oci.MediaTypeFeatureRecipe, recipeBundle(t, bridgeFeatureRecipeYAML)),
		{Kind: "Aggregate", URI: aggregateURI}:       writePinnedSlot(t, ws, cacheDir, aggregateURI, oci.MediaTypeAggregate, []byte(pinnedAggregateYAML)),
		{Kind: "SchemaLibrary", URI: schemaURI}:      writePinnedSlot(t, ws, cacheDir, schemaURI, oci.MediaTypeSchemaLibrary, []byte(dbSizingLibYAML)),
		{Kind: "MappingLibrary", URI: mappingURI}:    writePinnedSlot(t, ws, cacheDir, mappingURI, oci.MediaTypeMappingLibrary, []byte(pinnedMappingLibYAML)),
		{Kind: "WorkloadLibrary", URI: workloadURI}:  writePinnedSlot(t, ws, cacheDir, workloadURI, oci.MediaTypeWorkloadLibrary, []byte(pinnedWorkloadLibYAML)),
		{Kind: "ArtifactLibrary", URI: artifactsURI}: writePinnedSlot(t, ws, cacheDir, artifactsURI, oci.MediaTypeArtifactLibrary, []byte(provenancePinnedArtifactLibYAML)),
	}
	// The slot's own manifest digest, kept before one pin is made to record none.
	held := map[string]string{}
	for uri, pin := range pins {
		held[uri.URI] = pin.ManifestDigest
	}
	unrecorded := pins[PinKey{Kind: "Recipe", URI: recipeURI}]
	unrecorded.ManifestDigest = ""
	pins[PinKey{Kind: "Recipe", URI: recipeURI}] = unrecorded

	entity := func(uri, kindHint string) func(context.Context, closure.Lookup) (closure.Resolution, error) {
		return func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error) {
			return lookup.ResolveCatalogEntity(ctx, uri, kindHint)
		}
	}
	aggregate := func(uri string) func(context.Context, closure.Lookup) (closure.Resolution, error) {
		return func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error) {
			return lookup.ResolveAggregate(ctx, uri)
		}
	}
	library := func(axis, uri string) func(context.Context, closure.Lookup) (closure.Resolution, error) {
		return func(ctx context.Context, lookup closure.Lookup) (closure.Resolution, error) {
			res, err := lookup.ResolveLibrary(ctx, closure.LibraryAxisNamed(axis), uri)
			return res.Resolution, err
		}
	}
	subjects := []struct {
		name    string
		want    string
		resolve func(context.Context, closure.Lookup) (closure.Resolution, error)
	}{
		{"a folded Plugin entity", held[pluginURI], entity(pluginURI, closure.KindPlugin)},
		{"a folded Recipe whose pin records no manifest digest", held[recipeURI], entity(recipeURI, "recipe")},
		{"a folded FeatureRecipe", held[frURI], entity(frURI, "feature_recipe")},
		{"a folded Aggregate", held[aggregateURI], aggregate(aggregateURI)},
		{"a folded schema library", held[schemaURI], library("SchemaLibraries", schemaURI)},
		{"a folded mapping library", held[mappingURI], library("MappingLibraries", mappingURI)},
		{"a folded workload library", held[workloadURI], library("WorkloadLibraries", workloadURI)},
		{"a folded artifacts library", held[artifactsURI], library("ArtifactLibraries", artifactsURI)},
		{"control: a Recipe authored in a catalog directory", "", entity("compose/authored-db:1.0.0", "recipe")},
	}
	for _, s := range subjects {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			view := NewLazyLookup(ws, cacheDir, []string{dir}, nil)
			view.SetPins(pins)
			res, err := s.resolve(context.Background(), view.ClosureLookup(nil, ws, cacheDir, nil, nil, nil))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if res.NotFound {
				t.Fatal("the subject did not resolve, so what it reports cannot be compared")
			}
			if s.want == "" {
				if res.ManifestDigest != "" {
					t.Errorf("an entry authored in a catalog directory was reported under manifest %q", res.ManifestDigest)
				}
				return
			}
			if res.ResolutionPath != closure.ResolutionPathCache {
				t.Fatalf("ResolutionPath = %q; the subject was not served from its cache slot", res.ResolutionPath)
			}
			if res.ManifestDigest != s.want {
				t.Errorf("ManifestDigest = %q; want %q, the manifest the fold's read held the entry to", res.ManifestDigest, s.want)
			}
		})
	}
}
