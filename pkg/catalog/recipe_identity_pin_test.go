// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"testing"
)

// The tests in this file pin that a Recipe's and a FeatureRecipe's catalog
// identity is the capability its spec binds WITHOUT the version pin: the pin
// selects which version of the plugin serving the capability renders the entity,
// and never renames it. Both catalog backings are held to it, because a mapping
// resolves through whichever one the surface reading it holds.

// capabilityRecipeYAML builds a loadable Recipe binding the given capability and
// implementing the given type, declaring one artifact entry — the least a Recipe
// declares to pass load validation, which is enough for a resolution-only test.
func capabilityRecipeYAML(name, capability, implements string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  capability: \"" + capability + "\"\n  implements: " + implements + "\n" +
		"  artifacts:\n    probe.txt: \"probe\"\n"
}

// capabilityFeatureRecipeYAML builds a loadable FeatureRecipe binding the given
// capability and implementing the given type.
func capabilityFeatureRecipeYAML(name, capability, implements string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: " + name +
		"\n  version: 1.0.0\nspec:\n  capability: \"" + capability + "\"\n  implements: " + implements + "\n"
}

// TestAPinnedCapabilityIsNoPartOfTheIdentity resolves a pinned Recipe and a
// pinned FeatureRecipe by every reference shape, through the eager and the lazy
// backing. Each reference naming the bare capability (or none) resolves, and the
// resolved entity still carries its pin — which is what the provider selection
// reads. A reference spelling the pin into the capability segment names no
// identity the catalog holds, and is the negative row beside the controls.
func TestAPinnedCapabilityIsNoPartOfTheIdentity(t *testing.T) {
	t.Parallel()
	const pinned = "probe:1.2.3"
	dir := t.TempDir()
	writeFixture(t, dir, "r.yaml", capabilityRecipeYAML("svc", pinned, "core/a"))
	writeFixture(t, dir, "fr.yaml", capabilityFeatureRecipeYAML("frx", pinned, "sec/x"))

	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("eager load: %v", err)
	}
	lazy := NewLazyLookup(dir, "", []string{dir}, nil)

	type backing struct {
		name          string
		recipe        func(string) (string, bool)
		featureRecipe func(string) (string, bool)
	}
	for _, b := range []backing{
		{
			name: "eager",
			recipe: func(uri string) (string, bool) {
				r, ok := eager.RecipeByURI(uri)
				return r.Spec.Capability, ok
			},
			featureRecipe: func(uri string) (string, bool) {
				fr, ok := eager.FeatureRecipeByURI(uri)
				return fr.Spec.Capability, ok
			},
		},
		{
			name: "lazy",
			recipe: func(uri string) (string, bool) {
				r, ok := lazy.RecipeByURI(uri)
				return r.Spec.Capability, ok
			},
			featureRecipe: func(uri string) (string, bool) {
				fr, ok := lazy.FeatureRecipeByURI(uri)
				return fr.Spec.Capability, ok
			},
		},
	} {
		for _, row := range []struct {
			kind    string
			lookup  func(string) (string, bool)
			uri     string
			resolve bool
		}{
			{"Recipe", b.recipe, "probe/svc:1.0.0", true},
			{"Recipe", b.recipe, "probe/svc", true},
			{"Recipe", b.recipe, "svc:1.0.0", true},
			{"Recipe", b.recipe, pinned + "/svc:1.0.0", false},
			{"FeatureRecipe", b.featureRecipe, "probe/frx:1.0.0", true},
			{"FeatureRecipe", b.featureRecipe, "frx", true},
			{"FeatureRecipe", b.featureRecipe, pinned + "/frx:1.0.0", false},
		} {
			capability, ok := row.lookup(row.uri)
			if ok != row.resolve {
				t.Errorf("%s backing, %s %q: resolved = %v; want %v", b.name, row.kind, row.uri, ok, row.resolve)
				continue
			}
			if ok && capability != pinned {
				t.Errorf("%s backing, %s %q: resolved entity binds %q; want the pin kept, %q",
					b.name, row.kind, row.uri, capability, pinned)
			}
		}
	}
}

// TestAPinnedAndAnUnpinnedRecipeOfOneNameAreOneIdentity pins the consequence of
// the identity reading: two documents declaring one name and version, one with a
// pinned capability and one without, declare the same identity. The eager load
// refuses the pair in one directory as it refuses any duplicate identity; the
// lazy backing indexes the first and passes over the second. The control is the
// same pair under two names, which both backings hold side by side.
func TestAPinnedAndAnUnpinnedRecipeOfOneNameAreOneIdentity(t *testing.T) {
	t.Parallel()

	t.Run("one name: one identity", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFixture(t, dir, "a.yaml", capabilityRecipeYAML("svc", "probe", "core/a"))
		writeFixture(t, dir, "b.yaml", capabilityRecipeYAML("svc", "probe:1.2.3", "core/a"))
		if _, err := LoadAll("", dir); !errors.Is(err, ErrDuplicateEntityIdentity()) {
			t.Errorf("eager load: want the duplicate-identity refusal; got %v", err)
		}
		lazy := NewLazyLookup(dir, "", []string{dir}, nil)
		lazy.buildRecipeIndex()
		if got := len(lazy.recipes); got != 1 {
			t.Errorf("lazy backing indexed %d recipes; want 1, the pair being one identity", got)
		}
	})

	t.Run("control: two names, two identities", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFixture(t, dir, "a.yaml", capabilityRecipeYAML("svc", "probe", "core/a"))
		writeFixture(t, dir, "b.yaml", capabilityRecipeYAML("svc-pinned", "probe:1.2.3", "core/a"))
		eager, err := LoadAll("", dir)
		if err != nil {
			t.Fatalf("eager load: %v", err)
		}
		if got := len(eager.Recipes); got != 2 {
			t.Errorf("eager catalog holds %d recipes; want 2", got)
		}
		lazy := NewLazyLookup(dir, "", []string{dir}, nil)
		lazy.buildRecipeIndex()
		if got := len(lazy.recipes); got != 2 {
			t.Errorf("lazy backing indexed %d recipes; want 2", got)
		}
	})
}

// TestTheIdentityFormulaDropsTheCapabilityPin pins the exported face of the
// identity key and the registry location derived from it.
func TestTheIdentityFormulaDropsTheCapabilityPin(t *testing.T) {
	t.Parallel()
	for _, r := range []struct {
		capability, name, version, want string
	}{
		{"helm", "x", "1.0.0", "helm/x:1.0.0"},
		{"helm:1.4.0", "x", "1.0.0", "helm/x:1.0.0"},
		{"helm:^1.0.0", "x", "1.0.0", "helm/x:1.0.0"},
		{"helm:1.4.0", "x", "", "helm/x"},
		{"", "x", "1.0.0", "x:1.0.0"},
	} {
		if got := RecipeIdentityURI(r.capability, r.name, r.version); got != r.want {
			t.Errorf("RecipeIdentityURI(%q, %q, %q) = %q; want %q", r.capability, r.name, r.version, got, r.want)
		}
	}
	for _, r := range []struct {
		kind, scope, name, want string
	}{
		{"Recipe", "helm:1.4.0", "Mariadb", "recipe/helm/mariadb"},
		{"FeatureRecipe", "compose:^1.0.0", "banner", "featurerecipe/compose/banner"},
		{"Recipe", "helm", "mariadb", "recipe/helm/mariadb"},
		{"Plugin", "provider", "helm", "plugin/provider/helm"},
	} {
		if got := OCILocation(r.kind, r.scope, r.name); got != r.want {
			t.Errorf("OCILocation(%q, %q, %q) = %q; want %q", r.kind, r.scope, r.name, got, r.want)
		}
	}
}
