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

// TestPluginCompileCacheCold pins the up-front cold/warm heuristic:
//   - the disable-persistent-cache signal always reports cold;
//   - an absent or empty persistent cache directory reports cold;
//   - a populated persistent cache directory reports warm.
func TestPluginCompileCacheCold(t *testing.T) {
	// Point the user cache dir at a controlled, initially-empty temp dir so the
	// heuristic reads a known state rather than the developer's real cache.
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	// os.UserCacheDir on non-Linux may consult other vars; the test asserts
	// behaviour relative to whatever wasmCompileCacheDir resolves to.
	dir, ok := wasmCompileCacheDir()
	if !ok {
		t.Skip("no resolvable user cache directory in this environment")
	}

	base := context.Background()

	// Disable-cache signal → always cold, regardless of on-disk state.
	if !PluginCompileCacheCold(ContextWithDisablePluginCache(base, true)) {
		t.Errorf("disable-plugin-cache must report cold")
	}

	// Absent directory → cold.
	if !PluginCompileCacheCold(base) {
		t.Errorf("absent cache dir must report cold")
	}

	// Empty directory → cold.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	if !PluginCompileCacheCold(base) {
		t.Errorf("empty cache dir must report cold")
	}

	// Populated directory → warm.
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write cache entry: %v", err)
	}
	if PluginCompileCacheCold(base) {
		t.Errorf("populated cache dir must report warm")
	}
}
