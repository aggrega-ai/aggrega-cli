// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// stagePluginSidecar builds each fixture .wasm and stages it into a fresh temp
// sidecar directory, keyed by pluginpath.SidecarLeaf of the WasmPlugin entity's
// spec.plugin artifact reference. It returns the directory to set on
// pipelinetest.Input.PluginSidecarDir, so the build's PRODUCTION entity-front
// resolves entity URI → spec.plugin → cascade (sidecar tier) end-to-end — no
// hand-rolled test resolver. The map keys are the artifact references the
// WasmPlugin entities declare; the values are the wasmtest fixture artifact names.
// When the wasm toolchain is absent, the underlying build skips the calling test.
func stagePluginSidecar(t *testing.T, artifactToFixture map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for artifactURI, fixtureName := range artifactToFixture {
		bytes := wasmtest.EnsureFixtureBytes(t, fixtureName)
		leaf := pluginpath.SidecarLeaf(artifactURI)
		if err := os.WriteFile(filepath.Join(dir, leaf), bytes, 0o644); err != nil {
			t.Fatalf("stage sidecar plugin %q as %q: %v", fixtureName, leaf, err)
		}
	}
	return dir
}
