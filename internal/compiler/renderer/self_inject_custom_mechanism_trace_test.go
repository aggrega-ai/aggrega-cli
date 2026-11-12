// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof, through a render with the per-pass capture on, that a
// component's own inject block withholds a declared-sensitive value in the
// capture whichever mechanism delivers it: a custom mechanism the component type
// permits is shown exactly as env is.
//
// Every withheld position has an undeclared sibling delivered the same way, and
// the render is run twice — withholding, and with the capture told to show every
// value — so the search for the planted value in the withholding capture is
// proved able to find it.

package renderer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	customSelfInjectType     = "example/compute/custom-self-inject"
	customSelfInjectNode     = "app"
	customSelfInjectBoundary = "local"
	// The value the component's schema declares sensitive, and an undeclared one.
	customSelfInjectSecret = "custom-mechanism-secret-plaintext"
	customSelfInjectPublic = "custom-mechanism-public-name"
)

// customSelfInjectBlock delivers the declared value and the undeclared one twice:
// under env, and under a custom mechanism the component type permits.
func customSelfInjectBlock() *types.InjectBlock {
	pair := func(prefix string) []any {
		return []any{
			map[string]any{"in": prefix + "_TOKEN", "value": "{{ .values.token }}"},
			map[string]any{"in": prefix + "_NAME", "value": "{{ .values.appName }}"},
		}
	}
	block := &types.InjectBlock{Env: pair("ENV")}
	block.Additional.Set("vault_ref", pair("VAULT"))
	return block
}

// renderCustomSelfInject renders the one node with the capture on and returns the
// capture's root directory and the emitted files.
func renderCustomSelfInject(t *testing.T, mode trace.Mode) (string, map[string]renderer.AssembledFile) {
	t.Helper()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(customSelfInjectNode, types.IRNode{
		Component:    customSelfInjectType,
		BoundaryPath: customSelfInjectBoundary,
		SelfInject:   customSelfInjectBlock(),
	})
	values := types.OrderedMap[json.RawMessage]{}
	values.Set(customSelfInjectNode, json.RawMessage(
		`{"appName":"`+customSelfInjectPublic+`","token":"`+customSelfInjectSecret+`"}`))

	var schema yaml.Node
	if err := yaml.Unmarshal([]byte("values:\n  appName: { type: string }\n  token: { type: string, sensitive: true }\n"), &schema); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}

	traceRoot := t.TempDir()
	writer := trace.New(traceRoot, mode)
	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "custom-self-inject"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				ValuesMapping: values,
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		CatalogLookup: resolver.CatalogLookup{
			Component: func(typeURI string) (types.Component, bool) {
				if strings.HasPrefix(typeURI, customSelfInjectType) {
					return types.Component{
						Metadata: types.ComponentMetadata{Name: customSelfInjectType, Version: "1.0.0"},
						Spec:     types.ComponentSpec{Inject: []string{"env", "vault_ref"}, Schema: schema},
					}, true
				}
				return types.Component{}, false
			},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		Trace:     writer,
		TraceMode: mode,
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if !strings.HasPrefix(componentType, customSelfInjectType) {
				return nil, false
			}
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "custom-self-inject-recipe"},
				Spec: types.RecipeSpec{Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1beta1
kind: Workload
containers:
  main:
    image: own:1.0
    variables:
      ENV_TOKEN: '{{ .context.inject.env.ENV_TOKEN }}'
      ENV_NAME: '{{ .context.inject.env.ENV_NAME }}'
      VAULT_TOKEN: '{{ index .context.inject.vault_ref "VAULT_TOKEN" }}'
      VAULT_NAME: '{{ index .context.inject.vault_ref "VAULT_NAME" }}'
`}},
			}, true
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the capture: %v", err)
	}
	return traceRoot, result.Files
}

// customSelfInjectPipe reads the delivery pipe the capture shows at the pipe's
// own slot, by mechanism and then by delivery key.
func customSelfInjectPipe(t *testing.T, traceRoot string) map[string]map[string]string {
	t.Helper()
	path := filepath.Join(traceRoot, "boundaries", customSelfInjectBoundary, customSelfInjectNode, "03-inject-hydration.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the capture's pipe slot: %v", err)
	}
	var doc struct {
		Pipe map[string]map[string]string `yaml:"context.inject"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse the capture's pipe slot: %v\n%s", err, body)
	}
	if len(doc.Pipe) == 0 {
		t.Fatalf("the capture's pipe slot shows no delivery pipe:\n%s", body)
	}
	return doc.Pipe
}

// customSelfInjectCaptureFiles is every file the capture wrote, by path.
func customSelfInjectCaptureFiles(t *testing.T, traceRoot string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(traceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the capture: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the capture wrote no file; every search below would be vacuous")
	}
	return out
}

// TestSelfInjectCustomMechanism_TheCaptureWithholdsItAsItWithholdsEnv is the
// per-key table over the delivery pipe the capture shows. The declared value is
// withheld under env, under the custom mechanism and under the merged view of
// both; the undeclared value reads as written in every one of them.
func TestSelfInjectCustomMechanism_TheCaptureWithholdsItAsItWithholdsEnv(t *testing.T) {
	t.Parallel()
	traceRoot, _ := renderCustomSelfInject(t, trace.Mode{})
	pipe := customSelfInjectPipe(t, traceRoot)

	rows := []struct {
		mechanism, key, want string
	}{
		{"env", "ENV_TOKEN", redact.Placeholder()},
		{"env", "ENV_NAME", customSelfInjectPublic},
		{"vault_ref", "VAULT_TOKEN", redact.Placeholder()},
		{"vault_ref", "VAULT_NAME", customSelfInjectPublic},
		{"all", "ENV_TOKEN", redact.Placeholder()},
		{"all", "VAULT_TOKEN", redact.Placeholder()},
		{"all", "VAULT_NAME", customSelfInjectPublic},
	}
	for _, r := range rows {
		if got := pipe[r.mechanism][r.key]; got != r.want {
			t.Errorf("context.inject.%s.%s = %q, want %q", r.mechanism, r.key, got, r.want)
		}
	}
}

// TestSelfInjectCustomMechanism_TheValueAppearsNowhereInTheWithholdingCapture is
// the whole-capture half: no file of the withholding capture carries the declared
// value in the clear, whichever slot would have shown it. The capture told to show
// every value is the needle, and the emitted files are the boundary's other side:
// the deployable carries the value under both mechanisms whichever way the capture
// ran.
func TestSelfInjectCustomMechanism_TheValueAppearsNowhereInTheWithholdingCapture(t *testing.T) {
	t.Parallel()
	withholdingRoot, emitted := renderCustomSelfInject(t, trace.Mode{})
	unredactedRoot, _ := renderCustomSelfInject(t, trace.Mode{Unredacted: true})

	for path, body := range customSelfInjectCaptureFiles(t, withholdingRoot) {
		if strings.Contains(body, customSelfInjectSecret) {
			t.Errorf("%s carries the declared value in the clear\n--- file ---\n%s", path, body)
		}
	}

	found := false
	for _, body := range customSelfInjectCaptureFiles(t, unredactedRoot) {
		if strings.Contains(body, customSelfInjectSecret) {
			found = true
			break
		}
	}
	if !found {
		t.Error("the capture showing every value carries the declared value nowhere; the search above cannot be trusted to find it")
	}

	var artifact strings.Builder
	for _, f := range emitted {
		artifact.Write(f.Content)
	}
	got := flattenWhitespace(artifact.String())
	for _, want := range []string{
		"name: ENV_TOKEN value: " + customSelfInjectSecret,
		"name: VAULT_TOKEN value: " + customSelfInjectSecret,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the emitted files must carry %q; the deployable is never withheld\n--- emitted ---\n%s", want, artifact.String())
		}
	}
}
