// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover a Manifest name-tier key that names nothing in the
// build. The name tier is matched by exact string against the instance a lookup
// asks about, so such a key is consulted by no lookup; the two rules over the
// Manifest's mapping entries report it, naming the key, the Manifest, the names of
// that kind the build declares, and the one the author probably meant. Every row
// carries one such key against a graph whose every instance otherwise resolves, so
// a finding is about that key and nothing else. The condition is matched through
// its accessor.
//
// The rules are invoked the way the build invokes them: once per entry of the
// Manifest, presented beside the graph by the presentation the build uses, and
// converted by the conversion the build uses. What stands in for the build is the
// graph's own presentation, answered here from the graph fixture below; the build
// and its describing and emitting postures are driven for real by the pipeline's
// and the command's own tests.

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

// nameMappingGraphView answers the graph-stage facts the two rules read from the
// fixture graph and the fixture catalog, and leaves every other fact to the
// embedded nil view, so a rule reaching for a fact it does not declare reading
// panics rather than reading an empty answer.
type nameMappingGraphView struct {
	constraint.GraphView
	graph   *types.IRGraph
	catalog *Catalog
}

func (v nameMappingGraphView) FlattenedNodes() []string { return v.graph.Spec.Nodes.Keys() }

func (v nameMappingGraphView) FlattenedNodeComponentType(node string) (string, bool) {
	n, ok := v.graph.Spec.Nodes.Get(node)
	return n.Component, ok
}

func (v nameMappingGraphView) MergedAttachmentNames(node string) []string {
	merged, ok := v.graph.Spec.FeatureValuesMapping.Get(node)
	if !ok {
		return nil
	}
	return merged.Keys()
}

func (v nameMappingGraphView) AttachmentResolvedType(node, attachment string) string {
	merged, _ := v.graph.Spec.FeatureValuesMapping.Get(node)
	entry, _ := merged.Get(attachment)
	return entry.Type
}

func (v nameMappingGraphView) BoundaryAttachmentTypes() []constraint.BoundaryAttachmentType {
	var out []constraint.BoundaryAttachmentType
	for boundary, attachments := range v.graph.Spec.BoundaryFeatures.All() {
		for name, a := range attachments.All() {
			out = append(out, constraint.BoundaryAttachmentType{Boundary: boundary, Attachment: name, FeatureType: a.Type})
		}
	}
	return out
}

func (v nameMappingGraphView) MappedRecipeImplements(tier, reference string) (string, bool) {
	switch tier {
	case constraint.MappingTierComponents:
		if r, ok := v.catalog.RecipeByURI(reference); ok {
			return r.Spec.Implements, true
		}
	case constraint.MappingTierFeatures:
		if fr, ok := v.catalog.FeatureRecipeByURI(reference); ok {
			return fr.Spec.Implements, true
		}
	}
	return "", false
}

// nameMappingRuleFindings invokes the two rules over every entry of the Manifest, the
// components rule before the features rule, and converts what they raise.
func nameMappingRuleFindings(c *Catalog, m *types.Manifest, graph *types.IRGraph) []*resolver.ResolverError {
	presented := resolver.PresentManifestBesideGraph(m, nameMappingGraphView{graph: graph, catalog: c})
	document := resolver.ComposedManifestDocument
	entries := presented.ManifestRecipeMappingEntries(document)
	objections := constraint.RunOnDocumentMappingEntries(document, entries, presented, ComponentNameMappingNamesAComponent)
	objections = append(objections,
		constraint.RunOnDocumentMappingEntries(document, entries, presented, FeatureNameMappingNamesAnAttachment)...)
	return resolver.FindingsFromObjections(objections, "Manifest "+m.Metadata.Name)
}

// nameMappingManifest maps every type the fixture's graph holds through the type
// tier and writes the given name-tier entries, so the only finding a row can
// produce is about a name-tier key.
func nameMappingManifest(components, features [][2]string) *types.Manifest {
	comp := &types.MappingTier{}
	comp.Type.Set("app/greeter", "probe/greeter-blue:1.0.0")
	for _, kv := range components {
		comp.Name.Set(kv[0], kv[1])
	}
	feat := &types.MappingTier{}
	feat.Type.Set("sec/banner", "probe/banner-plain:1.0.0")
	feat.Type.Set("sec/logs", "probe/logs:1.0.0")
	feat.Type.Set("sec/waf", "probe/waf:1.0.0")
	for _, kv := range features {
		feat.Name.Set(kv[0], kv[1])
	}
	m := &types.Manifest{Spec: types.ManifestSpec{Mappings: types.ManifestMappings{Components: comp, Features: feat}}}
	m.Metadata.Name = "greeters-manifest"
	return m
}

// TestANameMappingKeyThatNamesNothingIsReported drives one name-tier key per row
// through the two rules, with a control whose every key names an instance the
// graph holds — on the component axis, the attachment axis keyed by a node, and
// the attachment axis keyed by a boundary.
func TestANameMappingKeyThatNamesNothingIsReported(t *testing.T) {
	t.Parallel()
	c, graph := nameMappingFixture(t)

	t.Run("every key names an instance the build holds", func(t *testing.T) {
		t.Parallel()
		m := nameMappingManifest(
			[][2]string{{"greeter-special", "probe/greeter-green:1.0.0"}},
			[][2]string{{"loud-banner", "probe/banner-loud:1.0.0"}, {"firewall", "probe/waf:1.0.0"}},
		)
		if findings := nameMappingRuleFindings(c, m, graph); len(findings) != 0 {
			t.Fatalf("a Manifest whose every name-tier key names an instance must produce no finding; got %d: %v", len(findings), findings)
		}
		if findings := c.ResolutionFindings(m, graph); len(findings) != 0 {
			t.Fatalf("the resolution findings must stay silent on the control: %v", findings)
		}
	})

	for _, tc := range []struct {
		name       string
		components [][2]string
		features   [][2]string
		locator    string
		want       []string
		wantNot    []string
	}{
		{
			name:     "a features.name key written as the name of the component the attachment sits on",
			features: [][2]string{{"greeter-special", "probe/banner-loud:1.0.0"}},
			locator:  ".spec.mappings.features.name.greeter-special",
			want: []string{
				`(Manifest greeters-manifest, at .spec.mappings.features.name.greeter-special, "greeter-special"`,
				`mapped to a feature recipe implementing "sec/banner"`,
				`attachments of that type in this build "banner, loud-banner"`,
				`did you mean "loud-banner")`,
				`the key is a component`,
			},
			wantNot: []string{"access-logs"},
		},
		{
			name:     "a features.name key one edit from an attachment's name",
			features: [][2]string{{"loud-baner", "probe/banner-loud:1.0.0"}},
			locator:  ".spec.mappings.features.name.loud-baner",
			want: []string{
				`"loud-baner"`,
				`did you mean "loud-banner")`,
				`correct the key, or remove the entry`,
			},
		},
		{
			name:     "a features.name key near no attachment's name",
			features: [][2]string{{"unrelated-key", "probe/banner-loud:1.0.0"}},
			locator:  ".spec.mappings.features.name.unrelated-key",
			want: []string{
				`attachments of that type in this build "banner, loud-banner"`,
			},
			wantNot: []string{"did you mean"},
		},
		{
			name:     "a features.name key mapped to a feature recipe the catalog does not hold",
			features: [][2]string{{"unrelated-key", "probe/absent:1.0.0"}},
			locator:  ".spec.mappings.features.name.unrelated-key",
			want: []string{
				`attachments in this build "access-logs, banner, firewall, loud-banner"`,
			},
			wantNot: []string{"implementing"},
		},
		{
			name:       "a components.name key one edit from an instance's name",
			components: [][2]string{{"greeter-specail", "probe/greeter-green:1.0.0"}},
			locator:    ".spec.mappings.components.name.greeter-specail",
			want: []string{
				`(Manifest greeters-manifest, at .spec.mappings.components.name.greeter-specail, "greeter-specail"`,
				`mapped to a recipe implementing "app/greeter"`,
				`component instances of that type in this build "greeter-default, greeter-special"`,
				`did you mean "greeter-special")`,
				`a components.name key names a component instance exactly as the Aggregate writes it`,
			},
		},
		{
			name:       "a components.name key written as the name of an attachment",
			components: [][2]string{{"loud-banner", "probe/greeter-green:1.0.0"}},
			locator:    ".spec.mappings.components.name.loud-banner",
			want: []string{
				`did you mean "greeter-special")`,
				`the key is the name of an attachment, which a features.name key names`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := nameMappingManifest(tc.components, tc.features)
			findings := nameMappingRuleFindings(c, m, graph)
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
			if short, ok := cerrors.ShortCodeOf(f.Err); !ok || short != "E0357" {
				t.Errorf("short code = %q (present %t), want %q", short, ok, "E0357")
			}
			if f.Field != tc.locator {
				t.Errorf("locator = %q, want %q", f.Field, tc.locator)
			}
			// The remedy is an edit to the Manifest's own mapping table, so the
			// category has to send the reader to the operator's document.
			if got := f.ToCompilerError().Category(); got != cerrors.OpsError {
				t.Errorf("category = %v, want %v", got, cerrors.OpsError)
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
			// The finding has one home: the resolution findings, which every surface
			// reads beside these rules, no longer raise it.
			for _, other := range c.ResolutionFindings(m, graph) {
				if errors.Is(other, ErrNameMappingMatchedNothing()) {
					t.Errorf("the resolution findings raise the condition a second time: %v", other)
				}
			}
		})
	}

	t.Run("several keys that name nothing are each reported, in the Manifest's order", func(t *testing.T) {
		t.Parallel()
		m := nameMappingManifest(
			[][2]string{{"zz-absent", "probe/greeter-green:1.0.0"}},
			[][2]string{{"zz-second", "probe/banner-loud:1.0.0"}, {"aa-third", "probe/banner-loud:1.0.0"}},
		)
		first := nameMappingRuleFindings(c, m, graph)
		if len(first) != 3 {
			t.Fatalf("want one finding per key that names nothing, got %d: %v", len(first), first)
		}
		for i, key := range []string{"zz-absent", "zz-second", "aa-third"} {
			if !strings.HasSuffix(first[i].Field, "."+key) {
				t.Errorf("finding %d is at %q; want it about key %q", i, first[i].Field, key)
			}
		}
		second := nameMappingRuleFindings(c, m, graph)
		for i := range first {
			if first[i].Error() != second[i].Error() {
				t.Errorf("finding %d differs between two runs over one input:\n%s\n%s", i, first[i], second[i])
			}
		}
	})
}
