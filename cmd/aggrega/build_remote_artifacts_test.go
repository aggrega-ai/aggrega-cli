// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// The shared artifact bundle, authored once and consumed two ways: as the
// ArtifactsLibrary bundle the import recipe pulls in (one artifacts entry → two
// output files), and as two inline artifacts entries the comparison recipe carries
// verbatim. Byte-identity of the two rendered dist trees is the arc's load-bearing
// property (a library-imported bundle expands to outputs identical to the
// equivalent inline artifacts, and the remote pin/pull path does not perturb
// output). File bodies carry a template expression (.values.port) so the proof
// exercises the full resolve → EvalTemplate path.
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
	writeFixture(t, filepath.Join(catalogRoot, "base", "providers", "helm.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: "1.0.0"
spec:
  plugin: "builtin:helm"
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

// TestBuildResolvesRemotePinnedArtifactsLibrary is the arc's headline shipped-proof
// behavior on the artifacts axis: a build resolves an ArtifactsLibrary pinned in the
// lockfile + present in the content cache but ABSENT from the local catalog, via the
// pinned-cache bridge, and the imported bundle's files' EVALUATED output
// materializes in the rendered dist tree. It then proves the load-bearing property
// (R5): the remote-imported build is byte-identical to the equivalent inline build —
// the one-key→N-file bundle expansion and the remote pin/pull path do not change
// output.
func TestBuildResolvesRemotePinnedArtifactsLibrary(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := writeArtifactsProofWorkspace(t, root, proofArtifactsImportRecipe, true)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	// Initial pull with the library authored locally → a real lockfile pinning it.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
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
		t.Fatalf("lockfile did not pin SharedArtifacts; artifact_libraries=%+v", lf.ArtifactsLibraries)
	}

	relocateArtifactsLibraryToCache(t, root, cacheDir, "1.0.0")

	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild with bridged remote artifacts library: %v", err)
	}
	remote := readDistTree(t, filepath.Join(root, "dist"))
	if !strings.Contains(remote, "store-8080-config") || !strings.Contains(remote, "store-8080-policy") {
		t.Errorf("imported bundle's evaluated files should materialize; got:\n%s", remote)
	}

	// Deterministic rebuild — byte-identical output.
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("second dispatchBuild: %v", err)
	}
	if again := readDistTree(t, filepath.Join(root, "dist")); again != remote {
		t.Errorf("rebuild not byte-identical:\nfirst:\n%s\nsecond:\n%s", remote, again)
	}

	// R5 byte-identity vs inline: rewrite the recipe to carry the identical two
	// files as inline artifacts entries, re-pull to a fresh lockfile (no artifacts
	// library in closure), rebuild, and assert the dist tree is byte-identical to
	// the remote-imported bundle one.
	writeFixture(t, filepath.Join(catalogDir, "recipes", "helm", "store.yaml"), proofArtifactsInlineRecipe)
	if err := os.Remove(filepath.Join(root, "release.lock")); err != nil {
		t.Fatalf("remove lockfile for inline re-pull: %v", err)
	}
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("inline pullRun: %v", err)
	}
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("inline dispatchBuild: %v", err)
	}
	if inline := readDistTree(t, filepath.Join(root, "dist")); inline != remote {
		t.Errorf("remote-imported bundle render is NOT byte-identical to inline (R5 breach):\nremote:\n%s\ninline:\n%s", remote, inline)
	}
}
