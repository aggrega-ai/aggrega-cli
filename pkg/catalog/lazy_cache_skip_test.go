// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The artifact cache lives under the workspace root, and a catalog directory
// defaults to the workspace root — so the cache sits INSIDE the walked catalog
// tree in the mainstream layout, with no flag set. Every fixture below builds
// exactly that layout, which is what makes the cache visible to a catalog walk
// in the first place.
const cacheUnderCatalogDir = ".aggrega/cache"

// staleLibYAML and pinnedLibYAML are two versions of ONE SchemaLibrary name.
// The version strings are chosen so the lexical order of their cache slots is
// the REVERSE of their semantic order: "1.10.0" sorts before "1.9.0" byte-wise,
// so a directory walk visits the newer one first and the older one last.
const (
	staleLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.9.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "STALE" }
`
	pinnedLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: 1.10.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "PINNED" }
`
	authoredMappingLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 2.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.authored }}'
`
	cachedMappingLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.cached }}'
`
	verifiedLibYAML = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Verified
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "VERIFIED" }
`
	authoredComponentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/authored-app
  version: "1.0.0"
spec:
  inject: ["env"]
`
)

// writeCachedLibrary drops a library.yaml into the artifact cache slot for
// slotURI, the way a completed pull lays it down, WITHOUT recording a pin. It is
// the "unverified route" fixture: bytes present in the cache that no lockfile
// vouches for.
func writeCachedLibrary(t *testing.T, ws, cacheDir, slotURI, body string) string {
	t.Helper()
	_, version, _ := resolver.SplitVersionedURI(slotURI)
	slot := mustCachePath(ws, cacheDir, slotURI, version)
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir cache slot %s: %v", slotURI, err)
	}
	path := filepath.Join(slot, "library.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write cache slot %s: %v", slotURI, err)
	}
	return path
}

// TestLazyWalks_SkipArtifactCacheUnderCatalogDir is the layout clause: a
// workspace whose artifact cache sits INSIDE a walked catalog directory — the
// mainstream shape, since a catalog directory defaults to the workspace root and
// the cache lives beneath it. No lazy walk may ingest a cached artifact as an
// authored catalog entity, while authored entities in the same tree still load.
//
// Non-vacuity canary: the SAME fixture, walked by a lookup constructed with an
// EMPTY cache directory, DOES ingest all three cached artifacts — so each
// assertion below is earned by the skip and not green because a fixture failed
// to parse.
func TestLazyWalks_SkipArtifactCacheUnderCatalogDir(t *testing.T) {
	ws := t.TempDir()

	writeFixture(t, ws, "authored-component.yaml", authoredComponentYAML)
	writeCachedLibrary(t, ws, cacheUnderCatalogDir, "DbSizing:1.9.0", staleLibYAML)
	writeCachedLibrary(t, ws, cacheUnderCatalogDir, "BaseMappings:1.0.0", cachedMappingLibYAML)
	// A cached Component lands in the cache as an extensionless blob in
	// production, but nothing stops a slot from holding YAML — the walk skip
	// must not depend on the filename a slot happens to carry.
	writeCachedLibrary(t, ws, cacheUnderCatalogDir, "core/cached-comp:1.0.0", authoredComponentYAML)

	subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
	subject.buildRecipeIndex()
	if err := subject.buildSchemaLibraries(); err != nil {
		t.Fatalf("buildSchemaLibraries: %v", err)
	}
	subject.buildComponentIndex()

	if _, ok := subject.schemaLibraries["DbSizing"]; ok {
		t.Error("buildSchemaLibraries ingested a cached SchemaLibrary as authored catalog; the cache walk-skip failed")
	}
	if _, ok := subject.mappingLibraries["BaseMappings"]; ok {
		t.Error("buildRecipeIndex ingested a cached MappingLibrary as authored catalog; the cache walk-skip failed")
	}
	for identity, doc := range subject.componentIndex {
		if inCache(doc.path) {
			t.Errorf("buildComponentIndex indexed Component %q at cache path %s; the cache walk-skip failed", identity, doc.path)
		}
	}

	// The skip must not over-reach: the authored entity beside the cache loads.
	if _, ok := subject.componentIndex[entityIndexKey("example/authored-app", "1.0.0")]; !ok {
		t.Error("the authored Component was dropped; the cache skip over-reached")
	}

	// --- Non-vacuity canary: same tree, no cache directory configured. ---
	canary := NewLazyLookup(ws, "", []string{ws}, nil)
	canary.buildRecipeIndex()
	if err := canary.buildSchemaLibraries(); err != nil {
		t.Fatalf("canary buildSchemaLibraries: %v", err)
	}
	canary.buildComponentIndex()
	if _, ok := canary.schemaLibraries["DbSizing"]; !ok {
		t.Error("canary failed: an unconfigured-cache walk did NOT ingest the cached SchemaLibrary, so the skip assertion is vacuous")
	}
	if _, ok := canary.mappingLibraries["BaseMappings"]; !ok {
		t.Error("canary failed: an unconfigured-cache walk did NOT ingest the cached MappingLibrary, so the skip assertion is vacuous")
	}
	var canarySawCachePath bool
	for _, doc := range canary.componentIndex {
		if inCache(doc.path) {
			canarySawCachePath = true
		}
	}
	if !canarySawCachePath {
		t.Error("canary failed: an unconfigured-cache walk did NOT index the cached Component, so the skip assertion is vacuous")
	}
}

// TestLazyWalks_CachedVersionCollisionDoesNotBypassPin is the first consequence.
// Two versions of ONE SchemaLibrary name sit in the cache; only 1.10.0 is
// lockfile-pinned. The Tier-1 walk keys the library on its BARE NAME with no
// version and overwrites unconditionally, so the two slots collide and the
// LEXICALLY LAST one wins — "DbSizing:1.9.0" sorts after "DbSizing:1.10.0", so
// the stale, UNPINNED bytes take the name. The pinned Tier-2 fold that follows
// is first-win, so it finds the name already claimed and skips the one slot that
// would have been digest- and manifest-verified.
//
// The net effect is a pin bypass: the build resolves bytes no lockfile vouches
// for, and the recorded pin never gets to say otherwise.
func TestLazyWalks_CachedVersionCollisionDoesNotBypassPin(t *testing.T) {
	ws := t.TempDir()

	// The stale sibling: present in the cache, pinned by nothing.
	writeCachedLibrary(t, ws, cacheUnderCatalogDir, "DbSizing:1.9.0", staleLibYAML)
	// The pinned slot, written with its manifest so the verified read succeeds.
	pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "DbSizing:1.10.0", oci.MediaTypeSchemaLibrary, []byte(pinnedLibYAML))

	subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
	subject.SetPins(map[string]Pin{"DbSizing": pin})
	if err := subject.buildSchemaLibraries(); err != nil {
		t.Fatalf("buildSchemaLibraries: %v", err)
	}

	lib, ok := subject.schemaLibraries["DbSizing"]
	if !ok {
		t.Fatal("the lockfile-pinned SchemaLibrary did not resolve at all")
	}
	if lib.Metadata.Version != "1.10.0" {
		t.Errorf("resolved SchemaLibrary DbSizing at version %q, want the lockfile-pinned %q; an unpinned cache slot won the name and the verified slot was skipped",
			lib.Metadata.Version, "1.10.0")
	}
	if got := subject.entityBytes[entityBytesKey("SchemaLibrary", "DbSizing")]; !bytes.Equal(got, []byte(pinnedLibYAML)) {
		t.Errorf("retained SchemaLibrary bytes are not the digest-verified pinned bytes:\n got %q", string(got))
	}
}

// TestLazyWalks_CachedMappingLibraryDoesNotShadowAuthored is the second
// consequence. A cached MappingLibrary and an authored one share a name. The
// recipe-shell fold is first-win and a directory walk visits entries in lexical
// order, where "." precedes every letter and digit — so the cache directory is
// descended into FIRST and its copy claims the name. The authored file is then
// dropped and recorded as the shadowed one, which points the author at their own
// correct file as the problem.
func TestLazyWalks_CachedMappingLibraryDoesNotShadowAuthored(t *testing.T) {
	ws := t.TempDir()

	writeFixture(t, ws, "authored-mappings.yaml", authoredMappingLibYAML)
	authoredPath := filepath.Join(ws, "authored-mappings.yaml")
	cachedPath := writeCachedLibrary(t, ws, cacheUnderCatalogDir, "BaseMappings:1.0.0", cachedMappingLibYAML)

	subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
	subject.buildRecipeIndex()

	lib, ok := subject.mappingLibraries["BaseMappings"]
	if !ok {
		t.Fatal("the authored MappingLibrary did not resolve at all")
	}
	if lib.Metadata.Version != "2.0.0" {
		t.Errorf("resolved MappingLibrary BaseMappings at version %q, want the authored %q; the cached copy shadowed the authored file",
			lib.Metadata.Version, "2.0.0")
	}
	if _, shadowed := subject.recipeSkips[authoredPath]; shadowed {
		t.Errorf("the authored file %s was reported as the shadowed duplicate; the diagnostic blames the author's own file", authoredPath)
	}
	if _, ingested := subject.recipeSkips[cachedPath]; ingested {
		t.Errorf("the cached copy at %s was parsed and folded (then reported); it should never have been visited", cachedPath)
	}
}

// TestUnverifiedCachedSchemaLibraries_NamesOnlyWhatWentUnused covers the
// diagnostic's precision. Stepping over the cache turns a previously-silent
// success into a halt, and the halt is only survivable if it says where the
// library went and what to do — but it must not accuse a library that resolved
// perfectly well.
func TestUnverifiedCachedSchemaLibraries_NamesOnlyWhatWentUnused(t *testing.T) {
	ws := t.TempDir()

	writeCachedLibrary(t, ws, cacheUnderCatalogDir, "DbSizing:1.9.0", staleLibYAML)
	pin := writePinnedSlot(t, ws, cacheUnderCatalogDir, "Verified:1.0.0", oci.MediaTypeSchemaLibrary, []byte(verifiedLibYAML))

	subject := NewLazyLookup(ws, cacheUnderCatalogDir, []string{ws}, nil)
	subject.SetPins(map[string]Pin{"Verified": pin})
	if err := subject.buildSchemaLibraries(); err != nil {
		t.Fatalf("buildSchemaLibraries: %v", err)
	}
	if _, ok := subject.schemaLibraries["Verified"]; !ok {
		t.Fatal("the lockfile-pinned library did not resolve; the fixture is not measuring what it claims")
	}

	got := subject.unverifiedCachedSchemaLibraries()
	want := []string{`SchemaLibrary "DbSizing"`}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("unverifiedCachedSchemaLibraries() = %v, want %v; a library that resolved must never be named as unused", got, want)
	}

	rem := cachedLibraryRemedy(got)
	for _, must := range []string{`SchemaLibrary "DbSizing"`, "aggrega pull --release", "lockfile"} {
		if !strings.Contains(rem, must) {
			t.Errorf("the remedy does not mention %q, so the halt does not tell the author how to recover:\n%s", must, rem)
		}
	}
	if strings.Contains(rem, "Verified") {
		t.Errorf("the remedy names a library that resolved:\n%s", rem)
	}
	if cachedLibraryRemedy(nil) != "" {
		t.Error("an empty finding set must produce no remedy line")
	}
}

// TestValidateClosurePillars_RemedyOnlyConsultedOnFailure pins the cost
// contract: the remedy provider reads the filesystem, so a clean validation must
// never call it.
func TestValidateClosurePillars_RemedyOnlyConsultedOnFailure(t *testing.T) {
	called := false
	probe := func() string {
		called = true
		return "remediation: probe"
	}

	if err := ValidateClosurePillars(nil, nil, nil, nil, nil, probe); err != nil {
		t.Fatalf("empty closure should validate: %v", err)
	}
	if called {
		t.Error("the remedy provider ran on a clean validation; it reads the filesystem and must only run on the failing path")
	}

	// A valid Component + library pair, then validated against an EMPTY library
	// map — the shape a reference to a library that never resolved produces.
	dir := t.TempDir()
	writeFixture(t, dir, "library.yaml", verifiedLibYAML)
	writeFixture(t, dir, "consumer.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/needs-lib
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Verified#/schemas/Storage"
`)
	cat, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("eager LoadAll of a valid fixture: %v", err)
	}
	verr := ValidateClosurePillars(cat.Components, cat.componentSources, nil, nil, nil, probe)
	if verr == nil {
		t.Fatal("a $ref to an absent library must fail closure pillars")
	}
	if !called {
		t.Error("the remedy provider was not consulted on the failing path")
	}
	if !strings.Contains(verr.Error(), "remediation: probe") {
		t.Errorf("the remedy is absent from the rendered message, so the diagnostic surface will not show it:\n%s", verr.Error())
	}
}

// inCache reports whether a walked path lies under the artifact cache directory
// used by the fixtures above.
func inCache(path string) bool {
	return bytes.Contains([]byte(filepath.ToSlash(path)), []byte("/"+cacheUnderCatalogDir+"/"))
}
