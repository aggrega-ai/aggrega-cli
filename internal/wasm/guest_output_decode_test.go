// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
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
			err := DecodeGuestOutput("validate", []byte(tc.payload), &out)
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
			if !strings.Contains(msg, `"validate"`) {
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
