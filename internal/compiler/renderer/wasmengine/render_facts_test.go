// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasmengine

import (
	"bytes"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// renderFactsWire is a guest's render reply carrying three facts. They are listed
// out of name order, the object value's keys are out of key order and carry a
// space after one separator, and one value is an array whose items are out of
// order — so a forward that sorted the list, re-serialised a value, or tidied its
// bytes would not reproduce any of them.
const renderFactsWire = `{"files":[],"render_facts":[` +
	`{"name":"zeta_fact","value":{"tier":"web", "app":"api"}},` +
	`{"name":"alpha_fact","value":["b","a"]},` +
	`{"name":"mid_fact","value":"plain"}]}`

// renderFactsWant is each fact the reply above carries, as the exact bytes the
// guest wrote for its value.
var renderFactsWant = []struct{ name, value string }{
	{"zeta_fact", `{"tier":"web", "app":"api"}`},
	{"alpha_fact", `["b","a"]`},
	{"mid_fact", `"plain"`},
}

// TestSurfaceRenderFacts_ForwardsTheGuestReportVerbatimAndInOrder proves the
// adapter hands the host exactly what the guest reported. The forward is the one
// hop between the wire field and the host's record of it, and the property that
// matters there is that nothing is read or reshaped on the way: the facts arrive
// in the guest's order and every value arrives as the guest's own bytes.
func TestSurfaceRenderFacts_ForwardsTheGuestReportVerbatimAndInOrder(t *testing.T) {
	t.Parallel()

	wire := []byte(renderFactsWire)
	var out renderer.RenderNodeOutput
	if err := out.UnmarshalJSON(wire); err != nil {
		t.Fatalf("decode render output: %v", err)
	}

	var got [][]renderer.RenderFact
	nodeCtx := renderer.NodeRenderContext{
		NodeID:         "api",
		ProviderName:   "helm",
		RenderFactSink: func(f []renderer.RenderFact) { got = append(got, f) },
	}
	e := &Engine{}
	e.surfaceRenderFacts(nodeCtx, out.RenderFacts)

	if len(got) != 1 {
		t.Fatalf("render-fact sink called %d times, want 1", len(got))
	}
	if len(got[0]) != len(renderFactsWant) {
		t.Fatalf("forwarded %d facts, want %d: %+v", len(got[0]), len(renderFactsWant), got[0])
	}
	for i, want := range renderFactsWant {
		fact := got[0][i]
		if fact.Name != want.name {
			t.Errorf("fact %d is %q, want %q: the guest's order did not survive the forward", i, fact.Name, want.name)
		}
		if string(fact.Value) != want.value {
			t.Errorf("fact %q value = %s, want the guest's own bytes %s", fact.Name, fact.Value, want.value)
		}
	}

	// The forwarded values must not share the reply's bytes: the reply holds every
	// file the guest emitted, and a fact kept after the crossing would keep it
	// alive. Overwriting the reply after the forward proves no value aliases it;
	// the decode and the forward each copy, so this holds while either one does.
	copy(wire, bytes.Repeat([]byte("#"), len(wire)))
	for i, want := range renderFactsWant {
		if string(got[0][i].Value) != want.value {
			t.Errorf("fact %q changed when the guest's reply was overwritten (%s): the value still shares the reply's bytes",
				want.name, got[0][i].Value)
		}
	}
}

// TestSurfaceRenderFacts_NothingReportedCostsNoCall covers the inert paths. A
// guest that reports nothing — the field omitted from its reply — costs the sink
// no call, and a crossing with no sink attached does not panic: the sink is
// host-only wiring, so a render without one behaves exactly as one with it.
func TestSurfaceRenderFacts_NothingReportedCostsNoCall(t *testing.T) {
	t.Parallel()

	var bare renderer.RenderNodeOutput
	if err := bare.UnmarshalJSON([]byte(`{"files":[]}`)); err != nil {
		t.Fatalf("decode render output with no facts: %v", err)
	}
	if len(bare.RenderFacts) != 0 {
		t.Fatalf("an omitted field decoded to %d facts, want 0", len(bare.RenderFacts))
	}

	e := &Engine{}
	called := 0
	nodeCtx := renderer.NodeRenderContext{
		NodeID:         "api",
		RenderFactSink: func([]renderer.RenderFact) { called++ },
	}
	e.surfaceRenderFacts(nodeCtx, bare.RenderFacts)
	if called != 0 {
		t.Errorf("a reply reporting nothing called the sink %d times, want 0", called)
	}

	var populated renderer.RenderNodeOutput
	if err := populated.UnmarshalJSON([]byte(renderFactsWire)); err != nil {
		t.Fatalf("decode render output: %v", err)
	}
	// No sink attached: nothing to call, and nothing to panic on.
	e.surfaceRenderFacts(renderer.NodeRenderContext{NodeID: "api"}, populated.RenderFacts)
}
