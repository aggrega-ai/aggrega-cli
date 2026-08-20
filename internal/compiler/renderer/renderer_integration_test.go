// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestIntegration_ProvidesSelfContainmentViolation drives a render given no
// presentation of the finalised graph, which is what this package can build: the
// presentation chain descends from the composition's own, whose constructor is
// unexported, so a render assembled here presents no stage and the rules declared
// over one are not invoked.
//
// What the build still owes in that state is the loud refusal, and it is asserted
// here: the entry reaches the evaluator, which refuses the same reference against
// the same declared scope and names both the expression and the roots the scope
// admits. So the reference is never rendered empty whether or not the earlier
// refusal was reachable — the two readings share one walk, which is what makes
// that a property rather than a coincidence.
//
// The earlier refusal, and the condition it names, are proven where a compilation
// is available and the presentation therefore exists.
func TestIntegration_ProvidesSelfContainmentViolation(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec: types.IRSpec{
				Nodes: nodes,
			},
		},
		FeatureOrder:  ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{Aggregate: &types.Aggregate{}},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			provides := types.OrderedMap[string]{}
			provides.Set("host", `{{ .context.inject.env.TARGET }}`)
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "bad-recipe"},
				Spec: types.RecipeSpec{
					Provides:      provides,
					ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest"}`},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatal("expected error for provides self-containment violation")
	}
	if !errors.Is(err, renderer.ErrProvidesEvaluation()) {
		t.Errorf("expected the provides-evaluation halt, got: %v", err)
	}
	// The refusal has to be actionable from the diagnostic alone: the expression
	// that reaches outside the data, and what the surface does define. Read over the
	// whole chain, because the outermost wrap names the pass and the cause names the
	// reference.
	var chain []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		chain = append(chain, e.Error())
	}
	text := strings.Join(chain, " | ")
	for _, want := range []string{".context.inject.env.TARGET", ".context.instance_id"} {
		if !strings.Contains(text, want) {
			t.Errorf("the halt omits %q, so the reference is refused without saying what to write instead: %s", want, text)
		}
	}
}

func TestIntegration_InjectMappingDeepMerge(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", BoundaryPath: "local"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test"},
			Spec: types.IRSpec{
				Nodes: nodes,
				Edges: []types.IREdge{
					{Source: "api", Target: "db", Socket: "databases", Alias: "main-db",
						Inject: func() types.OrderedMap[types.OrderedMap[string]] {
							env := types.OrderedMap[string]{}
							env.Set("DB_URL", "host")
							m := types.OrderedMap[types.OrderedMap[string]]{}
							m.Set("env", env)
							return m
						}()},
				},
				ValuesMapping: types.OrderedMap[json.RawMessage]{},
				FeatureValuesMapping: func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
					inner := types.OrderedMap[types.IRFeatureEntry]{}
					inner.Set("dns-resolver", types.IRFeatureEntry{Type: "core/dns-resolver"})
					outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
					outer.Set("api", inner)
					return outer
				}(),
			},
		},
		FeatureOrder: ir.FeatureOrder{"api": {"dns-resolver"}},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
	// Add db node for provides
	input.Graph.Spec.Nodes.Set("db", types.IRNode{Component: "core/datastore/postgres"})

	lookup := renderer.RecipeLookup{
		Recipe: func(_, ct string) (*types.Recipe, bool) {
			switch ct {
			case "core/compute/api-server":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: api:latest
    variables:
      DB_URL: "{{ .context.inject.env.DB_URL }}"
`},
					},
				}, true
			case "core/datastore/postgres":
				provides := types.OrderedMap[string]{}
				provides.Set("host", "original-host.svc")
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "pg-recipe"},
					Spec: types.RecipeSpec{
						Provides: provides,
						Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: pg:15
`},
					},
				}, true
			default:
				return nil, false
			}
		},
		FeatureRecipe: func(_, ft string) (*types.FeatureRecipe, bool) {
			if ft == "core/dns-resolver" {
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "dns-resolver-recipe"},
					Spec: types.FeatureRecipeSpec{
						InjectMapping: `{"env": {"DB_URL": "rewritten-by-feature.svc.cluster.local"}}`,
					},
				}, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The injectMapping should have rewritten DB_URL
	for path, content := range result.Files {
		if strings.Contains(path, "api") && strings.Contains(path, "deployment") {
			if strings.Contains(string(content.Content), "rewritten-by-feature.svc.cluster.local") {
				return // success — Phase A deep-merge worked
			}
			t.Errorf("expected rewritten DB_URL in api deployment, got:\n%s", string(content.Content))
			return
		}
	}
	t.Error("no api deployment found")
}

func TestIntegration_AggregaLabelsPresent(t *testing.T) {
	t.Parallel()
	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels, BoundaryPath: "prod/eks", ResolvedVersion: "1.0.0"})

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "labeled-app"},
			Spec: types.IRSpec{
				Nodes: nodes,

				Provenance: types.IRProvenance{CompilerVersion: "0.2.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "labeled-app"}},
			Release:       &types.Release{Metadata: types.ReleaseMetadata{Name: "prod", Environment: "production"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "r"},
				Spec: types.RecipeSpec{Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:latest
`}},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for path, content := range result.Files {
		if strings.Contains(path, "deployment") {
			s := string(content.Content)
			checks := []string{
				"aggrega.ai/component-id: api",
				"aggrega.ai/boundary-path: prod/eks",
				"aggrega.ai/compiler-version: 0.2.0",
				"aggrega.ai/aggregate-name: labeled-app",
				"aggrega.ai/environment: production",
			}
			for _, check := range checks {
				if !strings.Contains(s, check) {
					t.Errorf("deployment missing label: %s\n%s", check, s)
				}
			}
			return
		}
	}
	t.Error("no deployment found")
}

// TestIntegration_ValuesAndInfraFlow proves a values/infra-flow correctness
// invariant: .values from the Aggregate and .infra from the Release boundary
// must flow through the valuesMapping template to reach the rendered K8s manifest.
func TestIntegration_ValuesAndInfraFlow(t *testing.T) {
	t.Parallel()

	valuesRaw, _ := json.Marshal(map[string]any{"port": 8080})
	infraRaw, _ := json.Marshal(map[string]any{"replicas": 3})
	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels, BoundaryPath: "local"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("api", valuesRaw)
	infraMapping := types.OrderedMap[json.RawMessage]{}
	infraMapping.Set("api", infraRaw)

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "flow-test"},
			Spec: types.IRSpec{
				Nodes: nodes,

				ValuesMapping: valuesMapping,
				InfraMapping:  infraMapping,
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "flow-test"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "api-recipe"},
				Spec: types.RecipeSpec{
					// Synthesised Workload templates read .values.port and
					// .infra.replicas — the critical values/infra flow.
					Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
replicas: {{ .infra.replicas }}
containers:
  main:
    image: api:latest
    ports:
      web: {{ .values.port }}
`},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for path, content := range result.Files {
		if strings.Contains(path, "api") && strings.Contains(path, "deployment") {
			s := string(content.Content)
			if !strings.Contains(s, "containerPort: 8080") {
				t.Errorf("expected containerPort: 8080 (from .values.port), got:\n%s", s)
			}
			if !strings.Contains(s, "replicas: 3") {
				t.Errorf("expected replicas: 3 (from .infra.replicas), got:\n%s", s)
			}
			return
		}
	}
	t.Error("no api deployment found")
}

// captureComposerEngine implements both renderer.Provider and
// renderer.BoundaryAssembler; AssembleBoundary records the per-node intermediates
// it receives so the test can assert which EmittedFile entries the
// orchestrator routed via the per-(path, provider) intermediates pipe.
type captureComposerEngine struct {
	captured []renderer.EmittedFile
}

func (c *captureComposerEngine) Render(_ context.Context, _ map[string]any, _ renderer.NodeRenderContext) ([]renderer.RenderedFile, error) {
	return nil, nil
}

// Capabilities: permissive so the central gate admits this fake for the
// artifact-routing test (which exercises Intermediate routing via the
// BoundaryAssembler, not the input-kind gate). Real compose declining
// artifacts-only is asserted in the provider-capability-matrix test against
// the production engines.
func (c *captureComposerEngine) Contract() renderer.ProviderContract {
	return renderer.ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

func (c *captureComposerEngine) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string]renderer.AssembledFile, bool, error) {
	for _, files := range info.IntermediatesByNode {
		c.captured = append(c.captured, files...)
	}
	return map[string]renderer.AssembledFile{}, false, nil
}

// BoundaryAssembly marks this fake as a boundary-assembling provider that merges
// per-node fragments, so the orchestrator classifies its per-node output
// Intermediate=true and routes it to AssembleBoundary — the equivalent of the
// production compose guest's self-report.
func (c *captureComposerEngine) BoundaryAssembly() string { return "merge" }

// ArtifactTemplateDelimiters and EmitArtifacts satisfy renderer.ArtifactsEmitter,
// mirroring the Wasm adapter: the host builds the projection and this fake places
// each entry at its name, marking Intermediate from its own boundary-assembly
// self-report (merge ⇒ intermediate) so artifacts route via AssembleBoundary.
func (c *captureComposerEngine) ArtifactTemplateDelimiters() (string, string) { return "{{", "}}" }

func (c *captureComposerEngine) EmitArtifacts(_ context.Context, proj renderer.ArtifactsProjection, _ renderer.NodeRenderContext) ([]renderer.EmittedFile, error) {
	intermediate := renderer.RenderIsIntermediate(c)
	files := make([]renderer.EmittedFile, 0, len(proj.Entries))
	for _, ent := range proj.Entries {
		origin := renderer.EmitOriginArtifactsInline
		if ent.Origin == "import" {
			origin = renderer.EmitOriginArtifactsImport
		}
		files = append(files, renderer.EmittedFile{Name: ent.Name, Origin: origin, Body: ent.Content, Intermediate: intermediate})
	}
	return files, nil
}

// TestPhaseB3_ArtifactsIntermediate_RoutesViaBoundaryAssembler asserts that
// artifacts from a BoundaryAssembler-engine Recipe — whether inline or
// file-loaded — are routed by the orchestrator's post-Phase-B grouping into
// the per-(path, provider) intermediates pipe and arrive at AssembleBoundary
// through BoundaryInfo. The grouping is exercised end-to-end (RenderAll entry
// through AssembleBoundary's input); the assertion verifies that the routing
// is Origin-agnostic — both EmitOriginArtifactsInline and
// EmitOriginArtifactsImport EmittedFiles reach AssembleBoundary just like
// engine.Render's EmitOriginSource output does.
func TestPhaseB3_ArtifactsIntermediate_RoutesViaBoundaryAssembler(t *testing.T) {
	t.Parallel()

	fragmentBody := `services:
  sidecar:
    image: busybox:1.36
`

	type variant struct {
		name       string
		filename   string
		buildSpec  func(t *testing.T) types.RecipeSpec
		wantOrigin renderer.EmitOrigin
	}
	variants := []variant{
		{
			name:     "inline",
			filename: "extra-services.yaml",
			buildSpec: func(*testing.T) types.RecipeSpec {
				artifacts := types.OrderedMap[types.ArtifactEntry]{}
				artifacts.Set("extra-services.yaml", types.ArtifactEntry{Inline: fragmentBody})
				return types.RecipeSpec{Capability: "compose", Artifacts: artifacts}
			},
			wantOrigin: renderer.EmitOriginArtifactsInline,
		},
		{
			name:     "from-file",
			filename: "from-file-services.yaml",
			buildSpec: func(t *testing.T) types.RecipeSpec {
				dir := t.TempDir()
				p := filepath.Join(dir, "from-file-services.yaml")
				if err := os.WriteFile(p, []byte(fragmentBody), 0o644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				artifacts := types.OrderedMap[types.ArtifactEntry]{}
				artifacts.Set("from-file-services.yaml", types.ArtifactEntry{Import: p})
				return types.RecipeSpec{Capability: "compose", Artifacts: artifacts}
			},
			wantOrigin: renderer.EmitOriginArtifactsImport,
		},
	}

	for _, v := range variants {
		v := v
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()

			nodes := types.OrderedMap[types.IRNode]{}
			nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "default"})

			spec := v.buildSpec(t)
			input := renderer.RendererInput{
				Graph: &types.IRGraph{
					Metadata: types.IRMetadata{AggregateName: "compose-app"},
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
					return &types.Recipe{
						Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
						Spec:     spec,
					}, true
				},
				FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
			}

			capture := &captureComposerEngine{}
			factory := func(string) (renderer.Provider, error) { return capture, nil }

			if _, err := renderer.RenderAll(context.Background(), input, lookup, factory); err != nil {
				t.Fatalf("RenderAll: %v", err)
			}

			var found bool
			for _, f := range capture.captured {
				if f.Origin == v.wantOrigin && f.Name == v.filename {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected a %v EmittedFile named %q to reach AssembleBoundary via the per-boundary intermediates pipe; captured = %+v",
					v.wantOrigin, v.filename, capture.captured)
			}
		})
	}
}
