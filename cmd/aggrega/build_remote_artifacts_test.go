// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The shared artifact bundle, authored once and consumed two ways: as the
// ArtifactsLibrary bundle the import recipe pulls in (one artifacts entry → two
// output files), and as two inline artifacts entries the comparison recipe carries
// verbatim. What the two forms share is the rendered CONTENT — the bundle expands
// to the same documents the equivalent inline entries produce. What they
// deliberately do NOT share is the output PATH: an artifacts entry is named by its
// authored key, so a bundle consumed under the key `bundle` emits
// `bundle/<filename>`, while the same file authored inline under the key
// `store-config.yaml` emits `store-config.yaml`. File bodies carry a template
// expression (.values.port) so the proof exercises the full resolve → EvalTemplate
// path.
const proofArtifactsImportRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: store-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/store
  artifacts:
    bundle:
      $import: SharedArtifacts#/artifacts/StoreBundle
`

const proofArtifactsInlineRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: store-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/store
  artifacts:
    store-config.yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: store-{{ .values.port }}-config
      data:
        port: "{{ .values.port }}"
    store-policy.yaml: |
      apiVersion: networking.k8s.io/v1
      kind: NetworkPolicy
      metadata:
        name: store-{{ .values.port }}-policy
`

const proofArtifactsLibrary = `apiVersion: aggrega.ai/v1alpha1
kind: ArtifactsLibrary
metadata:
  name: SharedArtifacts
  version: "1.0.0"
spec:
  artifacts:
    StoreBundle:
      store-config.yaml: |
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: store-{{ .values.port }}-config
        data:
          port: "{{ .values.port }}"
      store-policy.yaml: |
        apiVersion: networking.k8s.io/v1
        kind: NetworkPolicy
        metadata:
          name: store-{{ .values.port }}-policy
`

// writeArtifactsProofWorkspace authors a buildable workspace whose store Recipe
// emits artifacts through the builtin helm engine. recipeYAML selects the artifacts
// form (library `$import` vs inline). The SharedArtifacts library is authored
// locally when authorLibrary is set (so an initial pull pins it).
func writeArtifactsProofWorkspace(t *testing.T, root, recipeYAML string, authorLibrary bool) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: proof
  version: "1.0.0"
spec:
  components:
    api:
      type: core/store
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: proof-release
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
  name: proof-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/store: store-recipe:1.0.0
`)
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "store.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/store
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "store.yaml"), recipeYAML)
	if authorLibrary {
		writeFixture(t, filepath.Join(catalogRoot, "base", "artifactslibraries", "sharedartifacts.yaml"), proofArtifactsLibrary)
	}
	return filepath.Join(root, "release.yaml")
}

// relocateArtifactsLibraryToCache moves SharedArtifacts from the local catalog into
// the content cache, reproducing the post-pull state of a remote-pulled library:
// present in the cache under the library layout, pinned in the lockfile, ABSENT
// from the local catalog. Mirrors relocateWorkloadLibraryToCache.
func relocateArtifactsLibraryToCache(t *testing.T, root, cacheDir, version string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, "catalog", "base", "artifactslibraries", "sharedartifacts.yaml")); err != nil {
		t.Fatalf("remove local artifacts library: %v", err)
	}
	slot := oci.CachePath(root, cacheDir, "SharedArtifacts:"+version, version)
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir cache slot: %v", err)
	}
	body := []byte(proofArtifactsLibrary)
	if err := os.WriteFile(filepath.Join(slot, "library.yaml"), body, 0o644); err != nil {
		t.Fatalf("write cache slot: %v", err)
	}
	mb, _, err := ociManifestArtifact(oci.MediaTypeArtifactsLibrary, body)
	if err != nil {
		t.Fatalf("build artifacts-library manifest: %v", err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, "SharedArtifacts:"+version, version, mb); err != nil {
		t.Fatalf("write cache manifest: %v", err)
	}
}

// clearDist removes the rendered output directory so the next build's tree is
// measured on its own. A build's own output cleanup does not cover this Release's
// boundary directory, so without this the previous variant's files stay on disk and
// are read as part of the next variant's tree.
func clearDist(t *testing.T, distDir string) {
	t.Helper()
	if err := os.RemoveAll(distDir); err != nil {
		t.Fatalf("clear rendered output %s: %v", distDir, err)
	}
}

// TestBuildResolvesRemotePinnedArtifactsLibrary is the end-to-end proof on the
// artifacts axis: a build resolves an ArtifactsLibrary pinned in the lockfile +
// present in the content cache but ABSENT from the local catalog, via the
// pinned-cache bridge, and the imported bundle's files' EVALUATED output
// materializes in the rendered dist tree.
//
// Three properties follow, and the split between them is the point:
//
//   - Where the library was resolved from does not reach the output. The same
//     bundle read out of the local catalog and read through the pinned-cache
//     bridge render the identical tree, paths and contents alike — compared as a
//     hash over sorted (path, content) pairs, so two files swapping bodies cannot
//     pass.
//   - A rebuild is byte-identical.
//   - Against the equivalent inline artifacts entries the rendered CONTENT is
//     identical and the output PATH deliberately is not. An artifacts entry is
//     named by its authored key, so the bundle consumed under the key `bundle`
//     nests its files at `bundle/<filename>`, while the inline entries — keyed by
//     filename — emit those filenames unnested. The expected nested layout is
//     DERIVED from the inline tree rather than hardcoded, so the assertion fails
//     if the nesting is dropped, if key and filename are joined into one flat
//     name, or if the files land anywhere other than under the key.
//
// Each variant is measured from a cleared output directory, so no variant's files
// can survive into the next variant's reading. The rebuild check is the deliberate
// exception and clears nothing: a stale file left by the previous build of that
// same variant has to show up as a difference.
func TestBuildResolvesRemotePinnedArtifactsLibrary(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := writeArtifactsProofWorkspace(t, root, proofArtifactsImportRecipe, true)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")
	distDir := filepath.Join(root, "dist")

	// Initial pull with the library authored locally → a real lockfile pinning it.
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	// Host-less pin: the lockfile pins the artifacts library by its bare-name
	// identity, no registry host.
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var pinned bool
	for _, a := range lf.ArtifactsLibraries {
		if a.URI == "SharedArtifacts" {
			pinned = true
			if strings.Contains(a.URI, "://") || strings.Contains(a.URI, "/") {
				t.Errorf("artifacts pin URI %q carries a host/path; want host-less name", a.URI)
			}
			if a.ResolvedVersion != "1.0.0" {
				t.Errorf("artifacts pin version = %q; want 1.0.0", a.ResolvedVersion)
			}
		}
	}
	if !pinned {
		t.Fatalf("lockfile did not pin SharedArtifacts; artifacts_libraries=%+v", lf.ArtifactsLibraries)
	}

	// Local-resolution baseline: the library is pinned in the lockfile and still
	// present in the catalog on disk, so this build reads it from there. Captured
	// as a (path, content) tree identity for the comparison after the relocation.
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild with local artifacts library: %v", err)
	}
	localTree := hashDistTree(t, distDir)
	localPaths := distFileList(t, distDir)
	clearDist(t, distDir)

	relocateArtifactsLibraryToCache(t, root, cacheDir, "1.0.0")

	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild with bridged remote artifacts library: %v", err)
	}
	remote := readDistTree(t, distDir)
	remotePaths := distFileList(t, distDir)
	if !strings.Contains(remote, "store-8080-config") || !strings.Contains(remote, "store-8080-policy") {
		t.Errorf("imported bundle's evaluated files should materialize; got:\n%s", remote)
	}
	if remoteTree := hashDistTree(t, distDir); remoteTree != localTree {
		t.Errorf("resolving the bundle through the pinned cache changed the rendered tree:\nlocal  hash %s paths %v\nremote hash %s paths %v\nremote tree:\n%s",
			localTree, localPaths, remoteTree, remotePaths, remote)
	}

	// Deterministic rebuild — byte-identical output, measured without clearing
	// dist so a stale file from the previous build would surface as a difference.
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("second dispatchBuild: %v", err)
	}
	if again := readDistTree(t, distDir); again != remote {
		t.Errorf("rebuild not byte-identical:\nfirst:\n%s\nsecond:\n%s", remote, again)
	}

	// Rewrite the recipe to carry the identical two files as inline artifacts
	// entries, re-pull to a fresh lockfile (no artifacts library in closure), and
	// rebuild from a cleared dist so the tree read back is this variant's alone.
	writeFixture(t, filepath.Join(catalogDir, "recipes", "helm", "store.yaml"), proofArtifactsInlineRecipe)
	if err := os.Remove(filepath.Join(root, "release.lock")); err != nil {
		t.Fatalf("remove lockfile for inline re-pull: %v", err)
	}
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("inline pullRun: %v", err)
	}
	clearDist(t, distDir)
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("inline dispatchBuild: %v", err)
	}
	inline := readDistTree(t, distDir)
	inlinePaths := distFileList(t, distDir)

	// The rendered documents are invariant across the two authoring forms.
	if inline != remote {
		t.Errorf("bundle-import render and inline render differ in rendered content:\nremote:\n%s\ninline:\n%s", remote, inline)
	}

	// The layout is not, and that difference is the behavior under test: every file
	// a bundle expands to is emitted under the key the recipe consumed it at.
	wantNested := make([]string, 0, len(inlinePaths))
	for _, p := range inlinePaths {
		wantNested = append(wantNested, filepath.Join(filepath.Dir(p), "bundle", filepath.Base(p)))
	}
	slices.Sort(wantNested)
	if !slices.Equal(remotePaths, wantNested) {
		t.Errorf("bundle files should be emitted under the key the bundle was consumed at:\nwant: %v\ngot:  %v\ninline: %v", wantNested, remotePaths, inlinePaths)
	}
}
