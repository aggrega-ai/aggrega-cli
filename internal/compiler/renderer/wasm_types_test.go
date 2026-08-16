// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// orderedMap builds an OrderedMap from ordered key/value pairs so the
// fixtures populate fields in a deliberately non-alphabetical author
// order — the order a map[string]X could not preserve.
func orderedMap[V any](t *testing.T, pairs ...any) types.OrderedMap[V] {
	t.Helper()
	m := types.NewOrderedMap[V]()
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			t.Fatalf("orderedMap: key at %d is not a string", i)
		}
		v, ok := pairs[i+1].(V)
		if !ok {
			t.Fatalf("orderedMap: value at %d is not the expected type", i+1)
		}
		m.Set(k, v)
	}
	return *m
}

// sampleRenderNodeInput builds a render_node input populated with a node
// carrying author-ordered values, a feature attachment reusing
// types.IRFeatureEntry, outbound uses edges, and a full render context with
// the three annotation buckets and a catalog projection.
func sampleRenderNodeInput(t *testing.T) renderer.RenderNodeInput {
	t.Helper()

	feature := types.IRFeatureEntry{
		Type:            "core/tls",
		ResolvedVersion: "1.2.0",
		Values:          orderedMap[any](t, "issuer", "letsencrypt", "renew_before", "720h"),
		Origin:          "explicit",
	}

	return renderer.RenderNodeInput{
		Node: renderer.IRNodeProjection{
			ID:             "payments-api",
			Type:           "core/web-service",
			ResolvedRecipe: "core/web-service/helm@1.4.2",
			// Deliberately non-alphabetical author order: replicas
			// before image before port. A map would reorder these.
			Values: orderedMap[any](t,
				"replicas", float64(3),
				"image", "payments/api:1.4.2",
				"port", float64(8080),
			),
			Features: orderedMap[types.IRFeatureEntry](t, "tls", feature),
			Uses:     orderedMap[[]string](t, "databases", []string{"payments-db"}),
		},
		Context: renderer.RenderContext{
			Parameters: orderedMap[any](t, "db_size", "100Gi", "region", "eu-west-1"),
			Annotations: renderer.RenderAnnotations{
				Aggregate: orderedMap[string](t, "team", "payments", "tier", "service"),
				Release:   orderedMap[string](t, "env", "prod"),
				Catalog:   orderedMap[string](t, "aggrega.ai/component-id", "web-service"),
			},
			Boundary: renderer.BoundaryContext{
				Name:         "prod",
				Path:         []string{"aws-prod", "eks-main", "payments"},
				SyncWave:     1,
				Directory:    "dist/prod",
				SubDirectory: "helm",
				Annotations:  orderedMap[string](t, "aggrega.ai/namespace", "payments"),
			},
		},
		SourceListing: &renderer.SourceListing{
			Root:      "/acquired/payments-chart",
			RootIsDir: true,
			Entries: []renderer.SourceEntry{
				{Path: "Chart.yaml", IsDir: false},
				{Path: "templates", IsDir: true},
				{Path: "templates/deployment.yaml", IsDir: false},
			},
		},
	}
}

// sampleRenderNodeOutput builds a render_node output in the reshaped form:
// each output file's serialized bytes (the host writes them verbatim) plus a
// diagnostic.
func sampleRenderNodeOutput(t *testing.T) renderer.RenderNodeOutput {
	t.Helper()
	return renderer.RenderNodeOutput{
		Files: []renderer.RenderedFile{
			{Path: "templates/deployment.yaml", Content: []byte("kind: Deployment\nmetadata:\n  name: payments-api\n  namespace: payments\n")},
		},
		Diagnostics: []renderer.Diagnostic{
			{
				Severity: "warning",
				Code:     "deprecated-field",
				Message:  "values.legacyPort is deprecated",
				Location: &renderer.DiagnosticLocation{
					Kind:    "values",
					URI:     "core/web-service",
					KeyPath: "values.legacyPort",
					Line:    12,
				},
				Suggest: "use values.port",
			},
		},
	}
}

// TestRenderNodeInput_RoundTrip asserts a render_node input survives a
// marshal -> unmarshal -> marshal cycle with byte-identical JSON,
// proving the JSON tags and shapes are stable.
func TestRenderNodeInput_RoundTrip(t *testing.T) {
	t.Parallel()
	fixture := sampleRenderNodeInput(t)

	first, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var round renderer.RenderNodeInput
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("marshal round: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("round-trip not byte-identical:\n  first:  %s\n  second: %s", first, second)
	}
}

// TestRenderNodeOutput_RoundTrip asserts a render_node output survives a
// marshal -> unmarshal -> marshal cycle byte-identically. The reshaped output
// carries each file's serialized bytes, so a byte-identical round-trip proves the
// bytes survive the boundary (base64 on the wire).
func TestRenderNodeOutput_RoundTrip(t *testing.T) {
	t.Parallel()
	fixture := sampleRenderNodeOutput(t)

	first, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round renderer.RenderNodeOutput
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("marshal round: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("round-trip not byte-identical:\n  first:  %s\n  second: %s", first, second)
	}
}

// TestAssembleBoundaryInput_RoundTrip asserts the assemble_boundary
// input envelope round-trips byte-identically.
func TestAssembleBoundaryInput_RoundTrip(t *testing.T) {
	t.Parallel()
	in := sampleRenderNodeInput(t)
	fixture := renderer.AssembleBoundaryInput{
		Boundary: in.Context.Boundary,
		RenderedNodes: []renderer.RenderedNode{
			{ID: "payments-api", Type: "core/web-service", Rendered: sampleRenderNodeOutput(t)},
		},
		Context: in.Context,
	}

	first, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round renderer.AssembleBoundaryInput
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("marshal round: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("round-trip not byte-identical:\n  first:  %s\n  second: %s", first, second)
	}
}

// TestRenderNodeInput_OrderedFieldByteIdentity proves the OrderedMap
// choice is load-bearing: an author-ordered, non-alphabetical field
// marshals to the same bytes on repeated marshals AND preserves author
// order on the wire. A plain Go map would flake the byte-identity here
// because its iteration order is randomized.
func TestRenderNodeInput_OrderedFieldByteIdentity(t *testing.T) {
	t.Parallel()
	fixture := sampleRenderNodeInput(t)

	// Repeated marshals of the same value are byte-identical.
	const iterations = 64
	want, err := json.Marshal(fixture.Node.Values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	for i := 0; i < iterations; i++ {
		got, err := json.Marshal(fixture.Node.Values)
		if err != nil {
			t.Fatalf("marshal values iteration %d: %v", i, err)
		}
		if string(got) != string(want) {
			t.Fatalf("values marshal not stable across runs:\n  want: %s\n  got:  %s", want, got)
		}
	}

	// Author order (replicas, image, port) is preserved on the wire —
	// not the alphabetical (image, port, replicas) a sorting map emits.
	const wantOrder = `{"replicas":3,"image":"payments/api:1.4.2","port":8080}`
	if string(want) != wantOrder {
		t.Errorf("author order not preserved on the wire:\n  want: %s\n  got:  %s", wantOrder, want)
	}

	// Annotation buckets are ordered too.
	gotAnn, err := json.Marshal(fixture.Context.Annotations.Aggregate)
	if err != nil {
		t.Fatalf("marshal annotations: %v", err)
	}
	const wantAnn = `{"team":"payments","tier":"service"}`
	if string(gotAnn) != wantAnn {
		t.Errorf("annotation author order not preserved:\n  want: %s\n  got:  %s", wantAnn, gotAnn)
	}
}

// TestDiagnostic_OmitemptyAndRoundTrip asserts the optional Location and
// Suggest fields drop from the wire when unset, that severity and code
// round-trip, and that a fully-populated diagnostic round-trips equal.
func TestDiagnostic_OmitemptyAndRoundTrip(t *testing.T) {
	t.Parallel()

	minimal := renderer.Diagnostic{
		Severity: "error",
		Code:     "missing-required",
		Message:  "values.image is required",
	}
	raw, err := json.Marshal(minimal)
	if err != nil {
		t.Fatalf("marshal minimal: %v", err)
	}
	const wantMinimal = `{"severity":"error","message":"values.image is required","code":"missing-required"}`
	if string(raw) != wantMinimal {
		t.Errorf("omitempty did not drop optional fields:\n  want: %s\n  got:  %s", wantMinimal, raw)
	}

	full := renderer.Diagnostic{
		Severity: "info",
		Code:     "style",
		Message:  "consider naming the port",
		Location: &renderer.DiagnosticLocation{Kind: "values", URI: "core/web-service", KeyPath: "values.port", Line: 9},
		Suggest:  "name: http",
	}
	rawFull, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal full: %v", err)
	}
	var round renderer.Diagnostic
	if err := json.Unmarshal(rawFull, &round); err != nil {
		t.Fatalf("unmarshal full: %v", err)
	}
	if !reflect.DeepEqual(full, round) {
		t.Errorf("diagnostic round-trip inequality:\n  before: %+v\n  after:  %+v", full, round)
	}
}

// TestAssembleBoundaryOutput_ForkedShape guards the assemble output's forked
// shape: AssembleBoundaryOutput is a distinct type from RenderNodeOutput,
// carrying the assembled files and diagnostics plus a nest-per-node signal the
// render output does not. It round-trips byte-identically, and its nest-per-node
// flag survives the boundary. A regression that re-aliases it to RenderNodeOutput
// (dropping the signal) breaks the distinctness assertion.
func TestAssembleBoundaryOutput_ForkedShape(t *testing.T) {
	t.Parallel()

	if reflect.TypeOf(renderer.AssembleBoundaryOutput{}) == reflect.TypeOf(renderer.RenderNodeOutput{}) {
		t.Fatal("AssembleBoundaryOutput must be a distinct type from RenderNodeOutput (it carries the nest-per-node signal)")
	}
	if _, ok := reflect.TypeOf(renderer.AssembleBoundaryOutput{}).FieldByName("NestPerNode"); !ok {
		t.Fatal("AssembleBoundaryOutput must carry a NestPerNode field")
	}

	render := sampleRenderNodeOutput(t)
	fixture := renderer.AssembleBoundaryOutput{
		Files:       render.Files,
		Diagnostics: render.Diagnostics,
		NestPerNode: true,
	}

	first, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round renderer.AssembleBoundaryOutput
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	second, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("marshal round: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("round-trip not byte-identical:\n  first:  %s\n  second: %s", first, second)
	}
	if !round.NestPerNode {
		t.Error("NestPerNode did not survive the round-trip")
	}
}

// TestIRNodeProjection_ReusesIRFeatureEntry guards the single-source
// reuse: IRNodeProjection.Features carries types.IRFeatureEntry, not a
// renderer-local redeclaration. A future local feature-entry type breaks
// this guard.
func TestIRNodeProjection_ReusesIRFeatureEntry(t *testing.T) {
	t.Parallel()
	// Compile-time proof of reuse: a types.IRFeatureEntry assigns
	// straight into the projection's Features map. A renderer-local
	// feature-entry redeclaration would break this assignment.
	proj := renderer.IRNodeProjection{Features: orderedMap[types.IRFeatureEntry](t)}
	proj.Features.Set("tls", types.IRFeatureEntry{Type: "core/tls"})

	featuresField, ok := reflect.TypeOf(renderer.IRNodeProjection{}).FieldByName("Features")
	if !ok {
		t.Fatal("IRNodeProjection has no Features field")
	}
	// The field type must be exactly OrderedMap over types.IRFeatureEntry.
	if featuresField.Type != reflect.TypeOf(types.OrderedMap[types.IRFeatureEntry]{}) {
		t.Errorf("Features field type drifted from OrderedMap[types.IRFeatureEntry]: %v", featuresField.Type)
	}
}

// TestRenderedFile_ContentIsBytes guards the reshape's load-bearing choice:
// RenderedFile.Content is the file's raw serialized bytes ([]byte), which the
// host writes verbatim — not structured content the host re-serializes. A future
// change back to a structured container breaks this guard.
func TestRenderedFile_ContentIsBytes(t *testing.T) {
	t.Parallel()
	contentField, ok := reflect.TypeOf(renderer.RenderedFile{}).FieldByName("Content")
	if !ok {
		t.Fatal("RenderedFile has no Content field")
	}
	if contentField.Type != reflect.TypeOf([]byte(nil)) {
		t.Errorf("Content field type drifted from []byte: %v", contentField.Type)
	}
}
