// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// writeKindFixture writes body into a temp file named <kind>.yaml and returns
// the path. Each per-kind golden test drives dispatchInspect against this file
// through the injected buffer (no global os.Stdout swap).
func writeKindFixture(t *testing.T, kind, body string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, kind+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s fixture: %v", kind, err)
	}
	return path
}

// runKindGoldens drives the three text levels (default / list / detail) plus the
// --output yaml round-trip of a fixture through dispatchInspect, comparing each
// against testdata/inspect_<kind>/. The temp path is masked to a stable token so
// goldens are machine-stable.
func runKindGoldens(t *testing.T, kind, body string) {
	t.Helper()
	path := writeKindFixture(t, kind, body)
	mask := func(s string) []byte {
		return []byte(strings.ReplaceAll(s, path, strings.ToUpper(kind)+"_PATH"))
	}

	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
		t.Fatalf("inspect %s default: %v", kind, err)
	}
	compareGoldenKind(t, kind, "default.text.golden", mask(buf.String()))

	buf.Reset()
	opts = testInspectOptions(&buf)
	opts.level = levelList
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
		t.Fatalf("inspect %s --list: %v", kind, err)
	}
	compareGoldenKind(t, kind, "list.text.golden", mask(buf.String()))

	buf.Reset()
	opts = testInspectOptions(&buf)
	opts.level = levelDetail
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
		t.Fatalf("inspect %s --detail: %v", kind, err)
	}
	compareGoldenKind(t, kind, "detail.text.golden", mask(buf.String()))

	buf.Reset()
	opts = testInspectOptions(&buf)
	opts.output = outputYAML
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
		t.Fatalf("inspect %s --output yaml: %v", kind, err)
	}
	compareGoldenKind(t, kind, "yaml.golden", buf.Bytes())
}

const featureFixture = `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/metrics
  version: "1.0.0"
  labels:
    feature.aggrega.ai/category: observability
spec:
  schema: |
    values: { port?: int | *9090 }
  usesSchema:
    backend:
      selector: 'component.aggrega.ai/type=stateful-server'
      inject: [env]
  featuresSchema:
    token:
      required: true
  appliesTo: 'component.aggrega.ai/type=server'
  interoperability:
    conflictsWith:
      - core/other
  assertions:
    port-positive: '{{ if lt .values.port 1 }}{{ fail "port must be positive" }}{{ end }}'
`

func TestInspectFeature_Goldens(t *testing.T) {
	t.Parallel()
	runKindGoldens(t, "feature", featureFixture)
}

const credentialFeatureFixture = `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/db-credential
  version: "1.0.0"
  labels:
    feature.aggrega.ai/kind: secret
spec:
  schema: |
    values: {}
`

func TestInspectFeature_CredentialMarker(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "feature_secret", credentialFeatureFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect credential feature: %v", err)
	}
	if !strings.Contains(buf.String(), "Credential Feature") {
		t.Errorf("secret-kind Feature should render the credential marker; got:\n%s", buf.String())
	}
}

const recipeSynthesizedFixture = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: core/api-server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/api-server:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
  provides:
    endpoint: api-server
`

const recipeSourcedFixture = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: core/redis-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/redis:1.0.0
  source: oci://charts.example.com/redis:17.0.0
  valuesMapping: |
    image: redis
`

// recipeThreeModeFixture declares all three input modes at once — a source: to
// render, a workload: to synthesize, and an artifacts: map — which is legal
// because a Recipe's modes compose by addition. It is the fixture that pins the
// declared modes being reported as a set: a renderer reporting only the mode a
// single-mode Recipe would resolve to prints one of these three and hides the
// rest.
const recipeThreeModeFixture = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: core/three-mode-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/api-server:1.0.0
  source: oci://charts.example.com/api-server:2.1.0
  valuesMapping: |
    image: api-server
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
  artifacts:
    servicemonitor.yaml: |
      apiVersion: monitoring.coreos.com/v1
      kind: ServiceMonitor
    networkpolicy.yaml: |
      apiVersion: networking.k8s.io/v1
      kind: NetworkPolicy
  provides:
    endpoint: api-server
`

func TestInspectRecipe_Synthesized_Goldens(t *testing.T) {
	t.Parallel()
	runKindGoldens(t, "recipe", recipeSynthesizedFixture)
}

func TestInspectRecipe_Sourced_Golden(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "recipe_sourced", recipeSourcedFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect sourced recipe: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Sourced") {
		t.Errorf("sourced recipe should render the ## Sourced mode header; got:\n%s", out)
	}
	if strings.Contains(out, "## Synthesized") {
		t.Errorf("sourced recipe should not render ## Synthesized; got:\n%s", out)
	}
	compareGoldenKind(t, "recipe_sourced", "default.text.golden", []byte(out))
}

// TestInspectRecipe_ThreeModes_ReportsEverySection asserts against the printed
// bytes that a Recipe declaring all three input modes has all three reported,
// and that they are ordered source → workload → artifacts — the same order the
// build applies. Asserting the sections are present is not enough on its own:
// the omission this pins was a renderer that read every field and printed one
// section, so each mode's own body line is asserted too, at every level that
// renders a mode section.
func TestInspectRecipe_ThreeModes_ReportsEverySection(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "recipe_three_mode", recipeThreeModeFixture)

	for _, tc := range []struct {
		name  string
		level inspectLevel
	}{
		{"default", levelDefault},
		{"detail", levelDetail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			opts := testInspectOptions(&buf)
			opts.level = tc.level
			if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
				t.Fatalf("inspect three-mode recipe: %v", err)
			}
			out := buf.String()

			// Every declared mode is named, with the body line that makes it
			// actionable — the source reference above all, since that is the
			// line a single-section renderer drops.
			for _, want := range []string{
				"## Sourced",
				"  source: oci://charts.example.com/api-server:2.1.0",
				"## Synthesized",
				"  workload: inline",
				"Artifacts: 2",
				"  - servicemonitor.yaml",
				"  - networkpolicy.yaml",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("three-mode recipe output is missing %q; got:\n%s", want, out)
				}
			}

			// Ordered source → workload → artifacts. Presence alone would pass
			// on a renderer that reports the modes in the reverse of the order
			// a build applies, which is what makes this output comparable
			// against a build at all.
			sourceAt := strings.Index(out, "## Sourced")
			workloadAt := strings.Index(out, "## Synthesized")
			artifactsAt := strings.Index(out, "Artifacts: 2")
			if !(sourceAt < workloadAt && workloadAt < artifactsAt) {
				t.Errorf("modes should be ordered source → workload → artifacts; got offsets source=%d workload=%d artifacts=%d in:\n%s",
					sourceAt, workloadAt, artifactsAt, out)
			}

			// A Recipe declaring a source: or a workload: is never artifacts-only,
			// whatever else it declares.
			if strings.Contains(out, "## Artifacts-only") {
				t.Errorf("a recipe declaring a source: and a workload: is not artifacts-only; got:\n%s", out)
			}
		})
	}
}

// TestInspectRecipe_ThreeModes_Golden pins the whole three-mode rendering in
// bytes, so the section set and its order are reviewable as a diff rather than
// as a list of substring assertions.
func TestInspectRecipe_ThreeModes_Golden(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "recipe_three_mode", recipeThreeModeFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect three-mode recipe: %v", err)
	}
	compareGoldenKind(t, "recipe_three_mode", "default.text.golden", buf.Bytes())
}

const featureRecipeFixture = `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: core/metrics-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/metrics:1.0.0
  artifacts:
    servicemonitor.yaml: |
      apiVersion: monitoring.coreos.com/v1
      kind: ServiceMonitor
  injectMapping: |
    {}
  executionScope: boundary
`

func TestInspectFeatureRecipe_Goldens(t *testing.T) {
	t.Parallel()
	runKindGoldens(t, "featurerecipe", featureRecipeFixture)
}

func TestInspectFeatureRecipe_ListArtifactCount(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "fr_count", featureRecipeFixture)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.level = levelList
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
		t.Fatalf("inspect fr --list: %v", err)
	}
	if !strings.Contains(buf.String(), "artifacts: 1") {
		t.Errorf("--list should show the artifact count; got:\n%s", buf.String())
	}
}

const mappingLibraryFixture = `apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: CommonHelmMappings
  version: "1.0.0"
  description: Reusable Helm value mappings.
spec:
  mappings:
    ResourcesBlock: |
      resources:
        requests:
          cpu: {{ .values.cpu }}
    ProbesBlock: |
      livenessProbe:
        httpGet:
          path: /healthz
`

func TestInspectMappingLibrary_Goldens(t *testing.T) {
	t.Parallel()
	runKindGoldens(t, "mappinglibrary", mappingLibraryFixture)
}

func TestInspectMappingLibrary_BareNameUseLine(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "ml_use", mappingLibraryFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect mapping library: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "$import: CommonHelmMappings#/mappings/ResourcesBlock") {
		t.Errorf("Use line should reference bare-name identity; got:\n%s", out)
	}
	if strings.Contains(out, "oci://") {
		t.Errorf("Use line must not use an oci:// URL; got:\n%s", out)
	}
}

const schemaLibraryFixture = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: CommonSchemas
  version: "1.0.0"
  description: Reusable schema definitions.
spec:
  schemas:
    Port:
      type: integer
      minimum: 1
    Replicas:
      type: integer
  templates:
    SizedField:
      $params:
        max:
          type: integer
          default: 100
      type: integer
`

func TestInspectSchemaLibrary_Goldens(t *testing.T) {
	t.Parallel()
	runKindGoldens(t, "schemalibrary", schemaLibraryFixture)
}

func TestInspectSchemaLibrary_RefUseLineAndTemplates(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "sl_use", schemaLibraryFixture)
	var buf bytes.Buffer
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
		t.Fatalf("inspect schema library: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "$ref: CommonSchemas#/schemas/Port") {
		t.Errorf("Use line should reference bare-name $ref identity; got:\n%s", out)
	}
	if !strings.Contains(out, "SizedField") || !strings.Contains(out, "max: integer") {
		t.Errorf("templates section should render the template and its $params; got:\n%s", out)
	}
}

const componentDetailFixture = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
  annotations:
    features.aggrega.ai/requiredType.observability: core/metrics
spec:
  schema: |
    values: { port?: int | *8080 }
  inject: [env, file]
  usesSchema:
    db:
      selector: 'component.aggrega.ai/type=stateful-server'
`

func TestInspectComponent_Detail_Golden(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "component_detail", componentDetailFixture)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.level = levelDetail
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
		t.Fatalf("inspect component --detail: %v", err)
	}
	out := strings.ReplaceAll(buf.String(), path, "COMPONENT_DETAIL_PATH")
	if !strings.Contains(out, "Required attachment types:") {
		t.Errorf("--detail should surface the component's presence mandates; got:\n%s", out)
	}
	if !strings.Contains(out, "observability: core/metrics") {
		t.Errorf("--detail should name each mandate's label and required type; got:\n%s", out)
	}
	if !strings.Contains(out, "Self-inject mechanisms") {
		t.Errorf("--detail should surface inject mechanisms; got:\n%s", out)
	}
	compareGoldenKind(t, "component_detail", "detail.text.golden", []byte(out))
}

// TestInspectKind_ColorOnHeader proves the colorizer threads through a per-kind
// renderer end-to-end on a representative kind (Feature).
func TestInspectKind_ColorOnHeader(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "feature_color", featureFixture)
	var buf bytes.Buffer
	opts := inspectOptions{out: &buf, colorize: newColorizer(colorAlways, &buf, false)}
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, opts); err != nil {
		t.Fatalf("inspect --color=always: %v", err)
	}
	if !hasANSI(buf.String()) {
		t.Errorf("--color=always should wrap the Feature header in ANSI; got:\n%s", buf.String())
	}
}

// TestInspectKind_Determinism asserts a per-kind default render is byte-identical
// across two runs (Invariant 8: OrderedMap iteration, no plain-map ranging).
func TestInspectKind_Determinism(t *testing.T) {
	t.Parallel()
	path := writeKindFixture(t, "feature_det", featureFixture)
	render := func() string {
		var buf bytes.Buffer
		if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
			t.Fatalf("inspect: %v", err)
		}
		return buf.String()
	}
	if a, b := render(), render(); a != b {
		t.Errorf("default render not byte-identical across runs:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

// TestInspectRecipe_ShowImplements_ResolvesComponent proves --show-implements
// resolves a Recipe's implements reference via the shared catalog lookup (the
// three-form matching shared with build), surfacing the bound Component and the
// presence mandates it declares.
func TestInspectRecipe_ShowImplements_ResolvesComponent(t *testing.T) {
	t.Parallel()
	catDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(catDir, "api-server.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
  annotations:
    features.aggrega.ai/requiredType.observability: core/metrics
spec:
  schema:
    values: {}
`), 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}
	recipePath := writeKindFixture(t, "recipe_impl", recipeSynthesizedFixture)

	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.showImplements = true
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), recipePath, []string{catDir}, opts); err != nil {
		t.Fatalf("inspect --show-implements: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Implements Component: core/api-server:1.0.0") {
		t.Errorf("--show-implements should resolve the bound Component; got:\n%s", out)
	}
	if !strings.Contains(out, "requires attachment (observability): core/metrics") {
		t.Errorf("--show-implements should surface the Component's presence mandates; got:\n%s", out)
	}
}

// TestInspectRecipe_ShowImplements_AbortsWhenAbsent asserts --show-implements
// aborts with a clear error when the implements target is not in the catalog
// (no best-effort degradation).
func TestInspectRecipe_ShowImplements_AbortsWhenAbsent(t *testing.T) {
	t.Parallel()
	catDir := t.TempDir() // empty catalog
	recipePath := writeKindFixture(t, "recipe_missing", recipeSynthesizedFixture)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.showImplements = true
	err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), recipePath, []string{catDir}, opts)
	if err == nil {
		t.Fatal("--show-implements should abort when the implements target is absent")
	}
	if !strings.Contains(err.Error(), "show-implements") {
		t.Errorf("abort error should name --show-implements; got: %v", err)
	}
}

// TestInspectRecipe_ShowImplements_MalformedPin asserts --show-implements reports
// an implements target whose version pin is malformed as that malformation rather
// than as a Component the catalog does not hold, while a well-formed pin the
// catalog does not hold keeps the plain not-found abort. The catalog holds the
// Component at 1.0.0, and the unedited fixture resolving against it is the control.
func TestInspectRecipe_ShowImplements_MalformedPin(t *testing.T) {
	t.Parallel()
	const authored = "  implements: core/api-server:1.0.0\n"
	if !strings.Contains(recipeSynthesizedFixture, authored) {
		t.Fatalf("fixture no longer authors %q; re-point this test at the implements it carries", authored)
	}
	catDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(catDir, "api-server.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values: {}
`), 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}

	for _, tc := range []struct {
		name          string
		implements    string
		wantMalformed bool
		wantResolved  bool
	}{
		{"a pin the catalog satisfies", "core/api-server:1.0.0", false, true},
		{"a caret pin whose range does not parse", "core/api-server:^garbage", true, false},
		{"an exact pin that is not a semantic version", "core/api-server:1.x", true, false},
		{"a well-formed pin the catalog does not hold", "core/api-server:9.9.9", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := strings.Replace(recipeSynthesizedFixture, authored, "  implements: "+tc.implements+"\n", 1)
			recipePath := writeKindFixture(t, "recipe_pin", fixture)
			var buf bytes.Buffer
			opts := testInspectOptions(&buf)
			opts.showImplements = true
			err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), recipePath, []string{catDir}, opts)
			if tc.wantResolved {
				if err != nil {
					t.Fatalf("control: %q must resolve; got %v", tc.implements, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("--show-implements must abort for %q", tc.implements)
			}
			if got := errors.Is(err, types.ErrMalformedVersionPin()); got != tc.wantMalformed {
				t.Errorf("abort %v: malformed-pin condition present = %v, want %v", err, got, tc.wantMalformed)
			}
			if tc.wantMalformed == strings.Contains(err.Error(), "not found in catalog") {
				t.Errorf("abort %q: a malformed pin must not claim the target is absent, and an absent one must", err.Error())
			}
		})
	}
}

// TestInspectFeatureRecipe_ShowImplements_CredentialMarker proves
// --show-implements resolves a FeatureRecipe's bound Feature and emits the
// credential marker when that Feature is secret-kind.
func TestInspectFeatureRecipe_ShowImplements_CredentialMarker(t *testing.T) {
	t.Parallel()
	catDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(catDir, "db-credential.yaml"), []byte(`apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/db-credential
  version: "1.0.0"
  labels:
    feature.aggrega.ai/kind: secret
spec:
  schema:
    values: {}
`), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	frBody := `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: core/db-credential-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/db-credential:1.0.0
  injectMapping: |
    {}
`
	frPath := writeKindFixture(t, "fr_cred", frBody)
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.showImplements = true
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), frPath, []string{catDir}, opts); err != nil {
		t.Fatalf("inspect fr --show-implements: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Implements Feature: core/db-credential:1.0.0") {
		t.Errorf("--show-implements should resolve the bound Feature; got:\n%s", out)
	}
	if !strings.Contains(out, "Credential FeatureRecipe — emits secret-kind injection") {
		t.Errorf("--show-implements should emit the credential marker for a secret-kind Feature; got:\n%s", out)
	}
}

// TestInspectDispatch_RegisteredKinds asserts each newly-registered kind routes
// to its renderer rather than the not-yet-implemented / unknown-kind branches.
func TestInspectDispatch_RegisteredKinds(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Feature":        featureFixture,
		"Recipe":         recipeSynthesizedFixture,
		"FeatureRecipe":  featureRecipeFixture,
		"Plugin":         pluginFixture,
		"MappingLibrary": mappingLibraryFixture,
		"SchemaLibrary":  schemaLibraryFixture,
	}
	for kind, body := range cases {
		path := writeKindFixture(t, strings.ToLower(kind), body)
		var buf bytes.Buffer
		if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, nil, testInspectOptions(&buf)); err != nil {
			t.Fatalf("%s should route to its renderer; got: %v", kind, err)
		}
		if strings.Contains(buf.String(), "not yet implemented") || strings.Contains(buf.String(), "unknown entity kind") {
			t.Errorf("%s should not hit a stub branch; got:\n%s", kind, buf.String())
		}
	}
}
