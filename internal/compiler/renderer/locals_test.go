// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func omLocals(pairs ...string) types.OrderedMap[string] {
	m := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

func omLocalsIface(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func TestEvaluateRecipeLocals_Basic(t *testing.T) {
	t.Parallel()

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "db"},
		Spec: types.RecipeSpec{
			Locals: omLocals("fqdn", `{{ .provides.host }}.svc`, "port", `{{ .values.port }}`),
		},
	}
	values := omLocalsIface("port", float64(5432))
	provides := omLocals("host", "db")

	got, err := EvaluateRecipeLocals(recipe, "db", values, types.OrderedMap[any]{}, provides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, _ := got.Get("fqdn"); v != "db.svc" {
		t.Errorf("fqdn: %q", v)
	}
	if v, _ := got.Get("port"); v != "5432" {
		t.Errorf("port: %q", v)
	}
}

func TestEvaluateRecipeLocals_NilRecipe(t *testing.T) {
	t.Parallel()
	got, err := EvaluateRecipeLocals(nil, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("expected empty map, got %v", got.Keys())
	}
}

func TestEvaluateRecipeLocals_EmptyLocals(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{Metadata: types.RecipeMetadata{Name: "x"}}
	got, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("expected empty map, got %v", got.Keys())
	}
}

func TestEvaluateRecipeLocals_RejectsInjectRef(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "bad"},
		Spec: types.RecipeSpec{
			Locals: omLocals("leak", `{{ .context.inject.env.DB_URL }}`),
		},
	}
	_, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err == nil {
		t.Fatal("expected self-containment error, got nil")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".context.inject.env.DB_URL") {
		t.Errorf("diagnostic does not name the authored path: %s", diagnosticChainText(err))
	}
}

func TestEvaluateRecipeLocals_RejectsUsesRef(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "bad"},
		Spec: types.RecipeSpec{
			Locals: omLocals("leak", `{{ (index .context.uses "db").provides.host }}`),
		},
	}
	_, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
	if err == nil {
		t.Fatal("expected self-containment error, got nil")
	}
}

func TestEvaluateRecipeLocals_Deterministic(t *testing.T) {
	t.Parallel()
	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "d"},
		Spec: types.RecipeSpec{
			Locals: omLocals("z_key", "zed", "a_key", "alpha", "m_key", "mid"),
		},
	}
	var first types.OrderedMap[string]
	for i := 0; i < 5; i++ {
		got, err := EvaluateRecipeLocals(recipe, "n", types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if i == 0 {
			first = got
			continue
		}
		for k, v := range first.All() {
			if gotV, _ := got.Get(k); gotV != v {
				t.Errorf("run %d key %q: got %q, want %q", i, k, gotV, v)
			}
		}
	}
}

func TestMergeFeatureLocals_FRWinsOnCollision(t *testing.T) {
	t.Parallel()

	base := omLocals("host", "db-base.svc", "port", "5432")
	frOwn := omLocals("host", "db-fr.svc", "tls", "enabled")

	merged := mergeFeatureLocals(base, frOwn)

	// FR wins on collision
	if v, _ := merged.Get("host"); v != "db-fr.svc" {
		t.Errorf("host: got %q, want %q (FR should win on collision)", v, "db-fr.svc")
	}
	if v, _ := merged.Get("port"); v != "5432" {
		t.Errorf("port: %q", v)
	}
	if v, _ := merged.Get("tls"); v != "enabled" {
		t.Errorf("tls: %q", v)
	}
	// Base is untouched
	if v, _ := base.Get("host"); v != "db-base.svc" {
		t.Error("base map was mutated")
	}
}

func TestMergeFeatureLocals_NilBase(t *testing.T) {
	t.Parallel()
	merged := mergeFeatureLocals(types.OrderedMap[string]{}, omLocals("k", "v"))
	if v, _ := merged.Get("k"); v != "v" || merged.Len() != 1 {
		t.Errorf("empty base: %v", merged.Keys())
	}
}

func TestMergeFeatureLocals_NilFROwn(t *testing.T) {
	t.Parallel()
	merged := mergeFeatureLocals(omLocals("k", "v"), types.OrderedMap[string]{})
	if v, _ := merged.Get("k"); v != "v" || merged.Len() != 1 {
		t.Errorf("empty FR: %v", merged.Keys())
	}
}
