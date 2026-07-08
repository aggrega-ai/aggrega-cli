// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"reflect"
	"testing"
)

// Minimal valid entity bodies, one per indexed kind, each parameterised by a
// distinguishing description so a first-win merge can be told apart from a
// last-win one. Identity (the merge key) is fixed per kind.
var dupKindBodies = map[string]func(desc string) string{
	"Component": func(d string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: dup/thing
  version: "1.0.0"
  description: ` + d + `
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
`
	},
	"Feature": func(d string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/dup-feature
  version: "1.0.0"
  description: ` + d + `
spec: {}
`
	},
	"Recipe": func(d string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: dup-recipe
  version: "1.0.0"
  description: ` + d + `
spec:
  target: helm
  implements: "dup/thing"
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: demo:1
`
	},
	"FeatureRecipe": func(d string) string {
		return `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: dup-fr
  version: "1.0.0"
  description: ` + d + `
spec:
  target: helm
  implements: "core/dup-feature"
`
	},
}

// TestIndexFile_WithinDirDuplicateHaltsStrict proves the homogenized rule: two
// files in ONE directory claiming the same identity halt the STRICT load with
// ErrDuplicateEntityIdentity — for every indexed kind, including the kinds that
// previously silently last-wins-overwrote (Component, Feature).
func TestIndexFile_WithinDirDuplicateHaltsStrict(t *testing.T) {
	t.Parallel()
	for kind, body := range dupKindBodies {
		kind, body := kind, body
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, dir+"/a.yaml", body("first"))
			writeFile(t, dir+"/b.yaml", body("second"))
			_, err := LoadAll("", dir)
			if err == nil {
				t.Fatalf("%s: expected within-dir duplicate to halt strict load", kind)
			}
			if !errors.Is(err, ErrDuplicateEntityIdentity) {
				t.Errorf("%s: want ErrDuplicateEntityIdentity, got %v", kind, err)
			}
		})
	}
}

// TestIndexFile_WithinDirDuplicateRecipeAliasStillMatches pins that the recipe
// facet of the within-dir collision still satisfies errors.Is against the
// preserved ErrDuplicateRecipeIdentity alias.
func TestIndexFile_WithinDirDuplicateRecipeAliasStillMatches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir+"/a.yaml", dupKindBodies["Recipe"]("first"))
	writeFile(t, dir+"/b.yaml", dupKindBodies["Recipe"]("second"))
	_, err := LoadAll("", dir)
	if !errors.Is(err, ErrDuplicateRecipeIdentity) {
		t.Errorf("recipe within-dir dup must still match the ErrDuplicateRecipeIdentity alias, got %v", err)
	}
}

// TestIndexFile_WithinDirDuplicateTolerantSkips proves Catch 1: in the TOLERANT
// load path (build Pass 1) a within-dir duplicate is a recorded SkippedEntity,
// not a hard halt — the build's out-of-closure tolerance is preserved.
func TestIndexFile_WithinDirDuplicateTolerantSkips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir+"/a.yaml", dupKindBodies["Component"]("first"))
	writeFile(t, dir+"/b.yaml", dupKindBodies["Component"]("second"))

	c, skipped, err := LoadPassOneTolerant("", "", dir)
	if err != nil {
		t.Fatalf("tolerant load must not halt on a within-dir dup: %v", err)
	}
	if _, ok := c.Components["dup/thing:1.0.0"]; !ok {
		t.Error("the first file's entity must still be indexed")
	}
	var found bool
	for _, s := range skipped {
		if errors.Is(s.Err, ErrDuplicateEntityIdentity) {
			found = true
		}
	}
	if !found {
		t.Errorf("within-dir dup must be recorded as a skip; skipped=%+v", skipped)
	}
}

// TestLoadAll_ThreeDirPrecedence proves the precedence stack across three dirs:
// the earliest dir declaring an identity wins, and every later occurrence is a
// recorded shadow.
func TestLoadAll_ThreeDirPrecedence(t *testing.T) {
	t.Parallel()
	body := dupKindBodies["Component"]
	a, b, cc := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, a+"/c.yaml", body("dir-a"))
	writeFile(t, b+"/c.yaml", body("dir-b"))
	writeFile(t, cc+"/c.yaml", body("dir-c"))

	cat, err := LoadAll("", a, b, cc)
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if got := cat.Components["dup/thing:1.0.0"].Metadata.Description; got != "dir-a" {
		t.Errorf("first-win across three dirs: want dir-a, got %q", got)
	}
	if len(cat.Shadows) != 2 {
		t.Fatalf("want 2 shadows (b and c shadowed by a), got %d: %+v", len(cat.Shadows), cat.Shadows)
	}
	for _, sh := range cat.Shadows {
		if sh.WinnerDir != a {
			t.Errorf("every shadow's winner must be dir-a, got %q", sh.WinnerDir)
		}
	}
}

// TestLoadAll_MultiDirDeterministic pins the byte-identical-output contract: the
// resolved entity set and the Shadows list are a pure function of dir order, so
// two loads of the same dirs in the same order produce equal results.
func TestLoadAll_MultiDirDeterministic(t *testing.T) {
	t.Parallel()
	body := dupKindBodies["Component"]
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, a+"/c.yaml", body("dir-a"))
	writeFile(t, b+"/c.yaml", body("dir-b"))

	c1, err := LoadAll("", a, b)
	if err != nil {
		t.Fatalf("load 1: %v", err)
	}
	c2, err := LoadAll("", a, b)
	if err != nil {
		t.Fatalf("load 2: %v", err)
	}
	if !reflect.DeepEqual(c1.Shadows, c2.Shadows) {
		t.Errorf("shadow list not deterministic:\n%+v\nvs\n%+v", c1.Shadows, c2.Shadows)
	}
	if c1.Components["dup/thing:1.0.0"].Metadata.Description != c2.Components["dup/thing:1.0.0"].Metadata.Description {
		t.Error("resolved winner not deterministic across identical loads")
	}
}
