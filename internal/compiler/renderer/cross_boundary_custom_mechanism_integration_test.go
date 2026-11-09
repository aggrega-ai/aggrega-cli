// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A uses edge may deliver a value under a mechanism its author names, beside env,
// file and arg. When the edge crosses into another output and its provider links
// the two outputs, the value a template writes under that mechanism has to become
// the provider's cross-reference exactly as the env value beside it does — a
// value naming a resource of the producer's own output means nothing in the
// consumer's. These tests build that through the render entry point and the
// embedded CloudFormation guest, with the database and its reader in two stacks.

const (
	customLinkConsumer = "api"
	customLinkProducer = "db"
)

// customMechanismConsumerFragment writes the value the edge delivers under env and
// the host it delivers under the custom mechanism ssm_ref into two parameters,
// each as a whole value.
const customMechanismConsumerFragment = `Resources:
  ApiEnv:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /api/db-host-env
      Type: String
      Value: {{ .context.inject.env.DB_HOST }}
  ApiCustom:
    Type: AWS::SSM::Parameter
    Properties:
      Name: /api/db-host-custom
      Type: String
      Value: {{ .context.inject.ssm_ref.DB_HOST_REF }}
`

// customMechanismProducerFragment declares the database and publishes its host as
// a reference to the database resource, which only the producer's stack declares,
// beside its port.
const customMechanismProducerFragment = `Resources:
  Db:
    Type: AWS::RDS::DBInstance
    Properties:
      Engine: mariadb
provides:
  host: !GetAtt Db.Endpoint.Address
  port: "3306"
`

// customMechanismLinkInput builds the two-stack render: the reader in the
// package's default boundary, the database in a boundary of its own, and the one
// edge between them delivering the database's host under ssm_ref and the provides
// key envFrom under env.
func customMechanismLinkInput(envFrom string) (renderer.RendererInput, renderer.RecipeLookup) {
	input := testInput()

	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "stateful-server")
	input.Graph.Spec.Nodes.Set(customLinkProducer, types.IRNode{
		Component:       "core/datastore/database",
		Labels:          labels,
		ResolvedVersion: "1.0.0",
		BoundaryPath:    "local/data",
	})
	input.Graph.Spec.ValuesMapping.Set(customLinkProducer, json.RawMessage(`{}`))

	var env, ssmRef types.OrderedMap[string]
	env.Set("DB_HOST", envFrom)
	ssmRef.Set("DB_HOST_REF", "host")
	edge := types.IREdge{Source: customLinkConsumer, Target: customLinkProducer, Socket: "database", Alias: "primary"}
	edge.Inject.Set("env", env)
	edge.Inject.Set("ssm_ref", ssmRef)
	input.Graph.Spec.Edges = []types.IREdge{edge}

	input.GraphView = crossProviderGraphFacts{
		targets: map[string]string{
			customLinkConsumer + "\x00database\x00primary": customLinkProducer,
		},
		inSet: map[string]bool{customLinkConsumer: true, customLinkProducer: true},
	}
	consumer := constraint.NodeInstance{ID: customLinkConsumer, SourceFile: "aggregate.yaml", AuthoredName: customLinkConsumer}
	input.GraphCoordinates = crossProviderCoordinates{
		nodes: []constraint.NodeInstance{
			consumer,
			{ID: customLinkProducer, SourceFile: "aggregate.yaml", AuthoredName: customLinkProducer},
		},
		edges: []constraint.EdgeInstance{{Node: consumer, Socket: "database", Alias: "primary"}},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			artifacts := types.OrderedMap[types.ArtifactEntry]{}
			recipe := &types.Recipe{
				APIVersion: "aggrega.ai/v1beta1",
				Kind:       "Recipe",
				Spec:       types.RecipeSpec{Capability: "cloudformation", Implements: componentType},
			}
			switch componentType {
			case "core/compute/api-server":
				recipe.Metadata = types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"}
				artifacts.Set("template.yaml", types.ArtifactEntry{Inline: customMechanismConsumerFragment})
			case "core/datastore/database":
				recipe.Metadata = types.RecipeMetadata{Name: "db-recipe", Version: "1.0.0"}
				artifacts.Set("template.yaml", types.ArtifactEntry{Inline: customMechanismProducerFragment})
				recipe.Spec.Provides.Set("host", "!GetAtt Db.Endpoint.Address")
				recipe.Spec.Provides.Set("port", "3306")
			default:
				return nil, false
			}
			recipe.Spec.Artifacts = artifacts
			return recipe, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
	return input, lookup
}

// customMechanismStackHolding returns the parsed stack template among the
// render's files that declares the named resource.
func customMechanismStackHolding(t *testing.T, files map[string]renderer.AssembledFile, resource string) map[string]any {
	t.Helper()
	for _, path := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasSuffix(path, "cloudformation-stack.json") {
			continue
		}
		var stack map[string]any
		if err := json.Unmarshal(files[path].Content, &stack); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if resources, _ := stack["Resources"].(map[string]any); resources[resource] != nil {
			return stack
		}
	}
	t.Fatalf("no assembled stack declares %s; files: %v", resource, slices.Sorted(maps.Keys(files)))
	return nil
}

// resourceValueProperty returns the Value property of the named parameter resource.
func resourceValueProperty(stack map[string]any, resource string) any {
	resources, _ := stack["Resources"].(map[string]any)
	r, _ := resources[resource].(map[string]any)
	props, _ := r["Properties"].(map[string]any)
	return props["Value"]
}

// TestRenderAll_ACustomMechanismAcrossStacksBecomesTheCrossStackReference is the
// row a build reaches: the reader's stack receives the database host it reads under
// ssm_ref as a cross-stack import of the host, as an env value receives one, rather
// than the producer's own reference to a resource the reader's stack does not
// declare, and no link placeholder survives into any output. In the first row env
// delivers the same host, so the two values are the one import; in the second env
// delivers the port, so only the custom mechanism carries the host across.
func TestRenderAll_ACustomMechanismAcrossStacksBecomesTheCrossStackReference(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		envFrom string
	}{
		{name: "env delivers the same host", envFrom: "host"},
		{name: "only the custom mechanism delivers the host", envFrom: "port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input, lookup := customMechanismLinkInput(tc.envFrom)

			result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
			if err != nil {
				t.Fatalf("the two-stack render: %s", cerrors.CauseChain(err))
			}
			for path, file := range result.Files {
				if strings.Contains(string(file.Content), rendercore.CrossBoundaryLinkSentinelPrefix) {
					t.Fatalf("%s retains a link placeholder:\n%s", path, file.Content)
				}
			}

			if producer := customMechanismStackHolding(t, result.Files, "Db"); resourceValueProperty(producer, "ApiEnv") != nil {
				t.Fatalf("the reader rendered into the database's stack; the edge does not cross a boundary")
			}
			reader := customMechanismStackHolding(t, result.Files, "ApiEnv")

			envValue, _ := resourceValueProperty(reader, "ApiEnv").(map[string]any)
			envImport, imported := envValue["Fn::ImportValue"].(string)
			if !imported || !strings.HasSuffix(envImport, tc.envFrom) {
				t.Fatalf("the env control is not the cross-stack import of %s, so the row proves nothing about the custom value; got %#v", tc.envFrom, resourceValueProperty(reader, "ApiEnv"))
			}
			// The export name ends with the provides key it publishes, so the host's
			// import is the env control's with its key swapped.
			want := any(map[string]any{"Fn::ImportValue": strings.TrimSuffix(envImport, tc.envFrom) + "host"})
			if got := resourceValueProperty(reader, "ApiCustom"); !reflect.DeepEqual(got, want) {
				t.Errorf("the value delivered under ssm_ref is %#v, want the cross-stack import of the host, %#v", got, want)
			}
		})
	}
}
