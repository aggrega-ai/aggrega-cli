// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
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
