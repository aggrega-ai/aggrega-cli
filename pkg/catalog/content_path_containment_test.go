// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests the bound on every path an entity declares for content it carries: each
// such path is read against the directory holding the entity's document, and one
// that leaves that directory — by climbing out of it, or by naming an absolute
// location — is refused as the document is parsed, under one condition.
//
// The rows are the surfaces, one per property of the four kinds that carry
// content, each driven in both directions through the per-entity parse every
// catalog read, pull, build and publication of the entity goes through. The
// escaping rows name a file that EXISTS outside the entity's directory, so a
// refusal is attributable to where the path points rather than to a missing
// file. The inside rows climb and come back, so a check keyed on the `..` token
// rather than on where the path lands fails them.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// containmentSurface is one content-path surface: the kind, the document it is
// authored in with a placeholder where the path goes, and the file the path names
// relative to the entity's directory.
type containmentSurface struct {
	name   string
	kind   string
	target string
	doc    string
}

// containmentPathMarker stands in for the authored path in every surface's
// document.
const containmentPathMarker = "__PATH__"

func containmentSurfaces() []containmentSurface {
	const recipeHead = `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: contained
  version: 1.0.0
spec:
  capability: helm
  implements: core/compute/server:1.0.0
`
	const featureRecipeHead = `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: contained-fr
  version: 1.0.0
spec:
  capability: helm
  implements: example/sidecar:1.0.0
`
	const componentHead = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: contained
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
`
	const featureHead = `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/contained
  version: 1.0.0
spec:
`
	return []containmentSurface{
		{"Recipe source", "Recipe", "charts/app", recipeHead + "  source: " + containmentPathMarker + "\n  valuesMapping: '{}'\n"},
		{"Recipe structured source url", "Recipe", "charts/app", recipeHead + "  source:\n    url: " + containmentPathMarker + "\n  valuesMapping: '{}'\n"},
		{"Recipe valuesMapping import", "Recipe", "mapping.tmpl", recipeHead + "  source: ./charts/app\n  valuesMapping:\n    $import: " + containmentPathMarker + "\n"},
		{"Recipe workload import", "Recipe", "workload.tmpl", recipeHead + "  workload:\n    $import: " + containmentPathMarker + "\n"},
		{"Recipe artifacts import", "Recipe", "extra.yaml", recipeHead + "  artifacts:\n    extra.yaml:\n      $import: " + containmentPathMarker + "\n"},
		{"FeatureRecipe source", "FeatureRecipe", "charts/app", featureRecipeHead + "  source: " + containmentPathMarker + "\n  valuesMapping: '{}'\n"},
		{"FeatureRecipe valuesMapping import", "FeatureRecipe", "mapping.tmpl", featureRecipeHead + "  source: ./charts/app\n  valuesMapping:\n    $import: " + containmentPathMarker + "\n"},
		{"FeatureRecipe workload import", "FeatureRecipe", "workload.tmpl", featureRecipeHead + "  workload:\n    $import: " + containmentPathMarker + "\n"},
		{"FeatureRecipe artifacts import", "FeatureRecipe", "extra.yaml", featureRecipeHead + "  artifacts:\n    extra.yaml:\n      $import: " + containmentPathMarker + "\n"},
		{"Component whole schema import", "Component", "schema.yaml", componentHead + "  schema:\n    $import: " + containmentPathMarker + "\n"},
		{"Component pillar schema import", "Component", "values.yaml", componentHead + "  schema:\n    values:\n      $import: " + containmentPathMarker + "\n"},
		{"Component socket value schema import", "Component", "peer-values.yaml", componentHead + "  usesSchema:\n    peer:\n      selector: \"component.aggrega.ai/type=server\"\n      valueSchema:\n        $import: " + containmentPathMarker + "\n"},
		{"Component socket infra schema import", "Component", "peer-infra.yaml", componentHead + "  usesSchema:\n    peer:\n      selector: \"component.aggrega.ai/type=server\"\n      infraSchema:\n        $import: " + containmentPathMarker + "\n"},
		{"Feature schema import", "Feature", "schema.yaml", featureHead + "  schema:\n    $import: " + containmentPathMarker + "\n"},
	}
}

// containmentBodies is the content each target file holds, inside the entity's
// directory and outside it alike: a schema file must parse as schema content for
// the inside rows to load, and the rest are read only at render.
var containmentBodies = map[string]string{
	"schema.yaml":      "values:\n  name:\n    type: string\n    default: x\n",
	"values.yaml":      "name:\n  type: string\n  default: x\n",
	"peer-values.yaml": "weight:\n  type: integer\n  default: 1\n",
	"peer-infra.yaml":  "timeout:\n  type: string\n  default: 5s\n",
}

// containmentFixture writes the surface's document at <root>/entity/1.0.0/<kind>.yaml
// with the path in place, writes the target file both inside that directory and at
// <root>/outside, and returns the document path and the root.
func containmentFixture(t *testing.T, s containmentSurface, authored func(root string) string) (string, string) {
	t.Helper()
	root := t.TempDir()
	entityDir := filepath.Join(root, "entity", "1.0.0")
	for _, dir := range []string{entityDir, filepath.Join(root, "outside")} {
		target := filepath.Join(dir, filepath.FromSlash(s.target))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		body, ok := containmentBodies[s.target]
		if !ok {
			body = "content\n"
		}
		if strings.HasPrefix(s.target, "charts/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			target = filepath.Join(target, "Chart.yaml")
			body = "apiVersion: v2\nname: app\nversion: 0.1.0\n"
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	doc := filepath.Join(entityDir, strings.ToLower(s.kind)+".yaml")
	text := strings.ReplaceAll(s.doc, containmentPathMarker, authored(root))
	if err := os.WriteFile(doc, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return doc, root
}

// parseContainmentSurface runs the document through the per-entity parse its kind
// goes through on every catalog read.
func parseContainmentSurface(t *testing.T, kind, doc string) error {
	t.Helper()
	data := mustRead(t, doc)
	var err error
	switch kind {
	case "Recipe":
		_, _, err = parseRecipeEntity(data, doc)
	case "FeatureRecipe":
		_, _, err = parseFeatureRecipeEntity(data, doc)
	case "Component":
		_, err = parseComponentEntity(data, doc)
	case "Feature":
		_, err = parseFeatureEntity(data, doc)
	default:
		t.Fatalf("no parse for kind %q", kind)
	}
	return err
}

// TestEveryContentPathThatLeavesTheEntityDirectoryIsRefused drives each surface
// with a path that climbs out of the entity's directory and with an absolute path,
// both naming a file that exists, and asserts each is refused with the registered
// condition under its short code, naming the path the author wrote.
func TestEveryContentPathThatLeavesTheEntityDirectoryIsRefused(t *testing.T) {
	for _, s := range containmentSurfaces() {
		for _, mode := range []struct {
			name     string
			authored func(s containmentSurface) func(root string) string
		}{
			{"a climb out", func(s containmentSurface) func(string) string {
				return func(string) string { return "../../outside/" + s.target }
			}},
			{"an absolute path", func(s containmentSurface) func(string) string {
				return func(root string) string { return filepath.Join(root, "outside", filepath.FromSlash(s.target)) }
			}},
		} {
			t.Run(s.name+"/"+mode.name, func(t *testing.T) {
				authored := mode.authored(s)
				doc, root := containmentFixture(t, s, authored)
				err := parseContainmentSurface(t, s.kind, doc)
				if err == nil {
					t.Fatal("a path outside the entity's directory was accepted")
				}
				if !errors.Is(err, sourceref.ErrContentOutsideEntityDirectory()) {
					t.Fatalf("refused for another reason: %v", err)
				}
				if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0304" {
					t.Errorf("short code = %q (%v), want E0304", code, ok)
				}
				if chain := cerrors.CauseChain(err); !strings.Contains(chain, authored(root)) {
					t.Errorf("the refusal does not name the authored path %q: %s", authored(root), chain)
				}
			})
		}
	}
}

// TestEveryContentPathThatStaysInsideTheEntityDirectoryLoads is the other
// direction: the same surfaces with a path that climbs into a sibling directory
// of the entity's own and back, which lands inside and loads.
func TestEveryContentPathThatStaysInsideTheEntityDirectoryLoads(t *testing.T) {
	for _, s := range containmentSurfaces() {
		t.Run(s.name, func(t *testing.T) {
			doc, _ := containmentFixture(t, s, func(string) string { return "./sub/../" + s.target })
			if err := os.MkdirAll(filepath.Join(filepath.Dir(doc), "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := parseContainmentSurface(t, s.kind, doc); err != nil {
				t.Fatalf("a path that stays inside the entity's directory was refused: %v", err)
			}
		})
	}
}
