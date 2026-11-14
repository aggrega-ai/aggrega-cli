// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composeimport

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
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
		"component.aggrega.ai/type": "server", "component.aggrega.ai/category": "compute",
		"component.aggrega.ai/upstream-licensing": "not-applicable"}},
	{Name: "core/datastore/postgres", Version: "1.0.0", Labels: map[string]string{
		"component.aggrega.ai/type": "stateful-server", "component.aggrega.ai/category": "datastore",
		"component.aggrega.ai/hosting": "mixed", "component.aggrega.ai/upstream-licensing": "osi-approved"}},
	{Name: "core/datastore/redis", Version: "1.0.0", Labels: map[string]string{
		"component.aggrega.ai/type": "server", "component.aggrega.ai/category": "datastore",
		"datastore.aggrega.ai/engine": "kv", "component.aggrega.ai/upstream-licensing": "mixed"}},
}}

// catalogHoldingTheRuntime is catalog with the standard library's Docker Model
// Runner runtime beside its Components, with the labels the hub gives it.
var catalogHoldingTheRuntime = &aggrega.CatalogProjection{Components: append(slices.Clone(catalog.Components),
	aggrega.CatalogComponent{Name: ModelRuntime, Version: "1.0.0", Labels: map[string]string{
		"component.aggrega.ai/type": "server", "component.aggrega.ai/category": "ai-model",
		"model.aggrega.ai/capability": "generative", "model.aggrega.ai/api": "openai",
		"component.aggrega.ai/hosting": "self-hosted", "component.aggrega.ai/upstream-licensing": "osi-approved"}})}

// importOne imports body as the one file of a request, against catalog, with
// settings.
func importOne(body []byte, settings string) aggrega.ImportOutput {
	return importOver(body, settings, catalog)
}

// importOver imports body as the one file of a request, against the catalog view
// given — none where it is nil — with settings.
func importOver(body []byte, settings string, view *aggrega.CatalogProjection) aggrega.ImportOutput {
	in := aggrega.ImportInput{Format: Format, Bytes: body, Catalog: view}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse)
}

// modelDecisions returns the decisions out records for models, and
// serviceDecisions those it records for services, each in order.
func modelDecisions(out aggrega.ImportOutput) []aggrega.ImportDecision {
	return slices.DeleteFunc(slices.Clone(out.Decisions), func(d aggrega.ImportDecision) bool {
		return d.SourceNode.Type != "model"
	})
}

func serviceDecisions(out aggrega.ImportOutput) []aggrega.ImportDecision {
	return slices.DeleteFunc(slices.Clone(out.Decisions), func(d aggrega.ImportDecision) bool {
		return d.SourceNode.Type == "model"
	})
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
// referenced as core/datastore/postgres and core/datastore/redis, matched at the
// HIGH tier since the rows carry those Components' labels as traits, and the
// default reuse mode references a HIGH match; the API, whose image no row maps,
// an instance of core/compute/server running its image on its port, connected
// to the database it depends on; the environment value naming the cache it does
// not depend on recorded as a hint and drawing no connection; and the Release
// carrying what each service states, its environment the drafted default. Under
// the hints-only reuse mode the two datastores are drafted instead, each with
// the hint naming the Component it resembles.
//
// 🔴 PLANTED DEFECT: in decide (composeimport.go), drop the !mapped branch: the
// API is drafted as a Component of its own, and the case fails on its type.
func TestAComposeFileIsDraftedAsAnAggregateAndARelease(t *testing.T) {
	t.Parallel()
	out := importOne(fixture(t, "authored/compose.yaml"), "")
	if out.Error != "" || len(codes(out, "parse-failed")) > 0 {
		t.Fatalf("the import failed: %s %+v", out.Error, out.Diagnostics)
	}
	if want := []string{"aggregate.yaml", "release.yaml"}; !slices.Equal(paths(out), want) {
		t.Errorf("the import wrote %v, want %v", paths(out), want)
	}
	aggregate := fileContent(t, out, "aggregate.yaml")
	if got := componentKeys(t, aggregate); !slices.Equal(got, []string{"db", "cache", "api"}) {
		t.Errorf("the Aggregate composes %v, want the file's order db, cache, api", got)
	}
	for _, want := range []string{
		"    db:\n      type: core/datastore/postgres\n",
		"    cache:\n      type: core/datastore/redis\n",
		"    api:\n      type: core/compute/server\n      uses:\n        db:\n          db:\n            target: db\n",
	} {
		if !strings.Contains(aggregate, want) {
			t.Errorf("the Aggregate does not hold\n%s\nin\n%s", want, aggregate)
		}
	}
	if strings.Contains(aggregate, "target: cache") {
		t.Errorf("an environment value naming the cache drew a connection:\n%s", aggregate)
	}

	if len(out.Decisions) != 3 {
		t.Fatalf("the import made %d decisions, want one per service", len(out.Decisions))
	}
	for i, want := range []string{"core/datastore/postgres", "core/datastore/redis"} {
		d := out.Decisions[i]
		if len(d.Candidates) == 0 || d.Candidates[0].ComponentName != want || d.Candidates[0].Tier != "HIGH" ||
			d.Chosen == nil || d.Chosen.Mode != "reference" || d.Chosen.Target != want {
			t.Errorf("the decision for %s chose %+v over %+v, want %s referenced at the HIGH tier", d.SourceNode.ID,
				d.Chosen, d.Candidates, want)
		}
	}

	hinted := importOne(fixture(t, "authored/compose.yaml"), `{"reuse_mode":"hints-only"}`)
	if want := []string{"components/imported-datastore-postgres.yaml", "components/imported-datastore-redis.yaml",
		"aggregate.yaml", "release.yaml"}; !slices.Equal(paths(hinted), want) {
		t.Errorf("under hints-only the import wrote %v, want %v", paths(hinted), want)
	}
	hintedAggregate := fileContent(t, hinted, "aggregate.yaml")
	for _, want := range []string{"    db:\n      type: imported/datastore/postgres\n",
		"    cache:\n      type: imported/datastore/redis\n"} {
		if !strings.Contains(hintedAggregate, want) {
			t.Errorf("under hints-only the Aggregate does not hold\n%s\nin\n%s", want, hintedAggregate)
		}
	}
	if !strings.Contains(fileContent(t, hinted, "components/imported-datastore-postgres.yaml"),
		"# resembles core/datastore/postgres") {
		t.Errorf("the drafted PostgreSQL Component carries no hint naming core/datastore/postgres")
	}
	if !strings.Contains(fileContent(t, hinted, "components/imported-datastore-redis.yaml"),
		"# resembles core/datastore/redis") {
		t.Errorf("the drafted Redis Component carries no hint naming core/datastore/redis")
	}
	for i, want := range []string{"core/datastore/postgres", "core/datastore/redis"} {
		if d := hinted.Decisions[i]; !strings.Contains(d.SwapHint, "reference it by setting type: "+want) {
			t.Errorf("under hints-only the decision for %s hints %q, want one naming %s", d.SourceNode.ID, d.SwapHint,
				want)
		}
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
		"  # manifest: left empty, since the import was given no target; an import given a target drafts the " +
			"Manifest and names it here.\n  manifest: \"\"\n",
		"  # environment is a drafted default, since no source names the environment this Release deploys to; " +
			"change it to name the one it deploys to.\n  environment: development\n",
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
		"    agent:\n      type: core/compute/server\n      uses:\n        database:\n          database:\n            target: database\n" +
			"        # The service agent reads the address of the model qwen3 as MODEL_RUNNER_URL and its identifier as " +
			"MODEL_RUNNER_MODEL, the names its models entry gives them; no key of the draft holds either name.\n" +
			"        qwen3:\n          qwen3:\n            target: qwen3\n",
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

// TestEachConnectionOfAServiceIsWrittenUnderAnAliasOfItsOwn imports a Compose
// file whose API depends on two services and uses a model: each of its three
// connections is written under the socket and the alias of its target's key, in
// the order of those keys, so no two of them share an alias — a component reads
// each connection by its alias, whatever socket it is wired into, and a build
// halts on two connections of one component under one alias. Two imports of the
// file write the same bytes.
//
// 🔴 PLANTED DEFECT: in renderAggregate (render.go), write every connection under
// the alias link again: the three connections share it, and the case fails on
// the bytes and on the aliases.
func TestEachConnectionOfAServiceIsWrittenUnderAnAliasOfItsOwn(t *testing.T) {
	t.Parallel()
	source := []byte("services:\n" +
		"  api:\n    image: ghcr.io/example/notes-api:2.3.0\n    depends_on: [db, cache]\n    models: [llm]\n" +
		"  db:\n    image: postgres:16\n" +
		"  cache:\n    image: redis:7\n" +
		"models:\n  llm:\n    model: ai/smollm2\n")
	out := importOne(source, "")
	if out.Error != "" || len(codes(out, "parse-failed")) > 0 {
		t.Fatalf("the import failed: %s %+v", out.Error, out.Diagnostics)
	}
	aggregate := fileContent(t, out, "aggregate.yaml")
	want := "    api:\n      type: core/compute/server\n      uses:\n" +
		"        cache:\n          cache:\n            target: cache\n" +
		"        db:\n          db:\n            target: db\n" +
		"        llm:\n          llm:\n            target: llm\n"
	if !strings.Contains(aggregate, want) {
		t.Errorf("the API's connections are not\n%s\nin\n%s", want, aggregate)
	}

	var doc struct {
		Spec struct {
			Components map[string]struct {
				Uses map[string]map[string]struct{ Target string } `yaml:"uses"`
			} `yaml:"components"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(aggregate), &doc); err != nil {
		t.Fatalf("the drafted Aggregate does not read as YAML: %v\n%s", err, aggregate)
	}
	connections := 0
	for key, c := range doc.Spec.Components {
		aliases := map[string]string{}
		for socket, entries := range c.Uses {
			for alias, entry := range entries {
				connections++
				if held, ok := aliases[alias]; ok {
					t.Errorf("%s writes the alias %s in the %s socket and in the %s socket", key, alias, held, socket)
				}
				aliases[alias] = socket
				if alias != entry.Target || socket != entry.Target {
					t.Errorf("%s writes the connection to %s under the socket %s and the alias %s, want both named %s",
						key, entry.Target, socket, alias, entry.Target)
				}
			}
		}
	}
	if connections != 3 {
		t.Errorf("the Aggregate draws %d connections, want the API's three", connections)
	}
	if again := fileContent(t, importOne(source, ""), "aggregate.yaml"); again != aggregate {
		t.Errorf("two imports of one file drafted different Aggregates:\n%s\nand\n%s", aggregate, again)
	}
}

// The variables the models fixture's long-form entry names its model's address
// and identifier under.
const (
	longFormEndpointVar = "AI_MODEL_URL"
	longFormModelVar    = "AI_MODEL_NAME"
)

// TestALongFormModelsEntryIsReportedAndAShortFormOneDraftsAsBefore imports the
// models fixture with no setting: the chat service names its model in the short
// form and drafts exactly as a short-form entry always has, with no comment above
// its connection and nothing added to its decision; the search service names its
// model in the long form, with an endpoint_var and a model_var, and both names
// are stated, as the entry writes them, in the comment above its connection and in
// its decision, and are held by no key or value of any draft.
//
// 🔴 PLANTED DEFECT: in service.merge (services.go), delete the loop calling
// readModelVars under case "models": the long-form entry's names are dropped
// again, and the case fails on the comment and on the decision.
func TestALongFormModelsEntryIsReportedAndAShortFormOneDraftsAsBefore(t *testing.T) {
	t.Parallel()
	source := fixture(t, "authored/models.compose.yaml")
	for _, needle := range []string{longFormEndpointVar, longFormModelVar} {
		if !strings.Contains(string(source), needle) {
			t.Fatalf("the needle: %q is not in the fixture, so the search finds nothing", needle)
		}
	}
	out := importOne(source, "")
	if out.Error != "" || len(out.Diagnostics) > 0 {
		t.Fatalf("the import failed or reported: %s %+v", out.Error, out.Diagnostics)
	}
	aggregate := fileContent(t, out, "aggregate.yaml")

	// The short-form service: the bytes a short-form entry drafts, nothing above
	// its connection.
	short := "    chat:\n      type: core/compute/server\n      uses:\n        llm:\n          llm:\n            target: llm\n" +
		"    search:\n"
	if !strings.Contains(aggregate, short) {
		t.Errorf("the short-form service does not draft as before:\n%s\nin\n%s", short, aggregate)
	}
	report := "the service search reads the address of the model embedder as " + longFormEndpointVar +
		" and its identifier as " + longFormModelVar +
		", the names its models entry gives them; no key of the draft holds either name"
	long := "    search:\n      type: core/compute/server\n      uses:\n        # T" + report[1:] + ".\n" +
		"        embedder:\n          embedder:\n            target: embedder\n"
	if !strings.Contains(aggregate, long) {
		t.Errorf("the long-form service's connection is not\n%s\nin\n%s", long, aggregate)
	}

	services := serviceDecisions(out)
	if len(services) != 2 || len(modelDecisions(out)) != 2 {
		t.Fatalf("the import made %d decisions, want one per service and one per model: %+v", len(out.Decisions),
			out.Decisions)
	}
	chat, search := services[0], services[1]
	want := "no row of the image table or of the mapping_overrides setting maps the repository ghcr.io/example/chat, " +
		"so the service is drafted as an instance of " + Server + " running its image"
	if chat.SourceNode.ID != "chat" || chat.Rationale != want {
		t.Errorf("the short-form service's decision for %s records %q, want %q", chat.SourceNode.ID, chat.Rationale, want)
	}
	if search.SourceNode.ID != "search" || !strings.HasSuffix(search.Rationale, "; "+report) {
		t.Errorf("the long-form service's decision for %s records %q, want it to end %q", search.SourceNode.ID,
			search.Rationale, "; "+report)
	}

	// No key or value of any draft holds either name; the walk finds a value the
	// draft does write, so its silence over the names is a finding.
	var scalars []string
	for _, f := range out.Files {
		text := f.Content
		if f.Path == "aggregate.yaml" {
			text = evaluatedAggregate(t, text)
		}
		scalars = append(scalars, scalarsOf(t, f.Path, text)...)
	}
	if !slices.Contains(scalars, "ai/mxbai-embed-large") {
		t.Fatalf("the walk over the drafts' keys and values found no model identifier, so its silence proves nothing: %v",
			scalars)
	}
	for _, s := range scalars {
		for _, needle := range []string{longFormEndpointVar, longFormModelVar} {
			if strings.Contains(s, needle) {
				t.Errorf("a key or value of a draft holds %q: %q", needle, s)
			}
		}
	}
}

// scalarsOf reads a draft as YAML and returns every key and value it writes, its
// comments left out, failing the test where it does not read as YAML.
func scalarsOf(t *testing.T, path, text string) []string {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		t.Fatalf("%s does not read as YAML: %v\n%s", path, err, text)
	}
	var out []string
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			out = append(out, n.Value)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&root)
	return out
}

// TestALongFormModelsEntryIsReadAsComposeMergesIt holds the report of a long-form
// models entry to what the entry states: a model_var alone is reported alone; an
// override file's endpoint_var replaces the Compose file's, which keeps its
// model_var; and an entry written empty states neither, so the service drafts
// byte for byte as the short form drafts it.
func TestALongFormModelsEntryIsReadAsComposeMergesIt(t *testing.T) {
	t.Parallel()
	const models = "models:\n  llm:\n    model: ai/smollm2\n"
	sources := func(files ...string) []aggrega.ImportSource {
		var out []aggrega.ImportSource
		for i := 0; i+1 < len(files); i += 2 {
			out = append(out, aggrega.ImportSource{Name: files[i], Bytes: []byte(files[i+1])})
		}
		return out
	}
	shortForm := Import(aggrega.ImportInput{Format: Format, Catalog: catalog, Sources: sources("compose.yaml",
		"services:\n  app:\n    image: ghcr.io/example/app:1.0.0\n    models:\n      - llm\n"+models)}, hostParse)
	for _, row := range []struct {
		name        string
		sources     []aggrega.ImportSource
		wantComment string
	}{
		{
			name: "a model_var alone",
			sources: sources("compose.yaml", "services:\n  app:\n    image: ghcr.io/example/app:1.0.0\n    models:\n"+
				"      llm:\n        model_var: LLM_IDENTIFIER\n"+models),
			wantComment: "        # The service app reads the identifier of the model llm as LLM_IDENTIFIER, the name its models " +
				"entry gives it; no key of the draft holds that name.\n        llm:\n",
		},
		{
			name: "an override file's endpoint_var",
			sources: sources(
				"compose.yaml", "services:\n  app:\n    image: ghcr.io/example/app:1.0.0\n    models:\n      llm:\n"+
					"        endpoint_var: BASE_URL\n        model_var: BASE_NAME\n"+models,
				"compose.override.yaml", "services:\n  app:\n    models:\n      llm:\n        endpoint_var: OVERRIDE_URL\n"),
			wantComment: "        # The service app reads the address of the model llm as OVERRIDE_URL and its identifier as " +
				"BASE_NAME, the names its models entry gives them; no key of the draft holds either name.\n        llm:\n",
		},
		{
			name: "an entry written empty",
			sources: sources("compose.yaml", "services:\n  app:\n    image: ghcr.io/example/app:1.0.0\n    models:\n"+
				"      llm: {}\n"+models),
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := Import(aggrega.ImportInput{Format: Format, Catalog: catalog, Sources: row.sources}, hostParse)
			aggregate := fileContent(t, out, "aggregate.yaml")
			if row.wantComment == "" {
				if want := fileContent(t, shortForm, "aggregate.yaml"); aggregate != want {
					t.Errorf("an entry stating neither variable drafts\n%s\nnot as the short form does\n%s", aggregate, want)
				}
				got, want := serviceDecisions(out)[0].Rationale, serviceDecisions(shortForm)[0].Rationale
				if got != want {
					t.Errorf("an entry stating neither variable records %q, not %q", got, want)
				}
				return
			}
			if !strings.Contains(aggregate, row.wantComment) {
				t.Errorf("the connection is not reported as\n%s\nin\n%s", row.wantComment, aggregate)
			}
			report := strings.TrimSuffix(strings.TrimPrefix(row.wantComment, "        # T"), ".\n        llm:\n")
			if got := serviceDecisions(out)[0].Rationale; !strings.HasSuffix(got, "; t"+report) {
				t.Errorf("the decision records %q, want it to end %q", got, "; t"+report)
			}
		})
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
		{"an override under the default reuse mode", `{` + override + `}`, "core/datastore/redis"},
		{"an override under hints-only", `{"reuse_mode":"hints-only",` + override + `}`, "imported/datastore/bitnami-redis"},
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

// TestAServiceTheTableMatchesIsReferencedUnderAutoApply imports the authored
// fixture with auto-apply and no other setting: the PostgreSQL and Redis
// services, whose rows carry their Components' labels as traits, are referenced
// as core/datastore/postgres and core/datastore/redis at a HIGH match at or above
// the default threshold, and no Component is drafted for either.
//
// 🔴 PLANTED DEFECT: in mappings (images.go), leave Traits unset: each match falls
// to the MEDIUM tier, both services are drafted, and the case fails.
func TestAServiceTheTableMatchesIsReferencedUnderAutoApply(t *testing.T) {
	t.Parallel()
	out := importOne(fixture(t, "authored/compose.yaml"), `{"reuse_mode":"auto-apply-above-threshold"}`)
	if out.Error != "" {
		t.Fatalf("the import failed: %s %+v", out.Error, out.Diagnostics)
	}
	if want := []string{"aggregate.yaml", "release.yaml"}; !slices.Equal(paths(out), want) {
		t.Errorf("the import wrote %v, want %v and no drafted Component", paths(out), want)
	}
	aggregate := fileContent(t, out, "aggregate.yaml")
	for _, want := range []string{"    db:\n      type: core/datastore/postgres\n", "    cache:\n      type: core/datastore/redis\n"} {
		if !strings.Contains(aggregate, want) {
			t.Errorf("the Aggregate does not hold\n%s\nin\n%s", want, aggregate)
		}
	}
	for i, want := range []string{"core/datastore/postgres", "core/datastore/redis"} {
		d := out.Decisions[i]
		if d.Chosen == nil || d.Chosen.Target != want || d.Chosen.Mode != "reference" || d.Chosen.Tier != "HIGH" ||
			len(d.Candidates) == 0 || d.Candidates[0].Score < 0.85 {
			t.Errorf("the decision for %s chose %+v over %+v, want a reference to %s at HIGH, scoring at least 0.85",
				d.SourceNode.ID, d.Chosen, d.Candidates, want)
		}
	}
}

// TestTheImageTableIsTheImportersMappingArm holds the image table, read as the
// mapping arm, to its rows: the PostgreSQL and Redis repositories map to the
// Components their Recipes implement, under the category their names place them
// in, with traits that leave the category out; every row's traits are the arm's;
// and every row is keyed by its repository, in order.
func TestTheImageTableIsTheImportersMappingArm(t *testing.T) {
	t.Parallel()
	arm := mappings()
	for repository, want := range map[string]string{"postgres": "core/datastore/postgres", "redis": "core/datastore/redis"} {
		got, ok := arm[repository]
		if !ok || got.Component != want || got.Category != "datastore" || got.Vendor != want[strings.LastIndex(want, "/")+1:] {
			t.Errorf("the arm maps %s to %+v, want %s in datastore", repository, got, want)
		}
		if _, category := got.Traits["component.aggrega.ai/category"]; len(got.Traits) == 0 || category {
			t.Errorf("the arm gives %s the traits %v, want its Component's labels but its category", repository, got.Traits)
		}
	}
	for _, row := range imageTable {
		if !maps.Equal(arm[row.Repository].Traits, row.Traits) {
			t.Errorf("the arm gives %s the traits %v, not its row's %v", row.Repository, arm[row.Repository].Traits, row.Traits)
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

// TestTwoImportsAreByteIdentical imports the authored fixtures and the LangGraph
// and CrewAI applications twice each, over a catalog lacking the model runtime
// and over one holding it, and requires the same output, byte for byte —
// entities, decisions and files alike.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"authored/compose.yaml", "authored/models.compose.yaml",
		"authored/model-runtime.compose.yaml", "compose-for-agents/langgraph.compose.yaml",
		"compose-for-agents/crew-ai.compose.yaml"} {
		body := fixture(t, name)
		for _, view := range []*aggrega.CatalogProjection{catalog, catalogHoldingTheRuntime} {
			first, _ := json.Marshal(importOver(body, "", view))
			second, _ := json.Marshal(importOver(body, "", view))
			if string(first) != string(second) {
				t.Errorf("two imports of %s over the catalog holding %d Components differ", name, len(view.Components))
			}
		}
	}
}

// TestANameHoldingDoubleBracesReadsBackInTheAggregate imports a Compose file whose
// service and model are named with "{{" — each name written into a comment of the
// Aggregate — and whose model's identifier holds one too, written as a value, and
// requires the Aggregate to run as a template — as the compiler evaluates its
// whole text, comments included — each comment reading back with the name as the
// file writes it and the identifier reading back as written.
//
// 🔴 PLANTED DEFECT: in importer.CommentLine
// (aggrega-pdk/go/pkg/importer/definition.go), return the line unescaped; or in
// renderAggregate (render.go), write the model's identifier through yamlString
// alone: the Aggregate no longer parses as a template, and the case fails on it.
func TestANameHoldingDoubleBracesReadsBackInTheAggregate(t *testing.T) {
	out := importOne([]byte("services:\n  \"web{{x}}\":\n    image: nginx:1.27\n    models:\n      - \"llm{{y}}\"\n"+
		"models:\n  \"llm{{y}}\":\n    model: \"ai/{{ z }}\"\n"), "")
	text := fileContent(t, out, "aggregate.yaml")
	got := evaluatedAggregate(t, text)
	for _, want := range []string{"# The service web{{x}}.\n", "# The model llm{{y}}.\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the evaluated Aggregate does not say %q:\n%s", want, got)
		}
	}
	var doc struct {
		Spec struct {
			Components map[string]struct {
				Values map[string]any `yaml:"values"`
			} `yaml:"components"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("the evaluated Aggregate does not read as YAML: %v\n%s", err, got)
	}
	if model := doc.Spec.Components["llm-y"].Values["model"]; model != "ai/{{ z }}" {
		t.Errorf("the model's identifier reads back as %#v, want %q:\n%s", model, "ai/{{ z }}", got)
	}
}

// golden reads one draft of the authored model-runtime fixture over a catalog
// lacking the model runtime, each model drafted as a Component of its own: the
// bytes the fallback is held to.
func golden(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "model-runtime-lacking", filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read the golden %s: %v", path, err)
	}
	return string(body)
}

// runtimeReason is how the decision for a model of the catalog's model runtime
// opens.
const runtimeReason = "the workspace's catalog holds " + ModelRuntime + ", which serves a model named by its OCI " +
	"reference through Docker Model Runner, so "

// wantDecision is the decision a case expects for one node: its identifier,
// its summary, the choice and the rationale, every one compared whole.
type wantDecision struct {
	id, summary, target, mode, rationale string
}

// assertDecisions holds got to want, one decision apiece, in order.
func assertDecisions(t *testing.T, got []aggrega.ImportDecision, want []wantDecision) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("the import made %d decisions, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		d := got[i]
		if d.SourceNode.ID != w.id || d.SourceNode.ParamsSummary != w.summary || d.Chosen == nil ||
			d.Chosen.Target != w.target || d.Chosen.Mode != w.mode || d.Chosen.Tier != "NONE" || d.Rationale != w.rationale {
			t.Errorf("decision %d is %+v (chose %+v), want %+v", i, d, d.Chosen, w)
		}
	}
}

// TestAModelIsAnInstanceOfTheModelRuntimeWhereTheCatalogHoldsIt imports the
// authored model-runtime fixture and the compose-for-agents CrewAI application
// over a catalog holding core/ai-model/docker-model-runner, under both reuse
// modes: every model is an instance of it, referenced, and no model Component is
// drafted; its values are the source's model, its context_size under
// context_window, the key the runtime declares for a model's context length, and
// its runtime_flags as written; the Release places each model's instance with an
// empty entry after the services', so a build places it; every model's decision
// says it is referenced and why; and every service drafts exactly as it does over
// a catalog lacking the runtime — its connection to each model, and the report of
// a long-form entry's variables above it and in its decision, included.
//
// 🔴 PLANTED DEFECT: in Import (composeimport.go), write `held := false` in place
// of `held := holdsModelRuntime(in.Catalog)`: every model is drafted again, and
// both rows fail on the files, the Aggregate and the decisions.
func TestAModelIsAnInstanceOfTheModelRuntimeWhereTheCatalogHoldsIt(t *testing.T) {
	t.Parallel()
	// A model's instance opens at the indent of every instance, after a line
	// break; its key alone also opens a connection to it, deeper.
	servicesBefore, _, _ := strings.Cut(golden(t, "aggregate.yaml"), "\n    llm:\n")
	worker := serviceDecisions(importOver(fixture(t, "authored/model-runtime.compose.yaml"), "", catalog))[1].Rationale
	for _, row := range []struct {
		name, fixture string
		// first opens the instance of the first model, which every service's
		// instance precedes.
		first string
		// aggregate is the whole drafted Aggregate, or, where release is empty, a
		// part of it; release is the whole drafted Release, and releaseTail how it
		// ends: the last service's entry, then an empty entry for each model.
		aggregate, release, releaseTail string
		decisions                       []wantDecision
	}{
		{
			name:    "the authored application",
			fixture: "authored/model-runtime.compose.yaml",
			first:   "\n    llm:\n",
			aggregate: servicesBefore +
				"\n    llm:\n      type: " + ModelRuntime + "\n      values:\n        model: \"ai/smollm2\"\n" +
				"        context_window: 4096\n        runtime_flags:\n          - \"--threads\"\n          - \"4\"\n" +
				"    embed:\n      type: " + ModelRuntime + "\n      values:\n        model: \"ai/mxbai-embed-large\"\n",
			release:     golden(t, "release.yaml"),
			releaseTail: "    worker:\n      infra:\n        image: \"ghcr.io/example/worker:1.0.0\"\n    llm: {}\n    embed: {}\n",
			decisions: []wantDecision{
				{"llm", "model ai/smollm2, context_size 4096, 2 runtime flags", ModelRuntime, "reference",
					runtimeReason + "llm is an instance of it, serving ai/smollm2, used by app"},
				{"embed", "model ai/mxbai-embed-large", ModelRuntime, "reference",
					runtimeReason + "embed is an instance of it, serving ai/mxbai-embed-large, used by worker"},
				{"app", "no parameters", Server, "reference", "no row of the image table or of the mapping_overrides " +
					"setting maps the repository ghcr.io/example/app, so the service is drafted as an instance of " +
					Server + " running its image"},
				{"worker", "no parameters", Server, "reference", worker},
			},
		},
		{
			name:    "the compose-for-agents CrewAI application",
			fixture: "compose-for-agents/crew-ai.compose.yaml",
			first:   "\n    qwen3:\n",
			aggregate: "\n    qwen3:\n      type: " + ModelRuntime + "\n      values:\n        model: \"ai/qwen3\"\n" +
				"        context_window: 8192\n        runtime_flags:\n          - \"--no-prefill-assistant\"\n",
			releaseTail: "        image: \"docker/mcp-gateway:latest\"\n    qwen3: {}\n",
			decisions: []wantDecision{
				{"qwen3", "model ai/qwen3, context_size 8192, 1 runtime flag", ModelRuntime, "reference",
					runtimeReason + "qwen3 is an instance of it, serving ai/qwen3, used by agents"},
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			body := fixture(t, row.fixture)
			out := importOver(body, "", catalogHoldingTheRuntime)
			if out.Error != "" || len(codes(out, "parse-failed")) > 0 {
				t.Fatalf("the import failed: %s %+v", out.Error, out.Diagnostics)
			}
			if want := []string{"aggregate.yaml", "release.yaml"}; !slices.Equal(paths(out), want) {
				t.Errorf("the import wrote %v, want %v and no drafted model Component", paths(out), want)
			}
			aggregate := fileContent(t, out, "aggregate.yaml")
			if row.release != "" {
				if aggregate != row.aggregate {
					t.Errorf("the Aggregate is\n%s\nwant\n%s", aggregate, row.aggregate)
				}
				if release := fileContent(t, out, "release.yaml"); release != row.release {
					t.Errorf("the Release is\n%s\nwant, as over a catalog lacking the runtime,\n%s", release, row.release)
				}
			} else if !strings.Contains(aggregate, row.aggregate) {
				t.Errorf("the Aggregate does not hold\n%s\nin\n%s", row.aggregate, aggregate)
			}
			if release := fileContent(t, out, "release.yaml"); !strings.HasSuffix(release, row.releaseTail) {
				t.Errorf("the Release does not end\n%s\nbut is\n%s", row.releaseTail, release)
			}
			services, models, found := strings.Cut(aggregate, row.first)
			lacking, _, _ := strings.Cut(fileContent(t, importOver(body, "", catalog), "aggregate.yaml"), row.first)
			if !found || services != lacking {
				t.Errorf("the services are drafted\n%s\nnot as over a catalog lacking the runtime\n%s", services, lacking)
			}
			for _, absent := range []string{"context_size", "/ai-model/qwen3", "imported/ai-model"} {
				if strings.Contains(aggregate, absent) {
					t.Errorf("the Aggregate writes %q:\n%s", absent, aggregate)
				}
			}
			got := modelDecisions(out)
			if row.release != "" {
				got = out.Decisions
			}
			assertDecisions(t, got, row.decisions)
			var referenced int
			for _, e := range out.Entities {
				if e.Kind == "Component" && e.Name == ModelRuntime && e.Mode == "reference" && e.DecisionRef == "decision:"+e.SourceRef {
					referenced++
				}
			}
			if referenced != len(modelDecisions(out)) {
				t.Errorf("the import emitted %d references to %s for %d models: %+v", referenced, ModelRuntime,
					len(modelDecisions(out)), out.Entities)
			}

			// The reference is made whatever the reuse mode says.
			hinted := importOver(body, `{"reuse_mode":"hints-only"}`, catalogHoldingTheRuntime)
			if _, got, _ := strings.Cut(fileContent(t, hinted, "aggregate.yaml"), row.first); got != models {
				t.Errorf("under hints-only the models are drafted\n%s\nnot as under the default reuse mode\n%s", got,
					models)
			}
		})
	}
}

// TestAModelDraftsAsBeforeWhereTheCatalogLacksTheRuntime imports the authored
// model-runtime fixture over a catalog lacking core/ai-model/docker-model-runner,
// and over no catalog at all: every draft is byte for byte its golden in
// testdata/model-runtime-lacking, which holds what an import referencing no model
// drafts — each model a Component of its own, its instance placed by an empty
// entry of the Release as an instance of the runtime is — and each model's
// decision says it is drafted and why: the workspace's catalog holds no
// core/ai-model/docker-model-runner.
//
// 🔴 PLANTED DEFECT: in holdsModelRuntime (composeimport.go), return true for a
// nil catalog: the row naming no catalog references the runtime and fails.
//
// 🔴 PLANTED DEFECT: in composeRelease (render.go), write an entry only for a
// model whose instance is of the runtime: both rows fail on release.yaml, which
// then places neither drafted model.
func TestAModelDraftsAsBeforeWhereTheCatalogLacksTheRuntime(t *testing.T) {
	t.Parallel()
	const reason = "the workspace's catalog holds no " + ModelRuntime + ", so the model is drafted as "
	for _, row := range []struct {
		name string
		view *aggrega.CatalogProjection
	}{
		{"a catalog lacking the runtime", catalog},
		{"no catalog", nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := importOver(fixture(t, "authored/model-runtime.compose.yaml"), "", row.view)
			want := []string{"components/imported-ai-model-llm.yaml", "components/imported-ai-model-embed.yaml",
				"aggregate.yaml", "release.yaml"}
			if !slices.Equal(paths(out), want) {
				t.Fatalf("the import wrote %v, want %v", paths(out), want)
			}
			for _, path := range want {
				if got, before := fileContent(t, out, path), golden(t, path); got != before {
					t.Errorf("%s is\n%s\nnot its golden\n%s", path, got, before)
				}
			}
			models := modelDecisions(out)
			assertDecisions(t, models, []wantDecision{
				{"llm", "model ai/smollm2, context_size 4096, 2 runtime flags", "imported/ai-model/llm", "draft",
					reason + "imported/ai-model/llm, a Component of its own, serving ai/smollm2, used by app"},
				{"embed", "model ai/mxbai-embed-large", "imported/ai-model/embed", "draft",
					reason + "imported/ai-model/embed, a Component of its own, serving ai/mxbai-embed-large, used by worker"},
			})
			if services := serviceDecisions(out); len(services) != 2 || services[0].SourceNode.ID != "app" ||
				services[1].SourceNode.ID != "worker" || out.Decisions[0].SourceNode.ID != "llm" {
				t.Errorf("the import decided %+v, want each model and then each service", out.Decisions)
			}
		})
	}
}

// TestAModelMemberTheRuntimeDeclaresNoValueForIsNamedAboveItsInstance imports
// models over a catalog holding the model runtime: a member the runtime declares
// no value for is written nowhere and named in a comment above the instance, as
// for a drafted model, and a model naming no OCI reference leaves values.model
// for the author, saying so above the instance, its decision naming none; the
// Release places each instance with an empty entry and writes nothing else of it.
func TestAModelMemberTheRuntimeDeclaresNoValueForIsNamedAboveItsInstance(t *testing.T) {
	t.Parallel()
	out := importOver([]byte("services:\n  app:\n    image: ghcr.io/example/app:1.0.0\n    models:\n      - llm\n"+
		"      - bare\nmodels:\n  llm:\n    model: ai/smollm2\n    x-gpu: required\n  bare:\n    context_size: 2048\n"),
		"", catalogHoldingTheRuntime)
	aggregate := fileContent(t, out, "aggregate.yaml")
	for _, want := range []string{
		"    # Not carried, since " + ModelRuntime + " declares no value for it: x-gpu.\n    llm:\n      type: " +
			ModelRuntime + "\n      values:\n        model: \"ai/smollm2\"\n",
		"    # values.model is left for the author, since the source names no model.\n    bare:\n      type: " +
			ModelRuntime + "\n      values:\n        context_window: 2048\n",
	} {
		if !strings.Contains(aggregate, want) {
			t.Errorf("the Aggregate does not hold\n%s\nin\n%s", want, aggregate)
		}
	}
	if strings.Contains(aggregate, "required") {
		t.Errorf("a member the runtime declares no value for is written as a value:\n%s", aggregate)
	}
	if release := fileContent(t, out, "release.yaml"); !strings.HasSuffix(release, "    llm: {}\n    bare: {}\n") ||
		strings.Contains(release, "required") {
		t.Errorf("the Release does not end with an empty entry for each model, and nothing else of them:\n%s", release)
	}
	assertDecisions(t, modelDecisions(out), []wantDecision{
		{"llm", "model ai/smollm2", ModelRuntime, "reference", runtimeReason + "llm is an instance of it, serving " +
			"ai/smollm2, used by app"},
		{"bare", "no model, context_size 2048", ModelRuntime, "reference", runtimeReason + "bare is an instance of it, " +
			"used by app"},
	})
}

// evaluatedAggregate evaluates a drafted Aggregate's text as a template, as the
// compiler does before it reads the Aggregate as YAML, failing the test where it
// does not parse or run as one, and returns the evaluated text.
func evaluatedAggregate(t *testing.T, text string) string {
	t.Helper()
	tmpl, err := template.New("aggregate").Parse(text)
	if err != nil {
		t.Fatalf("the drafted Aggregate does not parse as a template: %v\n%s", err, text)
	}
	var evaluated strings.Builder
	if err := tmpl.Execute(&evaluated, nil); err != nil {
		t.Fatalf("the drafted Aggregate does not run as a template: %v\n%s", err, text)
	}
	return evaluated.String()
}
