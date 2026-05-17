// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// fullstackAppInspectCatalog is the catalog tree the annotation-driven
// inspect-output assertions point at. Recursive walk covers base
// entities + per-variant recipes; the helm-synthesized variant supplies
// the Component recipes the fullstack-app IR builder consumes.
const fullstackAppInspectCatalog = "../../internal/compiler/testdata/fullstack-app/catalog"

// runInspectCaptured wires the captureStdout helper to a runInspect
// call against a Release file with the fullstack-app catalog set. The
// returned string is the per-component Feature-attachment block the
// inspect command emits.
func runInspectCaptured(t *testing.T, releasePath string) string {
	t.Helper()
	prior := inspectCatalogDir
	inspectCatalogDir = fullstackAppInspectCatalog
	defer func() { inspectCatalogDir = prior }()
	out, err := captureStdout(func() error {
		return runInspect(nil, []string{releasePath})
	})
	if err != nil {
		t.Fatalf("runInspect(%q): %v", releasePath, err)
	}
	return out
}

func TestInspect_AnnotationBoundary_Explicit_UniformBracket(t *testing.T) {
	out := runInspectCaptured(t, "../../internal/compiler/testdata/fullstack-app/releases/annotation-boundary-explicit.yaml")
	if !strings.Contains(out, "feature.myco/rewrite-host") {
		t.Errorf("inspect output missing rewrite-host Feature; got:\n%s", out)
	}
	if !strings.Contains(out, "[explicit]") {
		t.Errorf("inspect output missing [explicit] bracket on explicit pair; got:\n%s", out)
	}
}

func TestInspect_AnnotationBoundary_Annotation_UniformBracket(t *testing.T) {
	out := runInspectCaptured(t, "../../internal/compiler/testdata/fullstack-app/releases/annotation-boundary-annotation.yaml")
	if !strings.Contains(out, "feature.myco/rewrite-host") {
		t.Errorf("inspect output missing rewrite-host Feature; got:\n%s", out)
	}
	if !strings.Contains(out, "[annotation: feature.myco/rewrite-host from") {
		t.Errorf("inspect output missing [annotation: ...] bracket on annotation pair; got:\n%s", out)
	}
}

func TestInspect_AnnotationCrossSide_Explicit_PerLeafBrackets(t *testing.T) {
	out := runInspectCaptured(t, "../../internal/compiler/testdata/fullstack-app/releases/annotation-cross-side-explicit.yaml")
	// Explicit cross-side: both pillars contributed explicitly; per-leaf
	// brackets render [explicit] on every leaf rather than the compact
	// per-attachment marker.
	if !strings.Contains(out, "db-pool") {
		t.Errorf("inspect output missing db-pool Feature; got:\n%s", out)
	}
	if !strings.Contains(out, "[explicit]") {
		t.Errorf("inspect output missing [explicit] bracket on explicit cross-side; got:\n%s", out)
	}
}

func TestInspect_AnnotationCrossSide_Annotation_HeterogeneousBrackets(t *testing.T) {
	out := runInspectCaptured(t, "../../internal/compiler/testdata/fullstack-app/releases/annotation-cross-side-annotation.yaml")
	// Annotation cross-side: per-leaf brackets attribute each pillar's
	// authoring side. Values pillar leaf carries an aggregate-sourced
	// annotation; infra pillar leaf carries a release-sourced annotation.
	if !strings.Contains(out, "db-pool") {
		t.Errorf("inspect output missing db-pool Feature; got:\n%s", out)
	}
	if !strings.Contains(out, "[annotation: db-pool from") {
		t.Errorf("inspect output missing [annotation: db-pool from ...] bracket on cross-side annotation pair; got:\n%s", out)
	}
	if !strings.Contains(out, "aggregate") {
		t.Errorf("inspect output should mention aggregate source for the values-pillar leaf; got:\n%s", out)
	}
	if !strings.Contains(out, "release") {
		t.Errorf("inspect output should mention release source for the infra-pillar leaf; got:\n%s", out)
	}
}

// TestInspect_OciUri_ReturnsNotImplemented pins the OCI-URI branch's
// not-yet-implemented error message. The branch returns a
// forward-compatible error rather than silently falling through to
// the catalog-name path; operators see a clear signal that OCI
// inspect is deferred.
func TestInspect_OciUri_ReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	err := runInspect(nil, []string{"oci://registry.example.com/components/api:1.0.0"})
	if err == nil {
		t.Fatal("expected not-implemented error for oci:// argument; got nil")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error message should signal not-yet-implemented; got: %v", err)
	}
}

// TestInspect_UnknownKind_ReturnsError pins the kind-dispatch
// forward-compatibility behavior. A Manifest file (or any other
// non-Component/non-Release kind) returns the "not yet implemented for
// kind X" error so future inspector branches can be added without
// breaking the existing dispatch surface.
func TestInspect_UnknownKind_ReturnsError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.yaml")
	writeFixture(t, manifestPath, `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	err := runInspect(nil, []string{manifestPath})
	if err == nil {
		t.Fatal("expected not-implemented-for-kind error for Manifest input; got nil")
	}
	if !strings.Contains(err.Error(), "kind Manifest") {
		t.Errorf("error should name the kind; got: %v", err)
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error should signal not-yet-implemented; got: %v", err)
	}
}

// TestInspect_MissingKind_ReturnsError pins the missing-preamble
// failure mode. A YAML file without an apiVersion/kind preamble can't
// be inspected by kind dispatch; the error message tells the operator
// exactly what's missing.
func TestInspect_MissingKind_ReturnsError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bogusPath := filepath.Join(root, "bogus.yaml")
	writeFixture(t, bogusPath, `some_field: value
another_field: 42
`)
	err := runInspect(nil, []string{bogusPath})
	if err == nil {
		t.Fatal("expected error on missing-kind file; got nil")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error should mention the missing kind; got: %v", err)
	}
}

// TestInspect_ComponentFilePath_RendersSchema pins the Component-file
// branch. A Component authored as a standalone YAML file inspects with
// the same output shape as the catalog-name path's schema rendering:
// a header line naming the Component and its source path, followed by
// the rendered schema body.
func TestInspect_ComponentFilePath_RendersSchema(t *testing.T) {
	root := t.TempDir()
	compPath := filepath.Join(root, "component.yaml")
	writeFixture(t, compPath, `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
spec:
  schema: |
    values: { port?: int | *8080 }
`)

	out, err := captureStdout(func() error {
		return runInspect(nil, []string{compPath})
	})
	if err != nil {
		t.Fatalf("Component file path should inspect cleanly; got: %v", err)
	}
	if !strings.Contains(out, "// Component: core/server") {
		t.Errorf("output missing Component header naming core/server; got:\n%s", out)
	}
	if !strings.Contains(out, compPath) {
		t.Errorf("output missing source path suffix on the header; got:\n%s", out)
	}
	if !strings.Contains(out, "port?: int | *8080") {
		t.Errorf("output missing rendered schema body; got:\n%s", out)
	}
}

// captureStdout redirects os.Stdout for the duration of fn, returning
// what fn wrote. Used by tests that need to assert on user-facing
// output of commands that print directly to stdout.
//
// Pipe teardown runs in a defer so a panic in fn still closes the
// writer, unblocks the reader goroutine, and drains the captured
// buffer — the goroutine would otherwise leak past the test and (if
// the panic doesn't tear down the process) hold the pipe's file
// descriptor. Named returns let the defer populate the output after
// the drain completes, so callers see what fn actually wrote.
func captureStdout(fn func() error) (out string, fnErr error) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("pipe: %w", err)
	}
	os.Stdout = w

	done := make(chan struct{})
	var captured strings.Builder
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				captured.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		close(done)
	}()

	defer func() {
		os.Stdout = originalStdout
		w.Close()
		<-done
		r.Close()
		out = captured.String()
	}()

	fnErr = fn()
	return
}

// TestIsPathInput pins the input-form detector that drives kind
// dispatch. Path-shaped inputs route to the file loader; non-path
// inputs route to the catalog-name path.
func TestIsPathInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"./release.yaml", true},
		{"../release.yaml", true},
		{"/abs/path/release.yaml", true},
		{"release.yaml", true},
		{"release.yml", true},
		{"core/server", false},
		{"core/server/api-server", false},
		{"some-name", false},
	}
	for _, c := range cases {
		got := isPathInput(c.in)
		if got != c.want {
			t.Errorf("isPathInput(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestProvenanceBracket pins the trailing-bracket format for both
// explicit and annotation-driven Feature attachments when the
// attachment's per-leaf provenance is uniform. The bracket format is
// part of the inspect command's user-facing contract on Release
// inspection; regression here would silently break operator-readable
// output.
func TestProvenanceBracket(t *testing.T) {
	t.Parallel()
	// Explicit attachment: the bracket reads exactly [explicit].
	got := perAttachmentBracketShim("explicit", "", "")
	if got != "[explicit]" {
		t.Errorf("explicit bracket = %q, want [explicit]", got)
	}

	// Annotation attachment: the bracket carries the annotation key
	// and source location.
	got = perAttachmentBracketShim("annotation", "feature.myco/rewrite-host", "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host")
	want := "[annotation: feature.myco/rewrite-host from release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host]"
	if got != want {
		t.Errorf("annotation bracket = %q, want %q", got, want)
	}
}

// perAttachmentBracketShim constructs an IRFeatureEntry with the given
// provenance fields and renders its bracket. Wraps the package-private
// perAttachmentBracket so the test can name field values without an
// otherwise-noisy literal construction.
func perAttachmentBracketShim(origin, key, source string) string {
	return perAttachmentBracket(types.IRFeatureEntry{
		Origin:           origin,
		AnnotationKey:    key,
		AnnotationSource: source,
	})
}

// Per-leaf provenance rendering tests. featureProvenanceLayout decides
// uniform vs heterogeneous; uniform attachments use perAttachmentBracket
// (pre-Phase-F goldens preserved); heterogeneous attachments use
// perLeafBracket per leaf with the dotted-path leaf label.

func annotationProvenance(key, source, side string) types.FieldProvenance {
	return types.FieldProvenance{
		Origin:           "annotation",
		Side:             side,
		AnnotationKey:    key,
		AnnotationSource: source,
	}
}

func explicitProvenance() types.FieldProvenance {
	return types.FieldProvenance{Origin: "explicit"}
}

func TestFeatureProvenanceLayout_AllExplicit_Uniform(t *testing.T) {
	t.Parallel()
	entry := types.IRFeatureEntry{
		Values:           orderedMapAny("enabled", true),
		ValuesProvenance: orderedMapProvenance("enabled", explicitProvenance()),
	}
	if !featureProvenanceLayout(entry) {
		t.Error("all-explicit single leaf should be uniform")
	}
}

func TestFeatureProvenanceLayout_AllSameAnnotation_Uniform(t *testing.T) {
	t.Parallel()
	prov := annotationProvenance("feature.myco/db-pool", "aggregate.yaml:...db-pool", "aggregate")
	entry := types.IRFeatureEntry{
		Values: orderedMapAny("pool", "shared", "replicas", 5),
		ValuesProvenance: orderedMapProvenance(
			"pool", prov,
			"replicas", prov,
		),
	}
	if !featureProvenanceLayout(entry) {
		t.Error("two leaves with identical annotation provenance should be uniform")
	}
}

func TestFeatureProvenanceLayout_CrossSide_Heterogeneous(t *testing.T) {
	t.Parallel()
	// Values pillar from Aggregate-side annotation; Infra pillar from
	// Release-side annotation. Cross-side compose → heterogeneous.
	entry := types.IRFeatureEntry{
		Values:           orderedMapAny("pool", "shared-pool"),
		Infra:            orderedMapAny("pool_size", 20),
		ValuesProvenance: orderedMapProvenance("pool", annotationProvenance("feature.myco/db-pool", "aggregate.yaml", "aggregate")),
		InfraProvenance:  orderedMapProvenance("pool_size", annotationProvenance("feature.myco/db-pool", "release.yaml", "release")),
	}
	if featureProvenanceLayout(entry) {
		t.Error("cross-side compose should be heterogeneous (different Side values)")
	}
}

func TestFeatureProvenanceLayout_ExplicitOverridesAnnotation_Heterogeneous(t *testing.T) {
	t.Parallel()
	suppressed := annotationProvenance("feature.myco/db-pool", "aggregate.yaml", "aggregate")
	overrideProv := types.FieldProvenance{
		Origin:              "explicit",
		OverridesAnnotation: &suppressed,
	}
	entry := types.IRFeatureEntry{
		Values:           orderedMapAny("pool", "explicit-value"),
		ValuesProvenance: orderedMapProvenance("pool", overrideProv),
	}
	if featureProvenanceLayout(entry) {
		t.Error("attachment with OverridesAnnotation on any leaf should be heterogeneous")
	}
}

func TestPerLeafBracket_Explicit(t *testing.T) {
	t.Parallel()
	got := perLeafBracket(explicitProvenance())
	if got != "[explicit]" {
		t.Errorf("explicit leaf bracket = %q, want [explicit]", got)
	}
}

func TestPerLeafBracket_Annotation(t *testing.T) {
	t.Parallel()
	got := perLeafBracket(annotationProvenance("feature.myco/db-pool", "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool", "aggregate"))
	want := "[annotation: feature.myco/db-pool from aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool]"
	if got != want {
		t.Errorf("annotation leaf bracket = %q, want %q", got, want)
	}
}

func TestPerLeafBracket_ExplicitOverridesAnnotation(t *testing.T) {
	t.Parallel()
	suppressed := annotationProvenance("feature.myco/db-pool", "aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool", "aggregate")
	got := perLeafBracket(types.FieldProvenance{
		Origin:              "explicit",
		OverridesAnnotation: &suppressed,
	})
	want := "[explicit; overrides annotation: feature.myco/db-pool from aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool]"
	if got != want {
		t.Errorf("override bracket = %q, want %q", got, want)
	}
}

func TestWalkLeavesInOrder_NestedOrderedMaps_FlattenToDottedPaths(t *testing.T) {
	t.Parallel()
	// IRFeatureEntry whose Values pillar carries a nested OrderedMap
	// (the shape the IR builder produces when an annotation routes to
	// a binding path like values.ingress.host). The renderer must flatten through
	// the nested intermediate and emit one leaf record per primitive
	// leaf with the dotted-path label, looking up FieldProvenance by
	// the flat-dotted-leaf key.
	innerVals := types.OrderedMap[any]{}
	innerVals.Set("host", "api.example.com")
	innerVals.Set("port", 443)
	values := types.OrderedMap[any]{}
	values.Set("ingress", &innerVals)

	hostProv := annotationProvenance("feature.myco/host", "aggregate.yaml:components.api.annotations.feature.myco/host", "aggregate")
	portProv := annotationProvenance("feature.myco/port", "aggregate.yaml:components.api.annotations.feature.myco/port", "aggregate")
	provenance := types.OrderedMap[types.FieldProvenance]{}
	provenance.Set("ingress.host", hostProv)
	provenance.Set("ingress.port", portProv)

	entry := types.IRFeatureEntry{
		Values:           values,
		ValuesProvenance: provenance,
	}
	leaves := walkLeavesInOrder(entry)
	if len(leaves) != 2 {
		t.Fatalf("expected 2 flattened leaves; got %d: %v", len(leaves), leaves)
	}
	wantPaths := []string{"values.ingress.host", "values.ingress.port"}
	wantValues := []any{"api.example.com", 443}
	for i, leaf := range leaves {
		if leaf.path != wantPaths[i] {
			t.Errorf("leaves[%d].path = %q, want %q", i, leaf.path, wantPaths[i])
		}
		if leaf.value != wantValues[i] {
			t.Errorf("leaves[%d].value = %v, want %v", i, leaf.value, wantValues[i])
		}
	}
	if leaves[0].provenance.AnnotationKey != "feature.myco/host" {
		t.Errorf("leaves[0].provenance key = %q, want feature.myco/host", leaves[0].provenance.AnnotationKey)
	}
	if leaves[1].provenance.AnnotationKey != "feature.myco/port" {
		t.Errorf("leaves[1].provenance key = %q, want feature.myco/port", leaves[1].provenance.AnnotationKey)
	}
}

func TestWalkLeavesInOrder_ValuesThenInfra_AuthorOrderPreserved(t *testing.T) {
	t.Parallel()
	entry := types.IRFeatureEntry{
		Values:           orderedMapAny("alpha", 1, "bravo", 2),
		Infra:            orderedMapAny("charlie", 3),
		ValuesProvenance: orderedMapProvenance("alpha", explicitProvenance(), "bravo", explicitProvenance()),
		InfraProvenance:  orderedMapProvenance("charlie", explicitProvenance()),
	}
	leaves := walkLeavesInOrder(entry)
	if len(leaves) != 3 {
		t.Fatalf("want 3 leaves, got %d", len(leaves))
	}
	wantPaths := []string{"values.alpha", "values.bravo", "infra.charlie"}
	for i, leaf := range leaves {
		if leaf.path != wantPaths[i] {
			t.Errorf("leaves[%d].path = %q, want %q", i, leaf.path, wantPaths[i])
		}
	}
}

// orderedMapAny + orderedMapProvenance are tiny test-local builders that
// preserve author order when constructing OrderedMap entries inline.
func orderedMapAny(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

func orderedMapProvenance(pairs ...any) types.OrderedMap[types.FieldProvenance] {
	m := types.OrderedMap[types.FieldProvenance]{}
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1].(types.FieldProvenance))
	}
	return m
}
