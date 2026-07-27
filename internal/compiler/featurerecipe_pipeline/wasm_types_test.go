// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

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

// structuredUnit builds a structured (Tree-bearing) unit tokened by name,
// carrying the given JSON literal as its RFC-6902-addressable view. It is the
// PATCH-target shape the keyed set hands a guest.
func structuredUnit(token, treeJSON string) frp.Unit {
	return frp.Unit{Token: token, MediaType: "application/yaml", Tree: json.RawMessage(treeJSON)}
}

// sampleFRPStepInput builds an FRP step input populated from the worked
// vault-fetcher / env-injector pipeline: a keyed document set (own + a
// cross-entity host root, each with deliberately non-alphabetical
// author-ordered keys), a deliberately non-alphabetical author-ordered
// step input, a full render context, a step index, and a preceding
// fetcher step that emitted a CRD.
func sampleFRPStepInput(t *testing.T) frp.FRPStepInput {
	t.Helper()

	return frp.FRPStepInput{
		Documents: frp.DocumentSet{
			// Deliberately non-alphabetical author order: workload before
			// service. A map would reorder these keys. Each entry is a
			// structured unit (a Tree-bearing PATCH target).
			Own: orderedMap[frp.Unit](t,
				"workload.yaml", structuredUnit("workload.yaml", `{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"name":"app"}]}}}}`),
				"service.yaml", structuredUnit("service.yaml", `{"kind":"Service","spec":{"ports":[{"port":80}]}}`),
			),
			// The cross-entity host root: the units of the component this
			// component-scope FeatureRecipe is attached to.
			Host: orderedMap[frp.Unit](t,
				"host-workload.yaml", structuredUnit("host-workload.yaml", `{"kind":"StatefulSet","spec":{"template":{"spec":{"containers":[{"name":"db"}]}}}}`),
			),
		},
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
// example: an own-scoped add patch and a host-scoped replace patch, each
// naming its target document by scope and key; an emit_crd side effect
// (the vault fetch), a host-scoped emit_file side effect, and a reused
// renderer.Diagnostic.
func sampleFRPStepOutput() frp.FRPStepOutput {
	return frp.FRPStepOutput{
		Patches: []frp.JSONPatchOp{
			{
				Scope: frp.DocumentScopeOwn,
				Key:   "workload.yaml",
				Op:    "add",
				Path:  "/spec/template/spec/containers/0/env",
				Value: []any{map[string]any{"name": "DB_PASSWORD", "valueFrom": "vault"}},
			},
			{
				Scope: frp.DocumentScopeHost,
				Key:   "host-workload.yaml",
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
				Kind:  "emit_file",
				Scope: frp.DocumentScopeHost,
				File:  &frp.SideEffectFile{Path: "secrets/payments-db.yaml", Content: "kind: VaultSecretFetch\n"},
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
// through the JSONPatchOp shape — add, remove, replace, move, copy, test —
// each addressed to its target document by scope and key.
func TestJSONPatchOp_AllSixOps(t *testing.T) {
	t.Parallel()
	// JSON-native value types (float64, string, bool) so the round-trip
	// is value-equal: an int operand would decode back as float64 and
	// fail DeepEqual for a reason unrelated to the ABI shape.
	ops := []frp.JSONPatchOp{
		{Scope: frp.DocumentScopeOwn, Key: "a.yaml", Op: "add", Path: "/a", Value: float64(1)},
		{Scope: frp.DocumentScopeOwn, Key: "a.yaml", Op: "remove", Path: "/b"},
		{Scope: frp.DocumentScopeOwn, Key: "a.yaml", Op: "replace", Path: "/c", Value: "x"},
		{Scope: frp.DocumentScopeHost, Key: "b.yaml", Op: "move", Path: "/d", From: "/e"},
		{Scope: frp.DocumentScopeHost, Key: "b.yaml", Op: "copy", Path: "/f", From: "/g"},
		{Scope: frp.DocumentScopeHost, Key: "b.yaml", Op: "test", Path: "/h", Value: true},
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
	// All six op strings, plus the scope/key target address, reach the
	// wire intact.
	const want = `[{"scope":"own","key":"a.yaml","op":"add","path":"/a","value":1},{"scope":"own","key":"a.yaml","op":"remove","path":"/b"},{"scope":"own","key":"a.yaml","op":"replace","path":"/c","value":"x"},{"scope":"host","key":"b.yaml","op":"move","path":"/d","from":"/e"},{"scope":"host","key":"b.yaml","op":"copy","path":"/f","from":"/g"},{"scope":"host","key":"b.yaml","op":"test","path":"/h","value":true}]`
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

// TestDocumentSet_OrderedByteIdentity proves the OrderedMap choice on the
// keyed document set is load-bearing: a set whose keys are in deliberately
// non-alphabetical author order (own: workload before service; host:
// present) marshals to the same bytes across repeated marshals AND
// preserves key order on the wire. A plain Go map would flake the
// byte-identity because its iteration order is randomized, breaking the
// host's apply-back addressing.
func TestDocumentSet_OrderedByteIdentity(t *testing.T) {
	t.Parallel()
	fixture := sampleFRPStepInput(t).Documents

	const iterations = 64
	want, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal document set: %v", err)
	}
	for i := 0; i < iterations; i++ {
		got, err := json.Marshal(fixture)
		if err != nil {
			t.Fatalf("marshal document set iteration %d: %v", i, err)
		}
		if string(got) != string(want) {
			t.Fatalf("document set marshal not stable across runs:\n  want: %s\n  got:  %s", want, got)
		}
	}

	// own keys in author order (workload before service), then the host
	// root — not an alphabetical reordering a sorting map would emit. Each
	// entry is a tagged unit: token, media type, and the tree-view.
	const wantWire = `{"own":{"workload.yaml":{"token":"workload.yaml","media_type":"application/yaml","tree":{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"name":"app"}]}}}}},"service.yaml":{"token":"service.yaml","media_type":"application/yaml","tree":{"kind":"Service","spec":{"ports":[{"port":80}]}}}},"host":{"host-workload.yaml":{"token":"host-workload.yaml","media_type":"application/yaml","tree":{"kind":"StatefulSet","spec":{"template":{"spec":{"containers":[{"name":"db"}]}}}}}}}`
	if string(want) != wantWire {
		t.Errorf("document set order not preserved on the wire:\n  want: %s\n  got:  %s", wantWire, want)
	}
}

// TestDocumentSet_HostOmitzero proves the host root drops from the wire
// when absent (a Recipe step or boundary-scope FeatureRecipe step has no
// host component), while own always emits.
func TestDocumentSet_HostOmitzero(t *testing.T) {
	t.Parallel()
	ownOnly := frp.DocumentSet{
		Own: orderedMap[frp.Unit](t, "workload.yaml", structuredUnit("workload.yaml", `{"kind":"Deployment"}`)),
	}
	raw, err := json.Marshal(ownOnly)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"own":{"workload.yaml":{"token":"workload.yaml","media_type":"application/yaml","tree":{"kind":"Deployment"}}}}`
	if string(raw) != want {
		t.Errorf("absent host root did not drop from the wire:\n  want: %s\n  got:  %s", want, raw)
	}
}

// TestJSONPatchOp_Omitempty asserts value and from drop from the wire when
// unset while the scope/key target address is always present: a remove op
// emits scope, key, op, and path only.
func TestJSONPatchOp_Omitempty(t *testing.T) {
	t.Parallel()
	remove := frp.JSONPatchOp{Scope: frp.DocumentScopeOwn, Key: "workload.yaml", Op: "remove", Path: "/spec/template/spec/serviceAccountName"}
	raw, err := json.Marshal(remove)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"scope":"own","key":"workload.yaml","op":"remove","path":"/spec/template/spec/serviceAccountName"}`
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
	// An absent scope drops from the wire — the apply machinery reads an
	// absent scope as own.
	const wantFile = `{"kind":"emit_file","file":{"path":"a.yaml","content":"x"}}`
	if string(rawFile) != wantFile {
		t.Errorf("emit_file did not drop crd/scope:\n  want: %s\n  got:  %s", wantFile, rawFile)
	}

	// A host-scoped emit carries its scope explicitly.
	hostFile := frp.SideEffect{Kind: "emit_file", Scope: frp.DocumentScopeHost, File: &frp.SideEffectFile{Path: "a.yaml", Content: "x"}}
	rawHostFile, err := json.Marshal(hostFile)
	if err != nil {
		t.Fatalf("marshal host-scoped emit_file: %v", err)
	}
	const wantHostFile = `{"kind":"emit_file","scope":"host","file":{"path":"a.yaml","content":"x"}}`
	if string(rawHostFile) != wantHostFile {
		t.Errorf("host-scoped emit did not carry scope:\n  want: %s\n  got:  %s", wantHostFile, rawHostFile)
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

// TestWalk_WithEvalIntoStepInput exercises the injected template evaluator
// end to end: a step with a non-empty `with` body renders through the
// renderer's real EvalTemplate (passed as the walk's evaluator seam) and
// the rendered key reaches the guest's step input. It lives in the external
// test package so it may import the renderer without re-forming the cycle
// the internal test package must avoid.
func TestWalk_WithEvalIntoStepInput(t *testing.T) {
	var received []byte
	invoke := func(_ context.Context, _ types.PipelineStep, payload []byte) ([]byte, error) {
		received = payload
		return json.Marshal(frp.FRPStepOutput{})
	}

	var own types.OrderedMap[frp.Unit]
	own.Set("a.yaml", structuredUnit("a.yaml", `{"x":"0"}`))
	docs := frp.DocumentSet{Own: own}
	steps := []types.PipelineStep{{ID: "s1", Plugin: "p", Function: "f", With: "key: {{ .values.x }}"}}

	withCtx := &types.TemplateContext{}
	withCtx.Values.Set("x", "rendered-value")

	_, err := frp.Walk(context.Background(), docs, steps, invoke, withCtx, renderer.RenderContext{}, renderer.EvalTemplate)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	var got frp.FRPStepInput
	if err := json.Unmarshal(received, &got); err != nil {
		t.Fatalf("unmarshal received payload: %v", err)
	}
	v, ok := got.StepInput.Get("key")
	if !ok {
		t.Fatalf("step input missing rendered `with` key; payload=%s", received)
	}
	if s, _ := v.(string); s != "rendered-value" {
		t.Errorf("step input key = %v, want rendered-value", v)
	}
}

// TestFRPTypes_JSONSchemaFieldCoverage asserts every exported Go field of
// the FRP step socket types appears as a property in the corresponding
// $defs block of the JSON Schema. Catches "Go struct grew a field, schema
// didn't" drift directly.
//
// The assertGoStructPropertiesIn helper deliberately parallels the one in
// analyzer/wasm_types_test.go: each socket package's field-coverage test
// must stand alone over its own types and schema, so the helper is copied
// rather than shared — do not unify them into one importable helper, which
// would couple two independent per-package cross-checks.
func TestFRPTypes_JSONSchemaFieldCoverage(t *testing.T) {
	t.Parallel()
	var raw map[string]any
	if err := json.Unmarshal([]byte(schemas.WasmFrpStepV1alpha1), &raw); err != nil {
		t.Fatalf("unmarshal wasm-frp-step schema: %v", err)
	}
	defs, ok := raw["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("schema $defs missing or not an object")
	}

	assertGoStructPropertiesIn := func(t *testing.T, goType reflect.Type, defName string) {
		t.Helper()
		def, ok := defs[defName].(map[string]any)
		if !ok {
			t.Fatalf("schema $defs.%s missing or not an object", defName)
			return
		}
		props, ok := def["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema $defs.%s.properties missing or not an object", defName)
			return
		}
		for i := 0; i < goType.NumField(); i++ {
			f := goType.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "" {
				name = f.Name
			}
			if _, present := props[name]; !present {
				t.Errorf("schema $defs.%s.properties missing %q (Go field %s.%s)",
					defName, name, goType.Name(), f.Name)
			}
		}
	}

	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.FRPStepInput{}), "FRPStepInput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.FRPStepOutput{}), "FRPStepOutput")
	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.DocumentSet{}), "DocumentSet")
	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.Unit{}), "Unit")
	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.JSONPatchOp{}), "JSONPatchOp")
	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.SideEffect{}), "SideEffect")
	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.SideEffectFile{}), "SideEffectFile")
	assertGoStructPropertiesIn(t, reflect.TypeOf(frp.PrecedingStep{}), "PrecedingStep")
}
