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

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stubEngine is a no-op Engine used by tests across the package that need an
// Engine value but not real rendering.
type stubEngine struct{}

func (stubEngine) Render(_ context.Context, _ map[string]any, _ NodeRenderContext) (map[string][]byte, error) {
	return nil, nil
}

// Capabilities satisfies the Engine interface for the test stub. Permissive (all
// input kinds) so the central gate never declines it. Embedded by
// stubArtifactsEmitter and the host-capabilities test wrappers, which inherit it.
func (stubEngine) Contract() ProviderContract {
	return ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}

// stubArtifactsEmitter stands in for a provider's Wasm adapter on the
// ArtifactsEmitter seam: it reports a delimiter pair, captures the projection
// Phase B.3 builds, and places each entry at its name — the minimal guest
// behavior the dispatch test needs, without crossing into a provider package.
type stubArtifactsEmitter struct {
	stubEngine
	left, right string
	assembly    string // boundary-assembly self-report; "" ⇒ flat
	got         ArtifactsProjection
}

func (e *stubArtifactsEmitter) BoundaryAssembly() string { return e.assembly }

func (e *stubArtifactsEmitter) ArtifactTemplateDelimiters() (string, string) {
	if e.left == "" && e.right == "" {
		return "{{", "}}"
	}
	return e.left, e.right
}

func (e *stubArtifactsEmitter) EmitArtifacts(_ context.Context, proj ArtifactsProjection, _ NodeRenderContext) ([]EmittedFile, error) {
	e.got = proj
	intermediate := RenderIsIntermediate(e)
	files := make([]EmittedFile, 0, len(proj.Entries))
	for _, ent := range proj.Entries {
		origin := EmitOriginArtifactsInline
		if ent.Origin == "import" {
			origin = EmitOriginArtifactsImport
		}
		files = append(files, EmittedFile{Name: ent.Name, Origin: origin, Body: ent.Content, Intermediate: intermediate})
	}
	return files, nil
}

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
		Spec:       types.RecipeSpec{Capability: "helm", Implements: "core/api-server:1.0.0"},
	}
	state := NewPhaseBState(r, "api", "production", "recipe.yaml")
	nodeCtx := NodeRenderContext{
		NodeID:       "api",
		BoundaryPath: "production",
	}
	return tmplCtx, state, nodeCtx
}

// findEmitted returns the first EmittedFile whose Name equals name.
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

// readDocLabels parses an artifact body and returns its metadata.labels map.
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

func resetArtifacts(state *PhaseBState) {
	state.Recipe.Spec.Artifacts = types.OrderedMap[types.ArtifactEntry]{}
}

// ----- host projection: inline entry -----

func TestBuildArtifactsProjection_InlineEntry(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("configmap.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}-config
data:
  greeting: hello
`})

	proj, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	if len(proj.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(proj.Entries))
	}
	e := proj.Entries[0]
	if e.Name != "configmap.yaml" {
		t.Errorf("Name = %q, want configmap.yaml (author map key)", e.Name)
	}
	if e.Origin != "inline" {
		t.Errorf("Origin = %q, want inline", e.Origin)
	}
	if !strings.Contains(string(e.Content), "name: api-config") {
		t.Errorf("template not evaluated against the render context; content:\n%s", e.Content)
	}
	// Provenance key sets computed host-side and attached to the entry.
	if e.Labels["aggrega.ai/component-id"] != "api" {
		t.Errorf("entry labels missing component-id; got %v", e.Labels)
	}
	if e.Annotations["aggrega.ai/recipe-uri"] != "api-server-helm:1.0.0" {
		t.Errorf("entry annotations missing recipe-uri; got %v", e.Annotations)
	}
}

// ----- host projection: imported entry (name is the file base name) -----

func TestBuildArtifactsProjection_ImportEntry(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	dir := t.TempDir()
	p := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(p, []byte(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-all
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("whatever-key", types.ArtifactEntry{Import: p})

	proj, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	e := proj.Entries[0]
	if e.Name != "policy.yaml" {
		t.Errorf("Name = %q, want policy.yaml (imported file base name)", e.Name)
	}
	if e.Origin != "import" {
		t.Errorf("Origin = %q, want import", e.Origin)
	}
}

// E6: an imported artifact whose recorded pin matches renders; a pin that no
// longer matches the file's current content halts loudly with the gap-fix
// sentinel. This is the resolution-time freeze / build-time verify contract for
// $import artifact files.
func TestBuildArtifactsProjection_ImportPinVerify(t *testing.T) {
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	dir := t.TempDir()
	p := filepath.Join(dir, "policy.yaml")
	body := []byte("kind: NetworkPolicy\nmetadata:\n  name: deny-all\n")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("policy", types.ArtifactEntry{Import: p})
	// A matching pin renders without complaint.
	state.Recipe.Spec.ArtifactDigests = []types.ArtifactDigest{{Name: "policy", Digest: digest.ContentDigest(body)}}
	if _, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}"); err != nil {
		t.Fatalf("matching pin should render: %v", err)
	}

	// Tamper the imported file after the freeze: the build must halt loudly.
	if err := os.WriteFile(p, []byte("kind: NetworkPolicy\nmetadata:\n  name: allow-all\n"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	_, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err == nil {
		t.Fatalf("tampered import rendered without halting; expected a loud halt")
	}
	if !errors.Is(err, ErrArtifactContentMismatch) {
		t.Errorf("tampered-import error = %v; want ErrArtifactContentMismatch", err)
	}
}

// ----- host projection: whitespace-only render is skipped; empty-data body kept -----

// A presence-guarded artifact whose body renders to whitespace-only content is
// dropped from the projection (no zero-length file); an artifact that renders a
// real, non-empty body — a ConfigMap with an empty `data: {}` but a real kind
// and metadata — is retained. The skip keys off the rendered bytes after
// trimming, not off the authored template.
func TestBuildArtifactsProjection_SkipsEmptyRenderKeepsNonEmpty(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	// Guarded body that collapses to nothing when the guard is false: the
	// {{ if }} produces only whitespace after trimming.
	state.Recipe.Spec.Artifacts.Set("maybe.yaml", types.ArtifactEntry{Inline: `{{- if .values.enabled }}
kind: ConfigMap
{{- end }}
`})
	// A real manifest whose data map is empty but whose body is non-empty.
	state.Recipe.Spec.Artifacts.Set("configmap.yaml", types.ArtifactEntry{Inline: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .context.instance_id }}-config
data: {}
`})

	proj, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	if len(proj.Entries) != 1 {
		t.Fatalf("expected 1 entry (empty render skipped), got %d: %+v", len(proj.Entries), proj.Entries)
	}
	e := proj.Entries[0]
	if e.Name != "configmap.yaml" {
		t.Errorf("retained entry Name = %q, want configmap.yaml", e.Name)
	}
	if !strings.Contains(string(e.Content), "kind: ConfigMap") || !strings.Contains(string(e.Content), "data: {}") {
		t.Errorf("empty-data ConfigMap body must be kept intact; content:\n%s", e.Content)
	}
	for _, ent := range proj.Entries {
		if ent.Name == "maybe.yaml" {
			t.Errorf("whitespace-only render should have been skipped, but maybe.yaml is present")
		}
	}
}

// ----- host projection: mixed inline + imported, author order preserved -----

func TestBuildArtifactsProjection_MixedOrder(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	dir := t.TempDir()
	p := filepath.Join(dir, "external.yaml")
	if err := os.WriteFile(p, []byte("kind: ConfigMap\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("inline-cm.yaml", types.ArtifactEntry{Inline: "kind: ConfigMap\n"})
	state.Recipe.Spec.Artifacts.Set("external.yaml", types.ArtifactEntry{Import: p})

	proj, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	if len(proj.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(proj.Entries))
	}
	if proj.Entries[0].Origin != "inline" || proj.Entries[1].Origin != "import" {
		t.Errorf("author order not preserved: %q then %q", proj.Entries[0].Origin, proj.Entries[1].Origin)
	}
}

// ----- host projection: template accesses .workload -----

func TestBuildArtifactsProjection_TemplateAccessesWorkload(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.ParsedWorkload = &types.Workload{}
	tmplCtx.Workload = &types.Workload{}
	tmplCtx.Workload.Containers = types.OrderedMap[types.ContainerSpec]{}
	tmplCtx.Workload.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
	state.Recipe.Spec.Workload = types.WorkloadTemplate{Inline: "set"}

	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("monitor.yaml", types.ArtifactEntry{Inline: `kind: ServiceMonitor
spec:
  image: {{ (index .workload.containers "main").image }}
`})

	proj, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	if !strings.Contains(string(proj.Entries[0].Content), "image: app:1.0") {
		t.Errorf("expected templated image value, got:\n%s", proj.Entries[0].Content)
	}
}

// ----- host projection: template accesses .source -----

func TestBuildArtifactsProjection_TemplateAccessesSource(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	state.Recipe.Spec.Source = map[string]any{"chart": "nginx", "version": "1.2.3"}
	tmplCtx.Source = state.Recipe.Spec.Source

	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("info.yaml", types.ArtifactEntry{Inline: `kind: ConfigMap
data:
  chart: {{ .source.chart }}
`})

	proj, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	if !strings.Contains(string(proj.Entries[0].Content), "chart: nginx") {
		t.Errorf("expected templated chart name, got:\n%s", proj.Entries[0].Content)
	}
}

// ----- host projection: nil .workload produces a clear CATALOG_ERROR -----

func TestBuildArtifactsProjection_NilWorkloadProducesClearError(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("monitor.yaml", types.ArtifactEntry{Inline: `kind: ServiceMonitor
spec:
  port: {{ .workload.containers.main.ports.web }}
`})

	_, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
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

// ----- host projection: template parse error surfaces -----

func TestBuildArtifactsProjection_TemplateParseErrorSurfaces(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("broken.yaml", types.ArtifactEntry{Inline: `kind: ConfigMap
metadata:
  name: {{ .context.instance_id
`})

	_, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "{{", "}}")
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for malformed Go template syntax")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("expected CATALOG_ERROR prefix, got %q", msg)
	}
	if !strings.Contains(msg, "broken.yaml") {
		t.Errorf("error should name the artifact, got %q", msg)
	}
	if !strings.Contains(msg, "parse") {
		t.Errorf("error should indicate the parse stage, got %q", msg)
	}
}

// ----- host projection: helm-chart [[ ]] delimiters preserve Helm {{ }} -----

func TestBuildArtifactsProjection_AlternateDelimitersPreserveHelmSyntax(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	dir := t.TempDir()
	p := filepath.Join(dir, "mixed.yaml")
	if err := os.WriteFile(p, []byte(`kind: ConfigMap
metadata:
  name: [[ .context.instance_id ]]-cfg
data:
  fullname: {{ .Values.fullname }}
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("mixed.yaml", types.ArtifactEntry{Import: p})

	proj, err := buildArtifactsProjection(tmplCtx, state, nodeCtx, "[[", "]]")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	content := string(proj.Entries[0].Content)
	if !strings.Contains(content, "name: api-cfg") {
		t.Errorf("[[ .context.instance_id ]] should resolve to api-cfg; content:\n%s", content)
	}
	if !strings.Contains(content, "{{ .Values.fullname }}") {
		t.Errorf("Helm-side {{ .Values.fullname }} must survive untouched; content:\n%s", content)
	}
}

// ----- placer: provenance labels + annotations on a K8s resource -----

func TestSpliceArtifactProvenance_ProvenanceLabelsOnK8sResource(t *testing.T) {
	t.Parallel()
	_, state, nodeCtx := newArtifactsCtx()
	nodeCtx.Provenance = ProvenanceContext{
		CompilerVersion:  "v0.0.0-test",
		AggregateName:    "demo",
		AggregateVersion: "1.0.0",
		ReleaseName:      "demo-prod",
		Environment:      "prod",
	}
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))

	body, warning, err := SpliceArtifactProvenance("deployment.yaml", []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  labels:
    author: alice
spec:
  replicas: 1
`), labels, annotations, true, nodeCtx.YAMLIndent)
	if err != nil {
		t.Fatalf("SpliceArtifactProvenance: %v", err)
	}
	if warning != "" {
		t.Errorf("unexpected warning: %q", warning)
	}
	gotLabels := readDocLabels(t, body)
	mustLabels := map[string]string{
		"app.kubernetes.io/name":      "api",
		"aggrega.ai/component-id":     "api",
		"aggrega.ai/compiler-version": "v0.0.0-test",
		"aggrega.ai/aggregate-name":   "demo",
		"aggrega.ai/environment":      "prod",
		"author":                      "alice",
	}
	for k, want := range mustLabels {
		if got := gotLabels[k]; got != want {
			t.Errorf("label %q = %q, want %q", k, got, want)
		}
	}
	for _, k := range []string{"aggrega.ai/recipe-uri", "aggrega.ai/boundary-path"} {
		if _, has := gotLabels[k]; has {
			t.Errorf("label %q present; a charset-unsafe key must be an annotation", k)
		}
	}
	gotAnnotations := readDocAnnotations(t, body)
	mustAnnotations := map[string]string{
		"aggrega.ai/recipe-uri":    "api-server-helm:1.0.0",
		"aggrega.ai/boundary-path": "production",
	}
	for k, want := range mustAnnotations {
		if got := gotAnnotations[k]; got != want {
			t.Errorf("annotation %q = %q, want %q", k, got, want)
		}
	}
}

// ----- placer: authored key order preserved through the splice -----

func TestSpliceArtifactProvenance_PreservesAuthoredKeyOrder(t *testing.T) {
	t.Parallel()
	_, state, nodeCtx := newArtifactsCtx()
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))

	body, _, err := SpliceArtifactProvenance("ordered.yaml", []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 1
`), labels, annotations, true, nodeCtx.YAMLIndent)
	if err != nil {
		t.Fatalf("SpliceArtifactProvenance: %v", err)
	}
	want := []string{"apiVersion:", "kind:", "metadata:", "spec:"}
	idx := 0
	for _, line := range strings.Split(string(body), "\n") {
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
		t.Fatalf("authored top-level key order not preserved:\n%s", body)
	}
}

// ----- placer: Q1a malformed metadata.labels rejected with CATALOG_ERROR -----

func TestSpliceArtifactProvenance_RejectsMalformedMetadataLabels(t *testing.T) {
	t.Parallel()
	_, state, nodeCtx := newArtifactsCtx()
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))

	_, _, err := SpliceArtifactProvenance("bad-labels.yaml", []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: bad
  labels:
    - foo
    - bar
`), labels, annotations, true, nodeCtx.YAMLIndent)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for malformed metadata.labels (sequence under labels)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("expected CATALOG_ERROR prefix, got %q", msg)
	}
	if !strings.Contains(msg, "bad-labels.yaml") {
		t.Errorf("error should name the artifact, got %q", msg)
	}
	if !strings.Contains(msg, "expected mapping, got sequence") {
		t.Errorf("error should name the unexpected shape, got %q", msg)
	}
}

// ----- placer: default-delims non-YAML output is a hard CATALOG_ERROR -----

func TestSpliceArtifactProvenance_DefaultDelimsParseFailIsError(t *testing.T) {
	t.Parallel()
	// A structural Helm action does not parse as YAML. With the default-delimiter
	// policy (the guest's target syntax was Aggrega's own {{ }}, already resolved),
	// a non-YAML result is an authoring bug and halts.
	_, _, err := SpliceArtifactProvenance("bad.yaml", []byte("metadata:\n  labels:\n    {{- include \"x\" . }}\n"),
		map[string]string{"aggrega.ai/component-id": "api"}, nil, true, 2)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for non-YAML output under the default-delimiter policy")
	}
	if !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("error should indicate invalid YAML, got %q", err)
	}
}

// ----- placer: alternate-delims structural Helm skips provenance and warns -----

func TestSpliceArtifactProvenance_AlternateDelimsStructuralHelmSkipsAndWarns(t *testing.T) {
	t.Parallel()
	content := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
  labels:
    {{- include "common.labels" . | nindent 4 }}
`)
	body, warning, err := SpliceArtifactProvenance("with-include.yaml", content,
		map[string]string{"aggrega.ai/component-id": "api"}, nil, false, 2)
	if err != nil {
		t.Fatalf("expected no error for a non-round-trippable alternate-delims file, got %v", err)
	}
	if !strings.Contains(string(body), `{{- include "common.labels" . | nindent 4 }}`) {
		t.Errorf("structural Helm syntax should pass through unchanged; body:\n%s", body)
	}
	if warning == "" || !strings.Contains(warning, "with-include.yaml") || !strings.Contains(warning, "skipped provenance label injection") {
		t.Errorf("expected a skip warning naming the file, got %q", warning)
	}
}

// ----- placer: alternate-delims quoted-scalar round-trips and gets provenance -----

func TestSpliceArtifactProvenance_AlternateDelimsQuotedScalarInjects(t *testing.T) {
	t.Parallel()
	_, state, nodeCtx := newArtifactsCtx()
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))
	// A quoted Helm scalar round-trips (the reference rides inside a string), so
	// provenance is injected and the Helm token survives.
	body, warning, err := SpliceArtifactProvenance("netpol.yaml", []byte(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: api-netpol
  namespace: "{{ .Release.Namespace }}"
spec:
  podSelector: {}
`), labels, annotations, false, nodeCtx.YAMLIndent)
	if err != nil {
		t.Fatalf("SpliceArtifactProvenance: %v", err)
	}
	if warning != "" {
		t.Errorf("round-trippable file should not warn; got %q", warning)
	}
	if readDocLabels(t, body)["aggrega.ai/component-id"] != "api" {
		t.Errorf("expected provenance injected; body:\n%s", body)
	}
	if !strings.Contains(string(body), "{{ .Release.Namespace }}") {
		t.Errorf("Helm token must survive; body:\n%s", body)
	}
}

// ----- placer: mixed quoting, both token copies survive byte-faithfully -----

func TestSpliceArtifactProvenance_MixedQuotingByteFaithful(t *testing.T) {
	t.Parallel()
	_, state, nodeCtx := newArtifactsCtx()
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))
	body, warning, err := SpliceArtifactProvenance("mixed-quoting.yaml", []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: api-cm
data:
  quoted: "{{ .Values.shared }}"
  unquoted: {{ .Values.shared }}
`), labels, annotations, false, nodeCtx.YAMLIndent)
	if err != nil {
		t.Fatalf("SpliceArtifactProvenance: %v", err)
	}
	if warning != "" {
		t.Errorf("byte-faithful splice should not warn; got %q", warning)
	}
	if !strings.Contains(string(body), "aggrega.ai/component-id") {
		t.Errorf("byte-faithful splice should inject provenance; body:\n%s", body)
	}
	if !strings.Contains(string(body), `quoted: "{{ .Values.shared }}"`) || !strings.Contains(string(body), "unquoted: {{ .Values.shared }}") {
		t.Errorf("both Helm token copies must survive byte-faithfully; body:\n%s", body)
	}
}

// ----- placer: compose fragment without metadata is a silent no-op -----

func TestSpliceArtifactProvenance_NoMetadataIsNoOp(t *testing.T) {
	t.Parallel()
	content := []byte("services:\n  sidecar:\n    image: busybox:1.36\n")
	body, warning, err := SpliceArtifactProvenance("extra-services.yaml", content,
		map[string]string{"aggrega.ai/component-id": "api"}, nil, true, 2)
	if err != nil {
		t.Fatalf("SpliceArtifactProvenance: %v", err)
	}
	if warning != "" {
		t.Errorf("no-metadata fragment should not warn; got %q", warning)
	}
	if len(readDocLabels(t, body)) != 0 {
		t.Errorf("a fragment without metadata should gain no labels; body:\n%s", body)
	}
}

// ----- phase B.3 dispatch: builds the projection and routes to the emitter -----

func TestPhaseB3_DispatchesToArtifactsEmitter(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("cm.yaml", types.ArtifactEntry{Inline: "kind: ConfigMap\n"})

	emitter := &stubArtifactsEmitter{}
	if err := phaseB3(context.Background(), tmplCtx, state, emitter, nodeCtx); err != nil {
		t.Fatalf("phaseB3: %v", err)
	}
	if len(emitter.got.Entries) != 1 || emitter.got.Entries[0].Name != "cm.yaml" {
		t.Fatalf("emitter did not receive the built projection; got %+v", emitter.got)
	}
	got := findEmitted(t, state.EmittedFiles, "cm.yaml")
	if got.Origin != EmitOriginArtifactsInline {
		t.Errorf("Origin = %v, want EmitOriginArtifactsInline", got.Origin)
	}
	if got.Intermediate {
		t.Errorf("flat emitter should not mark Intermediate")
	}
}

// ----- phase B.3 dispatch: a merge-assembler emitter marks Intermediate -----

func TestPhaseB3_MergeAssemblerMarksIntermediate(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("frag.yaml", types.ArtifactEntry{Inline: "services:\n  x: {}\n"})

	emitter := &stubArtifactsEmitter{assembly: "merge"}
	if err := phaseB3(context.Background(), tmplCtx, state, emitter, nodeCtx); err != nil {
		t.Fatalf("phaseB3: %v", err)
	}
	if !findEmitted(t, state.EmittedFiles, "frag.yaml").Intermediate {
		t.Errorf("a merge-assembler emitter must mark its artifacts Intermediate")
	}
}

// shippedMariadbRdsBody is a verbatim copy of the main.tf artifact body from the
// shipped mariadb-rds terraform recipe. Embedded (not read from the catalog) so
// this unit test stays self-contained. It exercises the catalog template
// functions (trimSuffix, toJson, until, int, default) so the field-access
// pre-check and the host template evaluation are proven against a real-consumer
// body, not a simplified stand-in.
const shippedMariadbRdsBody = `resource "aws_db_instance" "{{ .locals.db_identifier }}" {
  identifier     = "{{ .locals.db_identifier }}"
  engine         = "mariadb"
  engine_version = "{{ .infra.engine_version }}"
  port           = {{ .values.port }}
  {{- if .infra.overrides }}
  {{- if .infra.overrides.instance_class }}
  instance_class = "{{ .infra.overrides.instance_class }}"
  {{- else }}
  instance_class = "db.t3.small"
  {{- end }}
  {{- else }}
  instance_class = "db.t3.small"
  {{- end }}
  allocated_storage = {{ trimSuffix "Gi" .infra.storage.size }}
  storage_type      = "{{ .infra.storage.type }}"
  db_name  = "{{ .values.database }}"
  {{- if .infra.overrides }}
  multi_az = {{ .infra.overrides.multi_az | default .infra.ha.enabled }}
  {{- else }}
  multi_az = {{ .infra.ha.enabled }}
  {{- end }}
  {{- if .infra.overrides.vpc_security_groups }}
  vpc_security_group_ids = {{ toJson .infra.overrides.vpc_security_groups }}
  {{- end }}
}
{{- if gt (int .infra.ha.read_replicas) 0 }}
{{- range $i := until (int .infra.ha.read_replicas) }}
resource "aws_db_instance" "{{ $.locals.db_identifier }}_replica_{{ $i }}" {
  identifier          = "{{ $.locals.db_identifier }}-replica-{{ $i }}"
  replicate_source_db = aws_db_instance.{{ $.locals.db_identifier }}.identifier
}
{{- end }}
{{- end }}
`

// TestBuildArtifactsProjection_ShippedMariadbRdsBodyResolves is the real-consumer
// acceptance for the host projection's template evaluation: the shipped
// mariadb-rds main.tf body — including its trimSuffix/toJson/until/int/default
// catalog-function expressions — both passes the field-access pre-check and
// resolves to concrete HCL with no residual template delimiters.
func TestBuildArtifactsProjection_ShippedMariadbRdsBodyResolves(t *testing.T) {
	t.Parallel()
	tmplCtx := &types.TemplateContext{
		Context: types.ContextBlock{InstanceID: "prod-mariadb"},
		Values:  types.OrderedMap[any]{},
		Infra:   types.OrderedMap[any]{},
		Locals:  types.OrderedMap[string]{},
	}
	tmplCtx.Locals.Set("db_identifier", "prod-mariadb")
	tmplCtx.Values.Set("port", 3306)
	tmplCtx.Values.Set("database", "appdb")
	tmplCtx.Infra.Set("engine_version", "10.6")
	tmplCtx.Infra.Set("storage", map[string]any{"size": "100Gi", "type": "gp3"})
	tmplCtx.Infra.Set("ha", map[string]any{"enabled": true, "read_replicas": 2})
	tmplCtx.Infra.Set("overrides", map[string]any{
		"multi_az":            true,
		"vpc_security_groups": []string{"sg-abc", "sg-def"},
	})

	r := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "mariadb-rds", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: "terraform"},
	}
	state := NewPhaseBState(r, "prod-mariadb", "production", "recipe.yaml")
	state.Recipe.Spec.Artifacts.Set("main.tf", types.ArtifactEntry{Inline: shippedMariadbRdsBody})

	proj, err := buildArtifactsProjection(tmplCtx, state, NodeRenderContext{NodeID: "prod-mariadb"}, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection on shipped body: %v", err)
	}
	body := string(proj.Entries[0].Content)
	if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
		t.Errorf("body still contains unresolved template delimiters:\n%s", body)
	}
	for _, want := range []string{
		"allocated_storage = 100",
		"port           = 3306",
		"multi_az = true",
		`vpc_security_group_ids = ["sg-abc","sg-def"]`,
		`resource "aws_db_instance" "prod-mariadb_replica_0"`,
		`resource "aws_db_instance" "prod-mariadb_replica_1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("resolved body missing %q; full body:\n%s", want, body)
		}
	}
}

// ----- phase B.3 dispatch: an engine that is not an ArtifactsEmitter halts -----

func TestPhaseB3_NonEmitterEngineHalts(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("cm.yaml", types.ArtifactEntry{Inline: "kind: ConfigMap\n"})

	err := phaseB3(context.Background(), tmplCtx, state, stubEngine{}, nodeCtx)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR for an engine that does not implement ArtifactsEmitter")
	}
	if !strings.Contains(err.Error(), "CATALOG_ERROR") || !strings.Contains(err.Error(), "artifacts emission") {
		t.Errorf("error should be a CATALOG_ERROR naming the missing capability, got %q", err)
	}
}
