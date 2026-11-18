// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package embeddedentities holds the test guard for the generated Plugin
// entities of the core plugins the binary embeds.
package embeddedentities

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/siblingrepo"
	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
)

// entityDir is the directory, relative to the module root, the generated
// Plugin entities of the embedded core plugins are written to and embedded from.
const entityDir = "internal/wasm/embedded/plugins"

// Require skips t when this checkout holds no generated Plugin entity for the
// core plugins the binary embeds and aggrega-hub is not checked out beside this
// repository. The entities are generated from the plugin overlays aggrega-hub
// carries, by scripts/gen-embedded-plugin-entities.sh, so a checkout without
// aggrega-hub builds a binary embedding the core plugins' guests but none of
// their entities, and a test reading what the binary resolves through them has
// nothing to read.
//
// Only the absence of aggrega-hub skips. With aggrega-hub checked out and no
// entity generated, t fails, naming the script that generates them.
//
// It reads the module root from the working directory the test binary started
// in, so a test may call it after changing its working directory.
func Require(t testing.TB) {
	t.Helper()
	if startModuleRootErr != nil {
		t.Fatalf("resolve this module's root from the directory the tests started in: %v", startModuleRootErr)
	}
	require(t, startModuleRoot)
}

// startModuleRoot is the root of the module holding the package under test,
// resolved from the working directory the test binary started in, before any
// test could change it.
var startModuleRoot, startModuleRootErr = moduleRootAtStart()

func moduleRootAtStart() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, _, err := projectionparity.ModuleRoot(cwd)
	return root, err
}

// require is [Require] for the module at moduleRoot.
func require(t testing.TB, moduleRoot string) {
	t.Helper()
	entities, err := filepath.Glob(filepath.Join(moduleRoot, entityDir, "*.plugin.yaml"))
	if err != nil {
		t.Fatalf("list the generated embedded plugin entities: %v", err)
	}
	if len(entities) > 0 {
		return
	}
	dir, err := siblingrepo.Dir(moduleRoot, "aggrega-hub")
	switch {
	case siblingrepo.IsAbsent(err):
		t.Skipf("this checkout holds no generated plugin entity under %s, and aggrega-hub, whose plugin "+
			"overlays they are generated from, is not checked out beside this repository (%v); this test reads "+
			"what the binary resolves through those entities, so check out aggrega-hub beside this repository and "+
			"run scripts/gen-embedded-plugin-entities.sh to run it", entityDir, err)
	case err != nil:
		t.Fatalf("aggrega-hub is beside this repository at %s but cannot be read as a checkout: %v", dir, err)
	default:
		t.Fatalf("this checkout holds no generated plugin entity under %s, though aggrega-hub is checked out "+
			"beside it: generate them with scripts/gen-embedded-plugin-entities.sh, then rebuild", entityDir)
	}
}
