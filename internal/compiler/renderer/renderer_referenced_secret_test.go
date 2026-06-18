// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// A core/referenced-secret credential Feature declares its referenceable-secret
// output contract (schema.provides {name, key}); the filling FeatureRecipe
// publishes it. The compiler resolves that fill's provides once and exposes it
// as .secrets.<slot>.provides.<key>, AND reuses the same bag as the credential
// render's .provides — so a consumer reading .secrets.<slot>.provides.name and
// the rendered Secret's name are one evaluated string (no drift, no locals
// re-derivation). A FeatureRecipe that omits a contract key halts.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// referencedSecretFeature builds a core/referenced-secret Feature whose
// schema.provides declares the {name, key} contract.
func referencedSecretFeature() types.Feature {
	var schema yaml.Node
	_ = yaml.Unmarshal([]byte("provides:\n  name: {type: string}\n  key: {type: string}\n"), &schema)
	return types.Feature{Spec: types.FeatureSpec{Schema: schema}}
}

// referencedSecretFixture: node "db" with an attached referenced-secret "password"
// backed by a FeatureRecipe that publishes provides {name, key} and emits a Secret
// artifact whose name derives from .provides.name. frProvides toggles the
// published keys to exercise the fulfilment halt.
func referencedSecretFixture(frProvides types.OrderedMap[string]) (renderer.RendererInput, renderer.RecipeLookup) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("db", types.IRNode{Component: "core/datastore/postgres", BoundaryPath: "backend"})

	vals := types.OrderedMap[any]{}
	vals.Set("key", "POSTGRES_PASSWORD")
	vals.Set("source", "supersecret")
	secrets := types.OrderedMap[types.IRSecretEntry]{}
	secrets.Set("password", types.IRSecretEntry{Type: "core/referenced-secret", Values: vals})
	nodeSecrets := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
	nodeSecrets.Set("db", secrets)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec:     types.IRSpec{Nodes: nodes, Secrets: nodeSecrets, ValuesMapping: types.OrderedMap[json.RawMessage]{}},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}, BoundaryInfra: map[string]*resolver.BoundaryInfra{}},
		CatalogLookup: resolver.CatalogLookup{
			Feature: func(typeURI string) (types.Feature, bool) {
				if typeURI == "core/referenced-secret" {
					return referencedSecretFeature(), true
				}
				return types.Feature{}, false
			},
		},
	}

	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("secret.yaml", types.ArtifactEntry{Inline: "apiVersion: v1\nkind: Secret\nmetadata:\n  name: \"{{ .provides.name }}\"\nstringData:\n  \"{{ .provides.key }}\": x\n"})

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			// The consumer (db recipe) reads .secrets.password.provides.name in an artifact.
			ra := types.OrderedMap[types.ArtifactEntry]{}
			ra.Set("consumer.yaml", types.ArtifactEntry{Inline: "host: \"{{ .secrets.password.provides.name }}\"\n"})
			return &types.Recipe{Metadata: types.RecipeMetadata{Name: "pg-recipe"}, Spec: types.RecipeSpec{Artifacts: ra}}, true
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft == "core/referenced-secret" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "k8s-secret-from-parameter"},
					Spec: types.FeatureRecipeSpec{
						Provider:   "helm",
						Implements: "core/referenced-secret:1.0.0",
						Provides:   frProvides,
						Artifacts:  artifacts,
					},
				}, true
			}
			return nil, false
		},
	}
	return input, lookup
}

func fullContractProvides() types.OrderedMap[string] {
	p := types.OrderedMap[string]{}
	p.Set("name", "{{ .context.instance_id }}-{{ .secret_name }}")
	p.Set("key", "{{ .values.key }}")
	return p
}

func TestIntegration_ReferencedSecret_ConsumerReadsGeneratedName(t *testing.T) {
	t.Parallel()
	input, lookup := referencedSecretFixture(fullContractProvides())

	// Consumer side: the resolved bag the .secrets.<slot>.provides exposure uses.
	bag, err := renderer.ResolveSecretProvides(input.Graph, "db", lookup, input.CatalogLookup)
	if err != nil {
		t.Fatalf("ResolveSecretProvides: %v", err)
	}
	pw := bag["password"]
	if got, _ := pw.Get("name"); got != "db-password" {
		t.Errorf(".secrets.password.provides.name = %q, want db-password", got)
	}

	// Producer side: the rendered Secret name must match (single source).
	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	var secretArtifact, consumerArtifact string
	for path, content := range result.Files {
		if strings.Contains(path, "secret.yaml") {
			secretArtifact = string(content)
		}
		if strings.Contains(path, "consumer.yaml") {
			consumerArtifact = string(content)
		}
	}
	if !strings.Contains(secretArtifact, "name: \"db-password\"") && !strings.Contains(secretArtifact, "name: db-password") {
		t.Errorf("rendered Secret name != db-password; got:\n%s", secretArtifact)
	}
	if !strings.Contains(consumerArtifact, "db-password") {
		t.Errorf("consumer read of .secrets.password.provides.name != db-password; got:\n%s", consumerArtifact)
	}
}

func TestIntegration_ReferencedSecret_FulfilmentHalt(t *testing.T) {
	t.Parallel()
	// FeatureRecipe omits the `key` contract key the Feature declares.
	partial := types.OrderedMap[string]{}
	partial.Set("name", "{{ .context.instance_id }}-{{ .secret_name }}")
	input, lookup := referencedSecretFixture(partial)

	_, err := renderer.ResolveSecretProvides(input.Graph, "db", lookup, input.CatalogLookup)
	if !errors.Is(err, renderer.ErrCredentialProvidesContractUnfulfilled) {
		t.Fatalf("a FeatureRecipe omitting a contract key should halt with ErrCredentialProvidesContractUnfulfilled; got: %v", err)
	}
}
