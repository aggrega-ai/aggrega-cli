// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composeimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// The Compose importer on the host toolchain: each case hands Import a request
// and the parse the host would run — the compiler's own YAML parser, its refusal
// read as the import socket reads it — and reads what it drafts.

// hostParse parses source as the host's YAML parser does and refuses it as the
// import socket hands an importer a parser's refusal.
func hostParse(_ string, source []byte) (json.RawMessage, error) {
	out := parsercore.ParseYAML(source)
	if err := importersocket.ParseRefusal(out); err != nil {
		return nil, err
	}
	return out.Tree, nil
}

// fixture reads one committed Compose fixture (internal/importops/testdata/compose).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "importops", "testdata", "compose",
		filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return body
}

// catalog is a view of a workspace catalog holding the standard library's
// PostgreSQL, Redis and generic server Components, with the labels the hub gives
// them.
var catalog = &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
	{Name: "core/compute/server", Version: "1.0.0", Labels: map[string]string{
		"component.aggrega.ai/type": "server", "component.aggrega.ai/category": "compute"}},
	{Name: "core/datastore/postgres", Version: "1.0.0", Labels: map[string]string{
		"component.aggrega.ai/type": "stateful-server", "component.aggrega.ai/category": "datastore"}},
	{Name: "core/datastore/redis", Version: "1.0.0", Labels: map[string]string{
		"component.aggrega.ai/type": "server", "component.aggrega.ai/category": "datastore",
		"datastore.aggrega.ai/engine": "kv"}},
}}

// importOne imports body as the one file of a request, against catalog, with
// settings.
func importOne(body []byte, settings string) aggrega.ImportOutput {
	in := aggrega.ImportInput{Format: Format, Bytes: body, Catalog: catalog}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse)
}

// fileContent returns the content of the file out holds at path, failing the
// test where it holds none.
func fileContent(t *testing.T, out aggrega.ImportOutput, path string) string {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("the import wrote no %s; it wrote %+v (diagnostics %+v)", path, paths(out), out.Diagnostics)
	return ""
}

// paths returns the paths of the files out holds, in order.
func paths(out aggrega.ImportOutput) []string {
	var p []string
	for _, f := range out.Files {
		p = append(p, f.Path)
	}
	return p
}

// codes returns the messages of the diagnostics coded code, in order.
func codes(out aggrega.ImportOutput, code string) []string {
	var messages []string
	for _, d := range out.Diagnostics {
		if d.Code == code {
			messages = append(messages, d.Message)
		}
	}
	return messages
}

// componentKeys reads a drafted Aggregate or Release as YAML and returns the keys
// of its spec.components, in the order the document writes them, failing the test
// where it does not read as YAML.
func componentKeys(t *testing.T, doc string) []string {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("the draft does not read as YAML: %v\n%s", err, doc)
	}
	member := func(n *yaml.Node, key string) *yaml.Node {
		for i := 0; n != nil && n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	components := member(member(root.Content[0], "spec"), "components")
	var keys []string
	for i := 0; components != nil && i+1 < len(components.Content); i += 2 {
		keys = append(keys, components.Content[i].Value)
	}
	return keys
}

// The planted values of the authored fixture: a key-shaped value under a variable
// whose name reads as a credential, and one under a variable whose name does not.
const (
	plantedKey    = "sk-planted-key-shaped-needle-8d21f0"
	plantedBanner = "planted-banner-needle-6b47e2"
)

// TestAComposeFileIsDraftedAsAnAggregateAndARelease imports the authored fixture
// with no setting: its services in the order the file writes them; the
// PostgreSQL and Redis services, whose images rows of the image table map,
// drafted with the hint naming core/datastore/postgres and core/datastore/redis;
// the API, whose image no row maps, an instance of core/compute/server running
// its image on its port, connected to the database it depends on; the
// environment value naming the cache it does not depend on recorded as a hint
// and drawing no connection; and the Release carrying what each service states.
//
// 🔴 PLANTED DEFECT: in decide (composeimport.go), drop the !mapped branch: the
// API is drafted as a Component of its own, and the case fails on its type.
func TestAComposeFileIsDraftedAsAnAggregateAndARelease(t *testing.T) {
	t.Parallel()
	out := importOne(fixture(t, "authored/compose.yaml"), "")
	if out.Error != "" || len(codes(out, "parse-failed")) > 0 {
		t.Fatalf("the import failed: %s %+v", out.Error, out.Diagnostics)
	}
	if want := []string{"components/imported-datastore-postgres.yaml", "components/imported-datastore-redis.yaml",
		"aggregate.yaml", "release.yaml"}; !slices.Equal(paths(out), want) {
		t.Errorf("the import wrote %v, want %v", paths(out), want)
	}
	aggregate := fileContent(t, out, "aggregate.yaml")
	if got := componentKeys(t, aggregate); !slices.Equal(got, []string{"db", "cache", "api"}) {
		t.Errorf("the Aggregate composes %v, want the file's order db, cache, api", got)
	}
	for _, want := range []string{
		"    db:\n      type: imported/datastore/postgres\n",
		"    cache:\n      type: imported/datastore/redis\n",
		"    api:\n      type: core/compute/server\n      uses:\n        db:\n          link:\n            target: db\n",
	} {
		if !strings.Contains(aggregate, want) {
			t.Errorf("the Aggregate does not hold\n%s\nin\n%s", want, aggregate)
		}
	}
	if strings.Contains(aggregate, "target: cache") {
		t.Errorf("an environment value naming the cache drew a connection:\n%s", aggregate)
	}
	if !strings.Contains(fileContent(t, out, "components/imported-datastore-postgres.yaml"),
		"# resembles core/datastore/postgres") {
		t.Errorf("the drafted PostgreSQL Component carries no hint naming core/datastore/postgres")
	}
	if !strings.Contains(fileContent(t, out, "components/imported-datastore-redis.yaml"), "# resembles core/datastore/redis") {
		t.Errorf("the drafted Redis Component carries no hint naming core/datastore/redis")
	}

	if len(out.Decisions) != 3 {
		t.Fatalf("the import made %d decisions, want one per service", len(out.Decisions))
	}
	api := out.Decisions[2]
	if api.Chosen == nil || api.Chosen.Target != Server || api.Chosen.Mode != "reference" {
		t.Errorf("the API's decision chose %+v, want a reference to %s", api.Chosen, Server)
	}
	if !strings.Contains(api.Rationale, "hint: the value of its environment variable CACHE_URL names the service cache") {
		t.Errorf("the API's decision records no hint naming the cache: %q", api.Rationale)
	}
	if strings.Contains(api.Rationale, "DATABASE_HOST") {
		t.Errorf("the API's decision records a hint for the database it depends on: %q", api.Rationale)
	}

	release := fileContent(t, out, "release.yaml")
	if got := componentKeys(t, release); !slices.Equal(got, []string{"db", "cache", "api"}) {
		t.Errorf("the Release holds %v, want db, cache, api", got)
	}
	for _, want := range []string{
		"    api:\n      infra:\n        image: \"ghcr.io/example/shop-api:1.4.2\"\n        port: 8080\n",
		"    #   image: \"postgres:16\"\n",
		"    #   environment:\n    #     POSTGRES_USER: \"shop\"\n    #     POSTGRES_DB: \"shop\"\n",
		"    #     UPSTREAM_TOKEN: \"${UPSTREAM_TOKEN}\"\n",
		"    # Not carried, since the draft reads no infrastructure from it: command.\n",
		"  # manifest: left for the operator, since the import drafts no Manifest",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("the Release does not hold\n%s\nin\n%s", want, release)
		}
	}
}

// TestTheEmptyCredentialsSettingDecidesWhatTheReleaseCarries imports the authored
// fixture under each value of the empty_credentials setting: absent, none and
// marked carry each environment value as written into the Release and name it in
// a credential-carried warning by its service and its variable; all writes each
// empty, reports it under credential-emptied, and leaves its value in no draft,
// decision or diagnostic. The variable named like a credential and the one named
// otherwise are treated alike, and a variable reference is never named.
//
// 🔴 PLANTED DEFECT: in located (composeimport.go), give each value the class
// importer.CredentialMarked: the marked row writes the values empty and fails.
func TestTheEmptyCredentialsSettingDecidesWhatTheReleaseCarries(t *testing.T) {
	t.Parallel()
	source := fixture(t, "authored/compose.yaml")
	for _, p := range []string{plantedKey, plantedBanner} {
		if !strings.Contains(string(source), p) {
			t.Fatalf("the needle: %q is not in the fixture, so the search finds nothing", p)
		}
	}
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			settings := ""
			if setting != "absent" {
				settings = `{"empty_credentials":"` + setting + `"}`
			}
			out := importOne(source, settings)
			everything, _ := json.Marshal(struct {
				Entities  []aggrega.EmittedEntity
				Decisions []aggrega.ImportDecision
				Files     []aggrega.ScaffoldFile
			}{out.Entities, out.Decisions, out.Files})
			diagnostics, _ := json.Marshal(out.Diagnostics)
			code := "credential-carried"
			if setting == "all" {
				code = "credential-emptied"
			}
			named := strings.Join(codes(out, code), "\n")
			for _, p := range []string{plantedKey, plantedBanner} {
				if strings.Contains(string(diagnostics), p) {
					t.Errorf("a diagnostic holds the planted %q", p)
				}
				release := fileContent(t, out, "release.yaml")
				if setting == "all" {
					if strings.Contains(string(everything), p) {
						t.Errorf("the planted %q reaches a draft", p)
					}
				} else if !strings.Contains(release, p) {
					t.Errorf("the Release does not carry the planted %q as written", p)
				}
			}
			for _, field := range []string{"environment.PAYMENT_API_KEY of node api", "environment.WELCOME_BANNER of node api"} {
				if !strings.Contains(named, field) {
					t.Errorf("no %s names %q:\n%s", code, field, named)
				}
			}
			if strings.Contains(named, "UPSTREAM_TOKEN") {
				t.Errorf("a variable reference is named as a credential value:\n%s", named)
			}
		})
	}
}

// TestAnOverrideFileIsMergedInComposesOrder imports a project directory: the
// override file is merged over compose.yaml as Compose merges it — the image
// replaced, ports and environment merged by their keys, a service added — the
// docker-compose.yml Compose does not read is not read, and the Aggregate states
// both.
func TestAnOverrideFileIsMergedInComposesOrder(t *testing.T) {
	t.Parallel()
	in := aggrega.ImportInput{Format: Format, Catalog: catalog, Sources: []aggrega.ImportSource{
		{Name: "compose.override.yaml", Bytes: fixture(t, "project/compose.override.yaml")},
		{Name: "compose.yaml", Bytes: fixture(t, "project/compose.yaml")},
		{Name: "docker-compose.yml", Bytes: fixture(t, "project/docker-compose.yml")},
	}}
	out := Import(in, hostParse)
	aggregate := fileContent(t, out, "aggregate.yaml")
	if got := componentKeys(t, aggregate); !slices.Equal(got, []string{"web", "db", "worker"}) {
		t.Errorf("the Aggregate composes %v, want web, db and the override's worker", got)
	}
	for _, want := range []string{
		"#   - compose.yaml\n#   - compose.override.yaml, merged over compose.yaml\n",
		"# Not read, since Compose reads the first Compose file and the first override file of a directory in its " +
			"order of preference: docker-compose.yml.\n",
	} {
		if !strings.Contains(aggregate, want) {
			t.Errorf("the Aggregate does not state\n%s\nin\n%s", want, aggregate)
		}
	}
	release := fileContent(t, out, "release.yaml")
	want := "    #   image: \"nginx:1.27-alpine\"\n    #   ports:\n    #     - \"8080:80\"\n    #     - \"8443:443\"\n" +
		"    #   environment:\n    #     MODE: \"override\"\n    #     LOG_LEVEL: \"info\"\n    #     DEBUG: \"1\"\n"
	if !strings.Contains(release, want) {
		t.Errorf("the merged web service is not\n%s\nin\n%s", want, release)
	}
	if strings.Contains(aggregate+release, "ignored") {
		t.Errorf("a service of the file Compose does not read was drafted")
	}
}

// TestModelsAreDraftedAsModelComponentsAndConnected imports the compose-for-agents
// LangGraph application: its model is drafted as a model Component writing its
// identifier as a value, the agent naming it is connected to it, and a service
// that builds its image is an instance of core/compute/server whose image is
// left for the Release.
func TestModelsAreDraftedAsModelComponentsAndConnected(t *testing.T) {
	t.Parallel()
	out := importOne(fixture(t, "compose-for-agents/langgraph.compose.yaml"), "")
	model := fileContent(t, out, "components/imported-ai-model-qwen3.yaml")
	for _, want := range []string{"  name: imported/ai-model/qwen3\n", "    component.aggrega.ai/category: ai-model\n",
		"        model: { type: string }\n"} {
		if !strings.Contains(model, want) {
			t.Errorf("the drafted model does not hold %q:\n%s", want, model)
		}
	}
	aggregate := fileContent(t, out, "aggregate.yaml")
	for _, want := range []string{
		"    qwen3:\n      type: imported/ai-model/qwen3\n      values:\n        model: \"ai/qwen3:14B-Q6_K\"\n",
		"    agent:\n      type: core/compute/server\n      uses:\n        database:\n          link:\n            target: database\n" +
			"        qwen3:\n          link:\n            target: qwen3\n",
		"    mcp-gateway:\n      type: core/compute/server\n",
	} {
		if !strings.Contains(aggregate, want) {
			t.Errorf("the Aggregate does not hold\n%s\nin\n%s", want, aggregate)
		}
	}
	release := fileContent(t, out, "release.yaml")
	if !strings.Contains(release, "#   - infra.image, since the service builds its image rather than naming one\n") {
		t.Errorf("the Release does not leave a built service's image for the Release:\n%s", release)
	}
	if !strings.Contains(release, "    mcp-gateway:\n      infra:\n        image: \"docker/mcp-gateway:latest\"\n") {
		t.Errorf("the Release does not run the gateway's image:\n%s", release)
	}
}

// TestAnOverrideMapsAnImageNoRowNames imports a service running an image no row
// of the image table names: with no setting it is an instance of
// core/compute/server running the image; with a mapping_overrides entry mapping
// the image's repository to core/datastore/redis under auto-apply it is
// referenced as that Component; and under hints-only it is drafted with the hint
// naming it.
//
// 🔴 PLANTED DEFECT: in the plugin kit's Decide (pkg/importer/import.go), build
// the mapping table without the overrides: both override rows fail.
func TestAnOverrideMapsAnImageNoRowNames(t *testing.T) {
	t.Parallel()
	source := []byte("services:\n  cache:\n    image: bitnami/redis:7.2\n")
	override := `"mapping_overrides":{"compose":{"bitnami/redis":{"component":"core/datastore/redis",` +
		`"category":"datastore","traits":{"datastore.aggrega.ai/engine":"kv"},"vendor":"redis"}}}`
	for _, row := range []struct {
		name, settings, wantType string
	}{
		{"no row", "", Server},
		{"an override under auto-apply", `{"reuse_mode":"auto-apply-above-threshold",` + override + `}`, "core/datastore/redis"},
		{"an override under hints-only", `{` + override + `}`, "imported/datastore/bitnami-redis"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := importOne(source, row.settings)
			aggregate := fileContent(t, out, "aggregate.yaml")
			if !strings.Contains(aggregate, "    cache:\n      type: "+row.wantType+"\n") {
				t.Errorf("the service is not an instance of %s:\n%s", row.wantType, aggregate)
			}
			release := fileContent(t, out, "release.yaml")
			if row.wantType == Server && !strings.Contains(release, "        image: \"bitnami/redis:7.2\"\n") {
				t.Errorf("the Release does not run the image:\n%s", release)
			}
		})
	}
}

// TestTheImageTableIsTheImportersMappingArm holds the image table, read as the
// mapping arm, to its rows: the PostgreSQL and Redis repositories map to the
// Components their Recipes implement, under the category their names place them
// in, and every row is keyed by its repository, in order.
func TestTheImageTableIsTheImportersMappingArm(t *testing.T) {
	t.Parallel()
	arm := mappings()
	for repository, want := range map[string]string{"postgres": "core/datastore/postgres", "redis": "core/datastore/redis"} {
		got, ok := arm[repository]
		if !ok || got.Component != want || got.Category != "datastore" || got.Vendor != want[strings.LastIndex(want, "/")+1:] {
			t.Errorf("the arm maps %s to %+v, want %s in datastore", repository, got, want)
		}
	}
	if len(arm) != len(imageTable) {
		t.Errorf("the arm holds %d rows and the table %d", len(arm), len(imageTable))
	}
	if !slices.IsSortedFunc(imageTable, func(a, b imageRow) int { return strings.Compare(a.Repository, b.Repository) }) {
		t.Errorf("the table's rows are not ordered by repository")
	}
	if TableRevision == "" {
		t.Errorf("the table names no revision it was generated from")
	}
}

// TestRepositoryReadsAReferenceAsDockerDoes reads the repository a reference
// names, its tag and its digest left out and its registry left out where it is
// Docker Hub's, and refuses text that is no reference.
func TestRepositoryReadsAReferenceAsDockerDoes(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		ref, want string
		ok        bool
	}{
		{"postgres", "postgres", true},
		{"postgres:16", "postgres", true},
		{"docker.io/library/postgres:16", "postgres", true},
		{"library/redis", "redis", true},
		{"index.docker.io/grafana/loki:3.6.7", "grafana/loki", true},
		{"ghcr.io/example/shop-api:1.4.2", "ghcr.io/example/shop-api", true},
		{"localhost:5000/app:dev", "localhost:5000/app", true},
		{"cr.agentgateway.dev/agentgateway:v1.6.0@sha256:9d3e", "cr.agentgateway.dev/agentgateway", true},
		{"redis:", "redis", true},
		{"", "", false},
		{"${IMAGE:-postgres:16}", "", false},
		{"Postgres", "", false},
		{"nginx-", "", false},
		{"/postgres", "", false},
	} {
		got, ok := Repository(row.ref)
		if got != row.want || ok != row.ok {
			t.Errorf("Repository(%q) = %q, %v; want %q, %v", row.ref, got, ok, row.want, row.ok)
		}
	}
}

// TestKeyOrderReadsTheOrderAFileWritesItsKeysIn reads the keys of a block mapping
// at a path in the order the text writes them, and leaves a mapping written in
// flow style, or a key the text does not write beneath the path, in sorted order
// after them.
func TestKeyOrderReadsTheOrderAFileWritesItsKeysIn(t *testing.T) {
	t.Parallel()
	text := []byte("# a comment\nname: x\nservices:\n  zeta:\n    image: a\n    environment:\n      B: 1\n      A: 2\n" +
		"  \"alpha\": &anchor\n    image: b\n  mid: {image: c}\nmodels: {m2: {model: x}, m1: {model: y}}\n")
	for _, row := range []struct {
		path        []string
		names, want []string
	}{
		{[]string{"services"}, []string{"alpha", "mid", "zeta"}, []string{"zeta", "alpha", "mid"}},
		{[]string{"services", "zeta", "environment"}, []string{"A", "B"}, []string{"B", "A"}},
		{[]string{"models"}, []string{"m1", "m2"}, []string{"m1", "m2"}},
		{[]string{"services"}, []string{"alpha", "extra", "zeta"}, []string{"zeta", "alpha", "extra"}},
	} {
		if got := keyOrder(text, row.path, row.names); !reflect.DeepEqual(got, row.want) {
			t.Errorf("keyOrder(%v, %v) = %v, want %v", row.path, row.names, got, row.want)
		}
	}
}

// TestASourceTheParserRefusesHaltsAndAFileHoldingNoServiceWarns imports a file
// the YAML parser refuses, which halts under parse-failed with the parser's
// reason, and a file holding no service and no model, which warns no-nodes and
// drafts nothing.
func TestASourceTheParserRefusesHaltsAndAFileHoldingNoServiceWarns(t *testing.T) {
	t.Parallel()
	refused := importOne([]byte("services: [\n"), "")
	if failed := codes(refused, "parse-failed"); len(failed) != 1 || !strings.Contains(failed[0], "yaml parse:") {
		t.Errorf("a source the parser refuses reported parse-failed %q, want one carrying the parser's reason", failed)
	}
	empty := importOne([]byte("volumes:\n  data: {}\n"), "")
	if len(codes(empty, "no-nodes")) != 1 || len(empty.Files) != 0 {
		t.Errorf("a file holding no service reported %+v and drafted %v", empty.Diagnostics, paths(empty))
	}
}

// TestTwoImportsAreByteIdentical imports the authored fixture and the LangGraph
// application twice each and requires the same output, byte for byte.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"authored/compose.yaml", "compose-for-agents/langgraph.compose.yaml"} {
		body := fixture(t, name)
		first, _ := json.Marshal(importOne(body, ""))
		second, _ := json.Marshal(importOne(body, ""))
		if string(first) != string(second) {
			t.Errorf("two imports of %s differ", name)
		}
	}
}
