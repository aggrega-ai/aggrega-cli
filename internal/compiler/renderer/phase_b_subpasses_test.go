// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// minimalCtx returns a TemplateContext with the minimum non-nil substructure
// the sub-pass functions read from.
func minimalCtx() *types.TemplateContext {
	return &types.TemplateContext{
		Context: types.ContextBlock{InstanceID: "test-node"},
		Values:  types.OrderedMap[any]{},
		Infra:   types.OrderedMap[any]{},
	}
}

func minimalRecipe(name string) *types.Recipe {
	return &types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: name, Version: "1.0.0"},
		Spec:       types.RecipeSpec{Provider: "helm", Implements: "core/test:1.0.0"},
	}
}

// ----- TestPhaseB_ContextAvailabilityMatrix -----

// Verifies the per-sub-pass availability matrix per chapter 16 §5.2:
// .source is nil before B.0; populated from B.0 onward.
// .workload is nil before B.2; populated from B.2 onward (when declared).
func TestPhaseB_ContextAvailabilityMatrix(t *testing.T) {
	t.Parallel()

	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Source = "./chart"
	r.Spec.ValuesMapping = types.ValuesMapping{Inline: `{"image": "demo:1"}`}
	state := NewPhaseBState(r, "test-node", "default", "recipe.yaml")
	emptyProvides := types.OrderedMap[string]{}

	// Before B.0: .source nil, .workload nil.
	if ctx.Source != nil {
		t.Errorf("before B.0: ctx.Source must be nil, got %v", ctx.Source)
	}
	if ctx.Workload != nil {
		t.Errorf("before B.0: ctx.Workload must be nil")
	}

	// After B.0: .source populated; .workload still nil.
	if err := phaseB0(ctx, state, emptyProvides); err != nil {
		t.Fatalf("phaseB0: %v", err)
	}
	if ctx.Source == nil {
		t.Errorf("after B.0: ctx.Source must be populated")
	}
	if ctx.Workload != nil {
		t.Errorf("after B.0: ctx.Workload must still be nil")
	}
	if state.PhaseReached != 0 {
		t.Errorf("after B.0: state.PhaseReached = %d, want 0", state.PhaseReached)
	}

	// After B.1: .source still populated; .workload still nil; ResolvedValues set.
	if err := phaseB1(ctx, state); err != nil {
		t.Fatalf("phaseB1: %v", err)
	}
	if state.ResolvedValues == nil {
		t.Errorf("after B.1: state.ResolvedValues must be populated")
	}
	if ctx.Workload != nil {
		t.Errorf("after B.1: ctx.Workload must still be nil")
	}
	if state.PhaseReached != 1 {
		t.Errorf("after B.1: state.PhaseReached = %d, want 1", state.PhaseReached)
	}

	// After B.2 (no workload declared): .workload still nil — sourced-mode
	// Recipe doesn't trigger workload parsing.
	emptySlots := types.OrderedMap[types.SecretSlot]{}
	if err := phaseB2(ctx, state, emptySlots); err != nil {
		t.Fatalf("phaseB2: %v", err)
	}
	if ctx.Workload != nil {
		t.Errorf("after B.2 with no workload: ctx.Workload must remain nil")
	}
	if state.PhaseReached != 2 {
		t.Errorf("after B.2: state.PhaseReached = %d, want 2", state.PhaseReached)
	}
}

// Verifies the matrix when a workload IS declared: ctx.Workload populates
// from B.2 onward. Companion to TestPhaseB_ContextAvailabilityMatrix which
// covers the no-workload case.
func TestPhaseB_ContextAvailabilityMatrix_WorkloadPopulates(t *testing.T) {
	t.Parallel()

	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:1.0
`}
	state := NewPhaseBState(r, "test-node", "default", "recipe.yaml")
	emptyProvides := types.OrderedMap[string]{}
	emptySlots := types.OrderedMap[types.SecretSlot]{}

	if err := phaseB0(ctx, state, emptyProvides); err != nil {
		t.Fatalf("phaseB0: %v", err)
	}
	// Before B.2: ctx.Workload still nil.
	if ctx.Workload != nil {
		t.Errorf("before B.2: ctx.Workload must be nil")
	}

	if err := phaseB1(ctx, state); err != nil {
		t.Fatalf("phaseB1: %v", err)
	}
	if ctx.Workload != nil {
		t.Errorf("after B.1: ctx.Workload must still be nil")
	}

	if err := phaseB2(ctx, state, emptySlots); err != nil {
		t.Fatalf("phaseB2: %v", err)
	}
	// After B.2: ctx.Workload populates.
	if ctx.Workload == nil {
		t.Fatal("after B.2 with workload declared: ctx.Workload must be populated")
	}
	main, ok := ctx.Workload.Containers.Get("main")
	if !ok || main.Image != "app:1.0" {
		t.Errorf("ctx.Workload.Containers.main: %+v", main)
	}
}

// ----- TestPhaseB2_WorkloadValidationFlow -----

// Verifies B.2's three-stage flow: yaml.Unmarshal + ValidateWorkload +
// ValidateWorkloadSecrets. Each stage's failure mode produces a distinct
// error type.
func TestPhaseB2_WorkloadValidationFlow_ValidPasses(t *testing.T) {
	t.Parallel()
	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:1.0
`}
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	emptySlots := types.OrderedMap[types.SecretSlot]{}

	// Run prerequisites (B.0, B.1) so the template eval has a complete ctx.
	emptyProvides := types.OrderedMap[string]{}
	if err := phaseB0(ctx, state, emptyProvides); err != nil {
		t.Fatalf("phaseB0: %v", err)
	}
	if err := phaseB1(ctx, state); err != nil {
		t.Fatalf("phaseB1: %v", err)
	}

	if err := phaseB2(ctx, state, emptySlots); err != nil {
		t.Fatalf("phaseB2: %v", err)
	}
	if state.ParsedWorkload == nil {
		t.Fatal("state.ParsedWorkload must be populated")
	}
	if ctx.Workload == nil {
		t.Fatal("ctx.Workload must be populated")
	}
	main, ok := state.ParsedWorkload.Containers.Get("main")
	if !ok || main.Image != "app:1.0" {
		t.Errorf("parsed workload main container: %+v", main)
	}
}

func TestPhaseB2_WorkloadValidationFlow_BadServiceTypeFails(t *testing.T) {
	t.Parallel()
	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:1.0
services:
  api:
    type: Bogus
    ports:
      web: 8080
`}
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	emptySlots := types.OrderedMap[types.SecretSlot]{}

	emptyProvides := types.OrderedMap[string]{}
	_ = phaseB0(ctx, state, emptyProvides)
	_ = phaseB1(ctx, state)

	err := phaseB2(ctx, state, emptySlots)
	if !errors.Is(err, validator.ErrWorkloadValidation) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

func TestPhaseB2_WorkloadValidationFlow_SecretsSchemaFails(t *testing.T) {
	t.Parallel()
	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:1.0
    secrets:
      DB:
        from: undeclared-slot
        as: env
`}
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")

	// Slots: only "other" exists; the workload references "undeclared-slot".
	slots := types.OrderedMap[types.SecretSlot]{}
	slots.Set("other", types.SecretSlot{Inject: []string{"env"}})

	emptyProvides := types.OrderedMap[string]{}
	_ = phaseB0(ctx, state, emptyProvides)
	_ = phaseB1(ctx, state)

	err := phaseB2(ctx, state, slots)
	if !errors.Is(err, validator.ErrWorkloadSecretsSchema) {
		t.Fatalf("expected ErrWorkloadSecretsSchema, got %v", err)
	}
}

// ----- TestPhaseB3_NoArtifactsNoOp -----
//
// Positive cases for the artifacts evaluator live in artifacts_test.go.
// This guard verifies the no-artifacts fast path remains a clean no-op.

func TestPhaseB3_NoArtifacts_NoError(t *testing.T) {
	t.Parallel()
	r := minimalRecipe("test")
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")

	if err := phaseB3(context.Background(), minimalCtx(), state, stubEngine{}, NodeRenderContext{}); err != nil {
		t.Errorf("expected no error when no artifacts declared, got %v", err)
	}
	if state.PhaseReached != 3 {
		t.Errorf("PhaseReached after B.3 = %d, want 3", state.PhaseReached)
	}
}

// ----- TestPhaseB5_NoCollision_FeaturesUnderSubPath -----

// Verifies that B.5 collision detection is scoped to cross-origin collisions
// at boundary root, not file-name collisions across feature subtrees. Two
// features emitting a file named "deployment.yaml" under their own
// "features/<x>/" prefix do NOT collide; they have distinct full paths.
//
// Note: today's file-prefixing happens at the orchestrator (lines 276, 333);
// the EmittedFile.Name carries the full path the orchestrator has already
// prefixed. So the collision detector groups by the full Name including
// subpath — which means features/x/deployment.yaml ≠ features/y/deployment.yaml,
// and rule 7 correctly does NOT fire.
func TestPhaseB5_NoCollision_FeaturesUnderSubPath(t *testing.T) {
	t.Parallel()
	r := minimalRecipe("test")
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	state.EmittedFiles = []EmittedFile{
		{Name: "features/mon/deployment.yaml", Origin: EmitOriginSource, Body: []byte("a")},
		{Name: "features/log/deployment.yaml", Origin: EmitOriginSource, Body: []byte("b")},
	}

	if err := phaseB5(state); err != nil {
		t.Errorf("non-colliding feature subpaths should pass, got %v", err)
	}
}

// Verifies that a true cross-origin collision at boundary root produces a
// rule 7 CATALOG_ERROR.
func TestPhaseB5_CrossOriginCollision_Fails(t *testing.T) {
	t.Parallel()
	r := minimalRecipe("test")
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	state.EmittedFiles = []EmittedFile{
		{Name: "deployment.yaml", Origin: EmitOriginSource, Body: []byte("a")},
		{Name: "deployment.yaml", Origin: EmitOriginWorkload, Body: []byte("b")},
	}

	err := phaseB5(state)
	if !errors.Is(err, validator.ErrRecipeRule7) {
		t.Fatalf("expected ErrRecipeRule7, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "deployment.yaml") {
		t.Errorf("error should name the colliding file, got: %v", msg)
	}
	if !strings.Contains(msg, "source") || !strings.Contains(msg, "workload") {
		t.Errorf("error should name both contributing origins, got: %v", msg)
	}
}

// Verifies that within-origin duplication (e.g., two artifacts entries with
// the same name) is NOT caught by rule 7 — that's rule 6's domain at load
// time. B.5 only fires on cross-origin collisions.
func TestPhaseB5_WithinOriginDuplication_NoRule7(t *testing.T) {
	t.Parallel()
	r := minimalRecipe("test")
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	state.EmittedFiles = []EmittedFile{
		{Name: "config.yaml", Origin: EmitOriginArtifactsInline, Body: []byte("a")},
		{Name: "config.yaml", Origin: EmitOriginArtifactsInline, Body: []byte("b")},
	}

	if err := phaseB5(state); err != nil {
		t.Errorf("within-origin duplication is rule 6's domain at load time; rule 7 must not fire, got %v", err)
	}
}

// ----- phaseB4 dispatch -----

// Verifies phaseB4 is a no-op when no Workload was parsed.
func TestPhaseB4_NoWorkload_NoOp(t *testing.T) {
	t.Parallel()
	ctx := minimalCtx()
	r := minimalRecipe("test")
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	// state.ParsedWorkload remains nil.

	if err := phaseB4(context.Background(), ctx, state, nil, NodeRenderContext{}); err != nil {
		t.Errorf("phaseB4 with nil workload should be a no-op, got %v", err)
	}
	if len(state.EmittedFiles) != 0 {
		t.Errorf("EmittedFiles should remain empty, got %d", len(state.EmittedFiles))
	}
	if state.PhaseReached != 4 {
		t.Errorf("PhaseReached after B.4 = %d, want 4", state.PhaseReached)
	}
}

// ----- NewPhaseBState -----

func TestNewPhaseBState_FieldsPopulated(t *testing.T) {
	t.Parallel()
	r := minimalRecipe("test")
	state := NewPhaseBState(r, "node-id", "boundary-x", "/tmp/recipe.yaml")

	if state.Recipe != r {
		t.Errorf("Recipe pointer mismatch")
	}
	if state.NodeID != "node-id" {
		t.Errorf("NodeID: %q", state.NodeID)
	}
	if state.BoundaryPath != "boundary-x" {
		t.Errorf("BoundaryPath: %q", state.BoundaryPath)
	}
	if state.RecipePath != "/tmp/recipe.yaml" {
		t.Errorf("RecipePath: %q", state.RecipePath)
	}
	if state.RecipeName != "test" {
		t.Errorf("RecipeName: %q", state.RecipeName)
	}
	if state.RecipeVersion != "1.0.0" {
		t.Errorf("RecipeVersion: %q", state.RecipeVersion)
	}
	if state.PhaseReached != -1 {
		t.Errorf("PhaseReached should start at -1, got %d", state.PhaseReached)
	}
}

func TestNewPhaseBState_NilRecipe(t *testing.T) {
	t.Parallel()
	state := NewPhaseBState(nil, "node-id", "default", "")
	if state.Recipe != nil {
		t.Errorf("Recipe should be nil")
	}
	if state.RecipeName != "" {
		t.Errorf("RecipeName should be empty for nil recipe, got %q", state.RecipeName)
	}
}
