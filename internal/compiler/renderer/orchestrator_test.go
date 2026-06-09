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
	renderHelm "github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helm"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// testHelmFactory returns an EngineFactory that always returns the helm
// engine — used by tests that predate the per-node factory dispatch.
// The helm engine supports sourceless rendering for orchestration unit
// tests' purposes; the helm-chart provider's sourceless path is no
// longer supported.
func testHelmFactory() renderer.EngineFactory {
	return func(string) (renderer.Engine, error) {
		return &renderHelm.HelmEngine{}, nil
	}
}

func testInput() renderer.RendererInput {
	labels := types.OrderedMap[string]{}
	labels.Set("component.aggrega.ai/type", "server")
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/server/api-server", Labels: labels, ResolvedVersion: "1.0.0", BoundaryPath: "local/default"})
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
			if componentType == "core/server/api-server" {
				provides := types.OrderedMap[string]{}
				provides.Set("host", `{{ .context.instance_id }}.svc.cluster.local`)
				provides.Set("service_port", `8080`)
				locals := types.OrderedMap[string]{}
				locals.Set("primary_compute", "api")
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:   "helm",
						Implements: "core/server/api-server",
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

	result, err := renderer.RenderAll(context.Background(), testInput(), testRecipeLookup(), testHelmFactory())
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

func TestRenderAll_ProvidesResolved(t *testing.T) {
	t.Parallel()

	input := testInput()
	dbLabels := types.OrderedMap[string]{}
	dbLabels.Set("component.aggrega.ai/type", "stateful-server")
	input.Graph.Spec.Nodes.Set("db", types.IRNode{
		Component: "core/database/postgres",
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
			case "core/server/api-server":
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
			case "core/database/postgres":
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

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for path, content := range result.Files {
		if strings.Contains(path, "api") && strings.Contains(path, "deployment") {
			if strings.Contains(string(content), "db.svc.cluster.local") {
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

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
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
	input.Graph.Spec.Nodes.Set("a-db", types.IRNode{Component: "core/database/postgres"})

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
		result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
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
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:   "helm",
						Implements: "core/server/api-server",
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
				locals := types.OrderedMap[string]{}
				locals.Set("scrape_target", `{{ .values.tier }}-{{ .values.scrape_port }}`)
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "mon"},
					Spec: types.FeatureRecipeSpec{
						Provider:      "helm-chart",
						Implements:    "core/monitoring",
						Locals:        locals,
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "prom/mon:v1", "ports": ["{{ .values.scrape_port }}:9090"]}`},
					},
				}, true
			}
			return nil, false
		},
	}

	// The monitoring FeatureRecipe here is values-only (no source): under the
	// sourced-only FeatureRecipe contract it is a resource-generating FR with no
	// render surface, so the build halts loudly. (A sourceless FR could produce
	// its own resources only when FeatureRecipes gain synthesised/artifacts
	// rendering.) A FeatureRecipe never synthesises a Deployment from bare
	// values — that would invent compute the author never declared.
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	if err == nil {
		t.Fatal("expected a loud error for a values-only resource-generating FeatureRecipe, got nil")
	}
	if !strings.Contains(err.Error(), "has no render surface") {
		t.Errorf("expected a 'no render surface' catalog error, got: %v", err)
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
			if componentType == "core/server/api-server" {
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
						Provider:      "helm-chart",
						InjectMapping: `{"env": {"RESOLVER": "corefile"}}`,
					},
				}, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
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
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:      "helm-chart",
						Implements:    "core/server/api-server",
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "myapp:latest", "ports": ["8080:8080"]}`},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
			if featureType == "core/db-password" {
				// Credential FR: its valuesMapping references .secret_name AND
				// the top-level .inject map populated by buildSecretInject.
				return &types.FeatureRecipe{
					Metadata: types.FeatureRecipeMetadata{Name: "db-password"},
					Spec: types.FeatureRecipeSpec{
						Provider:      "helm-chart",
						Implements:    "core/db-password",
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "pw-gen:v1", "environment": {"SECRET_NAME": "{{ .secret_name }}", "BINDING": "{{ .inject.env.DB_PASSWORD }}"}}`},
					},
				}, true
			}
			return nil, false
		},
	}

	// The credential FeatureRecipe here is values-only (no source): under the
	// sourced-only FeatureRecipe contract it is a resource-generating FR with no
	// render surface, so the build halts loudly rather than synthesising a
	// Deployment from bare values. (A credential FR could generate its own
	// resources from a workload spec only when FeatureRecipes gain
	// synthesised/artifacts rendering.)
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	if err == nil {
		t.Fatal("expected a loud error for a values-only credential FeatureRecipe, got nil")
	}
	if !strings.Contains(err.Error(), "has no render surface") {
		t.Errorf("expected a 'no render surface' catalog error, got: %v", err)
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
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:   "helm",
						Implements: "core/server/api-server",
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
				return &types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: "cache"}, Spec: types.FeatureRecipeSpec{Provider: "helm", Implements: "core/cache", Provides: p}}, true
			case "core/db-password":
				p := types.OrderedMap[string]{}
				p.Set("root_password", `{{ .values.generated }}`)
				return &types.FeatureRecipe{Metadata: types.FeatureRecipeMetadata{Name: "db-password"}, Spec: types.FeatureRecipeSpec{Provider: "helm", Implements: "core/db-password", Provides: p}}, true
			}
			return nil, false
		},
	}

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
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
	body := string(result.Files[apiDep])
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

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
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

func fileKeys(m map[string][]byte) []string {
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
		Metadata: types.ComponentMetadata{Name: "core/server/api-server"},
		Spec: types.ComponentSpec{
			Assertions: buildOrderedMap(map[string]string{
				"test_assert": `{{ fail "component-assertion-smoke-fired" }}`,
			}),
		},
	}
	catalogLookup := resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/server/api-server" {
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
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe-failing"},
					Spec: types.RecipeSpec{
						Provider:      "helm",
						Implements:    "core/server/api-server",
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

	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
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
