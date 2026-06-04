// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

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
  aggregate: aggregate
  manifest: manifest
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
  baseSchema: |
    type: object
    properties:
      image: { type: string }
      replicas: { type: integer, default: 1 }
    required: [image]
    additionalProperties: true
`)
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  provider: helm
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
	writeWorkspaceConfig(t, root, "  auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	// Initial pull with the library authored locally → a real lockfile pinning it.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDir: catalogDir}); err != nil {
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

	if err := dispatchBuild(releasePath, catalogDir, false); err != nil {
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
	writeWorkspaceConfig(t, root, "  auto_pull: false\n")
	catalogDir := filepath.Join(root, "catalog")

	// Remove the local library WITHOUT pinning/caching it, and hand the build a
	// lockfile so it reaches catalog load. The $ref cannot resolve.
	if err := os.Remove(filepath.Join(catalogDir, "schemalibraries", "dbsizing.yaml")); err != nil {
		t.Fatalf("remove local library: %v", err)
	}
	if err := lockfile.WriteLockfile(filepath.Join(root, "release.lock"), &lockfile.Lockfile{Version: 1}); err != nil {
		t.Fatalf("write empty lockfile: %v", err)
	}

	err := dispatchBuild(releasePath, catalogDir, false)
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
	writeWorkspaceConfig(t, root, "  auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDir: catalogDir}); err != nil {
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

	err = dispatchBuild(releasePath, catalogDir, false)
	if err == nil {
		t.Fatal("expected the build to halt on the tampered library pin; got nil")
	}
	if !errors.Is(err, oci.ErrFetchIntegrity) {
		t.Errorf("error should wrap ErrFetchIntegrity (verify-on-read at the bridge); got %v", err)
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
