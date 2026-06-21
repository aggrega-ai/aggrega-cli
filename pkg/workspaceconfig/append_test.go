// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeYAML writes content to a temp file and returns the path. Used
// by every _append spec to materialize a minimal workspace or global
// config fixture inline.
func writeYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestLoadWithGlobalCatalogDirsAppendComposesGlobalAndWorkspace
// verifies the canonical compose-don't-shadow case: global declares
// a catalog_dirs base, workspace declares only catalog_dirs_append;
// the merged result extends global's base with the workspace's
// append payload.
func TestLoadWithGlobalCatalogDirsAppendComposesGlobalAndWorkspace(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeYAML(t, dir, "global.yaml", "workspace:\n  catalog_dirs:\n    - /a\n    - /b\n")
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml", "workspace:\n  catalog_dirs_append:\n    - /c\n")

	merged, source, err := LoadWithGlobal(wsPath, globalPath)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if source != SourceWorkspace {
		t.Errorf("source: got %v, want SourceWorkspace", source)
	}
	if got, want := merged.Workspace.CatalogDirs, []string{"/a", "/b", "/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CatalogDirs: got %v, want %v", got, want)
	}
	if merged.Workspace.CatalogDirsAppend != nil {
		t.Errorf("CatalogDirsAppend should be zeroed post-merge; got %v", merged.Workspace.CatalogDirsAppend)
	}
}

// TestLoadWithGlobalAppendOnlyWhenBaseEmpty verifies the empty-base
// case (operator decision a): when no layer specifies the base list,
// a higher-precedence _append becomes the resolved list verbatim.
func TestLoadWithGlobalAppendOnlyWhenBaseEmpty(t *testing.T) {
	dir := t.TempDir()
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml", "workspace:\n  catalog_dirs_append:\n    - /c\n")

	merged, _, err := LoadWithGlobal(wsPath, "")
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if got, want := merged.Workspace.CatalogDirs, []string{"/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CatalogDirs: got %v, want %v", got, want)
	}
}

// TestLoadWithGlobalSameLayerBaseThenAppend verifies the same-layer
// interaction (operator decision three): when one layer specifies
// both the base and its _append companion, the resolved value at
// that layer is the base list followed by the append list.
func TestLoadWithGlobalSameLayerBaseThenAppend(t *testing.T) {
	dir := t.TempDir()
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"workspace:\n  catalog_dirs:\n    - /a\n    - /b\n  catalog_dirs_append:\n    - /c\n")

	merged, _, err := LoadWithGlobal(wsPath, "")
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if got, want := merged.Workspace.CatalogDirs, []string{"/a", "/b", "/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CatalogDirs: got %v, want %v", got, want)
	}
}

// TestLoadWithGlobalHigherLayerReplacesBaseButAppendsAccumulate
// verifies the cross-layer flow: workspace's base whole-replaces
// global's base; both layers' _append payloads accumulate in
// precedence order on top of the resolved base.
func TestLoadWithGlobalHigherLayerReplacesBaseButAppendsAccumulate(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeYAML(t, dir, "global.yaml",
		"workspace:\n  catalog_dirs:\n    - /a\n    - /b\n  catalog_dirs_append:\n    - /x\n")
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"workspace:\n  catalog_dirs:\n    - /c\n  catalog_dirs_append:\n    - /y\n")

	merged, _, err := LoadWithGlobal(wsPath, globalPath)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if got, want := merged.Workspace.CatalogDirs, []string{"/c", "/x", "/y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CatalogDirs: got %v, want %v", got, want)
	}
}

// TestLoadWithGlobalNarrowingExemptionsAppend smoke-tests that the
// publishing_discipline.narrowing_exemptions_append companion wires
// the same compose-don't-shadow semantic.
func TestLoadWithGlobalNarrowingExemptionsAppend(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeYAML(t, dir, "global.yaml",
		"lint:\n  publishing_discipline:\n    narrowing_exemptions:\n      - entity: e1\n        paths: [p1]\n        reason: r1\n")
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"lint:\n  publishing_discipline:\n    narrowing_exemptions_append:\n      - entity: e2\n        paths: [p2]\n        reason: r2\n")

	merged, _, err := LoadWithGlobal(wsPath, globalPath)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	got := merged.Lint.PublishingDiscipline.NarrowingExemptions
	if len(got) != 2 {
		t.Fatalf("NarrowingExemptions: got %d entries, want 2", len(got))
	}
	if got[0].Entity != "e1" || got[1].Entity != "e2" {
		t.Errorf("entries: got [%s, %s], want [e1, e2]", got[0].Entity, got[1].Entity)
	}
	if merged.Lint.PublishingDiscipline.NarrowingExemptionsAppend != nil {
		t.Errorf("NarrowingExemptionsAppend should be zeroed post-merge")
	}
}

// TestLoadWithGlobalMutatorAppend smoke-tests that
// plugins.mutator_append wires the same compose semantic.
func TestLoadWithGlobalMutatorAppend(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeYAML(t, dir, "global.yaml",
		"plugins:\n  mutator:\n    - uri: oci://a@v1\n")
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"plugins:\n  mutator_append:\n    - uri: oci://b@v1\n")

	merged, _, err := LoadWithGlobal(wsPath, globalPath)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if got, want := len(merged.Plugins.Mutator), 2; got != want {
		t.Fatalf("Mutator count: got %d, want %d", got, want)
	}
	if merged.Plugins.Mutator[0].URI != "oci://a@v1" || merged.Plugins.Mutator[1].URI != "oci://b@v1" {
		t.Errorf("URIs: got [%s, %s]", merged.Plugins.Mutator[0].URI, merged.Plugins.Mutator[1].URI)
	}
	if merged.Plugins.MutatorAppend != nil {
		t.Errorf("MutatorAppend should be zeroed post-merge")
	}
}

// TestLoadWithGlobalPolicyAppend smoke-tests plugins.policy_append.
func TestLoadWithGlobalPolicyAppend(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeYAML(t, dir, "global.yaml",
		"plugins:\n  policy:\n    - uri: oci://a@v1\n")
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"plugins:\n  policy_append:\n    - uri: oci://b@v1\n")

	merged, _, err := LoadWithGlobal(wsPath, globalPath)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if got := len(merged.Plugins.Policy); got != 2 {
		t.Errorf("Policy count: got %d, want 2", got)
	}
}

// TestLoadWithGlobalLinterAppend smoke-tests plugins.linter_append.
func TestLoadWithGlobalLinterAppend(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeYAML(t, dir, "global.yaml",
		"plugins:\n  linter:\n    - uri: oci://a@v1\n")
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"plugins:\n  linter_append:\n    - uri: oci://b@v1\n")

	merged, _, err := LoadWithGlobal(wsPath, globalPath)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if got := len(merged.Plugins.Linter); got != 2 {
		t.Errorf("Linter count: got %d, want 2", got)
	}
}

// TestLoadWithGlobalImporterAppend smoke-tests plugins.importer_append.
func TestLoadWithGlobalImporterAppend(t *testing.T) {
	dir := t.TempDir()
	globalPath := writeYAML(t, dir, "global.yaml",
		"plugins:\n  importer:\n    - uri: oci://a@v1\n")
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"plugins:\n  importer_append:\n    - uri: oci://b@v1\n")

	merged, _, err := LoadWithGlobal(wsPath, globalPath)
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if got := len(merged.Plugins.Importer); got != 2 {
		t.Errorf("Importer count: got %d, want 2", got)
	}
}

// TestLoadWithGlobalCachePassesThroughSchemaAndOverlay verifies the
// workspace-config-to-cache integration path: an operator-authored
// cache block parses through schema validation and reaches the
// merged Config's Cache field. Catches the failure mode where the
// Go types are wired but the JSON Schema rejects the block via
// additionalProperties:false at the root.
func TestLoadWithGlobalCachePassesThroughSchemaAndOverlay(t *testing.T) {
	dir := t.TempDir()
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"parse_cache:\n  max_entries: 500\n  max_bytes: 50000\n")

	merged, _, err := LoadWithGlobal(wsPath, "")
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if merged.ParseCache.MaxEntries != 500 {
		t.Errorf("Cache.MaxEntries: got %d, want 500", merged.ParseCache.MaxEntries)
	}
	if merged.ParseCache.MaxBytes != 50000 {
		t.Errorf("Cache.MaxBytes: got %d, want 50000", merged.ParseCache.MaxBytes)
	}
}

// TestLoadWithGlobalCacheDefaultsPreservedWhenAbsent verifies that
// a workspace config omitting the cache block preserves the seeded
// defaults (1000 / 100_000_000) — the Default()-seeded values
// survive the yaml overlay when the source mapping has no `cache`
// key.
func TestLoadWithGlobalCacheDefaultsPreservedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"default_registry: example.com\n")

	merged, _, err := LoadWithGlobal(wsPath, "")
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if merged.ParseCache.MaxEntries != 1000 {
		t.Errorf("Cache.MaxEntries: got %d, want default 1000", merged.ParseCache.MaxEntries)
	}
	if merged.ParseCache.MaxBytes != 100_000_000 {
		t.Errorf("Cache.MaxBytes: got %d, want default 100_000_000", merged.ParseCache.MaxBytes)
	}
}

// TestLoadWithGlobalCacheZeroIsExplicitUnbounded verifies that an
// operator explicitly setting either cap to 0 (the documented
// "unbounded on that axis" sentinel) survives the overlay. yaml.v3
// distinguishes "key absent" (defaults preserved) from "key explicit
// 0" (0 written into target) — this test pins the latter behavior
// against the Default()-seeded 1000.
func TestLoadWithGlobalCacheZeroIsExplicitUnbounded(t *testing.T) {
	dir := t.TempDir()
	wsPath := writeYAML(t, dir, ".aggrega-config.yaml",
		"parse_cache:\n  max_entries: 0\n  max_bytes: 0\n")

	merged, _, err := LoadWithGlobal(wsPath, "")
	if err != nil {
		t.Fatalf("LoadWithGlobal: %v", err)
	}
	if merged.ParseCache.MaxEntries != 0 {
		t.Errorf("Cache.MaxEntries: got %d, want explicit 0 (unbounded)", merged.ParseCache.MaxEntries)
	}
	if merged.ParseCache.MaxBytes != 0 {
		t.Errorf("Cache.MaxBytes: got %d, want explicit 0 (unbounded)", merged.ParseCache.MaxBytes)
	}
}
