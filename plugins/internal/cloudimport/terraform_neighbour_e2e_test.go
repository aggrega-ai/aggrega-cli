// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/terraform"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// terraformSourceFormat is a format reading the one graph it is given and
// drafting every resource from the source in the Terraform schema format, so
// each resource is a Component and a Recipe of the terraform target.
func terraformSourceFormat(g *Graph) Format {
	return Format{
		Name: "toy", Target: "terraform", Syntax: "json",
		Read:  func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic) { return g, nil },
		Draft: terraform.Format,
	}
}

// TestFromSourceTerraformDraftReceivesNeighbourValue drafts two resources from a
// source in the Terraform schema format, a bucket and a function whose one
// top-level property reads the bucket's attribute, and takes the Aggregate, the
// connection that injects the property, the Release, the Manifest, and both
// Components and Recipes from the importer's own writers. It then builds them
// in-process with the Recipes' modules vendored as the catalog holds them, the
// build `aggrega pin` and `aggrega build` run: the root configuration's module
// call of the function carries the bucket's output for that argument, and the
// bucket's own module call reads no other module.
func TestFromSourceTerraformDraftReceivesNeighbourValue(t *testing.T) {
	embeddedentities.Require(t)
	g := &Graph{Format: "toy", Read: []string{"main.tf.json"}, Nodes: []*Node{
		{ID: "assets", Type: "aws_s3_bucket", Form: FormRaw, Name: text("app-assets"), Source: "main.tf.json#/assets",
			Body: []Prop{leaf(text("app-assets"), "bucket")}},
		{ID: "reporter", Type: "aws_lambda_function", Form: FormRaw, Name: text("reporter"), Source: "main.tf.json#/reporter",
			Body: []Prop{leaf(text("reporter"), "function_name"),
				leaf(Ref{Node: "assets", Attr: []string{"bucket_domain_name"}}, "source_endpoint")}},
	}}
	in := aggrega.ImportInput{Format: "toy", Target: "terraform", Name: "main.tf.json",
		Catalog: &aggrega.CatalogProjection{}}
	out := Import(in, terraformSourceFormat(g), nil)
	noHalt(t, out.Diagnostics)
	if out.Error != "" {
		t.Fatalf("the import errs: %s", out.Error)
	}
	if aggregate := fileOf(t, out, "aggregate.yaml"); !strings.Contains(aggregate, "- in: \"source_endpoint\"\n") {
		t.Fatalf("the Aggregate writes no connection injecting source_endpoint:\n%s", aggregate)
	}

	// The catalog view holds no Recipe of the target, so the importer drafts no
	// Manifest: it is written here, mapping each drafted Component to the Recipe
	// drafted for it, and the Release names it.
	var components, recipes []string
	for _, e := range out.Entities {
		switch {
		case e.Kind == "Component" && e.Mode == "draft":
			components = append(components, e.Name)
		case e.Kind == "Recipe" && e.Mode == "draft":
			recipes = append(recipes, e.Name)
		}
	}
	if len(components) != 2 || len(recipes) != 2 {
		t.Fatalf("the import drafted the Components %q and the Recipes %q, want two of each", components, recipes)
	}
	manifest := "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: neighbour\n  version: 1.0.0\nspec:\n  mappings:\n    components:\n      type:\n"
	for i := range components {
		manifest += "        " + components[i] + ": terraform/" + recipes[i] + "\n"
	}
	release := fileOf(t, out, "release.yaml")
	if !strings.Contains(release, "manifest: \"\"\n") {
		t.Fatalf("the Release does not leave its Manifest empty:\n%s", release)
	}
	files := append([]aggrega.ScaffoldFile(nil), out.Files...)
	files = append(files, aggrega.ScaffoldFile{Path: "manifest.yaml", Content: manifest})
	for i := range files {
		if files[i].Path == "release.yaml" {
			files[i].Content = strings.Replace(release, "manifest: \"\"\n", "manifest: ./manifest.yaml\n", 1)
		}
	}

	root := t.TempDir()
	for _, f := range files {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	provider, ok := embedded.LookupEntity("provider/terraform")
	if !ok {
		t.Fatal("this binary embeds no entity for provider/terraform")
	}
	if err := os.MkdirAll(filepath.Join(root, "catalog", "plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog", "plugin", "provider-terraform.yaml"), provider, 0o644); err != nil {
		t.Fatal(err)
	}

	artifacts := pipelinetest.Build(t, pipelinetest.Input{ReleasePath: filepath.Join(root, "release.yaml"),
		CatalogPaths: []string{root}}).Artifacts
	var configs []string
	for path, body := range artifacts {
		if strings.HasSuffix(path, "aggrega.modules.tf") {
			configs = append(configs, string(body))
		}
	}
	if len(configs) != 1 {
		t.Fatalf("the build wrote %d root configurations among %d artifacts, want one", len(configs), len(artifacts))
	}
	block := func(name string) string {
		_, rest, ok := strings.Cut(configs[0], "module \""+name+"\" {")
		if !ok {
			t.Fatalf("the root configuration holds no module %q:\n%s", name, configs[0])
		}
		body, _, _ := strings.Cut(rest, "\n}")
		return body
	}
	if got := block("reporter"); !strings.Contains(got, `source_endpoint = "${module.assets.bucket_domain_name}"`) {
		t.Errorf("the function's module call does not carry the bucket's output:\n%s", got)
	}
	if got := block("assets"); strings.Contains(got, "module.") {
		t.Errorf("the bucket's module call reads another module:\n%s", got)
	}
}
