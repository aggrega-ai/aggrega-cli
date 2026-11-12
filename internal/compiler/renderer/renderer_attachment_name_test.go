// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestRenderAll_NonCredentialFeatureRecipeReadsAttachmentName pins that a
// FeatureRecipe attached through the ordinary feature surface — not the
// credential one — can name its own attachment instance in a template, and that
// the name reaches the emitted artifact. Both attachment families derive their
// render context through the same site, so the instance name is available to
// each of them rather than to the credential family alone.
func TestRenderAll_NonCredentialFeatureRecipeReadsAttachmentName(t *testing.T) {
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
			if featureType == "core/monitoring" {
				// A non-credential FeatureRecipe emitting an artifact that names
				// the attachment instance it is rendering for.
				art := types.OrderedMap[types.ArtifactEntry]{}
				art.Set("scrape.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}-{{ .attachment_name }}
data:
  attachment: "{{ .attachment_name }}"
`})
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "monitoring"},
					Spec: types.FeatureRecipeSpec{
						Capability: "helm",
						Implements: "core/monitoring",
						Artifacts:  art,
					},
				}, true
			}
			return nil, false
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

	if !strings.Contains(body, "name: api-scrape_target") {
		t.Errorf("expected the attachment instance name in the rendered artifact's metadata.name, got:\n%s", body)
	}
	if !strings.Contains(body, `attachment: "scrape_target"`) {
		t.Errorf("expected the attachment instance name in the rendered artifact's data, got:\n%s", body)
	}
}
