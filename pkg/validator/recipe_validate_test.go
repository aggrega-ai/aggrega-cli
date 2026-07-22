// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
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
		Spec:       types.RecipeSpec{Capability: provider, Implements: "core/test:1.0.0"},
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

// ----- Sourced Recipe with no values overlay is valid (chart defaults) -----

// A Recipe pointing at a source with no valuesMapping is legitimate — it
// renders the chart/module with its own defaults and authors no overlay. The
// compiler does not require an overlay (Invariant 1: it pipes whatever the
// source produces without policing it).
func TestValidateRecipe_SourceWithoutValuesMapping_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Source = "./chart" // no valuesMapping
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass for source-only Recipe (chart defaults), got %v", err)
	}
}

// Rule 3 is encoded structurally in WorkloadTemplate's content-shape
// dispatch: the type admits either inline content or {$import:},
// never both. No load-time rule check is needed.

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
	r.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"}
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestValidateRecipe_Rule5_HasArtifacts_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("deploy.yaml", types.ArtifactEntry{Import: "./manifests/deploy.yaml"})
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

// Rule 6 (artifact basename collision) is structurally impossible at
// load: Artifacts is a single OrderedMap keyed by output filename, and
// yaml.v3's OrderedMap unmarshal collapses duplicates last-wins. The
// Rule 6 call site stays declared in validator/recipe_validate.go for
// future collision classes (e.g., source-output basename vs artifacts
// entry names).

// ----- Helm-chart provider: inline artifacts accepted -----

// A helm-chart Recipe declaring an inline artifact alongside a source chart
// loads cleanly: the inline body is a loose chart file the provider places at
// the chart root for the chart's own templates to read via .Files.Get, not a
// rejected authoring form.
func TestValidateRecipe_HelmChartInlineArtifacts_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm-chart")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "app:1"}`}
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("app-config.properties", types.ArtifactEntry{Inline: "service=app\n"})

	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected helm-chart + inline artifact to load cleanly, got %v", err)
	}
}

func TestValidateRecipe_HelmChartArtifactsImportOnly_Passes(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm-chart")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "app:1"}`}
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("crd.yaml", types.ArtifactEntry{Import: "./manifests/crd.yaml"})

	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("expected helm-chart + import-only artifacts to pass, got %v", err)
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
	r.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"}
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint) {
		t.Fatalf("expected ErrRecipeEvalProviderConstraint, got %v", err)
	}
}

func TestValidateRecipe_Eval_RejectsArtifacts(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("x.yaml", types.ArtifactEntry{Import: "./x.yaml"})
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

// ----- outputLabels reserved-namespace rule -----

func recipeWithLabels(provider string, labels map[string]string) *types.Recipe {
	r := newRecipe(provider)
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("out.yaml", types.ArtifactEntry{Inline: "kind: ConfigMap"})
	r.Spec.OutputLabels = labels
	return r
}

func TestValidateRecipe_OutputLabels_AggregaAIReserved_Fails(t *testing.T) {
	t.Parallel()
	r := recipeWithLabels("helm", map[string]string{"aggrega.ai/foo": "bar"})
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace) {
		t.Fatalf("expected ErrRecipeOutputLabelReservedNamespace, got %v", err)
	}
	if !strings.Contains(err.Error(), "aggrega.ai/foo") {
		t.Errorf("error should name the offending key, got %q", err.Error())
	}
}

func TestValidateRecipe_OutputLabels_K8sRecommendedReserved_Fails(t *testing.T) {
	t.Parallel()
	r := recipeWithLabels("helm", map[string]string{"app.kubernetes.io/bar": "baz"})
	if err := validator.ValidateRecipe(r, tFile); !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace) {
		t.Fatalf("expected ErrRecipeOutputLabelReservedNamespace, got %v", err)
	}
}

func TestValidateRecipe_OutputLabels_NonReserved_Passes(t *testing.T) {
	t.Parallel()
	r := recipeWithLabels("helm", map[string]string{"hosting": "managed", "team": "payments"})
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("non-reserved outputLabels should pass, got %v", err)
	}
}

// The reserved-namespace rule is engine-agnostic: it fires even on an eval
// Recipe, which the eval carve-out would otherwise short-circuit past.
func TestValidateRecipe_OutputLabels_EvalReserved_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.OutputLabels = map[string]string{"aggrega.ai/x": "y"}
	if err := validator.ValidateRecipe(r, tFile); !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace) {
		t.Fatalf("expected reserved-namespace reject on eval Recipe, got %v", err)
	}
}

// The sentinel threads through the converted CompilerError, so callers that
// inspect the wrapped compiler error still match errors.Is.
func TestValidateRecipe_OutputLabels_ThreadsThroughCompilerError(t *testing.T) {
	t.Parallel()
	r := recipeWithLabels("helm", map[string]string{"aggrega.ai/foo": "bar"})
	err := validator.ValidateRecipe(r, tFile)
	var rve *validator.RecipeValidationError
	if !errors.As(err, &rve) {
		t.Fatalf("expected *RecipeValidationError, got %v", err)
	}
	if !errors.Is(rve.ToCompilerError(), validator.ErrRecipeOutputLabelReservedNamespace) {
		t.Fatalf("sentinel should thread through the converted CompilerError")
	}
}

// The first-reported collision is deterministic (keys checked in sorted order).
func TestValidateRecipe_OutputLabels_FirstReportedIsDeterministic(t *testing.T) {
	t.Parallel()
	r := recipeWithLabels("helm", map[string]string{
		"app.kubernetes.io/z": "1",
		"aggrega.ai/a":        "2",
	})
	err := validator.ValidateRecipe(r, tFile)
	if err == nil || !strings.Contains(err.Error(), "aggrega.ai/a") {
		t.Fatalf("expected the sorted-first key aggrega.ai/a reported, got %v", err)
	}
}
