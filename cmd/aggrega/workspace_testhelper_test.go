// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// resolveTempWorkspace resolves an isolated workspace rooted at a fresh temp
// directory that carries a `workspace: {}` marker, and returns it for tests
// that would otherwise pass a nil workspace to a command helper.
//
// A nil workspace routes through the resolve-from-current-directory fallback,
// which walks the current directory upward looking for the nearest config that
// anchors a root. When the repository checkout lives under a developer's home
// directory and that home holds a `.aggrega-config.yaml` with a `workspace:`
// block, the walk reaches it and adopts its catalog directories — a developer's
// ambient configuration would then leak into the test and resolve catalog
// paths that do not exist on the machine. Seeding `workspace: {}` at the temp
// directory and resolving from there anchors the root at the temp directory on
// the very first step of discovery, so resolution never climbs to any ancestor.
// The result is hermetic against whatever configuration happens to sit above
// the checkout.
//
// This is the single home for hermetic test-workspace construction; tests use
// it in place of nil rather than open-coding an isolated resolve. It must not
// be "simplified" back to nil — the isolation is the point.
func resolveTempWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rw
}
