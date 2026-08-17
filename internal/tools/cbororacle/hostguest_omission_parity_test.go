// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cbororacle_test

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	cbor "github.com/fxamacker/cbor/v2"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// An order-preserving container is realized as a struct value on the compiler
// side and as a plain string-keyed map on the plugin side, and both sides mark
// such a field as omitted when it is empty. A plain map has an absent state that
// a struct value does not, so the compiler side matches it only by asking the
// container itself whether it is empty — which makes conditional emission a
// property worth pinning from both directions at once.
//
// The tests below cover the empty and the populated state separately, because
// each state hides the other's failure: an encoder emitting the key
// unconditionally passes any test built only from populated fixtures, and one
// dropping it unconditionally passes any test built only from empty ones. The
// third test adds the arithmetic neither of those reaches — a definite-length
// CBOR map declares its pair count in its head, so a conditionally emitted field
// must make that count conditional under the same predicate, and every affected
// type is compared in both forms through a decoder that reads the head.

// refDecode decodes CBOR bytes with an independent conforming decoder rather
// than with the codec family that produced them.
//
// The independence is the point. This decoder reads the definite-length map head
// and then consumes exactly the number of pairs it declares, so it catches a
// head that disagrees with its body in both directions: an over-declaring head
// runs out of data, and an under-declaring one leaves trailing bytes. Re-decoding
// with the generating codec's own counterpart cannot see either, because a
// matching mistake on both sides of one wire round-trips cleanly.
func refDecode(t *testing.T, name string, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := cbor.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: the independent decoder rejected the bytes: %v\nhex: %s",
			name, err, hex.EncodeToString(b))
	}
	return m
}

// cborKeys is the top-level key set of a CBOR map, read through the independent
// decoder so the count it reports is the one the head declared.
func cborKeys(t *testing.T, name string, b []byte) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for k := range refDecode(t, name, b) {
		keys[k] = true
	}
	return keys
}

// jsonKeys is the top-level key set of a JSON object. The compiler side emits
// both encodings from one omission decision, so a key's presence is asserted on
// both wires rather than on the binary one alone.
func jsonKeys(t *testing.T, name string, b []byte) map[string]bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: re-parsing the emitted JSON: %v\nbytes: %s", name, err, b)
	}
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}

// TestIndependentDecoderCatchesHeadBodyDisagreement calibrates the instrument the
// conditional-head test below depends on. That test's value rests entirely on the
// decoder consuming exactly the pair count the head declares: a decoder tolerant
// of a head that disagrees with its body would let the desynchronisation this file
// exists to catch pass silently, which is a green from a blind instrument rather
// than a property of the bytes.
//
// The decoder is called directly here rather than through refDecode, which ends
// the test on a decode error by design. The duplication is deliberate — a
// calibration routed through the helper it calibrates cannot fail independently of
// it — so do not "simplify" this to reuse refDecode.
func TestIndependentDecoderCatchesHeadBodyDisagreement(t *testing.T) {
	t.Parallel()

	// One key/value pair each, as two one-byte text strings.
	pairKV := []byte{0x61, 'k', 0x61, 'v'}
	pairJW := []byte{0x61, 'j', 0x61, 'w'}

	cases := []struct {
		name string
		// A definite-length map head is major type 5 with the pair count in its
		// low bits, so 0xa0|n declares n pairs.
		encoded []byte
	}{
		{
			name:    "head over-declares its pair count",
			encoded: append([]byte{0xa0 | 2}, pairKV...),
		},
		{
			name:    "head under-declares its pair count",
			encoded: append(append([]byte{0xa0 | 1}, pairKV...), pairJW...),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var m map[string]any
			if err := cbor.Unmarshal(c.encoded, &m); err == nil {
				t.Errorf("the decoder accepted a map whose head disagrees with its body: %s decoded to %v",
					hex.EncodeToString(c.encoded), m)
			}
		})
	}
}

func TestHostGuestWireParity_OmitsEmptyOrderedContainers(t *testing.T) {
	t.Parallel()

	p := rendercore.IRNodeProjection{ID: "web", Type: "core/web-service", ResolvedRecipe: "core/helm"}
	conditional := []string{"values", "features", "uses"}

	b, err := p.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	gotCBOR := cborKeys(t, "IRNodeProjection(empty)", b)
	for _, k := range conditional {
		if gotCBOR[k] {
			t.Errorf("CBOR key %q is emitted for an empty order-preserving container; keys = %v", k, gotCBOR)
		}
	}

	jb, err := p.MarshalJSON()
	if err != nil {
		t.Fatalf("compiler-side JSON marshal: %v", err)
	}
	gotJSON := jsonKeys(t, "IRNodeProjection(empty)", jb)
	for _, k := range conditional {
		if gotJSON[k] {
			t.Errorf("JSON key %q is emitted for an empty order-preserving container; keys = %v", k, gotJSON)
		}
	}

	// The plugin side realizes each of these as a plain map, so it must decode a
	// payload with the keys absent and re-encode to the identical bytes. That is
	// what makes the two sides agree on the empty case rather than merely each
	// being self-consistent.
	var guest abi.IRNodeProjection
	crossSide(t, "IRNodeProjection(empty)", b, &guest)
	if len(guest.Values) != 0 || len(guest.Features) != 0 || len(guest.Uses) != 0 {
		t.Errorf("plugin side decoded non-empty containers from absent keys: %+v", guest)
	}
}

func TestHostGuestWireParity_EmitsPopulatedOrderedContainers(t *testing.T) {
	t.Parallel()

	p := rendercore.IRNodeProjection{
		ID:             "web",
		Type:           "core/web-service",
		ResolvedRecipe: "core/helm",
		Values:         om[any](t, "replicas", 2),
		Features:       om[types.IRFeatureEntry](t, "tls", types.IRFeatureEntry{}),
		Uses:           om[[]string](t, "db", []string{"primary"}),
	}
	conditional := []string{"values", "features", "uses"}

	b, err := p.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	gotCBOR := cborKeys(t, "IRNodeProjection(populated)", b)
	for _, k := range conditional {
		if !gotCBOR[k] {
			t.Errorf("CBOR key %q is dropped for a populated order-preserving container; keys = %v", k, gotCBOR)
		}
	}

	jb, err := p.MarshalJSON()
	if err != nil {
		t.Fatalf("compiler-side JSON marshal: %v", err)
	}
	gotJSON := jsonKeys(t, "IRNodeProjection(populated)", jb)
	for _, k := range conditional {
		if !gotJSON[k] {
			t.Errorf("JSON key %q is dropped for a populated order-preserving container; keys = %v", k, gotJSON)
		}
	}

	crossSide(t, "IRNodeProjection(populated)", b, &abi.IRNodeProjection{})
}

// TestHostGuestWireParity_ConditionalMapHeadMatchesBody covers every type on
// this socket whose declared pair count is conditional, in both its empty and
// its populated form. A head that disagreed with its body would fail in the
// independent decoder; a count that moved independently of the body would fail
// the arithmetic below.
func TestHostGuestWireParity_ConditionalMapHeadMatchesBody(t *testing.T) {
	t.Parallel()

	anyMap := om[any](t, "k", "v")
	strMap := om[string](t, "k", "v")
	listMap := om[[]string](t, "k", []string{"v"})
	featureMap := om[types.IRFeatureEntry](t, "f", types.IRFeatureEntry{})

	type encoder interface{ EncodeCBOR() ([]byte, error) }

	cases := []struct {
		name string
		// empty carries no order-preserving container; populated is the same
		// value with every conditional key's container filled.
		empty       encoder
		populated   encoder
		conditional []string
	}{
		{
			name:  "IRNodeProjection",
			empty: rendercore.IRNodeProjection{ID: "a", Type: "t", ResolvedRecipe: "r"},
			populated: rendercore.IRNodeProjection{
				ID: "a", Type: "t", ResolvedRecipe: "r",
				Values: anyMap, Features: featureMap, Uses: listMap,
			},
			conditional: []string{"values", "features", "uses"},
		},
		{
			name:        "RenderNodeOutput",
			empty:       rendercore.RenderNodeOutput{},
			populated:   rendercore.RenderNodeOutput{DerivedKeys: listMap},
			conditional: []string{"derived_keys"},
		},
		{
			name:        "RenderedNode",
			empty:       rendercore.RenderedNode{ID: "a", Type: "t"},
			populated:   rendercore.RenderedNode{ID: "a", Type: "t", Annotations: strMap},
			conditional: []string{"annotations"},
		},
		{
			name:        "SubChart",
			empty:       rendercore.SubChart{InstanceID: "a"},
			populated:   rendercore.SubChart{InstanceID: "a", Values: anyMap},
			conditional: []string{"values"},
		},
		{
			// The empty form of this one declares zero pairs, which is the edge
			// the head encoding is most likely to get wrong.
			name:        "BoundaryEnvOverridesInput",
			empty:       rendercore.BoundaryEnvOverridesInput{},
			populated:   rendercore.BoundaryEnvOverridesInput{Infra: anyMap},
			conditional: []string{"infra"},
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
					t.Errorf("key %q is emitted for an empty order-preserving container; keys = %v", k, emptyKeys)
				}
				if !populatedKeys[k] {
					t.Errorf("key %q is dropped for a populated order-preserving container; keys = %v", k, populatedKeys)
				}
			}

			// Each form's key count is what the independent decoder consumed, so
			// it is the pair count the head declared. Comparing the two forms
			// therefore pins the count's conditionality to the body's, which a
			// per-form check cannot do: a count frozen at either form's value
			// would leave one of the two heads self-consistent and wrong.
			if want := len(emptyKeys) + len(c.conditional); len(populatedKeys) != want {
				t.Errorf("populated form declares %d pairs, want %d (%d unconditional + %d conditional)",
					len(populatedKeys), want, len(emptyKeys), len(c.conditional))
			}
		})
	}
}
