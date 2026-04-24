// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestExecutePhaseB_Simple(t *testing.T) {
	t.Parallel()
	env := types.OrderedMap[string]{}
	env.Set("DB_URL", "db.svc.cluster.local:5432")
	values2 := types.OrderedMap[interface{}]{}
	values2.Set("port", 8080)
	infra := types.OrderedMap[interface{}]{}
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
			ValuesMapping: `{"image": "myapp:latest", "port": {{ .values.port }}, "replicas": {{ .infra.replicas }}}`,
		},
	}

	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["image"] != "myapp:latest" {
		t.Errorf("image: %v", values["image"])
	}
	if values["port"] != float64(8080) {
		t.Errorf("port: %v", values["port"])
	}
	if values["replicas"] != float64(3) {
		t.Errorf("replicas: %v", values["replicas"])
	}
}

func TestExecutePhaseB_NoValuesMapping(t *testing.T) {
	t.Parallel()
	values2 := types.OrderedMap[interface{}]{}
	values2.Set("key", "value")
	ctx := &types.TemplateContext{
		Values: values2,
	}

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "passthrough"},
		Spec:     types.RecipeSpec{},
	}

	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["key"] != "value" {
		t.Errorf("expected passthrough values, got %v", values)
	}
}

func TestExecutePhaseB_JsonnetRejected(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "jsonnet-recipe"},
		Spec: types.RecipeSpec{
			ValuesMappingJsonnet: "some.jsonnet",
		},
	}

	_, err := ExecutePhaseB(ctx, recipe)
	if err == nil {
		t.Fatal("expected error for valuesMappingJsonnet")
	}
	if !errors.Is(err, ErrJsonnetNotSupported) {
		t.Errorf("expected ErrJsonnetNotSupported, got: %v", err)
	}
}

func TestExecutePhaseB_NilRecipe(t *testing.T) {
	t.Parallel()

	values, err := ExecutePhaseB(&types.TemplateContext{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("expected empty values, got %v", values)
	}
}

func TestExecutePhaseB_InvalidJSON(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{}
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "bad-json"},
		Spec: types.RecipeSpec{
			ValuesMapping: `not json at all`,
		},
	}

	_, err := ExecutePhaseB(ctx, recipe)
	if err == nil {
		t.Fatal("expected error for invalid JSON output")
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
		Values: types.OrderedMap[interface{}]{},
	}

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "inject-consumer"},
		Spec: types.RecipeSpec{
			ValuesMapping: `{"env": [{"name": "DATABASE_URL", "value": "{{ .context.inject.env.DB_URL }}"}]}`,
		},
	}

	values, err := ExecutePhaseB(ctx, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	envArr, ok := values["env"].([]interface{})
	if !ok || len(envArr) != 1 {
		t.Fatalf("expected env array with 1 entry, got %v", values["env"])
	}
	entry := envArr[0].(map[string]interface{})
	if entry["value"] != "db.svc:5432" {
		t.Errorf("env value: %v", entry["value"])
	}
}
