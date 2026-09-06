// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stubEntityView answers a single name for a single kind and refuses everything
// else, so a row of the table below exercises one rung of the resolution and
// proves the rest of the ladder is walked past it.
type stubEntityView struct {
	component        string
	plugin           string
	feature          string
	recipe           string
	featureRecipe    string
	aggregate        string
	schemaLibrary    string
	mappingLibrary   string
	workloadLibrary  string
	artifactsLibrary string
}

func (v stubEntityView) Component(uri string) (types.Component, bool) {
	if v.component == "" || uri != v.component {
		return types.Component{}, false
	}
	c := types.Component{}
	c.Metadata.Name = uri
	c.Metadata.Version = "1.0.0"
	return c, true
}

func (v stubEntityView) PluginByURI(uri string) (types.Plugin, bool) {
	if v.plugin == "" || uri != v.plugin {
		return types.Plugin{}, false
	}
	p := types.Plugin{}
	p.Metadata.Name = uri
	p.Metadata.PluginType = "provider"
	return p, true
}

func (v stubEntityView) Feature(uri string) (types.Feature, bool) {
	if v.feature == "" || uri != v.feature {
		return types.Feature{}, false
	}
	f := types.Feature{}
	f.Metadata.Name = uri
	return f, true
}

func (v stubEntityView) RecipeByURI(uri string) (types.Recipe, bool) {
	if v.recipe == "" || uri != v.recipe {
		return types.Recipe{}, false
	}
	r := types.Recipe{}
	r.Metadata.Name = uri
	r.Spec.Capability = "helm"
	return r, true
}

func (v stubEntityView) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	if v.featureRecipe == "" || uri != v.featureRecipe {
		return types.FeatureRecipe{}, false
	}
	fr := types.FeatureRecipe{}
	fr.Metadata.Name = uri
	fr.Spec.Capability = "compose"
	return fr, true
}

func (v stubEntityView) AggregateLookup() resolver.AggregateLookup {
	return func(name string) (resolver.ResolvedAggregate, bool) {
		if v.aggregate == "" || name != v.aggregate {
			return resolver.ResolvedAggregate{}, false
		}
		a := &types.Aggregate{}
		a.Metadata.Name = name
		return resolver.ResolvedAggregate{
			Aggregate: a,
			Path:      "catalog/aggregates/" + name + ".yaml",
			Bytes:     []byte("kind: Aggregate\n"),
		}, true
	}
}

func (v stubEntityView) SchemaLibrary(uri string) (types.SchemaLibrary, bool) {
	if v.schemaLibrary == "" || uri != v.schemaLibrary {
		return types.SchemaLibrary{}, false
	}
	l := types.SchemaLibrary{}
	l.Metadata.Name = uri
	return l, true
}

func (v stubEntityView) MappingLibrary(uri string) (types.MappingLibrary, bool) {
	if v.mappingLibrary == "" || uri != v.mappingLibrary {
		return types.MappingLibrary{}, false
	}
	l := types.MappingLibrary{}
	l.Metadata.Name = uri
	return l, true
}

func (v stubEntityView) WorkloadLibrary(uri string) (types.WorkloadLibrary, bool) {
	if v.workloadLibrary == "" || uri != v.workloadLibrary {
		return types.WorkloadLibrary{}, false
	}
	l := types.WorkloadLibrary{}
	l.Metadata.Name = uri
	return l, true
}

func (v stubEntityView) ArtifactsLibrary(uri string) (types.ArtifactsLibrary, bool) {
	if v.artifactsLibrary == "" || uri != v.artifactsLibrary {
		return types.ArtifactsLibrary{}, false
	}
	l := types.ArtifactsLibrary{}
	l.Metadata.Name = uri
	return l, true
}

// TestResolveEntityReachesEveryKind drives the resolution once per kind it
// claims to reach, so a kind dropped from the ladder fails here rather than
// going quiet on whichever surface happened to be the only one asking for it.
//
// The rows are built from the kinds the resolution itself reports, and the test
// refuses a row it has no case for — so adding a kind to the ladder without a
// case here fails rather than passing silently.
func TestResolveEntityReachesEveryKind(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		view  stubEntityView
		scope string
	}{
		"Component":        {stubEntityView{component: "core/compute/api"}, ""},
		"Plugin":           {stubEntityView{plugin: "provider/helm"}, "provider"},
		"Feature":          {stubEntityView{feature: "core/metrics"}, ""},
		"Recipe":           {stubEntityView{recipe: "helm-api"}, "helm"},
		"FeatureRecipe":    {stubEntityView{featureRecipe: "compose-metrics"}, "compose"},
		"Aggregate":        {stubEntityView{aggregate: "db-module"}, ""},
		"SchemaLibrary":    {stubEntityView{schemaLibrary: "Probes"}, ""},
		"MappingLibrary":   {stubEntityView{mappingLibrary: "BaseMappings"}, ""},
		"WorkloadLibrary":  {stubEntityView{workloadLibrary: "Workloads"}, ""},
		"ArtifactsLibrary": {stubEntityView{artifactsLibrary: "Extras"}, ""},
	}

	kinds := compilerops.EntityKinds()
	if len(kinds) == 0 {
		t.Fatal("the resolution reports no kinds at all; the census is broken, not the code")
	}
	for _, kind := range kinds {
		tc, ok := cases[kind]
		if !ok {
			t.Errorf("the resolution reaches %q and this test has no case for it; add one", kind)
			continue
		}
		name := nameFor(tc.view)
		got, found := compilerops.ResolveEntity(tc.view, name)
		if !found {
			t.Errorf("%s: %q did not resolve", kind, name)
			continue
		}
		if got.Kind != kind {
			t.Errorf("%s: resolved as kind %q", kind, got.Kind)
		}
		if got.Name != name {
			t.Errorf("%s: reported name %q, want %q", kind, got.Name, name)
		}
		if got.Scope != tc.scope {
			t.Errorf("%s: reported scope %q, want %q", kind, got.Scope, tc.scope)
		}
		if got.Definition == nil {
			t.Errorf("%s: resolved with no definition", kind)
		}
	}
	if len(cases) != len(kinds) {
		t.Errorf("this test carries %d cases for %d kinds; a case names a kind the resolution does not reach",
			len(cases), len(kinds))
	}
}

// nameFor returns the single name a stub view answers for.
func nameFor(v stubEntityView) string {
	for _, n := range []string{
		v.component, v.plugin, v.feature, v.recipe, v.featureRecipe,
		v.aggregate, v.schemaLibrary, v.mappingLibrary, v.workloadLibrary, v.artifactsLibrary,
	} {
		if n != "" {
			return n
		}
	}
	return ""
}

// TestResolveEntityRefusesAnUnmatchedName pins the refusal every by-name surface
// depends on: a name nothing matches comes back unresolved, so a caller renders a
// refusal rather than an empty entity.
func TestResolveEntityRefusesAnUnmatchedName(t *testing.T) {
	t.Parallel()

	// A positive control on the same view, so a refusal here cannot be a view that
	// answers nothing at all.
	view := stubEntityView{component: "core/compute/api"}
	if _, found := compilerops.ResolveEntity(view, "core/compute/api"); !found {
		t.Fatal("positive control: the view refused the name it declares, so the refusal below proves nothing")
	}
	got, found := compilerops.ResolveEntity(view, "core/compute/typo")
	if found {
		t.Fatalf("an unmatched name resolved, to %s %q", got.Kind, got.Name)
	}
	if got.Kind != "" || got.Definition != nil {
		t.Errorf("an unmatched name came back carrying content: %+v", got)
	}
}

// TestResolveEntityAggregateCarriesItsAuthoredFile pins the one kind whose match
// carries a source path and bytes, because the surface that renders an Aggregate
// reads the authored file rather than re-encoding the resolved entity.
func TestResolveEntityAggregateCarriesItsAuthoredFile(t *testing.T) {
	t.Parallel()

	got, found := compilerops.ResolveEntity(stubEntityView{aggregate: "db-module"}, "db-module")
	if !found {
		t.Fatal("the Aggregate did not resolve")
	}
	if got.Source == "" {
		t.Error("the Aggregate match carries no source path")
	}
	if len(got.Bytes) == 0 {
		t.Error("the Aggregate match carries no authored bytes")
	}
}

// TestEveryByNameSurfaceRoutesThroughTheSharedResolution guards the property the
// resolution exists for: the surfaces that answer a name answer it from one
// implementation.
//
// A surface that resolves a name on its own carries a second copy of the ladder,
// and two copies agree right up until one of them learns about a kind the other
// does not — at which point the same name resolves on two surfaces and is
// reported absent on the third, with nothing failing. A comment asserting that
// two sites agree cannot hold them together, because nothing reads a comment.
//
// It is a census of call sites and therefore a floor: it notices a surface that
// stopped delegating, and cannot notice one that delegates and then post-processes
// the answer into disagreement.
func TestEveryByNameSurfaceRoutesThroughTheSharedResolution(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// path -> what that surface answers a name for.
	expected := map[string]string{
		"cmd/aggrega/inspect.go":        "the command line, resolving a bare identifier",
		"internal/serve/inspect.go":     "the daemon, answering a definition request",
		"internal/mcp/tool_describe.go": "the tool protocol, answering a describe call",
	}

	const marker = "compilerops.ResolveEntity("
	found := 0
	for path, why := range expected {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Errorf("%s (%s) could not be read: %v", path, why, err)
			continue
		}
		if !strings.Contains(string(b), marker) {
			t.Errorf("%s (%s) no longer runs the shared by-name resolution.\n"+
				"A surface resolving a name on its own re-rolls the kind ladder, and two such copies "+
				"agree until one of them learns about a kind the other did not. Route it through "+
				"ResolveEntity, or drop this row and say here what answers the name instead.", path, why)
			continue
		}
		found++
	}
	// Calibration: a census that matched nothing would pass every row above for
	// the wrong reason if the marker itself were wrong.
	if found == 0 {
		t.Fatalf("no surface carries %q at all; the census is broken, not the code", marker)
	}
}

// TestEntityKindForHintTranslatesTheRecordedHints pins the translation every
// surface reporting a closure applies, and pins that an unrecorded hint reports
// nothing rather than being guessed at.
func TestEntityKindForHintTranslatesTheRecordedHints(t *testing.T) {
	t.Parallel()

	for hint, want := range map[string]string{
		"recipe":         "Recipe",
		"feature_recipe": "FeatureRecipe",
		"aggregate":      "Aggregate",
		"":               "",
		"component":      "",
	} {
		if got := compilerops.EntityKindForHint(hint); got != want {
			t.Errorf("hint %q rendered as %q, want %q", hint, got, want)
		}
	}
}
