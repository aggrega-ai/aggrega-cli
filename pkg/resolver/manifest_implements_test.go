// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestImplementsSatisfies pins the caret semantics of the single implements
// matcher: given one recipe's spec.implements (impl) and a concrete
// component-type request (componentType), it reports whether impl satisfies the
// request under the three-form rule (exact, caret-range, unversioned base).
//
// Both surfaces that read the matcher depend on these cases — the implements
// SEARCH that suggests recipes, and the mapping-consistency rule that judges what
// an operator wrote — so this coverage guards both.
func TestImplementsSatisfies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                string
		impl, componentType string
		want                bool
	}{
		{"exact match", "core/api-server:1.0.0", "core/api-server:1.0.0", true},
		{"caret in range", "core/api-server:^1.0.0", "core/api-server:1.5.3", true},
		{"caret out of range", "core/api-server:^1.0.0", "core/api-server:2.0.0", false},
		{"caret base mismatch", "other/thing:^1.0.0", "core/api-server:1.5.0", false},
		{"unversioned impl matches versioned request", "core/api-server", "core/api-server:1.0.0", true},
		{"unversioned impl and unversioned request", "core/api-server", "core/api-server", true},
		{"versioned impl cannot match unversioned request", "core/api-server:1.0.0", "core/api-server", false},
		{"non-caret version mismatch same base", "core/api-server:1.0.0", "core/api-server:2.0.0", false},
		{"prerelease caller within prerelease caret baseline", "core/api-server:^1.0.0-beta", "core/api-server:1.0.0-rc1", true},
		{"caret cannot match unparseable request version", "core/api-server:^1.0.0", "core/api-server:not-a-version", false},
		{"unrelated base is a miss", "core/api-server:1.0.0", "different/component:1.0.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ImplementsSatisfies(tc.impl, tc.componentType); got != tc.want {
				t.Errorf("ImplementsSatisfies(%q, %q) = %v; want %v", tc.impl, tc.componentType, got, tc.want)
			}
		})
	}
}

// TestManifestMappingConsistent pins the bidirectional version truth table the
// mapping-consistency rule applies between a Manifest mapping key and the mapped
// recipe's spec.implements. The key may be bare or version-pinned; the implements
// may be bare, exact, or caret. A bare key imposes no version constraint (base
// match suffices, including against a version-pinned implements); a version-pinned
// key is judged by exact/caret/base satisfaction.
func TestManifestMappingConsistent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		impl, key string
		want      bool
	}{
		{"bare key, bare impl, same base", "core/datastore/postgres", "core/datastore/postgres", true},
		{"bare key, versioned impl, same base", "core/datastore/postgres:1.0.0", "core/datastore/postgres", true},
		{"bare key, caret impl, same base", "core/datastore/postgres:^1.0.0", "core/datastore/postgres", true},
		{"versioned key, bare impl, same base", "core/datastore/postgres", "core/datastore/postgres:1.0.0", true},
		{"versioned key, exact impl, same version", "core/datastore/postgres:1.0.0", "core/datastore/postgres:1.0.0", true},
		{"versioned key in caret range", "core/api:^1.0.0", "core/api:1.2.0", true},
		{"versioned key out of caret range", "core/api:^1.0.0", "core/api:2.0.0", false},
		{"versioned key, exact impl, version mismatch", "core/api:1.0.0", "core/api:2.0.0", false},
		{"bare key, differing base", "core/datastore/mysql", "core/datastore/postgres", false},
		{"versioned key, differing base", "core/datastore/mysql:1.0.0", "core/datastore/postgres:1.0.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := manifestMappingConsistent(tc.impl, tc.key); got != tc.want {
				t.Errorf("manifestMappingConsistent(impl=%q, key=%q) = %v; want %v", tc.impl, tc.key, got, tc.want)
			}
		})
	}
}

// TestManifestMappingSatisfiedByAncestry pins that ancestry enters the
// satisfaction rule as additional inputs rather than as a second matching rule:
// each base is offered to the same comparison the mapped key itself is offered
// to, so a Recipe written with a caret against a base satisfies an extending type
// exactly as it would satisfy the base directly.
func TestManifestMappingSatisfiedByAncestry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		impl     string
		ancestry []string
		want     bool
	}{
		{"no ancestry satisfies nothing", "core/base:1.0.0", nil, false},
		{"a base matched exactly", "core/base:1.0.0", []string{"core/base:1.0.0"}, true},
		{"a base matched through a caret", "core/base:^1.0.0", []string{"core/base:1.4.0"}, true},
		{"a base out of the caret range", "core/base:^2.0.0", []string{"core/base:1.4.0"}, false},
		{"a deeper base matched", "core/root:1.0.0", []string{"core/mid:1.0.0", "core/root:1.0.0"}, true},
		{"no base matched", "core/other:1.0.0", []string{"core/mid:1.0.0", "core/root:1.0.0"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := manifestMappingSatisfiedByAncestry(tc.impl, tc.ancestry); got != tc.want {
				t.Errorf("manifestMappingSatisfiedByAncestry(%q, %v) = %v; want %v",
					tc.impl, tc.ancestry, got, tc.want)
			}
		})
	}
}

// TestMappedRecipeImplements pins the tier dispatch and the nil-tolerance of the
// catalog reading the mapping-consistency rule is built on.
//
// The unrecognised-tier row is the one worth keeping: the tier vocabulary is
// closed by the authoring surface, so answering for one of the two kinds when
// handed something else would hide a caller mistake behind a plausible answer.
func TestMappedRecipeImplements(t *testing.T) {
	t.Parallel()

	full := CatalogLookup{
		Recipe: func(reference string) (types.Recipe, bool) {
			if reference != "helm/api" {
				return types.Recipe{}, false
			}
			return types.Recipe{Spec: types.RecipeSpec{Implements: "core/compute/api"}}, true
		},
		FeatureRecipe: func(reference string) (types.FeatureRecipe, bool) {
			if reference != "helm/patch" {
				return types.FeatureRecipe{}, false
			}
			return types.FeatureRecipe{Spec: types.FeatureRecipeSpec{Implements: "feature/patch"}}, true
		},
	}

	cases := []struct {
		name           string
		catalog        CatalogLookup
		tier           string
		reference      string
		wantImplements string
		wantResolved   bool
	}{
		{"components tier resolves a Recipe", full, constraint.MappingTierComponents, "helm/api", "core/compute/api", true},
		{"features tier resolves a FeatureRecipe", full, constraint.MappingTierFeatures, "helm/patch", "feature/patch", true},
		{"components tier does not reach the FeatureRecipe lookup", full, constraint.MappingTierComponents, "helm/patch", "", false},
		{"features tier does not reach the Recipe lookup", full, constraint.MappingTierFeatures, "helm/api", "", false},
		{"a reference the catalog does not hold", full, constraint.MappingTierComponents, "helm/absent", "", false},
		{"an unrecognised tier resolves nothing", full, "workloads", "helm/api", "", false},
		{"an unfilled lookup resolves nothing", CatalogLookup{}, constraint.MappingTierComponents, "helm/api", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			implements, resolved := mappedRecipeImplements(tc.catalog, tc.tier, tc.reference)
			if implements != tc.wantImplements || resolved != tc.wantResolved {
				t.Errorf("mappedRecipeImplements(tier=%q, reference=%q) = (%q, %v); want (%q, %v)",
					tc.tier, tc.reference, implements, resolved, tc.wantImplements, tc.wantResolved)
			}
		})
	}
}
