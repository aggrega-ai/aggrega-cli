// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestScopeToInstanceVersions holds the scoped lookup to the rule it states: a
// reference the instance-version table holds is answered at the version the table
// resolves it to — its Component, its Feature, its effective schema and the type
// its implements request is asked with — and every other reference is passed
// through as written, while the Manifest's mapping lookup keeps reading the
// reference as the Aggregate writes it.
func TestScopeToInstanceVersions(t *testing.T) {
	t.Parallel()

	t.Run("asks the lookup at the table's version for a reference the table holds", func(t *testing.T) {
		t.Parallel()
		var asked []string
		inner := resolver.CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				asked = append(asked, "Component "+typeURI)
				return types.Component{}, true
			},
			Feature: func(typeURI string) (types.Feature, bool) {
				asked = append(asked, "Feature "+typeURI)
				return types.Feature{}, true
			},
			EffectiveSchema: func(typeURI string) (compactform.Schema, bool) {
				asked = append(asked, "EffectiveSchema "+typeURI)
				return compactform.Schema{}, true
			},
		}
		var table InstanceVersions
		table.Selected.Set("core/compute/server", "1.0.0")
		table.Selected.Set("core/compute/server:^1.0.0", "1.1.0")
		table.FeatureSelected.Set("core/secret-value", "1.0.0")
		table.FeatureSelected.Set("core/compute/server", "1.1.0")

		scoped := ScopeToInstanceVersions(inner, table)
		scoped.Component("core/compute/server")
		scoped.Component("core/compute/server:^1.0.0")
		scoped.Component("example/web")
		scoped.Feature("core/secret-value")
		scoped.Feature("core/other-feature")
		// A Feature and a Component written alike read their own rows.
		scoped.Feature("core/compute/server")
		scoped.Component("core/secret-value")
		scoped.EffectiveSchema("core/compute/server")
		scoped.EffectiveSchema("example/web")

		want := []string{
			"Component core/compute/server:1.0.0",
			"Component core/compute/server:1.1.0",
			"Component example/web",
			"Feature core/secret-value:1.0.0",
			"Feature core/other-feature",
			"Feature core/compute/server:1.1.0",
			"Component core/secret-value",
			"EffectiveSchema core/compute/server:1.0.0",
			"EffectiveSchema example/web",
		}
		if !slices.Equal(asked, want) {
			t.Errorf("the scoped lookup asked\n %q\nwant\n %q", asked, want)
		}
	})

	t.Run("leaves a field the lookup does not carry absent", func(t *testing.T) {
		t.Parallel()
		var table InstanceVersions
		table.Selected.Set("core/compute/server", "1.0.0")
		scoped := ScopeToInstanceVersions(resolver.CatalogLookup{}, table)
		if scoped.Component != nil || scoped.Feature != nil || scoped.EffectiveSchema != nil || scoped.NodeRecipeResolution != nil {
			t.Error("the scoped lookup carries a field the lookup it scopes leaves absent")
		}
	})

	for _, v := range scopedLookupViews(t, instanceVersionsCatalog(t)) {
		t.Run(v.name+"/a bare type is answered at the version its mapping admits", func(t *testing.T) {
			t.Parallel()
			// The bare key maps the type to a Recipe implementing 1.0.0; the key
			// naming 1.1.0 sits beside it, so the bare type's base matches two keys
			// and only the reference as written finds the bare key exactly.
			manifest := instanceVersionsManifest([][2]string{
				{"core/compute/server", "compose/server:1.0.0"},
				{"core/compute/server:1.1.0", "compose/server:2.0.0"},
			}, nil)
			scoped := v.scope(t, manifest, componentInstance("web", "core/compute/server"))

			if got := v.lookup.Component; got == nil {
				t.Fatal("the view's lookup carries no Component resolution")
			}
			if comp, _ := v.lookup.Component("core/compute/server"); comp.Metadata.Version != "1.1.0" {
				t.Fatalf("the unscoped lookup answers %q; the case needs it to answer the highest version, 1.1.0", comp.Metadata.Version)
			}
			if comp, ok := scoped.Component("core/compute/server"); !ok || comp.Metadata.Version != "1.0.0" {
				t.Errorf("Component answered %q (found %t), want 1.0.0", comp.Metadata.Version, ok)
			}
			if comp, ok := scoped.Component("example/web"); !ok || comp.Metadata.Version != "1.1.0" {
				t.Errorf("a reference the table does not hold answered %q (found %t), want 1.1.0 as written", comp.Metadata.Version, ok)
			}

			resolution := scoped.NodeRecipeResolution(manifest, "web", "core/compute/server")
			want := resolver.NodeRecipeResolution{
				Reference:    "compose/server:1.0.0",
				Mapped:       true,
				Present:      true,
				TypeKey:      "core/compute/server",
				MappedByType: true,
				ResolvedType: "core/compute/server:1.0.0",
			}
			if !nodeRecipeResolutionsEqual(resolution, want) {
				t.Errorf("NodeRecipeResolution = %+v\nwant %+v", resolution, want)
			}
		})

		t.Run(v.name+"/a caret type is asked at the version the table selects", func(t *testing.T) {
			t.Parallel()
			manifest := instanceVersionsManifest([][2]string{
				{"core/compute/server", "compose/server:2.0.0"},
			}, nil)
			scoped := v.scope(t, manifest, componentInstance("web", "core/compute/server:^1.0.0"))
			if got := v.lookup.NodeRecipeResolution(manifest, "web", "core/compute/server:^1.0.0").ResolvedType; got != "core/compute/server:^1.0.0" {
				t.Fatalf("the unscoped resolution asks with %q; the case needs it to ask with the caret as written", got)
			}
			if got := scoped.NodeRecipeResolution(manifest, "web", "core/compute/server:^1.0.0").ResolvedType; got != "core/compute/server:1.1.0" {
				t.Errorf("ResolvedType = %q, want core/compute/server:1.1.0", got)
			}
		})

		t.Run(v.name+"/an unmapped instance is offered the Recipes implementing the table's version", func(t *testing.T) {
			t.Parallel()
			// web's name entry maps it to a Recipe implementing 1.0.0, which holds
			// the reference both instances write at 1.0.0; nothing maps api.
			manifest := instanceVersionsManifest(nil, [][2]string{{"web", "compose/server:1.0.0"}})
			scoped := v.scope(t, manifest,
				componentInstance("web", "core/compute/server"),
				componentInstance("api", "core/compute/server"))

			if got := v.lookup.NodeRecipeResolution(manifest, "api", "core/compute/server").PinCandidates; !slices.Equal(got, []string{"compose/server:2.0.0"}) {
				t.Fatalf("the unscoped resolution offers %q; the case needs it to offer the Recipe implementing 1.1.0", got)
			}
			resolution := scoped.NodeRecipeResolution(manifest, "api", "core/compute/server")
			want := resolver.NodeRecipeResolution{
				PinCandidates: []string{"compose/server:1.0.0"},
				ResolvedType:  "core/compute/server:1.0.0",
			}
			if !nodeRecipeResolutionsEqual(resolution, want) {
				t.Errorf("NodeRecipeResolution = %+v\nwant %+v", resolution, want)
			}
		})
	}
}

// scopedLookupView is one catalog view a build reads, as the lookup a compilation
// is handed and as the view the instance-version table reads.
type scopedLookupView struct {
	name   string
	lookup resolver.CatalogLookup
	view   InstanceVersionView
}

// scope resolves the table of the instances under manifest over the view and
// returns the view's lookup scoped to it.
func (v scopedLookupView) scope(t *testing.T, manifest *types.Manifest, instances ...closure.InstanceReference) resolver.CatalogLookup {
	t.Helper()
	table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
	if err != nil {
		t.Fatalf("resolve the instance versions: %v", err)
	}
	return ScopeToInstanceVersions(v.lookup, table)
}

// scopedLookupViews returns both catalog views a build reads over dir: the
// eagerly-loaded catalog and the lazy lookup.
func scopedLookupViews(t *testing.T, dir string) []scopedLookupView {
	t.Helper()
	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lazy := NewLazyLookup(dir, "", []string{dir}, nil)
	return []scopedLookupView{
		{name: "the eager catalog", lookup: eager.CatalogLookup(), view: eager},
		{name: "the lazy lookup", lookup: lazy.AsCatalogLookup(), view: lazy},
	}
}

// nodeRecipeResolutionsEqual reports whether two instance resolutions carry the
// same coordinates.
func nodeRecipeResolutionsEqual(a, b resolver.NodeRecipeResolution) bool {
	return a.Reference == b.Reference && a.Mapped == b.Mapped && a.Present == b.Present &&
		slices.Equal(a.AmbiguousKeys, b.AmbiguousKeys) && slices.Equal(a.PinCandidates, b.PinCandidates) &&
		a.PinCandidatesFromPinsAlone == b.PinCandidatesFromPinsAlone && a.TypeKey == b.TypeKey &&
		a.MappedByType == b.MappedByType && a.ResolvedType == b.ResolvedType
}
