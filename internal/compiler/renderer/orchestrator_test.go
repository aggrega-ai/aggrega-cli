// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// testHelmFactory returns an EngineFactory backed by the embedded provider
// guests — used by orchestration tests that render synthesised or artifacts-only
// recipes through the production Wasm render path. It routes each provider name
// to its embedded guest, so a helm node renders through the helm guest and a
// mixed-provider graph resolves each provider independently.
func testHelmFactory(t *testing.T) renderer.EngineFactory {
	return wasmEngineFactory(t)
}

func testInput() renderer.RendererInput {
	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: labels, ResolvedVersion: "1.0.0", BoundaryPath: "local/default"})
	valuesMapping := types.OrderedMap[json.RawMessage]{}
	valuesMapping.Set("api", json.RawMessage(`{"port":8080}`))
	return renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "test-app"},
			Spec: types.IRSpec{
				Nodes: nodes,

				Edges:          nil,
				ValuesMapping:  valuesMapping,
				InfraMapping:   types.OrderedMap[json.RawMessage]{},
				FeatureMarkers: nil,
				Provenance:     types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate: &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app"}},
			Release: &types.Release{
				Metadata: types.ReleaseMetadata{Name: "test-release", Version: "1.0.0", Environment: "local"},
			},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
	}
}

func testRecipeLookup() renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				provides := types.OrderedMap[string]{}
				provides.Set("host", `{{ .context.instance_id }}.svc.cluster.local`)
				provides.Set("service_port", `8080`)
				locals := types.OrderedMap[string]{}
				locals.Set("primary_compute", "api")
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Capability: "helm",
						Implements: "core/compute/api-server",
						Provides:   provides,
						Locals:     locals,
						// Synthesised mode (a Workload) is the real compute path
						// for a helm Recipe; the removed renderSourceless
						// synth-from-bare-values path is gone (Invariant 4).
						Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: myapp:latest
    ports:
      web: 8080
`},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			return nil, false
		},
	}
}

func TestRenderAll_SimpleNode(t *testing.T) {
	t.Parallel()

	result, err := renderer.RenderAll(context.Background(), testInput(), testRecipeLookup(), testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Files) == 0 {
		t.Fatal("expected files")
	}

	foundDep := false
	for path := range result.Files {
		if strings.Contains(path, "boundaries/local/default/") && strings.Contains(path, "deployment.yaml") {
			foundDep = true
		}
	}
	if !foundDep {
		t.Errorf("expected boundary-prefixed deployment, got keys: %v", fileKeys(result.Files))
	}
}

// TestRenderAll_ArtifactsOnlyRecipeRendersViaHelm is the end-to-end proof that
// artifacts-only is a first-class input kind: a Recipe declaring only artifacts:
// (no source:, no workload:) on a provider that supports standalone artifacts
// (helm) renders by emitting its declared artifact files — templated against the
// node context — and synthesises NO primary workload. No ghost compute.
func TestRenderAll_ArtifactsOnlyRecipeRendersViaHelm(t *testing.T) {
	t.Parallel()
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("networkpolicy.yaml", types.ArtifactEntry{Inline: `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: api-netpol-{{ .values.port }}
spec:
  podSelector: {}
  policyTypes:
    - Ingress
`})
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "netpol-recipe"},
					Spec: types.RecipeSpec{
						Capability: "helm",
						Implements: "core/compute/api-server",
						Artifacts:  art,
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), testInput(), lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var artFile string
	for k := range result.Files {
		if strings.Contains(k, "networkpolicy.yaml") {
			artFile = k
		}
		if strings.Contains(k, "deployment.yaml") || strings.Contains(k, "service.yaml") {
			t.Errorf("artifacts-only node must not synthesise a primary workload, got: %s", k)
		}
	}
	if artFile == "" {
		t.Fatalf("expected the declared artifact to be emitted, got keys: %v", fileKeys(result.Files))
	}
	body := string(result.Files[artFile].Content)
	if !strings.Contains(body, "kind: NetworkPolicy") {
		t.Errorf("artifact body not rendered, got:\n%s", body)
	}
	// The artifact template was evaluated against the node context (.values.port).
	if !strings.Contains(body, "api-netpol-8080") {
		t.Errorf("artifact template not evaluated against node values (.values.port), got:\n%s", body)
	}
}

// TestRenderAll_ArtifactsOnlyDeclinedByIncapableProvider pins the decline end to
// end: an artifacts-only Recipe on a provider that does not support standalone
// artifacts (compose, helm-chart) halts at the central input-kind gate with the
// uniform capability diagnostic — not a silent skip or a bespoke per-engine
// message. Uses wasmEngineFactory(t) so each provider name resolves to its
// real engine and its declared capabilities.
func TestRenderAll_ArtifactsOnlyDeclinedByIncapableProvider(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"compose", "helm-chart"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			art := types.OrderedMap[types.ArtifactEntry]{}
			art.Set("cm.yaml", types.ArtifactEntry{Inline: "kind: ConfigMap\n"})
			lookup := renderer.RecipeLookup{
				Recipe: func(_, componentType string) (*types.Recipe, bool) {
					if componentType == "core/compute/api-server" {
						return &types.Recipe{
							Metadata: types.RecipeMetadata{Name: "art-recipe"},
							Spec: types.RecipeSpec{
								Capability: provider,
								Implements: "core/compute/api-server",
								Artifacts:  art,
							},
						}, true
					}
					return nil, false
				},
				FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
			}
			_, err := renderer.RenderAll(context.Background(), testInput(), lookup, wasmEngineFactory(t))
			if err == nil {
				t.Fatalf("expected artifacts-only to be declined by provider %q, got nil", provider)
			}
			if !strings.Contains(err.Error(), "does not support artifacts-only") {
				t.Errorf("expected the uniform input-kind gate diagnostic, got: %v", err)
			}
		})
	}
}

func TestRenderAll_ProvidesResolved(t *testing.T) {
	t.Parallel()

	input := testInput()
	dbLabels := types.OrderedMap[string]{}
	dbLabels.Set("component.aggrega.ai/type", "stateful-server")
	input.Graph.Spec.Nodes.Set("db", types.IRNode{
		Component: "core/datastore/postgres",
		Labels:    dbLabels,
	})
	envInj := types.OrderedMap[string]{}
	envInj.Set("DB_HOST", "host")
	edgeInj := types.OrderedMap[types.OrderedMap[string]]{}
	edgeInj.Set("env", envInj)
	input.Graph.Spec.Edges = []types.IREdge{
		{Source: "api", Target: "db", Socket: "databases", Alias: "main-db", Inject: edgeInj},
	}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			switch componentType {
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
      DB_HOST: "{{ .context.inject.env.DB_HOST }}"
`},
					},
				}, true
			case "core/datastore/postgres":
				provides := types.OrderedMap[string]{}
				provides.Set("host", `{{ .context.instance_id }}.svc.cluster.local`)
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "pg-recipe"},
					Spec: types.RecipeSpec{
						Provides: provides,
						Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: postgres:15
`},
					},
				}, true
			default:
				return nil, false
			}
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for path, content := range result.Files {
		if strings.Contains(path, "api") && strings.Contains(path, "deployment") {
			if strings.Contains(string(content.Content), "db.svc.cluster.local") {
				return
			}
		}
	}
	t.Error("expected DB_HOST=db.svc.cluster.local in api deployment")
}

func TestRenderAll_PhaseCWarning(t *testing.T) {
	t.Parallel()

	input := testInput()
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "pipeline-recipe"},
				Spec: types.RecipeSpec{
					ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest"}`},
					Pipeline: []types.PipelineStep{
						{ID: "step1", Plugin: "envoy-sidecar.wasm", Function: "inject"},
					},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected Phase C pipeline warning")
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "not executed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing pipeline warning in %v", result.Warnings)
	}
}

func TestRenderAll_Deterministic(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.Nodes.Set("z-worker", types.IRNode{Component: "core/worker"})
	input.Graph.Spec.Nodes.Set("a-db", types.IRNode{Component: "core/datastore/postgres"})

	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "generic"},
				Spec:     types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest"}`}},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	var firstKeys []string
	for i := 0; i < 10; i++ {
		result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		keys := fileKeys(result.Files)
		if i == 0 {
			firstKeys = keys
		} else {
			for j, k := range keys {
				if k != firstKeys[j] {
					t.Errorf("run %d: file key changed at %d: %q vs %q", i, j, k, firstKeys[j])
				}
			}
		}
	}
}

func TestRenderAll_FeatureRecipeResourceGeneration(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.FeatureValuesMapping = func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
		vals := types.OrderedMap[any]{}
		vals.Set("scrape_port", float64(9090))
		vals.Set("tier", "prod")
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("mon", types.IRFeatureEntry{
			Type:   "core/monitoring",
			Values: vals,
		})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		return outer
	}()
	input.FeatureOrder = ir.FeatureOrder{"api": []string{"mon"}}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
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
    ports:
      web: 8080
`},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/monitoring" {
				// A synthesised FeatureRecipe: it declares workload: and the
				// helm provider synthesises its own compute (a Deployment)
				// under features/mon/, named after the feature instance.
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "mon"},
					Spec: types.FeatureRecipeSpec{
						Capability: "helm",
						Implements: "core/monitoring",
						Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: prom/mon:v1
    ports:
      metrics: {{ .values.scrape_port }}
`},
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

	// The synthesised FR Deployment lands under features/mon/, named for the
	// feature instance ("mon") — not the host node ("api") — so it never
	// collides with the host's own synthesised workload.
	var frDep string
	for k := range result.Files {
		if strings.Contains(k, "features/mon/") && strings.Contains(k, "deployment.yaml") {
			frDep = k
			break
		}
	}
	if frDep == "" {
		t.Fatalf("missing synthesised FR deployment under features/mon/, keys: %v", fileKeys(result.Files))
	}
	if !strings.Contains(frDep, "features/mon/mon-") {
		t.Errorf("FR deployment should be named for the instance (mon-*), got %q", frDep)
	}
	body := string(result.Files[frDep].Content)
	if !strings.Contains(body, "prom/mon:v1") {
		t.Errorf("FR deployment missing FR image, got:\n%s", body)
	}
	if !strings.Contains(body, "name: mon") {
		t.Errorf("FR deployment metadata.name should be the instance (mon), got:\n%s", body)
	}
}

func TestRenderAll_InjectOnlyFRProducesNoExtraFiles(t *testing.T) {
	t.Parallel()

	input := testInput()
	input.Graph.Spec.FeatureValuesMapping = func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("dns", types.IRFeatureEntry{Type: "core/dns-resolver"})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		return outer
	}()
	input.FeatureOrder = ir.FeatureOrder{"api": []string{"dns"}}

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec:     types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest"}`}},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/dns-resolver" {
				// Inject-only FR (only injectMapping, no source/valuesMapping) — must NOT generate resources.
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "dns"},
					Spec: types.FeatureRecipeSpec{
						Capability:    "helm-chart",
						InjectMapping: `{"env": {"RESOLVER": "corefile"}}`,
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
	for k := range result.Files {
		if strings.Contains(k, "features/") {
			t.Errorf("inject-only FR must not produce resource files, found: %s", k)
		}
	}
}

func TestRenderAll_CredentialFeatureRecipeRendering(t *testing.T) {
	t.Parallel()

	input := testInput()
	// Attach a credential secret to the api node.
	input.Graph.Spec.Secrets = func() types.OrderedMap[types.OrderedMap[types.IRSecretEntry]] {
		vals := types.OrderedMap[any]{}
		vals.Set("length", float64(32))
		envInj := types.OrderedMap[string]{}
		envInj.Set("DB_PASSWORD", "ref:root_password")
		inj := types.OrderedMap[types.OrderedMap[string]]{}
		inj.Set("env", envInj)
		inner := types.OrderedMap[types.IRSecretEntry]{}
		inner.Set("root_password", types.IRSecretEntry{
			Type:   "core/db-password",
			Values: vals,
			Inject: inj,
		})
		outer := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
		outer.Set("api", inner)
		return outer
	}()

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Capability:    "helm-chart",
						Implements:    "core/compute/api-server",
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "myapp:latest", "ports": ["8080:8080"]}`},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/db-password" {
				// Credential FR with artifacts: it emits a Secret manifest
				// (an artifact-shaped resource) referencing .secret_name and
				// the top-level .inject map populated by buildSecretInject.
				art := types.OrderedMap[types.ArtifactEntry]{}
				art.Set("secret.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: Secret
metadata:
  name: {{ .secret_name }}
stringData:
  binding: "{{ .inject.env.DB_PASSWORD }}"
`})
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "db-password"},
					Spec: types.FeatureRecipeSpec{
						Capability: "helm",
						Implements: "core/db-password",
						Artifacts:  art,
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

	// The credential FR artifact lands under secrets/<secretName>/.
	var secretArt string
	for k := range result.Files {
		if strings.Contains(k, "secrets/root_password/") && strings.Contains(k, "secret.yaml") {
			secretArt = k
			break
		}
	}
	if secretArt == "" {
		t.Fatalf("missing credential FR artifact under secrets/root_password/, keys: %v", fileKeys(result.Files))
	}
	body := string(result.Files[secretArt].Content)
	// .secret_name propagates into the rendered manifest.
	if !strings.Contains(body, "name: root_password") {
		t.Errorf("expected .secret_name=root_password in rendered manifest, got:\n%s", body)
	}
	// .inject top-level was built from the IR secret entry's inject block.
	if !strings.Contains(body, "ref:root_password") {
		t.Errorf("expected .inject.env.DB_PASSWORD=ref:root_password in rendered manifest, got:\n%s", body)
	}
}

// monitoringFeatureInput builds a RendererInput with a single "mon" feature
// (core/monitoring) attached to node "api", carrying a scrape_port value.
// Shared by the FeatureRecipe composed-kind and gate tests.
func monitoringFeatureInput(t *testing.T) renderer.RendererInput {
	t.Helper()
	input := testInput()
	input.Graph.Spec.FeatureValuesMapping = func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
		vals := types.OrderedMap[any]{}
		vals.Set("scrape_port", float64(9090))
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("mon", types.IRFeatureEntry{Type: "core/monitoring", Values: vals})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		return outer
	}()
	input.FeatureOrder = ir.FeatureOrder{"api": []string{"mon"}}
	return input
}

// hostWorkloadRecipeLookup returns a RecipeLookup whose host api-server Recipe
// synthesises a trivial workload, with featureRecipe supplying the monitoring
// FR under test.
func hostWorkloadRecipeLookup(fr *types.FeatureRecipe) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
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
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/monitoring" {
				return fr, true
			}
			return nil, false
		},
	}
}

// TestRenderAll_FeatureRecipeComposedWorkloadAndArtifacts proves a FeatureRecipe
// composes input kinds exactly as a Recipe does: a single FR declaring both
// workload: and artifacts: emits the synthesised Deployment AND the artifact,
// both under features/<instance>/.
func TestRenderAll_FeatureRecipeComposedWorkloadAndArtifacts(t *testing.T) {
	t.Parallel()
	input := monitoringFeatureInput(t)
	art := types.OrderedMap[types.ArtifactEntry]{}
	art.Set("scrape-config.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: scrape-config
data:
  port: "{{ .values.scrape_port }}"
`})
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm",
			Implements: "core/monitoring",
			Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: prom/mon:v1
`},
			Artifacts: art,
		},
	}
	result, err := renderer.RenderAll(context.Background(), input, hostWorkloadRecipeLookup(fr), testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundWorkload, foundArtifact := false, false
	for k := range result.Files {
		if !strings.Contains(k, "features/mon/") {
			continue
		}
		if strings.Contains(k, "deployment.yaml") {
			foundWorkload = true
		}
		if strings.Contains(k, "scrape-config.yaml") {
			foundArtifact = true
		}
	}
	if !foundWorkload {
		t.Errorf("composed FR missing synthesised workload under features/mon/, keys: %v", fileKeys(result.Files))
	}
	if !foundArtifact {
		t.Errorf("composed FR missing artifact under features/mon/, keys: %v", fileKeys(result.Files))
	}
}

// TestRenderAll_FeatureRecipeUnsupportedInputKindGated proves the FeatureRecipe
// render path runs the same central input-kind gate the component path does: a
// FR declaring workload: bound to the helm-chart provider (which does not
// implement workload synthesis) halts loudly with the uniform capability
// diagnostic — not a silent skip or an engine internal error.
func TestRenderAll_FeatureRecipeUnsupportedInputKindGated(t *testing.T) {
	t.Parallel()
	input := monitoringFeatureInput(t)
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "mon"},
		Spec: types.FeatureRecipeSpec{
			Capability: "helm-chart",
			Implements: "core/monitoring",
			Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: prom/mon:v1
`},
		},
	}
	// wasmEngineFactory(t) routes helm-chart to the real helm-chart engine,
	// whose Capabilities report Sourced only.
	_, err := renderer.RenderAll(context.Background(), input, hostWorkloadRecipeLookup(fr), wasmEngineFactory(t))
	if err == nil {
		t.Fatal("expected a gate error for a synthesised FR on a sourced-only provider, got nil")
	}
	if !strings.Contains(err.Error(), "does not support synthesised") {
		t.Errorf("expected the uniform input-kind gate diagnostic, got: %v", err)
	}
}

// TestRenderAll_AttachmentInjectReachesConsumerRenderedOutput is the end-to-end
// proof for top-level feature/secret inject delivery: an authored secret.inject and
// feature.inject (provides-key references) resolve against each attachment's own
// FeatureRecipe provides and reach the host component's .context.inject, so the host
// Recipe rendering .context.inject.env.<KEY> emits the delivered values into its
// output manifest. Inspected as real rendered output, both surfaces.
func TestRenderAll_AttachmentInjectReachesConsumerRenderedOutput(t *testing.T) {
	t.Parallel()

	input := testInput()
	// Non-credential feature attachment with an authored top-level inject.
	input.Graph.Spec.FeatureValuesMapping = func() types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]] {
		fvals := types.OrderedMap[any]{}
		fvals.Set("host", "redis.svc.cluster.local")
		fenv := types.OrderedMap[string]{}
		fenv.Set("CACHE_URL", "url")
		finj := types.OrderedMap[types.OrderedMap[string]]{}
		finj.Set("env", fenv)
		inner := types.OrderedMap[types.IRFeatureEntry]{}
		inner.Set("cache", types.IRFeatureEntry{Type: "core/cache", Values: fvals, Inject: finj})
		outer := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
		outer.Set("api", inner)
		return outer
	}()
	input.FeatureOrder = ir.FeatureOrder{"api": []string{"cache"}}
	// Credential secret attachment with an authored top-level inject.
	input.Graph.Spec.Secrets = func() types.OrderedMap[types.OrderedMap[types.IRSecretEntry]] {
		svals := types.OrderedMap[any]{}
		svals.Set("generated", "s3cr3t")
		senv := types.OrderedMap[string]{}
		senv.Set("DB_PASSWORD", "root_password")
		sinj := types.OrderedMap[types.OrderedMap[string]]{}
		sinj.Set("env", senv)
		inner := types.OrderedMap[types.IRSecretEntry]{}
		inner.Set("root_password", types.IRSecretEntry{Type: "core/db-password", Values: svals, Inject: sinj})
		outer := types.OrderedMap[types.OrderedMap[types.IRSecretEntry]]{}
		outer.Set("api", inner)
		return outer
	}()

	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Capability: "helm",
						Implements: "core/compute/api-server",
						Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: app:latest
    variables:
      DB_PASSWORD: "{{ .context.inject.env.DB_PASSWORD }}"
      CACHE_URL: "{{ .context.inject.env.CACHE_URL }}"
`},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			switch featureType {
			case "core/cache":
				p := types.OrderedMap[string]{}
				p.Set("url", `{{ .values.host }}`)
				return &types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: "cache"}, Spec: types.FeatureRecipeSpec{Capability: "helm", Implements: "core/cache", Provides: p}}, true
			case "core/db-password":
				p := types.OrderedMap[string]{}
				p.Set("root_password", `{{ .values.generated }}`)
				return &types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: "db-password"}, Spec: types.FeatureRecipeSpec{Capability: "helm", Implements: "core/db-password", Provides: p}}, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	var apiDep string
	for k := range result.Files {
		if strings.Contains(k, "api") && strings.Contains(k, "deployment") {
			apiDep = k
			break
		}
	}
	if apiDep == "" {
		t.Fatalf("no api deployment rendered; keys: %v", fileKeys(result.Files))
	}
	body := string(result.Files[apiDep].Content)
	if !strings.Contains(body, "s3cr3t") {
		t.Errorf("secret.inject did not reach consumer rendered output: want DB_PASSWORD=s3cr3t in:\n%s", body)
	}
	if !strings.Contains(body, "redis.svc.cluster.local") {
		t.Errorf("feature.inject did not reach consumer rendered output: want CACHE_URL=redis.svc.cluster.local in:\n%s", body)
	}
}

func TestRenderAll_GhostInfrastructureWarning(t *testing.T) {
	t.Parallel()

	input := testInput()
	// Values has {port: 8080} from testInput(); add an unused infra knob.
	input.Graph.Spec.InfraMapping = func() types.OrderedMap[json.RawMessage] {
		m := types.OrderedMap[json.RawMessage]{}
		m.Set("api", json.RawMessage(`{"replicas":3}`))
		return m
	}()

	// valuesMapping reads ONLY .values.port — ignores .infra.replicas.
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "ghost-recipe"},
				Spec: types.RecipeSpec{
					ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest", "port": {{ .values.port }}}`},
				},
			}, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gotInfra bool
	for _, w := range result.Warnings {
		if strings.Contains(w, ".infra") && strings.Contains(w, "replicas") {
			gotInfra = true
		}
	}
	if !gotInfra {
		t.Errorf("expected a ghost warning for unused .infra.replicas, got: %v", result.Warnings)
	}
}

func fileKeys(m map[string]renderer.AssembledFile) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestStep4b_AssertionFailureHaltsEvaluator pins the wire-in regression
// coverage for Component/Feature assertion evaluation. A Component with
// a `{{ fail "..." }}` assertion fails at Step 4b; the evaluator returns
// an error chain containing ErrAssertionFailed + the fail-call's message
// text. If a future refactor deletes the EvaluateComponentAndFeatureAssertions
// body's actual fail-call invocation, this test catches the regression.
//
// Note on call-site coverage: the production call to
// renderer.EvaluateComponentAndFeatureAssertions lives at
// cmd/aggrega/build.go:188; that call site is not exercised by this
// test (a separate CLI-level smoke test would catch a build.go:188
// deletion). This test exercises the evaluator body + the call-chain
// from a Component-with-failing-assertion through the resolver-level
// CatalogLookup to the per-instance evaluator.
func TestStep4b_AssertionFailureHaltsEvaluator(t *testing.T) {
	t.Parallel()

	input := testInput()

	failingComponent := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/compute/api-server"},
		Spec: types.ComponentSpec{
			Assertions: buildOrderedMap(map[string]string{
				"test_assert": `{{ fail "component-assertion-smoke-fired" }}`,
			}),
		},
	}
	catalogLookup := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/compute/api-server" {
				return failingComponent, true
			}
			return types.Component{}, false
		},
	}

	err := renderer.EvaluateComponentAndFeatureAssertions(input.Graph, catalogLookup)
	if err == nil {
		t.Fatalf("expected ErrAssertionFailed; got nil")
	}
	if !errors.Is(err, renderer.ErrAssertionFailed) {
		t.Errorf("error chain missing ErrAssertionFailed: %v", err)
	}
	if !strings.Contains(err.Error(), "component-assertion-smoke-fired") {
		t.Errorf("error message missing fail-call text: %v", err)
	}
}

// TestRenderAll_Step5bAssertionFailureHaltsPipeline pins the wire-in
// regression coverage for the Step 5b production call at
// orchestrator.go:111. A Recipe with a `{{ fail "..." }}` assertion
// fails inside RenderAll; the orchestrator's cerrors.Wrap surfaces the
// failure with ErrAssertionFailed in the inner chain.
//
// If a future refactor deletes the orchestrator.go:111
// EvaluateRecipeAndFeatureRecipeAssertions call, this test catches the
// regression because RenderAll would complete successfully without
// surfacing the assertion failure.
func TestRenderAll_Step5bAssertionFailureHaltsPipeline(t *testing.T) {
	t.Parallel()

	input := testInput()
	lookup := renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			if componentType == "core/compute/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe-failing"},
					Spec: types.RecipeSpec{
						Capability:    "helm",
						Implements:    "core/compute/api-server",
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "myapp:latest"}`},
						Assertions: buildOrderedMap(map[string]string{
							"recipe_assert": `{{ fail "recipe-assertion-smoke-fired" }}`,
						}),
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			return nil, false
		},
	}

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err == nil {
		t.Fatalf("expected RenderAll to halt with assertion failure; got nil")
	}
	if !errors.Is(err, renderer.ErrAssertionFailed) {
		t.Errorf("error chain missing ErrAssertionFailed: %v", err)
	}
	// The fail-call text is preserved in the inner wrap chain (cerrors.Wrap's
	// outer Error() surface displays only the prefix); walk the chain to
	// verify the inner message survives.
	innerMessage := false
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), "recipe-assertion-smoke-fired") {
			innerMessage = true
			break
		}
	}
	if !innerMessage {
		t.Errorf("error chain missing fail-call text in any cause: %v", err)
	}
}

// buildOrderedMap constructs an OrderedMap[string] from a regular map.
// Iteration order is sorted for deterministic test output.
func buildOrderedMap(m map[string]string) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out.Set(k, m[k])
	}
	return out
}
