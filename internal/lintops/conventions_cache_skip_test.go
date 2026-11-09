// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestWalkCatalogEntityFiles_SkipsArtifactCache pins the conventions walk to the
// contract RunRelease's documentation states: an entity resolved from a
// cache slot carries no local file to lint and is skipped,
// because the publishing rules check author-time conventions that only apply to
// a file the author owns. A cached slot is a resolution artifact — reporting a
// convention finding against it hands the author a path they cannot edit and a
// finding they cannot fix.
//
// The layout is the mainstream one: the artifact cache sits under the workspace
// root, and a catalog directory defaults to the workspace root, so the cache is
// inside the walked tree with no flag set.
//
// Non-vacuity canary: the SAME tree walked with no cache directory configured
// DOES return the cached file, so the assertion is earned by the skip rather
// than green because the fixture failed to parse.
func TestWalkCatalogEntityFiles_SkipsArtifactCache(t *testing.T) {
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"

	authored := filepath.Join(ws, "authored-component.yaml")
	mustWriteFile(t, authored, `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/authored-app
  version: "1.0.0"
spec:
  inject: ["env"]
`)

	cacheSlot, err := oci.CachePath(ws, cacheDir, "Component", "core/cached-comp:1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("CachePath: %v", err)
	}
	if err := os.MkdirAll(cacheSlot, 0o755); err != nil {
		t.Fatalf("mkdir cache slot: %v", err)
	}
	cached := filepath.Join(cacheSlot, "library.yaml")
	mustWriteFile(t, cached, `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`)

	files, _, err := walkCatalogEntityFiles(t.Context(), ws, []string{ws}, nil, cacheDir)
	if err != nil {
		t.Fatalf("walkCatalogEntityFiles: %v", err)
	}
	var sawAuthored bool
	for _, f := range files {
		if underCacheDir(f.Path, cacheDir) {
			t.Errorf("the conventions walk returned %s, a resolution artifact in the cache; a convention finding there names a path the author cannot edit", f.Path)
		}
		if f.Path == authored {
			sawAuthored = true
		}
	}
	if !sawAuthored {
		t.Error("the authored entity was dropped; the cache skip over-reached")
	}

	// --- Non-vacuity canary: same tree, no cache directory configured. ---
	canary, _, err := walkCatalogEntityFiles(t.Context(), ws, []string{ws}, nil, "")
	if err != nil {
		t.Fatalf("canary walkCatalogEntityFiles: %v", err)
	}
	var canarySawCached bool
	for _, f := range canary {
		if f.Path == cached {
			canarySawCached = true
		}
	}
	if !canarySawCached {
		t.Error("canary failed: an unconfigured-cache walk did NOT return the cached file, so the skip assertion is vacuous")
	}
}

func underCacheDir(path, cacheDir string) bool {
	return strings.Contains(filepath.ToSlash(path), "/"+filepath.ToSlash(cacheDir)+"/")
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
