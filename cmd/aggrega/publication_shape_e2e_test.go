// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// shapeRecipe is a Recipe authored with the spec lines the caller supplies.
func shapeRecipe(spec string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: shaped
  version: 1.0.0
spec:
  capability: helm
  implements: core/compute/server:1.0.0
` + spec
}

// shapeComponent is a Component authored with the spec lines the caller supplies.
func shapeComponent(spec string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/shaped
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
` + spec
}

const shapeChart = "apiVersion: v2\nname: app\nversion: 0.1.0\n"

// TestAPublicationCarriesExactlyWhatItsEntityDeclares drives the production
// publication of one entity per row into an in-memory registry and reads back what
// landed: its media type, whether it is the document or an archive, and — for an
// archive — every entry it holds.
//
// The rows are the shapes the rule tells apart. An entity declaring nothing beside
// its document publishes as that document, byte for byte, with a stray note beside
// it or without. One declaring a file publishes an archive holding the document and
// that file and nothing else. A local source naming one of two sibling directories
// publishes that directory alone, beside the lock its pin wrote; one written as a
// template over both publishes the whole directory and says so. And an Aggregate at
// a workspace root — beside the Release, the Manifest, the workspace configuration
// and a build's output — publishes as its document alone, because nothing it
// declares names any of them.
//
// A packer that archived everything passes every archive row and fails the document
// rows; one that walked the directory passes the document rows only where the
// directory happens to be empty. Each row carries a file nothing declares, so a
// directory listing that survived anywhere is named by the entry it lets through.
func TestAPublicationCarriesExactlyWhatItsEntityDeclares(t *testing.T) {
	rows := []struct {
		name      string
		entity    string
		files     map[string]string
		archive   bool
		entries   []string
		wholeNote bool
	}{
		{
			name:   "an entity alone publishes as its document",
			entity: "component/core/compute/shaped/1.0.0/component.yaml",
			files: map[string]string{
				"component/core/compute/shaped/1.0.0/component.yaml": shapeComponent("  schema:\n    values:\n      name: { type: string, default: x }\n"),
			},
		},
		{
			name:   "a file it declares makes it an archive of the document and that file",
			entity: "component/core/compute/shaped/1.0.0/component.yaml",
			files: map[string]string{
				"component/core/compute/shaped/1.0.0/component.yaml": shapeComponent("  schema:\n    $import: ./schema.yaml\n"),
				"component/core/compute/shaped/1.0.0/schema.yaml":    "values:\n  name:\n    type: string\n    default: x\n",
				"component/core/compute/shaped/1.0.0/NOTES.md":       "a note nothing declares\n",
			},
			archive: true,
			entries: []string{"component.yaml", "schema.yaml"},
		},
		{
			name:   "a stray note beside an entity declaring nothing changes nothing",
			entity: "recipe/helm/shaped/1.0.0/recipe.yaml",
			files: map[string]string{
				"recipe/helm/shaped/1.0.0/recipe.yaml": shapeRecipe("  workload: |\n    apiVersion: aggrega.ai/v1alpha1\n    kind: Workload\n"),
				"recipe/helm/shaped/1.0.0/README.md":   "a note nothing declares\n",
			},
		},
		{
			name:   "a source naming one subdirectory publishes that subdirectory",
			entity: "recipe/helm/shaped/1.0.0/recipe.yaml",
			files: map[string]string{
				"recipe/helm/shaped/1.0.0/recipe.yaml":                  shapeRecipe("  source: ./charts/app\n  valuesMapping: '{}'\n"),
				"recipe/helm/shaped/1.0.0/charts/app/Chart.yaml":        shapeChart,
				"recipe/helm/shaped/1.0.0/charts/app/templates/cm.yaml": "kind: ConfigMap\n",
				"recipe/helm/shaped/1.0.0/charts/other/Chart.yaml":      shapeChart,
				"recipe/helm/shaped/1.0.0/NOTES.md":                     "a note nothing declares\n",
			},
			archive: true,
			entries: []string{"charts/app/Chart.yaml", "charts/app/templates/cm.yaml", "entity.lock", "recipe.yaml"},
		},
		{
			name:   "a source written as a template over two subdirectories publishes both, and says so",
			entity: "recipe/helm/shaped/1.0.0/recipe.yaml",
			files: map[string]string{
				"recipe/helm/shaped/1.0.0/recipe.yaml": shapeRecipe("  locals:\n    flavour: app\n" +
					"  source: \"./charts/{{ .locals.flavour }}\"\n  valuesMapping: '{}'\n"),
				"recipe/helm/shaped/1.0.0/charts/app/Chart.yaml":   shapeChart,
				"recipe/helm/shaped/1.0.0/charts/other/Chart.yaml": shapeChart,
				"recipe/helm/shaped/1.0.0/NOTES.md":                "a note nothing declares, carried with the whole directory\n",
			},
			archive:   true,
			entries:   []string{"NOTES.md", "charts/app/Chart.yaml", "charts/other/Chart.yaml", "entity.lock", "recipe.yaml"},
			wholeNote: true,
		},
		{
			name:   "an Aggregate at a workspace root publishes as its document alone",
			entity: "app.yaml",
			files: map[string]string{
				"app.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: app\n  version: 1.0.0\n" +
					"spec:\n  components:\n    web:\n      type: core/compute/shaped:1.0.0\n",
				"release.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: dev\n  version: 1.0.0\n" +
					"spec:\n  aggregate: app.yaml\n  manifest: manifest.yaml\n",
				"manifest.yaml":   "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: m\n  version: 1.0.0\n",
				"release.lock":    "version: 1\n",
				"dist/web.yaml":   "rendered: output\n",
				"parameters.yaml": "replicas: 3\n",
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			for rel, body := range row.files {
				writeEntityFixture(t, filepath.Join(root, filepath.FromSlash(rel)), body)
			}
			entityPath := filepath.Join(root, filepath.FromSlash(row.entity))
			store := memory.New()
			var out bytes.Buffer
			// Each publication pins first, as its author would: a Recipe reading a
			// local source is refused unpinned, and the lock the pin writes is then
			// part of what that Recipe declares. An entity with nothing to pin is
			// left exactly as it was.
			if err := pushRun(t.Context(), pushConfig{
				Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				EntityPath: entityPath,
				Registry:   "example.com",
				PushTarget: store,
				Out:        &out,
				PinSources: boolPtr(true),
			}); err != nil {
				t.Fatalf("pushRun: %v\n%s", err, out.String())
			}
			pulled, err := oci.Pull(context.Background(), store, "1.0.0")
			if err != nil {
				t.Fatalf("oci.Pull: %v", err)
			}
			kind, archive, ok := oci.KindOfMediaType(pulled.MediaType)
			if !ok || archive != row.archive {
				t.Fatalf("published as %q (kind %q, archive %v); want archive %v", pulled.MediaType, kind, archive, row.archive)
			}
			if !row.archive {
				if !bytes.Equal(pulled.Bytes, []byte(row.files[row.entity])) || oci.IsTarGzBundle(pulled.Bytes) {
					t.Errorf("published %d bytes; want the authored document, byte for byte", len(pulled.Bytes))
				}
			} else {
				names := tarEntryNames(t, pulled.Bytes)
				sort.Strings(names)
				if !reflect.DeepEqual(names, row.entries) {
					t.Errorf("archive carries %q; want exactly %q", names, row.entries)
				}
			}
			const note = "carries: every file in the entity's directory"
			if got := strings.Contains(out.String(), note); got != row.wholeNote {
				t.Errorf("the output says it carries the whole directory: %v; want %v. Output:\n%s", got, row.wholeNote, out.String())
			}
			t.Logf("%s: %s, %d bytes", row.name, pulled.MediaType, len(pulled.Bytes))
		})
	}
}
