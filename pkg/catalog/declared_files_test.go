// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
)

// declaredFixture writes files (relative path → body) into a fresh entity
// directory and returns the path of the document at doc among them.
func declaredFixture(t *testing.T, doc string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "entity", "1.0.0")
	writeTree(t, dir, files)
	return filepath.Join(dir, doc)
}

const declaredRecipeHead = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: declared
  version: 1.0.0
spec:
  capability: helm
  implements: core/compute/server:1.0.0
`

const declaredComponentHead = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: declared
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
`

const declaredChart = "apiVersion: v2\nname: app\nversion: 0.1.0\n"

// TestDeclaredFilesAreWhatTheEntityNames drives the declared set over each shape
// an entity's declarations take, and over the files that sit beside it and are
// named by nothing. Each row states the files the publication carries, so a row
// whose set grew by a file nothing declares — the directory listing coming back
// through a side door — fails by naming the file.
func TestDeclaredFilesAreWhatTheEntityNames(t *testing.T) {
	stray := map[string]string{"NOTES.md": "a note nothing declares\n", "dist/out.yaml": "rendered: true\n"}
	with := func(base map[string]string, more map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	rows := []struct {
		name     string
		doc      string
		files    map[string]string
		want     []string
		whole    string
		surfaces []string
	}{
		{
			name:  "a Recipe declaring nothing carries nothing, whatever sits beside it",
			doc:   "recipe.yaml",
			files: with(stray, map[string]string{"recipe.yaml": declaredRecipeHead + "  workload: |\n    kind: Workload\n"}),
			want:  nil,
		},
		{
			name: "a local source directory contributes every file under it bar the hidden ones",
			doc:  "recipe.yaml",
			files: with(stray, map[string]string{
				"recipe.yaml":                declaredRecipeHead + "  source: ./chart\n  valuesMapping: '{}'\n",
				"chart/Chart.yaml":           declaredChart,
				"chart/templates/cm.yaml":    "kind: ConfigMap\n",
				"chart/.editor-leftover.swp": "x",
			}),
			want:     []string{"chart/Chart.yaml", "chart/templates/cm.yaml"},
			surfaces: []string{"spec.source"},
		},
		{
			name: "a structured source's local url is read like a scalar source",
			doc:  "recipe.yaml",
			files: with(stray, map[string]string{
				"recipe.yaml":      declaredRecipeHead + "  source:\n    url: ./chart\n  valuesMapping: '{}'\n",
				"chart/Chart.yaml": declaredChart,
			}),
			want:     []string{"chart/Chart.yaml"},
			surfaces: []string{"spec.source"},
		},
		{
			name: "a local source file contributes that file",
			doc:  "recipe.yaml",
			files: with(stray, map[string]string{
				"recipe.yaml":  declaredRecipeHead + "  source: ./compose.yaml\n  valuesMapping: '{}'\n",
				"compose.yaml": "services: {}\n",
			}),
			want:     []string{"compose.yaml"},
			surfaces: []string{"spec.source"},
		},
		{
			name: "every file an import names travels, a hidden one included, and an inline entry names none",
			doc:  "recipe.yaml",
			files: with(stray, map[string]string{
				"recipe.yaml": declaredRecipeHead + "  workload:\n    $import: ./.workload.tmpl\n  artifacts:\n" +
					"    a.yaml:\n      $import: ./extras/a.yaml\n    b.yaml: |\n      kind: ConfigMap\n",
				".workload.tmpl": "kind: Workload\n",
				"extras/a.yaml":  "kind: ConfigMap\n",
			}),
			want:     []string{".workload.tmpl", "extras/a.yaml"},
			surfaces: []string{"spec.workload", "spec.artifacts.a.yaml"},
		},
		{
			name: "a remote source contributes nothing and the lock beside the entity travels",
			doc:  "recipe.yaml",
			files: with(stray, map[string]string{
				"recipe.yaml": declaredRecipeHead + "  source: oci://registry.example/charts/app\n  valuesMapping: '{}'\n",
				"entity.lock": "version: 1\n",
			}),
			want:     []string{"entity.lock"},
			surfaces: []string{"entity.lock"},
		},
		{
			name: "a local source written as a template contributes the whole directory, and says so",
			doc:  "recipe.yaml",
			files: with(stray, map[string]string{
				"recipe.yaml":      declaredRecipeHead + "  locals:\n    dir: ./chart\n  source: \"{{ .locals.dir }}\"\n  valuesMapping: '{}'\n",
				"chart/Chart.yaml": declaredChart,
				"other/Chart.yaml": declaredChart,
				".hidden":          "x",
			}),
			want:     []string{"NOTES.md", "chart/Chart.yaml", "dist/out.yaml", "other/Chart.yaml"},
			whole:    "{{ .locals.dir }}",
			surfaces: []string{"spec.source"},
		},
		{
			name: "a declared path absent from disk contributes nothing",
			doc:  "recipe.yaml",
			files: map[string]string{
				"recipe.yaml": declaredRecipeHead + "  source: ./chart\n  valuesMapping: '{}'\n",
			},
			want:     nil,
			surfaces: []string{"spec.source"},
		},
		{
			name: "a Component carries every file its schema imports read, a chain's included",
			doc:  "component.yaml",
			files: with(stray, map[string]string{
				"component.yaml": declaredComponentHead + "  schema:\n    $import: ./schema.yaml\n" +
					"  usesSchema:\n    peer:\n      selector: \"component.aggrega.ai/type=server\"\n" +
					"      valueSchema:\n        $import: ./sockets/peer-values.yaml\n",
				"schema.yaml":              "$import: ./schemas/real.yaml\n",
				"schemas/real.yaml":        "values:\n  name:\n    type: string\n    default: x\n",
				"sockets/peer-values.yaml": "weight:\n  type: integer\n  default: 1\n",
				"schemas/unused.yaml":      "values: {}\n",
			}),
			want:     []string{"schema.yaml", "schemas/real.yaml", "sockets/peer-values.yaml"},
			surfaces: []string{"spec.schema", "spec.usesSchema.peer.valueSchema"},
		},
		{
			name: "a Component whose contract is inline carries nothing",
			doc:  "component.yaml",
			files: with(stray, map[string]string{
				"component.yaml": declaredComponentHead + "  schema:\n    values:\n      name: { type: string, default: x }\n",
			}),
			want: nil,
		},
		{
			name: "an Aggregate at a workspace root carries nothing of the workspace",
			doc:  "aggregate.yaml",
			files: with(stray, map[string]string{
				"aggregate.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: app\n  version: 1.0.0\n" +
					"spec:\n  components:\n    web:\n      type: core/compute/server:1.0.0\n",
				"release.yaml":         "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: dev\n",
				"manifest.yaml":        "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: m\n",
				".aggrega-config.yaml": "workspace: {}\n",
				"release.lock":         "version: 1\n",
			}),
			want: nil,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			path := declaredFixture(t, row.doc, row.files)
			got, err := DeclaredFilesOf(mustRead(t, path), path)
			if err != nil {
				t.Fatalf("DeclaredFilesOf: %v", err)
			}
			if files := got.Files(); !reflect.DeepEqual(files, row.want) {
				t.Errorf("carried files = %q; want %q", files, row.want)
			}
			if w := got.WholeDirectory(); w != row.whole {
				t.Errorf("whole-directory source = %q; want %q", w, row.whole)
			}
			var surfaces []string
			for _, d := range got.Declarations {
				surfaces = append(surfaces, d.Surface)
			}
			if !reflect.DeepEqual(surfaces, row.surfaces) {
				t.Errorf("declarations = %q; want %q", surfaces, row.surfaces)
			}
		})
	}
}

// TestDeclaredFilesReachEverySurfaceTheParseContains holds the declared set to the
// containment table: every surface the per-entity parse anchors and holds inside
// the entity's directory is one whose file a publication carries. A content
// surface the parse learned and the declared set did not would be a file the
// entity reads and its consumers never receive, and it would fail here by name.
func TestDeclaredFilesReachEverySurfaceTheParseContains(t *testing.T) {
	surfaces := containmentSurfaces()
	if len(surfaces) == 0 {
		t.Fatal("the containment table lists no surfaces, so this asserts nothing")
	}
	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			doc, _ := containmentFixture(t, s, func(string) string { return "./" + s.target })
			got, err := DeclaredFilesOf(mustRead(t, doc), doc)
			if err != nil {
				t.Fatalf("DeclaredFilesOf: %v", err)
			}
			want := s.target
			if strings.HasPrefix(s.target, "charts/") {
				want = s.target + "/Chart.yaml"
			}
			found := false
			for _, f := range got.Files() {
				found = found || f == want
			}
			if !found {
				t.Errorf("the %s names %s and the declared set carries %q without it", s.name, want, got.Files())
			}
		})
	}
}

// TestDeclaredFilesRefuseWhatTheParseRefuses pins that the declared set is read
// through the parse rather than beside it: a document naming content outside its
// own directory is refused under the containment condition, not listed.
func TestDeclaredFilesRefuseWhatTheParseRefuses(t *testing.T) {
	path := declaredFixture(t, "recipe.yaml", map[string]string{
		"recipe.yaml": declaredRecipeHead + "  source: ../outside\n  valuesMapping: '{}'\n",
	})
	if err := os.MkdirAll(filepath.Join(filepath.Dir(path), "..", "outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := DeclaredFilesOf(mustRead(t, path), path)
	if !errors.Is(err, sourceref.ErrContentOutsideEntityDirectory()) {
		t.Fatalf("a source leaving the entity's directory was not refused under the containment condition: %v", err)
	}
}
