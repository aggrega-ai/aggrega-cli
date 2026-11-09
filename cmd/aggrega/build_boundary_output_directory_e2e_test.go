// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage of where a Release boundary's output is written, through the
// command's own pin, pull and build on the terraform provider, which gives each
// contributor a directory of its own under modules/: two spellings of one
// boundary directory form one dispatch group and one root configuration; a
// directory climbing out of the output root is refused at build, naming the
// Release, the boundary and the field; and a Feature attached at a boundary under
// the name "." is refused rather than written into the directory holding every
// contributor, while a dotted ordinary name builds.

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// boundaryOutputWorkspace authors, pins and pulls a terraform workspace holding
// two components, app1 and app2, each with an artifacts-only Recipe, and a
// Feature probe/audit realized by a boundary-scope FeatureRecipe, under the
// Release given. It returns the Release path and the catalog directory.
func boundaryOutputWorkspace(t *testing.T, root, release string) (releasePath, catalogDir string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	var pinned []string
	for _, n := range []string{"app1", "app2"} {
		writeFixture(t, filepath.Join(catalogDir, "components", n+".yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
			"kind: Component\nmetadata:\n  name: "+n+"\n  version: \"1.0.0\"\n  labels:\n"+
			"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
		recipe := filepath.Join(catalogDir, "recipes", n, "recipe.yaml")
		writeFixture(t, recipe, "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: "+n+"\n"+
			"  version: \"1.0.0\"\nspec:\n  capability: terraform\n  implements: "+n+"\n  artifacts:\n"+
			"    "+n+".tf.json: |\n      {\"output\":{\""+n+"\":{\"value\":\""+n+"\"}}}\n")
		pinned = append(pinned, recipe)
	}
	writeFixture(t, filepath.Join(catalogDir, "features", "audit.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
		"kind: Feature\nmetadata:\n  name: probe/audit\n  version: \"1.0.0\"\nspec: {}\n")
	featureRecipe := filepath.Join(catalogDir, "feature-recipes", "audit", "featurerecipe.yaml")
	writeFixture(t, featureRecipe, "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: audit\n"+
		"  version: \"1.0.0\"\nspec:\n  capability: terraform\n  implements: probe/audit\n  executionScope: boundary\n"+
		"  artifacts:\n    audit.tf.json: |\n      {\"output\":{\"audit\":{\"value\":\"audit\"}}}\n")
	pinned = append(pinned, featureRecipe)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n"+
		"metadata:\n  name: aggregate\n  version: \"1.0.0\"\nspec:\n  components:\n"+
		"    app1:\n      type: app1\n    app2:\n      type: app2\n")
	writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\n"+
		"metadata:\n  name: manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n"+
		"        app1: app1\n        app2: app2\n    features:\n      type:\n        probe/audit: audit\n")
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, release)
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/terraform")

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	for _, p := range pinned {
		if err := pinRun(context.Background(), io.Discard, ws, p, false); err != nil {
			t.Fatalf("pin %s: %v", p, err)
		}
	}
	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("prime the lockfile via pull: %v", err)
	}
	return releasePath, catalogDir
}

// boundaryOutputRelease is a Release named name whose boundaries are given as
// their YAML, indented under spec.boundaries.
func boundaryOutputRelease(name, boundaries string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: " + name + "\n" +
		"  environment: testing\n  version: \"1.0.0\"\nspec:\n  aggregate: ./aggregate.yaml\n" +
		"  manifest: ./manifest.yaml\n  boundaries:\n" + boundaries
}

// TestBuild_TwoSpellingsOfOneBoundaryDirectoryAreOneDispatchGroup builds two
// boundaries whose directories are `out/a` and `out//a`, each with its
// sub-directory disabled, so the directory alone is the dispatch path. They are
// one directory, so they form one dispatch group: one root configuration naming
// both components' modules. Two groups writing one directory would each write a
// root configuration naming one component, and one would overwrite the other.
func TestBuild_TwoSpellingsOfOneBoundaryDirectoryAreOneDispatchGroup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir := boundaryOutputWorkspace(t, root, boundaryOutputRelease("two-spellings",
		"    left:\n      directory: out/a\n      subDirectory: \".\"\n      components:\n        app1: {}\n"+
			"    right:\n      directory: out//a\n      subDirectory: \".\"\n      components:\n        app2: {}\n"))

	tree := builtTree(t, root, releasePath, catalogDir)
	var roots []string
	for p := range tree {
		if strings.HasSuffix(p, "aggrega.modules.tf") {
			roots = append(roots, p)
		}
	}
	if len(roots) != 1 {
		t.Fatalf("want one root configuration for the one directory, got %v in %v", roots, sortedTreeKeys(tree))
	}
	for _, module := range []string{`module "app1"`, `module "app2"`} {
		if !strings.Contains(tree[roots[0]], module) {
			t.Errorf("the root configuration %s does not name %s, so the two boundaries did not form one group:\n%s",
				roots[0], module, tree[roots[0]])
		}
	}
	if !strings.HasSuffix(roots[0], "out/a/aggrega.modules.tf") || strings.Contains(roots[0], "//") {
		t.Errorf("the root configuration is at %s; want it under the cleaned directory out/a", roots[0])
	}
}

// TestBuild_ABoundaryDirectoryClimbingOutOfTheOutputRootIsRefused builds a
// boundary whose directory is `../escape`. The build halts with the boundary
// directory condition before anything is written, naming the Release, the
// boundary, the field and the value it evaluated to.
func TestBuild_ABoundaryDirectoryClimbingOutOfTheOutputRootIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir := boundaryOutputWorkspace(t, root, boundaryOutputRelease("escaping",
		"    infra:\n      directory: ../escape\n      components:\n        app1: {}\n        app2: {}\n"))

	err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
	if !errors.Is(err, renderer.ErrBoundaryDirectoryNotBeneathOutputRoot()) {
		t.Fatalf("got %v, want the boundary directory condition", err)
	}
	chain := cerrors.CauseChain(err)
	t.Logf("the refusal as a build reports it:\n%s", chain)
	for _, want := range []string{`Release "escaping"`, `boundary "infra"`, `directory: "../escape"`} {
		if !strings.Contains(chain, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, chain)
		}
	}
}

// TestBuild_ABoundaryFeatureNamedDotIsRefusedOnANestingProvider attaches the
// Feature at a boundary under the name ".", on a provider that gives each
// contributor a directory of its own. The build halts with the boundary Feature
// name condition, naming the Release, the boundary and the instance; the same
// Feature under the dotted name `audit.v2` builds and lands in a directory of
// that name.
func TestBuild_ABoundaryFeatureNamedDotIsRefusedOnANestingProvider(t *testing.T) {
	t.Parallel()
	boundary := func(instance string) string {
		return "    infra:\n      directory: infra\n      features:\n        \"" + instance + "\":\n" +
			"          type: probe/audit\n      components:\n        app1: {}\n        app2: {}\n"
	}

	t.Run("a Feature named dot is refused", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		releasePath, catalogDir := boundaryOutputWorkspace(t, root, boundaryOutputRelease("dot-feature", boundary(".")))
		err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
		if !errors.Is(err, renderer.ErrBoundaryFeatureNameNotADirectory()) {
			t.Fatalf("got %v, want the boundary Feature name condition", err)
		}
		chain := cerrors.CauseChain(err)
		t.Logf("the refusal as a build reports it:\n%s", chain)
		for _, want := range []string{`Release "dot-feature"`, `boundary "infra"`, `feature "."`} {
			if !strings.Contains(chain, want) {
				t.Errorf("the refusal does not carry %q:\n%s", want, chain)
			}
		}
	})

	t.Run("a dotted ordinary name builds", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		releasePath, catalogDir := boundaryOutputWorkspace(t, root, boundaryOutputRelease("named-feature", boundary("audit.v2")))
		tree := builtTree(t, root, releasePath, catalogDir)
		found := false
		for p := range tree {
			if strings.HasSuffix(p, "modules/audit.v2/audit.tf.json") {
				found = true
			}
		}
		if !found {
			t.Errorf("no file was written under modules/audit.v2/; wrote %v", sortedTreeKeys(tree))
		}
	})
}
