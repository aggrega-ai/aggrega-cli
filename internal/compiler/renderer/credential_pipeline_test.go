// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// credentialPipelineInput builds a RendererInput whose "api" node carries one
// credential attachment ("root_password") bound to a resource-generating
// credential FeatureRecipe. The FeatureRecipe declares a pipeline step, and the
// host component synthesizes a workload so the step receives a host document
// root alongside its own.
func credentialPipelineInput(t *testing.T) (renderer.RendererInput, renderer.RecipeLookup) {
	t.Helper()

	input := testInput()
	input.PluginPathLookup = pluginpath.NewResolver(
		pluginpath.ResolutionContext{Override: wasmtest.EnsureFixtureBuilt(t, "frp-step-patch-rust.wasm")},
		pluginpath.PinPostureNone,
		pluginpath.PinMaps{},
	).Resolve

	vals := types.OrderedMap[any]{}
	vals.Set("length", float64(32))
	envInj := types.OrderedMap[string]{}
	envInj.Set("DB_PASSWORD", "ref:root_password")
	inj := types.OrderedMap[types.OrderedMap[string]]{}
	inj.Set("env", envInj)
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("root_password", types.IRFeatureEntry{
		Type:   "core/db-password",
		Values: vals,
		Inject: inj,
	})
	outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	outer.Set("api", inner)
	input.Graph.Spec.FeatureValuesMapping = outer
	input.FeatureOrder = map[string][]string{"api": {"root_password"}}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec: types.RecipeSpec{
					Capability: "helm",
					Implements: "core/compute/api-server",
					Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: myapp:latest
`},
				},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != "core/db-password" {
				return nil, false
			}
			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set("secret.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: Secret
metadata:
  name: {{ .attachment_name }}
stringData:
  binding: "{{ .inject.env.DB_PASSWORD }}"
`})
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "db-password"},
				Spec: types.FeatureRecipeSpec{
					Capability: "helm",
					Implements: "core/db-password",
					Artifacts:  art,
					Pipeline: []types.PipelineStep{
						{ID: "patch", Plugin: "frp-step-patch-rust.wasm", Function: "step"},
					},
				},
			}, true
		},
	}
	return input, lookup
}

// TestRenderAll_CredentialFeatureRecipePipelineExecutes pins that a credential
// attachment's FeatureRecipe runs its spec.pipeline, exactly as an operational
// attachment's does: the step's own-scope emit lands beside the credential's
// own rendered resource, and its host-scope emit reaches the host component's
// document set. A credential whose FeatureRecipe declares a pipeline that never
// runs is the failure this test forbids — the plugin would be pinned into the
// lockfile at pull and silently skipped at render.
func TestRenderAll_CredentialFeatureRecipePipelineExecutes(t *testing.T) {
	input, lookup := credentialPipelineInput(t)
	t.Parallel()

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	keys := make([]string, 0, len(result.Files))
	for k := range result.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// The attachment's own-scope output carries its contributor prefix, and the
	// instance name's underscore is escaped there because the coordinates become
	// part of a filename rather than a directory segment of their own. Written out
	// rather than composed, so the spelling that actually reaches a consumer is
	// what this asserts.
	const ownPrefix = "api--root-zupassword--"
	var ownEmit, hostEmit string
	for _, k := range keys {
		switch {
		case strings.Contains(k, ownPrefix) && strings.HasSuffix(k, "pipeline-configmap.yaml"):
			ownEmit = k
		case strings.HasSuffix(k, "pipeline-host-marker.yaml"):
			hostEmit = k
		}
	}

	if ownEmit == "" {
		t.Errorf("credential FeatureRecipe pipeline own-scope emit missing; rendered keys:\n%s", strings.Join(keys, "\n"))
	} else if body := string(result.Files[ownEmit].Content); !strings.Contains(body, "name: pipeline-emitted") {
		t.Errorf("own-scope emit %q carries unexpected content:\n%s", ownEmit, body)
	}

	if hostEmit == "" {
		t.Errorf("credential FeatureRecipe pipeline host-scope emit missing; rendered keys:\n%s", strings.Join(keys, "\n"))
	} else if body := string(result.Files[hostEmit].Content); !strings.Contains(body, "injected-by: frp-step-patch") {
		t.Errorf("host-scope emit %q carries unexpected content:\n%s", hostEmit, body)
	}
}
