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
		Recipe: func(componentType string) (*types.Recipe, bool) {
			if componentType == "core/server/api-server" {
				provides := types.OrderedMap[string]{}
				provides.Set("host", `{{ .context.instance_id }}.svc.cluster.local`)
				provides.Set("service_port", `8080`)
				locals := types.OrderedMap[string]{}
				locals.Set("primary_compute", "api")
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						Provider:      "helm",
						Implements:    "core/server/api-server",
						Provides:      provides,
						Locals:        locals,
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "myapp:latest", "ports": ["8080:8080"], "environment": {"PORT": "8080"}}`},
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
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
		Recipe: func(componentType string) (*types.Recipe, bool) {
			switch componentType {
			case "core/server/api-server":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec: types.RecipeSpec{
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "api:latest", "environment": {"DB_HOST": "{{ .context.inject.env.DB_HOST }}"}}`},
					},
				}, true
			case "core/database/postgres":
				provides := types.OrderedMap[string]{}
				provides.Set("host", `{{ .context.instance_id }}.svc.cluster.local`)
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "pg-recipe"},
					Spec: types.RecipeSpec{
						Provides:      provides,
						ValuesMapping: types.ValuesMapping{Inline: `{"image": "postgres:15"}`},
					},
				}, true
			default:
				return nil, false
			}
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
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
		Recipe: func(componentType string) (*types.Recipe, bool) {
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
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
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
		Recipe: func(string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "generic"},
				Spec:     types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest"}`}},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
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
		Recipe: func(componentType string) (*types.Recipe, bool) {
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
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
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

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	keys := fileKeys(result.Files)

	// Base Recipe files live at the boundary root (flat-helm output;
	// per-instance file naming is "<instance>-<kind>.yaml")
	foundBaseDep := false
	// FR files live under features/<instance>/ off the boundary root
	foundFRDep := false
	frFiles := []string{}
	for _, k := range keys {
		if strings.Contains(k, "boundaries/local/default/helm-chart/api-deployment.yaml") &&
			!strings.Contains(k, "features/") {
			foundBaseDep = true
		}
		if strings.Contains(k, "boundaries/local/default/helm-chart/features/mon/") {
			frFiles = append(frFiles, k)
			if strings.Contains(k, "deployment.yaml") {
				foundFRDep = true
			}
		}
	}
	if !foundBaseDep {
		t.Errorf("missing base Recipe deployment at boundary root, got: %v", keys)
	}
	if !foundFRDep {
		t.Errorf("missing FR deployment under features/mon/, got: %v", keys)
	}
	if len(frFiles) < 2 {
		t.Errorf("expected multiple FR files under features/mon/, got %d: %v", len(frFiles), frFiles)
	}

	// FR rendered image should reflect FR valuesMapping
	for _, k := range frFiles {
		if strings.Contains(k, "deployment.yaml") {
			if !strings.Contains(string(result.Files[k]), "prom/mon:v1") {
				t.Errorf("FR deployment missing FR image, got:\n%s", result.Files[k])
			}
		}
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
		Recipe: func(componentType string) (*types.Recipe, bool) {
			if componentType == "core/server/api-server" {
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe"},
					Spec:     types.RecipeSpec{ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest"}`}},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
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
		Recipe: func(componentType string) (*types.Recipe, bool) {
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
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
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

	result, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Credential FR files live under secrets/<secretName>/ off the boundary root
	var secretDep string
	for k := range result.Files {
		if strings.Contains(k, "boundaries/local/default/helm-chart/secrets/root_password/") &&
			strings.Contains(k, "deployment.yaml") {
			secretDep = k
			break
		}
	}
	if secretDep == "" {
		t.Fatalf("missing credential FR deployment under secrets/root_password/, keys: %v", fileKeys(result.Files))
	}

	body := string(result.Files[secretDep])
	// .secret_name propagates into the rendered manifest
	if !strings.Contains(body, "SECRET_NAME") || !strings.Contains(body, "root_password") {
		t.Errorf("expected .secret_name=root_password in rendered manifest, got:\n%s", body)
	}
	// .inject top-level was built from the IR secret entry's inject block
	if !strings.Contains(body, "BINDING") || !strings.Contains(body, "ref:root_password") {
		t.Errorf("expected .inject.env.DB_PASSWORD=ref:root_password in rendered manifest, got:\n%s", body)
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
		Recipe: func(string) (*types.Recipe, bool) {
			return &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "ghost-recipe"},
				Spec: types.RecipeSpec{
					ValuesMapping: types.ValuesMapping{Inline: `{"image": "app:latest", "port": {{ .values.port }}}`},
				},
			}, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) { return nil, false },
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
		Recipe: func(componentType string) (*types.Recipe, bool) {
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
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
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
