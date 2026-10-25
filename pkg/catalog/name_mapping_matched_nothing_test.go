// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover a Manifest name-tier key that names nothing in the
// build. The name tier is matched by exact string against the instance a lookup
// asks about, so such a key is consulted by no lookup; the resolution findings
// report it, naming the key, the Manifest, the names of that kind the build
// declares, and the one the author probably meant. Every row carries one such key
// against a graph whose every instance otherwise resolves, so a finding is about
// that key and nothing else. The condition is matched through its accessor.

// nameMappingFixture loads a catalog through the real loader and returns it with a
// graph holding two greeters, an attachment of one Feature type on each, a second
// Feature type on one of them, and an attachment at boundary scope.
func nameMappingFixture(t *testing.T) (*Catalog, *types.IRGraph) {
	t.Helper()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "recipe/greeter-blue.yaml", recipeYAML("greeter-blue", "app/greeter"))
	writeCatalogEntity(t, dir, "recipe/greeter-green.yaml", recipeYAML("greeter-green", "app/greeter"))
	writeCatalogEntity(t, dir, "feature-recipe/banner-plain.yaml", featureRecipeYAML("banner-plain", "sec/banner"))
	writeCatalogEntity(t, dir, "feature-recipe/banner-loud.yaml", featureRecipeYAML("banner-loud", "sec/banner"))
	writeCatalogEntity(t, dir, "feature-recipe/logs.yaml", featureRecipeYAML("logs", "sec/logs"))
	writeCatalogEntity(t, dir, "feature-recipe/waf.yaml", featureRecipeYAML("waf", "sec/waf"))
	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}

	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("greeter-default", types.IRNode{Component: "app/greeter"})
	graph.Spec.Nodes.Set("greeter-special", types.IRNode{Component: "app/greeter"})
	onDefault := types.OrderedMap[types.IRFeatureEntry]{}
	onDefault.Set("banner", types.IRFeatureEntry{Type: "sec/banner"})
	graph.Spec.FeatureValuesMapping.Set("greeter-default", onDefault)
	onSpecial := types.OrderedMap[types.IRFeatureEntry]{}
	onSpecial.Set("loud-banner", types.IRFeatureEntry{Type: "sec/banner"})
	onSpecial.Set("access-logs", types.IRFeatureEntry{Type: "sec/logs"})
	graph.Spec.FeatureValuesMapping.Set("greeter-special", onSpecial)
	atEdge := types.OrderedMap[types.IRBoundaryFeature]{}
	atEdge.Set("firewall", types.IRBoundaryFeature{Type: "sec/waf"})
	graph.Spec.BoundaryFeatures.Set("edge", atEdge)
	return c, graph
}

// nameMappingManifest maps every type the fixture's graph holds through the type
// tier and writes the given name-tier entries, so the only finding a row can
// produce is about a name-tier key.
func nameMappingManifest(components, features [][2]string) *types.Manifest {
	comp := &types.MappingTier{}
	comp.Type.Set("app/greeter", "eval/greeter-blue:1.0.0")
	for _, kv := range components {
		comp.Name.Set(kv[0], kv[1])
	}
	feat := &types.MappingTier{}
	feat.Type.Set("sec/banner", "eval/banner-plain:1.0.0")
	feat.Type.Set("sec/logs", "eval/logs:1.0.0")
	feat.Type.Set("sec/waf", "eval/waf:1.0.0")
	for _, kv := range features {
		feat.Name.Set(kv[0], kv[1])
	}
	m := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp, Features: feat}}}
	m.Metadata.Name = "greeters-manifest"
	return m
}

// TestANameMappingKeyThatNamesNothingIsReported drives one name-tier key per row
// through the resolution findings, with a control whose every key names an
// instance the graph holds — on the component axis, the attachment axis keyed by
// a node, and the attachment axis keyed by a boundary.
func TestANameMappingKeyThatNamesNothingIsReported(t *testing.T) {
	t.Parallel()
	c, graph := nameMappingFixture(t)

	t.Run("every key names an instance the build holds", func(t *testing.T) {
		t.Parallel()
		m := nameMappingManifest(
			[][2]string{{"greeter-special", "eval/greeter-green:1.0.0"}},
			[][2]string{{"loud-banner", "eval/banner-loud:1.0.0"}, {"firewall", "eval/waf:1.0.0"}},
		)
		if findings := c.ResolutionFindings(m, graph); len(findings) != 0 {
			t.Fatalf("a Manifest whose every name-tier key names an instance must produce no finding; got %d: %v", len(findings), findings)
		}
		if err := c.VerifyRecipeResolution(m, graph); err != nil {
			t.Fatalf("the build's halt must stay silent on the control: %v", err)
		}
	})

	for _, tc := range []struct {
		name       string
		components [][2]string
		features   [][2]string
		want       []string
		wantNot    []string
	}{
		{
			name:     "a features.name key written as the name of the component the attachment sits on",
			features: [][2]string{{"greeter-special", "eval/banner-loud:1.0.0"}},
			want: []string{
				`Manifest "greeters-manifest", key "greeter-special" under mappings.features.name`,
				`no attachment in this build is named "greeter-special"`,
				`attachments of type "sec/banner" in this build: "banner", "loud-banner"`,
				`"greeter-special" is a component`,
				`did you mean "loud-banner"?`,
			},
			wantNot: []string{`"access-logs"`},
		},
		{
			name:     "a features.name key one edit from an attachment's name",
			features: [][2]string{{"loud-baner", "eval/banner-loud:1.0.0"}},
			want: []string{
				`key "loud-baner" under mappings.features.name`,
				`did you mean "loud-banner"?`,
			},
		},
		{
			name:     "a features.name key near no attachment's name",
			features: [][2]string{{"unrelated-key", "eval/banner-loud:1.0.0"}},
			want: []string{
				`key "unrelated-key" under mappings.features.name`,
				`attachments of type "sec/banner" in this build: "banner", "loud-banner"`,
			},
			wantNot: []string{"did you mean"},
		},
		{
			name:     "a features.name key mapped to a feature recipe the catalog does not hold",
			features: [][2]string{{"unrelated-key", "eval/absent:1.0.0"}},
			want: []string{
				`attachments in this build: "access-logs", "banner", "firewall", "loud-banner"`,
			},
		},
		{
			name:       "a components.name key one edit from an instance's name",
			components: [][2]string{{"greeter-specail", "eval/greeter-green:1.0.0"}},
			want: []string{
				`Manifest "greeters-manifest", key "greeter-specail" under mappings.components.name`,
				`no component instance in this build is named "greeter-specail"`,
				`component instances of type "app/greeter" in this build: "greeter-default", "greeter-special"`,
				`did you mean "greeter-special"?`,
			},
		},
		{
			name:       "a components.name key written as the name of an attachment",
			components: [][2]string{{"loud-banner", "eval/greeter-green:1.0.0"}},
			want: []string{
				`key "loud-banner" under mappings.components.name`,
				`"loud-banner" is the name of an attachment`,
				`did you mean the component carrying it, "greeter-special"?`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := c.ResolutionFindings(nameMappingManifest(tc.components, tc.features), graph)
			if len(findings) != 1 {
				t.Fatalf("want exactly the name-tier finding, got %d: %v", len(findings), findings)
			}
			f := findings[0]
			if !errors.Is(f, ErrNameMappingMatchedNothing()) {
				t.Fatalf("finding = %v; want ErrNameMappingMatchedNothing", f)
			}
			if errors.Is(f, ErrNoRecipeResolved()) {
				t.Errorf("a key that names nothing is not an instance that resolved to no recipe: %v", f)
			}
			for _, w := range tc.want {
				if !strings.Contains(f.Error(), w) {
					t.Errorf("finding does not carry %q:\n%s", w, f.Error())
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(f.Error(), w) {
					t.Errorf("finding carries %q, which it must not:\n%s", w, f.Error())
				}
			}
			if err := c.VerifyRecipeResolution(nameMappingManifest(tc.components, tc.features), graph); !errors.Is(err, ErrNameMappingMatchedNothing()) {
				t.Errorf("the build's halt does not carry the condition: %v", err)
			}
		})
	}

	t.Run("several keys that name nothing are each reported, in the Manifest's order", func(t *testing.T) {
		t.Parallel()
		m := nameMappingManifest(
			[][2]string{{"zz-absent", "eval/greeter-green:1.0.0"}},
			[][2]string{{"zz-second", "eval/banner-loud:1.0.0"}, {"aa-third", "eval/banner-loud:1.0.0"}},
		)
		first := c.ResolutionFindings(m, graph)
		if len(first) != 3 {
			t.Fatalf("want one finding per key that names nothing, got %d: %v", len(first), first)
		}
		for i, key := range []string{"zz-absent", "zz-second", "aa-third"} {
			if !strings.Contains(first[i].Error(), `key "`+key+`"`) {
				t.Errorf("finding %d = %q; want it about key %q", i, first[i].Error(), key)
			}
		}
		second := c.ResolutionFindings(m, graph)
		for i := range first {
			if first[i].Error() != second[i].Error() {
				t.Errorf("finding %d differs between two runs over one input:\n%s\n%s", i, first[i], second[i])
			}
		}
	})
}
