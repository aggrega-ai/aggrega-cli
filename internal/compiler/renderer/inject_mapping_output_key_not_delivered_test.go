// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// deliveredValueArtifact is the Recipe artifact a passing row renders: it reads
// the delivery the row makes under one mechanism and stops by name unless the
// value arrived intact, so a row that renders has proved the value reached the
// context the Recipe renders against.
func deliveredValueArtifact(mechanism string) string {
	return fmt.Sprintf(`{{- if ne (optional (optional .context.inject %q dict) "K" "") "PH54-placeholder" -}}
{{- fail "the delivery under %s did not reach the Recipe" -}}
{{- end -}}
services: {}
`, mechanism, mechanism)
}

// TestRenderAll_InjectMappingOutputKeyNotDelivered pins the halt for an
// injectMapping output that writes deliveries under a key no delivery mechanism of
// the host reads, through the render entry point every build takes.
//
// The merge applies env, file, arg and every custom mechanism under its own name,
// and rebuilds the all view from them rather than merging it, so a delivery
// written under all reaches nothing while the output still parses. The halting
// rows assert the registered condition, the key, the FeatureRecipe, the
// attachment's position and the keys an output may carry. The passing rows are the
// controls: an output using only delivered mechanisms, a custom mechanism the host
// declares, a custom mechanism it does not declare — the Recipe reads it on the
// pipe at its own name either way — and an all view written empty, which loses
// nothing. Each passing row's Recipe reads its delivery and stops unless it
// arrived, so a passing row also fails when a value is lost.
//
// Removing the rule's invocation from the render turns the halting rows into
// builds that succeed, which these rows refuse.
func TestRenderAll_InjectMappingOutputKeyNotDelivered(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		mapping string
		// declares is the custom mechanism the host component's own injection
		// block declares, or "" for none.
		declares string
		// reads is the mechanism a passing row's Recipe must find its delivery
		// under.
		reads string
		halts bool
	}{
		{name: "a delivery written only under all",
			mapping: `{"all": {"K": "PH54-placeholder"}}`, halts: true},
		{name: "env beside a delivery written under all",
			mapping: `{"env": {"K": "PH54-placeholder"}, "all": {"L": "PH54-other"}}`, halts: true},
		{name: "a delivery under all beside the custom mechanism the host declares",
			mapping:  `{"ssm_param": {"K": "PH54-placeholder"}, "all": {"L": "PH54-other"}}`,
			declares: "ssm_param", halts: true},

		{name: "only delivered mechanisms", mapping: `{"env": {"K": "PH54-placeholder"}}`, reads: "env"},
		{name: "a custom mechanism the host declares", mapping: `{"ssm_param": {"K": "PH54-placeholder"}}`,
			declares: "ssm_param", reads: "ssm_param"},
		{name: "a custom mechanism the host does not declare", mapping: `{"ph54mech": {"K": "PH54-placeholder"}}`,
			reads: "ph54mech"},
		{name: "an all view written empty", mapping: `{"env": {"K": "PH54-placeholder"}, "all": {}}`, reads: "env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input, lookup := injectMappingInput([]string{"db-creds"}, map[string]string{"db-creds": tc.mapping})
			node, _ := input.Graph.Spec.Nodes.Get("api")
			node.SelfInject = nil
			if tc.declares != "" {
				raw := &types.InjectBlock{}
				raw.Additional.Set(tc.declares, map[string]any{"K": "root_password"})
				node.SelfInject = raw
			}
			input.Graph.Spec.Nodes.Set("api", node)
			if tc.reads != "" {
				recipeFor := lookup.Recipe
				lookup.Recipe = func(instance, componentType string) (*types.Recipe, bool) {
					recipe, ok := recipeFor(instance, componentType)
					if !ok {
						return nil, false
					}
					artifacts := types.OrderedMap[types.ArtifactEntry]{}
					artifacts.Set("extra.yaml", types.ArtifactEntry{Inline: deliveredValueArtifact(tc.reads)})
					recipe.Spec.Artifacts = artifacts
					return recipe, true
				}
			}
			factory := func(string) (renderer.Provider, error) { return &captureComposerEngine{}, nil }

			_, err := renderer.RenderAll(context.Background(), input, lookup, factory)

			if !tc.halts {
				if err != nil {
					t.Fatalf("every key of this output is delivered, so the build must render with the value "+
						"in place; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("RenderAll succeeded; a delivery written under all reaches nothing and must halt the build")
			}
			if !errors.Is(err, renderer.ErrInjectMappingOutputKeyNotDelivered()) {
				t.Fatalf("the halt must carry the registered condition; got %v", err)
			}
			text := renderedHaltText(err)
			for _, want := range []string{`"all"`, `recipe "db-creds-recipe"`,
				".spec.components.api.features.db-creds", "an output may carry"} {
				if !strings.Contains(text, want) {
					t.Errorf("the halt must carry %s; got %s", want, text)
				}
			}
			if strings.Contains(text, "PH54-") {
				t.Errorf("the halt must not quote a delivered value; got %s", text)
			}
		})
	}
}
