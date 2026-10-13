// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cbororacle_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The pipeline-step socket's compiler-side payload is produced by a generated
// codec rather than by the reflective struct marshal it replaced, and the tests
// below pin the one property that transition owes: the bytes on the wire did not
// move.
//
// The reference the compiler side is held to is the shared vector corpus, whose
// documents were captured from the reflective marshal of the compiler-side types
// and are frozen. They are therefore an oracle the generated codec had no hand in
// producing, which is what makes the comparison meaningful — a codec re-decoded
// by its own counterpart proves only that the two agree, including where they
// agree on a mistake.

// sharedVector reads one document from the shared vector corpus. The corpus
// lives above this module, reached by relative path for the same reason its own
// harness reaches it that way.
func sharedVector(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "aggrega-abi", "testdata", name))
	if err != nil {
		t.Fatalf("read vector %s: %v", name, err)
	}
	return bytes.TrimSpace(b)
}

// TestHostWireFRP_MatchesFrozenVectors asserts the compiler-side codec reproduces
// the frozen wire documents exactly: decoding a document and re-encoding it must
// return the same bytes, key for key and in the same order.
//
// The input vector is the load-bearing one. It carries a side effect with no
// scope, which is the shape that distinguishes a conditional emission from an
// unconditional one — a codec emitting the scope key unconditionally would put an
// empty value on the wire here and fail, where a corpus built only from
// fully-populated fixtures would have accepted it.
func TestHostWireFRP_MatchesFrozenVectors(t *testing.T) {
	t.Parallel()

	t.Run("FRPStepInput", func(t *testing.T) {
		t.Parallel()
		want := sharedVector(t, "FRPStepInput.golden.json")
		var in frp.FRPStepInput
		if err := json.Unmarshal(want, &in); err != nil {
			t.Fatalf("compiler-side decode of the frozen document: %v", err)
		}
		got, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("compiler-side re-encode: %v", err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("the compiler-side wire moved:\n frozen: %s\n    got: %s", want, got)
		}
		// The absent scope stays absent. Asserted on the bytes rather than
		// inferred from the compare above, so a failure says which property broke.
		if bytes.Contains(got, []byte(`"scope"`)) {
			t.Errorf("an absent scope reached the wire: %s", got)
		}
	})

	t.Run("FRPStepOutput", func(t *testing.T) {
		t.Parallel()
		want := sharedVector(t, "FRPStepOutput.golden.json")
		var out frp.FRPStepOutput
		if err := json.Unmarshal(want, &out); err != nil {
			t.Fatalf("compiler-side decode of the frozen document: %v", err)
		}
		got, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("compiler-side re-encode: %v", err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("the compiler-side wire moved:\n frozen: %s\n    got: %s", want, got)
		}
	})
}

// TestHostWireFRP_ComparisonIsDiscriminating calibrates the comparison above. A
// byte-compare between two documents nobody perturbed passes whatever the codec
// does, so the compare is shown to reject a document that differs by one key
// before its acceptance of the real one is read as evidence.
//
// The perturbation is applied to the frozen bytes rather than to the codec, so
// the calibration exercises the same compare the assertions above use without
// depending on the codec being wrong.
func TestHostWireFRP_ComparisonIsDiscriminating(t *testing.T) {
	t.Parallel()

	frozen := sharedVector(t, "FRPStepOutput.golden.json")
	var out frp.FRPStepOutput
	if err := json.Unmarshal(frozen, &out); err != nil {
		t.Fatalf("compiler-side decode: %v", err)
	}
	got, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("compiler-side re-encode: %v", err)
	}

	// One key renamed: a document that differs from the frozen one in exactly the
	// way a drifted codec would produce.
	perturbed := bytes.Replace(frozen, []byte(`"patches"`), []byte(`"patched"`), 1)
	if bytes.Equal(perturbed, frozen) {
		t.Fatal("the perturbation changed nothing, so it calibrates nothing")
	}
	if bytes.Equal(perturbed, got) {
		t.Error("the compare accepts a document with a renamed key, so it cannot detect a drifted wire")
	}
}

// TestHostGuestWireParity_FRPStep pins that the two sides of the pipeline-step
// wire speak the same bytes. The plugin side does not hold the compiler side's
// types — an order-preserving container here is the compiler's own and there the
// plugin binding's, and the scope vocabulary is a named string type here and a
// bare string there — so agreement is a property to assert rather than one the
// shared schema delivers by itself.
func TestHostGuestWireParity_FRPStep(t *testing.T) {
	t.Parallel()

	units := om[frp.Unit](t,
		// Insertion order is z, a — neither of the two canonical positions.
		"z.yaml", frp.Unit{Token: "z.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Service"}`)},
		"a.yaml", frp.Unit{Token: "a.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)},
	)
	in := frp.FRPStepInput{
		Documents: frp.DocumentSet{Own: units},
		StepInput: om[any](t, "zeta", "1", "alpha", "2"),
		Context: renderctx.RenderContext{
			Annotations: renderctx.RenderAnnotations{Aggregate: om[string](t, "team", "payments")},
		},
		StepIndex: 3,
		PrecedingSteps: []frp.PrecedingStep{{
			Index:       0,
			PluginURI:   "uni://plugin/x",
			SideEffects: []frp.SideEffect{{Kind: "emit_file", File: &frp.SideEffectFile{Path: "/p", Content: "c"}}},
		}},
	}
	inBytes, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	crossSide(t, "FRPStepInput", inBytes, &abi.FRPStepInput{})

	// The compiler side rebuilds its order-preserving container from the wire in
	// the order the array form carries — the insertion order, which the fixture
	// chose to differ from the canonical sorted one — and re-encodes identically.
	var rt frp.FRPStepInput
	if err := rt.DecodeCBOR(inBytes); err != nil {
		t.Fatalf("compiler-side decode: %v", err)
	}
	again, err := rt.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side re-encode: %v", err)
	}
	if !bytes.Equal(inBytes, again) {
		t.Errorf("compiler-side round-trip is not byte-stable:\nfirst:  %s\nsecond: %s",
			hex.EncodeToString(inBytes), hex.EncodeToString(again))
	}
	if keys := rt.Documents.Own.Keys(); !sameKeys(keys, []string{"z.yaml", "a.yaml"}) {
		t.Errorf("decoded document key order = %v, want the inserted order [z.yaml a.yaml]", keys)
	}
	if keys := rt.StepInput.Keys(); !sameKeys(keys, []string{"zeta", "alpha"}) {
		t.Errorf("decoded step input key order = %v, want the inserted order [zeta alpha]", keys)
	}

	// The output payload carries no diagnostic, because the diagnostic carry is a
	// known asymmetry pinned separately below. Everything else on the payload is
	// asserted byte-identical here, so this is also the positive control for that
	// test: the same payload without a diagnostic crosses cleanly, which is what
	// attributes the refusal there to the diagnostic and to nothing else.
	out := frp.FRPStepOutput{
		Patches: []frp.JSONPatchOp{{
			Scope: frp.DocumentScopeOwn, Key: "a.yaml", Op: "add",
			Path: "/metadata/annotations/x", Value: "true",
		}},
		SideEffects: []frp.SideEffect{{Kind: "emit_file", File: &frp.SideEffectFile{Path: "/p", Content: "c"}}},
		Outputs:     om[any](t, "zeta", "1", "alpha", "2"),
	}
	outBytes, err := out.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side output encode: %v", err)
	}
	crossSide(t, "FRPStepOutput", outBytes, &abi.FRPStepOutput{})
}

// TestHostGuestWireParity_FRPDiagnosticCarry pins a known asymmetry rather than
// asserting the byte-identity the rest of this socket holds, and the asymmetry is
// not a property of this socket: it belongs to every socket carrying the shared
// diagnostic, because it comes from how the two sides realize that one shape.
//
// The plugin side realizes a diagnostic as a generated type it can encode
// natively, so it writes a structured value. The compiler side realizes it at a
// cross-package spelling its own codec cannot dispatch to, so it writes the
// diagnostic as opaque bytes instead. Neither side can read the other's binary
// form, in either direction.
//
// The transport that runs in a build is unaffected, and that is why this is a
// pinned shape rather than a failing build: the pipeline walk carries this payload
// as JSON, where the two sides agree byte for byte — asserted below so the claim
// is measured rather than assumed. The binary transport is not a path this socket
// uses today.
//
// The assertions fix the current shape in place so that closing the asymmetry, or
// widening it, is a deliberate change with a failing test to acknowledge rather
// than a silent drift.
func TestHostGuestWireParity_FRPDiagnosticCarry(t *testing.T) {
	t.Parallel()

	hostOut := frp.FRPStepOutput{Diagnostics: []renderctx.Diagnostic{{Severity: abi.SeverityWarn, Message: "heads up"}}}
	guestOut := abi.FRPStepOutput{Diagnostics: []abi.Diagnostic{{Severity: abi.SeverityWarn, Message: "heads up"}}}

	hostCBOR, err := hostOut.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	guestCBOR, err := guestOut.EncodeCBOR()
	if err != nil {
		t.Fatalf("plugin-side encode: %v", err)
	}
	if bytes.Equal(hostCBOR, guestCBOR) {
		t.Errorf("the two sides now agree on the binary diagnostic carry; the asymmetry this test pins has closed: %s",
			hex.EncodeToString(hostCBOR))
	}

	var intoGuest abi.FRPStepOutput
	if err := intoGuest.DecodeCBOR(hostCBOR); err == nil {
		t.Errorf("the plugin side now reads the compiler side's binary diagnostic carry; the asymmetry this test pins has changed")
	}
	var intoHost frp.FRPStepOutput
	if err := intoHost.DecodeCBOR(guestCBOR); err == nil {
		t.Errorf("the compiler side now reads the plugin side's binary diagnostic carry; the asymmetry this test pins has changed")
	}

	// The transport the pipeline walk actually uses. Both sides emit the same
	// bytes and each reads the other's, so the asymmetry above is confined to the
	// binary encoding.
	hostJSON, err := json.Marshal(hostOut)
	if err != nil {
		t.Fatalf("compiler-side JSON marshal: %v", err)
	}
	guestJSON, err := json.Marshal(guestOut)
	if err != nil {
		t.Fatalf("plugin-side JSON marshal: %v", err)
	}
	if !bytes.Equal(hostJSON, guestJSON) {
		t.Errorf("the two sides disagree on the JSON diagnostic carry, which the walk depends on:\ncompiler side: %s\nplugin side:   %s",
			hostJSON, guestJSON)
	}
	var jsonIntoHost frp.FRPStepOutput
	if err := json.Unmarshal(guestJSON, &jsonIntoHost); err != nil {
		t.Fatalf("compiler-side JSON decode of the plugin side's payload, the direction a build runs: %v", err)
	}
	if len(jsonIntoHost.Diagnostics) != 1 || jsonIntoHost.Diagnostics[0].Message != "heads up" {
		t.Errorf("the diagnostic did not survive the direction a build runs: %+v", jsonIntoHost.Diagnostics)
	}
}

// TestHostGuestWireParity_FRPConditionalMapHeadMatchesBody covers every
// pipeline-step type whose declared pair count is conditional, in both its empty
// and its populated form, through the independent decoder that reads the head.
//
// The count is asserted as the difference between the two forms rather than
// against a fixed number, because a pinned count leaves one of the two heads
// self-consistent and wrong, and an unrelated required field added to the schema
// later would break a frozen count for a reason that is not a defect.
func TestHostGuestWireParity_FRPConditionalMapHeadMatchesBody(t *testing.T) {
	t.Parallel()

	anyMap := om[any](t, "k", "v")
	unitMap := om[frp.Unit](t, "k", frp.Unit{Token: "k"})

	type encoder interface{ EncodeCBOR() ([]byte, error) }

	cases := []struct {
		name        string
		empty       encoder
		populated   encoder
		conditional []string
	}{
		{
			name:  "FRPStepInput",
			empty: frp.FRPStepInput{Documents: frp.DocumentSet{Own: unitMap}},
			populated: frp.FRPStepInput{
				Documents:      frp.DocumentSet{Own: unitMap},
				StepInput:      anyMap,
				PrecedingSteps: []frp.PrecedingStep{{Index: 0, PluginURI: "uni://plugin/x"}},
			},
			conditional: []string{"step_input", "preceding_steps"},
		},
		{
			// The empty form declares zero pairs, which is the edge the head
			// encoding is most likely to get wrong: every field is optional.
			name:  "FRPStepOutput",
			empty: frp.FRPStepOutput{},
			populated: frp.FRPStepOutput{
				Patches:     []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: "k", Op: "add", Path: "/a"}},
				SideEffects: []frp.SideEffect{{Kind: "emit_file"}},
				Diagnostics: []renderctx.Diagnostic{{Severity: abi.SeverityWarn, Message: "m"}},
				Outputs:     anyMap,
				Error:       "boom",
			},
			conditional: []string{"patches", "side_effects", "diagnostics", "outputs", "error"},
		},
		{
			name:        "DocumentSet",
			empty:       frp.DocumentSet{Own: unitMap},
			populated:   frp.DocumentSet{Own: unitMap, Host: unitMap},
			conditional: []string{"host"},
		},
		{
			name:        "Unit",
			empty:       frp.Unit{Token: "k"},
			populated:   frp.Unit{Token: "k", Bytes: []byte{0, 1}, MediaType: "application/yaml", Tree: json.RawMessage(`{}`)},
			conditional: []string{"bytes", "media_type", "tree"},
		},
		{
			name:        "JSONPatchOp",
			empty:       frp.JSONPatchOp{Scope: frp.DocumentScopeOwn, Key: "k", Op: "add", Path: "/a"},
			populated:   frp.JSONPatchOp{Scope: frp.DocumentScopeOwn, Key: "k", Op: "move", Path: "/a", Value: "v", From: "/b"},
			conditional: []string{"value", "from"},
		},
		{
			// The scope here is the field whose kind is invisible in its Go type
			// name: a named string type, which the omit logic classifies from the
			// schema's declared type rather than from the spelling.
			name:        "SideEffect",
			empty:       frp.SideEffect{Kind: "emit_file"},
			populated:   frp.SideEffect{Kind: "emit_file", Scope: frp.DocumentScopeHost, Crd: json.RawMessage(`{}`), File: &frp.SideEffectFile{Path: "/p", Content: "c"}},
			conditional: []string{"scope", "crd", "file"},
		},
		{
			name:        "PrecedingStep",
			empty:       frp.PrecedingStep{Index: 0, PluginURI: "uni://plugin/x"},
			populated:   frp.PrecedingStep{Index: 0, PluginURI: "uni://plugin/x", SideEffects: []frp.SideEffect{{Kind: "emit_file"}}},
			conditional: []string{"side_effects"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			eb, err := c.empty.EncodeCBOR()
			if err != nil {
				t.Fatalf("encoding the empty form: %v", err)
			}
			pb, err := c.populated.EncodeCBOR()
			if err != nil {
				t.Fatalf("encoding the populated form: %v", err)
			}
			emptyKeys := cborKeys(t, c.name+"(empty)", eb)
			populatedKeys := cborKeys(t, c.name+"(populated)", pb)

			for _, k := range c.conditional {
				if emptyKeys[k] {
					t.Errorf("key %q is emitted for an empty optional field; keys = %v", k, emptyKeys)
				}
				if !populatedKeys[k] {
					t.Errorf("key %q is dropped for a populated optional field; keys = %v", k, populatedKeys)
				}
			}

			if want := len(emptyKeys) + len(c.conditional); len(populatedKeys) != want {
				t.Errorf("populated form declares %d pairs, want %d (%d unconditional + %d conditional)",
					len(populatedKeys), want, len(emptyKeys), len(c.conditional))
			}
		})
	}
}

// TestHostWireFRP_OmitsAndEmitsOnTheJSONWire asserts the same conditionality on
// the JSON wire, in both directions. One omission decision drives both encodings,
// so a key's presence is asserted on both rather than on the binary one alone —
// and the JSON wire is the one the pipeline walk actually puts on the boundary.
func TestHostWireFRP_OmitsAndEmitsOnTheJSONWire(t *testing.T) {
	t.Parallel()

	conditional := []string{"scope", "crd", "file"}

	emptyJSON, err := json.Marshal(frp.SideEffect{Kind: "emit_file"})
	if err != nil {
		t.Fatalf("compiler-side JSON marshal of the empty form: %v", err)
	}
	for _, k := range conditional {
		if jsonKeys(t, "SideEffect(empty)", emptyJSON)[k] {
			t.Errorf("JSON key %q is emitted for an empty optional field: %s", k, emptyJSON)
		}
	}

	populated := frp.SideEffect{
		Kind:  "emit_file",
		Scope: frp.DocumentScopeHost,
		Crd:   json.RawMessage(`{}`),
		File:  &frp.SideEffectFile{Path: "/p", Content: "c"},
	}
	populatedJSON, err := json.Marshal(populated)
	if err != nil {
		t.Fatalf("compiler-side JSON marshal of the populated form: %v", err)
	}
	for _, k := range conditional {
		if !jsonKeys(t, "SideEffect(populated)", populatedJSON)[k] {
			t.Errorf("JSON key %q is dropped for a populated optional field: %s", k, populatedJSON)
		}
	}

	// The order-preserving containers on the step payloads answer emptiness
	// through the container rather than through a slice length, so they are
	// asserted in both directions too.
	emptyIn, err := json.Marshal(frp.FRPStepInput{Documents: frp.DocumentSet{Own: om[frp.Unit](t, "k", frp.Unit{Token: "k"})}})
	if err != nil {
		t.Fatalf("compiler-side JSON marshal of the empty step input: %v", err)
	}
	if jsonKeys(t, "FRPStepInput(empty)", emptyIn)["step_input"] {
		t.Errorf("JSON key \"step_input\" is emitted for an empty ordered container: %s", emptyIn)
	}
	populatedIn, err := json.Marshal(frp.FRPStepInput{
		Documents: frp.DocumentSet{Own: om[frp.Unit](t, "k", frp.Unit{Token: "k"})},
		StepInput: om[any](t, "k", "v"),
	})
	if err != nil {
		t.Fatalf("compiler-side JSON marshal of the populated step input: %v", err)
	}
	if !jsonKeys(t, "FRPStepInput(populated)", populatedIn)["step_input"] {
		t.Errorf("JSON key \"step_input\" is dropped for a populated ordered container: %s", populatedIn)
	}

	var emptyTypes types.OrderedMap[any]
	if !emptyTypes.IsZero() {
		t.Fatal("the emptiness the assertions above rest on is not what the container reports")
	}
}
