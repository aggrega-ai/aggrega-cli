// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestEvaluateFeatureRecipeLocals_FeatureScoped(t *testing.T) {
	t.Parallel()

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "ops/mon"},
		Spec: types.FeatureRecipeSpec{
			Locals: map[string]string{
				"scrape_target": `{{ .provides.host }}:{{ .values.scrape_port }}`,
				"tier":          `{{ .values.tier }}`,
			},
		},
	}
	// Feature-scoped values/infra (FR's own, not base Recipe's)
	values := map[string]interface{}{"scrape_port": float64(9090), "tier": "prod"}
	provides := map[string]string{"host": "api.svc"}

	got, err := EvaluateFeatureRecipeLocals(fr, "mon", values, nil, provides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["scrape_target"] != "api.svc:9090" {
		t.Errorf("scrape_target: %q", got["scrape_target"])
	}
	if got["tier"] != "prod" {
		t.Errorf("tier: %q", got["tier"])
	}
}

func TestEvaluateFeatureRecipeLocals_NilFR(t *testing.T) {
	t.Parallel()
	got, err := EvaluateFeatureRecipeLocals(nil, "n", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestEvaluateFeatureRecipeLocals_RejectsInjectRef(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "ops/bad"},
		Spec: types.FeatureRecipeSpec{
			Locals: map[string]string{
				"leak": `{{ .context.inject.env.DB_URL }}`,
			},
		},
	}
	_, err := EvaluateFeatureRecipeLocals(fr, "n", nil, nil, nil)
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
		Metadata: types.FeatureRecipeMetadata{Name: "ops/mon"},
		Spec: types.FeatureRecipeSpec{
			ValuesMapping: `{"image": "prom/mon:{{ .values.tag }}", "port": {{ .values.port }}}`,
		},
	}
	ctx := &types.TemplateContext{
		Values: map[string]interface{}{"tag": "v1", "port": float64(9090)},
	}

	values, err := ExecutePhaseBForFeature(ctx, fr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["image"] != "prom/mon:v1" {
		t.Errorf("image: %v", values["image"])
	}
	if values["port"] != float64(9090) {
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
	fr := &types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: "ops/x"}}
	ctx := &types.TemplateContext{Values: map[string]interface{}{"a": float64(1)}}
	got, err := ExecutePhaseBForFeature(ctx, fr)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got["a"] != float64(1) {
		t.Errorf("expected passthrough .values, got %v", got)
	}
}

func TestExecutePhaseBForFeature_RejectsJsonnet(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "ops/js"},
		Spec:     types.FeatureRecipeSpec{ValuesMappingJsonnet: "// jsonnet code"},
	}
	_, err := ExecutePhaseBForFeature(&types.TemplateContext{}, fr)
	if err == nil {
		t.Fatal("expected jsonnet rejection")
	}
}

func TestExecutePhaseBForFeature_InvalidJSON(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "ops/bad"},
		Spec:     types.FeatureRecipeSpec{ValuesMapping: `not json`},
	}
	_, err := ExecutePhaseBForFeature(&types.TemplateContext{}, fr)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
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
			Spec: types.FeatureRecipeSpec{ValuesMapping: `{"image":"x"}`},
		}, true},
		{"has valuesMappingFile", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{ValuesMappingFile: "/tmp/v.yaml"},
		}, true},
		{"has source string", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: "./charts/x"},
		}, true},
		{"has source structured", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: map[string]interface{}{"registry": "oci://x"}},
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
