// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIndexFile_FeatureUnmarshalError pins the Feature-kind unmarshal
// error branch.
func TestIndexFile_FeatureUnmarshalError(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
spec: [not, a, mapping]
`
	if err := c.indexFile([]byte(src), "p"); err == nil {
		t.Errorf("malformed Feature should error")
	}
}

// TestIndexFile_RecipeUnmarshalError pins the Recipe-kind unmarshal
// error branch.
func TestIndexFile_RecipeUnmarshalError(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
spec: [not, a, mapping]
`
	if err := c.indexFile([]byte(src), "p"); err == nil {
		t.Errorf("malformed Recipe should error")
	}
}

// TestIndexFile_FeatureRecipeUnmarshalError pins the FR-kind unmarshal
// error branch.
func TestIndexFile_FeatureRecipeUnmarshalError(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
spec: [not, a, mapping]
`
	if err := c.indexFile([]byte(src), "p"); err == nil {
		t.Errorf("malformed FR should error")
	}
}

// TestIndexFile_AggregateUnmarshalError pins the Aggregate-kind
// unmarshal error branch.
func TestIndexFile_AggregateUnmarshalError(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
spec: [not, a, mapping]
`
	if err := c.indexFile([]byte(src), "p"); err == nil {
		t.Errorf("malformed Aggregate should error")
	}
}

// TestIndexFile_SchemaLibraryUnmarshalError pins the SchemaLibrary
// unmarshal error.
func TestIndexFile_SchemaLibraryUnmarshalError(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
spec: [not, a, mapping]
`
	if err := c.indexFile([]byte(src), "p"); err == nil {
		t.Errorf("malformed SchemaLibrary should error")
	}
}

// TestIndexFile_MappingLibraryUnmarshalError pins the MappingLibrary
// unmarshal error.
func TestIndexFile_MappingLibraryUnmarshalError(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
spec: [not, a, mapping]
`
	if err := c.indexFile([]byte(src), "p"); err == nil {
		t.Errorf("malformed MappingLibrary should error")
	}
}

// TestIndexFile_RecipeWithStringValuesMapping pins the canonical
// string-valuesMapping shape (matching testdata fixtures) plus the
// absolute-source pass-through.
func TestIndexFile_RecipeWithStringValuesMapping(t *testing.T) {
	t.Parallel()
	c := makeEmptyCatalog()
	src := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: myco/r
  version: 1.0.0
spec:
  implements: core/compute/api
  capability: helm
  source: /absolute/path
  valuesMapping: '{"image": "x:latest"}'
`
	_ = c.indexFile([]byte(src), "/some/dir/r.yaml")
	if len(recipesImplementing(c.Recipes, "core/compute/api")) > 0 {
		var r types.Recipe
		for _, rr := range c.Recipes {
			if resolver.ImplementsSatisfies(rr.Spec.Implements, "core/compute/api") {
				r = rr
				break
			}
		}
		if s, _ := r.Spec.Source.(string); s != "/absolute/path" {
			t.Errorf("absolute source rewritten unexpectedly: %q", s)
		}
	}
}

// TestLoadAll_DuplicateKeysFirstWins pins the cross-directory merge semantic:
// catalog_dirs is an ordered first-win precedence stack, so a Component defined
// in two dirs with the same identity resolves to the EARLIER-listed dir, and the
// later occurrence is recorded as an observable ShadowedEntity (not an error).
func TestLoadAll_DuplicateKeysFirstWins(t *testing.T) {
	t.Parallel()
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()
	comp := func(desc string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: shared
  version: 1.0.0
  description: ` + desc + `
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`
	}
	writeFile(t, tmp1+"/comp.yaml", comp("from-dir-1"))
	writeFile(t, tmp2+"/comp.yaml", comp("from-dir-2"))

	// tmp1 listed first -> tmp1 wins.
	c, err := LoadAll("", tmp1, tmp2)
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	got, ok := c.Components["shared:1.0.0"]
	if !ok {
		t.Fatal("shared component missing")
	}
	if got.Metadata.Description != "from-dir-1" {
		t.Errorf("first-win violated: want the dir-1 entity, got description %q", got.Metadata.Description)
	}

	// The override is observable: exactly one shadow, dir-1 winner, dir-2 shadowed.
	if len(c.Shadows) != 1 {
		t.Fatalf("want exactly 1 shadow, got %d: %+v", len(c.Shadows), c.Shadows)
	}
	sh := c.Shadows[0]
	if sh.Kind != "Component" || sh.Identity != "shared:1.0.0" {
		t.Errorf("shadow identity wrong: %+v", sh)
	}
	if sh.WinnerDir != tmp1 || sh.ShadowedDir != tmp2 {
		t.Errorf("shadow dirs wrong: winner=%q shadowed=%q (want %q / %q)", sh.WinnerDir, sh.ShadowedDir, tmp1, tmp2)
	}

	// Reversing the order reverses the winner — precedence is purely positional.
	c2, err := LoadAll("", tmp2, tmp1)
	if err != nil {
		t.Fatalf("loadAll reversed: %v", err)
	}
	if c2.Components["shared:1.0.0"].Metadata.Description != "from-dir-2" {
		t.Errorf("reversed order should make dir-2 win, got %q", c2.Components["shared:1.0.0"].Metadata.Description)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
