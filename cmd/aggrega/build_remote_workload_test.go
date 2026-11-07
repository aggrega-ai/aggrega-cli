// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci/ocitest"
)

// The shared Workload body, authored once and consumed two ways: as the
// WorkloadLibrary entry the import recipe pulls in, and as the inline workload
// the comparison recipe carries verbatim. Byte-identity of the two rendered dist
// trees is the load-bearing property here (a library-imported workload renders
// identically to the equivalent inline workload, and the remote pin/pull path
// does not perturb output).
const proofWorkloadImportRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: store-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/store
  workload:
    $import: BaseWorkloads#/workloads/StatelessService
`

const proofWorkloadInlineRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: store-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/store
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      main:
        image: proof-service:v1
        ports:
          web: {{ .values.port }}
`

const proofWorkloadLibrary = `apiVersion: aggrega.ai/v1alpha1
kind: WorkloadLibrary
metadata:
  name: BaseWorkloads
  version: "1.0.0"
spec:
  workloads:
    StatelessService: |
      apiVersion: aggrega.ai/v1alpha1
      kind: Workload
      replicas: {{ .infra.replicas | default 1 }}
      containers:
        main:
          image: proof-service:v1
          ports:
            web: {{ .values.port }}
`

// writeWorkloadProofWorkspace authors a buildable workspace whose store Recipe
// synthesizes a Workload through the builtin helm engine. recipeYAML selects the
// workload form (library `$import` vs inline). The BaseWorkloads library is
// authored locally when authorLibrary is set (so an initial pull pins it), and
// relocated to the content cache afterward to reproduce the post-pull remote state.
func writeWorkloadProofWorkspace(t *testing.T, root, recipeYAML string, authorLibrary bool) string {
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
		writeFixture(t, filepath.Join(catalogRoot, "base", "workloadlibraries", "baseworkloads.yaml"), proofWorkloadLibrary)
	}
	return filepath.Join(root, "release.yaml")
}

// relocateWorkloadLibraryToCache moves BaseWorkloads from the local catalog into
// the content cache, reproducing the post-pull state of a remote-pulled library:
// present in the cache under the library layout, pinned in the lockfile, ABSENT
// from the local catalog. Mirrors relocateMappingLibraryToCache.
func relocateWorkloadLibraryToCache(t *testing.T, root, cacheDir, version string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, "catalog", "base", "workloadlibraries", "baseworkloads.yaml")); err != nil {
		t.Fatalf("remove local workload library: %v", err)
	}
	slot := ocitest.MustCachePath(root, cacheDir, "WorkloadLibrary", "BaseWorkloads:"+version, version)
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir cache slot: %v", err)
	}
	body := []byte(proofWorkloadLibrary)
	if err := os.WriteFile(filepath.Join(slot, "library.yaml"), body, 0o644); err != nil {
		t.Fatalf("write cache slot: %v", err)
	}
	mb, _, err := ociManifestArtifact(oci.MediaTypeWorkloadLibrary, body)
	if err != nil {
		t.Fatalf("build workload-library manifest: %v", err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, "WorkloadLibrary", "BaseWorkloads:"+version, version, mb); err != nil {
		t.Fatalf("write cache manifest: %v", err)
	}
}

// TestBuildResolvesRemotePinnedWorkloadLibrary is the end-to-end proof on the
// workload axis: a build resolves a WorkloadLibrary pinned in the
// lockfile + present in the content cache but ABSENT from the local catalog, via
// the pinned-cache bridge, and the imported workload's EVALUATED output
// materializes in the rendered manifest. It then proves the load-bearing
// property: the remote-imported build is byte-identical to the equivalent inline
// build — the library indirection and the remote pin/pull path do not change output.
func TestBuildResolvesRemotePinnedWorkloadLibrary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath := writeWorkloadProofWorkspace(t, root, proofWorkloadImportRecipe, true)
	// The relocated slot stands in for a hub fetch that recorded no signature.
	writeWorkspaceConfig(t, root, "auto_pull: false\n"+fakeRegistryTrust)
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	// Initial pull with the library authored locally → a real lockfile pinning it.
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	// Host-less pin: the lockfile pins the workload library by its bare-name
	// identity, no registry host.
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var pinned bool
	for _, w := range lf.WorkloadLibraries {
		if w.URI == "BaseWorkloads" {
			pinned = true
			if strings.Contains(w.URI, "://") || strings.Contains(w.URI, "/") {
				t.Errorf("workload pin URI %q carries a host/path; want host-less name", w.URI)
			}
			if w.ResolvedVersion != "1.0.0" {
				t.Errorf("workload pin version = %q; want 1.0.0", w.ResolvedVersion)
			}
		}
	}
	if !pinned {
		t.Fatalf("lockfile did not pin BaseWorkloads; workload_libraries=%+v", lf.WorkloadLibraries)
	}

	relocateWorkloadLibraryToCache(t, root, cacheDir, "1.0.0")

	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild with bridged remote workload library: %v", err)
	}
	remote := readDistTree(t, filepath.Join(root, "dist"))
	if !strings.Contains(remote, "proof-service:v1") {
		t.Errorf("imported workload's evaluated image should materialize; got:\n%s", remote)
	}

	// Deterministic rebuild — byte-identical output.
	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("second dispatchBuild: %v", err)
	}
	if again := readDistTree(t, filepath.Join(root, "dist")); again != remote {
		t.Errorf("rebuild not byte-identical:\nfirst:\n%s\nsecond:\n%s", remote, again)
	}

	// Byte-identity vs inline: rewrite the recipe to carry the identical
	// workload body inline, re-pull to a fresh lockfile (no workload library in
	// closure), rebuild, and assert the dist tree is byte-identical to the
	// remote-imported one.
	writeFixture(t, filepath.Join(catalogDir, "recipes", "helm", "store.yaml"), proofWorkloadInlineRecipe)
	if err := os.Remove(filepath.Join(root, "release.lock")); err != nil {
		t.Fatalf("remove lockfile for inline re-pull: %v", err)
	}
	if err := pullRun(context.Background(), pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("inline pullRun: %v", err)
	}
	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("inline dispatchBuild: %v", err)
	}
	if inline := readDistTree(t, filepath.Join(root, "dist")); inline != remote {
		t.Errorf("remote-imported workload render is NOT byte-identical to inline:\nremote:\n%s\ninline:\n%s", remote, inline)
	}
}
