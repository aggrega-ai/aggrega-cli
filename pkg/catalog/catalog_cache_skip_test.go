// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestLoadWithCacheDirSkipsCachedLibraries pins the cache-walk skip: a cached
// `library.yaml` under the configured cache directory is a resolution
// artifact, not authored catalog, so LoadWithCacheDir must NOT ingest it as a
// Tier-1 SchemaLibrary — while an authored library elsewhere in the same tree
// is still loaded. The non-vacuous canary: plain Load (cacheDir="") DOES ingest
// the cached library, proving the skip is the cause and not, say, an
// unparseable fixture.
func TestLoadWithCacheDirSkipsCachedLibraries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const cacheDir = ".aggrega/cache"

	mustWrite(t, filepath.Join(dir, "authored.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Authored
  version: 1.0.0
spec:
  schemas:
    Foo: {}
`)
	// A cached library, under <dir>/.aggrega/cache/ (the resolution-artifact
	// store) — the layout a prior `aggrega pull` would have written.
	cacheSlot := filepath.Join(dir, cacheDir, "entities", "enc", "1.0.0")
	if err := os.MkdirAll(cacheSlot, 0o755); err != nil {
		t.Fatalf("mkdir cache slot: %v", err)
	}
	mustWrite(t, filepath.Join(cacheSlot, "library.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Cached
  version: 1.0.0
spec:
  schemas:
    Bar: {}
`)

	// Skip path: the cached library is NOT ingested; the authored one is.
	skipped, err := catalog.LoadAllWithCacheDir(dir, cacheDir, dir)
	if err != nil {
		t.Fatalf("LoadWithCacheDir: %v", err)
	}
	if _, ok := skipped.SchemaLibraries["Cached"]; ok {
		t.Error("LoadWithCacheDir ingested the cached library at Tier-1; the cache walk-skip failed")
	}
	if _, ok := skipped.SchemaLibraries["Authored"]; !ok {
		t.Error("LoadWithCacheDir dropped the authored library; the skip over-reached")
	}

	// Non-vacuous canary: plain Load (no cache skip) DOES ingest the cached
	// library — so the assertion above is earned, not green because the
	// fixture was unloadable.
	unskipped, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := unskipped.SchemaLibraries["Cached"]; !ok {
		t.Error("canary failed: Load did not ingest the cached library, so the skip assertion is vacuous")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
