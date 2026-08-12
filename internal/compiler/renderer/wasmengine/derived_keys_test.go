// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestSurfaceDerivedKeys_ForwardsTheGuestDeclarationVerbatim proves the adapter
// hands the host exactly what the guest declared. The forward is the one hop
// between the wire field and the host's check, and the property that matters at
// it is that nothing is inspected or reshaped on the way: a space name and a name
// are opaque strings here, so any name any provider derives crosses unchanged.
func TestSurfaceDerivedKeys_ForwardsTheGuestDeclarationVerbatim(t *testing.T) {
	t.Parallel()

	var got []types.OrderedMap[[]string]
	nodeCtx := renderer.NodeRenderContext{
		NodeID:         "api",
		ProviderName:   "cloudformation",
		DerivedKeySink: func(d types.OrderedMap[[]string]) { got = append(got, d) },
	}

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{"paymentsapiQueue", "paymentsapiTopic"})
	declared.Set("export-names", []string{"aggrega-prod-api-orders-db-host"})

	e := &Engine{}
	e.surfaceDerivedKeys(nodeCtx, declared)

	if len(got) != 1 {
		t.Fatalf("derived-key sink called %d times, want 1", len(got))
	}
	// Declaration order survives the crossing, which is what keeps the payload a
	// function of the guest's output rather than of a map walk.
	if want := []string{"logical-ids", "export-names"}; !reflect.DeepEqual(got[0].Keys(), want) {
		t.Errorf("space order = %v, want %v", got[0].Keys(), want)
	}
	ids, ok := got[0].Get("logical-ids")
	if !ok {
		t.Fatal("logical-ids space did not reach the sink")
	}
	if want := []string{"paymentsapiQueue", "paymentsapiTopic"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("logical-ids = %v, want %v", ids, want)
	}
}

// TestSurfaceDerivedKeys_NilSinkAndEmptyDeclarationAreNoOps covers the two inert
// paths. A guest that declares nothing must cost the sink no call, and a render
// with no sink attached must not panic — the sink is host-only wiring, so a
// crossing that runs without one has to behave exactly as one that runs with one.
func TestSurfaceDerivedKeys_NilSinkAndEmptyDeclarationAreNoOps(t *testing.T) {
	t.Parallel()

	e := &Engine{}

	declared := types.OrderedMap[[]string]{}
	declared.Set("logical-ids", []string{"paymentsapiQueue"})
	// No sink attached: nothing to call, and nothing to panic on.
	e.surfaceDerivedKeys(renderer.NodeRenderContext{NodeID: "api"}, declared)

	called := 0
	nodeCtx := renderer.NodeRenderContext{
		NodeID:         "api",
		DerivedKeySink: func(types.OrderedMap[[]string]) { called++ },
	}
	e.surfaceDerivedKeys(nodeCtx, types.OrderedMap[[]string]{})
	if called != 0 {
		t.Errorf("an empty declaration called the sink %d times, want 0", called)
	}
}

// TestRenderNodeOutput_CarriesDeclaredNamesOffTheWire closes the gap between the
// wire and the forward. It decodes a guest's render output through the generated
// codec and asserts the declared names arrive on the field the adapter reads, so
// the field is proven populated from real wire bytes rather than only from a
// hand-built value.
func TestRenderNodeOutput_CarriesDeclaredNamesOffTheWire(t *testing.T) {
	t.Parallel()

	wire := []byte(`{"files":[],"derived_keys":{"logical-ids":["paymentsapiQueue","paymentsapiTopic"],` +
		`"export-names":["aggrega-prod-api-orders-db-host"]}}`)

	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(wire); err != nil {
		t.Fatalf("decode render output: %v", err)
	}
	if want := []string{"logical-ids", "export-names"}; !reflect.DeepEqual(out.DerivedKeys.Keys(), want) {
		t.Errorf("space order off the wire = %v, want %v", out.DerivedKeys.Keys(), want)
	}
	ids, ok := out.DerivedKeys.Get("logical-ids")
	if !ok {
		t.Fatal("logical-ids space absent after decoding the wire payload")
	}
	if want := []string{"paymentsapiQueue", "paymentsapiTopic"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("logical-ids off the wire = %v, want %v", ids, want)
	}

	// A guest that omits the field decodes to an empty declaration rather than to
	// anything the forward would treat as a declaration.
	var bare renderer.RenderNodeOutput
	if err := bare.UnmarshalJSON([]byte(`{"files":[]}`)); err != nil {
		t.Fatalf("decode render output with no declarations: %v", err)
	}
	if bare.DerivedKeys.Len() != 0 {
		t.Errorf("omitted field decoded to %d spaces, want 0", bare.DerivedKeys.Len())
	}
}
