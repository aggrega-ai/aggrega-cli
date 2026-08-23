// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestDecodeGuestOutputRefusesUndeclaredField walks the socket-output decode
// contract: an envelope carrying only fields the shape declares decodes, and one
// carrying a field it does not is refused with both the export and the offending
// field named. Naming both is the whole point — a socket's entry function is
// overridable per attachment, so the export identifies which line of
// configuration produced the wrong shape and the field identifies what the
// output actually was.
//
// The refusal also carries the condition's identity, so a caller classifies it
// with errors.Is rather than by matching the wording, and reads its code off the
// chain. The identity is attached here, at the host boundary, rather than by the
// codec: the codec's own refusal is the detail this wrap carries after the
// condition's sentence.
func TestDecodeGuestOutputRefusesUndeclaredField(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		payload string
		// wantField is the field the refusal must name; empty means the payload
		// is the declared shape and must decode.
		wantField string
		// wantResults is the verdict count a decoding payload must yield.
		wantResults int
	}{
		{
			name:        "the declared shape decodes",
			payload:     `{"results":[{"severity":"halt","message":"denied"}]}`,
			wantResults: 1,
		},
		{
			name:    "an empty verdict list is a legitimate allow and decodes",
			payload: `{"results":[]}`,
		},
		{
			name:      "a label-transform envelope is refused, naming its own field",
			payload:   `{"patches":[{"op":"add","path":"/nodes/a/labels/x","value":"y"}]}`,
			wantField: "patches",
		},
		{
			name:      "an annotation-transform envelope is refused, naming its own field",
			payload:   `{"injections":[]}`,
			wantField: "injections",
		},
		{
			name:      "a field neither shape declares is refused by name",
			payload:   `{"results":[],"verdicts":[]}`,
			wantField: "verdicts",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out abi.PolicyOutput
			err := DecodeGuestOutput("verdict_ir", []byte(tc.payload), &out)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("the declared shape must decode: %v", err)
				}
				if len(out.Results) != tc.wantResults {
					t.Errorf("decoded %d verdict(s), want %d", len(out.Results), tc.wantResults)
				}
				return
			}
			if err == nil {
				t.Fatalf("an envelope carrying %q is not this shape: expected a refusal, got nil", tc.wantField)
			}
			msg := err.Error()
			if !strings.Contains(msg, `"verdict_ir"`) {
				t.Errorf("the refusal does not name the export it was dispatched to:\n%v", msg)
			}
			if !strings.Contains(msg, tc.wantField) {
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

// TestDecodeGuestOutputCBORRefusesUndeclaredField walks the same socket-output
// decode contract on the binary transport: an envelope carrying only fields the
// shape declares decodes, and one carrying a field it does not is refused with
// both the export and the offending field named.
//
// It is the counterpart of TestDecodeGuestOutputRefusesUndeclaredField, and the
// two are separate for the same reason the two boundary functions are: while both
// encodings are in use on the socket wire, each has its own reader and each owes
// its own proof. The shapes here are the label-transform envelope and the verdict
// envelope, which are exactly the two a per-attachment entry-function override
// can confuse.
//
// Every payload is produced by the generated encoder rather than written out as
// bytes, so the input is canonical by construction and cannot drift from what a
// guest actually emits.
func TestDecodeGuestOutputCBORRefusesUndeclaredField(t *testing.T) {
	t.Parallel()

	mutatorEnvelope, err := abi.MutatorOutput{Patches: []json.RawMessage{}}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encoding a label-transform envelope: %v", err)
	}
	verdictEnvelope, err := abi.PolicyOutput{Results: []abi.Diagnostic{}}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encoding a verdict envelope: %v", err)
	}

	for _, tc := range []struct {
		name string
		// payload is the bytes that come back from the export.
		payload []byte
		// wantField is the field the refusal must name; empty means the payload
		// is the declared shape and must decode.
		wantField string
	}{
		{
			name:    "an empty patch list is a legitimate result and decodes",
			payload: mutatorEnvelope,
		},
		{
			name:      "a verdict envelope is refused, naming its own field",
			payload:   verdictEnvelope,
			wantField: "results",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out abi.MutatorOutput
			err := DecodeGuestOutputCBOR("mutate", tc.payload, &out)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("the declared shape must decode: %v", err)
				}
				if len(out.Patches) != 0 {
					t.Errorf("decoded %d patch(es), want 0", len(out.Patches))
				}
				return
			}
			if err == nil {
				t.Fatalf("an envelope carrying %q is not this shape: expected a refusal, got nil", tc.wantField)
			}
			msg := err.Error()
			if !strings.Contains(msg, `"mutate"`) {
				t.Errorf("the refusal does not name the export it was dispatched to:\n%v", msg)
			}
			if !strings.Contains(msg, tc.wantField) {
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
