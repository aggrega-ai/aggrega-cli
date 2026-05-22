// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// writeFixture writes content to a fresh path under t.TempDir and
// returns the path. The helper avoids per-test boilerplate around
// path resolution + cleanup.
func writeFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "release-prod.lock")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// fullPopulationFixture returns the canonical-form YAML for a
// lockfile populating every section with realistic content. Tests
// that exercise round-trip / round-trip-after-canonicalisation
// re-use this fixture.
const fullPopulationFixture = `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z

cue_registry:
  default: hub.aggrega.ai
  overrides:
    - prefix: acme.com
      registry: aggrega-registry.acme.com

entities:
  - uri: hub.aggrega.ai/component/core/compute/api-server
    resolved_version: 1.2.3
    digest: sha256:a1b2c3
    resolution_path: tier3-registry
    referenced_from:
      - aggregate.spec.components.api.type
  - uri: hub.aggrega.ai/component/core/datastore/mariadb
    resolved_version: 1.0.0
    digest: sha256:d4e5f6
    resolution_path: tier2-workspace
    referenced_from:
      - aggregate.spec.components.db.type

cue_modules: []

schema_libraries:
  - uri: hub.aggrega.ai/schema-library/core/base-types
    resolved_version: 1.0.0
    digest: sha256:v2w3x4
    resolution_path: tier3-registry
    consumers:
      - consumer_uri: hub.aggrega.ai/component/core/compute/api-server
        fragments:
          - "#/schemas/PortSpec"
          - "#/schemas/EndpointSpec"

mapping_libraries:
  - uri: hub.aggrega.ai/mapping-library/core/base-mappings
    resolved_version: 1.0.0
    digest: sha256:y5z6a7
    resolution_path: tier3-registry
    consumers:
      - consumer_uri: hub.aggrega.ai/recipe/core/helm/mariadb-bitnami
        fragments:
          - "#/mappings/SimpleValues"

wasm_plugins:
  - uri: hub.aggrega.ai/wasm/aggrega-security-baseline
    resolved_version: 1.2.5
    digest: sha256:s9t0u1
    resolution_path: tier3-registry
    referenced_from:
      - .aggrega-config.yaml.plugins.policy
`

func TestLoadReturnsAllFiveSections(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, fullPopulationFixture)
	lf, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Errorf("expected no diagnostics; got %d: %+v", len(diagnostics), diagnostics)
	}
	if lf.Version != 1 {
		t.Errorf("Version: %d", lf.Version)
	}
	if lf.Release != "./release-prod.yaml" {
		t.Errorf("Release: %q", lf.Release)
	}
	if lf.GeneratedAt != "2026-05-22T10:23:14Z" {
		t.Errorf("GeneratedAt: %q", lf.GeneratedAt)
	}
	if lf.CueRegistry.Default != "hub.aggrega.ai" {
		t.Errorf("CueRegistry.Default: %q", lf.CueRegistry.Default)
	}
	if len(lf.CueRegistry.Overrides) != 1 {
		t.Fatalf("CueRegistry.Overrides len: %d", len(lf.CueRegistry.Overrides))
	}
	if len(lf.Entities) != 2 {
		t.Fatalf("Entities len: %d", len(lf.Entities))
	}
	if len(lf.SchemaLibraries) != 1 || len(lf.SchemaLibraries[0].Consumers) != 1 {
		t.Fatalf("SchemaLibraries shape: %+v", lf.SchemaLibraries)
	}
	if len(lf.SchemaLibraries[0].Consumers[0].Fragments) != 2 {
		t.Errorf("SchemaLibrary fragments len: %d", len(lf.SchemaLibraries[0].Consumers[0].Fragments))
	}
	if len(lf.MappingLibraries) != 1 {
		t.Fatalf("MappingLibraries len: %d", len(lf.MappingLibraries))
	}
	if len(lf.WasmPlugins) != 1 {
		t.Fatalf("WasmPlugins len: %d", len(lf.WasmPlugins))
	}
}

func TestLoadEmitsSoftDiagnosticForMissingSchemaLibraries(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
mapping_libraries: []
`
	path := writeFixture(t, content)
	_, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	found := false
	for _, d := range diagnostics {
		if d.Field == "schema_libraries" && d.Severity == lockfile.DiagnosticSoft {
			found = true
		}
	}
	if !found {
		t.Errorf("expected soft diagnostic for missing schema_libraries; got %+v", diagnostics)
	}
}

func TestLoadEmitsSoftDiagnosticForMissingMappingLibraries(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
schema_libraries: []
`
	path := writeFixture(t, content)
	_, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	found := false
	for _, d := range diagnostics {
		if d.Field == "mapping_libraries" && d.Severity == lockfile.DiagnosticSoft {
			found = true
		}
	}
	if !found {
		t.Errorf("expected soft diagnostic for missing mapping_libraries; got %+v", diagnostics)
	}
}

func TestLoadEmitsDeprecationDiagnosticForLegacyCueModules(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
schema_libraries: []
mapping_libraries: []
cue_modules:
  - module: aggrega.ai/core@v1
    resolved_version: v1.0.0
    digest: sha256:p6q7r8
    resolution_path: tier3-registry
    registry: hub.aggrega.ai
`
	path := writeFixture(t, content)
	_, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	found := false
	for _, d := range diagnostics {
		if d.Severity == lockfile.DiagnosticDeprecation &&
			strings.HasPrefix(d.Field, "cue_modules[") &&
			strings.Contains(d.Message, "aggrega.ai/core@v1") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected deprecation diagnostic naming the cue_module; got %+v", diagnostics)
	}
}

func TestLoadEmitsSoftDiagnosticForMissingConsumersList(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
schema_libraries:
  - uri: hub.aggrega.ai/schema-library/core/base-types
    resolved_version: 1.0.0
    digest: sha256:v2w3x4
    resolution_path: tier3-registry
mapping_libraries: []
`
	path := writeFixture(t, content)
	_, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	found := false
	for _, d := range diagnostics {
		if d.Severity == lockfile.DiagnosticSoft &&
			strings.Contains(d.Field, "schema_libraries[0].consumers") &&
			strings.Contains(d.Message, "base-types") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected soft diagnostic naming the consumer-less library; got %+v", diagnostics)
	}
}

func TestLoadHaltsOnMalformedField(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
entities: "not-a-list"
`
	path := writeFixture(t, content)
	_, _, err := lockfile.LoadLockfile(path)
	if err == nil {
		t.Fatal("expected error for non-list entities; got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileInvalid) {
		t.Errorf("error should wrap ErrLockfileInvalid; got %v", err)
	}
	if !strings.Contains(err.Error(), "entities") {
		t.Errorf("error should name entities; got: %v", err)
	}
}

func TestLoadRejectsUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
unknown_section: true
`
	path := writeFixture(t, content)
	_, _, err := lockfile.LoadLockfile(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level key; got nil")
	}
	if !errors.Is(err, lockfile.ErrLockfileInvalid) {
		t.Errorf("error should wrap ErrLockfileInvalid; got %v", err)
	}
}

func TestLoadHandlesFileAbsenceAsError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "does-not-exist.lock")
	_, _, err := lockfile.LoadLockfile(path)
	if err == nil {
		t.Fatal("expected file-absence error; got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist; got %v", err)
	}
}

func TestWriteCanonicalIsDeterministic(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release-prod.yaml",
		GeneratedAt: "2026-05-22T10:23:14Z",
		Entities: []lockfile.Entity{
			{URI: "hub.aggrega.ai/component/core/z-late", ResolvedVersion: "1.0.0", Digest: "sha256:z"},
			{URI: "hub.aggrega.ai/component/core/a-early", ResolvedVersion: "1.0.0", Digest: "sha256:a"},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{
				URI: "hub.aggrega.ai/schema-library/core/base-types",
				ResolvedVersion: "1.0.0",
				Digest: "sha256:v2w3x4",
				Consumers: []lockfile.LibraryConsumer{
					{ConsumerURI: "hub.aggrega.ai/component/core/z-late", Fragments: []string{"#/schemas/Z", "#/schemas/A"}},
					{ConsumerURI: "hub.aggrega.ai/component/core/a-early", Fragments: []string{"#/schemas/B"}},
				},
			},
		},
	}

	dir := t.TempDir()
	path1 := filepath.Join(dir, "first.lock")
	path2 := filepath.Join(dir, "second.lock")

	if err := lockfile.WriteLockfile(path1, lf); err != nil {
		t.Fatalf("first WriteLockfile: %v", err)
	}
	if err := lockfile.WriteLockfile(path2, lf); err != nil {
		t.Fatalf("second WriteLockfile: %v", err)
	}

	b1, _ := os.ReadFile(path1)
	b2, _ := os.ReadFile(path2)
	if string(b1) != string(b2) {
		t.Errorf("two WriteLockfile runs produced different bytes:\n--- first ---\n%s\n--- second ---\n%s", b1, b2)
	}

	// Caller's *Lockfile must retain its original (deliberately-shuffled)
	// ordering after WriteLockfile. canonicalise deep-copies before
	// sorting; this assertion guards against a future regression where
	// sorting leaks back into the caller's value.
	if lf.Entities[0].URI != "hub.aggrega.ai/component/core/z-late" {
		t.Errorf("WriteLockfile mutated caller's lf.Entities ordering: %+v", lf.Entities)
	}
	if lf.SchemaLibraries[0].Consumers[0].ConsumerURI != "hub.aggrega.ai/component/core/z-late" {
		t.Errorf("WriteLockfile mutated caller's Consumers ordering: %+v", lf.SchemaLibraries[0].Consumers)
	}
	if lf.SchemaLibraries[0].Consumers[0].Fragments[0] != "#/schemas/Z" {
		t.Errorf("WriteLockfile mutated caller's Fragments ordering: %+v", lf.SchemaLibraries[0].Consumers[0].Fragments)
	}
}

func TestWriteSortsSlicesByCanonicalKey(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./release-prod.yaml",
		GeneratedAt: "2026-05-22T10:23:14Z",
		Entities: []lockfile.Entity{
			{URI: "hub.aggrega.ai/component/core/z-late", ResolvedVersion: "1.0.0", Digest: "sha256:z",
				ReferencedFrom: []string{"z-second-site", "a-first-site"}},
			{URI: "hub.aggrega.ai/component/core/a-early", ResolvedVersion: "1.0.0", Digest: "sha256:a"},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{
				URI: "hub.aggrega.ai/schema-library/core/z-lib",
				ResolvedVersion: "1.0.0",
				Digest: "sha256:zlib",
				Consumers: []lockfile.LibraryConsumer{
					{ConsumerURI: "hub.aggrega.ai/component/core/z-late", Fragments: []string{"#/schemas/Z", "#/schemas/A"}},
					{ConsumerURI: "hub.aggrega.ai/component/core/a-early", Fragments: []string{"#/schemas/B"}},
				},
			},
			{
				URI: "hub.aggrega.ai/schema-library/core/a-lib",
				ResolvedVersion: "1.0.0",
				Digest: "sha256:alib",
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "sorted.lock")
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	roundTripped, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}

	if roundTripped.Entities[0].URI != "hub.aggrega.ai/component/core/a-early" {
		t.Errorf("Entities not sorted: %+v", roundTripped.Entities)
	}
	if roundTripped.Entities[1].URI != "hub.aggrega.ai/component/core/z-late" {
		t.Errorf("Entities not sorted: %+v", roundTripped.Entities)
	}
	if !sort.StringsAreSorted(roundTripped.Entities[1].ReferencedFrom) {
		t.Errorf("ReferencedFrom not sorted: %+v", roundTripped.Entities[1].ReferencedFrom)
	}
	if roundTripped.SchemaLibraries[0].URI != "hub.aggrega.ai/schema-library/core/a-lib" {
		t.Errorf("SchemaLibraries not sorted: %+v", roundTripped.SchemaLibraries)
	}
	consumers := roundTripped.SchemaLibraries[1].Consumers
	if consumers[0].ConsumerURI != "hub.aggrega.ai/component/core/a-early" {
		t.Errorf("Consumers not sorted by ConsumerURI: %+v", consumers)
	}
	if !sort.StringsAreSorted(consumers[1].Fragments) {
		t.Errorf("Fragments not sorted: %+v", consumers[1].Fragments)
	}
}

func TestRoundTripPreservesData(t *testing.T) {
	t.Parallel()
	// Load full-population fixture, write it back, load again,
	// assert structural equality on the URI-keyed fields (slice
	// ordering canonicalises through the round-trip).
	path1 := writeFixture(t, fullPopulationFixture)
	first, _, err := lockfile.LoadLockfile(path1)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	dir := t.TempDir()
	path2 := filepath.Join(dir, "roundtrip.lock")
	if err := lockfile.WriteLockfile(path2, first); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	second, _, err := lockfile.LoadLockfile(path2)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if first.Version != second.Version || first.Release != second.Release {
		t.Errorf("scalar fields drifted: %+v vs %+v", first, second)
	}
	if len(first.Entities) != len(second.Entities) {
		t.Fatalf("entity count drifted: %d vs %d", len(first.Entities), len(second.Entities))
	}
	for _, want := range first.Entities {
		found := false
		for _, got := range second.Entities {
			if got.URI == want.URI && got.Digest == want.Digest {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("entity %q missing after round-trip", want.URI)
		}
	}
	if len(first.SchemaLibraries) != len(second.SchemaLibraries) {
		t.Errorf("schema_libraries count drifted")
	}
}

func TestLoadRequiresVersionReleaseGeneratedAt(t *testing.T) {
	t.Parallel()
	// Three sub-cases, each omitting one required key.
	cases := []struct {
		name    string
		content string
		field   string
	}{
		{
			name: "missing_version",
			content: `release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
`,
			field: "version",
		},
		{
			name: "missing_release",
			content: `version: 1
generated_at: 2026-05-22T10:23:14Z
`,
			field: "release",
		},
		{
			name: "missing_generated_at",
			content: `version: 1
release: ./release-prod.yaml
`,
			field: "generated_at",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeFixture(t, tc.content)
			_, _, err := lockfile.LoadLockfile(path)
			if err == nil {
				t.Fatalf("expected error for missing %s; got nil", tc.field)
			}
			if !errors.Is(err, lockfile.ErrLockfileInvalid) {
				t.Errorf("error should wrap ErrLockfileInvalid; got %v", err)
			}
		})
	}
}

func TestLoadDoesNotEmitDiagnosticForExplicitlyEmptySchemaLibraries(t *testing.T) {
	t.Parallel()
	// Explicit empty list (schema_libraries: []) is distinct from
	// absent-from-document. The tolerant-load diagnostic fires only
	// when the section is absent; explicit empty is silent. This
	// test pins that distinction.
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
schema_libraries: []
mapping_libraries: []
`
	path := writeFixture(t, content)
	_, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	for _, d := range diagnostics {
		if d.Field == "schema_libraries" || d.Field == "mapping_libraries" {
			t.Errorf("explicit empty list should not emit diagnostic; got %+v", d)
		}
	}
}
