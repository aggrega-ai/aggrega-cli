// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestEvaluateRecipeLocals_Basic(t *testing.T) {
	t.Parallel()

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "ops/db"},
		Spec: types.RecipeSpec{
			Locals: map[string]string{
				"fqdn": `{{ .provides.host }}.svc`,
				"port": `{{ .values.port }}`,
			},
		},
	}
	values := map[string]interface{}{"port": float64(5432)}
	provides := map[string]string{"host": "db"}

	got, err := EvaluateRecipeLocals(recipe, "db", values, nil, provides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["fqdn"] != "db.svc" {
		t.Errorf("fqdn: %q", got["fqdn"])
	}
	if got["port"] != "5432" {
		t.Errorf("port: %q", got["port"])
	}
}

func TestEvaluateRecipeLocals_NilRecipe(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeLocals(nil, "n", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestEvaluateRecipeLocals_EmptyLocals(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{Metadata: types.RecipeMetadata{Name: "ops/x"}}
	got, err := EvaluateRecipeLocals(recipe, "n", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestEvaluateRecipeLocals_RejectsInjectRef(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "ops/bad"},
		Spec: types.RecipeSpec{
			Locals: map[string]string{
				"leak": `{{ .context.inject.env.DB_URL }}`,
			},
		},
	}
	_, err := EvaluateRecipeLocals(recipe, "n", nil, nil, nil)
	if err == nil {
		t.Fatal("expected self-containment error, got nil")
	}
	if !strings.Contains(err.Error(), "locals must be self-contained") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestEvaluateRecipeLocals_RejectsUsesRef(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "ops/bad"},
		Spec: types.RecipeSpec{
			Locals: map[string]string{
				"leak": `{{ (index .context.uses "db").provides.host }}`,
			},
		},
	}
	_, err := EvaluateRecipeLocals(recipe, "n", nil, nil, nil)
	if err == nil {
		t.Fatal("expected self-containment error, got nil")
	}
}

func TestEvaluateRecipeLocals_Deterministic(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "ops/d"},
		Spec: types.RecipeSpec{
			Locals: map[string]string{
				"z_key": "zed",
				"a_key": "alpha",
				"m_key": "mid",
			},
		},
	}
	var first map[string]string
	for i := 0; i < 5; i++ {
		got, err := EvaluateRecipeLocals(recipe, "n", nil, nil, nil)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if i == 0 {
			first = got
			continue
		}
		for k, v := range first {
			if got[k] != v {
				t.Errorf("run %d key %q: got %q, want %q", i, k, got[k], v)
			}
		}
	}
}

func TestMergeFeatureLocals_FRWinsOnCollision(t *testing.T) {
	t.Parallel()

	base := map[string]string{"host": "db-base.svc", "port": "5432"}
	frOwn := map[string]string{"host": "db-fr.svc", "tls": "enabled"}

	merged := mergeFeatureLocals(base, frOwn)

	// FR wins on collision
	if merged["host"] != "db-fr.svc" {
		t.Errorf("host: got %q, want %q (FR should win on collision)", merged["host"], "db-fr.svc")
	}
	// Base keys not overridden are preserved
	if merged["port"] != "5432" {
		t.Errorf("port: %q", merged["port"])
	}
	// FR-only keys appear
	if merged["tls"] != "enabled" {
		t.Errorf("tls: %q", merged["tls"])
	}
	// Base is untouched
	if base["host"] != "db-base.svc" {
		t.Error("base map was mutated")
	}
}

func TestMergeFeatureLocals_NilBase(t *testing.T) {
	t.Parallel()
	merged := mergeFeatureLocals(nil, map[string]string{"k": "v"})
	if merged["k"] != "v" || len(merged) != 1 {
		t.Errorf("nil base: %v", merged)
	}
}

func TestMergeFeatureLocals_NilFROwn(t *testing.T) {
	t.Parallel()
	merged := mergeFeatureLocals(map[string]string{"k": "v"}, nil)
	if merged["k"] != "v" || len(merged) != 1 {
		t.Errorf("nil FR: %v", merged)
	}
}
