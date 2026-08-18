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
	if !errors.Is(err, validator.ErrRecipeRule1()) {
		t.Fatalf("expected ErrRecipeRule1, got %v", err)
	}
}

func TestValidateRecipe_Rule1_ValuesMappingFileImportWithoutSource_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("helm")
	r.Spec.ValuesMapping = types.ValuesMapping{Import: "./values.tmpl"} // no source
	if err := validator.ValidateRecipe(r, tFile); !errors.Is(err, validator.ErrRecipeRule1()) {
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
	if !errors.Is(err, validator.ErrRecipeRule5()) {
		t.Fatalf("expected ErrRecipeRule5, got %v", err)
	}
}

// A duplicate-entry collision cannot arise at load: Artifacts is a single
// OrderedMap keyed by entry name, and yaml.v3's OrderedMap unmarshal collapses
// duplicates last-wins, so two entries with one name never both reach a
// validator. Output-filename collisions are a different class — an entry's
// output name is its key only for the inline form — and they are detected at
// emission rather than here.

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
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint()) {
		t.Fatalf("expected ErrRecipeEvalProviderConstraint, got %v", err)
	}
}

func TestValidateRecipe_Eval_RejectsWorkload(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.Workload = types.WorkloadTemplate{Inline: "apiVersion: aggrega.ai/v1alpha1\nkind: Workload"}
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint()) {
		t.Fatalf("expected ErrRecipeEvalProviderConstraint, got %v", err)
	}
}

func TestValidateRecipe_Eval_RejectsArtifacts(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("x.yaml", types.ArtifactEntry{Import: "./x.yaml"})
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeEvalProviderConstraint()) {
		t.Fatalf("expected ErrRecipeEvalProviderConstraint, got %v", err)
	}
}

// ----- Error message format consistency -----

func TestValidateRecipe_ErrorMessage_NamesEntityRuleAndLocation(t *testing.T) {
	t.Parallel()
	// Use rule 5 to trip the validator (rule 1 isn't currently active on this path).
	// Every Recipe shape rule reports through one shape; this test asserts the
	// format contract independent of which rule fires.
	r := newRecipe("helm")
	// Bare Recipe (no input kinds) → rule 5 fires.
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	// The entity reference opens the message and is built from the kind the
	// failure carries, never parsed back out of the rendered text.
	if !strings.HasPrefix(msg, "Recipe ") {
		t.Errorf("message must open with the entity reference, got: %q", msg)
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
	if !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
		t.Fatalf("expected ErrRecipeOutputLabelReservedNamespace, got %v", err)
	}
	if !strings.Contains(err.Error(), "aggrega.ai/foo") {
		t.Errorf("error should name the offending key, got %q", err.Error())
	}
}

func TestValidateRecipe_OutputLabels_K8sRecommendedReserved_Fails(t *testing.T) {
	t.Parallel()
	r := recipeWithLabels("helm", map[string]string{"app.kubernetes.io/bar": "baz"})
	if err := validator.ValidateRecipe(r, tFile); !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
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
	if err := validator.ValidateRecipe(r, tFile); !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
		t.Fatalf("expected reserved-namespace reject on eval Recipe, got %v", err)
	}
}

// The sentinel threads through the converted CompilerError, so callers that
// inspect the wrapped compiler error still match errors.Is.
func TestValidateRecipe_OutputLabels_ThreadsThroughCompilerError(t *testing.T) {
	t.Parallel()
	r := recipeWithLabels("helm", map[string]string{"aggrega.ai/foo": "bar"})
	err := validator.ValidateRecipe(r, tFile)
	var ve *validator.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *validator.ValidationError, got %v", err)
	}
	if !errors.Is(ve.ToCompilerError(), validator.ErrRecipeOutputLabelReservedNamespace()) {
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

// ----- outputComponentLabels promoted-key rules -----

// recipeWithComponentLabels builds a renderable Recipe declaring promoted keys
// and, optionally, literal outputLabels alongside them.
func recipeWithComponentLabels(provider string, promoted []string, literal map[string]string) *types.Recipe {
	r := newRecipe(provider)
	r.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	r.Spec.Artifacts.Set("out.yaml", types.ArtifactEntry{Inline: "kind: ConfigMap"})
	r.Spec.OutputComponentLabels = promoted
	r.Spec.OutputLabels = literal
	return r
}

// The reserved keyspace is the bare root only. A scoped key sits in a
// namespace the platform does not own and stays promotable — that case is the
// reason the guard is a root test rather than a substring test.
func TestValidateRecipe_OutputComponentLabels_ReservedNamespace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		promoted []string
		reserved bool
	}{
		{"platform root rejected", []string{"aggrega.ai/component-id"}, true},
		{"k8s recommended root rejected", []string{"app.kubernetes.io/name"}, true},
		{"scoped platform key allowed", []string{"component.aggrega.ai/hosting"}, false},
		{"unscoped key allowed", []string{"hosting"}, false},
		{"reserved root reached later in the list is still rejected", []string{"hosting", "aggrega.ai/x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := recipeWithComponentLabels("helm", tc.promoted, nil)
			err := validator.ValidateRecipe(r, tFile)
			if tc.reserved {
				if !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
					t.Fatalf("expected ErrRecipeOutputLabelReservedNamespace, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("promoted key %v should pass, got %v", tc.promoted, err)
			}
		})
	}
}

// The diagnostic names the offending key and the field it was declared in, so
// the author is not left guessing which of the two label blocks to edit.
func TestValidateRecipe_OutputComponentLabels_ReservedMessageNamesKeyAndField(t *testing.T) {
	t.Parallel()
	r := recipeWithComponentLabels("helm", []string{"aggrega.ai/foo"}, nil)
	err := validator.ValidateRecipe(r, tFile)
	if err == nil {
		t.Fatal("expected a reserved-namespace error")
	}
	for _, want := range []string{"aggrega.ai/foo", "outputComponentLabels"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got %q", want, err.Error())
		}
	}
}

// A key carrying a value from two sources at once is rejected: the component's
// own label and a literal written in place cannot both win.
func TestValidateRecipe_OutputComponentLabels_CollidesWithLiteral_Fails(t *testing.T) {
	t.Parallel()
	r := recipeWithComponentLabels("helm",
		[]string{"hosting"},
		map[string]string{"hosting": "managed"})
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeOutputComponentLabelCollision()) {
		t.Fatalf("expected ErrRecipeOutputComponentLabelCollision, got %v", err)
	}
	if !strings.Contains(err.Error(), "hosting") {
		t.Errorf("error should name the colliding key, got %q", err.Error())
	}
}

// Disjoint blocks are the ordinary authoring shape and must pass.
func TestValidateRecipe_OutputComponentLabels_DisjointFromLiteral_Passes(t *testing.T) {
	t.Parallel()
	r := recipeWithComponentLabels("helm",
		[]string{"component.aggrega.ai/hosting", "tier"},
		map[string]string{"cost-centre": "platform"})
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("disjoint label blocks should pass, got %v", err)
	}
}

// An absent block is not a violation.
func TestValidateRecipe_OutputComponentLabels_Empty_Passes(t *testing.T) {
	t.Parallel()
	r := recipeWithComponentLabels("helm", nil, nil)
	if err := validator.ValidateRecipe(r, tFile); err != nil {
		t.Fatalf("absent outputComponentLabels should pass, got %v", err)
	}
}

// Rule precedence is fixed rather than incidental: a recipe breaking both
// rules always reports the reserved namespace, whichever entry comes first.
func TestValidateRecipe_OutputComponentLabels_ReservedOutranksCollision(t *testing.T) {
	t.Parallel()
	r := recipeWithComponentLabels("helm",
		[]string{"hosting", "aggrega.ai/spoofed"},
		map[string]string{"hosting": "managed", "aggrega.ai/spoofed": "x"})
	err := validator.ValidateRecipe(r, tFile)
	if !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
		t.Fatalf("reserved namespace should outrank the collision rule, got %v", err)
	}
}

// The first-reported entry is deterministic: a list is checked in author order.
func TestValidateRecipe_OutputComponentLabels_FirstReportedIsAuthorOrder(t *testing.T) {
	t.Parallel()
	r := recipeWithComponentLabels("helm",
		[]string{"app.kubernetes.io/z", "aggrega.ai/a"}, nil)
	err := validator.ValidateRecipe(r, tFile)
	if err == nil || !strings.Contains(err.Error(), "app.kubernetes.io/z") {
		t.Fatalf("expected the first authored key app.kubernetes.io/z reported, got %v", err)
	}
}

// Engine-agnostic, matching the literal block: the rule fires on an eval
// Recipe, which the eval carve-out would otherwise short-circuit past.
func TestValidateRecipe_OutputComponentLabels_EvalReserved_Fails(t *testing.T) {
	t.Parallel()
	r := newRecipe("eval")
	r.Spec.OutputComponentLabels = []string{"aggrega.ai/x"}
	if err := validator.ValidateRecipe(r, tFile); !errors.Is(err, validator.ErrRecipeOutputLabelReservedNamespace()) {
		t.Fatalf("expected reserved-namespace reject on eval Recipe, got %v", err)
	}
}

// The collision sentinel threads through the converted CompilerError, so
// callers inspecting the wrapped error still match errors.Is.
func TestValidateRecipe_OutputComponentLabels_ThreadsThroughCompilerError(t *testing.T) {
	t.Parallel()
	r := recipeWithComponentLabels("helm",
		[]string{"hosting"},
		map[string]string{"hosting": "managed"})
	err := validator.ValidateRecipe(r, tFile)
	var ve *validator.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *validator.ValidationError, got %v", err)
	}
	if !errors.Is(ve.ToCompilerError(), validator.ErrRecipeOutputComponentLabelCollision()) {
		t.Fatalf("sentinel should thread through the converted CompilerError")
	}
}
