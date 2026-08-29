// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The pillars a FeatureRecipe local reads are the attachment's own values and
// infra, while the identity it reads through .context.instance_id is the HOST
// COMPONENT. The instance name and the host id are deliberately different strings
// here, so a binding that confused the two could not pass.
func TestEvaluateFeatureRecipeLocals_FeatureScoped(t *testing.T) {
	t.Parallel()
	locals := types.OrderedMap[string]{}
	locals.Set("scrape_target", `{{ .context.instance_id }}:{{ .values.scrape_port }}`)
	locals.Set("tier", `{{ .values.tier }}`)
	locals.Set("attached_as", `{{ .attachment_name }}`)

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec: types.FeatureRecipeSpec{
			Locals: locals,
		},
	}
	// Feature-scoped values/infra (FR's own, not base Recipe's)
	values := types.OrderedMap[any]{}
	values.Set("scrape_port", float64(9090))
	values.Set("tier", "prod")

	got, err := EvaluateFeatureRecipeLocals(fr, "scrape_target", "api", values, types.OrderedMap[any]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scrapeTarget, _ := got.Get("scrape_target")
	if scrapeTarget != "api:9090" {
		t.Errorf("scrape_target = %q, want %q — .context.instance_id is the host component", scrapeTarget, "api:9090")
	}
	tier, _ := got.Get("tier")
	if tier != "prod" {
		t.Errorf("tier: %q", tier)
	}
	attachedAs, _ := got.Get("attached_as")
	if attachedAs != "scrape_target" {
		t.Errorf("attached_as = %q, want %q — .attachment_name is the attachment instance", attachedAs, "scrape_target")
	}
}

// The FeatureRecipe locals surface shares LocalsContext with the Recipe surface, so
// it refuses .provides for the same ordering reason: an attachment's locals resolve
// before the provides that may read them.
func TestEvaluateFeatureRecipeLocals_RejectsProvidesRef(t *testing.T) {
	t.Parallel()
	locals := types.OrderedMap[string]{}
	locals.Set("scrape_target", `{{ .provides.host }}:9090`)
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec:     types.FeatureRecipeSpec{Locals: locals},
	}
	_, err := EvaluateFeatureRecipeLocals(fr, "mon", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err == nil {
		t.Fatal("expected a halt on the reverse reference, got nil")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".provides.host") {
		t.Errorf("diagnostic does not name the authored path: %s", diagnosticChainText(err))
	}
}

func TestEvaluateFeatureRecipeLocals_NilFR(t *testing.T) {
	t.Parallel()
	got, err := EvaluateFeatureRecipeLocals(nil, "n", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("expected empty, got %v", got.Keys())
	}
}

func TestEvaluateFeatureRecipeLocals_RejectsInjectRef(t *testing.T) {
	t.Parallel()
	locals := types.OrderedMap[string]{}
	locals.Set("leak", `{{ .context.inject.env.DB_URL }}`)
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "bad"},
		Spec: types.FeatureRecipeSpec{
			Locals: locals,
		},
	}
	_, err := EvaluateFeatureRecipeLocals(fr, "n", "api", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	if err == nil {
		t.Fatal("expected self-containment error")
	}
	if !errors.Is(err, ErrTemplateUnknownRoot()) {
		t.Errorf("expected the unknown-reference halt, got: %v", err)
	}
	if !strings.Contains(diagnosticChainText(err), ".context.inject.env.DB_URL") {
		t.Errorf("diagnostic does not name the authored path: %s", diagnosticChainText(err))
	}
}

func TestExecutePhaseBForFeature_RendersValuesMapping(t *testing.T) {
	t.Parallel()

	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec: types.FeatureRecipeSpec{
			ValuesMapping: types.ValuesMapping{Inline: `{"image": "prom/mon:{{ .values.tag }}", "port": {{ .values.port }}}`},
		},
	}
	values2 := types.OrderedMap[any]{}
	values2.Set("tag", "v1")
	values2.Set("port", float64(9090))
	ctx := &types.TemplateContext{
		Values: values2,
	}

	values, err := ExecutePhaseBForFeature(ctx, fr, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if values["image"] != "prom/mon:v1" {
		t.Errorf("image: %v", values["image"])
	}
	gotPort, ok := asNumber(values["port"])
	if !ok || gotPort != 9090 {
		t.Errorf("port: %v (%T)", values["port"], values["port"])
	}
}

func TestExecutePhaseBForFeature_NilFR(t *testing.T) {
	t.Parallel()
	got, err := ExecutePhaseBForFeature(nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestExecutePhaseBForFeature_EmptyMappingReturnsContextValues(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: "x"}}
	values := types.OrderedMap[any]{}
	values.Set("a", float64(1))
	ctx := &types.TemplateContext{Values: values}
	got, err := ExecutePhaseBForFeature(ctx, fr, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got["a"] != float64(1) {
		t.Errorf("expected passthrough .values, got %v", got)
	}
}

func TestExecutePhaseBForFeature_ResolvesLibraryImport(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "lib"},
		Spec:     types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Import: "Helpers#/mappings/Core"}},
	}
	libs := map[string]types.MappingLibrary{
		"Helpers": mappingLibraryFixture("Helpers", map[string]string{
			"Core": `{"image": "prom/mon:{{ .values.tag }}"}`,
		}),
	}
	values2 := types.OrderedMap[any]{}
	values2.Set("tag", "v2")
	got, err := ExecutePhaseBForFeature(&types.TemplateContext{Values: values2}, fr, libs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["image"] != "prom/mon:v2" {
		t.Errorf("image: %v; want the imported fragment templated against .values", got["image"])
	}
}

func TestExecutePhaseBForFeature_LibraryImportNotFound(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "lib"},
		Spec:     types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Import: "Absent#/mappings/Core"}},
	}
	_, err := ExecutePhaseBForFeature(&types.TemplateContext{}, fr, nil)
	if err == nil {
		t.Fatal("expected an unresolved bare-name library import to error")
	}
	if !errors.Is(err, ErrMappingLibraryNotFound()) {
		t.Errorf("expected ErrMappingLibraryNotFound, got: %v", err)
	}
}

func TestExecutePhaseBForFeature_RejectsHostBearingImport(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "lib"},
		Spec:     types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Import: "oci://hub.aggrega.ai/aggrega/helpers@1.0.0#/mappings/Core"}},
	}
	_, err := ExecutePhaseBForFeature(&types.TemplateContext{}, fr, nil)
	if err == nil {
		t.Fatal("expected host-bearing $import rejection")
	}
	if !errors.Is(err, ErrHostInLibraryImport()) {
		t.Errorf("expected ErrHostInLibraryImport, got: %v", err)
	}
}

func TestExecutePhaseBForFeature_InvalidYAML(t *testing.T) {
	t.Parallel()
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "bad"},
		Spec:     types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `not a mapping`}},
	}
	_, err := ExecutePhaseBForFeature(&types.TemplateContext{}, fr, nil)
	if err == nil {
		t.Fatal("expected YAML parse error")
	}
	if !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("unexpected message: %v", err)
	}
}

func TestIsResourceGeneratingFR(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fr   *types.FeatureRecipe
		want bool
	}{
		{"nil", nil, false},
		{"empty spec", &types.FeatureRecipe{}, false},
		{"inject-only (only injectMapping)", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{InjectMapping: `{"env":{"X":"1"}}`},
		}, false},
		{"has valuesMapping", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"image":"x"}`}},
		}, true},
		{"has valuesMapping with $import", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Import: "/tmp/v.yaml"}},
		}, true},
		{"has source string", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: "./charts/x"},
		}, true},
		{"has source structured", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: map[string]any{"registry": "oci://x"}},
		}, true},
		{"empty source string", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: ""},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isResourceGeneratingFR(c.fr); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// pipelineStepFixture is the one-step pipeline the admission cases below declare.
// Nothing resolves it — the admission question is answered from the declaration
// alone, before any plugin is looked up.
func pipelineStepFixture() []types.PipelineStep {
	return []types.PipelineStep{{ID: "patch", Plugin: "pipeline-step/host-patch:1.0.0", Function: "step"}}
}

// TestParticipatesInRenderSequence enumerates what admits an attachment to the
// base-rendering sequence. Two disjoint reasons admit one and neither implies the
// other, so the cases cover both singly and together, plus the two shapes that
// declare neither.
//
// The pipeline-only row is the one that separates this predicate from
// isResourceGeneratingFR: such a FeatureRecipe generates no resource, so the
// resource predicate answers false for it, and it must still be admitted or its
// pipeline never runs.
func TestParticipatesInRenderSequence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		fr          *types.FeatureRecipe
		wantAdmit   bool
		wantGenerat bool
	}{
		{"nil", nil, false, false},
		{"pure-wiring (capability and implements only)", &types.FeatureRecipe{}, false, false},
		{"inject-only (only injectMapping)", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{InjectMapping: `{"env":{"X":"1"}}`},
		}, false, false},
		{"pipeline only, no render input", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Pipeline: pipelineStepFixture()},
		}, true, false},
		{"pipeline plus injectMapping, no render input", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{
				InjectMapping: `{"env":{"X":"1"}}`,
				Pipeline:      pipelineStepFixture(),
			},
		}, true, false},
		{"render input, no pipeline", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{Source: "./charts/x"},
		}, true, true},
		{"render input and pipeline", &types.FeatureRecipe{
			Spec: types.FeatureRecipeSpec{
				Source:   "./charts/x",
				Pipeline: pipelineStepFixture(),
			},
		}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := participatesInRenderSequence(c.fr); got != c.wantAdmit {
				t.Errorf("participatesInRenderSequence = %v, want %v", got, c.wantAdmit)
			}
			// The resource predicate keeps its own meaning: it is asserted on the
			// same row so a widening of it — the change this admission predicate
			// exists to avoid — fails here rather than passing silently.
			if got := isResourceGeneratingFR(c.fr); got != c.wantGenerat {
				t.Errorf("isResourceGeneratingFR = %v, want %v", got, c.wantGenerat)
			}
		})
	}
}

// TestParticipatesInRenderSequenceAgreesWithTheLockTimeDescent pins the render
// side of the pin-then-run pair. The lock-time descent walks every
// spec.pipeline[] step of every FeatureRecipe in the closure and resolves the
// plugin it names, with no render-input condition on it at all; a FeatureRecipe
// whose pipeline that descent walks and whose pipeline the render path skips is a
// plugin resolved for a step that never executes.
//
// So the render-side condition must be exactly "declares a pipeline", read off
// the same field the descent reads: this asserts that a FeatureRecipe declaring
// nothing but a pipeline is admitted, and that removing the pipeline is what
// takes it back out. The lock side of the pair is asserted in the closure
// package's descent tests.
func TestParticipatesInRenderSequenceAgreesWithTheLockTimeDescent(t *testing.T) {
	t.Parallel()

	fr := &types.FeatureRecipe{
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: "feature.myco/guardrail",
			Pipeline:   pipelineStepFixture(),
		},
	}
	// The fixture declares no render input — the shape the descent pins a plugin
	// for while the resource predicate answers false.
	if isResourceGeneratingFR(fr) {
		t.Fatalf("fixture is not pipeline-only: it declares a render input")
	}
	if !participatesInRenderSequence(fr) {
		t.Errorf("a FeatureRecipe whose declared pipeline the lock-time descent walks is not admitted to the render sequence")
	}

	withoutPipeline := *fr
	withoutPipeline.Spec.Pipeline = nil
	if participatesInRenderSequence(&withoutPipeline) {
		t.Errorf("a FeatureRecipe declaring neither a render input nor a pipeline is admitted; the descent walks no step for it either")
	}
}
