// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage of a Recipe's boundary block through the command's own build:
// the files a Recipe contributes to the shared output of the boundary it renders in
// are written once, beside the root configuration the terraform provider assembles,
// merged across contributors by name — and a provider that does not declare it
// accepts such files refuses the Recipe before anything renders. Every proof here
// builds through pin, pull and the build dispatch the command runs, and reads what
// the build wrote or the refusal it returned.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// boundaryRecipe is one component of a boundary-artifacts workspace: the component
// type it declares and the Recipe spec its type maps to, written after the
// capability and the implements line.
type boundaryRecipe struct {
	component string
	spec      string
}

// boundaryArtifactsWorkspace authors, pins and pulls a workspace whose one boundary
// holds the components given, each of its own type mapped to a Recipe of its own
// rendered through capability. It returns the Release path and the catalog
// directory a build is handed.
func boundaryArtifactsWorkspace(t *testing.T, root, capability string, components []boundaryRecipe) (releasePath, catalogDir string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")

	var aggregate, release, mappings strings.Builder
	aggregate.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: aggregate\n" +
		"  version: \"1.0.0\"\nspec:\n  components:\n")
	release.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: boundary-files\n" +
		"  environment: testing\n  version: \"1.0.0\"\nspec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n" +
		"  boundaries:\n    main:\n      directory: ./dist/main\n      components:\n")
	mappings.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: manifest\n" +
		"  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n")
	var recipePaths []string
	for _, c := range components {
		aggregate.WriteString("    " + c.component + ":\n      type: " + c.component + "\n")
		release.WriteString("        " + c.component + ": {}\n")
		mappings.WriteString("        " + c.component + ": " + c.component + "\n")
		writeFixture(t, filepath.Join(catalogDir, "components", c.component+".yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
			"kind: Component\nmetadata:\n  name: "+c.component+"\n  version: \"1.0.0\"\n  labels:\n"+
			"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
		// Each Recipe in a directory of its own: the pin step refuses an entity that
		// shares its directory with another, because the lock it writes is named
		// for the directory rather than for the entity.
		recipePath := filepath.Join(catalogDir, "recipes", c.component, "recipe.yaml")
		writeFixture(t, recipePath, "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: "+c.component+"\n"+
			"  version: \"1.0.0\"\nspec:\n  capability: "+capability+"\n  implements: "+c.component+"\n"+c.spec)
		recipePaths = append(recipePaths, recipePath)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), aggregate.String())
	writeFixture(t, filepath.Join(root, "release.yaml"), release.String())
	writeFixture(t, filepath.Join(root, "manifest.yaml"), mappings.String())
	seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/"+capability)

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	for _, recipePath := range recipePaths {
		if err := pinRun(context.Background(), io.Discard, ws, recipePath, false); err != nil {
			t.Fatalf("pin %s: %v", recipePath, err)
		}
	}
	releasePath = filepath.Join(root, "release.yaml")
	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("prime the lockfile via pull: %v", err)
	}
	return releasePath, catalogDir
}

// azurermProviderConfiguration is a provider configuration Terraform accepts only in
// the root configuration of a boundary.
const azurermProviderConfiguration = "provider \"azurerm\" {\n  features {}\n}\n"

// terraformBoundarySpec is a terraform Recipe spec writing one module file of its
// own, named for the component, and declaring the boundary files given.
func terraformBoundarySpec(component, shared string) string {
	return "  artifacts:\n    " + component + ".tf.json: |\n      {\"output\":{\"" + component +
		"\":{\"value\":\"" + component + "\"}}}\n" + shared
}

// providersBlock declares the azurerm provider configuration for the boundary.
const providersBlock = "  boundary:\n    artifacts:\n      providers.tf: |\n" +
	"        provider \"azurerm\" {\n          features {}\n        }\n"

// TestBuild_BoundaryArtifactsAreWrittenOnceBesideTheTerraformRoot builds two
// components whose Recipes both declare the same provider configuration for their
// boundary. The command writes it once, beside the root configuration and in no
// module, with the bytes declared; each component's own file stays in its module;
// and a second build writes a byte-identical tree.
func TestBuild_BoundaryArtifactsAreWrittenOnceBesideTheTerraformRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir := boundaryArtifactsWorkspace(t, root, "terraform", []boundaryRecipe{
		{component: "gateway", spec: terraformBoundarySpec("gateway", providersBlock)},
		{component: "vault", spec: terraformBoundarySpec("vault", providersBlock)},
	})

	tree := builtTree(t, root, releasePath, catalogDir)
	var shared, roots []string
	for p := range tree {
		switch {
		case strings.HasSuffix(p, "/providers.tf"):
			shared = append(shared, p)
		case strings.HasSuffix(p, "/aggrega.modules.tf"):
			roots = append(roots, p)
		}
	}
	if len(roots) != 1 {
		t.Fatalf("want one root configuration, got %v in %v", roots, sortedTreeKeys(tree))
	}
	rootDir := strings.TrimSuffix(roots[0], "aggrega.modules.tf")
	if len(shared) != 1 || shared[0] != rootDir+"providers.tf" {
		t.Fatalf("want the provider configuration once, beside %s; got %v", roots[0], shared)
	}
	if got := tree[shared[0]]; got != azurermProviderConfiguration {
		t.Errorf("the shared file carries %q; want the declared bytes %q", got, azurermProviderConfiguration)
	}
	for _, own := range []string{"modules/gateway/gateway.tf.json", "modules/vault/vault.tf.json"} {
		if _, ok := tree[rootDir+own]; !ok {
			t.Errorf("the component's own file %s is not in its module; wrote %v", own, sortedTreeKeys(tree))
		}
	}

	again := builtTree(t, root, releasePath, catalogDir)
	if len(again) != len(tree) {
		t.Fatalf("a second build wrote %d files; the first wrote %d", len(again), len(tree))
	}
	for p, content := range tree {
		if again[p] != content {
			t.Errorf("%s differs between two builds of the same workspace", p)
		}
	}
}

// TestBuild_BoundaryArtifactsDifferingUnderOneNameAreRefused builds two components
// whose Recipes declare one boundary file name with different bytes, and the build
// halts under the assembled-output collision naming the file and both components.
func TestBuild_BoundaryArtifactsDifferingUnderOneNameAreRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	other := "  boundary:\n    artifacts:\n      providers.tf: |\n" +
		"        provider \"azurerm\" {\n          features {}\n          storage_use_azuread = true\n        }\n"
	releasePath, catalogDir := boundaryArtifactsWorkspace(t, root, "terraform", []boundaryRecipe{
		{component: "gateway", spec: terraformBoundarySpec("gateway", providersBlock)},
		{component: "vault", spec: terraformBoundarySpec("vault", other)},
	})
	err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
	if !errors.Is(err, renderer.ErrAssembledOutputCollision()) {
		t.Fatalf("got %v, want the assembled-output collision", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{"providers.tf", "component gateway", "component vault"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, chain)
		}
	}
}

// TestBuild_BoundaryArtifactsOnAProviderThatDoesNotAcceptThemAreRefused builds a
// compose Recipe declaring a boundary block. The compose provider declares no such
// claim, so the build is refused before anything renders, naming the provider and
// the node, positioned at the block in the Recipe.
func TestBuild_BoundaryArtifactsOnAProviderThatDoesNotAcceptThemAreRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	releasePath, catalogDir := boundaryArtifactsWorkspace(t, root, "compose", []boundaryRecipe{
		{component: "app", spec: "  artifacts:\n    services.yaml: |\n      services:\n        app:\n          image: nginx:1.27\n" +
			"  boundary:\n    artifacts:\n      shared.env: |\n        LOG_LEVEL=info\n"},
	})
	err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
	if !errors.Is(err, renderer.ErrUnsupportedBoundaryArtifacts()) {
		t.Fatalf("got %v, want the refusal of a provider that does not support boundary artifacts", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		renderer.ErrUnsupportedBoundaryArtifacts().Error() + ` ("compose", node "app")`,
		"remove the boundary block",
		"at .spec.boundary",
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, chain)
		}
	}
}

// TestValidate_TheBoundaryBlockNegativeCorpus drives one document per constraint
// the block's schema declares through the single-entity scope's load, which reports
// the document-envelope condition with the findings that name the constraint. Each
// row carries one fault, and the control row is the same document with none.
//
// The findings are matched on the path and the offending token rather than on the
// whole sentence, so a rewording of the envelope's own phrasing does not hide which
// constraint fired.
func TestValidate_TheBoundaryBlockNegativeCorpus(t *testing.T) {
	t.Parallel()

	recipe := func(spec string) string {
		return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: corpus\n  version: \"1.0.0\"\nspec:\n" + spec
	}
	const artifactsOnly = "  capability: terraform\n  implements: server\n  artifacts:\n    main.tf: \"# main\"\n"

	for _, tc := range []struct {
		name, document string
		findings       []string
	}{
		{"a key other than artifacts under the block", recipe(artifactsOnly + "  boundary:\n    artifacts:\n      a.tf: \"# a\"\n    files: {}\n"),
			[]string{"spec.boundary", "files"}},
		{"a block declaring no artifacts", recipe(artifactsOnly + "  boundary: {}\n"),
			[]string{"spec.boundary", "artifacts"}},
		{"an empty artifacts map", recipe(artifactsOnly + "  boundary:\n    artifacts: {}\n"),
			[]string{"spec.boundary.artifacts"}},
		{"a key naming a path rather than a file", recipe(artifactsOnly + "  boundary:\n    artifacts:\n      ../providers.tf: \"# x\"\n"),
			[]string{"spec.boundary.artifacts", "../providers.tf"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "entity.yaml")
			writeFixture(t, path, tc.document)
			var out bytes.Buffer
			err := dispatchValidate(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, "", nil, false, &out)
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Fatalf("got %v, want the document-envelope condition\noutput:\n%s", err, out.String())
			}
			chain := cerrors.CauseChain(err)
			for _, finding := range tc.findings {
				if !strings.Contains(chain, finding) {
					t.Errorf("the refusal does not name the constraint (%q):\n%s", finding, chain)
				}
			}
		})
	}

	t.Run("control: a Recipe declaring the block beside its artifacts", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "entity.yaml")
		writeFixture(t, path, recipe(artifactsOnly+providersBlock))
		var out bytes.Buffer
		err := dispatchValidate(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, "", nil, false, &out)
		if errors.Is(err, envelope.ErrEntityEnvelope()) {
			t.Fatalf("the control is refused at the document envelope: %s", cerrors.CauseChain(err))
		}
		if !strings.Contains(out.String(), "Schema: valid") {
			t.Errorf("the control's schema tier did not pass:\n%s", out.String())
		}
	})
}

// sortedTreeKeys is a written tree's paths in sorted order, for a failure message.
func sortedTreeKeys(tree map[string]string) []string {
	out := make([]string, 0, len(tree))
	for p := range tree {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}
