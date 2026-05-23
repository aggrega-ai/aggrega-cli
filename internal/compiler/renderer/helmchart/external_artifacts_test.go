// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Compile-time assertions: the helm-chart engine satisfies the marker
// (`ExternalArtifactsHandler`) plus the two companion capability
// interfaces that drive Phase B.3's external-handler dispatch. A
// regression breaking any of the satisfactions surfaces as a build error
// here rather than as a silent dispatch fall-through in evaluateArtifacts.
//
// ExternalArtifactsHandler — opts the engine into the external-handler
// branch (inline artifacts: rejected; file-imported artifact entries
// routed via the capability interfaces).
//
// ArtifactsPathProvider — declares per-artifact path placement
// (templates/<basename>).
//
// ArtifactsTemplateDelims — declares the Aggrega pre-template delimiter
// pair ("[[", "]]") so Helm's `{{ ... }}` survives the pre-pass.
var (
	_ renderer.ExternalArtifactsHandler = (*HelmEngine)(nil)
	_ renderer.ArtifactsPathProvider    = (*HelmEngine)(nil)
	_ renderer.ArtifactsTemplateDelims  = (*HelmEngine)(nil)
)

// TestHelmChartArtifactsImport_EndToEnd is the integration anchor for
// helm-chart pre-template artifact injection. It stands up a minimal
// sub-chart skeleton under t.TempDir(), constructs an IRGraph with one
// node whose Recipe declares `provider: helm-chart`, `source: <chart-dir>`,
// and an `artifacts:` entry pointing at `<crd.yaml-path>` via `$import:`,
// then runs renderer.RenderAll with the helm-chart engine factory. The
// asserted output:
//
//   - boundaries/<bp>/charts/<nodeID>/templates/crd.yaml exists.
//   - Its body parses as YAML.
//   - metadata.labels carries aggrega.ai/component-id.
//   - The chart-root files (Chart.yaml, the original templates/_app.yaml)
//     also land in the per-node tree alongside the injected crd.yaml.
//
// Acceptance criterion 6 in the WI front-matter.
func TestHelmChartArtifactsImport_EndToEnd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Minimal sub-chart skeleton: Chart.yaml + a dummy template so
	// copySourcedChart returns a non-empty file map and the brief's
	// "alongside Aggrega's generated templates" framing is exercised.
	chartDir := filepath.Join(root, "api-chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: api-chart
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "_app.yaml"), []byte(`# placeholder template
`), 0o644); err != nil {
		t.Fatalf("WriteFile _app.yaml: %v", err)
	}

	// The injected artifact: a CRD whose name is templated through
	// Aggrega's [[ ]] context. No Helm `{{ }}` syntax — the file parses as
	// YAML pre-render, so provenance injection runs.
	artifactsDir := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	crdPath := filepath.Join(artifactsDir, "crd.yaml")
	if err := os.WriteFile(crdPath, []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: [[ .context.instance_id ]].example.com
spec:
  group: example.com
  scope: Namespaced
`), 0o644); err != nil {
		t.Fatalf("WriteFile crd.yaml: %v", err)
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{
		Component:    "core/server/api-server",
		BoundaryPath: "production",
	})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "demo"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "test"},
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(string) (*types.Recipe, bool) {
			artifacts := types.OrderedMap[types.ArtifactEntry]{}
			artifacts.Set("crd.yaml", types.ArtifactEntry{Import: crdPath})
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Provider:      "helm-chart",
					Source:        chartDir,
					ValuesMapping: types.ValuesMapping{Inline: `{}`},
					Artifacts:     artifacts,
				},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	factory := func(string) (renderer.Engine, error) { return &HelmEngine{}, nil }

	result, err := renderer.RenderAll(context.Background(), input, lookup, factory)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	const target = "boundaries/production/helm-chart/charts/api/templates/crd.yaml"
	body, ok := result.Files[target]
	if !ok {
		gotPaths := make([]string, 0, len(result.Files))
		for p := range result.Files {
			gotPaths = append(gotPaths, p)
		}
		t.Fatalf("expected %q in rendered output; got paths %v", target, gotPaths)
	}

	var doc struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name   string            `yaml:"name"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal injected artifact: %v\nbody:\n%s", err, string(body))
	}
	if doc.Kind != "CustomResourceDefinition" {
		t.Errorf("kind = %q, want CustomResourceDefinition", doc.Kind)
	}
	if !strings.Contains(doc.Metadata.Name, "api.example.com") {
		t.Errorf("Aggrega-side [[ .context.instance_id ]] should resolve to api.example.com, got %q", doc.Metadata.Name)
	}
	if got := doc.Metadata.Labels["aggrega.ai/component-id"]; got != "api" {
		t.Errorf("provenance label aggrega.ai/component-id = %q, want api; full labels %v", got, doc.Metadata.Labels)
	}
	if got := doc.Metadata.Labels["aggrega.ai/recipe-uri"]; got != "api-recipe:1.0.0" {
		t.Errorf("provenance label aggrega.ai/recipe-uri = %q, want api-recipe:1.0.0; full labels %v", got, doc.Metadata.Labels)
	}

	// Sanity: the chart-root Chart.yaml and the placeholder template the
	// fixture authored should also have landed under the per-node tree.
	if _, ok := result.Files["boundaries/production/helm-chart/charts/api/Chart.yaml"]; !ok {
		paths := make([]string, 0, len(result.Files))
		for p := range result.Files {
			paths = append(paths, p)
		}
		t.Errorf("expected the sourced Chart.yaml to land under boundaries/production/helm-chart/charts/api/; got paths %v", paths)
	}
	if _, ok := result.Files["boundaries/production/helm-chart/charts/api/templates/_app.yaml"]; !ok {
		paths := make([]string, 0, len(result.Files))
		for p := range result.Files {
			paths = append(paths, p)
		}
		t.Errorf("expected the placeholder template to land alongside the injected CRD; got paths %v", paths)
	}
}
