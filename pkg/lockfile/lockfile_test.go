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

source_artifacts:
  - kind: helm-oci-chart
    url: oci://hub.aggrega.ai/charts/mariadb
    pinned_version: "25.0.8"
    digest: sha256:abc123def
    mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
    referenced_by:
      - entity: recipe
        uri: hub.aggrega.ai/recipe/helm/mariadb-bitnami:1.0.0
        field: spec.source

wasm_plugins:
  - uri: hub.aggrega.ai/wasm/policy-validator/aggrega-security-baseline
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
				URI:             "hub.aggrega.ai/schema-library/core/base-types",
				ResolvedVersion: "1.0.0",
				Digest:          "sha256:v2w3x4",
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
				URI:             "hub.aggrega.ai/schema-library/core/z-lib",
				ResolvedVersion: "1.0.0",
				Digest:          "sha256:zlib",
				Consumers: []lockfile.LibraryConsumer{
					{ConsumerURI: "hub.aggrega.ai/component/core/z-late", Fragments: []string{"#/schemas/Z", "#/schemas/A"}},
					{ConsumerURI: "hub.aggrega.ai/component/core/a-early", Fragments: []string{"#/schemas/B"}},
				},
			},
			{
				URI:             "hub.aggrega.ai/schema-library/core/a-lib",
				ResolvedVersion: "1.0.0",
				Digest:          "sha256:alib",
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

// fullPopulationFixture's source_artifacts entry exercises the
// single-Recipe shape. Tests that need multi-Recipe dedupe or
// multi-kind canonical-ordering coverage write their own fixtures.

func TestLoadParsesSourceArtifactsSection(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, fullPopulationFixture)
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	if len(lf.SourceArtifacts) != 1 {
		t.Fatalf("SourceArtifacts len: %d; want 1", len(lf.SourceArtifacts))
	}
	sa := lf.SourceArtifacts[0]
	if sa.Kind != "helm-oci-chart" {
		t.Errorf("Kind = %q; want helm-oci-chart", sa.Kind)
	}
	if sa.URL != "oci://hub.aggrega.ai/charts/mariadb" {
		t.Errorf("URL = %q", sa.URL)
	}
	if sa.PinnedVersion != "25.0.8" {
		t.Errorf("PinnedVersion = %q", sa.PinnedVersion)
	}
	if sa.Digest != "sha256:abc123def" {
		t.Errorf("Digest = %q", sa.Digest)
	}
	if sa.Mediatype != "application/vnd.cncf.helm.chart.content.v1.tar+gzip" {
		t.Errorf("Mediatype = %q", sa.Mediatype)
	}
	if len(sa.ReferencedBy) != 1 {
		t.Fatalf("ReferencedBy len: %d; want 1", len(sa.ReferencedBy))
	}
	ref := sa.ReferencedBy[0]
	if ref.Entity != "recipe" || ref.URI != "hub.aggrega.ai/recipe/helm/mariadb-bitnami:1.0.0" || ref.Field != "spec.source" {
		t.Errorf("ReferencedBy[0] = %+v", ref)
	}
}

func TestLoadEmitsSoftDiagnosticForMissingSourceArtifacts(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
cue_registry:
  default: hub.aggrega.ai
entities: []
schema_libraries: []
mapping_libraries: []
wasm_plugins: []
`
	path := writeFixture(t, content)
	_, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	found := false
	for _, d := range diagnostics {
		if d.Field == "source_artifacts" && d.Severity == lockfile.DiagnosticSoft {
			found = true
			if !strings.Contains(d.Message, "source-artifact closure") {
				t.Errorf("message: %q", d.Message)
			}
		}
	}
	if !found {
		t.Errorf("expected soft diagnostic for missing source_artifacts; got %+v", diagnostics)
	}
}

func TestLoadDoesNotEmitDiagnosticForExplicitlyEmptySourceArtifacts(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./release-prod.yaml
generated_at: 2026-05-22T10:23:14Z
cue_registry:
  default: hub.aggrega.ai
entities: []
schema_libraries: []
mapping_libraries: []
source_artifacts: []
wasm_plugins: []
`
	path := writeFixture(t, content)
	_, diagnostics, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	for _, d := range diagnostics {
		if d.Field == "source_artifacts" {
			t.Errorf("explicit empty list should not emit diagnostic; got %+v", d)
		}
	}
}

func TestWriteSourceArtifactsCanonicalOrdering(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:23:14Z",
		SourceArtifacts: []lockfile.SourceArtifact{
			{Kind: "terraform-module-git", URL: "https://example.com/mod.git", PinnedVersion: "abc123", Digest: "sha256:1", Mediatype: "application/gzip", ReferencedBy: []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "u1", Field: "spec.source"}}},
			{Kind: "helm-oci-chart", URL: "oci://r/c", PinnedVersion: "26.0.0", Digest: "sha256:2", Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip", ReferencedBy: []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "u2", Field: "spec.source"}}},
			{Kind: "helm-oci-chart", URL: "oci://r/c", PinnedVersion: "25.0.0", Digest: "sha256:3", Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip", ReferencedBy: []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "u3", Field: "spec.source"}}},
			{Kind: "compose-https-fragment", URL: "https://example.com/a.yaml", PinnedVersion: "1.0.0", Digest: "sha256:4", Mediatype: "application/x-yaml", ReferencedBy: []lockfile.SourceArtifactReference{{Entity: "recipe", URI: "u4", Field: "spec.source"}}},
		},
	}
	path := filepath.Join(t.TempDir(), "out.lock")
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	out, _ := os.ReadFile(path)
	// Expected order by (Kind, URL, PinnedVersion):
	//   compose-https-fragment / https://example.com/a.yaml / 1.0.0
	//   helm-oci-chart / oci://r/c / 25.0.0
	//   helm-oci-chart / oci://r/c / 26.0.0
	//   terraform-module-git / https://example.com/mod.git / abc123
	composeIdx := strings.Index(string(out), "compose-https-fragment")
	helmIdx := strings.Index(string(out), "helm-oci-chart")
	tfIdx := strings.Index(string(out), "terraform-module-git")
	helm25Idx := strings.Index(string(out), "25.0.0")
	helm26Idx := strings.Index(string(out), "26.0.0")
	if !(composeIdx < helmIdx && helmIdx < tfIdx) {
		t.Errorf("SourceArtifacts not sorted by Kind: compose=%d helm=%d tf=%d", composeIdx, helmIdx, tfIdx)
	}
	if !(helm25Idx < helm26Idx) {
		t.Errorf("within same Kind+URL, PinnedVersion should sort lexicographically: 25.0.0@%d 26.0.0@%d", helm25Idx, helm26Idx)
	}
}

func TestWriteSourceArtifactsReferencedByDeterministic(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./r.yaml",
		GeneratedAt: "2026-05-22T10:23:14Z",
		SourceArtifacts: []lockfile.SourceArtifact{
			{
				Kind: "helm-oci-chart", URL: "oci://r/c", PinnedVersion: "1.0.0",
				Digest: "sha256:x", Mediatype: "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
				ReferencedBy: []lockfile.SourceArtifactReference{
					{Entity: "recipe", URI: "z-uri", Field: "spec.source"},
					{Entity: "recipe", URI: "a-uri", Field: "spec.source"},
					{Entity: "feature_recipe", URI: "m-uri", Field: "spec.source"},
				},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "out.lock")
	if err := lockfile.WriteLockfile(path, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	out, _ := os.ReadFile(path)
	aIdx := strings.Index(string(out), "a-uri")
	mIdx := strings.Index(string(out), "m-uri")
	zIdx := strings.Index(string(out), "z-uri")
	if !(aIdx < mIdx && mIdx < zIdx) {
		t.Errorf("ReferencedBy not sorted by URI: a=%d m=%d z=%d", aIdx, mIdx, zIdx)
	}
}

func TestRoundTripPreservesSourceArtifacts(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, fullPopulationFixture)
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	outPath := filepath.Join(t.TempDir(), "out.lock")
	if err := lockfile.WriteLockfile(outPath, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	lf2, _, err := lockfile.LoadLockfile(outPath)
	if err != nil {
		t.Fatalf("LoadLockfile (round-trip): %v", err)
	}
	if len(lf2.SourceArtifacts) != len(lf.SourceArtifacts) {
		t.Fatalf("SourceArtifacts len mismatch: got %d want %d",
			len(lf2.SourceArtifacts), len(lf.SourceArtifacts))
	}
	a, b := lf.SourceArtifacts[0], lf2.SourceArtifacts[0]
	if a.Kind != b.Kind || a.URL != b.URL || a.PinnedVersion != b.PinnedVersion ||
		a.Digest != b.Digest || a.Mediatype != b.Mediatype {
		t.Errorf("SourceArtifact scalar fields differ:\n  got  %+v\n  want %+v", b, a)
	}
	if len(a.ReferencedBy) != len(b.ReferencedBy) {
		t.Fatalf("ReferencedBy len: got %d want %d", len(b.ReferencedBy), len(a.ReferencedBy))
	}
	for i := range a.ReferencedBy {
		if a.ReferencedBy[i] != b.ReferencedBy[i] {
			t.Errorf("ReferencedBy[%d] differs:\n  got  %+v\n  want %+v",
				i, b.ReferencedBy[i], a.ReferencedBy[i])
		}
	}
}

func TestSchemaRejectsUnknownEntity(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./r.yaml
generated_at: 2026-05-22T10:23:14Z
cue_registry: { default: x }
entities: []
schema_libraries: []
mapping_libraries: []
source_artifacts:
  - kind: helm-oci-chart
    url: oci://r/c
    pinned_version: 1.0.0
    digest: sha256:x
    mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
    referenced_by:
      - entity: aggregate
        uri: u
        field: spec.source
wasm_plugins: []
`
	path := writeFixture(t, content)
	_, _, err := lockfile.LoadLockfile(path)
	if err == nil {
		t.Fatal("expected schema rejection for unknown entity")
	}
	if !errors.Is(err, lockfile.ErrLockfileInvalid) {
		t.Errorf("error should wrap ErrLockfileInvalid; got %v", err)
	}
}

func TestSchemaRejectsUnknownKind(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./r.yaml
generated_at: 2026-05-22T10:23:14Z
cue_registry: { default: x }
entities: []
schema_libraries: []
mapping_libraries: []
source_artifacts:
  - kind: made-up-kind
    url: x://r/c
    pinned_version: 1.0.0
    digest: sha256:x
    mediatype: x
    referenced_by:
      - entity: recipe
        uri: u
        field: spec.source
wasm_plugins: []
`
	path := writeFixture(t, content)
	_, _, err := lockfile.LoadLockfile(path)
	if err == nil {
		t.Fatal("expected schema rejection for unknown kind")
	}
	if !errors.Is(err, lockfile.ErrLockfileInvalid) {
		t.Errorf("error should wrap ErrLockfileInvalid; got %v", err)
	}
}

func TestSchemaRequiresAtLeastOneReferencedBy(t *testing.T) {
	t.Parallel()
	content := `version: 1
release: ./r.yaml
generated_at: 2026-05-22T10:23:14Z
cue_registry: { default: x }
entities: []
schema_libraries: []
mapping_libraries: []
source_artifacts:
  - kind: helm-oci-chart
    url: oci://r/c
    pinned_version: 1.0.0
    digest: sha256:x
    mediatype: application/vnd.cncf.helm.chart.content.v1.tar+gzip
    referenced_by: []
wasm_plugins: []
`
	path := writeFixture(t, content)
	_, _, err := lockfile.LoadLockfile(path)
	if err == nil {
		t.Fatal("expected schema rejection for empty referenced_by")
	}
	if !errors.Is(err, lockfile.ErrLockfileInvalid) {
		t.Errorf("error should wrap ErrLockfileInvalid; got %v", err)
	}
}
