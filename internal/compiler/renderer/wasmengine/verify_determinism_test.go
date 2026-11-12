// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmengine_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// testdataFixtureLookup builds the named testdata render fixture on demand and
// returns a lookup resolving its bytes plus the URI to address it by. A missing
// toolchain skips the test cleanly.
func testdataFixtureLookup(t *testing.T, artifactName, uri string) (pluginpath.Lookup, string) {
	t.Helper()
	path := wasmtest.EnsureFixtureBuilt(t, artifactName)
	guestBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read render fixture %q: %v", path, err)
	}
	lookup := pluginpath.Lookup(func(string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{
			Bytes:  guestBytes,
			URI:    uri,
			Tier:   pluginpath.TierEmbed,
			Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096},
		}, nil
	})
	return lookup, uri
}

// TestVerifyDeterminismHaltsOnNonDeterministicGuest proves the verify-determinism
// build mode halts, end-to-end through a real guest, when a provider renders a
// node twice from identical input and produces different output. It drives a
// deliberately non-deterministic render fixture — one whose output carries a
// per-instance invocation counter — through a render crossing with the
// verify-determinism dial set on the context. The second render diverges from the
// first, so the crossing halts with ErrRenderNondeterministic naming the node.
// Without the dial the same guest renders once and the build would complete,
// emitting non-deterministic output undetected — which is exactly what this mode
// exists to catch.
func TestVerifyDeterminismHaltsOnNonDeterministicGuest(t *testing.T) {
	lookup, uri := testdataFixtureLookup(t, "render-node-nondeterministic-rust.wasm", "oci://testdata/render-node-nondeterministic-rust")
	loader := wasm.NewCachingLoader(nil)
	e := wasmengine.New(lookup, uri, loader)

	ctx := wasm.ContextWithVerifyDeterminism(context.Background(), true)
	_, err := e.SynthesizeWorkload(ctx, nil, minimalSynthWorkload(), synthNodeCtx("nd-node"))
	if err == nil {
		t.Fatal("verify-determinism render of a non-deterministic guest returned nil; expected a halt")
	}
	if !errors.Is(err, renderer.ErrRenderNondeterministic()) {
		t.Fatalf("expected ErrRenderNondeterministic in chain; got %v", err)
	}
}

// TestVerifyDeterminismPassesForDeterministicGuest is the companion: the
// verify-determinism dial does not halt a deterministic guest. The embedded helm
// guest renders identical input to byte-identical output, so its second render
// matches the first and the crossing completes. This pins that the mode's
// render-twice comparison is a true equality check, not an unconditional halt.
func TestVerifyDeterminismPassesForDeterministicGuest(t *testing.T) {
	helmURI := embeddedGuestURI(t, "helm")
	loader := wasm.NewCachingLoader(nil)
	e := wasmengine.New(embeddedGuestLookup, helmURI, loader)

	ctx := wasm.ContextWithVerifyDeterminism(context.Background(), true)
	if _, err := e.SynthesizeWorkload(ctx, nil, minimalSynthWorkload(), synthNodeCtx("det-node")); err != nil {
		t.Fatalf("verify-determinism render of the deterministic helm guest halted: %v", err)
	}
}
