// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestExecutePhaseB_Simple(t *testing.T) {
	t.Parallel()
	env := types.OrderedMap[string]{}
	env.Set("DB_URL", "db.svc.cluster.local:5432")
	values2 := types.OrderedMap[any]{}
	values2.Set("port", 8080)
	infra := types.OrderedMap[any]{}
	infra.Set("replicas", 3)
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env: env,
			},
		},
		Values: values2,
		Infra:  infra,
	}

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "api-recipe"},
		Spec: types.RecipeSpec{
			ValuesMapping: types.ValuesMapping{Inline: `{"image": "myapp:latest", "port": {{ .values.port }}, "replicas": {{ .infra.replicas }}}`},
		},
	}

	values, err := ExecutePhaseB(ctx, recipe, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["image"] != "myapp:latest" {
		t.Errorf("image: %v", values["image"])
	}
	// yaml.v3 decodes integer literals as int (vs encoding/json's float64);
	// asNumber canonicalises both shapes to float64 for the comparison.
	gotPort, ok := asNumber(values["port"])
	if !ok || gotPort != 8080 {
		t.Errorf("port: %v (%T)", values["port"], values["port"])
	}
	gotReplicas, ok := asNumber(values["replicas"])
	if !ok || gotReplicas != 3 {
		t.Errorf("replicas: %v (%T)", values["replicas"], values["replicas"])
	}
}

func TestExecutePhaseB_NoValuesMapping(t *testing.T) {
	t.Parallel()
	values2 := types.OrderedMap[any]{}
	values2.Set("key", "value")
	ctx := &types.TemplateContext{
		Values: values2,
	}

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "passthrough"},
		Spec:     types.RecipeSpec{},
	}

	values, err := ExecutePhaseB(ctx, recipe, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["key"] != "value" {
		t.Errorf("expected passthrough values, got %v", values)
	}
}

func TestExecutePhaseB_ResolvesLibraryImport(t *testing.T) {
	t.Parallel()

	values := types.OrderedMap[any]{}
	values.Set("tag", "v9")
	ctx := &types.TemplateContext{Values: values}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "library-import-recipe"},
		Spec: types.RecipeSpec{
			ValuesMapping: types.ValuesMapping{Import: "Helpers#/mappings/Core"},
		},
	}
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{
			"Core": `{"image": "x:{{ .values.tag }}"}`,
		}),
	}

	got, err := ExecutePhaseB(ctx, recipe, libs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["image"] != "x:v9" {
		t.Errorf("image: %v; want the imported fragment templated against .values", got["image"])
	}
}

func TestExecutePhaseB_LibraryImportNotFound(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "library-import-recipe"},
		Spec: types.RecipeSpec{
			ValuesMapping: types.ValuesMapping{Import: "Absent#/mappings/Core"},
		},
	}

	_, err := ExecutePhaseB(ctx, recipe, nil)
	if err == nil {
		t.Fatal("expected error for an unresolved bare-name library-import valuesMapping")
	}
	if !errors.Is(err, ErrMappingLibraryNotFound) {
		t.Errorf("expected ErrMappingLibraryNotFound, got: %v", err)
	}
}

func TestExecutePhaseB_RejectsHostBearingImport(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "host-import-recipe"},
		Spec: types.RecipeSpec{
			ValuesMapping: types.ValuesMapping{Import: "oci://hub.aggrega.ai/aggrega/helpers@1.0.0#/mappings/Core"},
		},
	}

	_, err := ExecutePhaseB(ctx, recipe, nil)
	if err == nil {
		t.Fatal("expected error for host-bearing library-import valuesMapping")
	}
	if !errors.Is(err, ErrHostInLibraryImport) {
		t.Errorf("expected ErrHostInLibraryImport, got: %v", err)
	}
}

func TestExecutePhaseB_NilRecipe(t *testing.T) {
	t.Parallel()

	values, err := ExecutePhaseB(&types.TemplateContext{}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("expected empty values, got %v", values)
	}
}

func TestExecutePhaseB_InvalidYAML(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "bad-output"},
		Spec: types.RecipeSpec{
			// A bare scalar is parseable YAML but cannot decode into a
			// map[string]any; the parse-into-map step fails.
			ValuesMapping: types.ValuesMapping{Inline: `not a mapping at all`},
		},
	}

	_, err := ExecutePhaseB(ctx, recipe, nil)
	if err == nil {
		t.Fatal("expected error for non-mapping template output")
	}
}

func TestExecutePhaseB_UsesInjectPipe(t *testing.T) {
	t.Parallel()
	env := types.OrderedMap[string]{}
	env.Set("DB_URL", "db.svc:5432")

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env: env,
			},
		},
		Values: types.OrderedMap[any]{},
	}

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "inject-consumer"},
		Spec: types.RecipeSpec{
			ValuesMapping: types.ValuesMapping{Inline: `{"env": [{"name": "DATABASE_URL", "value": "{{ .context.inject.env.DB_URL }}"}]}`},
		},
	}

	values, err := ExecutePhaseB(ctx, recipe, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	envArr, ok := values["env"].([]any)
	if !ok || len(envArr) != 1 {
		t.Fatalf("expected env array with 1 entry, got %v", values["env"])
	}
	entry := envArr[0].(map[string]any)
	if entry["value"] != "db.svc:5432" {
		t.Errorf("env value: %v", entry["value"])
	}
}
