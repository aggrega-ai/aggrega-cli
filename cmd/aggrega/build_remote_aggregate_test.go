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

// remoteAggregateFixture authors a buildable workspace whose Release references
// its top-level Aggregate by IDENTITY (`spec.aggregate: myapp`, not a path), and
// whose `myapp` imports a sub-aggregate `mysub` by identity. Both aggregates are
// authored in the catalog so an initial `pullRun` resolves them at Tier-1 and
// pins them; the caller then relocates them to the content cache (and deletes
// the catalog copies) to construct the post-pull state a registry-pulled
// aggregate would have — present in the cache, pinned in the lockfile, ABSENT
// from the local catalog. Returns the release path.
func remoteAggregateFixture(t *testing.T, root string) string {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(catalogRoot, "aggregates", "myapp.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: myapp
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
  aggregates:
    mysub:
      type: mysub
`)
	writeFixture(t, filepath.Join(catalogRoot, "aggregates", "mysub.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mysub
  version: "1.0.0"
spec:
  components:
    worker:
      type: server
      values:
        port: 9090
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: smoke-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: myapp
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
        mysub.worker:
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
`)
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
        image: "test-server:latest"
        ports:
          web: {{ .values.port }}
`)
	return filepath.Join(root, "release.yaml")
}

// relocateAggregateToCache moves an Aggregate from the local catalog into the
// content cache via the SAME primitive the puller uses (oci.WriteCached → the
// `blob` slot keyed `<name>:<version>`), then deletes the catalog copy — so only
// the build-side cache→catalog bridge can resolve it. Returns nothing; fails the
// test on error.
func relocateAggregateToCache(t *testing.T, root, cacheDir, name string) {
	t.Helper()
	src := filepath.Join(root, "catalog", "aggregates", name+".yaml")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read aggregate %s: %v", name, err)
	}
	if err := oci.WriteCached(root, cacheDir, name+":1.0.0", "1.0.0", body); err != nil {
		t.Fatalf("write aggregate %s to cache: %v", name, err)
	}
	// Persist the OCI manifest committing to the aggregate body, mirroring the real
	// pull's writeEntityToCacheSlot: a manifest-less slot reads as a fail-closed miss.
	mb, _, err := ociManifestArtifact(oci.MediaTypeAggregate, body)
	if err != nil {
		t.Fatalf("build aggregate manifest %s: %v", name, err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, name+":1.0.0", "1.0.0", mb); err != nil {
		t.Fatalf("write aggregate manifest %s: %v", name, err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatalf("remove local aggregate %s: %v", name, err)
	}
}

// TestBuildResolvesRemotePinnedAggregate is the hermetic end-to-end: a Release's
// top-level aggregate (referenced by identity) AND its imported sub-aggregate are
// pinned in the lockfile + present in the content cache but ABSENT from the local
// catalog. The build resolves both through the build-side bridge — top-level via
// the triad AggregateLookup, the sub via SubAggregateLoader during topology
// flatten — from the cache, with no network, and renders both components.
func TestBuildResolvesRemotePinnedAggregate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteAggregateFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	// Initial pull with both aggregates authored locally → a real lockfile that
	// pins them at Tier-1.
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var sawTop, sawSub bool
	for _, e := range lf.Entities {
		switch e.URI {
		case "myapp":
			sawTop = true
		case "mysub":
			sawSub = true
		}
	}
	if !sawTop || !sawSub {
		t.Fatalf("lockfile must pin both aggregates; top=%v sub=%v entities=%+v", sawTop, sawSub, lf.Entities)
	}

	// Relocate ONLY the sub-aggregate to the cache + delete its catalog copy: the
	// top-level `myapp` stays locally authored (it is the Release's own aggregate,
	// resolved by the triad resolver as an external entity — not a cache-bridged
	// one), so only `mysub` must resolve through the build-side bridge.
	relocateAggregateToCache(t, root, cacheDir, "mysub")

	// Build is hermetic (auto_pull off, no puller) — it must resolve the
	// sub-aggregate from the cache via the bridge during topology flatten.
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild with bridged remote sub-aggregate: %v", err)
	}

	// Both components rendered: the top-level `api` (port 8080) and the
	// sub-aggregate `mysub.worker` (port 9090) — the latter proves the
	// sub-aggregate resolved from cache through SubAggregateLoader during flatten,
	// with no network.
	got := readDistTree(t, filepath.Join(root, "dist"))
	if !strings.Contains(got, "8080") {
		t.Errorf("top-level component (port 8080) did not render; got:\n%s", got)
	}
	if !strings.Contains(got, "9090") {
		t.Errorf("sub-aggregate component (port 9090) did not render — sub-from-cache failed; got:\n%s", got)
	}
}
