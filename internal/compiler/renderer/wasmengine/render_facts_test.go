// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmengine

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// renderFactsWire is a guest's render reply carrying three facts, every value a
// list. They are listed out of name order, the object entry's keys are out of key
// order and carry a space after one separator, and one list's items are out of
// order — so a forward that sorted the facts, re-serialised a value, or tidied its
// bytes would not reproduce any of them.
const renderFactsWire = `{"files":[],"render_facts":[` +
	`{"name":"zeta_fact","value":[{"tier":"web", "app":"api"}]},` +
	`{"name":"alpha_fact","value":["b","a"]},` +
	`{"name":"mid_fact","value":["plain"]}]}`

// renderFactsWant is each fact the reply above carries, as the exact bytes the
// guest wrote for its value.
var renderFactsWant = []struct{ name, value string }{
	{"zeta_fact", `[{"tier":"web", "app":"api"}]`},
	{"alpha_fact", `["b","a"]`},
	{"mid_fact", `["plain"]`},
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
	if err := e.surfaceRenderFacts(nodeCtx, out.RenderFacts); err != nil {
		t.Fatalf("a report whose every value is a list was refused: %v", err)
	}

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
	if err := e.surfaceRenderFacts(nodeCtx, bare.RenderFacts); err != nil {
		t.Fatalf("a reply reporting nothing was refused: %v", err)
	}
	if called != 0 {
		t.Errorf("a reply reporting nothing called the sink %d times, want 0", called)
	}

	var populated renderer.RenderNodeOutput
	if err := populated.UnmarshalJSON([]byte(renderFactsWire)); err != nil {
		t.Fatalf("decode render output: %v", err)
	}
	// No sink attached: nothing to call, and nothing to panic on.
	if err := e.surfaceRenderFacts(renderer.NodeRenderContext{NodeID: "api"}, populated.RenderFacts); err != nil {
		t.Fatalf("a conforming report with no sink attached was refused: %v", err)
	}
}

// TestSurfaceRenderFacts_RefusesAFactWhoseValueIsNotAList holds the socket's one
// rule about a fact's value: it is a list. Each row hands the forward a report
// through the same decode a guest's reply takes, and a report carrying a value of
// any other kind is refused with the condition's own identity, naming the fact,
// the node and the guest — and nothing of that report is forwarded, not even the
// facts before the one refused, because a report half-forwarded would leave the
// host holding a part the guest never meant as the whole. A refusal does not
// depend on a sink being attached: the report is the guest's whatever reads it.
//
// The accepted rows are the edges of "a list": an empty one, one nested inside
// another, and one written after white space, which is still a list.
func TestSurfaceRenderFacts_RefusesAFactWhoseValueIsNotAList(t *testing.T) {
	t.Parallel()

	const guest = "file:///guests/facts-provider.wasm"
	for _, tc := range []struct {
		name     string
		reply    string
		withSink bool
		// wantFact is the fact the refusal names and wantKind what it says the value
		// was; both empty for a report that must be accepted.
		wantFact, wantKind string
	}{
		{name: "an object", reply: `{"name":"pods","value":{"app":"api"}}`, withSink: true, wantFact: "pods", wantKind: "an object"},
		{name: "a string", reply: `{"name":"pods","value":"api"}`, withSink: true, wantFact: "pods", wantKind: "a string"},
		{name: "a number", reply: `{"name":"pods","value":3}`, withSink: true, wantFact: "pods", wantKind: "a number"},
		{name: "a boolean", reply: `{"name":"pods","value":false}`, withSink: true, wantFact: "pods", wantKind: "a boolean"},
		{name: "null", reply: `{"name":"pods","value":null}`, withSink: true, wantFact: "pods", wantKind: "null"},
		{name: "a list first, then an object", reply: `{"name":"paths","value":["a"]},{"name":"pods","value":{"app":"api"}}`,
			withSink: true, wantFact: "pods", wantKind: "an object"},
		{name: "an object with no sink attached", reply: `{"name":"pods","value":{"app":"api"}}`, withSink: false,
			wantFact: "pods", wantKind: "an object"},
		{name: "control: an empty list", reply: `{"name":"pods","value":[]}`, withSink: true},
		{name: "control: a list of lists", reply: `{"name":"pods","value":[["a"],[]]}`, withSink: true},
		{name: "control: a list after white space", reply: `{"name":"pods","value":` + "\n\t " + `[{"app":"api"}]}`, withSink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out renderer.RenderNodeOutput
			if err := out.UnmarshalJSON([]byte(`{"files":[],"render_facts":[` + tc.reply + `]}`)); err != nil {
				t.Fatalf("decode render output: %v", err)
			}
			called := 0
			nodeCtx := renderer.NodeRenderContext{NodeID: "api", ProviderName: "helm"}
			if tc.withSink {
				nodeCtx.RenderFactSink = func([]renderer.RenderFact) { called++ }
			}
			e := &Engine{pluginURI: guest}

			err := e.surfaceRenderFacts(nodeCtx, out.RenderFacts)
			if tc.wantFact == "" {
				if err != nil {
					t.Fatalf("a report whose every value is a list was refused: %v", err)
				}
				if tc.withSink && called != 1 {
					t.Errorf("the accepted report reached the sink %d times, want 1", called)
				}
				return
			}
			if err == nil {
				t.Fatal("a fact whose value is not a list was forwarded; the socket must refuse it")
			}
			if !errors.Is(err, renderer.ErrProviderRenderFactNotList()) {
				t.Fatalf("the refusal is not ErrProviderRenderFactNotList: %v", err)
			}
			if short, ok := cerrors.ShortCodeOf(err); !ok || short != "E0325" {
				t.Errorf("short code = %q (present %v), want E0325", short, ok)
			}
			for _, want := range []string{`fact "` + tc.wantFact + `"`, `node "api"`, `provider "` + guest + `"`, "its value is " + tc.wantKind} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %s: %v", want, err)
				}
			}
			if called != 0 {
				t.Errorf("a refused report reached the sink %d times, want 0", called)
			}
		})
	}
}
