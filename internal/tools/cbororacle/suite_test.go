// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cbororacle

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// The shared vector suite lives beside the codec in the aggrega-abi module, one
// tree consumed by all three languages. Each vector carries a JSON wire document
// (<Name>.golden.json) and its canonical-CBOR bytes (<Name>.cbor.hex). This test
// anchors the CBOR hex: it decodes the JSON document through the generated Go
// codec, encodes to canonical CBOR, and asserts the bytes equal the fxamacker
// reference (the independent oracle) and the committed hex. The Rust and TS
// harnesses assert their own encoders match the same hex, so a three-way hex
// match is a cross-language byte-identity proof anchored on the fxamacker oracle.
//
// Regenerate the hex goldens with AGGREGA_UPDATE_GOLDEN=1.
const testdataDir = "../../../../aggrega-abi/testdata"

// cborRT is the generated per-type CBOR codec surface over a value type T.
type cborRT[T any] interface {
	*T
	EncodeCBOR() ([]byte, error)
	DecodeCBOR([]byte) error
}

// runVec decodes a vector's JSON golden through the generated codec, encodes to
// canonical CBOR, checks the fxamacker reference (unless carved out) and the
// committed hex, and asserts a decode round-trip is byte-stable.
func runVec[T any, PT cborRT[T]](t *testing.T, file string, skipRef bool) {
	t.Helper()
	jsonBytes, err := os.ReadFile(filepath.Join(testdataDir, file+".golden.json"))
	if err != nil {
		t.Fatalf("read %s.golden.json: %v", file, err)
	}
	var v T
	if err := json.Unmarshal(bytes.TrimSpace(jsonBytes), PT(&v)); err != nil {
		t.Fatalf("%s: decode JSON golden: %v", file, err)
	}
	ours, err := PT(&v).EncodeCBOR()
	if err != nil {
		t.Fatalf("%s: EncodeCBOR: %v", file, err)
	}
	checkOrUpdateHex(t, file, ours)

	// Decode our bytes back to the canonical value, then re-encode: the round-trip
	// must be byte-stable, and this canonical value is what the fxamacker oracle
	// marshals. The generated codec canonicalizes a nil and an empty container to
	// the same empty CBOR container (a cross-language necessity — a Rust Vec or a
	// TS array cannot express nil, so all three emit the empty container), whereas
	// fxamacker preserves Go's nil-slice-as-null distinction. The JSON golden's
	// empty [] / {} decode to nil in Go's JSON codec; the CBOR decode yields the
	// non-nil empty container, so comparing fxamacker against the round-tripped
	// value keeps the oracle faithful to the encoding rules without asserting
	// fxamacker's nil-vs-empty opinion the canonical wire deliberately erases.
	var v2 T
	if err := PT(&v2).DecodeCBOR(ours); err != nil {
		t.Fatalf("%s: DecodeCBOR: %v", file, err)
	}
	rt, err := PT(&v2).EncodeCBOR()
	if err != nil {
		t.Fatalf("%s: re-encode: %v", file, err)
	}
	if !bytes.Equal(ours, rt) {
		t.Errorf("%s: round-trip not byte-stable\n in: %s\nout: %s",
			file, hex.EncodeToString(ours), hex.EncodeToString(rt))
	}
	if !skipRef {
		em := refEncoder(t)
		ref, err := em.Marshal(v2)
		if err != nil {
			t.Fatalf("%s: reference marshal: %v", file, err)
		}
		if !bytes.Equal(ours, ref) {
			t.Fatalf("%s: generated CBOR != fxamacker reference\n generated: %s\n reference: %s",
				file, hex.EncodeToString(ours), hex.EncodeToString(ref))
		}
	}
}

// checkOrUpdateHex writes the canonical hex under AGGREGA_UPDATE_GOLDEN, else
// asserts the committed hex matches.
func checkOrUpdateHex(t *testing.T, file string, cborBytes []byte) {
	t.Helper()
	p := filepath.Join(testdataDir, file+".cbor.hex")
	got := hex.EncodeToString(cborBytes)
	if os.Getenv("AGGREGA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(p, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("write %s.cbor.hex: %v", file, err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s.cbor.hex (run AGGREGA_UPDATE_GOLDEN=1 to create): %v", file, err)
	}
	if strings.TrimSpace(string(want)) != got {
		t.Errorf("%s: CBOR hex not byte-identical to golden\n golden: %s\n    got: %s",
			file, strings.TrimSpace(string(want)), got)
	}
}

// TestSuite_CBORVectors iterates the shared vector suite. FRPStepOutput is carved
// out of the fxamacker assertion: its patch operand is a bare opaque JSON value
// (any), which the R10 rule carries as a CBOR byte-string of the verbatim JSON
// bytes, whereas fxamacker encodes a Go any as its native CBOR type — a
// deliberate representation difference, so FRPStepOutput is proven by the
// cross-language hex match plus its round-trip, not by the fxamacker byte oracle.
func TestSuite_CBORVectors(t *testing.T) {
	runVec[abi.CLIRunInput](t, "CLIRunInput", false)
	runVec[abi.FRPStepInput](t, "FRPStepInput", false)
	runVec[abi.FRPStepOutput](t, "FRPStepOutput", true)
	runVec[abi.MutatorInput](t, "MutatorInput", false)
	runVec[abi.MutatorOutput](t, "MutatorOutput", false)
	runVec[abi.LinterInput](t, "LinterInput", false)
	runVec[abi.LinterOutput](t, "LinterOutput", false)
	runVec[abi.IRGraph](t, "IRGraph", false)
	runVec[abi.PolicyInput](t, "PolicyInput", false)
	runVec[abi.PolicyOutput](t, "PolicyOutput", false)
	runVec[abi.RenderNodeInput](t, "RenderNodeInput", false)
	runVec[abi.RenderNodeOutput](t, "RenderNodeOutput", false)
	runVec[abi.IRNodeProjection](t, "IRNodeProjectionSensitiveRenderedKeys", false)
	runVec[abi.IRFeatureEntry](t, "IRFeatureEntrySensitiveAuthoredLeaves", false)
	runVec[abi.AssembleBoundaryInput](t, "AssembleBoundaryInput", false)
	runVec[abi.AssembleBoundaryOutput](t, "AssembleBoundaryOutput", false)
	runVec[abi.LinkPublishInput](t, "LinkPublishInput", false)
	runVec[abi.LinkPublishOutput](t, "LinkPublishOutput", false)
	runVec[abi.LinkResolveInput](t, "LinkResolveInput", false)
	runVec[abi.LinkResolveOutput](t, "LinkResolveOutput", false)
	runVec[abi.PluginDescriptor](t, "PluginDescriptorCLI", false)
	runVec[abi.PluginDescriptor](t, "PluginDescriptorProvider", false)
	runVec[abi.PluginDescriptor](t, "PluginDescriptorMinimal", false)
	runVec[abi.PluginDescriptor](t, "PluginDescriptorMoo", false)
	runVec[abi.ParseInput](t, "ParseInput", false)
	runVec[abi.ParseOutput](t, "ParseOutput", false)
	runVec[abi.ParseOutput](t, "ParseOutputBare", false)
	runVec[abi.ImportInput](t, "ImportInput", false)
	runVec[abi.ImportOutput](t, "ImportOutput", false)
	runVec[abi.ParseSourceResponse](t, "ParseSourceResponse", false)
	runVec[abi.FetchExternalInputResponse](t, "FetchExternalInputResponse", false)
	runVec[abi.FetchExternalInputResponse](t, "FetchExternalInputResponseContent", false)
}

// floatVector is one entry of the shared float-boundary suite: a label, the f64's
// raw bits (hex, so NaN / ±Inf / −0.0 survive a JSON carrier that has no literal
// for them), and the canonical CBOR of a CandidateSignals whose five float fields
// all hold that value.
type floatVector struct {
	Label string `json:"label"`
	Bits  string `json:"bits"`
	Cbor  string `json:"cbor"`
}

// floatBitsTable is the boundary set the float suite pins, keyed to the raw f64
// bits so the special values round-trip through the JSON carrier exactly.
var floatBitsTable = []struct {
	label string
	value float64
}{
	{"neg_zero", math.Copysign(0, -1)},
	{"pos_zero", 0.0},
	{"one", 1.0},
	{"hundred", 100.0},
	{"f16_max", 65504.0},
	{"f16_exact_frac", 0.15625},
	{"needs_f64", 0.1},
	{"score_typical", 0.87},
	{"beyond_f32", 1e39},
	{"pos_inf", math.Inf(1)},
	{"neg_inf", math.Inf(-1)},
	{"nan", math.NaN()},
}

// TestSuite_FloatVectors anchors the shared float-boundary vectors: each value's
// CandidateSignals encoding matches the fxamacker reference and the committed
// hex, and round-trips byte-stably. The Rust and TS harnesses reconstruct the
// same f64 from the bits and assert their encoders reproduce the same hex, so the
// non-JSON-representable float cases (NaN, ±Inf, −0.0) are proven cross-language.
func TestSuite_FloatVectors(t *testing.T) {
	p := filepath.Join(testdataDir, "float_vectors.json")
	if os.Getenv("AGGREGA_UPDATE_GOLDEN") == "1" {
		var vecs []floatVector
		for _, e := range floatBitsTable {
			v := abi.CandidateSignals{Table: e.value, Category: e.value, DomainLabels: e.value, Shape: e.value, Name: e.value}
			b, err := v.EncodeCBOR()
			if err != nil {
				t.Fatalf("%s: EncodeCBOR: %v", e.label, err)
			}
			vecs = append(vecs, floatVector{
				Label: e.label,
				Bits:  hex.EncodeToString(u64be(math.Float64bits(e.value))),
				Cbor:  hex.EncodeToString(b),
			})
		}
		out, err := json.MarshalIndent(vecs, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(out, '\n'), 0o644); err != nil {
			t.Fatalf("write float_vectors.json: %v", err)
		}
		return
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read float_vectors.json (run AGGREGA_UPDATE_GOLDEN=1 to create): %v", err)
	}
	var vecs []floatVector
	if err := json.Unmarshal(raw, &vecs); err != nil {
		t.Fatalf("decode float_vectors.json: %v", err)
	}
	em := refEncoder(t)
	for _, fv := range vecs {
		bitsBytes, err := hex.DecodeString(fv.Bits)
		if err != nil || len(bitsBytes) != 8 {
			t.Fatalf("%s: bad bits %q", fv.Label, fv.Bits)
		}
		f := math.Float64frombits(be64(bitsBytes))
		v := abi.CandidateSignals{Table: f, Category: f, DomainLabels: f, Shape: f, Name: f}
		ours, err := v.EncodeCBOR()
		if err != nil {
			t.Fatalf("%s: EncodeCBOR: %v", fv.Label, err)
		}
		if got := hex.EncodeToString(ours); got != fv.Cbor {
			t.Errorf("%s: CBOR hex mismatch\n golden: %s\n    got: %s", fv.Label, fv.Cbor, got)
		}
		ref, err := em.Marshal(v)
		if err != nil {
			t.Fatalf("%s: reference marshal: %v", fv.Label, err)
		}
		if !bytes.Equal(ours, ref) {
			t.Fatalf("%s: generated CBOR != fxamacker reference\n generated: %s\n reference: %s",
				fv.Label, hex.EncodeToString(ours), hex.EncodeToString(ref))
		}
		var v2 abi.CandidateSignals
		if err := v2.DecodeCBOR(ours); err != nil {
			t.Fatalf("%s: DecodeCBOR: %v", fv.Label, err)
		}
		rt, _ := v2.EncodeCBOR()
		if !bytes.Equal(ours, rt) {
			t.Errorf("%s: round-trip not byte-stable", fv.Label)
		}
	}
}

func u64be(v uint64) []byte {
	return []byte{
		byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32),
		byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v),
	}
}

func be64(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}
