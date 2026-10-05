// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// entityNamesFixture lays out the two populations a completion naming an entity
// draws on: a workspace root holding documents of several kinds, and a catalog
// directory OUTSIDE that root holding what a workspace consumes rather than
// authors. It returns the root and the catalog directory.
//
// The catalog directory sits outside the root deliberately. A catalog directory
// under the root is also reached by the workspace walk, so a test built that way
// would pass with the catalog population dropped entirely — the walk would
// supply the same names by the other route.
//
// Each population carries something the other lacks, one name both carry, one
// name the catalog holds at two versions, and a Recipe the directory index holds
// no index for.
func entityNamesFixture(t *testing.T) (root, catalogDir string) {
	t.Helper()
	root = t.TempDir()
	catalogDir = t.TempDir()
	doc := func(kind, name, version, extra string) string {
		return "apiVersion: aggrega.ai/v1alpha1\nkind: " + kind + "\nmetadata:\n  name: " + name +
			"\n  version: \"" + version + "\"\nspec:" + extra + "\n"
	}
	files := map[string]string{
		filepath.Join(root, "ws-component.yaml"):                                          doc("Component", "ws/server", "1.0.0", " {}"),
		filepath.Join(root, "shared-component.yaml"):                                      doc("Component", "shared/db", "1.0.0", " {}"),
		filepath.Join(root, "ws-feature.yaml"):                                            doc("Feature", "ws/tagging", "1.0.0", " {}"),
		filepath.Join(root, "ws-recipe.yaml"):                                             doc("Recipe", "ws-recipe", "1.0.0", "\n  capability: helm\n  implements: ws/server"),
		filepath.Join(root, "aggregate.yaml"):                                             doc("Aggregate", "app", "1.0.0", " {}"),
		filepath.Join(catalogDir, "component", "cat", "cache", "1.0.0", "component.yaml"): doc("Component", "cat/cache", "1.0.0", " {}"),
		filepath.Join(catalogDir, "component", "cat", "cache", "2.0.0", "component.yaml"): doc("Component", "cat/cache", "2.0.0", " {}"),
		filepath.Join(catalogDir, "component", "shared", "db", "1.0.0", "component.yaml"): doc("Component", "shared/db", "1.0.0", " {}"),
		filepath.Join(catalogDir, "feature", "cat", "backup", "1.0.0", "feature.yaml"):    doc("Feature", "cat/backup", "1.0.0", " {}"),
		filepath.Join(catalogDir, "recipe", "helm", "cat-recipe", "1.0.0", "recipe.yaml"): doc("Recipe", "cat-recipe", "1.0.0", "\n  capability: helm\n  implements: cat/cache"),
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root, catalogDir
}

// TestEntityNamesDrawOnTheWorkspaceAndTheCatalogDirectories asserts the exact
// candidate list the enumeration yields per kind, over a workspace document set,
// an unsaved buffer and a catalog directory.
//
// A planted defect makes it fail: drop the catalog population from
// entityNamesIn — the loop reading the lookup's directory index — and the
// Component row loses cat/cache while the Feature row loses cat/backup, each row
// naming what it is missing. The Recipe row is the other direction: a catalog
// Recipe offered here would mean the enumeration had read a source other than
// the directory index, and that row would name it.
//
// The rows assert the whole list, order included, rather than membership. A
// name offered twice, a name offered once per version, and a list whose order
// followed delivery rather than the names would each pass a membership check.
func TestEntityNamesDrawOnTheWorkspaceAndTheCatalogDirectories(t *testing.T) {
	t.Parallel()
	root, catalogDir := entityNamesFixture(t)
	view := catalog.NewLazyLookup(root, "", []string{catalogDir}, nil)

	// An unsaved buffer the editor is holding, which no file on disk carries: the
	// workspace half of the answer reads it first.
	var buffer yaml.Node
	if err := yaml.Unmarshal([]byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: buffer/only\n"), &buffer); err != nil {
		t.Fatalf("parse the unsaved buffer: %v", err)
	}
	overlay := []workspaceDoc{{path: filepath.Join(root, "unsaved.yaml"), node: &buffer}}

	for _, tc := range []struct {
		name string
		view *catalog.LazyLookup
		kind string
		want []string
	}{
		{"components from both populations, each name once", view, "Component",
			[]string{"buffer/only", "cat/cache", "shared/db", "ws/server"}},
		{"features from both populations", view, "Feature",
			[]string{"cat/backup", "ws/tagging"}},
		{"a Recipe is the workspace's alone, since the directory index holds no recipe kind", view, "Recipe",
			[]string{"ws-recipe"}},
		{"an Aggregate is the workspace's alone, for the same reason", view, "Aggregate",
			[]string{"app"}},
		{"no catalog lookup leaves the workspace population", nil, "Component",
			[]string{"buffer/only", "shared/db", "ws/server"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := entityNamesIn(context.Background(), root, workspaceconfig.Config{}, tc.view, overlay, tc.kind, workspaceconfig.ReleaseReach{})
			if !slices.Equal(got, tc.want) {
				t.Errorf("entityNamesIn(%q) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}

// TestEntityNamesReadNoDirectoryIndexForAWithdrawnRequest pins the cancellation
// half: a request withdrawn before the enumeration runs walks no workspace file,
// and does not go on to read the catalog directories either. The live control
// over the same inputs is what makes the empty answer mean something — without
// it, an enumeration that returned nothing under every circumstance would pass.
func TestEntityNamesReadNoDirectoryIndexForAWithdrawnRequest(t *testing.T) {
	t.Parallel()
	root, catalogDir := entityNamesFixture(t)
	view := catalog.NewLazyLookup(root, "", []string{catalogDir}, nil)

	withdrawn, stop := context.WithCancel(context.Background())
	stop()
	if got := entityNamesIn(withdrawn, root, workspaceconfig.Config{}, view, nil, "Component", workspaceconfig.ReleaseReach{}); len(got) != 0 {
		t.Errorf("a withdrawn request enumerated %q; it was supposed to read nothing", got)
	}
	if got := entityNamesIn(context.Background(), root, workspaceconfig.Config{}, view, nil, "Component", workspaceconfig.ReleaseReach{}); !slices.Contains(got, "cat/cache") {
		t.Fatalf("the live control enumerated %q without the catalog's cat/cache, so the withdrawn arm above proves nothing", got)
	}
}

// completionLabels drives one aggrega/completion request through the dispatch
// loop, optionally with the slash trigger character, and returns the labels in
// the order the response carries them.
func completionLabels(t *testing.T, srv *Server, path string, line, char int, slash bool) []string {
	t.Helper()
	params := completionParams{URI: path, Position: yamlpos.Position{Line: line, Character: char}}
	if slash {
		params.Context = &completionContext{TriggerKind: 2, TriggerCharacter: "/"}
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(2, "aggrega/completion", params))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("completion returned an error: %+v", resp.Error)
	}
	var result completionResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal completion result: %v", err)
	}
	return labelsList(result.Items)
}

// TestCompletionOffersTheCatalogDirectoriesAtAComponentAndAFeatureType drives
// the real request at the two cursors a workspace consuming a catalog meets
// first — a component's type and a Feature attachment's type — plus the
// extends and implements cursors that read the same enumeration, and the slash
// narrowing, asserting the exact list each answers with.
//
// A planted defect makes it fail: with the catalog population dropped the
// component row answers only the workspace's own Components, the Feature row
// only ws/tagging, and the narrowed row nothing at all.
func TestCompletionOffersTheCatalogDirectoriesAtAComponentAndAFeatureType(t *testing.T) {
	t.Parallel()
	root, catalogDir := entityNamesFixture(t)

	agg := "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: shop\n  version: 1.0.0\nspec:\n" +
		"  components:\n    api:\n      type: placeholder\n      features:\n        backup:\n          type: placeholder\n" +
		"    cache:\n      type: cat/\n"
	aggPath := writeCompletionFixture(t, root, "shop.yaml", agg)
	child := "apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: ws/child\n  version: 1.0.0\nspec:\n  extends: placeholder\n"
	childPath := writeCompletionFixture(t, root, "child.yaml", child)
	recipe := "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: probe-recipe\n  version: 1.0.0\nspec:\n  capability: helm\n  implements: placeholder\n"
	recipePath := writeCompletionFixture(t, root, "probe-recipe.yaml", recipe)

	srv := newTestServer(t, root, nil, nil)
	srv.stderr = drainStderr()
	srv.lazyCatalog = catalog.NewLazyLookup(root, "", []string{catalogDir}, nil)
	validateFile(t, srv, aggPath, agg)
	validateFile(t, srv, childPath, child)
	validateFile(t, srv, recipePath, recipe)

	for _, tc := range []struct {
		name      string
		path      string
		line, col int
		slash     bool
		want      []string
	}{
		{"a component's type", aggPath, 8, 15, false,
			[]string{"cat/cache", "shared/db", "ws/child", "ws/server"}},
		{"a Feature attachment's type", aggPath, 11, 19, false,
			[]string{"cat/backup", "ws/tagging"}},
		{"a component's type narrowed after a slash", aggPath, 13, 16, true,
			[]string{"cat/cache"}},
		{"a Component's extends value", childPath, 6, 15, false,
			[]string{"cat/cache", "shared/db", "ws/child", "ws/server"}},
		{"a Recipe's implements value", recipePath, 7, 18, false,
			[]string{"cat/cache", "shared/db", "ws/child", "ws/server"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := completionLabels(t, srv, tc.path, tc.line, tc.col, tc.slash)
			if !slices.Equal(got, tc.want) {
				t.Errorf("completion at %s:%d:%d = %q, want %q", filepath.Base(tc.path), tc.line, tc.col, got, tc.want)
			}
		})
	}
}
