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
			ValuesMapping: types.ValuesMapping{Inline: `secretName: "{{ .attachment_name }}"`},
		},
	}
}

// componentInjectCtxWithAttachment returns a component render context whose
// inject pipe is empty and whose one attachment carries the given pillars —
// the shape Phase A reads .values/.infra from for every attachment family.
func componentInjectCtxWithAttachment(instance string, values types.OrderedMap[any]) *types.TemplateContext {
	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set(instance, types.TemplateAttachment{Type: "core/secret-value", Values: values})
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
		Features: features,
	}
}

func secretValues(key, source string) types.OrderedMap[any] {
	v := types.OrderedMap[any]{}
	v.Set("key", key)
	v.Set("origin", source)
	return v
}

// TestExecutePhaseA_CredentialScopedInjectReachesComponentPipe is the
// load-bearing credential proof: a core/secret-value credential attached via an
// inject-only FeatureRecipe (dotenv-secret shape) merges its value into the
// COMPONENT's inject pipe in Phase A — so a base Recipe reading
// .context.inject.env.<VAR> resolves it. The attachment-scoped context supplies
// .values from the attachment's own pillar, through the same lookup every
// attachment family uses.
func TestExecutePhaseA_CredentialScopedInjectReachesComponentPipe(t *testing.T) {
	t.Parallel()
	ctx := componentInjectCtxWithAttachment("root-password", secretValues("MARIADB_ROOT_PASSWORD", "dev-root-pw-change-me"))

	refs := []FeatureRecipeRef{
		{
			InstanceName:  "root-password",
			FeatureRecipe: dotenvSecretFR(),
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

// TestResolveFeatureRecipes_CredentialAttachmentGetsAPhaseARef pins that the
// orchestrator builds a Phase A ref for a credential attachment through the one
// ref-resolution path every attachment uses, scoped to the attachment's own
// pillars.
func TestResolveFeatureRecipes_CredentialAttachmentGetsAPhaseARef(t *testing.T) {
	t.Parallel()

	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("root-password", types.IRFeatureEntry{
		Type:   "core/secret-value",
		Values: secretValues("MARIADB_ROOT_PASSWORD", "dev-root-pw-change-me"),
	})
	outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	outer.Set("db", inner)
	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: outer}}

	lookup := RecipeLookup{
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/secret-value" {
				return dotenvSecretFR(), true
			}
			return nil, false
		},
	}

	refs := resolveFeatureRecipes("db", map[string][]string{"db": {"root-password"}}, graph, lookup)
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	if refs[0].InstanceName != "root-password" {
		t.Errorf("InstanceName = %q, want root-password", refs[0].InstanceName)
	}
	if refs[0].FeatureRecipe == nil {
		t.Fatal("credential ref carries no FeatureRecipe")
	}
}

// TestResolveFeatureRecipes_NoAttachmentsOrLookup covers the nil paths.
func TestResolveFeatureRecipes_NoAttachmentsOrLookup(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	if refs := resolveFeatureRecipes("db", map[string][]string{"db": {"x"}}, graph, RecipeLookup{}); refs != nil {
		t.Errorf("nil FeatureRecipe lookup should yield no refs, got %d", len(refs))
	}
	lookup := RecipeLookup{FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false }}
	if refs := resolveFeatureRecipes("absent", map[string][]string{}, graph, lookup); refs != nil {
		t.Errorf("node with no attachments should yield no refs, got %d", len(refs))
	}
}

// TestIsResourceGeneratingFR_CredentialShapes pins the predicate the Phase B.6
// render sequence relies on: an inject-only credential FR is skipped from render
// (false); resource-generating and dual-mode FRs render (true).
func TestIsResourceGeneratingFR_CredentialShapes(t *testing.T) {
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

// TestExecutePhaseA_DualModeCredentialFRMergesInject confirms a dual-mode
// credential FR (injectMapping AND source/valuesMapping) merges its inject in
// Phase A. Phase A owns the single inject merge; the Phase B.6 path (predicate
// true above) renders the FR's auxiliary resource but never merges into the
// component pipe — so there is no double-merge.
func TestExecutePhaseA_DualModeCredentialFRMergesInject(t *testing.T) {
	t.Parallel()
	ctx := componentInjectCtxWithAttachment("root-password", secretValues("MARIADB_ROOT_PASSWORD", "dev-root-pw-change-me"))

	dual := dotenvSecretFR()
	dual.Spec.Source = "./charts/k8s-secret"
	dual.Spec.ValuesMapping = types.ValuesMapping{Inline: `secretName: "x"`}

	refs := []FeatureRecipeRef{
		{
			InstanceName:  "root-password",
			FeatureRecipe: dual,
		},
	}

	if err := ExecutePhaseA(ctx, refs); err != nil {
		t.Fatalf("ExecutePhaseA: %v", err)
	}
	if v, _ := ctx.Context.Inject.Env.Get("MARIADB_ROOT_PASSWORD"); v != "dev-root-pw-change-me" {
		t.Errorf("dual-mode FR did not merge inject in Phase A: env=%v", ctx.Context.Inject.Env.Keys())
	}
}
