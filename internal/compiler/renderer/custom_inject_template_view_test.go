// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// customInjectMappingOutput delivers two custom mechanisms plus one core env
// needle. The custom mechanisms are authored in reverse-alphabetical order so a
// projection that lost author order and re-sorted would be visible, and the env
// needle calibrates the instrument: it travels the same overlay in the same build,
// so a custom mechanism missing from the output while the needle arrives is a
// measurement rather than a broken fixture.
const customInjectMappingOutput = `{"env": {"CAL_ENV": "cal-needle"}, ` +
	`"zz_vault": {"TOKEN_PATH": "vault-needle"}, ` +
	`"aa_ssm": {"DB_ARN": "ssm-needle"}}`

// customInjectWorkloadReads addresses both custom mechanisms by name, one key
// under each, and the merged union — the four reads whose answers differ.
const customInjectWorkloadReads = `      BY_NAME_SSM: '{{ .context.inject.aa_ssm.DB_ARN }}'
      BY_NAME_VAULT: '{{ .context.inject.zz_vault.TOKEN_PATH }}'
      WHOLE_MECHANISM: '{{ toJson .context.inject.aa_ssm }}'
      MERGED_UNION: '{{ toJson .context.inject.all }}'
      CORE_ENV: '{{ toJson .context.inject.env }}'
`

// apiServerFixture is one node, `api`, of type core/compute/api-server, rendered
// through a Recipe whose workload carries reads as its container's variables.
// selfInject, when set, is the inject block the component's author wrote;
// injectMapping, when set, attaches a core/secret-injector Feature named
// `secrets` whose FeatureRecipe delivers that interception output.
type apiServerFixture struct {
	aggregateName string
	selfInject    *types.InjectBlock
	reads         string
	injectMapping string
}

func (f apiServerFixture) input() renderer.RendererInput {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", BoundaryPath: "local", SelfInject: f.selfInject})

	featureValues := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	var featureOrder ir.FeatureOrder
	if f.injectMapping != "" {
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("secrets", types.IRFeatureEntry{Type: "core/secret-injector"})
		featureValues.Set("api", inner)
		featureOrder = ir.FeatureOrder{"api": {"secrets"}}
	}

	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: f.aggregateName},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: featureValues,
			},
		},
		FeatureOrder: featureOrder,
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
}

func (f apiServerFixture) lookup() renderer.RecipeLookup {
	lookup := renderer.RecipeLookup{
		Recipe: func(_, ct string) (*types.Recipe, bool) {
			if ct != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec: types.RecipeSpec{
					Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: api:latest
    variables:
` + f.reads},
				},
			}, true
		},
	}
	if f.injectMapping != "" {
		lookup.FeatureRecipe = func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft != "core/secret-injector" {
				return nil, false
			}
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "secret-injector-patcher"},
				Spec:     types.FeatureRecipeSpec{InjectMapping: f.injectMapping},
			}, true
		}
	}
	return lookup
}

// render renders the fixture and returns the emitted artifact containing
// marker, the name of one of the reads.
func (f apiServerFixture) render(t *testing.T, marker string) string {
	t.Helper()
	result, err := renderer.RenderAll(context.Background(), f.input(), f.lookup(), testHelmFactory(t))
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	for _, content := range result.Files {
		if strings.Contains(string(content.Content), marker) {
			return string(content.Content)
		}
	}
	t.Fatal("no emitted artifact carried the probe reads")
	return ""
}

// renderCustomInjectNode renders the authored example and returns the emitted
// artifact carrying the probe reads.
func renderCustomInjectNode(t *testing.T) string {
	t.Helper()
	return apiServerFixture{
		aggregateName: "custom-inject-app",
		reads:         customInjectWorkloadReads,
		injectMapping: customInjectMappingOutput,
	}.render(t, "BY_NAME_SSM")
}

// flattenWhitespace collapses every run of whitespace to one space, so an
// assertion names the delivered value rather than the emitting guest's chosen
// indentation. A read whose value is wrong still fails; a guest that re-indents
// does not.
func flattenWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestCustomInjectMechanism_ReachableByNameFromTemplate pins that a Recipe
// template can address what a CUSTOM inject mechanism delivered the same way it
// addresses env / file / arg, proved against emitted output rather than against a
// hand-built context.
//
// Two reads are the regression this pins. `.context.inject.<mechanism>.<key>`
// halted with a raw template error before the delivery pipe projected its custom
// mechanisms into its template view, because that view is produced by a reflective
// tag walk and the mechanisms are routed into a field no tag exposes; and
// `{{ toJson .context.inject.<mechanism> }}` rendered an empty string, which is
// worse than the halt because it reaches the artifact silently.
func TestCustomInjectMechanism_ReachableByNameFromTemplate(t *testing.T) {
	t.Parallel()
	raw := renderCustomInjectNode(t)
	got := flattenWhitespace(raw)

	for _, want := range []string{
		// Addressed by mechanism name, one key deep — the read that halted.
		"name: BY_NAME_SSM value: ssm-needle",
		"name: BY_NAME_VAULT value: vault-needle",
		// The whole mechanism — the read that silently rendered "".
		`name: WHOLE_MECHANISM value: '{"DB_ARN":"ssm-needle"}'`,
		// Non-regression: the merged union still carries every custom delivery,
		// and the core three are untouched by the projection.
		`name: MERGED_UNION value: '{"CAL_ENV":"cal-needle","DB_ARN":"ssm-needle","TOKEN_PATH":"vault-needle"}'`,
		`name: CORE_ENV value: '{"CAL_ENV":"cal-needle"}'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("emitted artifact missing %q; got:\n%s", want, raw)
		}
	}
}

// TestCustomInjectMechanism_TemplateViewDeterministic pins that the custom
// mechanisms' template view is byte-identical across renders.
//
// The projection is assembled by iterating an ordered map, so no Go-map iteration
// order can reach the emitted bytes. Reverting that to a map walk would leave this
// test as the thing that fails, which is the point: two mechanisms and three
// delivery keys are enough for a map walk to reorder something.
func TestCustomInjectMechanism_TemplateViewDeterministic(t *testing.T) {
	t.Parallel()
	first := renderCustomInjectNode(t)
	for i := 0; i < 8; i++ {
		if got := renderCustomInjectNode(t); got != first {
			t.Fatalf("render %d differs from first render\nfirst:\n%s\ngot:\n%s", i+1, first, got)
		}
	}
}
