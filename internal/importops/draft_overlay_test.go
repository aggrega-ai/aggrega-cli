// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// The view an import's composition and owed-value reading consult: the catalog,
// then the Components and Recipes the importer drafted, read by the catalog's own
// readings. Each case reads the view overlayDrafts returns over the
// draft-composition catalog as the import's real seam loads it.

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// overlayBase loads the draft-composition catalog the way an import's seam loads
// a catalog folder.
func overlayBase(t *testing.T) *catalog.Catalog {
	t.Helper()
	dir := importopstest.DraftComposerCatalogDir(t)
	cat, err := catalog.LoadAll(filepath.Dir(dir), dir)
	if err != nil {
		t.Fatalf("load the draft-composition catalog: %v", err)
	}
	return cat
}

// drafted is a drafted file at path holding body after the API version every
// drafted entity opens with.
func drafted(path, body string) abi.ScaffoldFile {
	return abi.ScaffoldFile{Path: path, Content: "apiVersion: " + apiversion.APIVersion + "\n" + body}
}

// overlayLookup is the catalog lookup the view answers, failing the test where
// it answers none.
func overlayLookup(t *testing.T, view RecipeIndex) resolver.CatalogLookup {
	t.Helper()
	cat, ok := view.(owedCatalog)
	if !ok {
		t.Fatalf("the view over a catalog answering a lookup answers none: %T", view)
	}
	return cat.CatalogLookup()
}

// infraKeys are the keys the compiled schema of typ declares under infra, sorted.
func infraKeys(t *testing.T, lookup resolver.CatalogLookup, typ string) []string {
	t.Helper()
	compiled, ok := resolver.CompileComponentSchema(lookup, typ)
	if !ok {
		t.Fatalf("the view compiles no schema for %s", typ)
	}
	infra, _ := compiled["properties"].(map[string]any)["infra"].(map[string]any)
	props, _ := infra["properties"].(map[string]any)
	var keys []string
	for key := range props {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// recipeIdentities renders Recipes as the catalog identifies them.
func recipeIdentities(recipes []types.Recipe) []string {
	out := make([]string, 0, len(recipes))
	for _, r := range recipes {
		out = append(out, catalog.RecipeIdentityURI(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version))
	}
	return out
}

// TestCatalogEntityWinsOverADraftOfItsIdentity drafts a Component and a Recipe
// of identities the catalog already holds — example/x at 1.0.0, declaring an
// infra key of its own, and compose/x-server at 1.0.0, running another image —
// and holds every reading of the view to the catalog's entities: the Component's
// schema, the Recipe a reference resolves to, the Recipes implementing the type,
// and no report.
//
// 🔴 PLANTED DEFECTS (internal/importops/draft_overlay.go):
//   - drop the identity check (have baseHoldsComponent and baseHoldsRecipe answer
//     false): the implementing row reddens on a doubled identity.
//   - drop it and consult the drafts before base in draftOverlay.resolve and
//     draftOverlay.RecipeByURI as well: the schema row reddens naming the drafted
//     key, and the reference row on the drafted image.
func TestCatalogEntityWinsOverADraftOfItsIdentity(t *testing.T) {
	t.Parallel()
	base := overlayBase(t)
	view, diags := overlayDrafts(base, []abi.ScaffoldFile{
		drafted("catalog/components/x.yaml", `kind: Component
metadata:
  name: example/x
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      drafted: { type: string, description: "Declared by the draft alone." }
`),
		drafted("catalog/recipes/x-server.yaml", `kind: Recipe
metadata:
  name: x-server
  version: "1.0.0"
spec:
  capability: compose
  implements: example/x
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: drafted-x-server:1.0.0
`),
	})
	if len(diags) != 0 {
		t.Errorf("drafts of identities the catalog holds drew %+v; want no report", diags)
	}
	if got := infraKeys(t, overlayLookup(t, view), "example/x:1.0.0"); !slices.Equal(got, []string{"port"}) {
		t.Errorf("the view's example/x declares the infra keys %v; want the catalog's own [port]", got)
	}
	recipe, ok := view.RecipeByURI("compose/x-server:1.0.0")
	want, _ := base.RecipeByURI("compose/x-server:1.0.0")
	if !ok || recipe.Spec.Workload.Inline != want.Spec.Workload.Inline {
		t.Errorf("compose/x-server:1.0.0 resolves (%v) to a workload of\n%s\nwant the catalog's\n%s", ok,
			recipe.Spec.Workload.Inline, want.Spec.Workload.Inline)
	}
	got := recipeIdentities(view.RecipesImplementing("example/x:1.0.0"))
	if want := recipeIdentities(base.RecipesImplementing("example/x:1.0.0")); !slices.Equal(got, want) {
		t.Errorf("the view's Recipes implementing example/x:1.0.0 are %v; want the catalog's %v", got, want)
	}
}

// TestCatalogEntityWinsOverADraftAtAnotherVersion drafts example/x at 0.5.0,
// which the catalog holds at 1.0.0 alone, and the compose Recipe x-old
// implementing that drafted version. The type asked with no version is asked at
// the catalog's version, so the view answers the catalog's Recipes and not
// x-old; asked at the drafted version, it answers x-old after the catalog's.
//
// 🔴 PLANTED DEFECT: ask both with the type as written in
// draftOverlay.RecipesImplementing (internal/importops/draft_overlay.go): the
// drafts then resolve the bare type to their own version, and the first row
// reddens naming compose/x-old:0.5.0.
func TestCatalogEntityWinsOverADraftAtAnotherVersion(t *testing.T) {
	t.Parallel()
	base := overlayBase(t)
	view, diags := overlayDrafts(base, []abi.ScaffoldFile{
		drafted("catalog/components/x-old.yaml", `kind: Component
metadata:
  name: example/x
  version: 0.5.0
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer }
`),
		drafted("catalog/recipes/x-old.yaml", `kind: Recipe
metadata:
  name: x-old
  version: 0.5.0
spec:
  capability: compose
  implements: example/x:0.5.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: x-old:0.5.0
`),
	})
	if len(diags) != 0 {
		t.Errorf("drafts of another version drew %+v; want no report", diags)
	}
	got := recipeIdentities(view.RecipesImplementing("example/x"))
	if want := recipeIdentities(base.RecipesImplementing("example/x")); !slices.Equal(got, want) {
		t.Errorf("the view's Recipes implementing example/x are %v; want the catalog's %v", got, want)
	}
	got = recipeIdentities(view.RecipesImplementing("example/x:0.5.0"))
	want := append(recipeIdentities(base.RecipesImplementing("example/x:0.5.0")), "compose/x-old:0.5.0")
	if !slices.Equal(got, want) {
		t.Errorf("the view's Recipes implementing example/x:0.5.0 are %v; want %v", got, want)
	}
}

// TestDraftedComponentAncestry drafts ws/storage/fast extending ws/storage/x,
// itself drafted, which extends the catalog's example/x, and holds the ancestry
// of the drafted type, asked at its version and with none, to its own chain into
// the catalog, nearest base first, each written as a mapping key names it; the
// catalog type's own ancestry is the catalog's.
//
// 🔴 PLANTED DEFECT: answer the ancestry of every type from the catalog alone in
// draftOverlay.ComponentAncestry: the drafted rows redden with no ancestry.
func TestDraftedComponentAncestry(t *testing.T) {
	t.Parallel()
	view, diags := overlayDrafts(overlayBase(t), []abi.ScaffoldFile{
		drafted("catalog/components/fast.yaml", `kind: Component
metadata:
  name: ws/storage/fast
  version: 0.1.0
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: ws/storage/x
  schema: inherited
`),
		drafted("catalog/components/x.yaml", `kind: Component
metadata:
  name: ws/storage/x
  version: 0.1.0
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: example/x:1.0.0
  schema: inherited
`),
	})
	if len(diags) != 0 {
		t.Errorf("drafted Components drew %+v; want no report", diags)
	}
	want := []string{"ws/storage/x:0.1.0", "example/x:1.0.0"}
	for _, typ := range []string{"ws/storage/fast:0.1.0", "ws/storage/fast"} {
		got, err := view.ComponentAncestry(typ)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("the ancestry of %s is %v (%v); want %v", typ, got, err, want)
		}
	}
	if got, err := view.ComponentAncestry("example/x:1.0.0"); err != nil || len(got) != 0 {
		t.Errorf("the catalog's example/x has the ancestry %v (%v); want none", got, err)
	}
}

// TestDraftedRecipeMapsADraftedTypeNoManifestMaps composes, for the arm target, a
// draft set whose importer drafted no Manifest: a Component ws/storage/x, the
// Recipe x of arm implementing it at its version, and an Aggregate instance of
// the type naming no version. The composition maps the type, as the drafts write
// it, to arm/x through the view, and reports nothing.
//
// 🔴 PLANTED DEFECT: answer the Recipes implementing a type from base alone in
// draftOverlay.RecipesImplementing (internal/importops/draft_overlay.go): the type
// is left unmapped and both rows redden.
func TestDraftedRecipeMapsADraftedTypeNoManifestMaps(t *testing.T) {
	t.Parallel()
	files := []abi.ScaffoldFile{
		drafted("catalog/components/x.yaml", `kind: Component
metadata:
  name: ws/storage/x
  version: 0.1.0
  labels:
    component.aggrega.ai/type: "resource"
spec:
  schema:
    infra:
      sku: { type: string }
`),
		drafted("catalog/recipes/arm/x.yaml", `kind: Recipe
metadata:
  name: x
  version: 0.1.0
spec:
  capability: arm
  implements: ws/storage/x:0.1.0
  artifacts:
    template.yaml: |
      resources: {}
`),
		drafted("aggregate.yaml", `kind: Aggregate
metadata:
  name: drafted
  version: 0.1.0
spec:
  components:
    store:
      type: ws/storage/x
`),
	}
	view, overlaid := overlayDrafts(overlayBase(t), files)
	out, diags, err := composeDraftSet("arm", view, files)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if all := append(overlaid, diags...); len(all) != 1 || all[0].Severity != abi.SeverityInfo {
		t.Errorf("the composition reported %+v; want only that no Release references the Manifest", all)
	}
	want := "apiVersion: " + apiversion.APIVersion + "\n" + `kind: Manifest
metadata:
  name: drafted
  version: 0.1.0
spec:
  mappings:
    components:
      type:
        ws/storage/x: arm/x
`
	if got := out[len(out)-1]; got.Path != ManifestFileName || got.Content != want {
		t.Errorf("the composition drafted %s as\n%s\nwant %s as\n%s", got.Path, got.Content, ManifestFileName, want)
	}
}
