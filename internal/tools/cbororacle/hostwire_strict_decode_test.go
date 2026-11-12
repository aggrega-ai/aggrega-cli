// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cbororacle_test

import (
	"encoding/json"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
)

// The socket-output shape contract on the canonical-CBOR wire.
//
// A socket's entry function is overridable per attachment, so an attachment may
// name an export whose output is a different envelope entirely. Read leniently,
// such an output decodes into an empty value of the expected type — and an empty
// value is itself a legitimate result (no verdict fired, no patch emitted), so
// the two are byte-indistinguishable and a build proceeds as though the plugin
// had approved it. A typo in an export name is enough; no attacker is needed.
//
// Every generated type therefore carries DecodeCBORStrict beside DecodeCBOR: the
// strict entry point refuses a top-level key the shape does not declare, naming
// it. The tests below drive real canonical CBOR — produced by the generated
// encoders, so the bytes are canonical by construction rather than by assertion
// — across the two envelopes that are genuinely confusable at a governance
// socket, in both directions.

// encodeCanonical encodes v with its generated encoder and fails the test if the
// encode fails, so each case below reads as the payload it is about.
func encodeCanonical(t *testing.T, v interface{ EncodeCBOR() ([]byte, error) }) []byte {
	t.Helper()
	b, err := v.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the fixture payload: %v", err)
	}
	return b
}

// TestDecodeCBORStrictRefusesAForeignEnvelope walks the contract in both
// directions across the two shapes a governance attachment can confuse: the
// verdict envelope (results) and the label-transform envelope (patches). Each
// direction asserts three things about one payload — the strict entry point
// refuses and names the offending key, the lenient entry point accepts the same
// bytes, and what the lenient read produces is an empty value nothing can tell
// from a real result.
//
// The lenient leg is not decoration. It is what makes the strict leg
// non-vacuous: it demonstrates that the refusal comes from the strictness and
// from nothing else about the payload, which a passing strict assertion on its
// own cannot show.
func TestDecodeCBORStrictRefusesAForeignEnvelope(t *testing.T) {
	t.Parallel()

	verdictEnvelope := encodeCanonical(t, abi.PolicyOutput{
		Results: []abi.Diagnostic{{Severity: "halt", Message: "denied"}},
	})
	patchEnvelope := encodeCanonical(t, topology.MutatorOutput{
		Patches: []json.RawMessage{json.RawMessage(`{"op":"add","path":"/x","value":"y"}`)},
	})

	t.Run("a verdict envelope is not the label-transform shape", func(t *testing.T) {
		t.Parallel()

		var strict topology.MutatorOutput
		err := strict.DecodeCBORStrict(verdictEnvelope)
		if err == nil {
			t.Fatal("a verdict envelope is not this shape: expected a refusal, got nil")
		}
		if !strings.Contains(err.Error(), "results") {
			t.Errorf("the refusal does not name the key that does not belong (%q):\n%v", "results", err)
		}

		var lenient topology.MutatorOutput
		if err := lenient.DecodeCBOR(verdictEnvelope); err != nil {
			t.Fatalf("the lenient entry point must still skip an undeclared key, not refuse it: %v", err)
		}
		if lenient.Patches != nil {
			t.Errorf("the lenient read produced %d patch(es); the hazard being guarded is that it produces none",
				len(lenient.Patches))
		}
	})

	t.Run("a label-transform envelope is not the verdict shape", func(t *testing.T) {
		t.Parallel()

		var strict abi.PolicyOutput
		err := strict.DecodeCBORStrict(patchEnvelope)
		if err == nil {
			t.Fatal("a label-transform envelope is not this shape: expected a refusal, got nil")
		}
		if !strings.Contains(err.Error(), "patches") {
			t.Errorf("the refusal does not name the key that does not belong (%q):\n%v", "patches", err)
		}

		var lenient abi.PolicyOutput
		if err := lenient.DecodeCBOR(patchEnvelope); err != nil {
			t.Fatalf("the lenient entry point must still skip an undeclared key, not refuse it: %v", err)
		}
		if lenient.Results != nil {
			t.Errorf("the lenient read produced %d verdict(s); the hazard being guarded is that it produces none",
				len(lenient.Results))
		}
	})
}

// TestDecodeCBORStrictAcceptsTheDeclaredShape pins the other half of the
// contract, and it is the half a strictness change breaks. The whole hazard is
// that an EMPTY envelope is a legitimate result, so an entry point that refused
// one would trade a silent allow for a false halt on every build where no
// verdict fires and no patch is emitted — which is most of them.
func TestDecodeCBORStrictAcceptsTheDeclaredShape(t *testing.T) {
	t.Parallel()

	t.Run("an empty verdict list is a legitimate allow", func(t *testing.T) {
		t.Parallel()
		payload := encodeCanonical(t, abi.PolicyOutput{Results: []abi.Diagnostic{}})
		var out abi.PolicyOutput
		if err := out.DecodeCBORStrict(payload); err != nil {
			t.Fatalf("an empty verdict list is the declared shape and must decode: %v", err)
		}
		if len(out.Results) != 0 {
			t.Errorf("decoded %d verdict(s), want 0", len(out.Results))
		}
	})

	t.Run("an empty patch list is a legitimate no-op", func(t *testing.T) {
		t.Parallel()
		payload := encodeCanonical(t, topology.MutatorOutput{Patches: []json.RawMessage{}})
		var out topology.MutatorOutput
		if err := out.DecodeCBORStrict(payload); err != nil {
			t.Fatalf("an empty patch list is the declared shape and must decode: %v", err)
		}
		if len(out.Patches) != 0 {
			t.Errorf("decoded %d patch(es), want 0", len(out.Patches))
		}
	})

	t.Run("a populated declared shape decodes with its content", func(t *testing.T) {
		t.Parallel()
		payload := encodeCanonical(t, abi.PolicyOutput{
			Results: []abi.Diagnostic{{Severity: "halt", Message: "denied"}},
		})
		var out abi.PolicyOutput
		if err := out.DecodeCBORStrict(payload); err != nil {
			t.Fatalf("the declared shape must decode: %v", err)
		}
		if len(out.Results) != 1 || out.Results[0].Message != "denied" {
			t.Errorf("the strict entry point must decode identically to the lenient one; got %+v", out.Results)
		}
	})
}

// TestDecodeCBORStrictReadsTopLevelKeysOnly pins the coverage bound, so a later
// reader does not cite this check as covering more than it does. It reads which
// keys the OUTER map carries and skips each value whole, so a key nested inside a
// value is tolerated exactly as it was before.
//
// The bound is asserted rather than described because it is the difference
// between a shape check and a schema validator, and the payloads are hand-built
// canonical CBOR rather than encoder output: a generated encoder cannot produce
// an undeclared key, which is precisely what these two cases need.
func TestDecodeCBORStrictReadsTopLevelKeysOnly(t *testing.T) {
	t.Parallel()

	// map(1){ "verdicts": array(0) } — a top-level key NEITHER envelope declares.
	topLevelUndeclared := []byte{
		0xa1,
		0x68, 'v', 'e', 'r', 'd', 'i', 'c', 't', 's',
		0x80,
	}
	// map(1){ "results": array(1)[ map(1){ "zzz": 0 } ] } — declared at the top
	// level, undeclared one level down.
	nestedUndeclared := []byte{
		0xa1,
		0x67, 'r', 'e', 's', 'u', 'l', 't', 's',
		0x81,
		0xa1,
		0x63, 'z', 'z', 'z',
		0x00,
	}

	var refuse abi.PolicyOutput
	err := refuse.DecodeCBORStrict(topLevelUndeclared)
	if err == nil {
		t.Fatal("a top-level key no shape declares must be refused, got nil")
	}
	if !strings.Contains(err.Error(), "verdicts") {
		t.Errorf("the refusal does not name the key that does not belong (%q):\n%v", "verdicts", err)
	}

	var tolerate abi.PolicyOutput
	if err := tolerate.DecodeCBORStrict(nestedUndeclared); err != nil {
		t.Fatalf("the check reads top-level keys only, so a nested undeclared key must still be "+
			"tolerated; refusing one would make this a schema validator: %v", err)
	}
	if len(tolerate.Results) != 1 {
		t.Errorf("the nested value must still decode; got %d result(s)", len(tolerate.Results))
	}
}
