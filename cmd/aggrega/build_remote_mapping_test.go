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

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// fakeRemoteMappingLibraryPuller is an in-memory LibraryPuller serving the
// HelmHelpers MappingLibrary at a fixed set of registry tags without touching
// the network. A pure bare-name reference (no `@version` floor) drives the
// version-discovery path (list tags → highest); the served body carries that
// resolved version as metadata.version so the pin and the resolution agree.
type fakeRemoteMappingLibraryPuller struct {
	tags           []string
	listTagsCalled bool
}

func (p *fakeRemoteMappingLibraryPuller) Pull(_ context.Context, _, _ string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound() // library resolution uses PullLibrary
}

func (p *fakeRemoteMappingLibraryPuller) PullLibrary(_ context.Context, _, _, floorVersion string) (*oci.PullResult, string, error) {
	version := floorVersion
	if version == "" {
		p.listTagsCalled = true
		for _, t := range p.tags {
			if version == "" || t > version {
				version = t
			}
		}
	}
	body := []byte(fmt.Sprintf(helmHelpersLibrarySourceFmt, version))
	mb, md, err := ociManifestArtifact(oci.MediaTypeMappingLibrary, body)
	if err != nil {
		return nil, "", err
	}
	return &oci.PullResult{MediaType: oci.MediaTypeMappingLibrary, Bytes: body, Digest: md, ManifestBytes: mb}, version, nil
}

// helmHelpersLibrarySourceFmt is the served/authored MappingLibrary body,
// parameterized on version. Its ServerValues entry is a Go-template values
// fragment producing the provider's `image` value from the component's
// `.values.tag`; sourceless helm renders that image into the Deployment.
const helmHelpersLibrarySourceFmt = `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: HelmHelpers
  version: "%s"
spec:
  mappings:
    ServerValues: |
      image: "mariadb-helpers:{{ .values.tag }}"
`

// remoteMappingLibraryFixture authors a buildable workspace whose `server`
// Recipe is sourceless and sources its provider values from a MappingLibrary
// entry by a host-less bare-name $import (`HelmHelpers#/mappings/ServerValues`).
// The library is authored LOCALLY so an initial `pullRun` produces a real,
// matching lockfile that pins it host-less; the caller then relocates it to the
// content cache to construct the post-pull state a remote-pulled library has.
func remoteMappingLibraryFixture(t *testing.T, root string) string {
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
        tag: "v3"
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
      tag: { type: string, default: "v3" }
`)
	writeFixture(t, filepath.Join(catalogRoot, "mappinglibraries", "helmhelpers.yaml"), fmt.Sprintf(helmHelpersLibrarySourceFmt, "1.0.0"))
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	// A minimal local Helm chart whose deployment template echoes .Values.image —
	// the valuesMapping output overlays the chart values, so the imported mapping
	// fragment's evaluated `image` materializes in the rendered manifest. A local
	// source path is forwarded verbatim (no network). The chart lives OUTSIDE the
	// catalog walk (its templated `{{ }}` files are not parseable catalog YAML),
	// referenced by an absolute path so it resolves independent of the recipe
	// file's directory.
	chartDir := filepath.Join(root, "charts", "server")
	writeMinimalChart(t, chartDir)
	// Sourced Recipe: a local chart source paired with a valuesMapping that imports
	// the MappingLibrary entry (the values-mapping rule: valuesMapping requires
	// source).
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source: %q
  valuesMapping:
    $import: HelmHelpers#/mappings/ServerValues
`, chartDir))
	return filepath.Join(root, "release.yaml")
}

// writeMinimalChart authors a minimal Helm chart whose deployment template echoes
// .Values.image, so the valuesMapping overlay's resolved image surfaces in the
// rendered output.
func writeMinimalChart(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, filepath.Join(dir, "Chart.yaml"), "apiVersion: v2\nname: server\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(dir, "values.yaml"), "image: placeholder:latest\nreplicas: 1\n")
	writeFixture(t, filepath.Join(dir, "templates", "deployment.yaml"), `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}
spec:
  replicas: {{ .Values.replicas }}
  template:
    spec:
      containers:
        - name: app
          image: {{ .Values.image }}
`)
}

// relocateMappingLibraryToCache moves HelmHelpers from the local catalog into the
// content cache, replicating the post-pull state of a remote-pulled library:
// present in the cache under the library layout (library.yaml at
// CachePath(<name>:<version>)), pinned in the lockfile, ABSENT from the local
// catalog.
func relocateMappingLibraryToCache(t *testing.T, root, cacheDir, version string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, "catalog", "mappinglibraries", "helmhelpers.yaml")); err != nil {
		t.Fatalf("remove local mapping library: %v", err)
	}
	slot := oci.CachePath(root, cacheDir, "HelmHelpers:"+version, version)
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatalf("mkdir cache slot: %v", err)
	}
	body := []byte(fmt.Sprintf(helmHelpersLibrarySourceFmt, version))
	if err := os.WriteFile(filepath.Join(slot, "library.yaml"), body, 0o644); err != nil {
		t.Fatalf("write cache slot: %v", err)
	}
	// Persist the OCI manifest committing to the library body, mirroring the real
	// pull's writeEntityToCacheSlot: a manifest-less slot reads as a fail-closed miss.
	mb, _, err := ociManifestArtifact(oci.MediaTypeMappingLibrary, body)
	if err != nil {
		t.Fatalf("build mapping-library manifest: %v", err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, "HelmHelpers:"+version, version, mb); err != nil {
		t.Fatalf("write cache manifest: %v", err)
	}
}

// TestBuildResolvesRemotePinnedMappingLibrary is the headline remote-mapping
// behavior: a build resolves a MappingLibrary pinned in the lockfile + present in
// the content cache but ABSENT from the local catalog, via the build-side bridge,
// and the imported fragment's EVALUATED output (the templated `image`)
// materializes in the rendered Deployment. Determinism: a second build produces
// byte-identical output.
func TestBuildResolvesRemotePinnedMappingLibrary(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteMappingLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	const cacheDir = ".aggrega/cache"
	catalogDir := filepath.Join(root, "catalog")

	// Initial pull with the library authored locally → a real lockfile pinning it.
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("initial pullRun: %v", err)
	}

	// Host-less pin: the lockfile pins the mapping library by its bare-name
	// identity, no registry host.
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var pinned bool
	for _, m := range lf.MappingLibraries {
		if m.URI == "HelmHelpers" {
			pinned = true
			if strings.Contains(m.URI, "://") || strings.Contains(m.URI, "/") {
				t.Errorf("mapping pin URI %q carries a host/path; want host-less name-path", m.URI)
			}
			if m.ResolvedVersion != "1.0.0" {
				t.Errorf("mapping pin version = %q; want 1.0.0", m.ResolvedVersion)
			}
		}
	}
	if !pinned {
		t.Fatalf("lockfile did not pin HelmHelpers; mappings=%+v", lf.MappingLibraries)
	}

	relocateMappingLibraryToCache(t, root, cacheDir, "1.0.0")

	if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild with bridged remote mapping library: %v", err)
	}
	first := readDistTree(t, filepath.Join(root, "dist"))
	if !strings.Contains(first, "mariadb-helpers:v3") {
		t.Errorf("imported fragment's evaluated image should materialize; got:\n%s", first)
	}

	// Deterministic rebuild — byte-identical output.
	if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("second dispatchBuild: %v", err)
	}
	if second := readDistTree(t, filepath.Join(root, "dist")); second != first {
		t.Errorf("rebuild not byte-identical:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestPullFetchesRemoteMappingLibraryViaTier3 is the real-pull end-to-end on the
// mapping axis: the library is NOT local, and `aggrega pull` fetches it through
// the puller — the version-discovery path (no floor → list-tags → highest),
// AOT-safe because the version resolves at pull and is pinned. The build then
// bridges the pinned-and-cached library and materializes the imported fragment.
func TestPullFetchesRemoteMappingLibraryViaTier3(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteMappingLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "default_registry: hub.aggrega.ai\n"+fakeRegistryTrust)
	catalogDir := filepath.Join(root, "catalog")

	// Make HelmHelpers genuinely remote.
	if err := os.Remove(filepath.Join(catalogDir, "mappinglibraries", "helmhelpers.yaml")); err != nil {
		t.Fatalf("remove local mapping library: %v", err)
	}

	puller := &fakeRemoteMappingLibraryPuller{tags: []string{"1.0.0", "2.0.0"}}
	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, puller: puller}); err != nil {
		t.Fatalf("pullRun (Tier-3 remote mapping fetch): %v", err)
	}
	if !puller.listTagsCalled {
		t.Error("expected the list-tags version-discovery path (no @version floor)")
	}
	lf, _, err := lockfile.LoadLockfile(filepath.Join(root, "release.lock"))
	if err != nil {
		t.Fatalf("LoadLockfile: %v", err)
	}
	var pinned string
	for _, m := range lf.MappingLibraries {
		if m.URI == "HelmHelpers" {
			pinned = m.ResolvedVersion
		}
	}
	if pinned != "2.0.0" {
		t.Errorf("lockfile should pin the highest tag (2.0.0) resolved at pull; got %q", pinned)
	}

	if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild after remote mapping fetch: %v", err)
	}
	if got := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(got, "mariadb-helpers:v3") {
		t.Errorf("remote-fetched mapping fragment should materialize; got:\n%s", got)
	}
}

// TestBuildResolvesRecursiveMappingImport proves the recursive case end-to-end:
// the imported entry's body is itself a field-root $import of a second library's
// entry, and the build resolves the chain to the terminal fragment, whose
// evaluated output materializes.
func TestBuildResolvesRecursiveMappingImport(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteMappingLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	catalogDir := filepath.Join(root, "catalog")

	// HelmHelpers.ServerValues now indirects to BaseValues.Real (recursive import).
	writeFixture(t, filepath.Join(catalogDir, "mappinglibraries", "helmhelpers.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: HelmHelpers
  version: "1.0.0"
spec:
  mappings:
    ServerValues: |
      $import: BaseValues#/mappings/Real
`)
	writeFixture(t, filepath.Join(catalogDir, "mappinglibraries", "basevalues.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseValues
  version: "1.0.0"
spec:
  mappings:
    Real: |
      image: "recursive-final:{{ .values.tag }}"
`)

	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pullRun: %v", err)
	}
	if err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("dispatchBuild (recursive mapping import): %v", err)
	}
	if got := readDistTree(t, filepath.Join(root, "dist")); !strings.Contains(got, "recursive-final:v3") {
		t.Errorf("recursive import should resolve to the terminal fragment; got:\n%s", got)
	}
}

// TestBuildHaltsOnMappingImportCycle proves the cycle guard end-to-end: a
// library↔library import cycle reached through a valuesMapping halts the build
// with ErrCircularImport rather than recursing without bound. The guard
// terminates (it is never disabled); its logic is proven non-vacuous at the unit
// level in pkg/refcycle.
func TestBuildHaltsOnMappingImportCycle(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath := remoteMappingLibraryFixture(t, root)
	writeWorkspaceConfig(t, root, "auto_pull: false\n")
	catalogDir := filepath.Join(root, "catalog")

	// HelmHelpers.ServerValues → Cycle.B → HelmHelpers.ServerValues (A↔B cycle).
	writeFixture(t, filepath.Join(catalogDir, "mappinglibraries", "helmhelpers.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: HelmHelpers
  version: "1.0.0"
spec:
  mappings:
    ServerValues: |
      $import: Cycle#/mappings/B
`)
	writeFixture(t, filepath.Join(catalogDir, "mappinglibraries", "cycle.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: Cycle
  version: "1.0.0"
spec:
  mappings:
    B: |
      $import: HelmHelpers#/mappings/ServerValues
`)

	if err := pullRun(pullConfig{ws: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("pullRun: %v", err)
	}
	err := dispatchBuild(resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}), releasePath, []string{catalogDir}, false, os.Stderr, os.Stdout)
	if err == nil {
		t.Fatal("expected the build to halt on the mapping-import cycle; got nil")
	}
	if !errors.Is(err, renderer.ErrCircularImport()) {
		t.Errorf("error should wrap ErrCircularImport; got %v", err)
	}
}
