// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestLoader_OnLoad_FiresOnConstruction pins the load-observer contract: a
// Loader with a non-nil OnLoad invokes it with the plugin's name each time it
// constructs a guest. The progress surface relies on this to report plugin
// compilation.
func TestLoader_OnLoad_FiresOnConstruction(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	var seen []string
	loader := &Loader{OnLoad: func(name string) { seen = append(seen, name) }}
	ctx := context.Background()

	plugin, err := loader.Load(ctx, PluginSpec{Name: "frp-step-go", Path: pluginPath})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer plugin.Close(ctx)

	if len(seen) != 1 || seen[0] != "frp-step-go" {
		t.Fatalf("OnLoad: got %v, want one call naming %q", seen, "frp-step-go")
	}
}

// TestLoader_NilOnLoad_Safe confirms the zero-value observer is inert: a Loader
// with a nil OnLoad loads a guest without panicking.
func TestLoader_NilOnLoad_Safe(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	loader := &Loader{}
	ctx := context.Background()
	plugin, err := loader.Load(ctx, PluginSpec{Name: "frp-step-go", Path: pluginPath})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	_ = plugin.Close(ctx)
}

// TestPluginCompileCacheCold pins the up-front cold/warm heuristic's two halves.
//
// The disable-persistent-cache signal always reports cold, which is a property of
// the exported function and is asserted through it. The three on-disk states —
// absent, present but empty, populated — are asserted against the predicate the
// function applies, over a directory this test owns. That split is deliberate: a
// test binary resolves the compile-cache directory to one shared root that other
// packages are concurrently reading and writing, so it can be neither started
// empty nor safely emptied, and a test that tried would be asserting on state it
// does not control.
func TestPluginCompileCacheCold(t *testing.T) {
	base := context.Background()

	// Disable-cache signal → always cold, regardless of on-disk state.
	if !PluginCompileCacheCold(ContextWithDisablePluginCache(base, true)) {
		t.Errorf("disable-plugin-cache must report cold")
	}

	dir := filepath.Join(t.TempDir(), "compile-cache")

	// Absent directory → cold.
	if !dirHasNoEntries(dir) {
		t.Errorf("absent cache dir must read as empty (cold)")
	}

	// Empty directory → cold.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if !dirHasNoEntries(dir) {
		t.Errorf("empty cache dir must read as empty (cold)")
	}

	// Populated directory → warm.
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write cache entry: %v", err)
	}
	if dirHasNoEntries(dir) {
		t.Errorf("populated cache dir must not read as empty (warm)")
	}
}
