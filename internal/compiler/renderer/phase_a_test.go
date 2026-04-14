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
					InjectMapping: `{"env": {"DB_URL": "{{ (index .Context.Uses "my-db").Provides.host }}"}}`,
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
