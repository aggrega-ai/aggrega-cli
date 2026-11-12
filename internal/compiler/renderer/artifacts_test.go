// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// stubEngine is a no-op Provider used by tests across the package that need a
// Provider value but not real rendering.
type stubEngine struct{}

func (stubEngine) Render(_ context.Context, _ map[string]any, _ NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}

// Capabilities satisfies the Provider interface for the test stub. Permissive (all
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
		APIVersion: "aggrega.ai/v1beta1",
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

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
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

// ----- host projection: imported entry (name is the authored key) -----

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
	// The key deliberately differs from the imported file's own name: the key names
	// the output, and the imported file's name selects the content.
	state.Recipe.Spec.Artifacts.Set("network-policy.yaml", types.ArtifactEntry{Import: p})

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	e := proj.Entries[0]
	if e.Name != "network-policy.yaml" {
		t.Errorf("Name = %q, want network-policy.yaml (the authored key)", e.Name)
	}
	if !strings.Contains(string(e.Content), "name: deny-all") {
		t.Errorf("content did not come from the imported file:\n%s", e.Content)
	}
	if e.Origin != "import" {
		t.Errorf("Origin = %q, want import", e.Origin)
	}
}

// E6: the resolution-time freeze / build-time verify contract for $import
// artifact files, at the half this tier can prove — that the walk retains the
// recorded pin for exactly the authoring form that carries one.
//
// The comparison itself is a declared rule reading two facts, so the halt is
// driven at the entry point rather than here: the rule needs the presentation
// over this retention, and a state assembled directly carries none. The
// production wiring, the halt and its message are proved in the invocation
// tests over RenderAll; what this fixes is the input those rules read.
//
// The form gate lives at the recording site, so the three rows below are the
// whole of it: an imported entry with a recorded pin retains that pin, an
// imported entry in a Recipe with no pin recorded retains nothing, and an inline entry
// retains nothing even where a pin was authored under its key. The last is the
// one a reader is most likely to expect the other way — a hand-authored pin on
// an inline entry is admitted by the schema and compared by nothing, because an
// inline entry is part of the entity and is pinned by whatever pins that.
func TestBuildArtifactsProjection_ImportPinRetention(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "policy.yaml")
	body := []byte("kind: NetworkPolicy\nmetadata:\n  name: deny-all\n")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	frozen := digest.ContentDigest(body)

	for _, row := range []struct {
		name     string
		entry    types.ArtifactEntry
		pins     []types.ArtifactDigest
		wantPin  string
		wantHeld bool
	}{
		{
			name:     "imported entry with a recorded pin retains it",
			entry:    types.ArtifactEntry{Import: p},
			pins:     []types.ArtifactDigest{{Name: "policy", Digest: frozen}},
			wantPin:  frozen,
			wantHeld: true,
		},
		{
			name:  "imported entry in a Recipe that was never frozen retains nothing",
			entry: types.ArtifactEntry{Import: p},
		},
		{
			name:  "inline entry retains nothing even where a pin names its key",
			entry: types.ArtifactEntry{Inline: "kind: Inline\n"},
			pins:  []types.ArtifactDigest{{Name: "policy", Digest: frozen}},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			tmplCtx, state, nodeCtx := newArtifactsCtx()
			store := newHydratedProjections(NodeProvides{})
			state.hydrated = store
			resetArtifacts(state)
			state.Recipe.Spec.Artifacts.Set("policy", row.entry)
			state.RecordedPins = recipepin.Pins{ArtifactDigests: row.pins}

			if _, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}"); err != nil {
				t.Fatalf("buildArtifactsProjection: %v", err)
			}
			proj, ok := store.projection("api")
			if !ok {
				t.Fatalf("the render recorded nothing for the node")
			}
			pin, held := proj.ArtifactRecordedContentPin.Get("policy")
			if held != row.wantHeld {
				t.Fatalf("pin held = %v, want %v.\n"+
					"INVARIANT: presence is what says a comparison is owed, and the form gate that "+
					"decides it lives at the recording site — so a reader cannot pair a pin with an "+
					"authoring form that carries none.", held, row.wantHeld)
			}
			if pin != row.wantPin {
				t.Errorf("retained pin = %q, want %q", pin, row.wantPin)
			}
		})
	}
}

// The tampered-file direction at this tier: the retention still carries the pin
// frozen at resolution, and the text beside it is the file's CURRENT content — so
// the two disagree, which is the input the declared rule refuses.
//
// It is the positive control for the retention rows above: without it a recording
// that stored the text's own digest rather than the one recorded beside the Recipe would
// satisfy every row there and never disagree with anything.
func TestBuildArtifactsProjection_TamperedImportLeavesTheTwoFactsDisagreeing(t *testing.T) {
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	store := newHydratedProjections(NodeProvides{})
	state.hydrated = store

	dir := t.TempDir()
	p := filepath.Join(dir, "policy.yaml")
	body := []byte("kind: NetworkPolicy\nmetadata:\n  name: deny-all\n")
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	frozen := digest.ContentDigest(body)

	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("policy", types.ArtifactEntry{Import: p})
	state.RecordedPins = recipepin.Pins{ArtifactDigests: []types.ArtifactDigest{{Name: "policy", Digest: frozen}}}

	if err := os.WriteFile(p, []byte("kind: NetworkPolicy\nmetadata:\n  name: allow-all\n"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}"); err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}

	proj, ok := store.projection("api")
	if !ok {
		t.Fatalf("the render recorded nothing for the node")
	}
	pin, held := proj.ArtifactRecordedContentPin.Get("policy")
	if !held || pin != frozen {
		t.Fatalf("retained pin = (%q, %v), want the digest frozen at resolution", pin, held)
	}
	text, _ := proj.ArtifactTemplateText.Get("policy")
	if got := digest.ContentDigest([]byte(text)); got == pin {
		t.Fatalf("the retained text digests to the recorded pin after the file was tampered, "+
			"so the two facts cannot disagree and the rule over them can never fire.\n"+
			"text digest %s, recorded pin %s", got, pin)
	}
	if !strings.Contains(text, "allow-all") {
		t.Errorf("the retained text is not the file's current content: %q", text)
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

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
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

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
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

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
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

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if err != nil {
		t.Fatalf("buildArtifactsProjection: %v", err)
	}
	if !strings.Contains(string(proj.Entries[0].Content), "chart: nginx") {
		t.Errorf("expected templated chart name, got:\n%s", proj.Entries[0].Content)
	}
}

// ----- host projection: nil .workload still halts, and does not emit -----

// TestBuildArtifactsProjection_NilWorkloadDescentStillHalts holds the one property
// of this state that the walk itself owns: a template descending through a pillar
// the Recipe does not declare halts with a CATALOG_ERROR rather than emitting a
// file built from a value nobody authored.
//
// It formerly also asserted that the halt names the failing expression and hints
// that no workload is declared. Those are diagnostic-QUALITY assertions and they
// belonged to the pre-check's pillar arms, which are gone: this state is a
// hand-assembled one carrying no presentation, so no declared value reads its
// pillar facts and the halt here comes from evaluation. The quality contract moved
// with the mechanism rather than being dropped, and it is asserted more strictly
// than these two lines did — TestTheArtifactPillarRuleObjectsInBothDirections
// compares each objection's exact detail, the offending expression and the
// captioned artifact included, over both arms; and
// TestTheArtifactPillarRuleHaltsTheBuildOnANodesOwnRecipe reads the same wording
// off a real build's halt.
func TestBuildArtifactsProjection_NilWorkloadDescentStillHalts(t *testing.T) {
	t.Parallel()
	tmplCtx, state, nodeCtx := newArtifactsCtx()
	resetArtifacts(state)
	state.Recipe.Spec.Artifacts.Set("monitor.yaml", types.ArtifactEntry{Inline: `kind: ServiceMonitor
spec:
  port: {{ .workload.containers.main.ports.web }}
`})

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
	if err == nil {
		t.Fatalf("expected CATALOG_ERROR for a .workload descent with no workload declared; "+
			"the walk projected %d entry/entries instead", len(proj.Entries))
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("expected CATALOG_ERROR prefix, got %q", msg)
	}
	if len(proj.Entries) != 0 {
		t.Errorf("the halting walk still projected %d entry/entries; want none — a file built from "+
			"an undeclared pillar must not reach a guest", len(proj.Entries))
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

	_, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
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

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "[[", "]]")
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
`), labels, annotations, true, nodeCtx.YAMLIndent, false)

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
`), labels, annotations, true, nodeCtx.YAMLIndent, false)

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
`), labels, annotations, true, nodeCtx.YAMLIndent, false)

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
	_, _, err := SpliceArtifactProvenance("bad.yaml", []byte("metadata:\n  labels:\n    {{- include \"x\" . }}\n"), map[string]string{"aggrega.ai/component-id": "api"}, nil, true, 2, false)

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
	body, warning, err := SpliceArtifactProvenance("with-include.yaml", content, map[string]string{"aggrega.ai/component-id": "api"}, nil, false, 2, false)

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
`), labels, annotations, false, nodeCtx.YAMLIndent, false)

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
`), labels, annotations, false, nodeCtx.YAMLIndent, false)

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
	body, warning, err := SpliceArtifactProvenance("extra-services.yaml", content, map[string]string{"aggrega.ai/component-id": "api"}, nil, true, 2, false)

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

	proj, err := buildArtifactsProjection(nil, tmplCtx, state, NodeRenderContext{NodeID: "prod-mariadb"}, "{{", "}}")
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

// ----- library bundle file names that name no file of their own beneath the key -----

// TestABundleFileNamingNoFileBeneathItsKeyHalts drives every spelling of a bundle
// file name that stays under its consumption key yet names no file of its own
// there through the real artifact walk, and asserts each halts with the
// condition's own identity, naming the bundle file and the key.
//
// These are the names a containment check alone admits: each stays at or under
// the key, so nothing about leaving the directory refuses it. Two groups, by what
// goes wrong once the name is composed under the key. "." and every name that
// cleans to it put the file at the key itself, so the key's directory becomes a
// file. A name with a trailing or doubled separator or a "." or ".." part is a
// second spelling of a file another bundle name can carry: the walk's collision
// guard compares the composed names as written, so "a//b" beside "a/b" reaches
// the writer as two outputs, and one silently overwrites the other.
//
// Each row is driven in a subtest that recovers a panic and fails on it, because
// testing.T does not fail a subtest whose panic was recovered, and a walk that
// panicked on one of these names would otherwise read as having refused it.
func TestABundleFileNamingNoFileBeneathItsKeyHalts(t *testing.T) {
	t.Parallel()

	const key = "widgets"
	cases := []struct {
		name  string
		fname string
	}{
		{"the key itself", "."},
		{"the key itself, with a trailing separator", "./"},
		{"a climb back onto the key", "x/.."},
		{"a climb back onto the key, with a trailing separator", "x/../"},
		{"a deeper climb back onto the key", "a/b/../.."},
		{"a trailing separator", "x/"},
		{"a doubled separator", "a//b"},
		{"a leading dot segment", "./a"},
		{"an interior dot segment", "a/./b"},
		{"an interior climb", "a/../b"},
		{"the empty name", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("bundle file %q under key %q panicked instead of halting: %v", tc.fname, key, r)
				}
			}()

			tmplCtx, state, nodeCtx := newArtifactsCtx()
			resetArtifacts(state)
			state.Recipe.Spec.Artifacts.Set(key, types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
			state.ArtifactLibraries = map[string]types.ArtifactLibrary{
				"SharedCRDs": artifactLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
					[]string{tc.fname}, []string{"kind: ConfigMap\n"}),
			}

			proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
			if !errors.Is(err, ErrArtifactLibraryFileEscapesKey()) {
				t.Fatalf("bundle file %q under key %q: got %v (projected %d entr(ies)); want ErrArtifactLibraryFileEscapesKey",
					tc.fname, key, err, len(proj.Entries))
			}
			msg := err.Error()
			for _, want := range []string{`artifact "` + key + `"`, `bundle file "` + tc.fname + `"`, "rename the file"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the diagnostic does not carry %q:\n%s", want, msg)
				}
			}
		})
	}

	// The positive row, through the same walk: a nested layout written plainly is
	// admitted and composed under the key unchanged, so the refusals above are a
	// statement about the names rather than about the fixture.
	t.Run("a nested layout written plainly is admitted", func(t *testing.T) {
		t.Parallel()
		tmplCtx, state, nodeCtx := newArtifactsCtx()
		resetArtifacts(state)
		state.Recipe.Spec.Artifacts.Set(key, types.ArtifactEntry{Import: "SharedCRDs#/artifacts/WidgetCRD"})
		state.ArtifactLibraries = map[string]types.ArtifactLibrary{
			"SharedCRDs": artifactLibraryFixtureOrdered("SharedCRDs", "WidgetCRD",
				[]string{"rbac/role.yaml", "a/b"}, []string{"kind: Role\n", "kind: ConfigMap\n"}),
		}
		proj, err := buildArtifactsProjection(nil, tmplCtx, state, nodeCtx, "{{", "}}")
		if err != nil {
			t.Fatalf("a bundle written plainly halted: %v", err)
		}
		var got []string
		for _, e := range proj.Entries {
			got = append(got, e.Name)
		}
		if strings.Join(got, ",") != key+"/rbac/role.yaml,"+key+"/a/b" {
			t.Errorf("projected %v; want [%s/rbac/role.yaml %s/a/b]", got, key, key)
		}
	})
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
