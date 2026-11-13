// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cbororacle

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	cbor "github.com/fxamacker/cbor/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
)

// The refused vectors are canonical CBOR every decoder of their shape must
// REFUSE: documents no encoder of the order-preserving map's array form can write,
// each malformed in exactly one way. The plugin bindings' harnesses hold every
// language's decoder to refusing them; this suite holds the two Go decoders of the
// shape — the plugin binding's and the compiler's — to the same, and does the one
// thing no generated decoder can do for it: it confirms, through a decoder this
// schema did not generate, that each vector really is well-formed CBOR whose only
// fault is the one its name states. Without that calibration a refusal could pass
// on a vector that was simply truncated, which every decoder refuses for the wrong
// reason.

// refusedSuffix is the file a refused vector is. It does not end in the vector
// pair's CBOR suffix, so the pair registration guard never counts it.
const refusedSuffix = ".refused-cbor.hex"

// refusedVector is one refused vector: the fault the reference decoder must find
// in its labels array form, and a fragment every refusal of it must carry.
type refusedVector struct {
	name string
	// fault reads the labels array the reference decoder produced and reports
	// whether it carries the vector's fault, so the vector is shown to be what
	// its name says before any refusal of it is trusted.
	fault func(entries []any) bool
	want  string
}

var refusedVectors = []refusedVector{
	{
		name: "MapOrderMutatorNodeRepeatedKey",
		fault: func(entries []any) bool {
			seen := map[string]bool{}
			for _, e := range entries {
				pair, ok := e.([]any)
				if !ok || len(pair) != 2 {
					return false
				}
				k, ok := pair[0].(string)
				if !ok {
					return false
				}
				if seen[k] {
					return true
				}
				seen[k] = true
			}
			return false
		},
		want: `duplicate key "a"`,
	},
	{
		name: "MapOrderMutatorNodeEntryArity",
		fault: func(entries []any) bool {
			for _, e := range entries {
				if pair, ok := e.([]any); ok && len(pair) != 2 {
					return true
				}
			}
			return false
		},
		want: "holds 3 items, want 2",
	},
	{
		name: "MapOrderMutatorNodeEntryKeyNotText",
		fault: func(entries []any) bool {
			for _, e := range entries {
				if pair, ok := e.([]any); ok && len(pair) == 2 {
					if _, isText := pair[0].(string); !isText {
						return true
					}
				}
			}
			return false
		},
		want: "expected text string",
	},
}

// readRefusedVector reads a refused vector, holding it to the hex golden shape.
func readRefusedVector(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testdataDir, name+refusedSuffix))
	if err != nil {
		t.Fatalf("read refused vector %s: %v", name, err)
	}
	body, ok := strings.CutSuffix(string(raw), "\n")
	if !ok || strings.ContainsFunc(body, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) {
		t.Fatalf("refused vector %s is not lowercase hex followed by one newline: %q", name, raw)
	}
	b, err := hex.DecodeString(body)
	if err != nil {
		t.Fatalf("decode refused vector %s: %v", name, err)
	}
	return b
}

// refuses runs one decode and asserts it refuses with an error carrying want and
// does not panic.
func refuses(t *testing.T, decoder string, want string, decode func() error) {
	t.Helper()
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("decode panicked rather than refusing: %v", r)
			}
		}()
		return decode()
	}()
	switch {
	case err == nil:
		t.Errorf("%s accepted a vector every decoder must refuse", decoder)
	case !strings.Contains(err.Error(), want):
		t.Errorf("%s refused it, but the error does not name the fault: got %q, want it to contain %q", decoder, err, want)
	}
}

// TestSuite_RefusedVectors calibrates each refused vector through the reference
// decoder, then holds both Go decoders of the shape, through both their decode
// entry points, to refusing it.
func TestSuite_RefusedVectors(t *testing.T) {
	registered := map[string]bool{}
	for _, rv := range refusedVectors {
		registered[rv.name] = true
		t.Run(rv.name, func(t *testing.T) {
			data := readRefusedVector(t, rv.name)

			var doc map[string]any
			if err := cbor.Unmarshal(data, &doc); err != nil {
				t.Fatalf("the reference decoder rejected the vector, so it is not well-formed CBOR and a refusal of it "+
					"proves nothing about the fault it is named for: %v", err)
			}
			entries, ok := doc["labels"].([]any)
			if !ok {
				t.Fatalf("the vector's labels are %T, not the array form, so it does not exercise the array form", doc["labels"])
			}
			if !rv.fault(entries) {
				t.Fatalf("the reference decoder does not find the fault %s is named for in its labels %v", rv.name, entries)
			}

			refuses(t, "the plugin binding's DecodeCBOR", rv.want, func() error {
				var v abi.MutatorNode
				return v.DecodeCBOR(data)
			})
			refuses(t, "the plugin binding's DecodeCBORStrict", rv.want, func() error {
				var v abi.MutatorNode
				return v.DecodeCBORStrict(data)
			})
			refuses(t, "the compiler's DecodeCBOR", rv.want, func() error {
				var v topology.MutatorNode
				return v.DecodeCBOR(data)
			})
			refuses(t, "the compiler's DecodeCBORStrict", rv.want, func() error {
				var v topology.MutatorNode
				return v.DecodeCBORStrict(data)
			})
		})
	}

	// The refused vectors on disk are exactly the ones registered above, read from
	// the directory rather than listed, for the reason the pair guard gives.
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("read the vector directory %s: %v", testdataDir, err)
	}
	onDisk := 0
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), refusedSuffix)
		if !ok || e.IsDir() {
			continue
		}
		onDisk++
		if !registered[name] {
			t.Errorf("refused vector %s is in the shared suite but registered by no assertion here", name)
		}
	}
	if onDisk == 0 {
		t.Fatalf("found no %s vectors under %s, so this check certifies nothing", refusedSuffix, testdataDir)
	}
}
