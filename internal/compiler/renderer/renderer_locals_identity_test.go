// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestRenderAll_FeatureRecipeLocalsCarryHostIDAndAttachmentName renders an
// attachment whose locals read both identities and whose artifact reads those
// locals, so what is pinned is the bytes an author receives rather than a value
// observed inside the evaluator. The host component and the attachment instance
// carry deliberately different names, so a binding that confused them would emit
// visibly wrong content.
//
// The same artifact reads the two names DIRECTLY as well as through the locals it
// resolved, which is what pins agreement between the two surfaces: an author reading
// .context.instance_id in a local and in an artifact of the same FeatureRecipe gets
// one answer.
func TestRenderAll_FeatureRecipeLocalsCarryHostIDAndAttachmentName(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.FeatureValuesMapping = func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("scrape_target", types.IRFeatureEntry{Type: "core/monitoring"})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		return outer
	}()
	input.FeatureOrder = ir.FeatureOrder{"api": []string{"scrape_target"}}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Capability:    "helm-chart",
						Implements:    "core/compute/api-server",
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "myapp:latest"}`},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != "core/monitoring" {
				return nil, false
			}
			locals := types.OrderedMap[string]{}
			locals.Set("host", `{{ .context.instance_id }}`)
			locals.Set("attached_as", `{{ .attachment_name }}`)
			locals.Set("scrape_name", `{{ .locals.host }}-{{ .locals.attached_as }}`)

			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set("scrape.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .locals.scrape_name }}
data:
  local_host: "{{ .locals.host }}"
  local_attached_as: "{{ .locals.attached_as }}"
  direct_host: "{{ .context.instance_id }}"
  direct_attached_as: "{{ .attachment_name }}"
`})
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "monitoring"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/monitoring",
					Locals:     locals,
					Artifacts:  art,
				},
			}, true
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var key string
	for k := range result.Files {
		if strings.Contains(k, "charts/api--scrape_target/") && strings.Contains(k, "scrape.yaml") {
			key = k
			break
		}
	}
	if key == "" {
		t.Fatalf("missing feature FR artifact under charts/api--scrape_target/, keys: %v", fileKeys(result.Files))
	}

	body := string(result.Files[key].Content)
	t.Logf("rendered %s:\n%s", key, body)

	for _, want := range []struct{ line, why string }{
		{`local_host: "api"`, "a local reading .context.instance_id resolves to the host component"},
		{`local_attached_as: "scrape_target"`, "a local reading .attachment_name resolves to the attachment instance"},
		{`direct_host: "api"`, "the artifact surface agrees with the locals surface on the host id"},
		{`direct_attached_as: "scrape_target"`, "the artifact surface agrees with the locals surface on the instance name"},
		{`name: api-scrape_target`, "a local assembled from both identities reaches the emitted artifact"},
	} {
		if !strings.Contains(body, want.line) {
			t.Errorf("emitted artifact omits %q — %s\ngot:\n%s", want.line, want.why, body)
		}
	}
}
