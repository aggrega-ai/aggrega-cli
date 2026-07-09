// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The cross-boundary link pass wires a uses edge whose ends assemble into
// different dispatch groups through a provider's link_publish / link_resolve
// exports. These tests drive the unexported pass with REAL wasm engines
// wrapping a compiled test guest, so the actual guest exports run rather than a
// Go stand-in. The guest (aggrega-pdk/rust/examples/link-cross-boundary):
//
//   - link_publish derives a deterministic pointer string from the edge,
//     returns it as the published handle, and returns one add op recording it
//     on the consumed (target) endpoint's "boundary.yaml".
//   - link_resolve reads the handle and returns one add op injecting the
//     pointer into the consuming (source) endpoint's "boundary.yaml".
//
// So the pointer flows publish → handle → resolve, and the pass applies the
// producer-side wiring to the target group and the consumer-side wiring to the
// source group.

const (
	linkFixture      = "link-cross-boundary-rust.wasm"
	renderFixture    = "render-node-rust.wasm"
	linkProvider     = "link-cross-boundary"
	otherProvider    = "render-only"
	linkFile         = "boundary.yaml"
	linkSocket       = "db"
	linkFileInitYAML = "kind: boundary\n"
)

// fixtureEngine builds a real wasmengine engine over a compiled test guest,
// resolved from its on-disk fixture bytes. It skips the test when the wasm
// toolchain is absent (EnsureFixtureBuilt marks that case).
func fixtureEngine(t *testing.T, loader *wasm.Loader, fixture string) renderer.Engine {
	t.Helper()
	path := wasmtest.EnsureFixtureBuilt(t, fixture)
	guestBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	uri := "file://" + path
	lookup := func(u string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{
			Bytes:  guestBytes,
			URI:    u,
			Tier:   pluginpath.TierLocal,
			Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096},
		}, nil
	}
	return wasmengine.New(lookup, uri, loader)
}

// linkEdge is the single cross-boundary uses edge every case wires: consumer
// (source) uses producer (target) on the "db" socket.
func linkEdge() types.IREdge {
	return types.IREdge{Source: "consumer", Target: "producer", Socket: linkSocket}
}

// TestCrossBoundaryLink_SameProviderWires proves a same-provider cross-boundary
// edge is wired end-to-end through the guest's link exports: the publish patch
// lands on the target group and the resolve patch lands on the source group,
// carrying the pointer the publish half derived.
func TestCrossBoundaryLink_SameProviderWires(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	engine := fixtureEngine(t, loader, linkFixture)
	factory := func(string) (renderer.Engine, error) { return engine, nil }

	nodes := map[string]renderer.LinkPassNode{
		"consumer": {Path: "app", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		"producer": {Path: "data", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
	}

	warnings, filesByPath, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
	if err != nil {
		t.Fatalf("link pass: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("same-provider wire must not warn, got %v", warnings)
	}

	// The pointer the guest derives from the edge: target node id + socket.
	const wantPointer = "producer--" + linkSocket

	target := string(filesByPath["data"][linkFile])
	source := string(filesByPath["app"][linkFile])

	// Producer-side wiring landed on the target group only.
	if !strings.Contains(target, "published") || !strings.Contains(target, wantPointer) {
		t.Errorf("target file missing publish wiring %q:\n%s", wantPointer, target)
	}
	if strings.Contains(target, "resolved") {
		t.Errorf("consumer-side wiring leaked into the target (producer) file:\n%s", target)
	}
	// Consumer-side wiring landed on the source group only, carrying the
	// pointer the publish half derived (proving the handle round-trip).
	if !strings.Contains(source, "resolved") || !strings.Contains(source, wantPointer) {
		t.Errorf("source file missing resolve wiring %q:\n%s", wantPointer, source)
	}
	if strings.Contains(source, "published") {
		t.Errorf("producer-side wiring leaked into the source (consumer) file:\n%s", source)
	}
	// Byte-faithful: the pre-existing content survives verbatim in both files.
	if !strings.Contains(target, "kind: boundary") || !strings.Contains(source, "kind: boundary") {
		t.Errorf("pre-existing content not preserved:\ntarget=%s\nsource=%s", target, source)
	}
}

// TestCrossBoundaryLink_Deterministic proves the pass produces byte-identical
// patched files across two runs of identical input.
func TestCrossBoundaryLink_Deterministic(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	engine := fixtureEngine(t, loader, linkFixture)
	factory := func(string) (renderer.Engine, error) { return engine, nil }

	run := func() map[string]map[string][]byte {
		nodes := map[string]renderer.LinkPassNode{
			"consumer": {Path: "app", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
			"producer": {Path: "data", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		}
		_, filesByPath, err := renderer.RunCrossBoundaryLinkPassForTest(
			context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
		if err != nil {
			t.Fatalf("link pass: %v", err)
		}
		return filesByPath
	}

	first := run()
	second := run()
	for _, path := range []string{"app", "data"} {
		if !bytes.Equal(first[path][linkFile], second[path][linkFile]) {
			t.Errorf("non-deterministic output for path %q:\nfirst=%s\nsecond=%s",
				path, first[path][linkFile], second[path][linkFile])
		}
	}
}

// TestCrossBoundaryLink_CrossProviderUnsupported proves a cross-boundary edge
// whose ends are rendered by different providers falls back to the scalar
// value-pipe and surfaces a loud warning wrapping ErrCrossProviderLinkUnsupported,
// naming both providers and the socket, while no assembled file changes.
func TestCrossBoundaryLink_CrossProviderUnsupported(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	linkEng := fixtureEngine(t, loader, linkFixture)
	// The target end is a genuinely different, non-linking provider, so only
	// the source advertises linking — the "at least one advertises" gate.
	otherEng := fixtureEngine(t, loader, renderFixture)
	factory := func(p string) (renderer.Engine, error) {
		if p == otherProvider {
			return otherEng, nil
		}
		return linkEng, nil
	}

	nodes := map[string]renderer.LinkPassNode{
		"consumer": {Path: "app", Provider: linkProvider, Engine: linkEng, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		"producer": {Path: "data", Provider: otherProvider, Engine: otherEng, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
	}

	warnings, filesByPath, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
	if err != nil {
		t.Fatalf("cross-provider must warn, not error: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("want exactly one warning, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	// The warning carries the sentinel message and names both providers + the socket.
	if !strings.Contains(w, renderer.ErrCrossProviderLinkUnsupported.Error()) {
		t.Errorf("warning missing sentinel message: %q", w)
	}
	for _, want := range []string{linkProvider, otherProvider, linkSocket} {
		if !strings.Contains(w, want) {
			t.Errorf("warning does not name %q: %q", want, w)
		}
	}
	// No assembled file changed: the scalar value-pipe stands.
	if got := string(filesByPath["app"][linkFile]); got != linkFileInitYAML {
		t.Errorf("source file changed on unsupported cross-provider edge:\n%s", got)
	}
	if got := string(filesByPath["data"][linkFile]); got != linkFileInitYAML {
		t.Errorf("target file changed on unsupported cross-provider edge:\n%s", got)
	}
}

// TestCrossBoundaryLink_OpaqueTargetHalts proves a publish patch whose target
// assembled file is a bare scalar (an opaque unit the codec cannot address)
// halts the pass loudly with ErrPipelineTargetOpaque.
func TestCrossBoundaryLink_OpaqueTargetHalts(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	engine := fixtureEngine(t, loader, linkFixture)
	factory := func(string) (renderer.Engine, error) { return engine, nil }

	nodes := map[string]renderer.LinkPassNode{
		"consumer": {Path: "app", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		// The target's assembled file is a bare scalar: no object tree-view,
		// so the guest's publish patch cannot address it.
		"producer": {Path: "data", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte("just a bare scalar line\n")}},
	}

	_, _, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
	if !errors.Is(err, frp.ErrPipelineTargetOpaque) {
		t.Fatalf("want ErrPipelineTargetOpaque, got %v", err)
	}
}
