// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"oras.land/oras-go/v2/content/memory"
)

// fakeRegistryTrust is the trust stance a fixture must declare when its registry
// tier is a fake puller rather than a real registry: verification is skipped for
// that one host.
//
// It is required, not optional. A library or entity the fake puller resolves is a
// registry-resolved (Tier-3) reference like any other, so the pull-time verify pass
// discovers its signature referrers at the real host bound from default_registry —
// which the fake puller does not serve. Without this stance the fixture reaches out
// to a host it never published to and the pull halts on a referrer it cannot fetch;
// worse, an untagged test that does reach the network is a test whose result depends
// on what a remote host happens to answer.
//
// The stance is per-host rather than the global switch, so the fixture silences
// exactly the host it fakes and the global opt-out's own loud warning stays
// meaningful. Library signature verification and marker discovery are proven
// against in-memory stores that DO serve referrers, so scoping it off here costs no
// coverage.
const fakeRegistryTrust = "registries:\n  - host: hub.aggrega.ai\n    skip_signature_verification: true\n"

// ociManifestArtifact round-trips body through an in-memory CAS to obtain the OCI
// manifest bytes and digest a real registry pull persists — the manifest whose
// single layer commits to body's content digest, and whose own content digest is
// the returned manifest digest. Fixture pullers and cache seeders attach the
// bytes so a cached entity slot is the complete manifest-plus-content slot the
// verifying read requires; a manifest-less slot for an always-OCI entity reads as
// a fail-closed miss. Returns an error (rather than taking *testing.T) so puller
// methods, which cannot fail a test directly, can propagate it.
func ociManifestArtifact(mediaType string, body []byte) (manifestBytes []byte, manifestDigest string, err error) {
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, "seed", mediaType, body, nil); err != nil {
		return nil, "", err
	}
	res, err := oci.Pull(ctx, store, "seed")
	if err != nil {
		return nil, "", err
	}
	return res.ManifestBytes, res.Digest, nil
}

// recordingSourceResolver is a stub closure.SourceResolver that records the
// source version it is asked to resolve (the value the source: template
// evaluated to against the materialized pillars) and returns a deterministic
// resolved artifact without a real fetch.
type recordingSourceResolver struct{ gotVersion string }

func (r *recordingSourceResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	r.gotVersion = spec.Version
	return &closure.ResolvedSource{
		Kind:          spec.Kind,
		URL:           spec.URL,
		PinnedVersion: spec.Version,
		Digest:        "sha256:" + strings.Repeat("a", 64),
		Mediatype:     "application/vnd.test",
	}, nil
}

// fakeRemoteLibraryPuller is an in-memory LibraryPuller: it serves the DbSizing
// SchemaLibrary at a fixed set of registry tags without touching the network. A
// pure bare-name reference (no `@version` floor) drives the version-discovery
// path — the puller
// is asked for the version, lists its tags, and serves the highest; the served
// body carries that resolved version as metadata.version so the pin and the
// build-time resolution agree.
type fakeRemoteLibraryPuller struct {
	tags           []string
	listTagsCalled bool
	// metadataVersion, when set, is the served body's metadata.version — DELIBERATELY
	// different from the resolved tag, to exercise the identity-not-tag pinning rule.
	// Empty means the body's version equals the resolved tag (the common case).
	metadataVersion string
}

func (p *fakeRemoteLibraryPuller) Pull(_ context.Context, _, _ string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound() // library resolution uses PullLibrary
}

func (p *fakeRemoteLibraryPuller) PullLibrary(_ context.Context, _, _, floorVersion string) (*oci.PullResult, string, error) {
	version := floorVersion
	if version == "" {
		p.listTagsCalled = true
		for _, t := range p.tags {
			if version == "" || t > version {
				version = t
			}
		}
	}
	bodyVersion := version
	if p.metadataVersion != "" {
		bodyVersion = p.metadataVersion
	}
	body := []byte(fmt.Sprintf("apiVersion: aggrega.ai/v1alpha1\n"+
		"kind: SchemaLibrary\nmetadata:\n  name: DbSizing\n  version: \"%s\"\n"+
		"spec:\n  schemas:\n    Storage:\n      size: { type: string, default: \"10Gi\" }\n", bodyVersion))
	// Return the resolved TAG (version); the body carries bodyVersion. When they
	// differ, the identity rule must pin bodyVersion (metadata.version), not the tag.
	// The manifest bytes mirror a real pull so the persisted slot is complete.
	mb, md, err := ociManifestArtifact(oci.MediaTypeSchemaLibrary, body)
	if err != nil {
		return nil, "", err
	}
	return &oci.PullResult{MediaType: oci.MediaTypeSchemaLibrary, Bytes: body, Digest: md, ManifestBytes: mb}, version, nil
}

// TestBuildResolvesRemoteLibraryTagVersionMismatch is the library identity proof:
// a registry-only library served under an OCI tag (2.0.0) DIFFERENT from its
// body's metadata.version (1.0.0) must pin metadata.version, and the build-side
// recompute must re-derive the same — so verify passes with no drift. The
// existing equal-tag tests are the regression; this is the case the
// tag==metadata.version fixture convention skipped.
func TestBuildResolvesRemoteLibraryTagVersionMismatch(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\nauto_pull: false\n"+fakeRegistryTrust)
	catalogDir := filepath.Join(root, "catalog")
	if err := os.Remove(filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml")); err != nil {
		t.Fatalf("remove local library: %v", err)
	}

	puller := &fakeRemoteLibraryPuller{tags: []string{"2.0.0"}, metadataVersion: "1.0.0"}
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, puller: puller}); err != nil {
		t.Fatalf("pullRun (library tag!=version): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var pinned string
	for _, s := range lf.SchemaLibraries {
		if s.URI == "DbSizing" {
			pinned = s.ResolvedVersion
		}
	}
	if pinned != "1.0.0" {
		t.Fatalf("library must pin its metadata.version (1.0.0), not the served tag (2.0.0); got %q", pinned)
	}

	// Build re-derives 1.0.0 via the bridge → tier-1; verify must pass (no drift),
	// and the library-declared default still materializes.
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild (library tag!=version) must succeed with no drift, got: %v", err)
	}
	if got := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(got, "10Gi") {
		t.Errorf("library default did not materialize; got:\n%s", got)
	}
}

// TestPullFetchesRemoteLibraryViaTier3 is the real-pull end-to-end: a Component
// references DbSizing by pure bare-name identity, the library is NOT local, and
// `aggrega pull` fetches it through the puller — the version-discovery path (no floor → list-tags
// → highest), AOT-safe because the version resolution happens at pull and is
// pinned. The build then bridges the pinned-and-cached library and materializes
// its default. Unlike the constructed-post-pull-state e2e, the cache + lockfile
// here are produced by an actual puller fetch.
func TestPullFetchesRemoteLibraryViaTier3(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	catalogDir := filepath.Join(root, "catalog")

	// Make DbSizing genuinely remote: remove the local copy so only a Tier-3
	// fetch through the puller can resolve it.
	if err := os.Remove(filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml")); err != nil {
		t.Fatalf("remove local library: %v", err)
	}

	puller := &fakeRemoteLibraryPuller{tags: []string{"1.0.0", "2.0.0"}}
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, puller: puller}); err != nil {
		t.Fatalf("pullRun (Tier-3 remote fetch): %v", err)
	}

	// A pure bare-name reference carried no floor, so the puller listed tags and
	// resolved the highest (2.0.0 over 1.0.0).
	if !puller.listTagsCalled {
		t.Error("expected the list-tags version-discovery path (no @version floor)")
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var pinned string
	for _, s := range lf.SchemaLibraries {
		if s.URI == "DbSizing" {
			pinned = s.ResolvedVersion
		}
	}
	if pinned != "2.0.0" {
		t.Errorf("lockfile should pin the highest tag (2.0.0) resolved at pull; got %q", pinned)
	}

	// The remote-fetched, pinned library bridges at build (no network) and its
	// declared default materializes in the rendered output.
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild after remote fetch: %v", err)
	}
	if got := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(got, "10Gi") {
		t.Errorf("remote-fetched library default should materialize; got:\n%s", got)
	}
}

// remoteLibraryFixture authors a buildable workspace whose `server` Component
// pulls an infra field from a SchemaLibrary by a host-less bare-name $ref
// (`DbSizing#/schemas/Storage`). The library is authored LOCALLY here so an
// initial `pullRun` produces a real, matching lockfile that pins it host-less;
// the caller then relocates the library to the content cache (and deletes the
// local copy) to construct the post-pull state a remote-pulled library would
// have. Returns the release path.
func remoteLibraryFixture(t *testing.T, root string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: smoke-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
`)
	// The server Component's infra.storage field is sourced by a host-less
	// bare-name cross-document $ref into the DbSizing SchemaLibrary; the library
	// entry declares the default the build must materialize.
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
    infra:
      replicas: { type: integer, default: 1 }
      storage:
        $ref: "DbSizing#/schemas/Storage"
`)
	writeFixture(t, filepath.Join(catalogRoot, "schemalibraries", "dbsizing.yaml"), dbSizingLibrarySource)
	writeFixture(t, filepath.Join(catalogRoot, "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      server:
        image: "test-server:{{ .infra.storage.size }}"
        ports:
          web: 8080
`)
	return filepath.Join(root, "release.yaml")
}

const dbSizingLibrarySource = `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: DbSizing
  version: "1.0.0"
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`

// relocateLibraryToCache moves the DbSizing library from the local catalog into
// the content cache, replicating the post-pull state of a remote-pulled library:
// present in the cache under the library layout (library.yaml at
// CachePath(<name>:<version>)), pinned in the lockfile, ABSENT from the local
// catalog. The cache layout (library.yaml at this exact path/key) mirrors the
// puller's writeEntityToCacheSlot; the bridge's read key symmetry is pinned by
// the catalog-package TestReadPinnedLibrary, which exercises the real primitive.
func relocateLibraryToCache(t *testing.T, root, cacheDir string) {
	t.Helper()
	localPath := filepath.Join(root, "catalog", "schemalibraries", "dbsizing.yaml")
	if err := os.Remove(localPath); err != nil {
		t.Fatalf("remove local library: %v", err)
	}
	slot := oci.CachePath(root, cacheDir, "DbSizing:1.0.0", "1.0.0")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir cache slot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(slot, "library.yaml"), []byte(dbSizingLibrarySource), 0o644); err != nil {
		t.Fatalf("write cache slot: %v", err)
	}
	// Persist the OCI manifest committing to the library body, mirroring the real
	// pull's writeEntityToCacheSlot: a manifest-less slot reads as a fail-closed miss.
	mb, _, err := ociManifestArtifact(oci.MediaTypeSchemaLibrary, []byte(dbSizingLibrarySource))
	if err != nil {
		t.Fatalf("build library manifest: %v", err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, "DbSizing:1.0.0", "1.0.0", mb); err != nil {
		t.Fatalf("write cache manifest: %v", err)
	}
}

// TestBuildResolvesRemotePinnedSchemaLibrary is the headline remote-library
// behavior: a build
// resolves a SchemaLibrary that is pinned in the lockfile + present in the
// content cache but ABSENT from the local catalog, via the build-side bridge, and
// the library-declared default materializes in the rendered output.
func TestBuildResolvesRemotePinnedSchemaLibrary(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	// Initial pull with the library authored locally → a real lockfile pinning it.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	// D — host-less pin: the lockfile pins the library by its bare-name identity,
	// no registry host.
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var pinned bool
	for _, s := range lf.SchemaLibraries {
		if s.URI == "DbSizing" {
			pinned = true
			if strings.Contains(s.URI, "://") || strings.Contains(s.URI, "/") {
				t.Errorf("library pin URI %q carries a host/path; want host-less name-path", s.URI)
			}
			if s.ResolvedVersion != "1.0.0" {
				t.Errorf("library pin version = %q; want 1.0.0", s.ResolvedVersion)
			}
		}
	}
	if !pinned {
		t.Fatalf("lockfile did not pin DbSizing; libraries=%+v", lf.SchemaLibraries)
	}

	// Relocate the library to the cache + delete the local copy: now only the
	// build-side bridge can resolve the consumer's $ref.
	relocateLibraryToCache(t, root, cacheDir)

	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild with bridged remote library: %v", err)
	}

	// The library-declared default (size: 10Gi) materialized into the rendered
	// output via the bridged $ref.
	got := readDistTree(t, filepath.Join(root, "dist"))
	if !strings.Contains(got, "10Gi") {
		t.Errorf("rendered output should carry the bridged library default 10Gi; got:\n%s", got)
	}
}

// TestBuildWithoutBridgeFailsUnresolvedRef is the non-vacuity companion: with the
// library neither local nor pinned, the consumer's $ref is unresolvable and the
// build halts — proving the bridge (not some other path) is what resolves it.
func TestBuildWithoutBridgeFailsUnresolvedRef(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	catalogDir := filepath.Join(root, "catalog")

	// Remove the local library WITHOUT pinning/caching it, and hand the build a
	// lockfile so it reaches catalog load. The $ref cannot resolve.
	if err := os.Remove(filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml")); err != nil {
		t.Fatalf("remove local library: %v", err)
	}
	if err := lockfile.WriteLockfile(filepath.Join(root, "release.lock"), &lockfile.Lockfile{Version: 1}); err != nil {
		t.Fatalf("write empty lockfile: %v", err)
	}

	err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected build to halt on unresolved cross-document $ref; got nil")
	}
}

// TestBuildHaltsOnTamperedBridgedLibrary is the integrity gate for the library
// axis: with a remote library bridged into the build, mutating its pinned digest
// in the PRE-EXISTING lockfile makes the build halt LOUDLY. For a bridged library
// the digest is verified at the cache-read boundary (the 09f-pre verify-on-read,
// threaded through F via ReadPinnedLibrary), so a tampered pin surfaces as
// ErrFetchIntegrity BEFORE the lockfile-drift verify runs — the library-axis
// integrity check is preserved, just at the bridge rather than the drift verify.
// (The entity-axis lockfile-drift gate, ErrLockfileDrift, is covered by
// TestBuildVerifiesLockfileAndHaltsOnDrift and still runs against the bridged
// catalog; the e2e's clean build proves the drift verify discovers + matches the
// bridged library — no spurious drift.) Built on a pre-existing, then-mutated
// lockfile so the gate actually fires (an auto-pulled lockfile would not be
// tampered). Non-vacuous: revert the mutation → the e2e builds clean.
func TestBuildHaltsOnTamperedBridgedLibrary(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}
	relocateLibraryToCache(t, root, cacheDir)

	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var mutated bool
	for i := range lf.SchemaLibraries {
		if lf.SchemaLibraries[i].URI == "DbSizing" {
			lf.SchemaLibraries[i].Digest = "sha256:" + strings.Repeat("0", 64)
			mutated = true
		}
	}
	if !mutated {
		t.Fatal("fixture lockfile did not pin DbSizing to mutate")
	}
	if err := lockfile.WriteLockfile(filepath.Join(root, "release.lock"), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	err = dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected the build to halt on the tampered library pin; got nil")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("error should wrap ErrFetchIntegrity (verify-on-read at the bridge); got %v", err)
	}
}

// TestPullSourceTemplateReadsBridgedLibraryDefault proves the pull-side
// bridge closes the lock==build regression: a Recipe's source: version reads a
// library-$ref-composed infra default (`.infra.storage.size`), with the library
// remote-pinned. Pull bridges the discovered library BEFORE source-descent, so
// the default materializes and the source template resolves to it — the identical
// value build computes from the same bridged catalog (pull-identity ==
// build-identity, no false ErrFetchIntegrity).
//
// Non-vacuity: revert the pull-side bridge (Pass-1 only) and the default never
// materializes — the source template halts on the missing `.infra.storage.size`
// key, so this test fails (pull errors) rather than silently passing.
func TestPullSourceTemplateReadsBridgedLibraryDefault(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	catalogDir := filepath.Join(root, "catalog")

	// Sourced-mode Recipe whose source: version reads the library-$ref-composed
	// infra default; replaces the workload Recipe from the fixture.
	writeFixture(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source:
    kind: helm-oci-chart
    url: oci://registry.example.com/charts/server
    version: "{{ .infra.storage.size }}"
  valuesMapping: |
    {}
`)
	// Make DbSizing remote so only the bridge supplies its default.
	if err := os.Remove(filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml")); err != nil {
		t.Fatalf("remove local library: %v", err)
	}

	puller := &fakeRemoteLibraryPuller{tags: []string{"1.0.0"}}
	resolver := &recordingSourceResolver{}
	if err := pullRun(pullConfig{
		ReleasePath:    releasePath,
		CatalogDirs:    []string{catalogDir},
		puller:         puller,
		sourceResolver: resolver,
	}); err != nil {
		t.Fatalf("pullRun (source template over bridged library default): %v", err)
	}

	if resolver.gotVersion != "10Gi" {
		t.Errorf("source version resolved from the bridged library default = %q; want 10Gi", resolver.gotVersion)
	}
}

// readDistTree concatenates every rendered file under dir for content assertions.
func readDistTree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		b.WriteString(string(data))
		b.WriteByte('\n')
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read dist tree: %v", err)
	}
	return b.String()
}
