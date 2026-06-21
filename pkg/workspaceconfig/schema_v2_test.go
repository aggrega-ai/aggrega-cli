// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestV2FieldLocations pins that the promoted top-level settings and the
// renamed cache blocks load from their new homes, and that catalog_dirs
// stays on the workspace root marker.
func TestV2FieldLocations(t *testing.T) {
	t.Parallel()
	content := `
default_registry: registry.example.com
offline: true
auto_pull: false
artifact_cache_dir: .aggrega/custom
workspace:
  catalog_dirs:
    - vendor/catalog
parse_cache:
  max_entries: 7
  max_bytes: 8888
`
	got, err := workspaceconfig.Load(writeFixture(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DefaultRegistry != "registry.example.com" {
		t.Errorf("DefaultRegistry: %q", got.DefaultRegistry)
	}
	if !got.Offline {
		t.Errorf("Offline: want true")
	}
	if got.AutoPull {
		t.Errorf("AutoPull: want false (explicit)")
	}
	if got.ArtifactCacheDir != ".aggrega/custom" || got.ResolvedArtifactCacheDir() != ".aggrega/custom" {
		t.Errorf("ArtifactCacheDir: %q", got.ArtifactCacheDir)
	}
	if len(got.Workspace.CatalogDirs) != 1 || got.Workspace.CatalogDirs[0] != "vendor/catalog" {
		t.Errorf("Workspace.CatalogDirs: %+v", got.Workspace.CatalogDirs)
	}
	if got.ParseCache.MaxEntries != 7 || got.ParseCache.MaxBytes != 8888 {
		t.Errorf("ParseCache: %+v", got.ParseCache)
	}
}

// TestEmptyWorkspaceMarkerValidates pins that `workspace: {}` is the
// canonical empty root marker: it validates and leaves the documented
// defaults intact (the marker carries no settings).
func TestEmptyWorkspaceMarkerValidates(t *testing.T) {
	t.Parallel()
	got, err := workspaceconfig.Load(writeFixture(t, "workspace: {}\n"))
	if err != nil {
		t.Fatalf("Load(workspace: {}): %v", err)
	}
	if got.DefaultRegistry != "hub.aggrega.ai" {
		t.Errorf("default not preserved under empty marker: DefaultRegistry=%q", got.DefaultRegistry)
	}
	if got.ResolvedArtifactCacheDir() != ".aggrega/cache" {
		t.Errorf("artifact cache default not preserved: %q", got.ResolvedArtifactCacheDir())
	}
	if len(got.Workspace.CatalogDirs) != 0 {
		t.Errorf("CatalogDirs should be empty: %+v", got.Workspace.CatalogDirs)
	}
}

// TestBareWorkspaceRejectedWithFixIt pins that a null `workspace:`
// (the key with no mapping) fails with the fix-it pointing at `workspace: {}`.
func TestBareWorkspaceRejectedWithFixIt(t *testing.T) {
	t.Parallel()
	_, err := workspaceconfig.Load(writeFixture(t, "workspace:\n"))
	if err == nil {
		t.Fatal("expected a bare `workspace:` (null) to be rejected; got nil")
	}
	if !errors.Is(err, workspaceconfig.ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "workspace: {}") {
		t.Errorf("error should offer the `workspace: {}` fix-it; got %v", err)
	}
}

// TestMovedKeysReportRelocation pins the migration diagnostics: an
// old-shape key under `workspace:` and the old top-level `cache:` block
// report where they moved, rather than a bare schema rejection.
func TestMovedKeysReportRelocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"default_registry", "workspace:\n  default_registry: x\n", "moved to the top level"},
		{"cache_dir", "workspace:\n  cache_dir: .x\n", "artifact_cache_dir"},
		{"offline", "workspace:\n  offline: true\n", "moved to the top level"},
		{"auto_pull", "workspace:\n  auto_pull: false\n", "moved to the top level"},
		{"cache_block", "parse_cache:\n  max_entries: 1\ncache:\n  max_entries: 2\n", "renamed to `parse_cache`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := workspaceconfig.Load(writeFixture(t, tc.content))
			if err == nil {
				t.Fatalf("expected a relocation diagnostic for %s; got nil", tc.name)
			}
			if !errors.Is(err, workspaceconfig.ErrConfigInvalid) {
				t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should mention %q; got %v", tc.want, err)
			}
		})
	}
}

// TestV2FullRoundTrip pins that a fully-populated v2 config across every
// block loads without error and the relocated fields read back correctly.
func TestV2FullRoundTrip(t *testing.T) {
	t.Parallel()
	content := `
default_registry: hub.aggrega.ai
offline: false
auto_pull: true
artifact_cache_dir: .aggrega/cache
workspace:
  catalog_dirs:
    - a
  catalog_dirs_append:
    - b
build:
  yaml_indent: 4
plugins:
  linter:
    - uri: ./plugin.wasm
lint:
  publishing_discipline:
    enforce_immutability: true
parse_cache:
  max_entries: 1000
  max_bytes: 100000000
`
	got, err := workspaceconfig.Load(writeFixture(t, content))
	if err != nil {
		t.Fatalf("Load full v2 config: %v", err)
	}
	if got.DefaultRegistry != "hub.aggrega.ai" || !got.AutoPull || got.Build.YAMLIndent != 4 {
		t.Errorf("round-trip mismatch: registry=%q autoPull=%v indent=%d", got.DefaultRegistry, got.AutoPull, got.Build.YAMLIndent)
	}
	if len(got.Workspace.CatalogDirs) != 1 || len(got.Plugins.Linter) != 1 {
		t.Errorf("round-trip lists: catalogDirs=%+v linter=%+v", got.Workspace.CatalogDirs, got.Plugins.Linter)
	}
}
