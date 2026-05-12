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

const (
	tFile = "aggrega-catalog/recipe/test/foo/recipe.yaml"
	tName = "test/foo"
	tVer  = "1.0.0"
)

func newRecipe(provider string) *types.Recipe {
	return &types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: tName, Version: tVer},
		Spec:       types.RecipeSpec{Provider: provider, Implements: "core/test:1.0.0"},
	}
}

// ----- Rule 1: valuesMapping requires source -----

func TestValidateRecipe_Rule1_ValuesMappingWithSource_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "demo:1.0"}`}
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule1_ValuesMappingWithoutSource_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "demo:1.0"}`} // no source
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeRule1) {
		t.Fatalf("expected ErrRecipeRule1, got %v", err)
	}
}

func TestValidateRecipe_Rule1_ValuesMappingFileImportWithoutSource_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.ValuesMapping = types.ValuesMapping{Import: "./values.tmpl"} // no source
	if err := validator.ValidateRecipe(r, tFile); !errors.Is(err, validator.ErrRecipeRule1) {
		t.Fatalf("expected ErrRecipeRule1, got %v", err)
	}
}

// ----- Rule 2: source requires valuesMapping -----

func TestValidateRecipe_Rule2_SourceWithValuesMapping_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "demo:1.0"}`}
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule2_SourceWithoutValuesMapping_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Source = "./chart" // no valuesMapping
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeRule2) {
		t.Fatalf("expected ErrRecipeRule2, got %v", err)
	}
}

// ----- Rule 3: workload + workloadFrom mutually exclusive -----

func TestValidateRecipe_Rule3_WorkloadOnly_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Workload = `apiVersion: aggrega.ai/v1alpha1
kind: Workload
spec: {}`
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule3_WorkloadFromOnly_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.WorkloadFrom = "./workload.yaml"
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule3_BothWorkloadFields_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Workload = `apiVersion: aggrega.ai/v1alpha1`
	r.Spec.WorkloadFrom = "./workload.yaml"
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeRule3) {
		t.Fatalf("expected ErrRecipeRule3, got %v", err)
	}
}

// ----- Rule 5: at least one input kind -----

func TestValidateRecipe_Rule5_HasSourceAndValuesMapping_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"a": 1}`}
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule5_HasWorkload_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Workload = "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule5_HasArtifactsFrom_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.ArtifactsFrom = []string{"./manifests/deploy.yaml"}
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule5_NoInputKind_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	// Bare Recipe with no source / workload / artifacts.
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeRule5) {
		t.Fatalf("expected ErrRecipeRule5, got %v", err)
	}
}

// ----- Rule 6: artifacts ↔ artifactsFrom basename collision -----

func TestValidateRecipe_Rule6_NoCollision_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Artifacts = types.OrderedMap[string]{}
	r.Spec.Artifacts.Set("config.yaml", `data: {}`)
	r.Spec.ArtifactsFrom = []string{"./manifests/deploy.yaml"}
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule6_BasenameCollision_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Artifacts = types.OrderedMap[string]{}
	r.Spec.Artifacts.Set("deploy.yaml", `kind: Deployment`)
	r.Spec.ArtifactsFrom = []string{"./manifests/deploy.yaml"}
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeRule6) {
		t.Fatalf("expected ErrRecipeRule6, got %v", err)
	}
	// Detail line names the colliding basename.
	if !strings.Contains(err.Error(), "deploy.yaml") {
		t.Errorf("error detail should name the collision: %v", err)
	}
}

func TestValidateRecipe_Rule6_OnlyOneSide_NoCheck(t *testing.T) {
	t.Parallel()
	// Inline artifacts with no artifactsFrom: inline×from class doesn't
	// apply. The Recipe still needs to be otherwise valid.
	r := newRecipe("helm")
	r.Spec.Artifacts = types.OrderedMap[string]{}
	r.Spec.Artifacts.Set("config.yaml", `data: {}`)
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule6_ArtifactsFromInternalCollision_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.ArtifactsFrom = []string{"./a/foo.yaml", "./b/foo.yaml"}
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeRule6) {
		t.Fatalf("expected ErrRecipeRule6 for within-artifactsFrom basename collision, got %v", err)
	}
	if !strings.Contains(err.Error(), "foo.yaml") {
		t.Errorf("error detail should name the colliding basename: %v", err)
	}
}

// ----- Helm-chart provider: inline artifacts forbidden -----

func TestValidateRecipe_HelmChartInlineArtifacts_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm-chart")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "app:1"}`}
	r.Spec.Artifacts = types.OrderedMap[string]{}
	r.Spec.Artifacts.Set("inline-cm.yaml", `kind: ConfigMap`)

	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeHelmChartInlineArtifacts) {
		t.Fatalf("expected ErrRecipeHelmChartInlineArtifacts, got %v", err)
	}
	if !strings.Contains(err.Error(), "inline-cm.yaml") {
		t.Errorf("error detail should name the offending inline filename, got %v", err)
	}
	if !strings.Contains(err.Error(), "artifactsFrom") {
		t.Errorf("error should recommend artifactsFrom, got %v", err)
	}
}

func TestValidateRecipe_HelmChartArtifactsFromOnly_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm-chart")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "app:1"}`}
	r.Spec.ArtifactsFrom = []string{"./manifests/crd.yaml"}

	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected helm-chart + artifactsFrom-only to pass, got %v", err)
	}
}

// ----- Eval-provider constraint -----

func TestValidateRecipe_Eval_BareProvides_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.Provides = types.OrderedMap[string]{}
	r.Spec.Provides.Set("connection_string", `{{ .infra.host }}:{{ .infra.port }}`)
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Eval_RejectsValuesMapping(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"x": 1}`}
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint) {
		t.Fatalf("expected ErrRecipeEvalProviderConstraint, got %v", err)
	}
}

func TestValidateRecipe_Eval_RejectsWorkload(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.Workload = "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint) {
		t.Fatalf("expected ErrRecipeEvalProviderConstraint, got %v", err)
	}
}

func TestValidateRecipe_Eval_RejectsArtifacts(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.ArtifactsFrom = []string{"./x.yaml"}
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint) {
		t.Fatalf("expected ErrRecipeEvalProviderConstraint, got %v", err)
	}
}

// ----- Error message format consistency -----

func TestValidateRecipe_ErrorMessage_CATALOG_ERRORPrefixedAndStructured(t *testing.T) {
	t.Parallel()
	// Use rule 5 to trip the validator (rule 1 isn't currently active on this path).
	// Rule 5's CATALOG_ERROR shape is identical to rule 1's; this test
	// asserts the format contract independent of which rule fires.
	r := newRecipe("helm")
	// Bare Recipe (no input kinds) → rule 5 fires.
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "CATALOG_ERROR: Recipe ") {
		t.Errorf("message must start with CATALOG_ERROR prefix, got: %q", msg)
	}
	if !strings.Contains(msg, tFile) {
		t.Errorf("message must contain file path: %q", msg)
	}
	if !strings.Contains(msg, tName+":"+tVer) {
		t.Errorf("message must contain name:version: %q", msg)
	}
	if !strings.Contains(msg, "rule 5") {
		t.Errorf("message must name the rule: %q", msg)
	}
	if !strings.Contains(msg, "at least one input kind") {
		t.Errorf("message must include rule description: %q", msg)
	}
}

// ----- Nil handling -----

func TestValidateRecipe_Nil_NoError(t *testing.T) {
	t.Parallel()
	if err := validator.ValidateRecipe(nil, tFile); err != nil {
		t.Errorf("nil Recipe should pass, got %v", err)
	}
}
