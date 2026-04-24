// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestExecutePhaseA_SimpleInjectMapping(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("DB_URL", "original"); return _m }(),
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("DB_URL", "original"); return _m }(),
			},
			Uses: func() types.OrderedMap[types.EnrichedEdge] { _m := types.OrderedMap[types.EnrichedEdge]{}; _m.Set("my-db", types.EnrichedEdge{
					Target:   "db",
					Provides: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("host", "db.namespace.svc.cluster.local"); return _m }(),
					Annotations: &types.AnnotationBuckets{
						Release: func() types.OrderedMap[string] {
							m := types.OrderedMap[string]{}
							m.Set("namespace", "payments")
							return m
						}(),
					},
				}); return _m }(),
		},
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "dns-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "k8s-dns-resolver"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"env": {"DB_URL": "{{ (index .context.uses "my-db").provides.host }}"}}`,
				},
			},
		},
	}

	err := ExecutePhaseA(ctx, refs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DB_URL should be overwritten by the injectMapping
	dbURL, _ := ctx.Context.Inject.Env.Get("DB_URL")
	if dbURL != "db.namespace.svc.cluster.local" {
		t.Errorf("DB_URL = %q, want %q", dbURL, "db.namespace.svc.cluster.local")
	}
	// .all should be rebuilt
	allDBURL, _ := ctx.Context.Inject.All.Get("DB_URL")
	if allDBURL != "db.namespace.svc.cluster.local" {
		t.Errorf("all.DB_URL = %q", allDBURL)
	}
}

func TestExecutePhaseA_NoInjectMapping(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("X", "1"); return _m }(),
				All: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("X", "1"); return _m }(),
			},
		},
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "empty",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "no-inject"},
				Spec:     types.FeatureRecipeSpec{},
			},
		},
	}

	err := ExecutePhaseA(ctx, refs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, _ := ctx.Context.Inject.Env.Get("X"); v != "1" {
		t.Error("inject should be unchanged")
	}
}

func TestExecutePhaseA_LastWins(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "first",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "first-recipe"},
				Spec:     types.FeatureRecipeSpec{InjectMapping: `{"env": {"HOST": "first-value"}}`},
			},
		},
		{
			InstanceName: "second",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "second-recipe"},
				Spec:     types.FeatureRecipeSpec{InjectMapping: `{"env": {"HOST": "second-value"}}`},
			},
		},
	}

	err := ExecutePhaseA(ctx, refs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	host, _ := ctx.Context.Inject.Env.Get("HOST")
	if host != "second-value" {
		t.Errorf("HOST = %q, want 'second-value' (last wins)", host)
	}
}

func TestExecutePhaseA_FeatureLocalsOverlay(t *testing.T) {
	t.Parallel()

	// Base Recipe locals (already evaluated by orchestrator preamble in real flow).
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{Env: types.OrderedMap[string]{}, File: types.OrderedMap[string]{}, Arg: types.OrderedMap[string]{}, All: types.OrderedMap[string]{}},
		},
		Locals:   func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("host", "db-base.svc"); _m.Set("port", "5432"); return _m }(),
		Features: func() types.OrderedMap[types.TemplateFeature] { _m := types.OrderedMap[types.TemplateFeature]{}; _m.Set("mon", types.TemplateFeature{Type: "core/monitoring"}); return _m }(),
	}

	// FR with own locals — "host" collides (FR wins), "scrape_path" is new.
	// injectMapping references .locals to prove the merged map is visible.
	refs := []FeatureRecipeRef{
		{
			InstanceName: "mon",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "ops/mon"},
				Spec: types.FeatureRecipeSpec{
					Locals: func() types.OrderedMap[string] { _m := types.OrderedMap[string]{}; _m.Set("host", "db-fr.svc"); _m.Set("scrape_path", "/metrics"); return _m }(),
					InjectMapping: `{"env": {"SCRAPE_HOST": "{{ .locals.host }}", "SCRAPE_PATH": "{{ .locals.scrape_path }}"}}`,
				},
			},
		},
	}

	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// FR's own "host" overrides base
	if got, _ := ctx.Context.Inject.Env.Get("SCRAPE_HOST"); got != "db-fr.svc" {
		t.Errorf("SCRAPE_HOST: got %q, want %q (FR locals should override base)", got, "db-fr.svc")
	}
	// FR-only key "scrape_path" visible
	if got, _ := ctx.Context.Inject.Env.Get("SCRAPE_PATH"); got != "/metrics" {
		t.Errorf("SCRAPE_PATH: got %q, want %q", got, "/metrics")
	}
	// Parent ctx.Locals untouched (shallow-copy contract)
	if got, _ := ctx.Locals.Get("host"); got != "db-base.svc" {
		t.Errorf("parent ctx.Locals was mutated: got %q", got)
	}
	if _, exists := ctx.Locals.Get("scrape_path"); exists {
		t.Error("FR-only local leaked into parent ctx")
	}
}

func TestExecutePhaseA_NilRecipe(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{Env: types.OrderedMap[string]{}},
		},
	}

	err := ExecutePhaseA(ctx, []FeatureRecipeRef{{InstanceName: "nil", FeatureRecipe: nil}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
