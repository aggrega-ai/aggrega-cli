// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// stubEngine is a no-op Engine used for tests that do not need real
// rendering — only the type-assertion fork inside evaluateArtifacts.
type stubEngine struct{}

func (stubEngine) Render(_ context.Context, _ map[string]any, _ NodeRenderContext) (map[string][]byte, error) {
	return nil, nil
}

// Capabilities satisfies the Engine interface for the test stub. Permissive
// (all input kinds) so the central gate never declines it — these tests
// exercise evaluateArtifacts / routing directly, not the gate. Embedded by
// stubComposeEngine and stubExternalArtifactsEngine, which inherit it.
func (stubEngine) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// stubComposeEngine implements both Engine and BoundaryAssembler so the
// evaluator routes its emissions to Intermediate=true.
type stubComposeEngine struct{ stubEngine }

func (stubComposeEngine) AssembleBoundary(_ context.Context, _ BoundaryInfo) (map[string][]byte, bool, error) {
	return nil, false, nil
}

// stubExternalArtifactsEngine implements the ExternalArtifactsHandler
// marker plus the two companion capability interfaces (ArtifactsPathProvider,
// ArtifactsTemplateDelims) the artifacts evaluator's external-handler
// branch dispatches against. Same shape as the production helm-chart
// engine — `templates/<basename>` placement and `[[ ]]` delimiters —
// so renderer-package tests exercise the production dispatch path
// without crossing into the helmchart package (which would create a
// circular import).
type stubExternalArtifactsEngine struct{ stubEngine }

func (stubExternalArtifactsEngine) HandlesArtifactsExternally() {}
func (stubExternalArtifactsEngine) PathForArtifact(_ NodeRenderContext, basename string) string {
	return "templates/" + basename
}
func (stubExternalArtifactsEngine) ArtifactDelimiters() (string, string) { return "[[", "]]" }

func newArtifactsCtx() (*types.TemplateContext, *PhaseBState, NodeRenderContext) {
	tmplCtx := &types.TemplateContext{
		Context: types.ContextBlock{InstanceID: "api"},
		Values:  types.OrderedMap[any]{},
		Infra:   types.OrderedMap[any]{},
	}
	r := &types.Recipe{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Recipe",
		Metadata:   types.RecipeMetadata{Name: "api-server-helm", Version: "1.0.0"},
		Spec:       types.RecipeSpec{Provider: "helm", Implements: "core/api-server:1.0.0"},
	}
	state := NewPhaseBState(r, "api", "production", "recipe.yaml")
	nodeCtx := NodeRenderContext{
		NodeID:       "api",
		BoundaryPath: "production",
	}
	return tmplCtx, state, nodeCtx
}

// findEmitted returns the first EmittedFile whose Name equals name. Test
// helper that asserts presence and returns the entry for further checks.
func findEmitted(t *testing.T, files []EmittedFile, name string) EmittedFile {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no emitted file named %q; got %d files", name, len(files))
	return EmittedFile{}
}

// readDocLabels parses an emitted artifact's body and returns its
// metadata.labels map. Reflection-style assertion: we don't depend on
// byte-exact YAML output, only on the parsed shape. Decodes through the
// typed shape `map[string]map[string]string` so yaml.v3 produces
// string-keyed maps end-to-end.
func readDocLabels(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var doc struct {
		Metadata struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal artifact body: %v", err)
	}
	return doc.Metadata.Labels
}

func readDocAnnotations(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var doc struct {
		Metadata struct {
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal artifact body: %v", err)
	}
	return doc.Metadata.Annotations
}

// ----- Test 1: inline-only -----

func TestEvaluateArtifacts_InlineOnly(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("configmap.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
data:
  greeting: hello
`})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 emitted file, got %d", len(files))
	}
	got := files[0]
	if got.Name != "configmap.yaml" {
		t.Errorf("Name = %q, want configmap.yaml", got.Name)
	}
	if got.Origin != EmitOriginArtifactsInline {
		t.Errorf("Origin = %v, want EmitOriginArtifactsInline", got.Origin)
	}
	if got.Intermediate {
		t.Errorf("Intermediate = true, want false for non-ReleaseComposer engine")
	}
	labels := readDocLabels(t, got.Body)
	if labels["aggrega.ai/component-id"] != "api" {
		t.Errorf("component-id = %q, want api", labels["aggrega.ai/component-id"])
	}
}

// ----- Test 2: file-imported artifacts only -----

func TestEvaluateArtifacts_FromFileOnly(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(manifestPath, []byte(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-all
spec:
  podSelector: {}
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(manifestPath), types.ArtifactEntry{Import: manifestPath})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 emitted file, got %d", len(files))
	}
	got := files[0]
	if got.Name != "policy.yaml" {
		t.Errorf("Name = %q, want policy.yaml (basename)", got.Name)
	}
	if got.Origin != EmitOriginArtifactsImport {
		t.Errorf("Origin = %v, want EmitOriginArtifactsImport", got.Origin)
	}
}

// ----- Test 3: mixed inline + file-imported artifacts -----

func TestEvaluateArtifacts_MixedInlineAndFromFile(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("inline-cm.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: inline-config
`})

	dir := t.TempDir()
	p := filepath.Join(dir, "external.yaml")
	if err := os.WriteFile(p, []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: from-file
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(p), types.ArtifactEntry{Import: p})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 emitted files, got %d", len(files))
	}
	if files[0].Origin != EmitOriginArtifactsInline {
		t.Errorf("first emission Origin = %v, want EmitOriginArtifactsInline (declaration order: inline first)", files[0].Origin)
	}
	if files[1].Origin != EmitOriginArtifactsImport {
		t.Errorf("second emission Origin = %v, want EmitOriginArtifactsImport", files[1].Origin)
	}
}

// ----- Test 4: template accesses .workload -----

func TestEvaluateArtifacts_TemplateAccessesWorkload(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.ParsedWorkload = &types.Workload{}
	tmplCtx.Workload = &types.Workload{}
	tmplCtx.Workload.Containers = types.OrderedMap[types.ContainerSpec]{}
	tmplCtx.Workload.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
	state.Recipe.Spec.Workload = types.WorkloadTemplate{Inline: "set"} // marker; template doesn't read it but pre-check uses ParsedWorkload.

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("monitor.yaml", types.ArtifactEntry{Inline: `apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: api-monitor
spec:
  image: {{ (index .workload.containers "main").image }}
`})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	body := string(findEmitted(t, files, "monitor.yaml").Body)
	if !strings.Contains(body, "image: app:1.0") {
		t.Errorf("expected templated image value, got body:\n%s", body)
	}
}

// ----- Test 5: template accesses .source -----

func TestEvaluateArtifacts_TemplateAccessesSource(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Source = map[string]any{"chart": "nginx", "version": "1.2.3"}
	tmplCtx.Source = state.Recipe.Spec.Source

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("info.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: source-info
data:
  chart: {{ .source.chart }}
`})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	body := string(findEmitted(t, files, "info.yaml").Body)
	if !strings.Contains(body, "chart: nginx") {
		t.Errorf("expected templated chart name, got body:\n%s", body)
	}
}

// ----- Test 6: nil .workload produces clear CATALOG_ERROR -----

func TestEvaluateArtifacts_NilWorkloadProducesClearError(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	// state.ParsedWorkload remains nil (Recipe declares no workload:).

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("monitor.yaml", types.ArtifactEntry{Inline: `apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
spec:
  port: {{ .workload.containers.main.ports.web }}
`})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("evaluateArtifacts panicked on nil .workload: %v", r)
		}
	}()
	_, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for .workload reference with no workload declared")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("expected CATALOG_ERROR prefix, got %q", msg)
	}
	if !strings.Contains(msg, ".workload.containers.main.ports.web") {
		t.Errorf("error should name the failing expression, got %q", msg)
	}
	if !strings.Contains(msg, "no workload") {
		t.Errorf("error should hint that no workload is declared, got %q", msg)
	}
}

// ----- Test 7: provenance labels on K8s resource -----

func TestEvaluateArtifacts_ProvenanceLabelsOnK8sResource(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	nodeCtx.Provenance = ProvenanceContext{
		CompilerVersion:  "v0.0.0-test",
		AggregateName:    "demo",
		AggregateVersion: "1.0.0",
		ReleaseName:      "demo-prod",
		Environment:      "prod",
	}

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("deployment.yaml", types.ArtifactEntry{Inline: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  labels:
    author: alice
spec:
  replicas: 1
`})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	body := findEmitted(t, files, "deployment.yaml").Body
	labels := readDocLabels(t, body)

	// Label-safe provenance + the artifact's own author label stay in labels.
	mustLabels := map[string]string{
		"app.kubernetes.io/name":      "api",
		"aggrega.ai/component-id":     "api",
		"aggrega.ai/compiler-version": "v0.0.0-test",
		"aggrega.ai/aggregate-name":   "demo",
		"aggrega.ai/environment":      "prod",
		"author":                      "alice",
	}
	for k, want := range mustLabels {
		if got := labels[k]; got != want {
			t.Errorf("label %q = %q, want %q", k, got, want)
		}
	}
	// Charset-unsafe provenance lands in annotations, not labels.
	for _, k := range []string{"aggrega.ai/recipe-uri", "aggrega.ai/boundary-path"} {
		if _, has := labels[k]; has {
			t.Errorf("label %q present; a charset-unsafe key must be an annotation", k)
		}
	}
	annotations := readDocAnnotations(t, body)
	mustAnnotations := map[string]string{
		"aggrega.ai/recipe-uri":    "api-server-helm:1.0.0",
		"aggrega.ai/boundary-path": "production",
	}
	for k, want := range mustAnnotations {
		if got := annotations[k]; got != want {
			t.Errorf("annotation %q = %q, want %q", k, got, want)
		}
	}
}

// ----- Test 8: ConfigMap artifact referenced by Workload -----

func TestEvaluateArtifacts_ConfigMapReferencedByWorkload(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("api-config.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
data:
  feature_flag: "true"
`})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	got := findEmitted(t, files, "api-config.yaml")
	var doc map[string]any
	if err := yaml.Unmarshal(got.Body, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if doc["kind"] != "ConfigMap" {
		t.Errorf("kind = %v, want ConfigMap", doc["kind"])
	}
}

// ----- Test 9: compose engine routes to Intermediate=true -----

func TestEvaluateArtifacts_ComposeProviderProducesIntermediate(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "compose"

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("extra-services.yaml", types.ArtifactEntry{Inline: `services:
  sidecar:
    image: busybox:1.36
`})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubComposeEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	got := findEmitted(t, files, "extra-services.yaml")
	if !got.Intermediate {
		t.Errorf("Intermediate = false, want true for ReleaseComposer engine")
	}
	// Compose fragment has no metadata; provenance injection silent no-op.
	labels := readDocLabels(t, got.Body)
	if len(labels) != 0 {
		t.Errorf("compose fragment without metadata should not gain labels, got %v", labels)
	}
}

// ----- Test 10: ExternalArtifactsHandler inline rejection (acceptance 11a) -----

func TestEvaluateArtifacts_HelmChartProviderRejectsInline(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "helm-chart"

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("any.yaml", types.ArtifactEntry{Inline: `kind: ConfigMap`})

	_, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalArtifactsEngine{}, nodeCtx)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for ExternalArtifactsHandler engine with inline artifacts")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("expected CATALOG_ERROR prefix, got %q", msg)
	}
	if !strings.Contains(msg, "helm-chart") {
		t.Errorf("error should name the helm-chart provider, got %q", msg)
	}
	if !strings.Contains(msg, state.RecipeName) {
		t.Errorf("error should name the recipe, got %q", msg)
	}
}

// ----- authored key order preserved through provenance injection -----

func TestEvaluateArtifacts_PreservesAuthoredKeyOrder(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("ordered.yaml", types.ArtifactEntry{Inline: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 1
`})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	body := string(findEmitted(t, files, "ordered.yaml").Body)

	// Top-level fields must remain in the author-declared order:
	// apiVersion → kind → metadata → spec. Walk lines, advance idx only
	// when the next expected top-level key matches; nested / blank /
	// commented lines pass through unaffected.
	want := []string{"apiVersion:", "kind:", "metadata:", "spec:"}
	idx := 0
	for _, line := range strings.Split(body, "\n") {
		if idx == len(want) {
			break
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, want[idx]) {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("authored top-level key order not preserved through round-trip:\n%s", body)
	}
}

// ----- template parse errors surface through artifacts evaluator -----

func TestEvaluateArtifacts_TemplateParseErrorSurfaces(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	// Unbalanced "{{" — Go template parser rejects this at parse time.
	state.Recipe.Spec.Artifacts.Set("broken.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id
`})

	_, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for malformed Go template syntax")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("expected CATALOG_ERROR prefix, got %q", msg)
	}
	if !strings.Contains(msg, "broken.yaml") {
		t.Errorf("error should name the artifact filename, got %q", msg)
	}
	if !strings.Contains(msg, "parse") {
		t.Errorf("error should indicate the parse stage, got %q", msg)
	}
}

// ----- Q1a: malformed metadata.labels rejected with CATALOG_ERROR -----

func TestEvaluateArtifacts_RejectsMalformedMetadataLabels(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()

	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set("bad-labels.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: bad
  labels:
    - foo
    - bar
`})

	_, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for malformed metadata.labels (sequence under labels)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("expected CATALOG_ERROR prefix, got %q", msg)
	}
	if !strings.Contains(msg, "bad-labels.yaml") {
		t.Errorf("error should name the artifact filename, got %q", msg)
	}
	if !strings.Contains(msg, "expected mapping, got sequence") {
		t.Errorf("error should name the unexpected shape, got %q", msg)
	}
}

// ----- Test 11b (rewrite): ExternalArtifactsHandler $import: entries emit at templates/<basename> -----

func TestEvaluateArtifacts_HelmChartProviderArtifactsImportInjects(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "helm-chart"

	dir := t.TempDir()
	p := filepath.Join(dir, "api-config.yaml")
	if err := os.WriteFile(p, []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
data:
  greeting: hello
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(p), types.ArtifactEntry{Import: p})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalArtifactsEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 emitted file, got %d", len(files))
	}
	got := files[0]
	if got.Name != "templates/api-config.yaml" {
		t.Errorf("Name = %q, want templates/api-config.yaml (engine PathForArtifact placement)", got.Name)
	}
	if got.Origin != EmitOriginArtifactsImport {
		t.Errorf("Origin = %v, want EmitOriginArtifactsImport", got.Origin)
	}
	labels := readDocLabels(t, got.Body)
	if labels["aggrega.ai/component-id"] != "api" {
		t.Errorf("provenance labels missing or wrong; got %v", labels)
	}
}

// ----- Test 12: helm-chart [[ ]] delimiters preserve Helm's {{ }} syntax -----

func TestEvaluateArtifacts_HelmChartDelimitersPreserveHelmSyntax(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "helm-chart"

	dir := t.TempDir()
	p := filepath.Join(dir, "mixed.yaml")
	// Aggrega-side `[[ ]]` references resolve at the pre-pass; Helm's
	// `{{ }}` syntax must survive untouched into Helm's downstream render.
	if err := os.WriteFile(p, []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: [[ .context.instance_id ]]-cfg
data:
  fullname: {{ .Values.fullname }}
  release: {{ .Release.Name }}
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(p), types.ArtifactEntry{Import: p})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalArtifactsEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	body := string(findEmitted(t, files, "templates/mixed.yaml").Body)
	if !strings.Contains(body, "name: api-cfg") {
		t.Errorf("Aggrega-side [[ .context.instance_id ]] should resolve to api-cfg, got body:\n%s", body)
	}
	if !strings.Contains(body, "{{ .Values.fullname }}") {
		t.Errorf("Helm-side {{ .Values.fullname }} should survive untouched, got body:\n%s", body)
	}
	if !strings.Contains(body, "{{ .Release.Name }}") {
		t.Errorf("Helm-side {{ .Release.Name }} should survive untouched, got body:\n%s", body)
	}
}

// ----- Test 12b: helm-chart quoted-scalar artifact round-trips and gets provenance -----
//
// A dual-delimiter artifact whose only Helm `{{ }}` sits at a QUOTED
// scalar position parses as YAML before Helm rendering, so the compiler
// injects provenance and the Helm reference survives (re-quoted) for the
// downstream render. Before the single-gate change this file was skipped
// by the pre-emptive `{{` substring check — so the provenance assertion
// is non-vacuous.
func TestEvaluateArtifacts_HelmChartQuotedScalarInjectsProvenance(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "helm-chart"

	dir := t.TempDir()
	p := filepath.Join(dir, "netpol.yaml")
	if err := os.WriteFile(p, []byte(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: [[ .context.instance_id ]]-netpol
  namespace: "{{ .Release.Namespace }}"
spec:
  podSelector:
    matchLabels:
      app: [[ .context.instance_id ]]
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(p), types.ArtifactEntry{Import: p})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalArtifactsEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	emitted := findEmitted(t, files, "templates/netpol.yaml")
	body := string(emitted.Body)

	// (1) Provenance injected — non-vacuous (this file was skipped pre-fix).
	labels := readDocLabels(t, emitted.Body)
	if labels["aggrega.ai/component-id"] != "api" {
		t.Errorf("expected provenance label aggrega.ai/component-id=api; got labels %v\nbody:\n%s", labels, body)
	}
	// (2) Aggrega `[[ ]]` resolved at compile time.
	if !strings.Contains(body, "name: api-netpol") {
		t.Errorf("[[ .context.instance_id ]] should resolve to api-netpol; body:\n%s", body)
	}
	// (3) Helm `{{ }}` survives for Helm's downstream render (quote style
	// may shift; the reference itself is preserved verbatim).
	if !strings.Contains(body, "{{ .Release.Namespace }}") {
		t.Errorf("Helm {{ .Release.Namespace }} should survive untouched; body:\n%s", body)
	}
	// (4) No skip warning for a round-trippable file.
	for _, w := range state.Warnings {
		if strings.Contains(w, "netpol.yaml") && strings.Contains(w, "skipped provenance label injection") {
			t.Errorf("round-trippable artifact should NOT warn; got %q", w)
		}
	}
}

// ----- Test 12c: same Helm token quoted AND unquoted in one file skips (count guard) -----
//
// `{{ .Values.shared }}` appears both quoted (round-trips) and unquoted
// (yaml.v3 re-marshals it as a flow mapping, dropping the token). A pure
// substring check would see the surviving quoted copy and wrongly accept
// the file; the token-COUNT check catches the dropped copy and forces a
// skip, so a partially-corrupted artifact is never injected.
func TestEvaluateArtifacts_HelmChartMixedQuotingSameTokenInjectsByteFaithfully(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "helm-chart"

	dir := t.TempDir()
	p := filepath.Join(dir, "mixed-quoting.yaml")
	if err := os.WriteFile(p, []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: [[ .context.instance_id ]]-cm
data:
  quoted: "{{ .Values.shared }}"
  unquoted: {{ .Values.shared }}
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(p), types.ArtifactEntry{Import: p})

	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalArtifactsEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	body := string(findEmitted(t, files, "templates/mixed-quoting.yaml").Body)

	// The surgical splice injects provenance into metadata WITHOUT re-marshalling
	// the body, so BOTH the quoted and the unquoted Helm token survive byte-for-byte
	// in the untouched `data:` section — the round-trip mangling that forced 09o to
	// skip this file no longer applies.
	if !strings.Contains(body, "aggrega.ai/component-id") {
		t.Errorf("byte-faithful splice should inject provenance; body:\n%s", body)
	}
	if !strings.Contains(body, `quoted: "{{ .Values.shared }}"`) || !strings.Contains(body, "unquoted: {{ .Values.shared }}") {
		t.Errorf("both Helm token copies must survive byte-faithfully in the untouched body; body:\n%s", body)
	}
	for _, w := range state.Warnings {
		if strings.Contains(w, "mixed-quoting.yaml") {
			t.Errorf("byte-faithful inject should not warn; got %v", state.Warnings)
		}
	}
}

// ----- Test 13: helm-chart file with structural Helm syntax skips provenance and warns -----
//
// The fixture's `{{- include "common.labels" . | nindent 4 }}` is
// structural Helm syntax that does not parse as YAML before Helm
// rendering, so the yaml.Unmarshal round-trip fails and the file is
// emitted unchanged with a provenance-skip warning. This is the genuine
// non-round-trippable case the warning reserves `_helpers.tpl` for —
// distinct from a quoted-scalar `{{ }}`, which round-trips and DOES get
// provenance (see TestEvaluateArtifacts_HelmChartQuotedScalarInjectsProvenance).

func TestEvaluateArtifacts_HelmChartFileWithHelmSyntaxSkipsProvenanceAndWarns(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "helm-chart"

	dir := t.TempDir()
	p := filepath.Join(dir, "with-include.yaml")
	// Structural Helm syntax — `{{- include ... }}` at a position that
	// emits YAML at Helm-render-time but isn't valid YAML pre-render.
	if err := os.WriteFile(p, []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
  labels:
    {{- include "common.labels" . | nindent 4 }}
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(p), types.ArtifactEntry{Import: p})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("evaluateArtifacts panicked on unparseable file: %v", r)
		}
	}()
	files, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalArtifactsEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("expected no error for unparseable helm-chart imported artifact, got %v", err)
	}
	got := findEmitted(t, files, "templates/with-include.yaml")
	if !strings.Contains(string(got.Body), `{{- include "common.labels" . | nindent 4 }}`) {
		t.Errorf("structural Helm syntax should pass through unchanged, got body:\n%s", string(got.Body))
	}
	// Warning emission via state.Warnings.
	if len(state.Warnings) == 0 {
		t.Fatalf("expected state.Warnings to carry the provenance-skip notice, got none")
	}
	var found bool
	for _, w := range state.Warnings {
		if strings.Contains(w, "with-include.yaml") && strings.Contains(w, "skipped provenance label injection") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'skipped provenance label injection' warning naming with-include.yaml; got %v", state.Warnings)
	}
}

// ----- Test 14: helm-chart artifact-import collision at Phase B.5 -----

func TestEvaluateArtifacts_HelmChartCollisionAtPhaseB5(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Provider = "helm-chart"

	// Pre-seed state.EmittedFiles with a templates/<basename>-tagged source
	// emission to simulate the helm-chart engine's Render output containing
	// the same filename. Phase B.3's file-imported artifact emission below
	// uses the same templates/<basename> path, so B.5's rule-7 collision
	// detector should fire.
	state.EmittedFiles = append(state.EmittedFiles, EmittedFile{
		Name:   "templates/conflicting.yaml",
		Origin: EmitOriginSource,
		Body:   []byte("apiVersion: v1\nkind: ConfigMap\n"),
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "conflicting.yaml")
	if err := os.WriteFile(p, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: clash\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
	state.Recipe.Spec.Artifacts.Set(filepath.Base(p), types.ArtifactEntry{Import: p})

	out, err := evaluateArtifacts(context.Background(), tmplCtx, state, stubExternalArtifactsEngine{}, nodeCtx)
	if err != nil {
		t.Fatalf("evaluateArtifacts: %v", err)
	}
	state.EmittedFiles = append(state.EmittedFiles, out...)

	err = phaseB5(state)
	if err == nil {
		t.Fatal("expected rule 7 cross-origin collision at templates/conflicting.yaml")
	}
	if !errors.Is(err, validator.ErrRecipeRule7) {
		t.Errorf("expected ErrRecipeRule7, got %v", err)
	}
	if !strings.Contains(err.Error(), "templates/conflicting.yaml") {
		t.Errorf("error should name the colliding path, got %v", err)
	}
}
