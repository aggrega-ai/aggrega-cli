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
				Env:  map[string]string{"DB_URL": "original"},
				File: map[string]string{},
				Arg:  map[string]string{},
				All:  map[string]string{"DB_URL": "original"},
			},
			Uses: map[string]types.EnrichedEdge{
				"my-db": {
					Target:   "db",
					Provides: map[string]string{"host": "db.namespace.svc.cluster.local"},
					Annotations: &types.AnnotationBuckets{
						Release: map[string]string{"namespace": "payments"},
					},
				},
			},
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
	if ctx.Context.Inject.Env["DB_URL"] != "db.namespace.svc.cluster.local" {
		t.Errorf("DB_URL = %q, want %q", ctx.Context.Inject.Env["DB_URL"], "db.namespace.svc.cluster.local")
	}
	// .all should be rebuilt
	if ctx.Context.Inject.All["DB_URL"] != "db.namespace.svc.cluster.local" {
		t.Errorf("all.DB_URL = %q", ctx.Context.Inject.All["DB_URL"])
	}
}

func TestExecutePhaseA_NoInjectMapping(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env: map[string]string{"X": "1"},
				All: map[string]string{"X": "1"},
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
	if ctx.Context.Inject.Env["X"] != "1" {
		t.Error("inject should be unchanged")
	}
}

func TestExecutePhaseA_LastWins(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{
				Env:  map[string]string{},
				File: map[string]string{},
				Arg:  map[string]string{},
				All:  map[string]string{},
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
	if ctx.Context.Inject.Env["HOST"] != "second-value" {
		t.Errorf("HOST = %q, want 'second-value' (last wins)", ctx.Context.Inject.Env["HOST"])
	}
}

func TestExecutePhaseA_FeatureLocalsOverlay(t *testing.T) {
	t.Parallel()

	// Base Recipe locals (already evaluated by orchestrator preamble in real flow).
	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{Env: map[string]string{}, File: map[string]string{}, Arg: map[string]string{}, All: map[string]string{}},
		},
		Locals:   map[string]string{"host": "db-base.svc", "port": "5432"},
		Features: map[string]types.TemplateFeature{"mon": {Type: "core/monitoring"}},
	}

	// FR with own locals — "host" collides (FR wins), "scrape_path" is new.
	// injectMapping references .locals to prove the merged map is visible.
	refs := []FeatureRecipeRef{
		{
			InstanceName: "mon",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "ops/mon"},
				Spec: types.FeatureRecipeSpec{
					Locals: map[string]string{
						"host":         "db-fr.svc",  // collision — FR should win
						"scrape_path": "/metrics",
					},
					InjectMapping: `{"env": {"SCRAPE_HOST": "{{ .locals.host }}", "SCRAPE_PATH": "{{ .locals.scrape_path }}"}}`,
				},
			},
		},
	}

	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// FR's own "host" overrides base
	if got := ctx.Context.Inject.Env["SCRAPE_HOST"]; got != "db-fr.svc" {
		t.Errorf("SCRAPE_HOST: got %q, want %q (FR locals should override base)", got, "db-fr.svc")
	}
	// FR-only key "scrape_path" visible
	if got := ctx.Context.Inject.Env["SCRAPE_PATH"]; got != "/metrics" {
		t.Errorf("SCRAPE_PATH: got %q, want %q", got, "/metrics")
	}
	// Parent ctx.Locals untouched (shallow-copy contract)
	if ctx.Locals["host"] != "db-base.svc" {
		t.Errorf("parent ctx.Locals was mutated: got %q", ctx.Locals["host"])
	}
	if _, exists := ctx.Locals["scrape_path"]; exists {
		t.Error("FR-only local leaked into parent ctx")
	}
}

func TestExecutePhaseA_NilRecipe(t *testing.T) {
	t.Parallel()

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			Inject: types.InjectPipe{Env: map[string]string{}},
		},
	}

	err := ExecutePhaseA(ctx, []FeatureRecipeRef{{InstanceName: "nil", FeatureRecipe: nil}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
