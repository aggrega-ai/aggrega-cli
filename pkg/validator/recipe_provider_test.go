// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
)

func providerWithSchema(name, schema string) *types.Provider {
	return &types.Provider{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Provider",
		Metadata:   types.ProviderMetadata{Name: name, Version: "1.0.0"},
		Spec: types.ProviderSpec{
			Plugin:         "hub.aggrega.ai/providers/" + name + "-renderer:1.0.0",
			WorkloadSchema: schema,
		},
	}
}

func providerWithoutSchema(name string) *types.Provider {
	return &types.Provider{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Provider",
		Metadata:   types.ProviderMetadata{Name: name, Version: "1.0.0"},
		Spec: types.ProviderSpec{
			Plugin: "hub.aggrega.ai/providers/" + name + "-renderer:1.0.0",
		},
	}
}

// ----- Rule 4: workload requires provider workloadSchema -----

func TestValidateRecipeProviderBinding_NoWorkload_Passes(t *testing.T) {
	t.Parallel()
	// Recipe with sourced-mode only (no workload) — rule 4 doesn't apply
	// regardless of provider's workloadSchema state.
	r := newRecipe("helm")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = `{"x": 1}`
	p := providerWithoutSchema("helm")
	if err := validator.ValidateRecipeProviderBinding(r, p, tFile); err != nil {
		t.Fatalf("expected pass for sourced-mode Recipe, got %v", err)
	}
}

func TestValidateRecipeProviderBinding_WorkloadWithSchema_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Workload = "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"
	p := providerWithSchema("helm", "Workload → Deployment + Services + PVCs")
	if err := validator.ValidateRecipeProviderBinding(r, p, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipeProviderBinding_WorkloadFromWithSchema_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.WorkloadFrom = "./workload.yaml"
	p := providerWithSchema("helm", "Workload → ...")
	if err := validator.ValidateRecipeProviderBinding(r, p, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipeProviderBinding_WorkloadWithoutSchema_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Workload = "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"
	p := providerWithoutSchema("helm")
	err := validator.ValidateRecipeProviderBinding(r, p, tFile)
	if !errors.Is(err, validator.ErrRecipeRule4) {
		t.Fatalf("expected ErrRecipeRule4, got %v", err)
	}
	if !strings.Contains(err.Error(), "helm") {
		t.Errorf("message must name the provider: %v", err)
	}
}

func TestValidateRecipeProviderBinding_NilProvider_FailsForWorkload(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.WorkloadFrom = "./workload.yaml"
	err := validator.ValidateRecipeProviderBinding(r, nil, tFile)
	if !errors.Is(err, validator.ErrRecipeRule4) {
		t.Fatalf("expected ErrRecipeRule4 for nil provider with workload Recipe, got %v", err)
	}
}

func TestValidateRecipeProviderBinding_NilRecipe_NoError(t *testing.T) {
	t.Parallel()
	p := providerWithoutSchema("helm")
	if err := validator.ValidateRecipeProviderBinding(nil, p, tFile); err != nil {
		t.Errorf("nil Recipe should pass, got %v", err)
	}
}
