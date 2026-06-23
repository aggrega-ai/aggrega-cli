// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline_test

import (
	"encoding/json"
	"reflect"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
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

// sampleFRPStepInput builds an FRP step input populated from the worked
// vault-fetcher / env-injector pipeline: a cumulative AST, a
// deliberately non-alphabetical author-ordered step input, a full render
// context, a step index, and a preceding fetcher step that emitted a CRD.
func sampleFRPStepInput(t *testing.T) frp.FRPStepInput {
	t.Helper()

	return frp.FRPStepInput{
		AST: json.RawMessage(`{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"name":"app"}]}}}}`),
		// Deliberately non-alphabetical author order: secret before key
		// before mount. A map would reorder these.
		StepInput: orderedMap[any](t,
			"secret", "vault/payments/db",
			"key", "password",
			"mount", "kv",
		),
		Context: renderer.RenderContext{
			Parameters: orderedMap[any](t, "db_size", "100Gi", "region", "eu-west-1"),
			Annotations: renderer.RenderAnnotations{
				Aggregate: orderedMap[string](t, "team", "payments", "tier", "service"),
				Release:   orderedMap[string](t, "env", "prod"),
				Catalog:   orderedMap[string](t, "aggrega.ai/component-id", "web-service"),
			},
			Boundary: renderer.BoundaryContext{
				Name:      "prod",
				Path:      []string{"aws-prod", "eks-main", "payments"},
				SyncWave:  1,
				Directory: "dist/prod",
			},
		},
		StepIndex: 1,
		PrecedingSteps: []frp.PrecedingStep{
			{
				Index:     0,
				PluginURI: "core/vault-fetcher@1.0.0",
				SideEffects: []frp.SideEffect{
					{
						Kind: "emit_crd",
						CRD:  json.RawMessage(`{"apiVersion":"secrets.aggrega.ai/v1alpha1","kind":"VaultSecretFetch","metadata":{"name":"payments-db"}}`),
					},
				},
			},
		},
	}
}

// sampleFRPStepOutput builds an FRP step output populated from the worked
// example: an add patch and a replace patch against the workload AST, an
// emit_crd side effect (the vault fetch), an emit_file side effect, and a
// reused renderer.Diagnostic.
func sampleFRPStepOutput() frp.FRPStepOutput {
	return frp.FRPStepOutput{
		ASTPatches: []frp.JSONPatchOp{
			{
				Op:    "add",
				Path:  "/spec/template/spec/containers/0/env",
				Value: []any{map[string]any{"name": "DB_PASSWORD", "valueFrom": "vault"}},
			},
			{
				Op:    "replace",
				Path:  "/spec/template/spec/serviceAccountName",
				Value: "payments-sa",
			},
		},
		SideEffects: []frp.SideEffect{
			{
				Kind: "emit_crd",
				CRD:  json.RawMessage(`{"apiVersion":"secrets.aggrega.ai/v1alpha1","kind":"VaultSecretFetch","metadata":{"name":"payments-db"}}`),
			},
			{
				Kind: "emit_file",
				File: &frp.SideEffectFile{Path: "secrets/payments-db.yaml", Content: "kind: VaultSecretFetch\n"},
			},
		},
		Diagnostics: []renderer.Diagnostic{
			{
				Severity: "warning",
				Code:     "deprecated-mount",
				Message:  "the kv v1 mount is deprecated",
				Location: &renderer.DiagnosticLocation{
					Kind:    "values",
					URI:     "core/vault-injector",
					KeyPath: "values.mount",
					Line:    7,
				},
				Suggest: "use the kv v2 mount",
			},
		},
	}
}

// TestFRPStepInput_RoundTrip asserts an FRP step input survives a
// marshal -> unmarshal -> marshal cycle with byte-identical JSON, proving
// the JSON tags and shapes are stable.
func TestFRPStepInput_RoundTrip(t *testing.T) {
	t.Parallel()
	fixture := sampleFRPStepInput(t)

	first, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var round frp.FRPStepInput
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

// TestFRPStepOutput_RoundTrip asserts an FRP step output — including all
// of JSONPatchOp, an emit_crd and an emit_file SideEffect, and a reused
// renderer.Diagnostic — survives a marshal round-trip equal to the
// original Go value.
func TestFRPStepOutput_RoundTrip(t *testing.T) {
	t.Parallel()
	fixture := sampleFRPStepOutput()

	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round frp.FRPStepOutput
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(fixture, round) {
		t.Errorf("round-trip inequality:\n  before: %+v\n  after:  %+v", fixture, round)
	}
}

// TestJSONPatchOp_AllSixOps asserts every RFC 6902 operation round-trips
// through the JSONPatchOp shape: add, remove, replace, move, copy, test.
func TestJSONPatchOp_AllSixOps(t *testing.T) {
	t.Parallel()
	// JSON-native value types (float64, string, bool) so the round-trip
	// is value-equal: an int operand would decode back as float64 and
	// fail DeepEqual for a reason unrelated to the ABI shape.
	ops := []frp.JSONPatchOp{
		{Op: "add", Path: "/a", Value: float64(1)},
		{Op: "remove", Path: "/b"},
		{Op: "replace", Path: "/c", Value: "x"},
		{Op: "move", Path: "/d", From: "/e"},
		{Op: "copy", Path: "/f", From: "/g"},
		{Op: "test", Path: "/h", Value: true},
	}
	first, err := json.Marshal(ops)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round []frp.JSONPatchOp
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(ops, round) {
		t.Errorf("six-op round-trip inequality:\n  before: %+v\n  after:  %+v", ops, round)
	}
	// All six op strings reach the wire intact.
	const want = `[{"op":"add","path":"/a","value":1},{"op":"remove","path":"/b"},{"op":"replace","path":"/c","value":"x"},{"op":"move","path":"/d","from":"/e"},{"op":"copy","path":"/f","from":"/g"},{"op":"test","path":"/h","value":true}]`
	if string(first) != want {
		t.Errorf("six-op wire mismatch:\n  want: %s\n  got:  %s", want, first)
	}
}

// TestFRPStepInput_OrderedFieldByteIdentity proves the OrderedMap choice
// on StepInput is load-bearing: an author-ordered, non-alphabetical step
// input marshals to the same bytes across repeated marshals AND preserves
// author order on the wire. A plain Go map would flake the byte-identity
// because its iteration order is randomized.
func TestFRPStepInput_OrderedFieldByteIdentity(t *testing.T) {
	t.Parallel()
	fixture := sampleFRPStepInput(t)

	const iterations = 64
	want, err := json.Marshal(fixture.StepInput)
	if err != nil {
		t.Fatalf("marshal step input: %v", err)
	}
	for i := 0; i < iterations; i++ {
		got, err := json.Marshal(fixture.StepInput)
		if err != nil {
			t.Fatalf("marshal step input iteration %d: %v", i, err)
		}
		if string(got) != string(want) {
			t.Fatalf("step input marshal not stable across runs:\n  want: %s\n  got:  %s", want, got)
		}
	}

	// Author order (secret, key, mount) is preserved on the wire — not
	// the alphabetical (key, mount, secret) a sorting map emits.
	const wantOrder = `{"secret":"vault/payments/db","key":"password","mount":"kv"}`
	if string(want) != wantOrder {
		t.Errorf("author order not preserved on the wire:\n  want: %s\n  got:  %s", wantOrder, want)
	}
}

// TestJSONPatchOp_Omitempty asserts value and from drop from the wire when
// unset: a remove op emits op and path only.
func TestJSONPatchOp_Omitempty(t *testing.T) {
	t.Parallel()
	remove := frp.JSONPatchOp{Op: "remove", Path: "/spec/template/spec/serviceAccountName"}
	raw, err := json.Marshal(remove)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"op":"remove","path":"/spec/template/spec/serviceAccountName"}`
	if string(raw) != want {
		t.Errorf("omitempty did not drop value/from:\n  want: %s\n  got:  %s", want, raw)
	}
}

// TestSideEffect_Omitempty asserts crd and file drop per kind: an
// emit_crd side effect carries crd only; an emit_file side effect carries
// file only.
func TestSideEffect_Omitempty(t *testing.T) {
	t.Parallel()

	crd := frp.SideEffect{Kind: "emit_crd", CRD: json.RawMessage(`{"kind":"VaultSecretFetch"}`)}
	rawCRD, err := json.Marshal(crd)
	if err != nil {
		t.Fatalf("marshal emit_crd: %v", err)
	}
	const wantCRD = `{"kind":"emit_crd","crd":{"kind":"VaultSecretFetch"}}`
	if string(rawCRD) != wantCRD {
		t.Errorf("emit_crd did not drop file:\n  want: %s\n  got:  %s", wantCRD, rawCRD)
	}

	file := frp.SideEffect{Kind: "emit_file", File: &frp.SideEffectFile{Path: "a.yaml", Content: "x"}}
	rawFile, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal emit_file: %v", err)
	}
	const wantFile = `{"kind":"emit_file","file":{"path":"a.yaml","content":"x"}}`
	if string(rawFile) != wantFile {
		t.Errorf("emit_file did not drop crd:\n  want: %s\n  got:  %s", wantFile, rawFile)
	}
}

// TestFRPStepInput_ReusesRendererContext guards the single-source reuse:
// FRPStepInput.Context is exactly renderer.RenderContext, imported rather
// than redeclared. A package-local context redeclaration breaks both the
// compile-time assignment and the reflect identity check.
func TestFRPStepInput_ReusesRendererContext(t *testing.T) {
	t.Parallel()
	// Compile-time proof: a renderer.RenderContext assigns straight into
	// the field. A package-local redeclaration would break this.
	in := frp.FRPStepInput{Context: renderer.RenderContext{}}
	var _ renderer.RenderContext = in.Context

	field, ok := reflect.TypeOf(frp.FRPStepInput{}).FieldByName("Context")
	if !ok {
		t.Fatal("FRPStepInput has no Context field")
	}
	if field.Type != reflect.TypeOf(renderer.RenderContext{}) {
		t.Errorf("Context field type drifted from renderer.RenderContext: %v", field.Type)
	}
}

// TestFRPStepOutput_ReusesRendererDiagnostic guards the single-source
// reuse: FRPStepOutput.Diagnostics carries renderer.Diagnostic, imported
// rather than redeclared. A package-local diagnostic redeclaration breaks
// the reflect identity check on the slice element type.
func TestFRPStepOutput_ReusesRendererDiagnostic(t *testing.T) {
	t.Parallel()
	field, ok := reflect.TypeOf(frp.FRPStepOutput{}).FieldByName("Diagnostics")
	if !ok {
		t.Fatal("FRPStepOutput has no Diagnostics field")
	}
	if field.Type.Kind() != reflect.Slice {
		t.Fatalf("Diagnostics is not a slice: %v", field.Type)
	}
	if field.Type.Elem() != reflect.TypeOf(renderer.Diagnostic{}) {
		t.Errorf("Diagnostics element type drifted from renderer.Diagnostic: %v", field.Type.Elem())
	}
}
