// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importersocket

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestReadImportOutput drives the socket's reading of an importer's answer with
// one fault per row, each asserted against the condition that fault pins, beside
// a positive control.
//
// The text row is the one this socket's wire depends on: the socket carries the
// canonical CBOR encoding in both directions, so an importer still answering in
// JSON is refused loudly, naming the importer and the export, rather than read
// leniently into an empty result — which would be a legitimate-looking import
// that drafted nothing. The bytes come from the generated codec's own encoders,
// so the rows differ in encoding and content only, never in shape.
func TestReadImportOutput(t *testing.T) {
	t.Parallel()

	const coordinate = "importer/demo"
	drafted := abi.ImportOutput{
		Entities: []abi.EmittedEntity{{Kind: "Component", Name: "demo", Mode: "draft", Body: "kind: Component\n"}},
		Files:    []abi.ScaffoldFile{{Path: "demo.yaml", Content: "kind: Component\n"}},
	}
	binaryAnswer := encodeImportAnswer(t, drafted)
	textAnswer, err := drafted.MarshalJSON()
	if err != nil {
		t.Fatalf("encode the text answer: %v", err)
	}
	failureAnswer := encodeImportAnswer(t, abi.ImportOutput{Error: "the source document is not a recognized format"})
	severityAnswer := encodeImportAnswer(t, abi.ImportOutput{Diagnostics: []abi.Diagnostic{{Severity: "fatal", Message: "x"}}})

	cases := []struct {
		name    string
		out     []byte
		wantIs  error
		wantSub string
	}{
		{name: "a binary answer is read", out: binaryAnswer},
		{name: "a text answer is refused", out: textAnswer, wantIs: wasm.ErrWasmOutputShape(), wantSub: fmt.Sprintf("call %q", wasm.ImportExport)},
		{name: "an answer reporting a failure halts with it", out: failureAnswer, wantIs: wasm.ErrWasmGuestError(), wantSub: "not a recognized format"},
		{name: "an undeclared severity halts", out: severityAnswer, wantIs: wasm.ErrWasmDiagnosticSeverity(), wantSub: "fatal"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := readImportOutput(coordinate, tc.out)
			if tc.wantIs == nil {
				if err != nil {
					t.Fatalf("readImportOutput: %v", err)
				}
				if len(got.Entities) != 1 || got.Entities[0].Name != "demo" || len(got.Files) != 1 || got.Files[0].Path != "demo.yaml" {
					t.Fatalf("read %+v; want the drafted entity and its file", got)
				}
				return
			}
			if !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v; want %v", err, tc.wantIs)
			}
			for _, want := range []string{coordinate, tc.wantSub} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// encodeImportAnswer encodes an importer's answer in the canonical CBOR the
// socket carries.
func encodeImportAnswer(t *testing.T, out abi.ImportOutput) []byte {
	t.Helper()
	b, err := out.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode an import answer: %v", err)
	}
	return b
}
