// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestUnmarshalJSONStaysLenient pins the additive half of the contract: the
// standard decode entry point still SKIPS a field the shape does not declare.
//
// It is asserted rather than assumed because the tempting simplification — one
// strict decoder instead of two entry points — would change every round-trip,
// golden vector and cross-language parity fixture that reads the standard entry
// point, and would turn a forward-compatible read into a halt everywhere rather
// than at the one boundary where skipping is indistinguishable from a result.
func TestUnmarshalJSONStaysLenient(t *testing.T) {
	t.Parallel()

	const payload = `{"results":[],"a-field-no-shape-declares":1}`
	var lenient abi.PolicyOutput
	if err := lenient.UnmarshalJSON([]byte(payload)); err != nil {
		t.Fatalf("the standard decode entry point must skip an undeclared field, not refuse it: %v", err)
	}
	var strict abi.PolicyOutput
	if err := strict.UnmarshalJSONStrict([]byte(payload)); err == nil {
		t.Fatalf("the strict decode entry point must refuse an undeclared field, got nil")
	}
}

// TestDecodeGuestOutputCBORRefusesUndeclaredField walks the socket-output decode
// contract over the binary encoding every socket output carries: an envelope
// carrying only fields the shape declares decodes, and one carrying a field it
// does not is refused with both the export and the offending field named. Naming
// both is the whole point — a socket's entry function is overridable per
// attachment, so the export identifies which line of configuration produced the
// wrong shape and the field identifies what the output actually was.
//
// The envelopes refused are the ones a per-attachment entry-function override can
// confuse — a verdict read where a label-transform answer is expected and the
// reverse, an annotation-transform answer read where a verdict is expected — plus
// a field no shape declares, and an answer a guest wrote with a JSON encoder. The
// JSON answer is not a map in the binary encoding at all, so there is no field to
// name, and the refusal names the export and carries the condition.
//
// The refusal also carries the condition's identity, so a caller classifies it
// with errors.Is rather than by matching the wording, and reads its code off the
// chain. The identity is attached here, at the host boundary, rather than by the
// codec: the codec's own refusal is the detail this wrap carries after the
// condition's sentence.
//
// Every payload a codec can write is produced by the generated encoder rather than
// written out as bytes, so the input is canonical by construction and cannot drift
// from what a guest actually emits. The one exception is the envelope carrying a
// field no shape declares, which no generated encoder can write, so its bytes are
// spelled out.
func TestDecodeGuestOutputCBORRefusesUndeclaredField(t *testing.T) {
	t.Parallel()

	encode := func(v interface{ EncodeCBOR() ([]byte, error) }) []byte {
		t.Helper()
		b, err := v.EncodeCBOR()
		if err != nil {
			t.Fatalf("encoding %T: %v", v, err)
		}
		return b
	}
	oneVerdict := encode(abi.PolicyOutput{Results: []abi.Diagnostic{{Severity: "halt", Message: "denied"}}})
	noVerdict := encode(abi.PolicyOutput{Results: []abi.Diagnostic{}})
	patches := encode(abi.MutatorOutput{Patches: []json.RawMessage{json.RawMessage(`{"op":"add","path":"/nodes/a/labels/x","value":"y"}`)}})
	noPatch := encode(abi.MutatorOutput{Patches: []json.RawMessage{}})
	injections := encode(abi.AnnotationTransformOutput{Injections: []abi.AnnotationInjection{}})
	// A two-entry map, in canonical key order: "results" holding an empty array,
	// then "verdicts" holding an empty array.
	undeclared := append(append(append([]byte{0xa2, 0x67}, "results"...), 0x80, 0x68), append([]byte("verdicts"), 0x80)...)

	// verdicts and patchList read the decoded envelope's entry count for a
	// payload that must decode.
	verdicts := func() (GuestOutputCBOR, func() int) {
		var out abi.PolicyOutput
		return &out, func() int { return len(out.Results) }
	}
	patchList := func() (GuestOutputCBOR, func() int) {
		var out abi.MutatorOutput
		return &out, func() int { return len(out.Patches) }
	}

	for _, tc := range []struct {
		name   string
		export string
		// into is the socket's output shape, and how many entries it decoded.
		into func() (GuestOutputCBOR, func() int)
		// payload is the bytes that come back from the export.
		payload []byte
		// refused says the payload is not the socket's shape. wantField is the
		// field the refusal must name, where the payload has fields to name.
		refused   bool
		wantField string
		// wantEntries is the entry count a decoding payload must yield.
		wantEntries int
	}{
		{name: "the declared verdict shape decodes", export: "verdict_ir", into: verdicts, payload: oneVerdict, wantEntries: 1},
		{name: "an empty verdict list is a legitimate allow and decodes", export: "verdict_ir", into: verdicts, payload: noVerdict},
		{name: "a label-transform envelope read as a verdict is refused, naming its own field", export: "verdict_ir", into: verdicts, payload: patches, refused: true, wantField: "patches"},
		{name: "an annotation-transform envelope read as a verdict is refused, naming its own field", export: "verdict_ir", into: verdicts, payload: injections, refused: true, wantField: "injections"},
		{name: "a field no shape declares is refused by name", export: "verdict_ir", into: verdicts, payload: undeclared, refused: true, wantField: "verdicts"},
		{name: "a verdict answer written as JSON is refused", export: "verdict_ir", into: verdicts, payload: []byte(`{"results":[]}`), refused: true},
		{name: "an empty patch list is a legitimate result and decodes", export: "mutate", into: patchList, payload: noPatch},
		{name: "a verdict envelope read as a label-transform answer is refused, naming its own field", export: "mutate", into: patchList, payload: noVerdict, refused: true, wantField: "results"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, entries := tc.into()
			err := DecodeGuestOutputCBOR(tc.export, tc.payload, out)
			if !tc.refused {
				if err != nil {
					t.Fatalf("the declared shape must decode: %v", err)
				}
				if got := entries(); got != tc.wantEntries {
					t.Errorf("decoded %d entr(ies), want %d", got, tc.wantEntries)
				}
				return
			}
			if err == nil {
				t.Fatalf("the payload is not this socket's shape: expected a refusal, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, strconv.Quote(tc.export)) {
				t.Errorf("the refusal does not name the export it was dispatched to:\n%v", msg)
			}
			if tc.wantField != "" && !strings.Contains(msg, strconv.Quote(tc.wantField)) {
				t.Errorf("the refusal does not name the field that does not belong (%q):\n%v", tc.wantField, msg)
			}
			if !stderrors.Is(err, ErrWasmOutputShape()) {
				t.Errorf("the refusal must be classifiable with errors.Is against its own condition:\n%v", msg)
			}
			want, ok := cerrors.CodeOf(ErrWasmOutputShape())
			if !ok {
				t.Fatalf("the condition reports no code, so what the refusal should report cannot be built")
			}
			if got, found := cerrors.CodeOf(err); !found || got != want {
				t.Errorf("the refusal reports code %q (found=%v); want %q, the code the condition is registered under", got, found, want)
			}
			sentence := ErrWasmOutputShape().Error()
			if n := strings.Count(msg, sentence); n != 1 {
				t.Errorf("the condition's sentence appears %d times, want exactly 1\n%v", n, msg)
			}
		})
	}
}

// TestDecodeCBORStaysLenient pins the additive half of the binary transport's
// contract, exactly as TestUnmarshalJSONStaysLenient does for the textual one:
// the standard decode entry point still SKIPS a field the shape does not declare,
// so only the socket boundary halts on one.
//
// It also states the hazard the strict reader exists for as an assertion rather
// than as prose: the lenient reader accepts a verdict envelope as a
// label-transform output and yields no patches, which is indistinguishable from a
// plugin that legitimately asked for no change.
func TestDecodeCBORStaysLenient(t *testing.T) {
	t.Parallel()

	verdictEnvelope, err := abi.PolicyOutput{Results: []abi.Diagnostic{}}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encoding a verdict envelope: %v", err)
	}

	var lenient abi.MutatorOutput
	if err := lenient.DecodeCBOR(verdictEnvelope); err != nil {
		t.Fatalf("the standard decode entry point must skip an undeclared field, not refuse it: %v", err)
	}
	if lenient.Patches != nil {
		t.Errorf("the lenient read yielded %d patch(es); the point of the strict reader is that this is empty and looks legitimate", len(lenient.Patches))
	}
	var strict abi.MutatorOutput
	if err := strict.DecodeCBORStrict(verdictEnvelope); err == nil {
		t.Fatalf("the strict decode entry point must refuse an undeclared field, got nil")
	}
}
