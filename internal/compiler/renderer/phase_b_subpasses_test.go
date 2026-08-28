// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
		Spec:       types.RecipeSpec{Capability: "helm", Implements: "core/test:1.0.0"},
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
	emptyLocals := types.OrderedMap[string]{}

	// Before B.0: .source nil, .workload nil.
	if ctx.Source != nil {
		t.Errorf("before B.0: ctx.Source must be nil, got %v", ctx.Source)
	}
	if ctx.Workload != nil {
		t.Errorf("before B.0: ctx.Workload must be nil")
	}

	// After B.0: .source populated; .workload still nil.
	if err := phaseB0(ctx, state, emptyLocals); err != nil {
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
	emptySlots := types.OrderedMap[types.AttachmentSlot]{}
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
	emptyLocals := types.OrderedMap[string]{}
	emptySlots := types.OrderedMap[types.AttachmentSlot]{}

	if err := phaseB0(ctx, state, emptyLocals); err != nil {
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
// ValidateWorkloadAttachments. Each stage's failure mode produces a distinct
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
	emptySlots := types.OrderedMap[types.AttachmentSlot]{}

	// Run prerequisites (B.0, B.1) so the template eval has a complete ctx.
	emptyLocals := types.OrderedMap[string]{}
	if err := phaseB0(ctx, state, emptyLocals); err != nil {
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
	emptySlots := types.OrderedMap[types.AttachmentSlot]{}

	emptyLocals := types.OrderedMap[string]{}
	_ = phaseB0(ctx, state, emptyLocals)
	_ = phaseB1(ctx, state)

	err := phaseB2(ctx, state, emptySlots)
	if !errors.Is(err, validator.ErrWorkloadValidation()) {
		t.Fatalf("expected ErrWorkloadValidation, got %v", err)
	}
}

// TestPhaseB2_WorkloadTemplateStrayTopLevelKeyFails pins that the rendered
// workload document is checked as the bytes the template produced. The Workload
// schema closes the document to its declared top-level keys, so a stray or
// misspelled one is a violation — but a decode into the typed model discards
// any key the model does not name, which makes the violation unobservable from
// the decoded value alone. The stray key here is the whole point of the test:
// remove it and nothing distinguishes this case from a valid workload.
func TestPhaseB2_WorkloadTemplateStrayTopLevelKeyFails(t *testing.T) {
	t.Parallel()
	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
replicaz: 3
containers:
  main:
    image: app:1.0
`}
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	emptySlots := types.OrderedMap[types.AttachmentSlot]{}

	emptyLocals := types.OrderedMap[string]{}
	_ = phaseB0(ctx, state, emptyLocals)
	_ = phaseB1(ctx, state)

	err := phaseB2(ctx, state, emptySlots)
	if !errors.Is(err, validator.ErrWorkloadValidation()) {
		t.Fatalf("expected ErrWorkloadValidation for a stray top-level key, got %v", err)
	}
	if !strings.Contains(err.Error(), "replicaz") {
		t.Errorf("error must name the offending key; got %v", err)
	}
}

// TestPhaseB2_WorkloadTemplateEmptyRequiredValueFails pins the complementary
// direction: a value the document carries as an explicit empty string satisfies
// the schema in the document and vanishes when the document is decoded into the
// typed model, where the schema then reports it missing. So the render path runs
// both checks deliberately — neither one subsumes the other, and deleting
// either loses a rejection class the remaining one cannot see.
func TestPhaseB2_WorkloadTemplateEmptyRequiredValueFails(t *testing.T) {
	t.Parallel()
	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: ""
`}
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")
	emptySlots := types.OrderedMap[types.AttachmentSlot]{}

	emptyLocals := types.OrderedMap[string]{}
	_ = phaseB0(ctx, state, emptyLocals)
	_ = phaseB1(ctx, state)

	err := phaseB2(ctx, state, emptySlots)
	if !errors.Is(err, validator.ErrWorkloadValidation()) {
		t.Fatalf("expected ErrWorkloadValidation for an empty required value, got %v", err)
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error must name the missing property; got %v", err)
	}
}

func TestPhaseB2_WorkloadValidationFlow_FeaturesSchemaFails(t *testing.T) {
	t.Parallel()
	ctx := minimalCtx()
	r := minimalRecipe("test")
	r.Spec.Workload = types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:1.0
    attachments:
      DB:
        from: undeclared-slot
        as: env
`}
	state := NewPhaseBState(r, "test", "default", "recipe.yaml")

	// Slots: only "other" exists; the workload references "undeclared-slot".
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("other", types.AttachmentSlot{Inject: []string{"env"}})

	emptyLocals := types.OrderedMap[string]{}
	_ = phaseB0(ctx, state, emptyLocals)
	_ = phaseB1(ctx, state)

	err := phaseB2(ctx, state, slots)
	if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
		t.Fatalf("expected ErrWorkloadFeaturesSchema, got %v", err)
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

// ----- the rule over a node's own emitted names -----

// nodeEmissionVerdict drives the rule declared over one node's own emission across
// the emission it is handed, and returns the render's halt.
//
// It records the emission into a real retention, presents it through the real
// presentation, and asks the rule through the framework's own traversal over the
// real coordinate axis — so what these cases exercise is the production path rather
// than the predicate in isolation. The caller-presentation flag is true, because
// these cases are about what the rule decides rather than about the arrangement that
// would make it skip.
func nodeEmissionVerdict(t *testing.T, files []EmittedFile) error {
	t.Helper()
	state := NewPhaseBState(minimalRecipe("test"), "test", "default", "recipe.yaml")
	state.EmittedFiles = files

	retention := newEmittedProjections()
	retention.recordNode(state)
	view := newEmittedView(stubHydratedForEmitted{}, retention)
	if view == nil {
		t.Fatal("no presentation was built over the retention, so a verdict from it would certify nothing")
	}
	axis := retention.coordinates()
	if axis == nil {
		t.Fatal("no coordinate axis was built over the retention")
	}
	// The traversal must reach a subject. An axis that selected none would report no
	// objection and read exactly like a rule that ran and approved.
	if visited := len(axis.NodeEmittedPaths("test")); visited == 0 {
		t.Fatal("the traversal would visit no emitted name; a verdict from it would certify nothing")
	}
	return runNodeEmissionCollisionRule(
		context.Background().Err, true, view, axis,
		constraint.NodeInstance{ID: "test", SourceFile: "aggregate.yaml"},
	)
}

// Verifies the rule is scoped to two blocks meeting on ONE name, and not to two
// blocks emitting names that merely resemble each other. Two attachments emitting a
// file called "deployment.yaml" under their own "features/<x>/" prefix do not meet:
// the name each carries is the whole path, so the two names differ.
func TestNodeEmission_DistinctPrefixedNames_NoCollision(t *testing.T) {
	t.Parallel()
	err := nodeEmissionVerdict(t, []EmittedFile{
		{Name: "features/mon/deployment.yaml", Origin: EmitOriginSource, Body: []byte("a")},
		{Name: "features/log/deployment.yaml", Origin: EmitOriginSource, Body: []byte("b")},
	})
	if err != nil {
		t.Errorf("names that differ do not meet, so the rule must not fire, got %v", err)
	}
}

// Verifies two blocks meeting on one name halt, and that the diagnostic names both
// the output name and the two blocks — which is what makes the finding actionable,
// since a name alone does not say which of two blocks to rename.
func TestNodeEmission_TwoBlocksOnOneName_Halts(t *testing.T) {
	t.Parallel()
	err := nodeEmissionVerdict(t, []EmittedFile{
		{Name: "deployment.yaml", Origin: EmitOriginSource, Body: []byte("a")},
		{Name: "deployment.yaml", Origin: EmitOriginWorkload, Body: []byte("b")},
	})
	if !errors.Is(err, validator.ErrRecipeFilenameCollisionAcrossInputKinds()) {
		t.Fatalf("expected ErrRecipeFilenameCollisionAcrossInputKinds, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "deployment.yaml") {
		t.Errorf("the diagnostic should name the output name, got: %v", msg)
	}
	if !strings.Contains(msg, "source") || !strings.Contains(msg, "workload") {
		t.Errorf("the diagnostic should name both contributing blocks, got: %v", msg)
	}
}

// Verifies that ONE block emitting several files under one name does not fire the
// rule. That is one authoring decision rather than two blocks meeting, and the
// retention records such a block as a single contributor for exactly that reason;
// duplicate keys inside one artifacts block are refused when the document is
// decoded.
func TestNodeEmission_OneBlockRepeatingAName_NoCollision(t *testing.T) {
	t.Parallel()
	err := nodeEmissionVerdict(t, []EmittedFile{
		{Name: "config.yaml", Origin: EmitOriginArtifactsInline, Body: []byte("a")},
		{Name: "config.yaml", Origin: EmitOriginArtifactsInline, Body: []byte("b")},
	})
	if err != nil {
		t.Errorf("one block repeating a name is one contributor, so the rule must not fire, got %v", err)
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
