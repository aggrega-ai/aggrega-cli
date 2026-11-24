// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// authoredViewFolder lays down one catalog folder holding two Components, comp/a
// and comp/b, the Recipe a of compose implementing comp/a and the Recipe b of
// helm implementing comp/b, each in a document of its own, and the Feature
// feat/f, and returns the folder.
func authoredViewFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	docs := map[string]string{
		"components/a.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: comp/a
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer }
`,
		"components/b.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: comp/b
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer }
`,
		"recipes/a.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: a
  version: "1.0.0"
spec:
  capability: compose
  implements: comp/a
  artifacts:
    a.yaml: |
      services: {}
`,
		"recipes/b.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: b
  version: "1.0.0"
spec:
  capability: helm
  implements: comp/b
  artifacts:
    b.yaml: |
      resources: {}
`,
		"features/f.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: feat/f
  version: "1.0.0"
spec:
  schema:
    values:
      level: { type: string }
`,
	}
	for rel, body := range docs {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, body)
	}
	return dir
}

// implementingKeys are the identities Recipes are indexed under, in their order.
func implementingKeys(recipes []types.Recipe) []string {
	out := make([]string, 0, len(recipes))
	for _, r := range recipes {
		out = append(out, recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version))
	}
	return out
}

// sameIndex reports whether two indexes of one kind are one map, shared.
func sameIndex(a, b any) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// TestAuthoredViewLeavesOutOnlyTheNamedDocuments names the documents of comp/a
// and of the Recipe a: the view's Component and Recipe indexes, and every Recipe
// reading over them, leave those two out and hold the others; every other index
// is the catalog's own; the catalog itself is unchanged. A document the catalog
// holds no entity of leaves the view holding everything.
//
// 🔴 PLANTED DEFECT: have WithoutEntitiesAuthoredIn (pkg/catalog/catalog.go) hand
// the view c's Recipe index: the Recipe a stays in view, and the Recipes, the
// Recipes implementing comp/a, the compose/a and the capabilities rows redden.
func TestAuthoredViewLeavesOutOnlyTheNamedDocuments(t *testing.T) {
	t.Parallel()
	dir := authoredViewFolder(t)
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load the catalog folder: %v", err)
	}
	named := []string{filepath.Join(dir, "components", "a.yaml"), filepath.Join(dir, "recipes", "a.yaml")}

	view := c.WithoutEntitiesAuthoredIn(func(document string) bool { return slices.Contains(named, document) })

	if got, want := sortedMapKeys(view.Components), []string{"comp/b:1.0.0"}; !slices.Equal(got, want) {
		t.Errorf("the view's Components are %v; want %v", got, want)
	}
	if got, want := sortedMapKeys(view.Recipes), []string{"helm/b:1.0.0"}; !slices.Equal(got, want) {
		t.Errorf("the view's Recipes are %v; want %v", got, want)
	}
	if got := implementingKeys(view.RecipesImplementing("comp/a")); len(got) != 0 {
		t.Errorf("the view's Recipes implementing comp/a are %v; want none", got)
	}
	if got, want := implementingKeys(view.RecipesImplementing("comp/b")), []string{"helm/b:1.0.0"}; !slices.Equal(got, want) {
		t.Errorf("the view's Recipes implementing comp/b are %v; want %v", got, want)
	}
	if r, ok := view.RecipeByURI("compose/a"); ok {
		t.Errorf("the view resolves compose/a to %s; want no Recipe", r.Metadata.Name)
	}
	if _, ok := view.RecipeByURI("helm/b"); !ok {
		t.Errorf("the view resolves no helm/b; want the Recipe b")
	}
	if got, want := view.RecipeCapabilities(), []string{"helm"}; !slices.Equal(got, want) {
		t.Errorf("the view's Recipes bind to %v; want %v", got, want)
	}

	if !sameIndex(view.Features, c.Features) || !sameIndex(view.FeatureRecipes, c.FeatureRecipes) ||
		!sameIndex(view.Aggregates, c.Aggregates) || !sameIndex(view.Plugins, c.Plugins) ||
		!sameIndex(view.SchemaLibraryVersions, c.SchemaLibraryVersions) {
		t.Errorf("the view does not share the catalog's every other index")
	}
	if got, want := sortedMapKeys(view.Features), []string{"feat/f:1.0.0"}; !slices.Equal(got, want) {
		t.Errorf("the view's Features are %v; want %v", got, want)
	}

	if got, want := sortedMapKeys(c.Components), []string{"comp/a:1.0.0", "comp/b:1.0.0"}; !slices.Equal(got, want) {
		t.Errorf("the catalog's Components after the view are %v; want %v", got, want)
	}
	if got, want := sortedMapKeys(c.Recipes), []string{"compose/a:1.0.0", "helm/b:1.0.0"}; !slices.Equal(got, want) {
		t.Errorf("the catalog's Recipes after the view are %v; want %v", got, want)
	}
	if got, want := c.RecipeCapabilities(), []string{"compose", "helm"}; !slices.Equal(got, want) {
		t.Errorf("the catalog's Recipes bind to %v after the view; want %v", got, want)
	}

	dangling := filepath.Join(dir, "components", "gone.yaml")
	whole := c.WithoutEntitiesAuthoredIn(func(document string) bool { return document == dangling })
	if !reflect.DeepEqual(whole.Components, c.Components) || !reflect.DeepEqual(whole.Recipes, c.Recipes) {
		t.Errorf("a view leaving out a document the catalog holds no entity of holds Components %v and Recipes %v; "+
			"want the catalog's", sortedMapKeys(whole.Components), sortedMapKeys(whole.Recipes))
	}
	if got, want := whole.RecipeCapabilities(), []string{"compose", "helm"}; !slices.Equal(got, want) {
		t.Errorf("a view leaving out a dangling document binds to %v; want %v", got, want)
	}
}
