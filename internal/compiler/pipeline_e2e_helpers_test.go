// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compiler_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// stageColocatedCatalog copies the given catalog directories into a fresh temp
// tree, builds each fixture .wasm, and lays it down CO-LOCATED beside its Plugin
// entity's plugin.yaml — the on-disk shape the catalog indexer maps into
// PluginColocatedDirs, so the build's PRODUCTION entity-front resolves entity URI
// → spec.plugin → co-located tier end-to-end with no hand-rolled test resolver.
// It returns the temp catalog directories to pass as pipelinetest.Input.CatalogPaths.
//
// The map keys are the artifact references the Plugin entities resolve to (each
// entity's `<pluginType>/<name>` coordinate — the same reference the cascade
// resolves by); the values are the wasmtest fixture artifact names. A key with no
// indexed Plugin entity is a test-authoring fault and fails loudly. When the wasm
// toolchain is absent, the underlying fixture build skips the calling test.
func stageColocatedCatalog(t *testing.T, catalogPaths []string, artifactToFixture map[string]string) []string {
	t.Helper()
	root := t.TempDir()

	staged := make([]string, len(catalogPaths))
	for i, src := range catalogPaths {
		dst := filepath.Join(root, filepath.FromSlash(src))
		copyDirTree(t, src, dst)
		staged[i] = dst
	}

	// Load the copied catalog through the real indexer so the co-located dirs come
	// from the same PluginColocatedDirs derivation the build uses — keyed by each
	// Plugin entity's artifact reference, valued at the directory its plugin.yaml
	// was loaded from.
	cat, err := catalog.LoadAll("", staged...)
	if err != nil {
		t.Fatalf("stageColocatedCatalog: load copied catalog: %v", err)
	}
	dirs := cat.PluginColocatedDirs()

	for artifactRef, fixtureName := range artifactToFixture {
		dir, ok := dirs[artifactRef]
		if !ok {
			t.Fatalf("stageColocatedCatalog: no indexed Plugin entity for artifact reference %q (have %v)", artifactRef, dirs)
		}
		bytes := wasmtest.EnsureFixtureBytes(t, fixtureName)
		leaf := pluginpath.PluginLeaf(artifactRef)
		if err := os.WriteFile(filepath.Join(dir.Dir, leaf), bytes, 0o644); err != nil {
			t.Fatalf("stageColocatedCatalog: stage %q as %q beside its entity: %v", fixtureName, leaf, err)
		}
	}
	return staged
}

// copyDirTree recursively copies the directory tree at src into dst.
func copyDirTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyDirTree %s -> %s: %v", src, dst, err)
	}
}
