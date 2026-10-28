// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage of the provider a boundary-scope FeatureRecipe's output is
// labelled with, through the command's own pin, pull and build: a FeatureRecipe
// rendering through helm, attached at a boundary whose component renders through
// compose, labels what it emits with helm — the provider that rendered it — while
// the component's own output keeps compose.

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuild_ABoundaryFeatureLabelsItsOutputWithItsOwnProvider builds a compose
// component beside a helm boundary-scope FeatureRecipe emitting one ConfigMap, and
// reads the provider label off both outputs.
func TestBuild_ABoundaryFeatureLabelsItsOutputWithItsOwnProvider(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(catalogDir, "components", "app.yaml"), "apiVersion: aggrega.ai/v1alpha1\n"+
		"kind: Component\nmetadata:\n  name: app\n  version: \"1.0.0\"\n  labels:\n"+
		"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
	recipe := filepath.Join(catalogDir, "recipes", "app", "recipe.yaml")
	writeFixture(t, recipe, "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: app\n"+
		"  version: \"1.0.0\"\nspec:\n  capability: compose\n  implements: app\n  workload: |\n"+
		"    apiVersion: aggrega.ai/v1alpha1\n    kind: Workload\n    containers:\n      main:\n"+
		"        image: \"nginx:1.27\"\n")
	writeFixture(t, filepath.Join(catalogDir, "features", "audit.yaml"), "apiVersion: aggrega.ai/v1alpha1\n"+
		"kind: Feature\nmetadata:\n  name: probe/audit\n  version: \"1.0.0\"\nspec: {}\n")
	featureRecipe := filepath.Join(catalogDir, "feature-recipes", "audit", "featurerecipe.yaml")
	writeFixture(t, featureRecipe, "apiVersion: aggrega.ai/v1alpha1\nkind: FeatureRecipe\nmetadata:\n  name: audit\n"+
		"  version: \"1.0.0\"\nspec:\n  capability: helm\n  implements: probe/audit\n  executionScope: boundary\n"+
		"  artifacts:\n    cm.yaml: |\n      apiVersion: v1\n      kind: ConfigMap\n      metadata:\n"+
		"        name: audit\n      data:\n        read: \"{{ .boundary.boundary_name }}\"\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n"+
		"metadata:\n  name: aggregate\n  version: \"1.0.0\"\nspec:\n  components:\n    app:\n      type: app\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\n"+
		"metadata:\n  name: manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n"+
		"        app: app\n    features:\n      type:\n        probe/audit: audit\n")
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, boundaryOutputRelease("provider-label",
		"    edge:\n      directory: out\n      features:\n        audit:\n          type: probe/audit\n"+
			"      components:\n        app: {}\n"))
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/compose", "provider/helm")

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	for _, p := range []string{recipe, featureRecipe} {
		if err := pinRun(context.Background(), io.Discard, ws, p, false); err != nil {
			t.Fatalf("pin %s: %v", p, err)
		}
	}
	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("prime the lockfile via pull: %v", err)
	}

	tree := builtTree(t, root, releasePath, catalogDir)
	var configMap, composeFile string
	for p, content := range tree {
		switch {
		case strings.HasSuffix(p, "cm.yaml"):
			configMap = content
		case strings.HasSuffix(p, "docker-compose.yaml"):
			composeFile = content
		}
	}
	if configMap == "" || composeFile == "" {
		t.Fatalf("want the FeatureRecipe's ConfigMap and the component's compose file; wrote %v", sortedTreeKeys(tree))
	}
	if !strings.Contains(configMap, "aggrega.ai/provider: helm") {
		t.Errorf("the ConfigMap a helm FeatureRecipe rendered must be labelled with helm:\n%s", configMap)
	}
	if strings.Contains(configMap, "aggrega.ai/provider: compose") {
		t.Errorf("the ConfigMap a helm FeatureRecipe rendered is labelled with the component's provider:\n%s", configMap)
	}
	if !strings.Contains(composeFile, "aggrega.ai/provider: compose") {
		t.Errorf("the component's own output must keep its provider label:\n%s", composeFile)
	}
}
