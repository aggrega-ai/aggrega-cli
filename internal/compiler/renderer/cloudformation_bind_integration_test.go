// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive a cloudformation Recipe that sources a SAM template fetched from
// a git repository, seeded in the cache as a pull leaves it, through RenderAll and
// the embedded cloudformation guest. The Recipe's valuesMapping binds two of the
// template's parameters; the guest substitutes their references and drops their
// declarations, and the assembled stack carries the template's own Transform.

const (
	cfnBindURL    = "https://example.com/bind/templates.git"
	cfnBindRef    = "v1.0.0"
	cfnBindCommit = "89abcdef0123456789abcdef0123456789abcdef"
)

// cfnBindTemplate is a SAM template whose function takes its memory and its stage
// from declared parameters, the stage through Fn::Sub's string form.
const cfnBindTemplate = `AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-31
Parameters:
  FunctionMemory:
    Type: Number
    Default: 128
  StageName:
    Type: String
  Untouched:
    Type: String
    Default: kept
Resources:
  Handler:
    Type: AWS::Serverless::Function
    Properties:
      Runtime: python3.12
      Handler: index.handler
      InlineCode: "def handler(event, context): return 'ok'"
      MemorySize: !Ref FunctionMemory
      Environment:
        Variables:
          STAGE: !Sub "${StageName}-api"
          OTHER: !Ref Untouched
`

// cfnBindRecipeLookup resolves the one component type to a Recipe sourcing the
// fetched template with the valuesMapping given.
func cfnBindRecipeLookup(mapping string) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType != "core/compute/api-server" {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability:    "cloudformation",
					Implements:    "core/compute/api-server",
					Source:        map[string]any{"kind": "git-repository", "url": cfnBindURL, "ref": cfnBindRef},
					ValuesMapping: types.ValuesMapping{Inline: mapping},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
}

// renderCfnBind renders the one component and returns its assembled stack.
func renderCfnBind(t *testing.T, mapping string) (map[string]any, error) {
	t.Helper()
	root := t.TempDir()
	blob := buildIntegrationTGZ(t, map[string]string{"api.yaml": cfnBindTemplate})
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "git-repository", cfnBindURL, cfnBindCommit, blob, digest)

	input := integrationInput(t, root, "core/compute/api-server", "api", "cloudformation", digest, cfnBindURL, cfnBindCommit)
	row := &input.Lockfile.SourceArtifacts[0]
	row.Kind = "git-repository"
	row.Mediatype = "application/gzip"
	row.ReferencedBy[0].RequestedRef = cfnBindRef
	input.GraphView = derivedKeyGraphFacts{}

	result, err := renderer.RenderAll(context.Background(), input, cfnBindRecipeLookup(mapping), wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	for _, path := range slices.Sorted(maps.Keys(result.Files)) {
		if !strings.HasSuffix(path, "cloudformation-stack.json") {
			continue
		}
		var stack map[string]any
		if err := json.Unmarshal(result.Files[path].Content, &stack); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		return stack, nil
	}
	t.Fatalf("the build assembled no stack template; files: %v", slices.Sorted(maps.Keys(result.Files)))
	return nil, nil
}

// TestRenderAll_CloudFormationBindsASourcedTemplatesParametersAndKeepsItsTransform is
// the binding and the carried Transform read through the build: the mapping's two
// keys bind the template's two parameters — the memory from the Component's values,
// converted to the string a Ref to a Number yields, and the stage as a literal
// through Fn::Sub's variable map — those parameters leave the template, the one no
// key names stays, and the stack carries the template's own Transform.
func TestRenderAll_CloudFormationBindsASourcedTemplatesParametersAndKeepsItsTransform(t *testing.T) {
	t.Parallel()

	stack, err := renderCfnBind(t, "FunctionMemory: {{ .values.replicas }}\nStageName: prod\n")
	if err != nil {
		t.Fatalf("the build binding the template's parameters: %s", cerrors.CauseChain(err))
	}

	if got := stack["Transform"]; got != "AWS::Serverless-2016-10-31" {
		t.Errorf("the stack's Transform is %#v, want the template's own AWS::Serverless-2016-10-31", got)
	}
	params, _ := stack["Parameters"].(map[string]any)
	if got := slices.Sorted(maps.Keys(params)); !slices.Equal(got, []string{"Untouched"}) {
		t.Errorf("the stack declares the parameters %v, want only the unbound Untouched", got)
	}
	resources, _ := stack["Resources"].(map[string]any)
	handler, _ := resources["Handler"].(map[string]any)
	props, _ := handler["Properties"].(map[string]any)
	if got := props["MemorySize"]; got != "1" {
		t.Errorf("MemorySize is %#v, want the bound value as the string \"1\"", got)
	}
	env, _ := props["Environment"].(map[string]any)
	vars, _ := env["Variables"].(map[string]any)
	if got, want := vars["STAGE"], any(map[string]any{"Fn::Sub": []any{"${StageName}-api", map[string]any{"StageName": "prod"}}}); !reflect.DeepEqual(got, want) {
		t.Errorf("STAGE is %#v, want %#v", got, want)
	}
	if got, want := vars["OTHER"], any(map[string]any{"Ref": "Untouched"}); !reflect.DeepEqual(got, want) {
		t.Errorf("OTHER is %#v, want the unbound reference %#v", got, want)
	}

	twice, err := renderCfnBind(t, "FunctionMemory: {{ .values.replicas }}\nStageName: prod\n")
	if err != nil {
		t.Fatalf("the second build: %s", cerrors.CauseChain(err))
	}
	if !reflect.DeepEqual(stack, twice) {
		t.Errorf("two builds binding the same values assembled different stacks")
	}
}

// TestRenderAll_CloudFormationMappedKeyNamingNoParameterHalts holds the binding's
// halt on a mapped key no template declares, under the plugin condition for an input
// the guest could not resolve, naming the key.
func TestRenderAll_CloudFormationMappedKeyNamingNoParameterHalts(t *testing.T) {
	t.Parallel()

	_, err := renderCfnBind(t, "StageNmae: prod\n")
	if !errors.Is(err, wasm.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("a mapped key naming no parameter: got %v, want the guest's halt", err)
	}
	if chain := cerrors.CauseChain(err); !strings.Contains(chain, `"StageNmae"`) || !strings.Contains(chain, `"StageName"`) {
		t.Errorf("the halt does not name the key and the declared parameters:\n%s", chain)
	}
}
