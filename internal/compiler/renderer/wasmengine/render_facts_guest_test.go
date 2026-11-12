// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmengine_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// kitGuestFacts is what the plugin kit's render example reports for a node: the
// paths it emitted, then the node it echoed — a fixed order that is deliberately
// not name order, every value a list, with the echoed entry's keys out of key
// order too. Each value is the exact bytes the guest writes.
func kitGuestFacts(nodeID string) []renderer.RenderFact {
	return []renderer.RenderFact{
		{Name: "emitted_paths", Value: []byte(`["parity.txt","attachments.txt"]`)},
		{Name: "echoed_node", Value: []byte(`[{"prefix":"rendered-node:","id":"` + nodeID + `"}]`)},
	}
}

// crossing is one way a provider guest renders a node: from a source, from a
// workload, or by placing an artifact set. The host forwards a guest's facts at
// each of them, so each is driven separately.
type crossing struct {
	name   string
	render func(e *wasmengine.Engine, nodeCtx renderer.NodeRenderContext) ([]string, error)
}

func crossings(t *testing.T) []crossing {
	t.Helper()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "values.yaml"), []byte("replicas: 1\n"), 0o644); err != nil {
		t.Fatalf("write fixture source: %v", err)
	}
	return []crossing{
		{name: "source render", render: func(e *wasmengine.Engine, nodeCtx renderer.NodeRenderContext) ([]string, error) {
			nodeCtx.Source = source
			files, err := e.Render(context.Background(), map[string]any{}, nodeCtx)
			out := make([]string, 0, len(files))
			for _, f := range files {
				out = append(out, f.Path+"="+string(f.Content))
			}
			return out, err
		}},
		{name: "workload synthesis", render: func(e *wasmengine.Engine, nodeCtx renderer.NodeRenderContext) ([]string, error) {
			files, err := e.SynthesizeWorkload(context.Background(), nil, minimalSynthWorkload(), nodeCtx)
			return emittedBodies(files), err
		}},
		{name: "artifacts placement", render: func(e *wasmengine.Engine, nodeCtx renderer.NodeRenderContext) ([]string, error) {
			proj := renderer.ArtifactsProjection{Entries: []renderer.ArtifactProjectionEntry{
				{Name: "settings.yaml", Origin: "inline", Content: []byte("enabled: true\n")},
			}}
			files, err := e.EmitArtifacts(context.Background(), proj, nodeCtx)
			return emittedBodies(files), err
		}},
	}
}

func emittedBodies(files []renderer.EmittedFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name+"="+string(f.Body))
	}
	return out
}

// TestRenderFacts_AKitGuestsReportReachesTheNodeSinkAtEveryCrossing is the
// forward proved through the real wire: a guest built from the plugin kit's
// render example reports facts beside its files, and at each of the three
// crossings the node's sink receives them once, in the guest's order, every value
// byte for byte as the guest wrote it. The files written are the same whether a
// sink is attached or not, because reporting facts never changes what is written.
func TestRenderFacts_AKitGuestsReportReachesTheNodeSinkAtEveryCrossing(t *testing.T) {
	lookup, uri := nonReusableGuestLookup(t)
	e := wasmengine.New(lookup, uri, wasm.NewCachingLoader(nil))

	for _, c := range crossings(t) {
		t.Run(c.name, func(t *testing.T) {
			const nodeID = "facts-node"

			silent, err := c.render(e, synthNodeCtx(nodeID))
			if err != nil {
				t.Fatalf("render with no sink attached: %v", err)
			}

			var got [][]renderer.RenderFact
			nodeCtx := synthNodeCtx(nodeID)
			nodeCtx.RenderFactSink = func(f []renderer.RenderFact) { got = append(got, f) }
			reported, err := c.render(e, nodeCtx)
			if err != nil {
				t.Fatalf("render with a sink attached: %v", err)
			}

			if len(got) != 1 {
				t.Fatalf("render-fact sink called %d times at one crossing, want 1", len(got))
			}
			want := kitGuestFacts(nodeID)
			if len(got[0]) != len(want) {
				t.Fatalf("the sink received %d facts, want %d: %+v", len(got[0]), len(want), got[0])
			}
			for i := range want {
				if got[0][i].Name != want[i].Name {
					t.Errorf("fact %d is %q, want %q: the guest's order did not survive the crossing", i, got[0][i].Name, want[i].Name)
				}
				if string(got[0][i].Value) != string(want[i].Value) {
					t.Errorf("fact %q value = %s, want the guest's own bytes %s", want[i].Name, got[0][i].Value, want[i].Value)
				}
			}
			if !reflect.DeepEqual(silent, reported) {
				t.Errorf("the files written moved when a sink was attached:\n without: %v\n    with: %v", silent, reported)
			}
		})
	}
}

// TestRenderFacts_AGuestReportingNothingLeavesTheSinkUncalled is the other
// direction, through a guest that reports no facts: the embedded compose guest.
// Its sink is never called and the files it writes are the same with the sink
// attached as without it, so a provider that reports nothing renders exactly as
// it did.
func TestRenderFacts_AGuestReportingNothingLeavesTheSinkUncalled(t *testing.T) {
	composeURI := embeddedGuestURI(t, "compose")
	e := wasmengine.New(embeddedGuestLookup, composeURI, wasm.NewCachingLoader(nil))
	if !e.Contract().Synthesized {
		t.Fatal("the compose guest no longer declares the synthesized input kind, so this test drives no render")
	}

	without, err := e.SynthesizeWorkload(context.Background(), nil, minimalSynthWorkload(), synthNodeCtx("quiet-node"))
	if err != nil {
		t.Fatalf("render with no sink attached: %v", err)
	}

	called := 0
	nodeCtx := synthNodeCtx("quiet-node")
	nodeCtx.RenderFactSink = func([]renderer.RenderFact) { called++ }
	with, err := e.SynthesizeWorkload(context.Background(), nil, minimalSynthWorkload(), nodeCtx)
	if err != nil {
		t.Fatalf("render with a sink attached: %v", err)
	}
	if called != 0 {
		t.Errorf("a guest reporting no facts called the sink %d times, want 0", called)
	}
	if len(with) == 0 {
		t.Fatal("the compose guest emitted nothing, so the comparison below would hold over nothing")
	}
	if !reflect.DeepEqual(emittedBodies(without), emittedBodies(with)) {
		t.Errorf("the files written moved when a sink was attached:\n without: %v\n    with: %v",
			emittedBodies(without), emittedBodies(with))
	}
}
