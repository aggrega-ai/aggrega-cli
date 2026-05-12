// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestEvaluateFeatureRecipeLocals_FeatureScoped(t *testing.T) {
	t.Parallel()
	locals := types.OrderedMap[string]{}
	locals.Set("scrape_target", `{{ .provides.host }}:{{ .values.scrape_port }}`)
	locals.Set("tier", `{{ .values.tier }}`)

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec: types.FeatureRecipeSpec{
			Locals: locals,
		},
	}
	// Feature-scoped values/infra (FR's own, not base Recipe's)
	values := types.OrderedMap[any]{}
	values.Set("scrape_port", float64(9090))
	values.Set("tier", "prod")
	provides := types.OrderedMap[string]{}
	provides.Set("host", "api.svc")

	got, err := EvaluateFeatureRecipeLocals(fr, "mon", values, types.OrderedMap[any]{}, provides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scrapeTarget, _ := got.Get("scrape_target")
	if scrapeTarget != "api.svc:9090" {
		t.Errorf("scrape_target: %q", scrapeTarget)
	}
	tier, _ := got.Get("tier")
	if tier != "prod" {
		t.Errorf("tier: %q", tier)
	}
}

func TestEvaluateFeatureRecipeLocals_NilFR(t *testing.T) {
	t.Parallel()
	got, err := EvaluateFeatureRecipeLocals(nil, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("expected empty, got %v", got.Keys())
	}
}

func TestEvaluateFeatureRecipeLocals_RejectsInjectRef(t *testing.T) {
	t.Parallel()
	locals := types.OrderedMap[string]{}
	locals.Set("leak", `{{ .context.inject.env.DB_URL }}`)
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "bad"},
		Spec: types.FeatureRecipeSpec{
			Locals: locals,
		},
	}
	_, err := EvaluateFeatureRecipeLocals(fr, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err == nil {
		t.Fatal("expected self-containment error")
	}
	if !strings.Contains(err.Error(), "self-contained") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestExecutePhaseBForFeature_RendersValuesMapping(t *testing.T) {
	t.Parallel()

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec: types.FeatureRecipeSpec{
			ValuesMapping: types.ValuesMapping{Inline: `{"image": "prom/mon:{{ .values.tag }}", "port": {{ .values.port }}}`},
		},
	}
	values2 := types.OrderedMap[any]{}
	values2.Set("tag", "v1")
	values2.Set("port", float64(9090))
	ctx := &types.TemplateContext{
		Values: values2,
	}

	values, err := ExecutePhaseBForFeature(ctx, fr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["image"] != "prom/mon:v1" {
		t.Errorf("image: %v", values["image"])
	}
	gotPort, ok := asNumber(values["port"])
	if !ok || gotPort != 9090 {
		t.Errorf("port: %v (%T)", values["port"], values["port"])
	}
}

func TestExecutePhaseBForFeature_NilFR(t *testing.T) {
	t.Parallel()
	got, err := ExecutePhaseBForFeature(nil, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestExecutePhaseBForFeature_EmptyMappingReturnsContextValues(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: "x"}}
	values := types.OrderedMap[any]{}
	values.Set("a", float64(1))
	ctx := &types.TemplateContext{Values: values}
	got, err := ExecutePhaseBForFeature(ctx, fr)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got["a"] != float64(1) {
		t.Errorf("expected passthrough .values, got %v", got)
	}
}

func TestExecutePhaseBForFeature_RejectsLibraryImport(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "lib"},
		Spec:     types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Import: "oci://hub.aggrega.ai/aggrega/helpers@1.0.0#/mappings/Core"}},
	}
	_, err := ExecutePhaseBForFeature(&types.TemplateContext{}, fr)
	if err == nil {
		t.Fatal("expected library-import rejection")
	}
	if !errors.Is(err, ErrLibraryImportNotYetSupported) {
		t.Errorf("expected ErrLibraryImportNotYetSupported, got: %v", err)
	}
}

func TestExecutePhaseBForFeature_InvalidYAML(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "bad"},
		Spec:     types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `not a mapping`}},
	}
	_, err := ExecutePhaseBForFeature(&types.TemplateContext{}, fr)
	if err == nil {
		t.Fatal("expected YAML parse error")
	}
	if !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestIsResourceGeneratingFR(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fr   *types.FeatureRecipe
		want bool
	}{
		{"nil", nil, false},
		{"empty spec", &types.FeatureRecipe{}, false},
		{"inject-only (only injectMapping)", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{InjectMapping: `{"env":{"X":"1"}}`},
		}, false},
		{"has valuesMapping", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"image":"x"}`}},
		}, true},
		{"has valuesMappingFile", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Import: "/tmp/v.yaml"}},
		}, true},
		{"has source string", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: "./charts/x"},
		}, true},
		{"has source structured", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: map[string]any{"registry": "oci://x"}},
		}, true},
		{"empty source string", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: ""},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isResourceGeneratingFR(c.fr); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
