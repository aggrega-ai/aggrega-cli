// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// inspectReferencesAggregate is the Aggregate the rows below name, by a path or by
// its name. Its file is called app.yaml and its name is not app, so a reading of
// the name as a file name finds nothing, while the resolution every build makes —
// a name looked up among the documents beside the Release — finds it.
const inspectReferencesAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: insp-references
  version: "1.0.0"
spec:
  components:
    api:
      type: core/server
      values:
        port: 8080
`

const inspectReferencesManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: insp-manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`

// inspectReferencesRelease names the Aggregate and the Manifest by head.
func inspectReferencesRelease(head string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
` + head + `  boundaries:
    main:
      directory: ./dist/main
      components:
        api:
          infra:
            replicas: 1
`
}

// TestInspectResolvesAReleasesReferencesAsABuildDoes drives the inspect command's
// view of a Release over a workspace beside a directory outside it.
//
// The command draws the graph a build of the same Release would render, so it
// resolves the documents the Release names as a build resolves them: a name by the
// documents beside the Release, and a path held to the workspace. The first row is
// the control that the documents are found at all; the second is a name the
// command could only resolve as a build does; the last two are references a build
// refuses, refused here with the same condition, before anything is read from
// outside.
func TestInspectResolvesAReleasesReferencesAsABuildDoes(t *testing.T) {
	for _, row := range []struct {
		name    string
		head    string
		refused bool
	}{
		{name: "control: both documents by a path beside the Release",
			head: "  aggregate: ./app.yaml\n  manifest: ./manifest.yaml\n"},
		{name: "an Aggregate by its name, from a file named otherwise",
			head: "  aggregate: insp-references\n  manifest: ./manifest.yaml\n"},
		{name: "an Aggregate by a path climbing out of the workspace",
			head: "  aggregate: ../outside/app.yaml\n  manifest: ./manifest.yaml\n", refused: true},
		{name: "a Manifest by a path climbing out of the workspace",
			head: "  aggregate: ./app.yaml\n  manifest: ../outside/manifest.yaml\n", refused: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			base := t.TempDir()
			root, outside := filepath.Join(base, "ws"), filepath.Join(base, "outside")
			for _, dir := range []string{root, outside} {
				writeFixture(t, filepath.Join(dir, "app.yaml"), inspectReferencesAggregate)
				writeFixture(t, filepath.Join(dir, "manifest.yaml"), inspectReferencesManifest)
			}
			writeFixture(t, filepath.Join(root, "release.yaml"), inspectReferencesRelease(row.head))
			catalogRoot := filepath.Join(root, "catalog")
			writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/server
  labels:
    component.aggrega.ai/type: "server"
  version: 1.0.0
spec:
  schema:
    values:
      port: { type: integer }
`)
			writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
			writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      c:
        image: x:latest
        ports:
          web: {{ .values.port }}
`)
			t.Chdir(root)

			err := renderReleaseFile(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				filepath.Join(root, "release.yaml"), nil, []string{catalogRoot}, testInspectOptions(io.Discard))
			if !row.refused {
				if err != nil {
					t.Fatalf("the Release's documents were not resolved as a build resolves them: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReferenceLeadsOutOfWorkspace()) {
				t.Fatalf("want the reference-leads-out-of-workspace halt, got %v", err)
			}
			if !strings.Contains(err.Error(), "../outside/") {
				t.Errorf("the halt does not name the reference as written: %v", err)
			}
		})
	}
}

// TestInspectHoldsAManifestNamedFromOutsideTheWorkspaceToItsOwnDirectory drives the
// inspect command's view of a Manifest the owner names on the command line from
// outside the workspace. Such a Manifest is a root of its own: its extends may name
// a base beside it, and one leading out of its own directory is refused before the
// base is read, naming that directory as the one the reference must stay inside.
func TestInspectHoldsAManifestNamedFromOutsideTheWorkspaceToItsOwnDirectory(t *testing.T) {
	for _, row := range []struct {
		name    string
		extends string
		refused bool
	}{
		{name: "control: a base beside the Manifest", extends: "./base.yaml"},
		{name: "a base climbing out of the Manifest's own directory", extends: "../base.yaml", refused: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			base := t.TempDir()
			root, named := filepath.Join(base, "ws"), filepath.Join(base, "outside", "manifests")
			for _, dir := range []string{filepath.Dir(named), named} {
				writeFixture(t, filepath.Join(dir, "base.yaml"), inspectReferencesManifest)
			}
			manifestPath := filepath.Join(named, "manifest.yaml")
			data := strings.Replace(inspectReferencesManifest, "\nspec:\n", "\nspec:\n  extends: "+row.extends+"\n", 1)
			writeFixture(t, manifestPath, data)
			writeFixture(t, filepath.Join(root, "README.md"), "a workspace holding nothing the Manifest names\n")

			err := renderManifestFile(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				manifestPath, []byte(data), nil, testInspectOptions(io.Discard))
			if !row.refused {
				if err != nil {
					t.Fatalf("the Manifest's base beside it was not read: %v", err)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReferenceLeadsOutOfWorkspace()) {
				t.Fatalf("want the reference-leads-out-of-workspace halt, got %v", err)
			}
			for _, want := range []string{
				".spec.extends: " + row.extends,
				"written in " + manifestPath,
				"outside " + named + ", the directory of a document named outside the workspace " + root,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not name %q: %v", want, err)
				}
			}
		})
	}
}
