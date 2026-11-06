// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// implementsRequestComponentYAML is a minimal loadable Component of the type
// app/db at version.
func implementsRequestComponentYAML(version string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: app/db\n  version: \"" + version +
		"\"\n  labels:\n    component.aggrega.ai/type: server\nspec: {}\n"
}

// implementsRequestCatalog writes, under a fresh directory, the Component app/db
// at 1.0.0 and 2.0.0 and the Recipes implementing it in each of the forms an
// implements takes — the exact 1.0.0, the exact 2.0.0, the unversioned type and
// the caret range ^2.0.0 — plus a Recipe implementing app/none, a type the
// catalog holds no Component of, unversioned, and one implementing it at 1.0.0.
// It returns the directory.
func implementsRequestCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "component/app/db/1.0.0/component.yaml", implementsRequestComponentYAML("1.0.0"))
	writeCatalogEntity(t, dir, "component/app/db/2.0.0/component.yaml", implementsRequestComponentYAML("2.0.0"))
	writeCatalogEntity(t, dir, "recipe/db-one.yaml", recipeYAML("db-one", "app/db:1.0.0"))
	writeCatalogEntity(t, dir, "recipe/db-two.yaml", recipeYAML("db-two", "app/db:2.0.0"))
	writeCatalogEntity(t, dir, "recipe/db-any.yaml", recipeYAML("db-any", "app/db"))
	writeCatalogEntity(t, dir, "recipe/db-caret.yaml", recipeYAML("db-caret", `"app/db:^2.0.0"`))
	writeCatalogEntity(t, dir, "recipe/none-any.yaml", recipeYAML("none-any", "app/none"))
	writeCatalogEntity(t, dir, "recipe/none-one.yaml", recipeYAML("none-one", "app/none:1.0.0"))
	return dir
}

// recipeIdentities projects Recipes onto their identity keys, in order.
func recipeIdentities(recipes []types.Recipe) []string {
	out := make([]string, 0, len(recipes))
	for _, r := range recipes {
		out = append(out, recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version))
	}
	return out
}

// TestAnUnversionedTypeIsAskedAtTheVersionTheCatalogResolvesItTo holds the
// implements enumeration to the version a build resolves a type naming none to,
// one row per shape of request, over a catalog holding the Component app/db at
// 1.0.0 and 2.0.0:
//
//   - the bare type is asked at 2.0.0, the highest version held, so it finds the
//     Recipes implementing 2.0.0 exactly, the caret range ^2.0.0 and the
//     unversioned type, and not the one implementing 1.0.0;
//   - a type pinning 1.0.0 is asked as written;
//   - a bare type the catalog holds no Component of is asked as written, so only
//     the Recipe implementing that unversioned type answers;
//   - asked with no Component resolution at all, the bare type is asked as
//     written too — the reading the enumeration gave every bare type before it
//     resolved one, and the reason it found none of the versioned Recipes.
//
// Each row is read through the composer's entry point, (*Catalog).RecipesImplementing,
// and the pin-candidate projection the no-mapping halt reads, and the two are
// required to name the same Recipes in the same order.
//
// 🔴 PLANTED DEFECT: in implementsRequest (pkg/catalog/manifest_lookup.go), return
// componentType unconditionally: the bare-type row reddens — it finds db-any
// alone — and so does TestABareInstanceIsOfferedTheRecipesImplementingItsResolvedVersion.
func TestAnUnversionedTypeIsAskedAtTheVersionTheCatalogResolvesItTo(t *testing.T) {
	t.Parallel()
	c, err := LoadAll("", implementsRequestCatalog(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, tc := range []struct {
		name          string
		componentType string
		component     componentResolver
		want          []string
	}{
		{name: "a bare type the catalog holds at two versions", componentType: "app/db", component: c.componentOfType,
			want: []string{"probe/db-any:1.0.0", "probe/db-caret:1.0.0", "probe/db-two:1.0.0"}},
		{name: "a type pinning a version", componentType: "app/db:1.0.0", component: c.componentOfType,
			want: []string{"probe/db-any:1.0.0", "probe/db-one:1.0.0"}},
		{name: "a bare type the catalog holds no Component of", componentType: "app/none", component: c.componentOfType,
			want: []string{"probe/none-any:1.0.0"}},
		{name: "a bare type asked with no Component resolution", componentType: "app/db", component: nil,
			want: []string{"probe/db-any:1.0.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := recipesImplementing(c.Recipes, tc.componentType, tc.component)
			if !slices.Equal(candidates, tc.want) {
				t.Errorf("the pin candidates for %q are %v; want %v", tc.componentType, candidates, tc.want)
			}
			if tc.component == nil {
				return
			}
			if drafted := recipeIdentities(c.RecipesImplementing(tc.componentType)); !slices.Equal(drafted, candidates) {
				t.Errorf("the composer's enumeration for %q names %v and the halt's %v; the two readers must "+
					"name the same Recipes", tc.componentType, drafted, candidates)
			}
		})
	}
}

// TestABareInstanceIsOfferedTheRecipesImplementingItsResolvedVersion holds the
// no-mapping halt's input for an instance whose type names no version to the
// Recipes implementing the version the build resolves it to, on both catalog
// views a build reads: the eagerly-loaded catalog, through the instance
// resolution its own lookup serves, and the lazy lookup, whose recipe shell holds
// no Component index and is handed the lookup's own Component resolution.
//
// 🔴 PLANTED DEFECT: in LazyLookup.NodeRecipeResolution (pkg/catalog/lazy_lookup.go),
// pass nil in place of l.component: the lazy row reddens, offering db-any alone.
func TestABareInstanceIsOfferedTheRecipesImplementingItsResolvedVersion(t *testing.T) {
	t.Parallel()
	dir := implementsRequestCatalog(t)
	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lazy := NewLazyLookup(dir, "", []string{dir}, nil)
	want := []string{"probe/db-any:1.0.0", "probe/db-caret:1.0.0", "probe/db-two:1.0.0"}
	for _, tc := range []struct {
		name    string
		resolve func(manifest *types.Manifest, instanceName, componentType string) []string
	}{
		{name: "the eager catalog", resolve: func(m *types.Manifest, instance, componentType string) []string {
			return eager.CatalogLookup().NodeRecipeResolution(m, instance, componentType).PinCandidates
		}},
		{name: "the lazy lookup", resolve: func(m *types.Manifest, instance, componentType string) []string {
			return lazy.NodeRecipeResolution(m, instance, componentType).PinCandidates
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.resolve(&types.Manifest{}, "db", "app/db"); !slices.Equal(got, want) {
				t.Errorf("an unmapped instance of app/db is offered %v; want %v", got, want)
			}
		})
	}
}

// featureImplementsRequestFeatureYAML is a minimal loadable Feature of the type
// name at version.
func featureImplementsRequestFeatureYAML(name, version string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: " + name + "\n  version: \"" + version +
		"\"\nspec:\n  schema:\n    values:\n      label: { type: string, default: \"x\" }\n"
}

// featureImplementsRequestCatalog writes, under a fresh directory, the Feature
// sec/cred at 1.0.0 and 2.0.0 with FeatureRecipes implementing each version
// exactly — the form the standard-library catalog's FeatureRecipes declare — one
// binding to probe for each version and one binding to elsewhere for 2.0.0; the
// Feature sec/lonely at 1.0.0, which no FeatureRecipe implements; and two Recipes
// implementing app/api, one binding to probe and one to other, for the component
// an attachment sits on. It returns the directory.
func featureImplementsRequestCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "feature/sec/cred/1.0.0/feature.yaml", featureImplementsRequestFeatureYAML("sec/cred", "1.0.0"))
	writeCatalogEntity(t, dir, "feature/sec/cred/2.0.0/feature.yaml", featureImplementsRequestFeatureYAML("sec/cred", "2.0.0"))
	writeCatalogEntity(t, dir, "feature/sec/lonely/1.0.0/feature.yaml", featureImplementsRequestFeatureYAML("sec/lonely", "1.0.0"))
	writeCatalogEntity(t, dir, "feature-recipe/cred-one.yaml", featureRecipeYAML("cred-one", "sec/cred:1.0.0"))
	writeCatalogEntity(t, dir, "feature-recipe/cred-two.yaml", featureRecipeYAML("cred-two", "sec/cred:2.0.0"))
	writeCatalogEntity(t, dir, "feature-recipe/cred-two-elsewhere.yaml",
		capabilityFeatureRecipeYAML("cred-two-elsewhere", "elsewhere", "sec/cred:2.0.0"))
	writeCatalogEntity(t, dir, "recipe/api-probe.yaml", capabilityRecipeYAML("api-probe", "probe", "app/api"))
	writeCatalogEntity(t, dir, "recipe/api-other.yaml", capabilityRecipeYAML("api-other", "other", "app/api"))
	return dir
}

// TestAnUnmappedFeatureNamesWhatIsMissing drives the resolution-completeness
// halt a build installs before it renders over an unmapped feature instance
// whose type names no version, on both catalog views a build reads — the
// eagerly-loaded catalog and the lazy lookup the build command wires in, whose
// recipe shell holds no Feature index and is handed the lookup's own Feature
// resolution — and holds the message to the case the instance is in. The type is
// resolved to 2.0.0, the highest held, so the FeatureRecipe implementing 1.0.0 is
// never offered:
//
//   - the component's Recipe binds to probe, which a FeatureRecipe implementing
//     the type binds to: only that one is named, to pin;
//   - the component's Recipe binds to other, which none binds to: the message
//     says none implements sec/cred:2.0.0 for other, names the ones that do for
//     other capabilities, and offers both remedies — pin one of those, which
//     renders as its own output beside the component's, or write one for other;
//   - nothing implements the type: the message names the type at the version
//     searched for, so it claims an absence only of what was asked about;
//   - the component is mapped to no Recipe, so the capability is not known:
//     every implementer is named.
//
// Every row carries the one condition, no-recipe-resolved.
//
// 🔴 PLANTED DEFECT: in unmappedFeatureFault (pkg/catalog/manifest_lookup.go),
// restore the earlier "no feature recipe implements this type" sentence: the
// unimplemented rows redden. Pass nil in place of l.feature in
// LazyLookup.ResolutionFindings (pkg/catalog/lazy_lookup.go): every lazy row on
// sec/cred reddens, reporting that no feature recipe implements the type. Make
// hostTargetCapability return "": the two rows naming a capability redden.
func TestAnUnmappedFeatureNamesWhatIsMissing(t *testing.T) {
	t.Parallel()
	dir := featureImplementsRequestCatalog(t)
	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lazy := NewLazyLookup(dir, "", []string{dir}, nil)
	views := []struct {
		name   string
		verify func(*types.Manifest, *types.IRGraph) error
	}{
		{name: "the eager catalog", verify: eager.VerifyRecipeResolution},
		{name: "the lazy lookup", verify: lazy.VerifyRecipeResolution},
	}
	// hostMappedTo is a Manifest mapping app/api to recipe and nothing else, so
	// every attachment stays unmapped.
	hostMappedTo := func(recipe string) *types.Manifest {
		comp := &types.MappingTier{}
		comp.Type.Set("app/api", recipe)
		return &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp}}}
	}
	cases := []struct {
		name        string
		manifest    *types.Manifest
		featureType string
		want        string
		notWant     string
	}{
		{
			name:        "implementers exist for the component's capability",
			manifest:    hostMappedTo("probe/api-probe:1.0.0"),
			featureType: "sec/cred",
			want:        `feature "password" (type "sec/cred"); feature recipes implementing it for capability "probe", the capability its component's Recipe binds to: [probe/cred-two:1.0.0]; pin one in the Manifest`,
			notWant:     "elsewhere/",
		},
		{
			name:        "implementers exist only for other capabilities",
			manifest:    hostMappedTo("other/api-other:1.0.0"),
			featureType: "sec/cred",
			want: `feature "password" (type "sec/cred"); no feature recipe in the catalog implements sec/cred:2.0.0 for capability "other", ` +
				`the capability its component's Recipe binds to; feature recipes implementing it for other capabilities: ` +
				`[elsewhere/cred-two-elsewhere:1.0.0 probe/cred-two:1.0.0]; pin one of those in the Manifest, and it renders ` +
				`as its own output beside the component's, or write one for "other", then map the type to it`,
			notWant: "pin one in the Manifest",
		},
		{
			name:        "no feature recipe implements the resolved version",
			manifest:    hostMappedTo("probe/api-probe:1.0.0"),
			featureType: "sec/lonely",
			want:        `feature "password" (type "sec/lonely"); no feature recipe in the catalog implements sec/lonely:1.0.0; write one, then map the type to it`,
			notWant:     "pin one in the Manifest",
		},
		{
			name:        "the component's capability is not known",
			manifest:    &types.Manifest{},
			featureType: "sec/cred",
			want:        `feature "password" (type "sec/cred"); feature recipes implementing it: [elsewhere/cred-two-elsewhere:1.0.0 probe/cred-two:1.0.0]; pin one in the Manifest`,
			notWant:     "for capability",
		},
	}
	for _, view := range views {
		for _, tc := range cases {
			t.Run(view.name+"/"+tc.name, func(t *testing.T) {
				graph := &types.IRGraph{}
				graph.Spec.Nodes.Set("api", types.IRNode{Component: "app/api"})
				feats := types.OrderedMap[types.IRFeatureEntry]{}
				feats.Set("password", types.IRFeatureEntry{Type: tc.featureType})
				graph.Spec.FeatureValuesMapping.Set("api", feats)

				err := view.verify(tc.manifest, graph)
				if err == nil {
					t.Fatalf("an unmapped feature instance must halt")
				}
				if !errors.Is(err, ErrNoRecipeResolved()) {
					t.Errorf("expected ErrNoRecipeResolved in the chain; got %v", err)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("the halt must read %q; got %v", tc.want, err)
				}
				if strings.Contains(err.Error(), tc.notWant) {
					t.Errorf("the halt must not read %q in this case; got %v", tc.notWant, err)
				}
			})
		}
	}
}
