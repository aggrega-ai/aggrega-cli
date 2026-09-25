// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// TestALocalSourcePinsExactlyWhatItContributes holds a local source's pins to the
// declared set's spec.source declaration, file for file: the pins name exactly the
// files that declaration contributes, bar the entity document and the lock, and
// each digest is the content digest of the file's bytes.
//
// The files beside each entity include ones nothing declares — a note, build
// output, a hidden file — so a pin taken over a directory listing rather than
// over the declaration fails a row by naming the file it should not have pinned.
// The whole-directory rows are the one shape where the declaration itself reaches
// the document and the lock, which is what the two exclusions are for.
func TestALocalSourcePinsExactlyWhatItContributes(t *testing.T) {
	stray := map[string]string{"NOTES.md": "a note\n", "dist/out.yaml": "rendered: true\n", ".hidden": "x"}
	with := func(more map[string]string) map[string]string {
		out := map[string]string{"entity.lock": "version: 1\n"}
		for k, v := range stray {
			out[k] = v
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	rows := []struct {
		name     string
		files    map[string]string
		want     []string
		declares bool
	}{
		{
			name: "a source naming a subdirectory pins the files under it and nothing beside it",
			files: with(map[string]string{
				"recipe.yaml":             declaredRecipeHead + "  source: ./chart\n  valuesMapping: '{}'\n",
				"chart/Chart.yaml":        declaredChart,
				"chart/templates/cm.yaml": "kind: ConfigMap\n",
				"chart/.helmignore":       "*.tmp\n",
			}),
			want:     []string{"chart/Chart.yaml", "chart/templates/cm.yaml"},
			declares: true,
		},
		{
			name: "a source written as a template pins every file in the directory bar the document and the lock",
			files: with(map[string]string{
				"recipe.yaml":           declaredRecipeHead + "  source: \"./charts/{{ .values.flavour }}\"\n  valuesMapping: '{}'\n",
				"charts/app/Chart.yaml": declaredChart,
				"charts/db/Chart.yaml":  declaredChart,
			}),
			want:     []string{"NOTES.md", "charts/app/Chart.yaml", "charts/db/Chart.yaml", "dist/out.yaml"},
			declares: true,
		},
		{
			name: "a source naming the entity's own directory pins every file there bar the document and the lock",
			files: with(map[string]string{
				"recipe.yaml": declaredRecipeHead + "  source: .\n  valuesMapping: '{}'\n",
				"Chart.yaml":  declaredChart,
			}),
			want:     []string{"Chart.yaml", "NOTES.md", "dist/out.yaml"},
			declares: true,
		},
		{
			name: "a remote source is no local source and pins nothing here",
			files: with(map[string]string{
				"recipe.yaml": declaredRecipeHead + "  source: oci://registry.example/charts/app\n  valuesMapping: '{}'\n",
			}),
		},
		{
			name:  "an entity with no source pins nothing here",
			files: with(map[string]string{"recipe.yaml": declaredRecipeHead + "  workload: |\n    kind: Workload\n"}),
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			doc := declaredFixture(t, "recipe.yaml", row.files)
			data, err := os.ReadFile(doc)
			if err != nil {
				t.Fatal(err)
			}

			declared, err := DeclaredFilesOf(data, doc)
			if err != nil {
				t.Fatalf("DeclaredFilesOf: %v", err)
			}
			files, declares := declared.LocalSourceFiles()
			if declares != row.declares {
				t.Fatalf("LocalSourceFiles declares = %v; want %v", declares, row.declares)
			}
			if len(files) == 0 {
				files = nil
			}
			if !reflect.DeepEqual(files, row.want) {
				t.Errorf("LocalSourceFiles = %v; want %v", files, row.want)
			}

			pins, err := LocalSourcePinsOf(data, doc)
			if err != nil {
				t.Fatalf("LocalSourcePinsOf: %v", err)
			}
			var pinned []string
			for _, p := range pins {
				pinned = append(pinned, p.Path)
				body, rerr := os.ReadFile(filepath.Join(filepath.Dir(doc), filepath.FromSlash(p.Path)))
				if rerr != nil {
					t.Fatalf("read pinned file %s: %v", p.Path, rerr)
				}
				if p.Digest != digest.ContentDigest(body) {
					t.Errorf("the pin for %s is %s; the file digests to %s", p.Path, p.Digest, digest.ContentDigest(body))
				}
			}
			if !reflect.DeepEqual(pinned, row.want) {
				t.Errorf("the pins name %v; the declaration contributes %v — the two must be one set", pinned, row.want)
			}
			for _, p := range pinned {
				if p == "recipe.yaml" || p == recipepin.LockFileName {
					t.Errorf("the pins name %s, which is never an input the lock can pin", p)
				}
			}

			byPath, err := LocalSourcePinsAt(doc)
			if err != nil {
				t.Fatalf("LocalSourcePinsAt: %v", err)
			}
			if !reflect.DeepEqual(byPath, pins) {
				t.Errorf("reading the document from disk gave %+v; reading its bytes gave %+v", byPath, pins)
			}
		})
	}
}
