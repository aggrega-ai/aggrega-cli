// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// inspectExtendsChild is a Release that names its parent and nothing else, so its
// Aggregate and Manifest are the parent's and reach it only through the fold.
const inspectExtendsChild = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: rel-child
  environment: testing
  version: "1.0.0"
spec:
  extends: ./release.yaml
`

// TestInspectFoldsAnExtendingRelease drives the inspect command's resolved view and
// its graph over a Release that extends another and declares nothing else.
//
// Such a Release inherits both of its references, so the views are drawn from the
// Release its extends chain folds to — the one a build of it composes — rather than
// failing on the empty references the child document holds on its own. The parent
// rows are the control that the same views of a Release extending nothing still
// draw the parent's own picture.
func TestInspectFoldsAnExtendingRelease(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "ws")
	writeFixture(t, filepath.Join(root, "app.yaml"), inspectReferencesAggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), inspectReferencesManifest)
	writeFixture(t, filepath.Join(root, "release.yaml"),
		inspectReferencesRelease("  aggregate: ./app.yaml\n  manifest: ./manifest.yaml\n"))
	writeFixture(t, filepath.Join(root, "release-child.yaml"), inspectExtendsChild)
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      c:
        image: x:latest
        ports:
          web: {{ .values.port }}
`)
	t.Chdir(root)

	for _, row := range []struct {
		name    string
		release string
		irGraph bool
		want    []string
	}{
		{name: "the resolved view of a Release extending another", release: "release-child.yaml",
			want: []string{"Release: rel-child:1.0.0\n", "Boundary: main\n", "Directory: ./dist/main\n", "Component: api"}},
		{name: "the graph of a Release extending another", release: "release-child.yaml", irGraph: true,
			want: []string{`"aggregate_name": "insp-references"`, "Release: rel-child:1.0.0\n"}},
		{name: "control: the resolved view of a Release extending nothing", release: "release.yaml",
			want: []string{"Release: rel:1.0.0\n", "Boundary: main\n", "Directory: ./dist/main\n", "Component: api"}},
		{name: "control: the graph of a Release extending nothing", release: "release.yaml", irGraph: true,
			want: []string{`"aggregate_name": "insp-references"`, "Release: rel:1.0.0\n"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			var buf bytes.Buffer
			opts := testInspectOptions(&buf)
			opts.irGraph = row.irGraph
			if err := renderReleaseFile(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				filepath.Join(root, row.release), nil, []string{catalogRoot}, opts); err != nil {
				t.Fatalf("inspect %s: %v", row.release, err)
			}
			out := buf.String()
			for _, want := range row.want {
				if !strings.Contains(out, want) {
					t.Errorf("output does not carry %q; got:\n%s", want, out)
				}
			}
		})
	}
}

// inspectManifestExtendsBase carries the whole mapping table, the Feature's
// realization included; inspectManifestExtendsChild extends it and maps only the
// component type, so the Feature's realization reaches the child only by
// inheritance.
const (
	inspectManifestExtendsBase = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: inspect-manifest-base
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/app: app-recipe:1.0.0
    features:
      type:
        feature.test/db-credential: db-credential-compose:1.0.0
`
	inspectManifestExtendsChild = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: inspect-manifest-child
  version: "1.0.0"
spec:
  extends: ./manifest-base.yaml
  mappings:
    components:
      type:
        core/app: app-recipe:1.0.0
`
)

// inspectManifestExtendsRelease is a Release extending nothing that names its
// Manifest by path.
func inspectManifestExtendsRelease(manifest string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: manifest-extends-release
  environment: local
  version: "1.0.0"
spec:
  aggregate: sensitive-redaction-app
  manifest: ` + manifest + `
  boundaries:
    cluster:
      directory: ./dist/cluster
      components:
        app: {}
`
}

// TestInspectComposesTheManifestsOwnExtendsChain drives the inspect command's
// resolved view over a Release whose Manifest inherits the mapping that realizes
// one of its attachments.
//
// A build composes a Manifest's extends chain before it binds any attachment, so
// the realization it renders for an inherited mapping is the parent Manifest's.
// The view draws the graph a build renders, so it names that realization too. The
// control row names a Manifest extending nothing that writes the same mapping
// itself, which the view has always realized.
func TestInspectComposesTheManifestsOwnExtendsChain(t *testing.T) {
	fixture, err := filepath.Abs("../../internal/compiler/testdata/sensitive-redaction")
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := os.ReadFile(filepath.Join(fixture, "sensitive-redaction-app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	catalogRoot := filepath.Join(fixture, "catalog", "base")
	root := filepath.Join(t.TempDir(), "ws")
	writeFixture(t, filepath.Join(root, "app.yaml"), string(aggregate))
	writeFixture(t, filepath.Join(root, "manifest-base.yaml"), inspectManifestExtendsBase)
	writeFixture(t, filepath.Join(root, "manifest-child.yaml"), inspectManifestExtendsChild)
	writeFixture(t, filepath.Join(root, "release-inherits.yaml"), inspectManifestExtendsRelease("./manifest-child.yaml"))
	writeFixture(t, filepath.Join(root, "release-writes.yaml"), inspectManifestExtendsRelease("./manifest-base.yaml"))
	t.Chdir(root)

	const realized = "    - credentials (feature.test/db-credential:1.0.0)    [slot: credentials, sensitive, realization: db-credential-compose:1.0.0]    [explicit]"
	for _, row := range []struct {
		name    string
		release string
	}{
		{name: "a Manifest inheriting the mapping through its extends chain", release: "release-inherits.yaml"},
		{name: "control: a Manifest extending nothing that writes the mapping", release: "release-writes.yaml"},
	} {
		t.Run(row.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderReleaseFile(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				filepath.Join(root, row.release), nil, []string{catalogRoot}, testInspectOptions(&buf)); err != nil {
				t.Fatalf("inspect %s: %v", row.release, err)
			}
			if out := buf.String(); !strings.Contains(out, realized) {
				t.Errorf("output does not carry %q; got:\n%s", realized, out)
			}
		})
	}
}
