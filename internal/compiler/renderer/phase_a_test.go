// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
				Metadata: types.FeatureRecipeMetadata{Name: "mon"},
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

// TestExecutePhaseA_CustomMechanismFlowsToAdditional pins the user-trap-
// closure invariant at the rendering layer. A Developer authoring a custom
// mechanism (ssm_param) on the Component-level inject block sees the data
// preserved end-to-end when the matched FR's injectMapping template produces
// a corresponding top-level key.
func TestExecutePhaseA_CustomMechanismFlowsToAdditional(t *testing.T) {
	t.Parallel()

	provides := types.OrderedMap[string]{}
	provides.Set("password", "secret-arn-1234")
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("my-secret", types.EnrichedEdge{Target: "vault", Provides: provides})

	authoredInject := &types.InjectBlock{}
	authoredInject.Additional.Set("ssm_param", map[string]any{"DB_PASSWORD": "password"})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
			Uses: uses,
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "secret-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "aws-ssm-resolver"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"ssm_param": {"DB_PASSWORD": "{{ (index .context.uses "my-secret").provides.password }}"}}`,
				},
			},
		},
	}

	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ssm, ok := ctx.Context.Inject.Additional.Get("ssm_param")
	if !ok {
		t.Fatalf("expected ssm_param in Additional, got keys=%v", ctx.Context.Inject.Additional.Keys())
	}
	dbPwd, _ := ssm.Get("DB_PASSWORD")
	if dbPwd != "secret-arn-1234" {
		t.Errorf("ssm_param.DB_PASSWORD = %q, want %q", dbPwd, "secret-arn-1234")
	}
}

// TestExecutePhaseA_UnmappedCustomMechanismHalts pins the dangling-reference
// invariant: a Developer authors a custom mechanism (ssm_param) on the
// Component-level inject block, but the matched FR's injectMapping template
// produces only env keys (no ssm_param top-level key). The Phase A consumer
// halts with ErrUnmappedCustomMechanism rather than silently dropping the
// mechanism into the flat-map fallback.
func TestExecutePhaseA_UnmappedCustomMechanismHalts(t *testing.T) {
	t.Parallel()

	authoredInject := &types.InjectBlock{}
	authoredInject.Additional.Set("ssm_param", map[string]any{"DB_PASSWORD": "password"})

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "wrong-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "env-only-resolver"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"env": {"OTHER_VAR": "value"}}`,
				},
			},
		},
	}

	err := ExecutePhaseA(ctx, refs)
	if err == nil {
		t.Fatalf("expected ErrUnmappedCustomMechanism, got nil")
	}
	if !errors.Is(err, ErrUnmappedCustomMechanism) {
		t.Errorf("expected errors.Is(err, ErrUnmappedCustomMechanism), got: %v", err)
	}
	// Walk the error chain — cerrors.Wrap masks the inner-error text in its
	// top-level Error() representation, so check the unwrap chain for the
	// authored-mechanism name.
	foundMech := false
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), "ssm_param") {
			foundMech = true
			break
		}
	}
	if !foundMech {
		t.Errorf("error chain should name the unmapped mechanism ssm_param: %v", err)
	}
}

// TestExecutePhaseA_FlatMapFallbackUnchanged pins the legitimate flat-output
// idiom. A Developer authors only inject.env; the matched FR's injectMapping
// template produces a flat env-vars dictionary (no top-level env key); the
// flat-map fallback handles it without firing the dangling-reference halt.
func TestExecutePhaseA_FlatMapFallbackUnchanged(t *testing.T) {
	t.Parallel()

	authoredInject := &types.InjectBlock{
		Env: map[string]any{"DB_URL": "host"},
	}

	provides := types.OrderedMap[string]{}
	provides.Set("host", "db.svc.cluster.local")
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("my-db", types.EnrichedEdge{Target: "db", Provides: provides})

	env := types.OrderedMap[string]{}
	env.Set("DB_URL", "original")

	ctx := &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Inject: types.InjectPipe{
				Env:  env,
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
			Uses: uses,
		},
		InjectRaw: authoredInject,
	}

	refs := []FeatureRecipeRef{
		{
			InstanceName: "flat-resolver",
			FeatureRecipe: &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "k8s-flat"},
				Spec: types.FeatureRecipeSpec{
					InjectMapping: `{"DB_URL": "{{ (index .context.uses "my-db").provides.host }}"}`,
				},
			},
		},
	}

	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("unexpected error from flat-map fallback path: %v", err)
	}
	dbURL, _ := ctx.Context.Inject.Env.Get("DB_URL")
	if dbURL != "db.svc.cluster.local" {
		t.Errorf("DB_URL = %q, want %q", dbURL, "db.svc.cluster.local")
	}
}
