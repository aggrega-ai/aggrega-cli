// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestReadPinnedLibrary pins the build-side bridge's verified cache read: it
// reads a lockfile-pinned SchemaLibrary from the content cache by its host-less
// name-path + resolved version, verifies the bytes against the pin at the cache
// boundary, and parses through the shared per-entity resolve. A cache miss steers
// to `aggrega pull`; a digest mismatch is ErrFetchIntegrity. The versioned cache
// key is `<uri>:<version>` (the cache/lockfile separator is `:`).
func TestReadPinnedLibrary(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const name = "DbSizing"
	const version = "1.0.0"
	const versionedURI = name + ":" + version
	body := []byte("apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: SchemaLibrary\nmetadata:\n  name: DbSizing\n  version: 1.0.0\n" +
		"spec:\n  schemas:\n    Storage:\n      size: { type: string, default: \"10Gi\" }\n")
	// Write the slot through the SAME primitive the real puller uses for a
	// library (writeEntityToCacheSlot → `library.yaml`, keyed encodeURI(uri)/version),
	// not the generic `blob` layout — otherwise the read would exercise a fictional
	// cache layout and the proof would be vacuous.
	if err := writeEntityToCacheSlot(ws, cacheDir, versionedURI, version, &oci.PullResult{
		MediaType: oci.MediaTypeSchemaLibrary,
		Bytes:     body,
	}); err != nil {
		t.Fatalf("writeEntityToCacheSlot: %v", err)
	}
	pin := contentDigest(body)

	t.Run("verified read returns the exact pinned bytes", func(t *testing.T) {
		got, err := ReadPinnedLibrary(ws, cacheDir, name, version, pin)
		if err != nil {
			t.Fatalf("ReadPinnedLibrary: %v", err)
		}
		// Exact bytes (not a re-serialised parse) so the bridge's entityBytes
		// digest equals the lockfile pin — keeping the drift verify faithful.
		if string(got) != string(body) {
			t.Errorf("bytes = %q; want the pinned library body verbatim", got)
		}
	})

	t.Run("digest mismatch is ErrFetchIntegrity", func(t *testing.T) {
		_, err := ReadPinnedLibrary(ws, cacheDir, name, version,
			"sha256:0000000000000000000000000000000000000000000000000000000000000000")
		if !errors.Is(err, oci.ErrFetchIntegrity) {
			t.Errorf("error = %v; want ErrFetchIntegrity", err)
		}
	})

	t.Run("cache miss is ErrFetchNotFound (pull-first)", func(t *testing.T) {
		_, err := ReadPinnedLibrary(ws, cacheDir, "Absent", version, pin)
		if !errors.Is(err, oci.ErrFetchNotFound) {
			t.Errorf("error = %v; want ErrFetchNotFound", err)
		}
	})
}

const dbSizingLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`

const xrefConsumerYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "DbSizing#/schemas/Storage"
`

// TestLoadWithExtraLibraries pins the build-side bridge's catalog merge: a
// lockfile-pinned library injected via `extra` resolves a consumer's
// cross-document $ref under Pass-2 validation. It proves the reject gate is
// preserved (Load rejects the unresolved $ref WITHOUT the library, so Pass-2 runs
// after the merge — not bypassed) and that a name present both locally and pinned
// resolves local-wins with a collision warning.
func TestLoadWithExtraLibraries(t *testing.T) {
	t.Parallel()

	extra := map[string][]byte{"DbSizing": []byte(dbSizingLibYAML)}

	t.Run("bridged library resolves the consumer cross-document $ref", func(t *testing.T) {
		dir := t.TempDir()
		writeBridgeFixture(t, dir, "consumer.yaml", xrefConsumerYAML)

		// Gate is real: without the library in the local walk, Pass-2 pillar
		// validation rejects the unresolved cross-document $ref.
		if _, err := LoadAllWithCacheDir("", "", dir); err == nil {
			t.Fatal("expected Load to reject the unresolved cross-document $ref; got nil")
		}

		// Bridging the pinned library in lets Pass-2 resolve it — the gate runs
		// over the merged set, it is not bypassed.
		cat, warnings, err := LoadWithExtraLibraries("", "", extra, nil, dir)
		if err != nil {
			t.Fatalf("LoadWithExtraLibraries: %v", err)
		}
		if len(warnings) != 0 {
			t.Errorf("warnings = %v; want none", warnings)
		}
		if _, ok := cat.SchemaLibraries["DbSizing"]; !ok {
			t.Error("DbSizing not merged into the catalog")
		}
		// entityBytes carries the EXACT verified bytes, so the closure's Tier-1
		// digest for the bridged library equals the lockfile pin — the basis of a
		// faithful lockfile-drift verify. A re-serialised parse would
		// diverge here and drift the build spuriously.
		if got := contentDigest(cat.entityBytes[entityBytesKey("SchemaLibrary", "DbSizing")]); got != contentDigest([]byte(dbSizingLibYAML)) {
			t.Errorf("bridged entityBytes digest = %s; want the pinned bytes' digest", got)
		}
	})

	t.Run("local definition wins on name collision with a warning", func(t *testing.T) {
		dir := t.TempDir()
		writeBridgeFixture(t, dir, "consumer.yaml", xrefConsumerYAML)
		writeBridgeFixture(t, dir, "dbsizing.yaml", dbSizingLibYAML) // also local

		cat, warnings, err := LoadWithExtraLibraries("", "", extra, nil, dir)
		if err != nil {
			t.Fatalf("LoadWithExtraLibraries: %v", err)
		}
		if len(warnings) != 1 {
			t.Fatalf("warnings = %v; want exactly one collision notice", warnings)
		}
		if !strings.Contains(warnings[0], "DbSizing") {
			t.Errorf("collision warning should name the library; got %q", warnings[0])
		}
		if _, ok := cat.SchemaLibraries["DbSizing"]; !ok {
			t.Error("DbSizing missing from catalog")
		}
	})
}

// TestResolveSchemaLibraryBareNameTier1 pins the bare-name Tier-1 resolution
// contract: a
// host-less bare-name library reference (the form discovery now records)
// resolves at Tier-1 against the local catalog, keyed by metadata.name. This
// guards schemaLibraryNameFromURI against silent divergence — discovery (A)
// depends on a bare name resolving here. (Tier-3 pull of a remote bare-name
// library needs the MVS-selected version and lands with the pull-flow reshape.)
func TestResolveSchemaLibraryBareNameTier1(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeBridgeFixture(t, dir, "dbsizing.yaml", dbSizingLibYAML)
	cat, err := Load("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")
	res, err := lookup.ResolveSchemaLibrary("DbSizing")
	if err != nil {
		t.Fatalf("ResolveSchemaLibrary: %v", err)
	}
	if res.NotFound {
		t.Fatal("bare-name library should resolve at Tier-1; got NotFound")
	}
	if res.ResolvedVersion != "1.0.0" {
		t.Errorf("ResolvedVersion = %q; want 1.0.0", res.ResolvedVersion)
	}
	if res.ResolutionPath != "tier-1-catalog" {
		t.Errorf("ResolutionPath = %q; want tier-1-catalog", res.ResolutionPath)
	}
	if _, ok := res.Entries["Storage"]; !ok {
		t.Errorf("entry index should include Storage; got %v", res.Entries)
	}
}

// TestHighestSemverTag pins the first-pull version selector: the highest
// dotted-numeric tag wins, by NUMERIC (not lexical) component comparison, and
// non-semver tags (e.g. "latest") are ignored. An empty result means no usable
// version (the puller then surfaces a fetch error).
func TestHighestSemverTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tags []string
		want string
	}{
		{"highest of several", []string{"1.0.0", "2.3.1", "2.10.0", "2.3.10"}, "2.10.0"},
		{"numeric not lexical", []string{"1.9.0", "1.10.0"}, "1.10.0"},
		{"ignores non-semver tags", []string{"latest", "1.0.0", "edge"}, "1.0.0"},
		{"none usable", []string{"latest", "stable"}, ""},
		{"empty input", nil, ""},
	}
	for _, tc := range cases {
		if got := highestSemverTag(tc.tags); got != tc.want {
			t.Errorf("%s: highestSemverTag(%v) = %q; want %q", tc.name, tc.tags, got, tc.want)
		}
	}
}

// TestLibraryReference pins the pull-side name→location binding: a host-less
// library identity resolves to `<registry>/<kind-segment>/<name>`, matching the
// push-side publish path so a pulled library is fetched from where it was
// published (the identity round-trip).
func TestLibraryReference(t *testing.T) {
	t.Parallel()
	cases := []struct {
		registry, kind, name, want string
	}{
		// The OCI repository is a lowercased LOCATION: the PascalCase library
		// identity is preserved in the lockfile/cache, while the fetch location
		// lowercases the name. Pull-location == push-location.
		{"hub.aggrega.ai", "SchemaLibrary", "DbSizing", "hub.aggrega.ai/schemalibrary/dbsizing"},
		{"reg.example.com", "MappingLibrary", "BaseMappings", "reg.example.com/mappinglibrary/basemappings"},
	}
	for _, tc := range cases {
		if got := libraryReference(tc.registry, tc.kind, tc.name); got != tc.want {
			t.Errorf("libraryReference(%q, %q, %q) = %q; want %q", tc.registry, tc.kind, tc.name, got, tc.want)
		}
	}
}

func writeBridgeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
