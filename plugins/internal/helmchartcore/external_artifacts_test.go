// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// helmChartWasmFactory builds an EngineFactory that renders helm-chart nodes
// through the embedded helm-chart provider guest via the production wasmengine
// assembler adapter, carrying the provider's own host-capability holder
// (external-artifacts placement, delimiters, sub-chart projection). It is the
// Wasm-only successor to the removed in-tree helm-chart engine as this package's
// end-to-end render backend.
func helmChartWasmFactory(t *testing.T) renderer.EngineFactory {
	t.Helper()
	loader := wasm.NewCachingLoader(nil)
	var uri string
	for _, u := range embedded.URIs() {
		if strings.HasSuffix(u, "/helm-chart") {
			uri = u
			break
		}
	}
	if uri == "" {
		t.Fatalf("no embedded helm-chart guest; embedded URIs: %v", embedded.URIs())
	}
	lookup := func(u string) (pluginpath.ResolvedPlugin, error) {
		b, ok := embedded.Lookup(u)
		if !ok {
			return pluginpath.ResolvedPlugin{}, fmt.Errorf("no embedded guest for %q", u)
		}
		return pluginpath.ResolvedPlugin{Bytes: b, URI: u, Tier: pluginpath.TierEmbed, Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096}}, nil
	}
	return func(string) (renderer.Provider, error) {
		assembler := wasmengine.NewAssembler(lookup, uri, loader)
		// Release the assembler's reused plugin instance when the test ends: a guest
		// that declares a reusable instance keeps that instance on the engine until
		// Close, so an unclosed assembler outlives its test and the instances
		// accumulate for the life of the test binary.
		t.Cleanup(func() { _ = assembler.Close(context.Background()) })
		return assembler, nil
	}
}

// TestHelmChartArtifactsImport_EndToEnd is the integration anchor for
// helm-chart pre-template artifact injection. It stands up a minimal
// sub-chart skeleton under t.TempDir(), constructs an IRGraph with one
// node whose Recipe declares `target: helm-chart`, `source: <chart-dir>`,
// and an `artifacts:` entry pointing at `<crd.yaml-path>` via `$import:`,
// then runs renderer.RenderAll with the helm-chart engine factory. The
// asserted output:
//
//   - boundaries/<bp>/charts/<nodeID>/templates/crd.yaml exists.
//   - Its body parses as YAML.
//   - metadata.labels carries aggrega.ai/component-id.
//   - The chart-root files (Chart.yaml, the original templates/_app.yaml)
//     also land in the per-node tree alongside the injected crd.yaml.
func TestHelmChartArtifactsImport_EndToEnd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Minimal sub-chart skeleton: Chart.yaml + a dummy template so the
	// sourced-chart walk returns a non-empty file map and the brief's
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
		Component:    "core/compute/api-server",
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
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			artifacts := types.OrderedMap[types.ArtifactEntry]{}
			artifacts.Set("crd.yaml", types.ArtifactEntry{Import: crdPath})
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability:    "helm-chart",
					Source:        chartDir,
					ValuesMapping: types.ValuesMapping{Inline: `{}`},
					Artifacts:     artifacts,
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	factory := helmChartWasmFactory(t)

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
			Name        string            `yaml:"name"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(body.Content, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal injected artifact: %v\nbody:\n%s", err, string(body.Content))
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
	// recipe-uri is charset-unsafe (the ':') so it lands in annotations, not labels.
	if _, has := doc.Metadata.Labels["aggrega.ai/recipe-uri"]; has {
		t.Errorf("recipe-uri must be an annotation, not a label; labels %v", doc.Metadata.Labels)
	}
	if got := doc.Metadata.Annotations["aggrega.ai/recipe-uri"]; got != "api-recipe:1.0.0" {
		t.Errorf("provenance annotation aggrega.ai/recipe-uri = %q, want api-recipe:1.0.0; full annotations %v", got, doc.Metadata.Annotations)
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

// TestHelmChartArtifactsImport_ProvenanceSkipWarningSurfaces proves that a
// warning a provider guest raises while rendering reaches the build result's
// warnings — the diagnostic channel from the guest's render output through the
// wasmengine to RenderResult.Warnings. It renders a helm-chart node whose
// $import artifact carries a structural Helm action ({{- if }} ... {{- end }}):
// helm-chart's [[ ]] host-template delimiters leave the {{ }} untouched, so the
// bytes reach the guest verbatim; there the file does not survive the pre-render
// YAML round-trip, so provenance injection is skipped and the guest emits a
// warn-severity diagnostic. Before the diagnostic channel was plumbed this
// warning was silently swallowed; the test fails if it does not surface.
func TestHelmChartArtifactsImport_ProvenanceSkipWarningSurfaces(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

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
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "_app.yaml"), []byte("# placeholder template\n"), 0o644); err != nil {
		t.Fatalf("WriteFile _app.yaml: %v", err)
	}

	// A structural Helm action never parses as YAML pre-render, so provenance
	// injection is skipped and the guest raises the skip warning.
	artifactsDir := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cmPath := filepath.Join(artifactsDir, "conditional.yaml")
	if err := os.WriteFile(cmPath, []byte(`{{- if .Values.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: conditional-cm
{{- end }}
`), 0o644); err != nil {
		t.Fatalf("WriteFile conditional.yaml: %v", err)
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{
		Component:    "core/compute/api-server",
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
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			artifacts := types.OrderedMap[types.ArtifactEntry]{}
			artifacts.Set("conditional.yaml", types.ArtifactEntry{Import: cmPath})
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
				Spec: types.RecipeSpec{
					Capability:    "helm-chart",
					Source:        chartDir,
					ValuesMapping: types.ValuesMapping{Inline: `{}`},
					Artifacts:     artifacts,
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, helmChartWasmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	var found bool
	for _, w := range result.Warnings {
		if strings.Contains(w, "skipped provenance label injection") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("guest provenance-skip warning did not reach result.Warnings; warnings: %#v", result.Warnings)
	}

	// The artifact itself is still emitted verbatim (skipping provenance does not
	// drop the file) — the warning advises, it does not halt or mutate output.
	const target = "boundaries/production/helm-chart/charts/api/templates/conditional.yaml"
	if _, ok := result.Files[target]; !ok {
		t.Errorf("expected the artifact %q to be emitted despite the provenance skip", target)
	}
}
