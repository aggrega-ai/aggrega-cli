// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// orasPullerAdapter wraps an in-memory oras Target so it satisfies
// catalog.OCIPuller. Production wires this through a remote.Repository;
// tests use the same shape over an in-memory CAS.
type orasPullerAdapter struct{ target *memory.Store }

func (a *orasPullerAdapter) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	return oci.Pull(ctx, a.target, tag)
}

// RegistryHost lets the adapter satisfy the puller's registry-reporting seam so a
// bare plugin identity binds to a physical location during resolution. The
// in-memory store ignores the reference (it keys on the tag alone), so the exact
// host is immaterial to the fetch — it only needs to be a well-formed registry.
func (a *orasPullerAdapter) RegistryHost() string { return "hub.aggrega.ai" }

func writeYAMLToCatalog(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestClosureLookupResolvesEntitiesFromCatalog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	compBody := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`
	writeYAMLToCatalog(t, dir, "component/core/api-server/api-server.yaml", compBody)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")

	res, err := lookup.ResolveCatalogEntity(context.Background(), "api-server:1.0.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity: %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-1 hit; got NotFound")
	}
	if res.ResolvedVersion != "1.0.0" {
		t.Errorf("ResolvedVersion: got %q; want %q", res.ResolvedVersion, "1.0.0")
	}
	// Golden literal (not a re-derivation): the exact content-address of
	// compBody. Guards the digest format itself — a spelling change in
	// pkg/digest.ContentDigest fails here loudly instead of being mirrored.
	const wantCompDigest = "sha256:63a6f93856eff3633dba32c5d7c1d92236ee48930a4c7fbe4bce2f63bddb19fd"
	if res.Digest != wantCompDigest {
		t.Errorf("Digest: got %q; want %q", res.Digest, wantCompDigest)
	}
	if !strings.Contains(res.ResolutionPath, "tier-1") {
		t.Errorf("ResolutionPath: got %q; want tier-1", res.ResolutionPath)
	}
}

// TestClosureLookupRoutesRecipeURIToMetadataName pins that
// ResolveCatalogEntity finds Recipe entries via their metadata.name
// (the form Manifest mappings pass to the closure walker as recipe
// URIs) — NOT via spec.implements. The Recipe's metadata.name and
// spec.implements are deliberately distinct in this fixture to catch
// any regression that re-introduces spec.implements-keyed Tier-1
// lookups, which would silently miss every Recipe URI the closure
// walker actually produces.
func TestClosureLookupRoutesRecipeURIToMetadataName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recipeBody := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: 2.5.0
spec:
  capability: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      app:
        image: x:1
        ports:
          web: 80
    services:
      svc:
        type: ClusterIP
        ports:
          web: 80
`
	writeYAMLToCatalog(t, dir, "recipes/server.yaml", recipeBody)

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")

	// metadata.name form — what Manifest mappings produce. MUST resolve.
	res, err := lookup.ResolveCatalogEntity(context.Background(), "server-recipe:2.5.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity(metadata.name URI): %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-1 hit on Recipe metadata.name URI; got NotFound")
	}
	if res.ResolvedVersion != "2.5.0" {
		t.Errorf("ResolvedVersion: got %q; want %q", res.ResolvedVersion, "2.5.0")
	}
	// Golden literal (not a re-derivation): the exact content-address of
	// recipeBody, guarding the digest format.
	const wantRecipeDigest = "sha256:1e2507fcdc810835bad282236c776c987c13305a43ec96b70da971ab1118eb06"
	if res.Digest != wantRecipeDigest {
		t.Errorf("Digest: got %q; want %q", res.Digest, wantRecipeDigest)
	}

	// spec.implements form — the COMPONENT-type URI, not a Recipe URI.
	// The closure walker never asks ResolveCatalogEntity for this
	// against the Recipe; it asks for the Component (which is absent
	// in this fixture). Returns NotFound so the walker can surface
	// the missing-Component diagnostic correctly.
	res, err = lookup.ResolveCatalogEntity(context.Background(), "core/server:2.5.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity(spec.implements URI): %v", err)
	}
	if !res.NotFound {
		t.Errorf("spec.implements URI should NOT resolve via Recipe lookup; got %+v", res)
	}
}

func TestClosureLookupFallsBackToOCIPullerOnCacheMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// A valid Component: the registry tier puts a fetched body through its kind's
	// schema before it caches it, so the served document must be one its schema admits.
	compBody := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: api-server\n  version: 2.0.0\n" +
		"  labels:\n    component.aggrega.ai/type: server\nspec: {}\n")

	store := memory.New()
	if _, err := oci.Push(ctx, store, "2.0.0", oci.MediaTypeComponent, compBody, nil); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}

	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")

	res, err := lookup.ResolveCatalogEntity(context.Background(), "hub.aggrega.ai/component/core/api-server:2.0.0", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity: %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-3 hit; got NotFound")
	}
	// Golden literal (not a re-derivation): the exact content-address of the
	// version-2.0.0 compBody, guarding the digest format.
	const wantComp2Digest = "sha256:347ccb4ba0cbbd569133e338f62b59b1627c19011b267d252d03b38fad7c9eba"
	if res.Digest != wantComp2Digest {
		t.Errorf("Digest: got %q; want %q", res.Digest, wantComp2Digest)
	}
	if res.ResolvedVersion != "2.0.0" {
		t.Errorf("ResolvedVersion: got %q; want %q", res.ResolvedVersion, "2.0.0")
	}

	// Verify cache slot was populated as <slot>/blob for non-library mediatypes.
	slot := mustCachePath(dir, ".aggrega/cache", "hub.aggrega.ai/component/core/api-server:2.0.0", "2.0.0")
	if _, err := os.Stat(filepath.Join(slot, "blob")); err != nil {
		t.Errorf("expected cache slot /blob to exist; got %v", err)
	}
}

func TestClosureLookupNilPullerReturnsNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	lookup := cat.ClosureLookup(nil, dir, ".aggrega/cache")
	res, err := lookup.ResolveCatalogEntity(context.Background(), "missing/component:9.9.9", "")
	if err != nil {
		t.Fatalf("ResolveCatalogEntity: %v", err)
	}
	if !res.NotFound {
		t.Errorf("expected NotFound with nil puller; got %+v", res)
	}
}

// pulled SchemaLibrary
// artifacts land at <slot>/library.yaml so the catalog walker's
// canonical per-version layout (matching the local-authoring shape)
// is preserved on the pull side.
func TestPullWritesSchemaLibraryAsLibraryYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	libBody := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Probes
  version: 1.0.0
spec:
  schemas:
    LivenessProbe:
      type: object
`)
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeSchemaLibrary, libBody, nil); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}

	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")
	res, err := lookup.ResolveLibrary(context.Background(), closure.LibraryAxisNamed("SchemaLibraries"), "hub.aggrega.ai/schemalibrary/core/Probes:1.0.0")
	if err != nil {
		t.Fatalf("ResolveSchemaLibrary: %v", err)
	}
	if res.NotFound {
		t.Fatal("expected Tier-3 hit; got NotFound")
	}

	slot := mustCachePath(dir, ".aggrega/cache", "hub.aggrega.ai/schemalibrary/core/Probes:1.0.0", "1.0.0")
	libYAML := filepath.Join(slot, "library.yaml")
	got, err := os.ReadFile(libYAML)
	if err != nil {
		t.Fatalf("expected cache slot /library.yaml: %v", err)
	}
	if string(got) != string(libBody) {
		t.Errorf("library.yaml content mismatch:\n got %q\n want %q", got, libBody)
	}

	// No blob filename in the slot (library kinds use library.yaml exclusively).
	if _, err := os.Stat(filepath.Join(slot, "blob")); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no /blob filename for library slot; got err=%v", err)
	}
}

func TestPullWritesMappingLibraryAsLibraryYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	libBody := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: 1.0.0
spec:
  mappings:
    SimpleValues: 'image: {{ .Values.image }}'
`)
	store := memory.New()
	if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeMappingLibrary, libBody, nil); err != nil {
		t.Fatalf("oci.Push: %v", err)
	}

	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")
	if _, err := lookup.ResolveLibrary(context.Background(), closure.LibraryAxisNamed("MappingLibraries"), "hub.aggrega.ai/mappinglibrary/core/BaseMappings:1.0.0"); err != nil {
		t.Fatalf("ResolveMappingLibrary: %v", err)
	}

	slot := mustCachePath(dir, ".aggrega/cache", "hub.aggrega.ai/mappinglibrary/core/BaseMappings:1.0.0", "1.0.0")
	got, err := os.ReadFile(filepath.Join(slot, "library.yaml"))
	if err != nil {
		t.Fatalf("expected cache slot /library.yaml: %v", err)
	}
	if string(got) != string(libBody) {
		t.Errorf("library.yaml content mismatch")
	}
}

// TestResolvePluginVersionTagRoundTrip drives the bare-identity plugin-artifact
// resolution: a Plugin published as ONE manifest (entity config + inline .wasm
// layer) tagged by its version resolves through ResolvePlugin's version-tag path,
// the .wasm layer is selected, the content pin is DERIVED from its bytes, the slot
// lands keyed by that derived pin, an offline re-resolve honoring the recorded pin
// hits the cache, and a tampered cache slot halts with ErrFetchIntegrity.
func TestResolvePluginVersionTagRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wasmBytes := []byte("\x00asm\x01\x00\x00\x00 version-tag round-trip fixture")
	entityYAML := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\n")
	contentDigest := digest.ContentDigest(wasmBytes)
	const identity = "frp/patch"
	const version = "1.0.0"
	artifactURI := identity

	store := memory.New()
	// Publish the ONE plugin manifest — entity config + inline .wasm layer —
	// tagged by the entity's version.
	if _, err := oci.PushPluginManifest(ctx, store, version, entityYAML, wasmBytes, nil); err != nil {
		t.Fatalf("oci.PushPluginManifest: %v", err)
	}

	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")

	// First resolution: pull the manifest by version, select the .wasm layer,
	// derive the content pin, cache both blobs.
	res, err := lookup.ResolvePlugin(context.Background(), artifactURI, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (pull): %v", err)
	}
	if res.NotFound {
		t.Fatal("expected a tier-3 pull hit; got NotFound")
	}
	if res.ResolutionPath != "tier-3-pull" {
		t.Errorf("ResolutionPath = %q; want %q", res.ResolutionPath, "tier-3-pull")
	}
	if res.Digest != contentDigest {
		t.Errorf("Digest = %q; want the derived content pin %q", res.Digest, contentDigest)
	}
	if res.ResolvedVersion != contentDigest {
		t.Errorf("ResolvedVersion = %q; want %q", res.ResolvedVersion, contentDigest)
	}
	if res.ManifestDigest == "" {
		t.Error("ManifestDigest is empty; want the manifest digest the signature commits to")
	}

	// The cache slot the build re-reads is keyed by (artifactURI, contentDigest)
	// — the (uri, version) pair the build derives from the lockfile pin.
	cached, err := oci.ReadCached(dir, ".aggrega/cache", artifactURI, contentDigest)
	if err != nil {
		t.Fatalf("expected .wasm in cache after pull: %v", err)
	}
	if string(cached) != string(wasmBytes) {
		t.Errorf("cached .wasm bytes differ from published bytes")
	}

	// Second resolution offline, honoring the recorded pin: a cache hit. A
	// digest-free reference addresses the cache through the lockfile pin (the
	// content digest is not in the URI), so the reuse carries it.
	reuse := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{
		artifactURI: {ResolvedVersion: contentDigest, Digest: contentDigest, ManifestDigest: res.ManifestDigest},
	}}
	cachedLookup := cat.ClosureLookupWithReuse(nil, dir, ".aggrega/cache", nil, nil, reuse)
	res2, err := cachedLookup.ResolvePlugin(context.Background(), artifactURI, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (cache): %v", err)
	}
	if res2.NotFound {
		t.Fatal("expected a cache hit on the second resolve; got NotFound")
	}
	if res2.Digest != contentDigest || res2.ResolvedVersion != contentDigest {
		t.Errorf("cache-hit resolution = %+v; want digest/version %q", res2, contentDigest)
	}

	// Tamper: corrupt the cached blob; the content check must halt with
	// ErrFetchIntegrity rather than return drifted bytes.
	slot := mustCachePath(dir, ".aggrega/cache", artifactURI, contentDigest)
	if err := os.WriteFile(filepath.Join(slot, "blob"), []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper write: %v", err)
	}
	if _, err := cachedLookup.ResolvePlugin(context.Background(), artifactURI, version); !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("tampered cache: err = %v; want ErrFetchIntegrity", err)
	}
}

// TestResolvePluginManifestSelfVerify proves the offline manifest self-verify:
// a content-pin .wasm pull writes BOTH a content blob and a manifest blob into
// the same slot; a faithful cached manifest re-hashes to the pinned manifest
// digest and PASSES on the offline (nil-puller) read; a tampered cached manifest
// FAILS loudly with ErrCachedManifestIntegrity; and an incomplete slot (content
// blob, no manifest blob) is treated as a cache miss — offline that surfaces
// NotFound (pull-first), online it re-pulls and repopulates both blobs.
func TestResolvePluginManifestSelfVerify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wasmBytes := []byte("\x00asm\x01\x00\x00\x00 manifest self-verify fixture")
	entityYAML := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/patch\n  version: 1.0.0\n")
	contentDigest := digest.ContentDigest(wasmBytes)
	const identity = "frp/patch"
	const version = "1.0.0"
	artifactURI := identity

	store := memory.New()
	if _, err := oci.PushPluginManifest(ctx, store, version, entityYAML, wasmBytes, nil); err != nil {
		t.Fatalf("oci.PushPluginManifest: %v", err)
	}

	// Tier-3 pull writes both blobs.
	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")
	res, err := lookup.ResolvePlugin(context.Background(), artifactURI, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (pull): %v", err)
	}
	manifestBytes, err := oci.ReadCachedManifest(dir, ".aggrega/cache", artifactURI, contentDigest)
	if err != nil {
		t.Fatalf("expected a manifest blob in cache after pull: %v", err)
	}
	// The stored manifest re-hashes to the recorded manifest digest — the exact
	// computation the offline verify performs.
	if got := digest.ContentDigest(manifestBytes); got != res.ManifestDigest {
		t.Errorf("cached manifest digest %s != resolution ManifestDigest %s", got, res.ManifestDigest)
	}

	// Faithful cached manifest verifies on the offline read that honors the pin.
	// A digest-free reference addresses the cache through the lockfile pin.
	reuse := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{
		artifactURI: {ResolvedVersion: contentDigest, Digest: contentDigest, ManifestDigest: res.ManifestDigest},
	}}
	offline := cat.ClosureLookupWithReuse(nil, dir, ".aggrega/cache", nil, nil, reuse)
	res2, err := offline.ResolvePlugin(context.Background(), artifactURI, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (offline, faithful): %v", err)
	}
	if res2.NotFound {
		t.Fatal("faithful cached manifest: got NotFound; want a cache hit")
	}
	if res2.ManifestDigest != res.ManifestDigest {
		t.Errorf("offline ManifestDigest = %s; want %s", res2.ManifestDigest, res.ManifestDigest)
	}

	// Tamper the manifest blob: flip its content. With the prior lockfile's
	// manifest digest as the independent pin, the offline read HALTS with
	// ErrCachedManifestIntegrity — the stored manifest no longer matches the pin
	// it is anchored to (not a digest re-derived from the cached content).
	slot := mustCachePath(dir, ".aggrega/cache", artifactURI, contentDigest)
	if err := os.WriteFile(filepath.Join(slot, "manifest"), []byte("tampered-manifest"), 0o644); err != nil {
		t.Fatalf("tamper manifest write: %v", err)
	}
	if _, err := offline.ResolvePlugin(context.Background(), artifactURI, version); !errors.Is(err, oci.ErrCachedManifestIntegrity()) {
		t.Errorf("tampered manifest (reuse pin): err = %v; want ErrCachedManifestIntegrity", err)
	}

	// Incomplete slot (content blob, no manifest blob): offline resolves to
	// NotFound (cache miss forces re-pull); the content blob is left intact.
	if err := os.Remove(filepath.Join(slot, "manifest")); err != nil {
		t.Fatalf("remove manifest blob: %v", err)
	}
	resMissing, err := offline.ResolvePlugin(context.Background(), artifactURI, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (offline, incomplete slot): %v", err)
	}
	if !resMissing.NotFound {
		t.Errorf("incomplete slot offline: resolution = %+v; want NotFound", resMissing)
	}

	// The same incomplete slot re-pulls online and repopulates the manifest blob.
	online := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")
	if _, err := online.ResolvePlugin(context.Background(), artifactURI, version); err != nil {
		t.Fatalf("ResolvePlugin (online repopulate): %v", err)
	}
	if _, err := oci.ReadCachedManifest(dir, ".aggrega/cache", artifactURI, contentDigest); err != nil {
		t.Errorf("expected manifest blob repopulated after online re-pull: %v", err)
	}
}

// TestResolvePluginManifestAuthorityBeatsForgedContentPin is the plugin tamper
// proof: an attacker forges the .wasm content AND the content pin that names it
// (the spec.plugin @sha256 the slot is keyed by), so the content-vs-pin check
// would pass — but the persisted manifest, an independent record of the original
// content, still halts the read. The manifest is a root of trust beyond the
// content pin.
func TestResolvePluginManifestAuthorityBeatsForgedContentPin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wasmBytes := []byte("\x00asm\x01\x00\x00\x00 authority fixture")
	entityYAML := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: frp/authority\n  version: 1.0.0\n")
	origDigest := digest.ContentDigest(wasmBytes)
	const identity = "frp/authority"
	const version = "1.0.0"
	artifactURI := identity

	store := memory.New()
	if _, err := oci.PushPluginManifest(ctx, store, version, entityYAML, wasmBytes, nil); err != nil {
		t.Fatalf("oci.PushPluginManifest: %v", err)
	}
	// A faithful pull populates the slot and gives us the authoritative manifest.
	lookup := cat.ClosureLookup(&orasPullerAdapter{target: store}, dir, ".aggrega/cache")
	res, err := lookup.ResolvePlugin(context.Background(), artifactURI, version)
	if err != nil {
		t.Fatalf("ResolvePlugin (pull): %v", err)
	}
	origManifest, err := oci.ReadCachedManifest(dir, ".aggrega/cache", artifactURI, origDigest)
	if err != nil {
		t.Fatalf("read original manifest: %v", err)
	}

	// The attacker forges the content AND a matching content pin — the lockfile
	// Digest they control equals the forged bytes, so the slot key and the
	// content-vs-pin defense-in-depth check both agree with the forgery — but
	// reuses the ORIGINAL manifest (the record they cannot re-forge without the
	// independent lockfile manifest digest).
	forged := []byte("\x00asm\x01\x00\x00\x00 FORGED artifact body")
	forgedDigest := digest.ContentDigest(forged)
	if err := oci.WriteCached(dir, ".aggrega/cache", artifactURI, forgedDigest, forged); err != nil {
		t.Fatalf("forge content write: %v", err)
	}
	if err := oci.WriteCachedManifest(dir, ".aggrega/cache", artifactURI, forgedDigest, origManifest); err != nil {
		t.Fatalf("plant original manifest: %v", err)
	}

	// The lockfile pin the offline read honors: the forged content digest (attacker
	// controlled), but the GENUINE manifest digest (the independent root of trust).
	reuse := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{
		artifactURI: {ResolvedVersion: forgedDigest, Digest: forgedDigest, ManifestDigest: res.ManifestDigest},
	}}
	offline := cat.ClosureLookupWithReuse(nil, dir, ".aggrega/cache", nil, nil, reuse)
	if _, err := offline.ResolvePlugin(context.Background(), artifactURI, version); !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("forged content + forged content pin: err = %v; want ErrFetchIntegrity from the manifest-authority check", err)
	}
}
