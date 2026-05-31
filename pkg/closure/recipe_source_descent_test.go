// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// fakeRecipeLoader is a static-result RecipeLoader keyed by URI.
type fakeRecipeLoader struct {
	recipes  map[string]*types.Recipe
	features map[string]*types.FeatureRecipe
}

func (f *fakeRecipeLoader) LoadRecipe(uri string) (*types.Recipe, error) {
	r, ok := f.recipes[uri]
	if !ok {
		return nil, fmt.Errorf("recipe %q not in fixture loader", uri)
	}
	return r, nil
}

func (f *fakeRecipeLoader) LoadFeatureRecipe(uri string) (*types.FeatureRecipe, error) {
	r, ok := f.features[uri]
	if !ok {
		return nil, fmt.Errorf("feature recipe %q not in fixture loader", uri)
	}
	return r, nil
}

// fakeSourceResolver is a static-result SourceResolver that returns
// a synthetic ResolvedSource from the input spec. Lets descent
// tests exercise the dedupe + dispatch logic without exercising the
// adapter pull machinery.
type fakeSourceResolver struct {
	pinTo map[string]string // SourceSpec.URL@Version → resolved PinnedVersion
}

func (f *fakeSourceResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	pinned := spec.Version
	if remap, ok := f.pinTo[spec.URL+"@"+spec.Version]; ok {
		pinned = remap
	}
	return &closure.ResolvedSource{
		Kind:           spec.Kind,
		URL:            spec.URL,
		PinnedVersion:  pinned,
		Digest:         "sha256:fake-" + pinned,
		Mediatype:      mediatypeFor(spec.Kind),
		CachePath:      "/tmp/fake-cache/" + pinned + "/blob.tgz",
		OriginalSource: spec.OriginalSource,
	}, nil
}

func mediatypeFor(kind string) string {
	switch kind {
	case "helm-oci-chart", "helm-https-chart":
		return "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	case "compose-https-fragment":
		return "application/x-yaml"
	case "terraform-module-git":
		return "application/gzip"
	default:
		return "application/octet-stream"
	}
}

func makeRecipe(uri string, source any) *types.Recipe {
	return &types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: uri, Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Provider: "helm",
			Source:   source,
		},
	}
}

func makeFeatureRecipe(uri string, source any) *types.FeatureRecipe {
	return &types.FeatureRecipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "FeatureRecipe",
		Metadata:   types.FeatureRecipeMetadata{Name: uri, Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Provider: "helm",
			Source:   source,
		},
	}
}

// usedFromClosure marks every entity in the closure as used by exactly one
// instance with empty infra. Source-descent only descends entities that have a
// using instance; a test exercising descent of an entity supplies one this way
// (the empty infra suffices for sources that read no infra). Tests that need
// per-instance infra pass their own map instead.
func usedFromClosure(cl closure.Closure) map[string][]closure.ComponentSourceInfra {
	m := map[string][]closure.ComponentSourceInfra{}
	for _, e := range cl.Entities {
		m[e.URI] = []closure.ComponentSourceInfra{{Infra: map[string]any{}}}
	}
	return m
}

func TestDescendResolvesScalarSourceShape(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/mariadb-bitnami:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			uri: makeRecipe(uri, "oci://hub.aggrega.ai/charts/mariadb"),
		},
	}
	resolver := &fakeSourceResolver{}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}

	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, resolver, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("artifacts: %d; want 1", len(got))
	}
	if got[0].Kind != "helm-oci-chart" {
		t.Errorf("Kind = %q; want helm-oci-chart (inferred from oci:// scheme)", got[0].Kind)
	}
}

func TestDescendResolvesStructuredSourceShape(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/mariadb",
		"version": "25.0.8",
	}
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if got[0].PinnedVersion != "25.0.8" {
		t.Errorf("PinnedVersion = %q", got[0].PinnedVersion)
	}
}

func TestDescendEvaluatesInfraTemplate(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/mariadb",
		"version": `{{ optional .infra "db_chart_version" "fallback" }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}

	perComp := map[string][]closure.ComponentSourceInfra{
		uri: {{Component: "db", Infra: map[string]any{"db_chart_version": "25.0.8"}}},
	}

	got, err := closure.DescendRecipeSources(perComp, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if got[0].PinnedVersion != "25.0.8" {
		t.Errorf("PinnedVersion = %q; want 25.0.8", got[0].PinnedVersion)
	}
}

func TestDescendEvaluatesLocalsOverInfra(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/mariadb",
		"version": `{{ .locals.computed }}`,
	}
	r := makeRecipe(uri, source)
	r.Spec.Locals.Set("computed", `{{ .infra.db_chart_version }}`)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	perComp := map[string][]closure.ComponentSourceInfra{
		uri: {{Component: "db", Infra: map[string]any{"db_chart_version": "26.0.0"}}},
	}

	got, err := closure.DescendRecipeSources(perComp, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if got[0].PinnedVersion != "26.0.0" {
		t.Errorf("PinnedVersion = %q; want 26.0.0", got[0].PinnedVersion)
	}
}

// TestDescendResolvesEachComponentAgainstOwnInfra pins the core
// per-component contract: one Recipe backing two components whose infra
// differs resolves to TWO source identities, each carrying that
// component's own infra value — not a single first-wins collapse to one
// version. The reference back-pointers carry the resolving component.
func TestDescendResolvesEachComponentAgainstOwnInfra(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/mariadb",
		"version": `{{ .infra.chart_version }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	perComp := map[string][]closure.ComponentSourceInfra{
		uri: {
			{Component: "db-a", Infra: map[string]any{"chart_version": "25.0.8"}},
			{Component: "db-b", Infra: map[string]any{"chart_version": "26.0.0"}},
		},
	}

	got, err := closure.DescendRecipeSources(perComp, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("artifacts: %d; want 2 (per-component infra differs — no first-wins collapse)", len(got))
	}
	byVersion := map[string]string{}
	for _, a := range got {
		if len(a.ReferencedBy) != 1 {
			t.Fatalf("artifact %s: ReferencedBy = %d; want 1", a.PinnedVersion, len(a.ReferencedBy))
		}
		byVersion[a.PinnedVersion] = a.ReferencedBy[0].Component
	}
	if byVersion["25.0.8"] != "db-a" {
		t.Errorf("version 25.0.8 resolved by %q; want db-a", byVersion["25.0.8"])
	}
	if byVersion["26.0.0"] != "db-b" {
		t.Errorf("version 26.0.0 resolved by %q; want db-b", byVersion["26.0.0"])
	}
}

// TestDescendDoesNotShareInfraAcrossComponents pins the no-bleed
// contract: a component's source template resolves against only that
// component's own infra. A template referencing an infra key the
// resolving component does not declare halts at the source-eval gate —
// it cannot silently read a sibling component's value, because there is
// no flattened Release-wide bag to read from.
func TestDescendDoesNotShareInfraAcrossComponents(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/mariadb:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/mariadb",
		"version": `{{ .infra.peer_only }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	perComp := map[string][]closure.ComponentSourceInfra{
		uri: {{Component: "db-a", Infra: map[string]any{"own_key": "x"}}},
	}

	_, err := closure.DescendRecipeSources(perComp, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime) {
		t.Errorf("a component reading a key it does not declare should halt (no cross-component bleed); got %v", err)
	}
}

// TestDescendResolvesFeatureRecipeAgainstPerInstanceInfra pins the FeatureRecipe
// arm of the per-instance contract: a FeatureRecipe source resolves against each
// attaching feature instance's own infra (the BoundaryFeature.infra the pull-side
// walk keys into perEntityInfra), not the empty-infra fallback. Two feature
// instances at different infra produce two source identities; the reference
// back-pointer carries the resolving feature instance.
func TestDescendResolvesFeatureRecipeAgainstPerInstanceInfra(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/feature-recipe/helm/tls:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/tls",
		"version": `{{ .infra.chart_version }}`,
	}
	loader := &fakeRecipeLoader{features: map[string]*types.FeatureRecipe{uri: makeFeatureRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	perEntity := map[string][]closure.ComponentSourceInfra{
		uri: {
			{Component: "web.features.tls", Infra: map[string]any{"chart_version": "1.2.3"}},
			{Component: "api.features.tls", Infra: map[string]any{"chart_version": "4.5.6"}},
		},
	}

	got, err := closure.DescendRecipeSources(perEntity, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("artifacts: %d; want 2 (per-instance FeatureRecipe infra differs — no empty-infra collapse)", len(got))
	}
	byVersion := map[string]string{}
	for _, a := range got {
		if len(a.ReferencedBy) != 1 {
			t.Fatalf("artifact %s: ReferencedBy = %d; want 1", a.PinnedVersion, len(a.ReferencedBy))
		}
		byVersion[a.PinnedVersion] = a.ReferencedBy[0].Component
	}
	if byVersion["1.2.3"] != "web.features.tls" {
		t.Errorf("version 1.2.3 resolved by %q; want web.features.tls", byVersion["1.2.3"])
	}
	if byVersion["4.5.6"] != "api.features.tls" {
		t.Errorf("version 4.5.6 resolved by %q; want api.features.tls", byVersion["4.5.6"])
	}
}

// TestDescendFeatureRecipeDoesNotShareInfraAcrossInstances pins the no-bleed
// contract for FeatureRecipe sources: a feature instance reading an infra key it
// does not declare halts at the source-eval gate — it cannot read a sibling
// instance's value, mirroring the component arm.
func TestDescendFeatureRecipeDoesNotShareInfraAcrossInstances(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/feature-recipe/helm/tls:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/tls",
		"version": `{{ .infra.peer_only }}`,
	}
	loader := &fakeRecipeLoader{features: map[string]*types.FeatureRecipe{uri: makeFeatureRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	perEntity := map[string][]closure.ComponentSourceInfra{
		uri: {{Component: "web.features.tls", Infra: map[string]any{"own_key": "x"}}},
	}

	_, err := closure.DescendRecipeSources(perEntity, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime) {
		t.Errorf("a feature instance reading a key it does not declare should halt (no cross-instance bleed); got %v", err)
	}
}

// TestDescendSkipsEntityWithNoUsingInstance pins the no-using-instance contract:
// an entity mapped in the Manifest but instantiated/attached by nothing in the
// Release (absent from perEntityInfra) is not descended. Even with an
// infra-templated source it is skipped — not false-halted for want of infra no
// instance supplies — and no spurious artifact is pinned for a source the build
// never renders.
func TestDescendSkipsEntityWithNoUsingInstance(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/unused:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://hub.aggrega.ai/charts/unused",
		"version": `{{ .infra.chart_version }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}

	// No perEntityInfra entry for uri — nothing instantiates it.
	got, err := closure.DescendRecipeSources(map[string][]closure.ComponentSourceInfra{}, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("an unused mapped entity must be skipped, not halt; got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("artifacts: %d; want 0 (no using instance — nothing to pin)", len(got))
	}
}

func TestDescendHaltsOnProvidesReference(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/x:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://r/c",
		"version": `{{ .provides.bad }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err == nil {
		t.Fatal("expected ErrSourceTemplateAtLockTime; got nil")
	}
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime) {
		t.Errorf("error should wrap ErrSourceTemplateAtLockTime; got %v", err)
	}
	if !contains(err.Error(), "spec.source.version") {
		t.Errorf("error should name the offending field; got %v", err)
	}
}

func TestDescendHaltsOnLocalsReferencingProvides(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/x:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://r/c",
		"version": `{{ .locals.bad }}`,
	}
	r := makeRecipe(uri, source)
	r.Spec.Locals.Set("bad", `{{ .provides.X }}`)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime) {
		t.Errorf(".locals→.provides chain should halt with ErrSourceTemplateAtLockTime; got %v", err)
	}
}

func TestDescendDedupesIdenticalSources(t *testing.T) {
	t.Parallel()
	src := "oci://hub.aggrega.ai/charts/mariadb"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     src,
		"version": "25.0.8",
	}
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			"hub.aggrega.ai/recipe/helm/a:1.0.0": makeRecipe("a", source),
			"hub.aggrega.ai/recipe/helm/b:1.0.0": makeRecipe("b", source),
		},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "hub.aggrega.ai/recipe/helm/a:1.0.0"},
		{URI: "hub.aggrega.ai/recipe/helm/b:1.0.0"},
	}}
	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("artifacts: %d; want 1 (dedupe by {kind, url, pinned_version})", len(got))
	}
	if len(got[0].ReferencedBy) != 2 {
		t.Errorf("ReferencedBy: %d; want 2", len(got[0].ReferencedBy))
	}
}

func TestDescendPreservesTerraformGitRequestedRef(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/terraform/vpc:1.0.0"
	source := map[string]any{
		"kind":    "terraform-module-git",
		"url":     "https://example.com/mod.git",
		"version": "v1.0.0",
		"ref":     "v1.0.0",
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	resolver := &fakeSourceResolver{
		pinTo: map[string]string{"https://example.com/mod.git@v1.0.0": "abc123def456abc123def456abc123def456abc1"},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, resolver, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if got[0].PinnedVersion != "abc123def456abc123def456abc123def456abc1" {
		t.Errorf("PinnedVersion = %q; want commit SHA", got[0].PinnedVersion)
	}
	if got[0].ReferencedBy[0].RequestedRef != "v1.0.0" {
		t.Errorf("RequestedRef = %q; want v1.0.0", got[0].ReferencedBy[0].RequestedRef)
	}
}

func TestDescendOrdersReferencedByByConsumerURI(t *testing.T) {
	t.Parallel()
	src := "oci://hub.aggrega.ai/charts/mariadb"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     src,
		"version": "25.0.8",
	}
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			"hub.aggrega.ai/recipe/helm/z:1.0.0": makeRecipe("z", source),
			"hub.aggrega.ai/recipe/helm/a:1.0.0": makeRecipe("a", source),
			"hub.aggrega.ai/recipe/helm/m:1.0.0": makeRecipe("m", source),
		},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "hub.aggrega.ai/recipe/helm/z:1.0.0"},
		{URI: "hub.aggrega.ai/recipe/helm/a:1.0.0"},
		{URI: "hub.aggrega.ai/recipe/helm/m:1.0.0"},
	}}
	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	refs := got[0].ReferencedBy
	if len(refs) != 3 {
		t.Fatalf("ReferencedBy: %d; want 3", len(refs))
	}
	for i := 1; i < len(refs); i++ {
		if refs[i-1].URI > refs[i].URI {
			t.Errorf("ReferencedBy not sorted by URI: %v", refs)
		}
	}
}

func TestDescendHaltsOnAmbiguousKind(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/x:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{
			uri: makeRecipe(uri, "https://example.com/something-without-extension"),
		},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime) {
		t.Errorf("ambiguous scalar source should halt with ErrSourceTemplateAtLockTime; got %v", err)
	}
}

func TestDescendIgnoresTier1LocalSources(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/x:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{uri: makeRecipe(uri, "./local-chart")},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("local source should not emit artifact; got %d", len(got))
	}
}

func TestDescendIgnoresStructuredLocalSources(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/x:1.0.0"
	loader := &fakeRecipeLoader{
		recipes: map[string]*types.Recipe{uri: makeRecipe(uri, map[string]any{
			"kind":    "helm-oci-chart",
			"url":     "./vendored-chart",
			"version": "1.0.0",
		})},
	}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("structured local source should not emit artifact; got %d", len(got))
	}
}

func TestDescendSkipsNonRecipeEntities(t *testing.T) {
	t.Parallel()
	loader := &fakeRecipeLoader{}
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "hub.aggrega.ai/component/core/api-server:1.0.0"},
		{URI: "hub.aggrega.ai/aggregate/example:1.0.0"},
	}}
	got, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("non-Recipe entities should not produce artifacts; got %d", len(got))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestDescendAcceptsLiteralProvidesString pins the AST-vs-textual
// distinction: a string-literal that happens to contain the text
// ".provides" must NOT trigger ErrSourceTemplateAtLockTime — only a
// real pipeline reference to .provides does.
func TestDescendAcceptsLiteralProvidesString(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/x:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://r/c",
		"version": `25.0.8-from-{{ printf "%s.provides-marker" .infra.label }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}
	perComp := map[string][]closure.ComponentSourceInfra{
		uri: {{Component: "x", Infra: map[string]any{"label": "myns"}}},
	}

	got, err := closure.DescendRecipeSources(perComp, cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if err != nil {
		t.Fatalf("DescendRecipeSources should not halt on literal .provides text; got %v", err)
	}
	if got[0].PinnedVersion != "25.0.8-from-myns.provides-marker" {
		t.Errorf("PinnedVersion = %q; want literal .provides text passed through", got[0].PinnedVersion)
	}
}

// TestDescendRejectsProvidesInsideRangeBranch pins the AST walk's
// coverage of pipeline-side .provides references nested inside
// branch nodes (range/if/with) — a textual strings.Contains would
// catch this too, but the AST walk MUST also catch it.
func TestDescendRejectsProvidesInsideRangeBranch(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/x:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://r/c",
		"version": `{{ range $k := .infra.list }}{{ $k }}{{ .provides.X }}{{ end }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime) {
		t.Errorf("AST walk should reject .provides nested in range branch; got %v", err)
	}
}

// TestDescendRejectsProvidesInsideTemplateInvocation pins the AST
// walker's coverage of *parse.TemplateNode: a sub-template
// invocation with .provides.X passed as the pipeline argument MUST
// be caught. Defense-in-depth case — source-version templates
// rarely use sub-template invocation, but the walker covers it for
// completeness.
func TestDescendRejectsProvidesInsideTemplateInvocation(t *testing.T) {
	t.Parallel()
	uri := "hub.aggrega.ai/recipe/helm/x:1.0.0"
	source := map[string]any{
		"kind":    "helm-oci-chart",
		"url":     "oci://r/c",
		"version": `{{ template "sub" .provides.X }}`,
	}
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: makeRecipe(uri, source)}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri}}}

	_, err := closure.DescendRecipeSources(usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache")
	if !errors.Is(err, closure.ErrSourceTemplateAtLockTime) {
		t.Errorf(".provides in template-invocation pipeline should halt; got %v", err)
	}
}
