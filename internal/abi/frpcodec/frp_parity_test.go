// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package frpcodec_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/abi/frpcodec"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The parity guards prove the generated reflect-free codec produces and
// consumes the same JSON wire bytes as encoding/json on the host hand types in
// featurerecipe_pipeline. The neutral codec vocabulary maps the opaque
// cross-package fields (render context, diagnostics) to json.RawMessage and
// the order-preserving host container (OrderedMap) to a plain map; parity is
// preserved because RawMessage carries the opaque bytes through verbatim and
// the fixtures use sorted map keys so an OrderedMap's author order matches the
// stdlib map's sorted marshal order.

// orderedUnit builds a host OrderedMap[Unit] from key/value pairs inserted in
// the given order. The parity fixtures insert keys already sorted, so the
// OrderedMap's author order equals encoding/json's sorted map-key order.
func orderedUnit(t *testing.T, pairs ...any) types.OrderedMap[frp.Unit] {
	t.Helper()
	m := types.NewOrderedMap[frp.Unit]()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1].(frp.Unit))
	}
	return *m
}

// TestParity_Unit marshals an equivalent Unit through both the generated codec
// and encoding/json on the hand type and asserts byte-equal output, for a
// structured unit (tree, no bytes) and an opaque unit (bytes, no tree).
func TestParity_Unit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hand frp.Unit
		gen  frpcodec.Unit
	}{
		{
			name: "structured",
			hand: frp.Unit{Token: "a.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)},
			gen:  frpcodec.Unit{Token: "a.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)},
		},
		{
			name: "opaque",
			hand: frp.Unit{Token: "blob.bin", MediaType: "application/octet-stream", Bytes: []byte("\x00\x01raw")},
			gen:  frpcodec.Unit{Token: "blob.bin", MediaType: "application/octet-stream", Bytes: []byte("\x00\x01raw")},
		},
		{
			name: "token-only",
			hand: frp.Unit{Token: "t"},
			gen:  frpcodec.Unit{Token: "t"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertMarshalParity(t, tc.hand, tc.gen)
		})
	}
}

// TestParity_JSONPatchOp covers the patch op including the optional any-typed
// value (omitted when nil, emitted otherwise) and the optional from pointer.
func TestParity_JSONPatchOp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hand frp.JSONPatchOp
		gen  frpcodec.JSONPatchOp
	}{
		{
			name: "with-value",
			hand: frp.JSONPatchOp{Scope: frp.DocumentScopeOwn, Key: "a.yaml", Op: "replace", Path: "/x", Value: "y"},
			gen:  frpcodec.JSONPatchOp{Scope: "own", Key: "a.yaml", Op: "replace", Path: "/x", Value: "y"},
		},
		{
			name: "no-value",
			hand: frp.JSONPatchOp{Scope: frp.DocumentScopeHost, Key: "b.yaml", Op: "remove", Path: "/y"},
			gen:  frpcodec.JSONPatchOp{Scope: "host", Key: "b.yaml", Op: "remove", Path: "/y"},
		},
		{
			name: "move-with-from",
			hand: frp.JSONPatchOp{Scope: frp.DocumentScopeOwn, Key: "c.yaml", Op: "move", Path: "/z", From: "/w"},
			gen:  frpcodec.JSONPatchOp{Scope: "own", Key: "c.yaml", Op: "move", Path: "/z", From: "/w"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertMarshalParity(t, tc.hand, tc.gen)
		})
	}
}

// TestParity_SideEffect covers both effect kinds: emit_crd (crd present, file
// nil) and emit_file (file present, crd nil), plus an absent scope.
func TestParity_SideEffect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hand frp.SideEffect
		gen  frpcodec.SideEffect
	}{
		{
			name: "emit_crd",
			hand: frp.SideEffect{Kind: "emit_crd", Scope: frp.DocumentScopeOwn, CRD: json.RawMessage(`{"apiVersion":"v1"}`)},
			gen:  frpcodec.SideEffect{Kind: "emit_crd", Scope: "own", Crd: json.RawMessage(`{"apiVersion":"v1"}`)},
		},
		{
			name: "emit_file",
			hand: frp.SideEffect{Kind: "emit_file", File: &frp.SideEffectFile{Path: "/etc/x", Content: "hi"}},
			gen:  frpcodec.SideEffect{Kind: "emit_file", File: &frpcodec.SideEffectFile{Path: "/etc/x", Content: "hi"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertMarshalParity(t, tc.hand, tc.gen)
		})
	}
}

// TestParity_DocumentSet marshals a DocumentSet with own+host maps (keys in
// sorted order so the host OrderedMap's author order matches encoding/json's
// sorted map-key order) and asserts byte-equal output.
func TestParity_DocumentSet(t *testing.T) {
	t.Parallel()
	hand := frp.DocumentSet{
		Own: orderedUnit(t,
			"a.yaml", frp.Unit{Token: "a.yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)},
			"b.yaml", frp.Unit{Token: "b.yaml", Tree: json.RawMessage(`{"kind":"Service"}`)},
		),
		Host: orderedUnit(t,
			"h.yaml", frp.Unit{Token: "h.yaml", Bytes: []byte("raw")},
		),
	}
	gen := frpcodec.DocumentSet{
		Own: map[string]frpcodec.Unit{
			"a.yaml": {Token: "a.yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)},
			"b.yaml": {Token: "b.yaml", Tree: json.RawMessage(`{"kind":"Service"}`)},
		},
		Host: map[string]frpcodec.Unit{
			"h.yaml": {Token: "h.yaml", Bytes: []byte("raw")},
		},
	}
	assertMarshalParity(t, hand, gen)
}

// TestParity_FRPStepOutput covers the output payload: patches, side effects,
// and the opaque diagnostics array (a json.RawMessage in the codec, a
// []renderctx.Diagnostic on the host — RawMessage carries the bytes through).
func TestParity_FRPStepOutput(t *testing.T) {
	t.Parallel()
	diag := renderctx.Diagnostic{Severity: "warning", Message: "heads up"}
	diagsBytes, err := json.Marshal([]renderctx.Diagnostic{diag})
	if err != nil {
		t.Fatalf("marshal diagnostics fixture: %v", err)
	}
	hand := frp.FRPStepOutput{
		Patches:     []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: "a.yaml", Op: "replace", Path: "/x", Value: float64(1)}},
		SideEffects: []frp.SideEffect{{Kind: "emit_file", File: &frp.SideEffectFile{Path: "/p", Content: "c"}}},
		Diagnostics: []renderctx.Diagnostic{diag},
	}
	gen := frpcodec.FRPStepOutput{
		Patches:     []frpcodec.JSONPatchOp{{Scope: "own", Key: "a.yaml", Op: "replace", Path: "/x", Value: float64(1)}},
		SideEffects: []frpcodec.SideEffect{{Kind: "emit_file", File: &frpcodec.SideEffectFile{Path: "/p", Content: "c"}}},
		Diagnostics: json.RawMessage(diagsBytes),
	}
	assertMarshalParity(t, hand, gen)
}

// TestParity_FRPStepInput_RoundTrip proves the generated codec round-trips the
// host's marshaled FRPStepInput byte-identically. The host payload is marshaled
// with encoding/json (including its renderctx.RenderContext); the generated
// codec unmarshals that JSON (the context lands in a json.RawMessage verbatim)
// and re-marshals it, and the bytes must match. This exercises the opaque
// context passthrough end-to-end without parallel construction.
func TestParity_FRPStepInput_RoundTrip(t *testing.T) {
	t.Parallel()
	stepInput := types.NewOrderedMap[any]()
	stepInput.Set("alpha", "one")
	stepInput.Set("beta", float64(2))

	ctx := renderctx.RenderContext{}
	ctx.Annotations.Aggregate = func() types.OrderedMap[string] {
		m := types.NewOrderedMap[string]()
		m.Set("team", "payments")
		return *m
	}()

	hand := frp.FRPStepInput{
		Documents: frp.DocumentSet{
			Own: orderedUnit(t,
				"a.yaml", frp.Unit{Token: "a.yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)},
			),
		},
		StepInput: *stepInput,
		Context:   ctx,
		StepIndex: 3,
		PrecedingSteps: []frp.PrecedingStep{
			{Index: 0, PluginURI: "uni://plugin/x", SideEffects: []frp.SideEffect{{Kind: "emit_file", File: &frp.SideEffectFile{Path: "/p", Content: "c"}}}},
		},
	}
	handJSON, err := json.Marshal(hand)
	if err != nil {
		t.Fatalf("marshal hand FRPStepInput: %v", err)
	}

	var roundtrip frpcodec.FRPStepInput
	if err := json.Unmarshal(handJSON, &roundtrip); err != nil {
		t.Fatalf("generated codec unmarshal: %v", err)
	}
	got, err := json.Marshal(roundtrip)
	if err != nil {
		t.Fatalf("generated codec re-marshal: %v", err)
	}
	if !bytes.Equal(handJSON, got) {
		t.Errorf("FRPStepInput round-trip not byte-identical:\n hand: %s\n  got: %s", handJSON, got)
	}
}

// assertMarshalParity marshals the host hand value and the generated codec
// value, asserts the wire bytes are identical, and asserts the generated codec
// round-trips its own output (marshal(unmarshal(x)) == x).
func assertMarshalParity(t *testing.T, hand any, gen json.Marshaler) {
	t.Helper()
	handJSON, err := json.Marshal(hand)
	if err != nil {
		t.Fatalf("marshal hand: %v", err)
	}
	genJSON, err := gen.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal generated: %v", err)
	}
	if !bytes.Equal(handJSON, genJSON) {
		t.Fatalf("wire bytes differ:\n hand: %s\n  gen: %s", handJSON, genJSON)
	}
	// The generated codec round-trips its own output byte-identically.
	roundTripGenerated(t, gen, genJSON)
}

// roundTripGenerated unmarshals genJSON back through the generated codec and
// re-marshals, asserting marshal(unmarshal(x)) == x. The concrete generated
// type is recovered via a type switch so the right UnmarshalJSON runs.
func roundTripGenerated(t *testing.T, gen json.Marshaler, genJSON []byte) {
	t.Helper()
	var (
		out []byte
		err error
	)
	switch gen.(type) {
	case frpcodec.Unit:
		var v frpcodec.Unit
		err = json.Unmarshal(genJSON, &v)
		if err == nil {
			out, err = json.Marshal(v)
		}
	case frpcodec.JSONPatchOp:
		var v frpcodec.JSONPatchOp
		err = json.Unmarshal(genJSON, &v)
		if err == nil {
			out, err = json.Marshal(v)
		}
	case frpcodec.SideEffect:
		var v frpcodec.SideEffect
		err = json.Unmarshal(genJSON, &v)
		if err == nil {
			out, err = json.Marshal(v)
		}
	case frpcodec.DocumentSet:
		var v frpcodec.DocumentSet
		err = json.Unmarshal(genJSON, &v)
		if err == nil {
			out, err = json.Marshal(v)
		}
	case frpcodec.FRPStepOutput:
		var v frpcodec.FRPStepOutput
		err = json.Unmarshal(genJSON, &v)
		if err == nil {
			out, err = json.Marshal(v)
		}
	default:
		t.Fatalf("roundTripGenerated: unhandled type %T", gen)
	}
	if err != nil {
		t.Fatalf("generated round-trip: %v", err)
	}
	if !bytes.Equal(genJSON, out) {
		t.Errorf("generated round-trip not byte-identical:\n first: %s\n  second: %s", genJSON, out)
	}
}
