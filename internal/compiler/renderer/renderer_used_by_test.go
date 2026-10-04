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

// usedByRender is what one render of the used-by fixture produced: the host
// component's own artifact, and the artifact of the Feature attached to it when
// one is attached.
type usedByRender struct {
	host        string
	feature     string
	featureSeen bool
}

// renderUsedBy renders a graph in which the named callers, in the order given,
// each draw one `uses` connection to the component "api", with a Feature attached
// to "api" when attach is set. Every node renders artifacts only, through the
// embedded helm guest, so the two files the assertions read are produced by the
// real render path and nothing else is emitted around them.
//
// Each caller publishes a provides key derived from its own identity, which is
// what an attached Feature reads to tell one caller from another. The host's own
// artifact reads .context.used_by too, so a host context that carried the callers
// would print them into the host's bytes.
func renderUsedBy(t *testing.T, callers []string, attach bool) usedByRender {
	t.Helper()

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", ResolvedVersion: "1.0.0", BoundaryPath: "local/default"})
	var edges []types.IREdge
	for _, caller := range callers {
		nodes.Set(caller, types.IRNode{Component: "core/example/" + caller, ResolvedVersion: "1.0.0", BoundaryPath: "local/default"})
		edges = append(edges, types.IREdge{Source: caller, Target: "api", Socket: "apis", Alias: "backend"})
	}
	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "used-by"},
			Spec: types.IRSpec{
				Nodes:         nodes,
				Edges:         edges,
				ValuesMapping: types.OrderedMap[json.RawMessage]{},
				InfraMapping:  types.OrderedMap[json.RawMessage]{},
				Provenance:    types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "used-by"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "used-by-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
	if attach {
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("guard", types.IRFeatureEntry{Type: "core/caller-guard"})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		input.Graph.Spec.FeatureValuesMapping = outer
		input.FeatureOrder = ir.FeatureOrder{"api": []string{"guard"}}
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				art := types.OrderedMap[types.ArtifactEntry]{}
				art.Set("host-view.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}-view
data:
  seen: "{{ range .context.used_by }}{{ .component_id }};{{ end }}"
`})
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec:     types.RecipeSpec{Capability: "helm", Implements: componentType, Artifacts: art},
				}, true
			}
			caller, ok := strings.CutPrefix(componentType, "core/example/")
			if !ok {
				return nil, false
			}
			provides := types.OrderedMap[string]{}
			provides.Set("selector", `app={{ .context.instance_id }}`)
			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set(caller+".yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}
`})
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: caller + "-recipe"},
				Spec:     types.RecipeSpec{Capability: "helm", Implements: componentType, Provides: provides, Artifacts: art},
			}, true
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType != "core/caller-guard" {
				return nil, false
			}
			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set("callers.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}-{{ .attachment_name }}
data:
  callers: "{{ range .context.used_by }}{{ .component_id }}/{{ .socket }}/{{ .provides.selector }};{{ end }}"
`})
			return &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "caller-guard"},
				Spec:     types.FeatureRecipeSpec{Capability: "helm", Implements: featureType, Artifacts: art},
			}, true
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var out usedByRender
	for key, file := range result.Files {
		switch {
		case strings.HasSuffix(key, "callers.yaml"):
			out.feature, out.featureSeen = string(file.Content), true
		case strings.HasSuffix(key, "host-view.yaml"):
			out.host = string(file.Content)
		}
	}
	if out.host == "" {
		t.Fatalf("the host's own artifact was not emitted; keys: %v", fileKeys(result.Files))
	}
	return out
}

// TestRenderAll_AttachedFeatureSeesTheComponentsThatUseItsHost is the invocation
// proof for .context.used_by: a FeatureRecipe attached to a component renders the
// components that use it, in the order they were written, while the component's
// own rendered bytes stay the same whoever uses it.
//
// The control row is two callers written in an order that is not alphabetical,
// so a projection that sorted them fails the control. The row adding a third
// caller must change the Feature's artifact and leave the host's artifact
// byte-identical to the control's. The row with no Feature attached proves the
// host never sees its callers on its own. Rows run in order: the later rows
// compare against the control's host bytes.
func TestRenderAll_AttachedFeatureSeesTheComponentsThatUseItsHost(t *testing.T) {
	t.Parallel()

	const quietHost = `seen: ""`
	var control usedByRender
	for _, tc := range []struct {
		name        string
		callers     []string
		attach      bool
		wantFeature string // the callers line the Feature renders; empty when no Feature is attached
	}{
		{
			name:        "control: two callers in the order they were written",
			callers:     []string{"worker", "frontend"},
			attach:      true,
			wantFeature: `callers: "worker/apis/app=worker;frontend/apis/app=frontend;"`,
		},
		{
			name:        "a third caller changes the Feature and not the host",
			callers:     []string{"worker", "frontend", "reporter"},
			attach:      true,
			wantFeature: `callers: "worker/apis/app=worker;frontend/apis/app=frontend;reporter/apis/app=reporter;"`,
		},
		{
			name:    "no Feature attached",
			callers: []string{"worker", "frontend"},
			attach:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderUsedBy(t, tc.callers, tc.attach)
			t.Logf("host artifact:\n%s", got.host)
			t.Logf("feature artifact (emitted=%v):\n%s", got.featureSeen, got.feature)

			if !strings.Contains(got.host, quietHost) {
				t.Errorf("the host's own render saw its callers; want %s in:\n%s", quietHost, got.host)
			}
			switch {
			case tc.wantFeature == "" && got.featureSeen:
				t.Errorf("no Feature is attached, yet a Feature artifact was emitted:\n%s", got.feature)
			case tc.wantFeature != "" && !got.featureSeen:
				t.Fatalf("the attached Feature's artifact was not emitted")
			case tc.wantFeature != "" && !strings.Contains(got.feature, tc.wantFeature):
				t.Errorf("the attached Feature rendered the wrong callers; want %s in:\n%s", tc.wantFeature, got.feature)
			}

			if control.host == "" {
				control = got
				return
			}
			if tc.attach && got.host != control.host {
				t.Errorf("the host's rendered bytes moved with its callers:\n got:\n%s\n control:\n%s", got.host, control.host)
			}
			if tc.attach && got.feature == control.feature {
				t.Errorf("a different set of callers left the Feature's render unchanged:\n%s", got.feature)
			}
		})
	}
}
