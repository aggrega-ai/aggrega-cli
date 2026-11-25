// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// neighbourComponents are the two Components the case writes by hand: a storage
// Component publishing an endpoint, and a consumer whose connection to it injects
// that endpoint on the env mechanism.
const (
	neighbourStoreComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/storage/object-store
  version: 1.0.0
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  schema:
    values:
      type: object
      properties:
        bucket: { type: string, required: true }
    provides:
      type: object
      properties:
        endpoint: { type: string }
`
	neighbourConsumerComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/compute/reporting-api
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    store:
      selector: "component.aggrega.ai/type=stateful-server"
      inject: ["env"]
`
	neighbourAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: reporting
  version: 1.0.0
spec:
  components:
    store:
      type: example/storage/object-store:1.0.0
      values:
        bucket: app-artifacts
    api:
      type: example/compute/reporting-api:1.0.0
      uses:
        store:
          store:
            target: store
            inject:
              env:
                - in: STORE_ENDPOINT
                  from: endpoint
`
	neighbourManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        example/storage/object-store:1.0.0: terraform/store-bucket:0.1.0
        example/compute/reporting-api:1.0.0: terraform/reporting-api:0.1.0
`
	neighbourRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: reporting
  version: 1.0.0
  environment: probe
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: main
      components:
        store: {}
        api: {}
`
)

// layDraftedRecipe writes the Recipe of m, drafted by WriteModule, with its module
// beside it, under catalog.
func layDraftedRecipe(t *testing.T, catalog, name, implements string, m Module) {
	t.Helper()
	draft, diags := WriteModule(m)
	if halts := haltsOf(diags); len(halts) != 0 {
		t.Fatalf("drafting %s raised %+v", name, halts)
	}
	draft.Name, draft.Version, draft.Capability, draft.Implements = name, "0.1.0", "terraform", implements
	dir := filepath.Join(catalog, "recipe", "terraform", name, "0.1.0")
	write := func(rel, body string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("recipe.yaml", importer.DraftRecipe(draft).Entity.Body)
	for _, f := range draft.Files {
		write(f.Path, f.Content)
	}
}

// TestDraftedTerraformResourceReceivesNeighbourValue drafts two Recipes through
// WriteModule, a storage resource publishing its endpoint and a consumer whose
// one input is read from the env inject mechanism, writes their Components by
// hand and wires the consumer to the store with a uses edge. The build's root
// configuration then calls the consumer's module with the store's published
// reference, naming the store's own module call, for that argument, and calls
// the store's module with its own argument alone.
func TestDraftedTerraformResourceReceivesNeighbourValue(t *testing.T) {
	embeddedentities.Require(t)
	addr := address(terraformHost, "example", "store", "1.0.0")
	bucket := prop(kString, "bucket")
	bucket.Required = true
	endpoint := prop(kString, "endpoint")
	endpoint.ReadOnly = true
	store := moduleOf("store_bucket", addr, []schemadraft.Input{in(bucket, schemadraft.PillarValues, "bucket", true)},
		schemadraft.Provide{Key: "endpoint", Attribute: []string{"endpoint"}})
	store.Type.Properties = append(store.Type.Properties, endpoint)
	consumer := moduleOf("reporting_api", addr, []schemadraft.Input{
		injected(prop(kString, "store_endpoint"), "env", "STORE_ENDPOINT", true)})

	root := t.TempDir()
	catalog := filepath.Join(root, "catalog")
	layDraftedRecipe(t, catalog, "store-bucket", "example/storage/object-store:1.0.0", store)
	layDraftedRecipe(t, catalog, "reporting-api", "example/compute/reporting-api:1.0.0", consumer)
	provider, ok := embedded.LookupEntity("provider/terraform")
	if !ok {
		t.Fatal("this binary embeds no entity for provider/terraform")
	}
	for path, body := range map[string]string{
		"catalog/plugin/provider-terraform.yaml":                               string(provider),
		"catalog/component/example/storage/object-store/1.0.0/component.yaml":  neighbourStoreComponent,
		"catalog/component/example/compute/reporting-api/1.0.0/component.yaml": neighbourConsumerComponent,
		"aggregate.yaml": neighbourAggregate, "manifest.yaml": neighbourManifest, "release.yaml": neighbourRelease,
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	artifacts := pipelinetest.Build(t, pipelinetest.Input{ReleasePath: filepath.Join(root, "release.yaml"),
		CatalogPaths: []string{catalog}}).Artifacts
	var rootConfig string
	for path, body := range artifacts {
		if strings.HasSuffix(path, "aggrega.modules.tf") {
			rootConfig = string(body)
		}
	}
	if rootConfig == "" {
		t.Fatalf("the build wrote no root configuration; it wrote %d artifacts", len(artifacts))
	}
	block := func(name string) string {
		_, rest, ok := strings.Cut(rootConfig, "module \""+name+"\" {")
		if !ok {
			t.Fatalf("the root configuration holds no module %q:\n%s", name, rootConfig)
		}
		body, _, _ := strings.Cut(rest, "\n}")
		return body
	}
	if api := block("api"); !strings.Contains(api, `store_endpoint = "${module.store.endpoint}"`) {
		t.Errorf("the consumer's module call does not carry the store's published reference:\n%s", api)
	}
	if storeBlock := block("store"); strings.Contains(storeBlock, "module.") || !strings.Contains(storeBlock, `bucket = "app-artifacts"`) {
		t.Errorf("the store's module call is not its own argument alone:\n%s", storeBlock)
	}
}

// TestFromSourceTerraformDraftReceivesNeighbourValue drafts two resources as a
// source writes them through DraftResource with the Terraform format, a bucket
// publishing its endpoint and a function whose one top-level property a
// neighbour supplies, lays the Components and Recipes as drafted, wires the
// function to the bucket with the connection that injects the property from the
// published endpoint, and builds. The build's root configuration calls the
// function's module with the bucket's published output for that argument.
func TestFromSourceTerraformDraftReceivesNeighbourValue(t *testing.T) {
	embeddedentities.Require(t)
	in := aggrega.ImportInput{Format: Format.Name, Name: "main.tf.json", Bytes: []byte("{}")}
	bucket := schemadraft.WrittenResource{
		ID: "assets", Type: "aws_s3_bucket",
		Properties: []schemadraft.WrittenProperty{
			{Path: []string{"bucket"}, Value: json.RawMessage(`"app-assets"`), Identity: true},
		},
		Provides: []schemadraft.Provide{{Key: "endpoint", Attribute: []string{"bucket_domain_name"}}},
	}
	function := schemadraft.WrittenResource{
		ID: "reporter", Type: "aws_lambda_function",
		Properties: []schemadraft.WrittenProperty{
			{Path: []string{"function_name"}, Value: json.RawMessage(`"reporter"`), Identity: true},
			{Path: []string{"source_endpoint"}, Inject: "source_endpoint"},
		},
	}
	root := t.TempDir()
	var drafted []schemadraft.Drafted
	for _, w := range []schemadraft.WrittenResource{bucket, function} {
		d, diags := schemadraft.DraftResource(in, Format, w)
		if halts := haltsOf(diags); len(halts) != 0 {
			t.Fatalf("drafting %s halted: %+v", w.ID, halts)
		}
		drafted = append(drafted, d)
		decisions := []aggrega.ImportDecision{d.Decision}
		for _, f := range importer.CatalogFiles(in.Settings, d.Entities, decisions) {
			writeFile(t, filepath.Join(root, filepath.FromSlash(f.Path)), f.Content)
		}
	}
	store, consumer := drafted[0], drafted[1]

	provider, ok := embedded.LookupEntity("provider/terraform")
	if !ok {
		t.Fatal("this binary embeds no entity for provider/terraform")
	}
	writeFile(t, filepath.Join(root, "catalog/plugin/provider-terraform.yaml"), string(provider))
	writeFile(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: reporting
  version: 1.0.0
spec:
  components:
    assets:
      type: `+store.Name+`:0.1.0
      values:
        bucket: app-assets
    reporter:
      type: `+consumer.Name+`:0.1.0
      values:
        function_name: reporter
      uses:
        assets:
          assets:
            target: assets
            inject:
              property:
                - in: source_endpoint
                  from: endpoint
`)
	writeFile(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        `+store.Name+`:0.1.0: terraform/`+store.Recipe+`:0.1.0
        `+consumer.Name+`:0.1.0: terraform/`+consumer.Recipe+`:0.1.0
`)
	writeFile(t, filepath.Join(root, "release.yaml"), neighbourRelease2)

	artifacts := pipelinetest.Build(t, pipelinetest.Input{ReleasePath: filepath.Join(root, "release.yaml"),
		CatalogPaths: []string{filepath.Join(root, "catalog")}}).Artifacts
	var rootConfig string
	for path, body := range artifacts {
		if strings.HasSuffix(path, "aggrega.modules.tf") {
			rootConfig = string(body)
		}
	}
	if rootConfig == "" {
		t.Fatalf("the build wrote no root configuration; it wrote %d artifacts", len(artifacts))
	}
	block := func(name string) string {
		_, rest, ok := strings.Cut(rootConfig, "module \""+name+"\" {")
		if !ok {
			t.Fatalf("the root configuration holds no module %q:\n%s", name, rootConfig)
		}
		body, _, _ := strings.Cut(rest, "\n}")
		return body
	}
	if got := block("reporter"); !strings.Contains(got, `source_endpoint = "${module.assets.bucket_domain_name}"`) {
		t.Errorf("the function's module call does not carry the bucket's published reference:\n%s", got)
	}
	if got := block("assets"); strings.Contains(got, "module.") {
		t.Errorf("the bucket's module call reads another module:\n%s", got)
	}
}

// neighbourRelease2 places both instances in one boundary.
const neighbourRelease2 = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: reporting
  version: 1.0.0
  environment: probe
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: main
      components:
        assets: {}
        reporter: {}
`

// writeFile writes body at path, creating its directory.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
