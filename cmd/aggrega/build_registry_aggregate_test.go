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
)

// fakeAggregatePuller is an in-memory AggregatePuller: it serves published
// Aggregates by bare identity without touching the network. Each entry carries
// the OCI tag the puller "fetched under" (servedTag) and the body bytes; the
// body's metadata.version may deliberately DIFFER from servedTag to exercise
// the identity-not-tag pinning rule. It implements OCIPuller (Pull is unused —
// aggregate resolution goes through PullAggregate) plus AggregatePuller.
//
// The fixtures using it run under an offline workspace: closure resolution is
// exercised through this injected puller, while the pull-time signature-verify
// pass (a separate live-registry referrer discovery this in-memory puller does
// not back) is not attempted. Signed-entity verification through a real registry
// is covered by the signing integration suites.
type fakeAggregatePuller struct {
	entries map[string]fakeAggEntry // bare name -> served artifact
	calls   []string
}

type fakeAggEntry struct {
	servedTag string
	body      []byte
}

func (p *fakeAggregatePuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound()
}

func (p *fakeAggregatePuller) PullAggregate(ctx context.Context, name, _ string) (*oci.PullResult, string, error) {
	p.calls = append(p.calls, name)
	e, ok := p.entries[name]
	if !ok {
		return nil, "", oci.ErrFetchNotFound()
	}
	// Populate the OCI manifest bytes AND digest exactly as the real pull does — the
	// manifest wrapping the served body — so the sub-aggregate carries a non-empty
	// manifest digest for signature-referrer resolution, and the persisted cache
	// slot carries the manifest the verifying read requires (a manifest-less slot
	// reads as a fail-closed miss).
	mb, md, err := ociManifestArtifact(oci.MediaTypeAggregate, e.body)
	if err != nil {
		return nil, "", err
	}
	return &oci.PullResult{MediaType: oci.MediaTypeAggregate, Bytes: e.body, Digest: md, ManifestBytes: mb}, e.servedTag, nil
}

func registryAggregateBody(name, version string, subURIs ...string) []byte {
	src := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: " + name +
		"\n  version: \"" + version + "\"\nspec:\n  components:\n    " + name + "-svc:\n      type: server\n      values:\n        port: 8080\n"
	if len(subURIs) > 0 {
		src += "  aggregates:\n"
		for i, u := range subURIs {
			src += "    sub" + itoaSmall(i) + ":\n      type: " + u + "\n"
		}
	}
	return []byte(src)
}

func itoaSmall(i int) string { return string(rune('0' + i)) }

// writeServerCatalog authors the (local) server Component + Recipe + helm
// Provider shared by the registry-aggregate fixtures. The aggregates themselves
// are NOT authored here — they are served only by the fake puller, so the build
// must resolve them through the registry tier + cache→catalog bridge.
func writeServerCatalog(t *testing.T, catalogRoot string) {
	t.Helper()
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
}

func writeServerManifest(t *testing.T, root string) {
	t.Helper()
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
}

// TestRegistryTopLevelResolvesEndToEnd is the registry-only top-level proof: a
// Release's spec.aggregate is a bare identity present ONLY in the registry (not
// same-dir, not ./file, not the local catalog). Pull fetches + caches + pins it
// (a real registry entity); build resolves + renders it from the cache through
// the bridge, with no network.
func TestRegistryTopLevelResolvesEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	catalogDir := filepath.Join(root, "catalog")
	writeServerCatalog(t, catalogDir)
	writeServerManifest(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\nauto_pull: false\noffline: true\nskip_signature_verification: true\n")
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: testing
  version: "1.0.0"
spec:
  aggregate: myapp
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        myapp-svc:
          infra:
            replicas: 1
`)
	releasePath := filepath.Join(root, "release.yaml")
	puller := &fakeAggregatePuller{entries: map[string]fakeAggEntry{
		"myapp": {servedTag: "1.0.0", body: registryAggregateBody("myapp", "1.0.0")},
	}}

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, puller: puller}); err != nil {
		t.Fatalf("pullRun (registry top-level): %v", err)
	}
	if len(puller.calls) == 0 {
		t.Fatalf("expected the registry tier to fetch the top-level aggregate")
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	assertPinned(t, lf, "myapp", "1.0.0")

	// Build is hermetic (no puller) — it must resolve the top-level from the cache
	// via the bridge.
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild (registry top-level from cache): %v", err)
	}
	if got := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(got, "8080") {
		t.Errorf("top-level component did not render; got:\n%s", got)
	}
}

// TestRegistrySubResolvesEndToEnd is the registry-only SUB proof (the gap the
// review surfaced): a LOCAL top-level imports a sub-aggregate by bare identity
// that is present ONLY in the registry — absent from both the catalog and the
// cache. Pull's closure walker Tier-3 fetches + caches + pins it; build folds +
// flattens it from cache and renders its component, with no network.
func TestRegistrySubResolvesEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	catalogDir := filepath.Join(root, "catalog")
	writeServerCatalog(t, catalogDir)
	writeServerManifest(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\nauto_pull: false\noffline: true\nskip_signature_verification: true\n")
	// The top-level is a LOCAL ./file that imports `mysub` by bare identity.
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: top
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
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
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
        mysub.mysub-svc:
          infra:
            replicas: 1
`)
	releasePath := filepath.Join(root, "release.yaml")
	puller := &fakeAggregatePuller{entries: map[string]fakeAggEntry{
		"mysub": {servedTag: "1.0.0", body: registryAggregateBody("mysub", "1.0.0")},
	}}

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, puller: puller}); err != nil {
		t.Fatalf("pullRun (registry sub): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	assertPinned(t, lf, "mysub", "1.0.0")

	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild (registry sub from cache): %v", err)
	}
	if got := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(got, "8080") {
		t.Errorf("sub-aggregate component did not render; got:\n%s", got)
	}
}

// TestRegistryAggregateDriftTagVsVersion is the load-bearing identity proof: the
// puller serves the aggregate under an OCI tag DIFFERENT from the body's
// metadata.version. The lockfile must pin metadata.version (identity), not the
// tag, and the build-side recompute must re-derive the same — so verify passes
// with no drift. This is the case a relocate-a-catalog-entity-to-the-cache
// fixture cannot reach (both ends resolve at the catalog tier there).
func TestRegistryAggregateDriftTagVsVersion(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	catalogDir := filepath.Join(root, "catalog")
	writeServerCatalog(t, catalogDir)
	writeServerManifest(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\nauto_pull: false\noffline: true\nskip_signature_verification: true\n")
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: testing
  version: "1.0.0"
spec:
  aggregate: myapp
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        myapp-svc:
          infra:
            replicas: 1
`)
	releasePath := filepath.Join(root, "release.yaml")
	// Served under tag "2.0.0" but the body's metadata.version is "1.0.0".
	puller := &fakeAggregatePuller{entries: map[string]fakeAggEntry{
		"myapp": {servedTag: "2.0.0", body: registryAggregateBody("myapp", "1.0.0")},
	}}

	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, puller: puller}); err != nil {
		t.Fatalf("pullRun (drift): %v", err)
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	// The pin is the metadata.version identity (1.0.0), NOT the served tag (2.0.0).
	assertPinned(t, lf, "myapp", "1.0.0")

	// Build re-derives 1.0.0 via the bridge → tier-1; verify must pass (no drift).
	if err := dispatchBuild(nil, releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild (drift) must succeed with no drift, got: %v", err)
	}
}

func assertPinned(t *testing.T, lf *lockfile.Lockfile, uri, wantVersion string) {
	t.Helper()
	for _, e := range lf.Entities {
		if e.URI == uri {
			if e.ResolvedVersion != wantVersion {
				t.Fatalf("lockfile pins %q at version %q; want %q", uri, e.ResolvedVersion, wantVersion)
			}
			return
		}
	}
	t.Fatalf("lockfile does not pin %q; entities=%+v", uri, lf.Entities)
}
