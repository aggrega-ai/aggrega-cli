// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

func newFeatureRecipe(provider string) *types.FeatureRecipe {
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: tName, Version: tVer},
		Spec:     types.FeatureRecipeSpec{Target: provider, Implements: "core/x"},
	}
}

// A pure-wiring FeatureRecipe (provider + implements only) is valid: it renders
// nothing; its effect is the uses-edge the Feature it implements carries. There
// is no at-least-one-input-kind rule for FeatureRecipes.
func TestValidateFeatureRecipe_PureWiring_Passes(t *testing.T) {
	t.Parallel()
	if err := validator.ValidateFeatureRecipe(newFeatureRecipe("helm"), tFile); err != nil {
		t.Fatalf("expected pure-wiring FeatureRecipe to pass, got %v", err)
	}
}

// An inject-only FeatureRecipe is valid.
func TestValidateFeatureRecipe_InjectOnly_Passes(t *testing.T) {
	t.Parallel()
	fr := newFeatureRecipe("helm")
	fr.Spec.InjectMapping = `{"env": {"X": "y"}}`
	if err := validator.ValidateFeatureRecipe(fr, tFile); err != nil {
		t.Fatalf("expected inject-only FeatureRecipe to pass, got %v", err)
	}
}

// Rule 1: valuesMapping requires source.
func TestValidateFeatureRecipe_Rule1_ValuesMappingWithoutSource_Fails(t *testing.T) {
	t.Parallel()
	fr := newFeatureRecipe("helm")
	fr.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "x:1"}`}
	err := validator.ValidateFeatureRecipe(fr, tFile)
	if !errors.Is(err, validator.ErrFeatureRecipeRule1) {
		t.Fatalf("expected ErrFeatureRecipeRule1, got %v", err)
	}
}

func TestValidateFeatureRecipe_Rule1_SourceWithValuesMapping_Passes(t *testing.T) {
	t.Parallel()
	fr := newFeatureRecipe("helm")
	fr.Spec.Source = "./chart"
	fr.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "x:1"}`}
	if err := validator.ValidateFeatureRecipe(fr, tFile); err != nil {
		t.Fatalf("expected source+valuesMapping FeatureRecipe to pass, got %v", err)
	}
}

// eval provider forbids render input-kind fields; injectMapping (a Phase-A
// delivery transform, not a rendering surface) remains allowed.
func TestValidateFeatureRecipe_EvalProvider_RejectsRenderKinds(t *testing.T) {
	t.Parallel()
	fr := newFeatureRecipe("eval")
	fr.Spec.Source = "./chart"
	err := validator.ValidateFeatureRecipe(fr, tFile)
	if !errors.Is(err, validator.ErrFeatureRecipeEvalProviderConstraint) {
		t.Fatalf("expected ErrFeatureRecipeEvalProviderConstraint, got %v", err)
	}
}

func TestValidateFeatureRecipe_EvalProvider_InjectOnly_Passes(t *testing.T) {
	t.Parallel()
	fr := newFeatureRecipe("eval")
	fr.Spec.InjectMapping = `{"env": {"X": "y"}}`
	if err := validator.ValidateFeatureRecipe(fr, tFile); err != nil {
		t.Fatalf("expected eval inject-only FeatureRecipe to pass, got %v", err)
	}
}

// The helm-chart provider accepts an inline artifact alongside a source chart:
// the inline body is a loose chart file placed at the chart root for the
// chart's own templates to read via .Files.Get, not a rejected authoring form.
func TestValidateFeatureRecipe_HelmChartInlineArtifacts_Passes(t *testing.T) {
	t.Parallel()
	fr := newFeatureRecipe("helm-chart")
	fr.Spec.Source = "./chart"
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("app-config.properties", types.ArtifactEntry{Inline: "service=app\n"})
	fr.Spec.Artifacts = art
	if err := validator.ValidateFeatureRecipe(fr, tFile); err != nil {
		t.Fatalf("expected helm-chart + inline artifact to load cleanly, got %v", err)
	}
}

// The supported composed helm-chart path: source + {$import:} (file-import)
// artifacts passes at load — the engine injects the imported file into the
// chart's templates/ directory. This pins that the rule rejects only inline
// artifacts, not the composed file-import path.
func TestValidateFeatureRecipe_HelmChartImportArtifacts_Passes(t *testing.T) {
	t.Parallel()
	fr := newFeatureRecipe("helm-chart")
	fr.Spec.Source = "./chart"
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("servicemonitor.yaml", types.ArtifactEntry{Import: "./artifacts/servicemonitor.yaml"})
	fr.Spec.Artifacts = art
	if err := validator.ValidateFeatureRecipe(fr, tFile); err != nil {
		t.Fatalf("expected helm-chart source + {$import:} artifacts to pass, got %v", err)
	}
}
