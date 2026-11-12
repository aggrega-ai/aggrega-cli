// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cbororacle

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	cbor "github.com/fxamacker/cbor/v2"
)

// refEncoder is the reference deterministic-CBOR encoder: fxamacker's Core
// Deterministic preset.
func refEncoder(t *testing.T) cbor.EncMode {
	t.Helper()
	em, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		t.Fatalf("build reference encoder: %v", err)
	}
	return em
}

// cborEncoder is implemented by every generated guest type.
type cborEncoder interface {
	EncodeCBOR() ([]byte, error)
}

// assertOracle asserts the generated codec's bytes equal the reference
// encoder's bytes for v, and logs the canonical hex.
func assertOracle(t *testing.T, name string, v cborEncoder) []byte {
	t.Helper()
	em := refEncoder(t)
	ours, err := v.EncodeCBOR()
	if err != nil {
		t.Fatalf("%s: EncodeCBOR: %v", name, err)
	}
	ref, err := em.Marshal(v)
	if err != nil {
		t.Fatalf("%s: reference marshal: %v", name, err)
	}
	if !bytes.Equal(ours, ref) {
		t.Fatalf("%s: generated CBOR != reference\n generated: %s\n reference: %s",
			name, hex.EncodeToString(ours), hex.EncodeToString(ref))
	}
	t.Logf("%s -> %s", name, hex.EncodeToString(ours))
	return ours
}

// negZero is a true IEEE-754 negative zero (the Go source literal -0.0 is
// positive zero; negative zero needs Copysign).
var negZero = math.Copysign(0, -1)

// TestOracle_FloatBoundaries proves the CDE §4.2 float rule against the
// reference across the float16/32/64 width boundaries, ±0 sign preservation,
// integer-valued floats (no numeric reduction), NaN, and ±Inf, on the live
// float-carrying socket type (CandidateSignals). The score-of-1.0 and
// zero-signal cases are the real import-socket data the arc admitted floats for.
func TestOracle_FloatBoundaries(t *testing.T) {
	cases := map[string]float64{
		"neg_zero":       negZero,
		"pos_zero":       0.0,
		"one":            1.0,     // integer-valued: stays a float16, no reduction
		"hundred":        100.0,   // integer-valued
		"f16_max":        65504.0, // largest float16
		"f16_exact_frac": 0.15625, // 5/32, exact in float16
		"needs_f64":      0.1,     // not float32-exact -> float64
		"score_typical":  0.87,    // real score value -> float64
		"beyond_f32":     1e39,    // beyond float32 range -> float64
		"pos_inf":        math.Inf(1),
		"neg_inf":        math.Inf(-1),
		"nan":            math.NaN(),
	}
	for name, f := range cases {
		v := abi.CandidateSignals{Table: f}
		got := assertOracle(t, name, v)
		// Round-trip: decode our bytes and re-encode; assert stable (bytes, not
		// value — NaN != NaN, but its canonical bytes are stable).
		var v2 abi.CandidateSignals
		if err := v2.DecodeCBOR(got); err != nil {
			t.Fatalf("%s: DecodeCBOR: %v", name, err)
		}
		rt, err := v2.EncodeCBOR()
		if err != nil {
			t.Fatalf("%s: re-encode: %v", name, err)
		}
		if !bytes.Equal(got, rt) {
			t.Errorf("%s: round-trip not byte-stable\n in: %s\nout: %s",
				name, hex.EncodeToString(got), hex.EncodeToString(rt))
		}
	}
}

// TestOracle_NegativeZeroPreserved pins the load-bearing property that the
// canonical float encoding preserves sign: negative zero survives the round-trip
// as negative zero (its sign bit is kept), distinct from positive zero on the
// wire, with no numeric reduction to an integer.
func TestOracle_NegativeZeroPreserved(t *testing.T) {
	got := assertOracle(t, "neg_zero_signal", abi.CandidateSignals{Table: negZero})
	// The negative-zero half-float is f9 8000; positive zero is f9 0000.
	if !bytes.Contains(got, []byte{0xf9, 0x80, 0x00}) {
		t.Fatalf("negative zero not encoded as f9 8000: %s", hex.EncodeToString(got))
	}
	var v abi.CandidateSignals
	if err := v.DecodeCBOR(got); err != nil {
		t.Fatal(err)
	}
	if math.Signbit(v.Table) != true || v.Table != 0 {
		t.Fatalf("decoded value is not negative zero: signbit=%v value=%v", math.Signbit(v.Table), v.Table)
	}
}

// TestOracle_NestedAndScalars proves the canonical struct-map encoding — fields
// in canonical key order, a nested struct as a nested map, scalars, and an
// integer-valued float score staying a float — matches the reference on a fully
// populated ImportCandidate.
func TestOracle_NestedAndScalars(t *testing.T) {
	v := abi.ImportCandidate{
		ComponentName: "core/datastore/postgres",
		Score:         1.0,
		Tier:          "exact",
		Signals:       abi.CandidateSignals{Table: 0.9, Category: 1.0, DomainLabels: 0.0, Shape: 0.5, Name: 0.25},
	}
	got := assertOracle(t, "ImportCandidate", v)
	var v2 abi.ImportCandidate
	if err := v2.DecodeCBOR(got); err != nil {
		t.Fatalf("DecodeCBOR: %v", err)
	}
	rt, _ := v2.EncodeCBOR()
	if !bytes.Equal(got, rt) {
		t.Errorf("round-trip not stable")
	}
}

// TestOracle_OpaquePassthrough proves R10: an opaque json.RawMessage field
// carries as a CBOR byte-string of its verbatim bytes, byte-identical to the
// reference (fxamacker encodes a json.RawMessage []byte as a byte-string of the
// same bytes), and round-trips verbatim — so an inner float rides untouched.
func TestOracle_OpaquePassthrough(t *testing.T) {
	// ParseInput carries an opaque options document (json.RawMessage) plus base64
	// source bytes ([]byte -> CBOR byte-string of raw bytes) — both R10/byte-string
	// carries the reference mirrors exactly.
	opaque := json.RawMessage(`{"weight":0.5,"mode":"strict","nested":{"n":3}}`)
	v := abi.ParseInput{
		Format:  "hcl",
		Bytes:   []byte("resource \"x\" {}\n"),
		Options: opaque,
	}
	got := assertOracle(t, "ParseInput_opaque", v)
	var v2 abi.ParseInput
	if err := v2.DecodeCBOR(got); err != nil {
		t.Fatalf("DecodeCBOR: %v", err)
	}
	if !bytes.Equal(v2.Options, opaque) {
		t.Errorf("opaque not verbatim after round-trip:\n want: %s\n  got: %s", opaque, v2.Options)
	}
	if !bytes.Equal(v2.Bytes, v.Bytes) {
		t.Errorf("byte-string field not verbatim after round-trip")
	}
}
