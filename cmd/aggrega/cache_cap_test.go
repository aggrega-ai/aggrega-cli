// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// seedCapTreeRoot writes a workspace-tree config carrying the given body under
// the workspace root marker and returns the root. The `workspace: {}` marker is
// what anchors the root, so the cascade resolves from this directory rather than
// climbing out of the temp tree.
func seedCapTreeRoot(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	content := "workspace: {}\n" + body
	if err := os.WriteFile(filepath.Join(dir, workspaceconfig.ConfigFilename), []byte(content), 0o644); err != nil {
		t.Fatalf("seed tree config: %v", err)
	}
	return dir
}

// capFromTree resolves a workspace at root through the production wiring and
// returns the compiled-module cache cap the CLI would arm from it.
func capFromTree(t *testing.T, root string) int64 {
	t.Helper()
	ctx := withWorkspaceResolver(context.Background(), root, globalConfigLookup("", frozenEnv(nil)))
	return compiledModuleCacheCap(ctx)
}

// TestCompiledModuleCacheCapDefaultsWithNoDialSet is the guard acceptance of the
// default rests on: a workspace that says nothing about the compiled-module cache
// still yields the shipped cap, so the store is bounded for everyone who
// configures nothing — which is nearly everyone.
//
// It resolves a real workspace through the production path rather than reading the
// default constant back, so a wiring that dropped the block (a missing struct
// field, a mistyped key) would surface here as a zero cap, which means uncapped.
func TestCompiledModuleCacheCapDefaultsWithNoDialSet(t *testing.T) {
	t.Parallel()
	root := seedCapTreeRoot(t, "default_registry: example.invalid\n")

	got := capFromTree(t, root)
	if got != wasm.DefaultCompileCacheCapBytes {
		t.Fatalf("with no cap written the resolved cap is %d, want the default %d; "+
			"a zero here means the compiled-module cache is uncapped", got, wasm.DefaultCompileCacheCapBytes)
	}
	if got != 53_687_091_200 {
		t.Fatalf("the default cap resolved to %d, not the 53687091200 bytes (50 GiB) the configuration surface publishes", got)
	}
}

// TestCompiledModuleCacheCapIsReadFromTheDial proves the setting controls the cap
// rather than merely being accepted. A configuration key with no reader is worse
// than none at all: it advertises control that does not exist, and nothing about
// the file's appearance reveals that it is inert.
//
// The negative control above is what makes this meaningful — it shows the default
// is what appears with no dial written, so a value that differs from the default
// can only have come from the file.
func TestCompiledModuleCacheCapIsReadFromTheDial(t *testing.T) {
	t.Parallel()
	const want int64 = 7_340_032 // 7 MiB: nothing like the default, so a match cannot be a coincidence.
	root := seedCapTreeRoot(t, "compiled_module_cache:\n  max_bytes: 7340032\n")

	got := capFromTree(t, root)
	if got == wasm.DefaultCompileCacheCapBytes {
		t.Fatalf("the resolved cap is still the default %d; the compiled_module_cache.max_bytes setting was accepted and ignored", got)
	}
	if got != want {
		t.Fatalf("resolved cap %d, want the configured %d", got, want)
	}
}

// TestCompiledModuleCacheCapZeroIsUncapped covers the documented escape hatch:
// someone who genuinely wants an unbounded store writes zero and gets one. It is
// asserted here because zero is also what a broken wiring produces, so the two
// must be distinguishable — the reader must carry a written zero through rather
// than substituting the default for it.
func TestCompiledModuleCacheCapZeroIsUncapped(t *testing.T) {
	t.Parallel()
	root := seedCapTreeRoot(t, "compiled_module_cache:\n  max_bytes: 0\n")

	if got := capFromTree(t, root); got != 0 {
		t.Fatalf("an explicit zero resolved to %d; a written zero must be honoured as uncapped", got)
	}
}

// TestCompiledModuleCacheCapFromGlobalLayer asserts the cap resolves from the
// global config file as well as from a workspace tree. That layer is the one the
// setting most belongs in: the compiled-module store is shared by every workspace
// on the machine, so a per-machine answer is the natural one, and it would be a
// defect if the only place the cap could be written were a per-workspace file.
func TestCompiledModuleCacheCapFromGlobalLayer(t *testing.T) {
	t.Parallel()
	const want int64 = 3_145_728 // 3 MiB
	globalPath := filepath.Join(t.TempDir(), "aggrega", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("mkdir global dir: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("compiled_module_cache:\n  max_bytes: 3145728\n"), 0o644); err != nil {
		t.Fatalf("seed global config: %v", err)
	}
	// A tree root that says nothing about the cap, so the only source is the
	// global file named by the config-path override.
	root := seedCapTreeRoot(t, "default_registry: example.invalid\n")

	ctx := withWorkspaceResolver(context.Background(), root, globalConfigLookup(globalPath, frozenEnv(nil)))
	got := compiledModuleCacheCap(ctx)
	if got == wasm.DefaultCompileCacheCapBytes {
		t.Fatalf("the cap resolved to the default %d; the global config layer was not consulted", got)
	}
	if got != want {
		t.Fatalf("resolved cap %d from the global layer, want %d", got, want)
	}
}

// TestCompiledModuleCacheCapUnresolvableWorkspaceStaysCapped asserts the failure
// direction is the safe one: when no workspace resolves there is no configured
// answer to honour, and the cap must fall back to the shipped default rather than
// to zero. Falling back to zero would silently uncap the store in exactly the
// situation nobody is watching.
func TestCompiledModuleCacheCapUnresolvableWorkspaceStaysCapped(t *testing.T) {
	t.Parallel()
	// A context with no workspace resolver attached is the shape a command helper
	// invoked outside the root wiring sees, and the shape resolution failure
	// presents as here.
	if got := compiledModuleCacheCap(context.Background()); got != wasm.DefaultCompileCacheCapBytes {
		t.Fatalf("with no resolvable workspace the cap is %d, want the default %d; "+
			"an unresolvable workspace must not uncap the store", got, wasm.DefaultCompileCacheCapBytes)
	}
}
