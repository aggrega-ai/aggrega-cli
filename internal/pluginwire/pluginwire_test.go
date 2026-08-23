// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginwire

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
)

// TestEncodeForExportBinaryWire proves the encode arm produces the bytes the
// guest reads, byte for byte, rather than merely producing something.
//
// The expected bytes come from the payload shape's own encoder rather than from a
// literal, because a literal would only restate whatever the encoder emits; what
// this pins is that the command's path reaches that encoder at all, which is the
// half that was missing.
func TestEncodeForExportBinaryWire(t *testing.T) {
	t.Parallel()

	want, err := topology.MutatorInput{}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the empty payload shape: %v", err)
	}
	got, err := EncodeForExport("mutate", []byte(`{}`))
	if err != nil {
		t.Fatalf("encoding for a binary-wire export should succeed; got %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded payload = %x; want %x", got, want)
	}
	if bytes.Equal(got, []byte(`{}`)) {
		t.Fatalf("the payload reached the guest unchanged, so nothing encoded it")
	}
}

// TestEncodeForExportTextWirePassesThrough covers the two ways an export gets its
// authored bytes back: a text-wire export the table knows, and an export the
// table does not know at all. The second matters as much as the first — a guest
// reads its own input and reports its own failure, so an unknown name is passed
// through rather than refused.
func TestEncodeForExportTextWirePassesThrough(t *testing.T) {
	t.Parallel()

	for _, export := range []string{"verdict_ir", "transform_annotations", "parse", "describe"} {
		t.Run(export, func(t *testing.T) {
			t.Parallel()
			text := []byte(`{"k":1}`)
			got, err := EncodeForExport(export, text)
			if err != nil {
				t.Fatalf("a text-wire export should pass its payload through; got %v", err)
			}
			if !bytes.Equal(got, text) {
				t.Fatalf("payload = %q; want it unchanged at %q", got, text)
			}
		})
	}
}

// TestEncodeForExportRefusesAnAmbiguousBinaryExport drives the arm the generated
// table cannot reach yet: one export naming two payload shapes, on a socket whose
// wire has moved to binary. The refusal is the point — encoding as one of the two
// would hand the guest bytes it silently misreads, and an export name cannot say
// which shape was meant.
func TestEncodeForExportRefusesAnAmbiguousBinaryExport(t *testing.T) {
	t.Parallel()

	table := []entry{
		{export: "verdict_ir", shape: "PolicyInput", binary: true, encode: failEncode(t)},
		{export: "verdict_ir", shape: "RenderPolicyInput", binary: true, encode: failEncode(t)},
	}
	_, err := encodeForExport(table, "verdict_ir", []byte(`{}`))
	if err == nil {
		t.Fatal("an export naming two payload shapes on a binary wire should refuse rather than guess")
	}
	for _, want := range []string{"verdict_ir", "PolicyInput", "RenderPolicyInput"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestEncodeForExportEncodesAnUnambiguousBinaryExport is the control for the
// refusal above: the same synthetic shape with one payload shape encodes.
func TestEncodeForExportEncodesAnUnambiguousBinaryExport(t *testing.T) {
	t.Parallel()

	table := []entry{
		{export: "verdict_ir", shape: "PolicyInput", binary: true, encode: func(text []byte) ([]byte, error) {
			return append([]byte("encoded:"), text...), nil
		}},
	}
	got, err := encodeForExport(table, "verdict_ir", []byte(`{}`))
	if err != nil {
		t.Fatalf("one payload shape on a binary wire should encode; got %v", err)
	}
	if string(got) != "encoded:{}" {
		t.Fatalf("payload = %q; want the table's own encoder to have run", got)
	}
}

// failEncode returns an encoder that fails the test if it is called, so a
// refusal that quietly encoded one of two shapes cannot read as a refusal.
func failEncode(t *testing.T) func([]byte) ([]byte, error) {
	t.Helper()
	return func([]byte) ([]byte, error) {
		t.Error("an ambiguous binary-wire export must refuse before any encoder runs")
		return nil, nil
	}
}

// TestEveryRegisteredEncoderRuns exercises every row's encoder, not only the
// rows whose socket has already moved.
//
// The encoders are emitted for every row on purpose, so that moving a socket to
// the binary wire is a one-field edit on its registration rather than new code.
// The cost of that is three encoders nothing calls yet, and an unexercised
// encoder is a mechanism whose first real use is also its first test — at the
// moment a wire moves, which is the worst moment to discover it. So each one is
// driven here.
//
// The assertion is the shape of the result rather than its bytes: a canonical
// encoding of a struct is a CBOR map, whose leading byte carries major type 5.
// That is a property of the encoding rather than of any one payload shape, so it
// holds for a row added later without this test being rewritten.
func TestEveryRegisteredEncoderRuns(t *testing.T) {
	t.Parallel()

	if len(entries) == 0 {
		t.Fatal("the generated registry is empty, so this test asserts nothing")
	}
	for _, e := range entries {
		t.Run(e.export+"/"+e.shape, func(t *testing.T) {
			t.Parallel()
			if e.encode == nil {
				t.Fatalf("row %s/%s carries no encoder", e.export, e.shape)
			}
			got, err := e.encode([]byte(`{}`))
			if err != nil {
				t.Fatalf("encoding the empty payload should succeed; got %v", err)
			}
			if len(got) == 0 {
				t.Fatal("the encoder produced no bytes")
			}
			if major := got[0] >> 5; major != 5 {
				t.Fatalf("encoded payload starts with major type %d (% x); a canonical struct "+
					"encoding is a map, which is major type 5", major, got)
			}
		})
	}
}
