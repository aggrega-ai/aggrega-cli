// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestExecutePhaseA_SimpleInjectMapping(t *testing.T) {
	t.Parallel()
	provides := types.OrderedMap[string]{}
	provides.Set("host", "db.namespace.svc.cluster.local")
	env := types.OrderedMap[string]{}
	env.Set("DB_URL", "original")
	all := types.OrderedMap[string]{}
	all.Set("DB_URL", "original")
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("my-db", types.EnrichedEdge{Target: "db", Provides: provides, Annotations: &types.AnnotationBuckets{Release: func() types.OrderedMap[string] {
		m := types.OrderedMap[string]{}
		m.Set("namespace", "payments")
		return m

	}()}})
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  env,
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  all,
			},
			Uses: uses,
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
	env := types.OrderedMap[string]{}
	env.Set("X", "1")
	all := types.OrderedMap[string]{}
	all.Set("X", "1")

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env: env,
				All: all,
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
	locals :=

		// Base Recipe locals (already evaluated by orchestrator preamble in real flow).
		types.OrderedMap[string]{}
	locals.Set("host", "db-base.svc")
	locals.Set("port", "5432")
	features := types.OrderedMap[types.TemplateFeature]{}
	features.Set("mon", types.TemplateFeature{Type: "core/monitoring"})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{Env: types.OrderedMap[string]{}, File: types.OrderedMap[string]{}, Arg: types.OrderedMap[string]{}, All: types.OrderedMap[string]{}},
		},
		Locals:   locals,
		Features: features,
	}
	locals2 :=

		// FR with own locals — "host" collides (FR wins), "scrape_path" is new.
		// injectMapping references .locals to prove the merged map is visible.
		types.OrderedMap[string]{}
	locals2.Set("host", "db-fr.svc")
	locals2.Set("scrape_path", "/metrics")

	refs := []FeatureRecipeRef{
		{
			InstanceName: "mon",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "ops/mon"},
				Spec: types.FeatureRecipeSpec{
					Locals:        locals2,
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
