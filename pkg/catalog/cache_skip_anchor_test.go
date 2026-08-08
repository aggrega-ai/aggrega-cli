// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// nestedCatalogSubdir is the catalog directory in the layouts below. It sits one
// level BELOW the workspace root, which is the shape that separates the two
// possible anchors: the cache setting is written against the workspace root,
// while the walk starts here. A comparison anchored on the walked directory
// agrees with one anchored on the workspace root only when the two coincide, so
// every fixture in this file deliberately makes them differ.
const nestedCatalogSubdir = "sub"

// nestedCacheDir is `artifact_cache_dir` as an author would write it for that
// layout: relative to the workspace root, naming a cache that happens to live
// inside the catalog directory.
const nestedCacheDir = "sub/.cache"

// nestedAuthoredComponentYAML is the authored entity that must survive the skip.
// It carries the type label the whole-catalog load requires, so a failure in
// these tests reports on the cache anchor rather than on a rejected fixture.
const nestedAuthoredComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/authored-app
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  inject: ["env"]
`

// TestEagerLoad_SkipsArtifactCacheBelowWorkspaceRoot is the nested-layout clause.
// The workspace root is one level above the catalog directory, so the cache path
// as configured ("sub/.cache") and the cache path as the walk sees it (".cache")
// are different strings for one directory. The walk must still step over it: a
// cache slot holds resolution artifacts, and whether the author placed their
// catalog at the workspace root or one level down changes nothing about that.
//
// Non-vacuity canary: the SAME tree, loaded with no cache directory configured,
// DOES ingest the cached library — so the assertion is earned by the skip rather
// than green because the fixture failed to parse.
func TestEagerLoad_SkipsArtifactCacheBelowWorkspaceRoot(t *testing.T) {
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, nestedCatalogSubdir)
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir catalog dir: %v", err)
	}

	writeFixture(t, catalogDir, "authored-component.yaml", nestedAuthoredComponentYAML)
	writeCachedLibrary(t, ws, nestedCacheDir, "DbSizing:1.9.0", staleLibYAML)

	cat, err := LoadPassOne(ws, nestedCacheDir, catalogDir)
	if err != nil {
		t.Fatalf("LoadPassOne: %v", err)
	}
	if _, ok := cat.SchemaLibraries["DbSizing"]; ok {
		t.Error("the catalog load ingested a cached SchemaLibrary as an authored entity; the cache skip did not fire for a cache below the workspace root")
	}
	if _, ok := cat.Components["core/authored-app:1.0.0"]; !ok {
		t.Error("the authored Component was dropped; the cache skip over-reached")
	}

	// --- Non-vacuity canary: same tree, no cache directory configured. ---
	canary, err := LoadPassOne(ws, "", catalogDir)
	if err != nil {
		t.Fatalf("canary LoadPassOne: %v", err)
	}
	if _, ok := canary.SchemaLibraries["DbSizing"]; !ok {
		t.Error("canary failed: an unconfigured-cache load did NOT ingest the cached SchemaLibrary, so the skip assertion is vacuous")
	}
}

// TestLazyWalks_SkipArtifactCacheBelowWorkspaceRoot is the same nested layout on
// the lazy walks, which enumerate the catalog directories independently of the
// eager load. Both strategies read the same setting against the same anchor, so
// neither may resolve the cache to a different directory than the other.
func TestLazyWalks_SkipArtifactCacheBelowWorkspaceRoot(t *testing.T) {
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, nestedCatalogSubdir)
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir catalog dir: %v", err)
	}

	writeFixture(t, catalogDir, "authored-component.yaml", nestedAuthoredComponentYAML)
	writeCachedLibrary(t, ws, nestedCacheDir, "DbSizing:1.9.0", staleLibYAML)
	writeCachedLibrary(t, ws, nestedCacheDir, "BaseMappings:1.0.0", cachedMappingLibYAML)

	subject := NewLazyLookup(ws, nestedCacheDir, []string{catalogDir}, nil)
	subject.buildRecipeIndex()
	if err := subject.buildSchemaLibraries(); err != nil {
		t.Fatalf("buildSchemaLibraries: %v", err)
	}
	subject.buildComponentIndex()

	if _, ok := subject.schemaLibraries["DbSizing"]; ok {
		t.Error("buildSchemaLibraries ingested a cached SchemaLibrary from a cache below the workspace root")
	}
	if _, ok := subject.mappingLibraries["BaseMappings"]; ok {
		t.Error("buildRecipeIndex ingested a cached MappingLibrary from a cache below the workspace root")
	}
	if _, ok := subject.componentIndex["core/authored-app"]; !ok {
		t.Error("the authored Component was dropped; the cache skip over-reached")
	}

	// --- Non-vacuity canary: same tree, no cache directory configured. ---
	canary := NewLazyLookup(ws, "", []string{catalogDir}, nil)
	if err := canary.buildSchemaLibraries(); err != nil {
		t.Fatalf("canary buildSchemaLibraries: %v", err)
	}
	if _, ok := canary.schemaLibraries["DbSizing"]; !ok {
		t.Error("canary failed: an unconfigured-cache walk did NOT ingest the cached SchemaLibrary, so the skip assertion is vacuous")
	}
}

// TestSkipDirIfArtifactCache_Anchors pins the decision function itself across the
// anchor combinations the walks actually produce: the walked directory may be the
// workspace root, below it, or beside it, and it arrives absolute from a resolved
// catalog directory or relative from a caller that walks the working directory.
// The one directory the setting names must be recognised in every one of them,
// and no other directory may be.
func TestSkipDirIfArtifactCache_Anchors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		wsRoot    string
		path      string
		cacheDir  string
		wantSkip  bool
		rationale string
	}{
		{
			name:      "cache at the workspace root, walked from the workspace root",
			wsRoot:    "/ws",
			path:      "/ws/.aggrega/cache",
			cacheDir:  ".aggrega/cache",
			wantSkip:  true,
			rationale: "the mainstream layout must keep working",
		},
		{
			name:      "cache below the workspace root, walked from the catalog directory",
			wsRoot:    "/ws",
			path:      "/ws/sub/.cache",
			cacheDir:  "sub/.cache",
			wantSkip:  true,
			rationale: "the walk starts below the anchor the setting is written against",
		},
		{
			name:      "cache beside the walked directory",
			wsRoot:    "/ws",
			path:      "/ws/other/.cache",
			cacheDir:  "other/.cache",
			wantSkip:  true,
			rationale: "a cache outside the walked subtree is still the cache when reached",
		},
		{
			name:      "an unrelated directory of the same base name",
			wsRoot:    "/ws",
			path:      "/ws/elsewhere/.cache",
			cacheDir:  "sub/.cache",
			wantSkip:  false,
			rationale: "matching on the base name alone would skip authored directories",
		},
		{
			name:      "a directory below the cache",
			wsRoot:    "/ws",
			path:      "/ws/sub/.cache/entities",
			cacheDir:  "sub/.cache",
			wantSkip:  false,
			rationale: "the walk is cut at the cache itself; a descendant is never reached",
		},
		{
			name:      "an absolute cache setting is taken as already resolved",
			wsRoot:    "/ws",
			path:      "/elsewhere/cache",
			cacheDir:  "/elsewhere/cache",
			wantSkip:  true,
			rationale: "joining it under the workspace root would silently relocate it",
		},
		{
			name:      "no cache configured",
			wsRoot:    "/ws",
			path:      "/ws/.aggrega/cache",
			cacheDir:  "",
			wantSkip:  false,
			rationale: "a caller with no cache of its own disables the check",
		},
		{
			name:      "an unnormalised spelling of the same directory",
			wsRoot:    "/ws",
			path:      "/ws/sub/.cache",
			cacheDir:  "./sub/../sub/.cache",
			wantSkip:  true,
			rationale: "both sides normalise, so a spelling difference is not a different directory",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SkipDirIfArtifactCache(tc.wsRoot, tc.path, tc.cacheDir)
			if (got == fs.SkipDir) != tc.wantSkip {
				t.Errorf("SkipDirIfArtifactCache(%q, %q, %q) = %v, want skip=%v: %s",
					tc.wsRoot, tc.path, tc.cacheDir, got, tc.wantSkip, tc.rationale)
			}
		})
	}
}
