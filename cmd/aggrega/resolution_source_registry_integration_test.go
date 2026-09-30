// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// Where a resolution from a registry took each reference from, named alike on
// every surface that reports it.
//
// Build-tagged `integration` because the Release compared here resolves its
// dependencies from a real in-process OCI registry on the loopback interface,
// whose test-only dependency must not enter the production binary's import graph
// — the same reason the registry round-trip test carries the tag. The comparison
// itself is not restated: each test below hands this fixture to the requirement
// resolution_source_parity_test.go holds every other fixture to.
//
// The registry is published to once per test, before any surface runs, and only
// read afterwards, so every surface meets the same registry content. Each surface
// run still gets a fresh workspace of its own, and the content cache lives beneath
// the workspace, so each run starts with nothing cached and its first read of a
// reference is a fetch. That is what lets the fixture be reported under
// `fetched-fresh` at all, and what keeps a later run from reading a copy an
// earlier one cached and reporting `cache-reuse` for a reason that has nothing to
// do with the surface under test.

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// registryResolutionPublication is one entity the fixture publishes: where it sits
// in the publisher's tree, and its document.
type registryResolutionPublication struct {
	path string
	body string
}

// registryResolutionPublisher is the catalog the fixture publishes, in the order it
// is published: a shared schema document, a Component drawing on it, and the
// Recipe realising the Component. Each entity sits in a directory of its own,
// because a publication refuses an entity that shares its directory with another.
//
// No provider is published. The binary carries the helm provider, and a Release
// whose workspace declares none of its own resolves the provider from there, so
// the registry holds only what the Release's own references name.
var registryResolutionPublisher = []registryResolutionPublication{
	{path: "schemalibrary/basetypes/1.0.0/library.yaml", body: `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseTypes
  version: "1.0.0"
spec:
  schemas:
    PortSpec:
      port: { type: integer, default: 8080 }
`},
	{path: "component/demo/web/1.0.0/component.yaml", body: `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/web
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
      ports:
        $ref: "BaseTypes:1.0.0#/schemas/PortSpec"
`},
	{path: "recipe/helm/demo-web-helm/1.0.0/recipe.yaml", body: `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-web-helm
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/web:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      web:
        image: demo-web:latest
        ports:
          http: {{ .values.port }}
`},
}

// publishRegistryResolutionCatalog publishes registryResolutionPublisher to the
// registry at host through the production publication, from a publisher workspace
// of its own. Each publication runs the pin step first, as a publisher would; an
// entity with nothing to pin publishes exactly as it would without it.
func publishRegistryResolutionCatalog(t *testing.T, host string) {
	t.Helper()
	pub := t.TempDir()
	for _, entity := range registryResolutionPublisher {
		writeFixture(t, filepath.Join(pub, filepath.FromSlash(entity.path)), entity.body)
	}
	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	for _, entity := range registryResolutionPublisher {
		var out bytes.Buffer
		if err := pushRun(t.Context(), pushConfig{
			Workspace:  publisher,
			EntityPath: filepath.Join(pub, filepath.FromSlash(entity.path)),
			Registry:   host,
			PinSources: boolPtr(true),
			Out:        &out,
		}); err != nil {
			t.Fatalf("publish %s to the loopback registry: %v\n%s", entity.path, err, out.String())
		}
	}
}

// writeRegistryResolutionConsumer lays down a Release that reaches every entity it
// depends on by identity, in a workspace that switches the local catalog off and
// names the registry at host as its default, and returns the Release's path
// relative to root. Nothing it depends on can resolve from anywhere but the
// registry, the binary, and the Release's own files.
func writeRegistryResolutionConsumer(t *testing.T, root, host string) string {
	t.Helper()
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), `workspace:
  catalog_dirs: []
default_registry: "`+host+`"
offline: false
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: shop
  version: "1.0.0"
spec:
  components:
    web:
      type: demo/web:1.0.0
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: shop-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/web:1.0.0: helm/demo-web-helm:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: shop-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        web: {}
`)
	return "release.yaml"
}

// registryResolutionFixture starts a loopback registry for the calling test,
// publishes the fixture's catalog to it, and returns the fixture every surface run
// lays down afresh against that registry.
func registryResolutionFixture(t *testing.T) sourceParityFixture {
	t.Helper()
	host := startRegistry(t, nil)
	publishRegistryResolutionCatalog(t, host)
	return sourceParityFixture{
		name: "a Release resolving from a loopback registry",
		write: func(t *testing.T, root string) string {
			return writeRegistryResolutionConsumer(t, root, host)
		},
		mustName: closure.SourceFetchedFresh,
	}
}

// TestIntegration_BuildResolutionFromARegistryNamesEachSourceAsPullDetailDoes
// holds the daemon's build of a Release whose dependencies resolve from a loopback
// registry to the requirement every fixture is held to: each entry of the answer's
// resolution report names the source `aggrega pull --detail` prints for the same
// reference, over the same starting state.
func TestIntegration_BuildResolutionFromARegistryNamesEachSourceAsPullDetailDoes(t *testing.T) {
	requireBuildNamesSourcesAsPullDetail(t, registryResolutionFixture(t))
}

// TestIntegration_PullToolResolutionFromARegistryNamesEachSourceAsPullDetailDoes
// is the same requirement on the assistant-facing pull tool, over the same
// fixture.
func TestIntegration_PullToolResolutionFromARegistryNamesEachSourceAsPullDetailDoes(t *testing.T) {
	requirePullToolNamesSourcesAsPullDetail(t, registryResolutionFixture(t))
}
