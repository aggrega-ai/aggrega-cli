// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive an attached Feature whose FeatureRecipe sources an artifact
// its author shipped and fills that artifact's inputs through its valuesMapping,
// through RenderAll and the embedded guests. What they prove is that the values
// the attachment's mapping produced reach the boundary's assembler as that
// contributor's own values, marked as a mapping's output, exactly as a
// component's own sourced Recipe's do: the CloudFormation stack learns which
// parameters the attachment's binding filled, and the Terraform root
// configuration writes the attachment's module arguments. Each scope an
// attachment can be declared at is driven, because each routes its contributor
// to the group by a different site.

const (
	attachmentAssemblyBoundary = "cluster"
	attachmentAssemblyHost     = "frhost"
	attachmentAssemblyInstance = "aux"
	attachmentAssemblyFeature  = "core/aux:1.0.0"
)

// attachmentAssemblyScopes are the two places an attachment is declared, each
// with the name the case is reported under and the identity a halt names its
// binding by: the component an attachment is declared on, or the identity composed
// from the boundary's path and the attachment's name, which its resources carry.
var attachmentAssemblyScopes = []struct {
	name          string
	boundaryScope bool
	binder        string
}{
	{name: "an attachment declared on the component", binder: attachmentAssemblyHost},
	{name: "an attachment declared on the boundary", boundaryScope: true, binder: "bfr--" + attachmentAssemblyBoundary + "--" + attachmentAssemblyInstance},
}

// attachmentAssemblyInput is one host component at one boundary carrying one
// attachment of attachmentAssemblyFeature, declared on the component or on the
// boundary.
func attachmentAssemblyInput(hostComponent string, boundaryScope bool) renderer.RendererInput {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(attachmentAssemblyHost, types.IRNode{Component: hostComponent, BoundaryPath: attachmentAssemblyBoundary})

	clusterBoundary := types.Boundary{
		Directory:  "dist/cluster",
		Components: types.OrderedMap[types.BoundaryComponent]{},
		Boundaries: types.OrderedMap[types.Boundary]{},
	}
	clusterBoundary.Components.Set(attachmentAssemblyHost, types.BoundaryComponent{})
	boundaryFeatures := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	if boundaryScope {
		authored := types.OrderedMap[types.BoundaryFeature]{}
		authored.Set(attachmentAssemblyInstance, types.BoundaryFeature{Type: attachmentAssemblyFeature, Infra: types.OrderedMap[any]{}})
		clusterBoundary.Features = authored
		projected := types.OrderedMap[types.IRBoundaryFeature]{}
		projected.Set(attachmentAssemblyInstance, types.IRBoundaryFeature{Type: attachmentAssemblyFeature, Infra: types.OrderedMap[any]{}})
		boundaryFeatures.Set(attachmentAssemblyBoundary, projected)
	}
	// The attachment sits in the host's own attachment sequence at either scope, as
	// the graph the compiler builds carries it: a boundary's attachment is projected
	// onto every component the boundary holds, and the render dispatches a
	// boundary-scope FeatureRecipe from the first such component's sequence, once
	// for the boundary. Left out of the sequence, a boundary attachment never renders.
	entries := types.OrderedMap[types.IRFeatureEntry]{}
	entries.Set(attachmentAssemblyInstance, types.IRFeatureEntry{Type: attachmentAssemblyFeature})
	featureValues := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	featureValues.Set(attachmentAssemblyHost, entries)
	featureOrder := ir.FeatureOrder{attachmentAssemblyHost: []string{attachmentAssemblyInstance}}
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(attachmentAssemblyBoundary, clusterBoundary)

	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "attachment-assembly-app"},
			Spec: types.IRSpec{
				Nodes:                nodes,
				ValuesMapping:        types.OrderedMap[json.RawMessage]{},
				InfraMapping:         types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: featureValues,
				BoundaryFeatures:     boundaryFeatures,
				Provenance:           types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder: featureOrder,
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "attachment-assembly-app"}},
			Release:   &types.Release{Spec: types.ReleaseSpec{Boundaries: boundaries}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{
				attachmentAssemblyHost: {BoundaryPath: attachmentAssemblyBoundary, Directory: "dist/cluster"},
			},
		},
	}
}

// attachmentAssemblyLookup serves the host's Recipe and the attachment's
// FeatureRecipe, the latter sourcing sourceDir with the valuesMapping given (none
// when empty).
func attachmentAssemblyLookup(host *types.Recipe, capability, sourceDir, mapping string, boundaryScope bool) renderer.RecipeLookup {
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "aux-fr", Version: "1.0.0"},
		Spec: types.FeatureRecipeSpec{
			Capability:    capability,
			Implements:    attachmentAssemblyFeature,
			Source:        sourceDir,
			ValuesMapping: types.ValuesMapping{Inline: mapping},
		},
	}
	if boundaryScope {
		fr.Spec.ExecutionScope = "boundary"
	}
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == host.Spec.Implements {
				return host, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if strings.HasPrefix(featureType, "core/aux") {
				return fr, true
			}
			return nil, false
		},
	}
}

// attachmentAssemblyCfnTemplate is the attachment's sourced template: it declares
// Stage and references it, the declaration a binding of Stage removes.
const attachmentAssemblyCfnTemplate = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Stage:
    Type: String
Resources:
  TopicA:
    Type: AWS::SNS::Topic
    Properties:
      DisplayName: !Ref Stage
`

// attachmentAssemblyCfnHostFragment is the host component's own fragment, which
// references Stage without declaring it.
const attachmentAssemblyCfnHostFragment = `Resources:
  HostTopic:
    Type: AWS::SNS::Topic
    Properties:
      DisplayName: !Ref Stage
`

// renderAttachmentCfnStack renders the host and its attachment into one stack and
// returns the stack's bytes.
func renderAttachmentCfnStack(t *testing.T, sourceDir, mapping string, boundaryScope bool) ([]byte, error) {
	t.Helper()
	host := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "frhost-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "cloudformation",
			Implements: "core/frhost",
			Artifacts:  oneInlineArtifact("host.yaml", attachmentAssemblyCfnHostFragment),
		},
	}
	result, err := renderer.RenderAll(context.Background(),
		attachmentAssemblyInput("core/frhost", boundaryScope),
		attachmentAssemblyLookup(host, "cloudformation", sourceDir, mapping, boundaryScope),
		wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	for _, path := range slices.Sorted(maps.Keys(result.Files)) {
		if strings.HasSuffix(path, "cloudformation-stack.json") {
			return result.Files[path].Content, nil
		}
	}
	t.Fatalf("the build assembled no stack template; files: %v", slices.Sorted(maps.Keys(result.Files)))
	return nil, nil
}

// TestRenderAll_CloudFormationReferenceToAParameterAnAttachmentsBindingFilledHalts
// is the assembly's halt for a binding an attached Feature's FeatureRecipe made:
// the attachment sources a template declaring Stage and its valuesMapping binds
// Stage, so the binding removes Stage from the stack, and the host component's own
// fragment still references it. The host carries the attachment's mapped values and
// their mark onto the rendered node the assembler receives, so the reference halts
// under the plugin condition for an input the guest could not resolve, naming the
// component whose content references it, that content's template and the
// parameter. The control maps nothing: Stage stays declared by the attachment's
// template, the host's reference resolves to it, and two builds assemble the same
// bytes.
func TestRenderAll_CloudFormationReferenceToAParameterAnAttachmentsBindingFilledHalts(t *testing.T) {
	t.Parallel()
	sourceDir := writeTerraformModule(t, map[string]string{"template.yaml": attachmentAssemblyCfnTemplate})
	for _, scope := range attachmentAssemblyScopes {
		t.Run(scope.name, func(t *testing.T) {
			t.Parallel()
			_, err := renderAttachmentCfnStack(t, sourceDir, "Stage: dev\n", scope.boundaryScope)
			if !errors.Is(err, wasm.ErrPluginRequiredInputUnresolved()) {
				t.Fatalf("the host referencing a parameter its attachment's binding filled: got %v, want the assembly's halt", err)
			}
			chain := cerrors.CauseChain(err)
			for _, want := range []string{
				`component "frhost"'s template "host.yaml" references parameter "Stage"`,
				`valuesMapping of a Recipe or FeatureRecipe rendering for component "` + scope.binder + `"`,
			} {
				if !strings.Contains(chain, want) {
					t.Errorf("the halt does not name %s:\n%s", want, chain)
				}
			}

			control, err := renderAttachmentCfnStack(t, sourceDir, "", scope.boundaryScope)
			if err != nil {
				t.Fatalf("the control mapping nothing: %s", cerrors.CauseChain(err))
			}
			var stack map[string]any
			if err := json.Unmarshal(control, &stack); err != nil {
				t.Fatalf("parse the control's stack: %v", err)
			}
			params, _ := stack["Parameters"].(map[string]any)
			if _, declared := params["Stage"]; !declared {
				t.Errorf("the control's stack does not declare Stage; Parameters: %v", params)
			}
			resources, _ := stack["Resources"].(map[string]any)
			hostTopic, _ := resources["HostTopic"].(map[string]any)
			props, _ := hostTopic["Properties"].(map[string]any)
			if got, want := props["DisplayName"], any(map[string]any{"Ref": "Stage"}); !reflect.DeepEqual(got, want) {
				t.Errorf("the control's HostTopic DisplayName is %#v, want the reference as written %#v", got, want)
			}
			again, err := renderAttachmentCfnStack(t, sourceDir, "", scope.boundaryScope)
			if err != nil {
				t.Fatalf("the control's second build: %s", cerrors.CauseChain(err))
			}
			if !bytes.Equal(control, again) {
				t.Errorf("two builds of the control assembled different bytes")
			}
		})
	}
}

// attachmentAssemblyTfModule is the attachment's sourced module: one input
// variable, with no default, that a resource reads.
func attachmentAssemblyTfModule() map[string]string {
	return map[string]string{
		"variables.tf": "variable \"bucket_name\" {\n  type = string\n}\n",
		"main.tf":      "resource \"aws_s3_bucket\" \"aux\" {\n  bucket = var.bucket_name\n}\n",
	}
}

// renderAttachmentTfRoot renders the host and its attachment and returns the
// boundary's root configuration.
func renderAttachmentTfRoot(t *testing.T, sourceDir, mapping string, boundaryScope bool) string {
	t.Helper()
	host := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "tfhost-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "terraform",
			Implements: "core/tfhost",
			Artifacts:  oneInlineArtifact("host.tf.json", "{\"output\":{\"host_marker\":{\"value\":\"host\"}}}\n"),
		},
	}
	result, err := renderer.RenderAll(context.Background(),
		attachmentAssemblyInput("core/tfhost", boundaryScope),
		attachmentAssemblyLookup(host, "terraform", sourceDir, mapping, boundaryScope),
		wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %s", cerrors.CauseChain(err))
	}
	return string(findBySuffix(t, result.Files, "aggrega.modules.tf"))
}

// TestRenderAll_TerraformAttachmentsSourcedModuleCarriesItsMappedArguments is the
// root configuration's half for an attached Feature's sourced module: the values
// the attachment's valuesMapping produced become the arguments of the attachment's
// own module block, as a component's own sourced Recipe's do. The control maps
// nothing: the attachment's block carries its source alone, and two builds write
// the same bytes.
func TestRenderAll_TerraformAttachmentsSourcedModuleCarriesItsMappedArguments(t *testing.T) {
	t.Parallel()
	sourceDir := writeTerraformModule(t, attachmentAssemblyTfModule())
	for _, scope := range []struct {
		name          string
		boundaryScope bool
		module        string
	}{
		{name: attachmentAssemblyScopes[0].name, module: "frhost--aux"},
		{name: attachmentAssemblyScopes[1].name, boundaryScope: true, module: "aux"},
	} {
		t.Run(scope.name, func(t *testing.T) {
			t.Parallel()
			root := renderAttachmentTfRoot(t, sourceDir, "bucket_name: zq-bucket-mapped\n", scope.boundaryScope)
			want := "module \"" + scope.module + "\" {\n  source = \"./modules/" + scope.module + "\"\n\n  bucket_name = \"zq-bucket-mapped\"\n}\n"
			if !strings.Contains(root, want) {
				t.Errorf("the attachment's module block does not carry its mapped argument:\n got:\n%s\nwant a block:\n%s", root, want)
			}

			control := renderAttachmentTfRoot(t, sourceDir, "", scope.boundaryScope)
			bare := "module \"" + scope.module + "\" {\n  source = \"./modules/" + scope.module + "\"\n}\n"
			if !strings.Contains(control, bare) {
				t.Errorf("the control's attachment block is not its source alone:\n got:\n%s\nwant a block:\n%s", control, bare)
			}
			if again := renderAttachmentTfRoot(t, sourceDir, "", scope.boundaryScope); again != control {
				t.Errorf("two builds of the control wrote different root configurations")
			}
		})
	}
}
