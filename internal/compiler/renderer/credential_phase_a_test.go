// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// dotenvSecretFR is the inject-only credential FeatureRecipe shape: an
// injectMapping that delivers the secret's value into the component env, with NO
// source / valuesMapping (nothing to render). This is the real secret path
// (implements core/secret-value), NOT the app/literal-credential workaround.
func dotenvSecretFR() *types.FeatureRecipe {
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "dotenv-secret"},
		Spec: types.FeatureRecipeSpec{
			Capability:    "compose",
			Implements:    "core/secret-value:1.0.0",
			InjectMapping: "env:\n  {{ .values.key }}: \"{{ .values.origin }}\"\n",
		},
	}
}

// k8sSecretFR is the resource-generating credential FeatureRecipe shape: a
// source + valuesMapping that renders a standalone Secret manifest.
func k8sSecretFR() *types.FeatureRecipe {
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "k8s-secret-from-parameter"},
		Spec: types.FeatureRecipeSpec{
			Capability:    "helm",
			Implements:    "core/secret-value:1.0.0",
			Source:        "./charts/k8s-secret",
			ValuesMapping: types.ValuesMapping{Inline: `secretName: "{{ .secret_name }}"`},
		},
	}
}

func emptyComponentInjectCtx() *types.TemplateContext {
	return &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "db",
			Inject: types.InjectPipe{
				Env:  types.OrderedMap[string]{},
				File: types.OrderedMap[string]{},
				Arg:  types.OrderedMap[string]{},
				All:  types.OrderedMap[string]{},
			},
		},
	}
}

func secretValues(key, source string) types.OrderedMap[any] {
	v := types.OrderedMap[any]{}
	v.Set("key", key)
	v.Set("origin", source)
	return v
}

// TestExecutePhaseA_SecretScopedInjectReachesComponentPipe is the load-bearing
// secret-subsystem proof: a core/secret-value Secret attached via an inject-only credential FR
// (dotenv-secret shape) merges its value into the COMPONENT's inject pipe in
// Phase A — so a base Recipe reading .context.inject.env.<VAR> resolves it. The
// secret-scoped context supplies .values from the secret entry. This is the
// secret subsystem, not the literal-credential Feature.
func TestExecutePhaseA_SecretScopedInjectReachesComponentPipe(t *testing.T) {
	t.Parallel()
	ctx := emptyComponentInjectCtx()

	refs := []FeatureRecipeRef{
		{
			InstanceName:  "root-password",
			FeatureRecipe: dotenvSecretFR(),
			scope: &phaseAScope{
				Values: secretValues("MARIADB_ROOT_PASSWORD", "dev-root-pw-change-me"),
				Infra:  types.OrderedMap[any]{},
			},
		},
	}

	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("ExecutePhaseA: %v", err)
	}

	got, ok := ctx.Context.Inject.Env.Get("MARIADB_ROOT_PASSWORD")
	if !ok {
		t.Fatalf("MARIADB_ROOT_PASSWORD absent from component inject pipe; env keys=%v", ctx.Context.Inject.Env.Keys())
	}
	if got != "dev-root-pw-change-me" {
		t.Errorf("MARIADB_ROOT_PASSWORD = %q, want %q", got, "dev-root-pw-change-me")
	}
	// .all is rebuilt from the merged pipe.
	if v, _ := ctx.Context.Inject.All.Get("MARIADB_ROOT_PASSWORD"); v != "dev-root-pw-change-me" {
		t.Errorf("all.MARIADB_ROOT_PASSWORD = %q", v)
	}
}

// TestResolveSecretRecipes_ScopesToSecretEntryValues pins that the orchestrator
// builds a Phase A ref for each node secret, scoped to the secret entry's own
// values with empty infra.
func TestResolveSecretRecipes_ScopesToSecretEntryValues(t *testing.T) {
	t.Parallel()

	inner := types.OrderedMap[types.IRSecretEntry]{}
	inner.Set("root-password", types.IRSecretEntry{
		Type:   "core/secret-value",
		Values: secretValues("MARIADB_ROOT_PASSWORD", "dev-root-pw-change-me"),
	})
	outer := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
	outer.Set("db", inner)
	graph := &types.IRGraph{Spec: types.IRSpec{Secrets: outer}}

	lookup := RecipeLookup{
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/secret-value" {
				return dotenvSecretFR(), true
			}
			return nil, false
		},
	}

	refs := resolveSecretRecipes("db", graph, lookup)
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	if refs[0].InstanceName != "root-password" {
		t.Errorf("InstanceName = %q, want root-password", refs[0].InstanceName)
	}
	if refs[0].scope == nil {
		t.Fatal("secret ref carries no scope")
	}
	if v, _ := refs[0].scope.Values.Get("key"); v != "MARIADB_ROOT_PASSWORD" {
		t.Errorf("scope.Values.key = %v, want MARIADB_ROOT_PASSWORD", v)
	}
	if refs[0].scope.Infra.Len() != 0 {
		t.Errorf("scope.Infra should be empty, got %v", refs[0].scope.Infra.Keys())
	}
}

// TestResolveSecretRecipes_NoSecretsOrLookup covers the nil paths.
func TestResolveSecretRecipes_NoSecretsOrLookup(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	if refs := resolveSecretRecipes("db", graph, RecipeLookup{}); refs != nil {
		t.Errorf("nil FeatureRecipe lookup should yield no refs, got %d", len(refs))
	}
	lookup := RecipeLookup{FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false }}
	if refs := resolveSecretRecipes("absent", graph, lookup); refs != nil {
		t.Errorf("node with no secrets should yield no refs, got %d", len(refs))
	}
}

// TestIsResourceGeneratingFR_SecretShapes pins the predicate the Phase B.6
// credential guard relies on: inject-only secret FRs are skipped from render
// (false), resource-generating and dual-mode FRs render (true).
func TestIsResourceGeneratingFR_SecretShapes(t *testing.T) {
	t.Parallel()
	if isResourceGeneratingFR(dotenvSecretFR()) {
		t.Error("inject-only dotenv-secret FR must NOT be resource-generating (B.6 must skip it)")
	}
	if !isResourceGeneratingFR(k8sSecretFR()) {
		t.Error("k8s-secret-from-parameter FR must be resource-generating (B.6 renders it)")
	}
	dual := dotenvSecretFR()
	dual.Spec.Source = "./charts/k8s-secret"
	dual.Spec.ValuesMapping = types.ValuesMapping{Inline: `secretName: "x"`}
	if !isResourceGeneratingFR(dual) {
		t.Error("dual-mode secret FR (injectMapping + source) must be resource-generating (B.6 renders the resource)")
	}
}

// TestExecutePhaseA_DualModeSecretFRMergesInject confirms a dual-mode secret FR
// (injectMapping AND source/valuesMapping) merges its inject in Phase A. Phase A
// owns the single inject merge; the Phase B.6 path (predicate true above) renders
// the FR's auxiliary resource but never merges into the component pipe — so there
// is no double-merge.
func TestExecutePhaseA_DualModeSecretFRMergesInject(t *testing.T) {
	t.Parallel()
	ctx := emptyComponentInjectCtx()

	dual := dotenvSecretFR()
	dual.Spec.Source = "./charts/k8s-secret"
	dual.Spec.ValuesMapping = types.ValuesMapping{Inline: `secretName: "x"`}

	refs := []FeatureRecipeRef{
		{
			InstanceName:  "root-password",
			FeatureRecipe: dual,
			scope: &phaseAScope{
				Values: secretValues("MARIADB_ROOT_PASSWORD", "dev-root-pw-change-me"),
				Infra:  types.OrderedMap[any]{},
			},
		},
	}

	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("ExecutePhaseA: %v", err)
	}
	if v, _ := ctx.Context.Inject.Env.Get("MARIADB_ROOT_PASSWORD"); v != "dev-root-pw-change-me" {
		t.Errorf("dual-mode FR did not merge inject in Phase A: env=%v", ctx.Context.Inject.Env.Keys())
	}
}
