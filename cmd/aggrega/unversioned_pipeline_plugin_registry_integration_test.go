// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// binaryOnlyPlugin is a plugin the binary carries and no registry or catalog
// directory here holds, of a type other than provider.
const binaryOnlyPlugin = "governance/label-injector"

// unpublishedPlugin is a plugin nothing holds: no catalog directory, no registry and
// not the binary.
const unpublishedPlugin = "pipeline-step/nowhere"

// embeddedPluginVersion reads the version the binary's own copy of a plugin
// declares, so a reference pinned to it names exactly what the binary carries.
func embeddedPluginVersion(t *testing.T, coordinate string) string {
	t.Helper()
	raw, ok := embedded.LookupEntity(coordinate)
	if !ok {
		t.Fatalf("fixture: the binary carries no entity for %s", coordinate)
	}
	var doc struct {
		Metadata struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil || doc.Metadata.Version == "" {
		t.Fatalf("fixture: read the version of the binary's %s: %v (version %q)", coordinate, err, doc.Metadata.Version)
	}
	return doc.Metadata.Version
}

// pipelinePluginReferenceWorkspace writes a workspace reaching a registry at host,
// whose catalog directory holds a Component and its Recipe, the Recipe's one
// pipeline step naming pluginRef. It returns the workspace root and the Release path.
func pipelinePluginReferenceWorkspace(t *testing.T, host, pluginRef string) (root, releasePath string) {
	t.Helper()
	root = t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs:\n    - ./catalog\ndefault_registry: \""+host+"\"\noffline: false\nauto_pull: false")
	writeFixture(t, filepath.Join(catalogDir, "component", "test", "svc", "1.0.0", "component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe", "helm", "svc", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
  pipeline:
    - id: patch
      plugin: `+pluginRef+`
      function: step
`)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: helm/svc:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
	return root, releasePath
}

// TestIntegration_APullAnswersAPipelinePluginWrittenWithoutAVersionAsItsVersionedForm
// drives the pull and then the build over a Recipe whose pipeline step names a
// Plugin no registry publishes, against a real in-process registry holding nothing,
// with the reference written with its version and without one.
//
// The registry answers both forms "no such repository": a version-less reference is
// listed (tags/list), a versioned one resolved (manifests/<version>), and each row
// asserts the registry was asked the question its form asks, so no row passes
// without having reached it. The pull must then decide both forms the same way.
// A plugin only the binary carries is left to the build, which loads it from the
// binary: the pull writes its lockfile, and the build's refusal is the one the
// step's plugin type earns once loaded — a governance plugin cannot run as a
// pipeline step — and never the plugin-entity-not-found condition. A plugin nothing
// holds is refused by the pull with the plugin-entity-not-found condition and the
// sentence the build's own load raises, and no lockfile is written. A pull that let
// the registry's listing answer through as a fetch failure fails the version-less
// rows of both, which is the disagreement this test exists to hold shut.
func TestIntegration_APullAnswersAPipelinePluginWrittenWithoutAVersionAsItsVersionedForm(t *testing.T) {
	binaryVersion := embeddedPluginVersion(t, binaryOnlyPlugin)
	for _, tc := range []struct {
		name       string
		ref        string
		askedPath  string
		binaryHeld bool
	}{
		{name: "a plugin only the binary carries, without a version", ref: binaryOnlyPlugin,
			askedPath: "/v2/plugin/" + binaryOnlyPlugin + "/tags/list", binaryHeld: true},
		{name: "a plugin only the binary carries, at the binary's version", ref: binaryOnlyPlugin + ":" + binaryVersion,
			askedPath: "/v2/plugin/" + binaryOnlyPlugin + "/manifests/" + binaryVersion, binaryHeld: true},
		{name: "a plugin nothing holds, without a version", ref: unpublishedPlugin,
			askedPath: "/v2/plugin/" + unpublishedPlugin + "/tags/list"},
		{name: "a plugin nothing holds, with a version", ref: unpublishedPlugin + ":1.0.0",
			askedPath: "/v2/plugin/" + unpublishedPlugin + "/manifests/1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked atomic.Int32
			host := startRegistry(t, func(h http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == tc.askedPath {
						asked.Add(1)
					}
					h.ServeHTTP(w, r)
				})
			})
			root, releasePath := pipelinePluginReferenceWorkspace(t, host, tc.ref)

			pullErr := unheldPluginPull(t, root, releasePath)
			if asked.Load() == 0 {
				t.Errorf("the pull never asked the registry %s, so this row says nothing about how its answer is read", tc.askedPath)
			}

			if !tc.binaryHeld {
				if pullErr == nil {
					t.Fatalf("the pull accepted a pipeline step naming %s, which nothing holds", tc.ref)
				}
				printed := formatCompilerError(pullErr)
				if !errors.Is(pullErr, pluginpath.ErrPluginEntityNotFound()) {
					t.Fatalf("the pull refused %s, but not with the plugin-entity-not-found condition:\n%s", tc.ref, printed)
				}
				if sentence := pluginpath.UnresolvedEntity(tc.ref, pluginpath.SurfaceEntityOnly).Error(); !strings.Contains(printed, sentence) {
					t.Errorf("the pull refusal does not carry the sentence the build's load raises\nwant within it: %s\ngot:\n%s", sentence, printed)
				}
				if strings.Contains(printed, "list tags") {
					t.Errorf("the pull refusal reports the registry's listing rather than the reference nothing holds:\n%s", printed)
				}
				if _, statErr := os.Stat(lockfile.PathFor(releasePath)); !errors.Is(statErr, fs.ErrNotExist) {
					t.Errorf("the refused pull wrote a lockfile (stat: %v); a refusal writes nothing", statErr)
				}
				return
			}

			if pullErr != nil {
				t.Fatalf("the pull refused %s, a plugin the binary carries and the build loads from it:\n%s", tc.ref, formatCompilerError(pullErr))
			}
			if _, statErr := os.Stat(lockfile.PathFor(releasePath)); statErr != nil {
				t.Fatalf("the pull completed but wrote no lockfile: %v", statErr)
			}
			buildErr := unheldPluginBuild(t, root, releasePath)
			if buildErr == nil {
				t.Fatalf("fixture: the build ran a governance plugin as a pipeline step; the row expects the refusal its plugin type earns once loaded")
			}
			printed := formatCompilerError(buildErr)
			if errors.Is(buildErr, pluginpath.ErrPluginEntityNotFound()) {
				t.Fatalf("the build did not find %s, which the binary carries:\n%s", tc.ref, printed)
			}
			if !errors.Is(buildErr, wasm.ErrWasmPluginTypeMismatch()) {
				t.Fatalf("the build's refusal is not the one a loaded governance plugin earns as a pipeline step, so it is not shown to have loaded it from the binary:\n%s", printed)
			}
		})
	}
}
