// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs of the reference Compose importer through the real Import,
// over a workspace whose catalog folder holds the reference importers and a JSON
// parser beside their entities, and the hub's PostgreSQL, Redis and generic server
// Components — the YAML a Compose file is read as is parsed by the parser the
// binary carries, since the folder serves none:
//
//   - the three authored applications and three applications of the
//     compose-for-agents repository, each laid down as compose.yaml, are recognised
//     as the compose format with no format named and drafted as an Aggregate and a
//     Release;
//   - where the catalog folder holds the hub's Docker Model Runner runtime, each
//     model is an instance of it, and no model Component is drafted;
//   - by default every environment value is carried as written into the Release
//     and named in a warning, and the empty_credentials setting at all writes each
//     empty, alike whatever its variable is called;
//   - a project directory is merged in Compose's order;
//   - two imports of one application are byte-identical;
//   - a Compose file the parser refuses halts the import with the parser's reason.
//
// The fixtures and their provenance are described in testdata/compose/README.md.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// composeFixtureDir holds the committed Compose fixtures, relative to this
// package.
const composeFixtureDir = "testdata/compose"

// composeHubEntities are the hub's entities the import's catalog view holds, by
// their path beneath platformHub: the Components a Compose service is matched
// against or drafted as, and the schema library the generic server's values
// read.
var composeHubEntities = []string{
	"component/core/datastore/postgres/1.0.0/component.yaml",
	"component/core/datastore/redis/1.0.0/component.yaml",
	"component/core/compute/server/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// composeWorkspace is catalogFolderImportWorkspaceAttaching with
// composeHubEntities copied into the catalog folder at the paths the hub keeps
// them under.
func composeWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	layHubEntities(t, folder.Dir, composeHubEntities)
	return rw
}

// readComposeFixture reads one committed Compose fixture.
func readComposeFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(composeFixtureDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the committed fixture %s: %v", name, err)
	}
	return body
}

// composeFixtures are the committed applications, each with the instance keys its
// draft holds, in the order the file writes its services and then its models.
var composeFixtures = []struct {
	fixture string
	keys    []string
}{
	{"authored/compose.yaml", []string{"db", "cache", "api"}},
	{"authored/models.compose.yaml", []string{"chat", "search", "llm", "embedder"}},
	{"authored/model-runtime.compose.yaml", []string{"app", "worker", "llm", "embed"}},
	{"compose-for-agents/langgraph.compose.yaml", []string{"database", "importer", "agent", "mcp-gateway", "qwen3"}},
	{"compose-for-agents/adk.compose.yaml", []string{"adk", "mcp-gateway", "gemma3"}},
	{"compose-for-agents/crew-ai.compose.yaml", []string{"agents", "mcp-gateway", "qwen3"}},
}

// TestEveryComposeApplicationIsRecognisedAndDraftedAsAnAggregateAndARelease
// imports each committed application laid down as compose.yaml, naming no format,
// and requires it read as the compose format and drafted as an Aggregate and a
// Release holding its services and models in the order the file writes them; the
// authored application's PostgreSQL and Redis services drafted with the hints
// naming core/datastore/postgres and core/datastore/redis, its API an instance of
// core/compute/server.
//
// 🔴 PLANTED DEFECT: in detect (plugins/compose-importer/main.go), write
// `{Path: "services", Is: "array"}`: every row is refused asking for the format.
func TestEveryComposeApplicationIsRecognisedAndDraftedAsAnAggregateAndARelease(t *testing.T) {
	t.Parallel()
	for _, f := range composeFixtures {
		t.Run(f.fixture, func(t *testing.T) {
			t.Parallel()
			res, dir := writtenImport(t, composeWorkspace(t, nil), "compose.yaml", readComposeFixture(t, f.fixture))
			if res.Format != "compose" {
				t.Fatalf("the source was read as the %q format, want compose", res.Format)
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			aggregateKeys, releaseKeys, aggregate, release := mcpDrafts(t, dir)
			if !slices.Equal(aggregateKeys, f.keys) {
				t.Errorf("the Aggregate holds %v, want %v", aggregateKeys, f.keys)
			}
			services := slices.DeleteFunc(slices.Clone(f.keys), func(k string) bool {
				return slices.Contains([]string{"qwen3", "gemma3", "llm", "embedder", "embed"}, k)
			})
			if !slices.Equal(releaseKeys, services) {
				t.Errorf("the Release holds %v, want the services %v", releaseKeys, services)
			}
			if f.fixture != "authored/compose.yaml" {
				return
			}
			for _, want := range []string{"    db:\n      type: imported/datastore/postgres\n",
				"    cache:\n      type: imported/datastore/redis\n", "    api:\n      type: core/compute/server\n"} {
				if !strings.Contains(aggregate, want) {
					t.Errorf("the Aggregate does not hold\n%s\nin\n%s", want, aggregate)
				}
			}
			for path, hint := range map[string]string{
				"components/imported-datastore-postgres.yaml": "# resembles core/datastore/postgres",
				"components/imported-datastore-redis.yaml":    "# resembles core/datastore/redis",
			} {
				body, err := os.ReadFile(filepath.Join(dir, path))
				if err != nil || !strings.Contains(string(body), hint) {
					t.Errorf("the drafted %s does not carry %q (%v)", path, hint, err)
				}
			}
			if !strings.Contains(release, "        image: \"ghcr.io/example/shop-api:1.4.2\"\n        port: 8080\n") {
				t.Errorf("the Release does not run the API's image on its port:\n%s", release)
			}
			var hinted bool
			for _, d := range res.Decisions {
				hinted = hinted || strings.Contains(d.Rationale, "environment variable CACHE_URL names the service cache")
			}
			if !hinted {
				t.Errorf("no decision records the API's environment naming the cache: %+v", res.Decisions)
			}
		})
	}
}

// modelRuntimeEntity is the hub's Docker Model Runner runtime, by its path
// beneath platformHub.
const modelRuntimeEntity = "component/core/ai-model/docker-model-runner/1.0.0/component.yaml"

// TestAComposeModelIsAnInstanceOfTheHubsModelRuntimeWhereTheCatalogHoldsIt
// imports the authored model-runtime application and the compose-for-agents
// CrewAI application, naming no format, over the catalog folder with the hub's
// core/ai-model/docker-model-runner laid beside the other Components: the catalog
// the import hands the importer carries the runtime, so each model is an
// instance of it, writing the source's context_size as context_window, the
// Release places it with an empty entry, no model Component file is drafted, and
// each model's decision references it.
//
// 🔴 PLANTED DEFECT: in Import (plugins/internal/composeimport/composeimport.go),
// write `held := false`: every row drafts a model Component and fails.
func TestAComposeModelIsAnInstanceOfTheHubsModelRuntimeWhereTheCatalogHoldsIt(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		fixture, model, values string
	}{
		{"authored/model-runtime.compose.yaml", "llm",
			"        model: \"ai/smollm2\"\n        context_window: 4096\n        runtime_flags:\n" +
				"          - \"--threads\"\n          - \"4\"\n"},
		{"compose-for-agents/crew-ai.compose.yaml", "qwen3",
			"        model: \"ai/qwen3\"\n        context_window: 8192\n        runtime_flags:\n" +
				"          - \"--no-prefill-assistant\"\n"},
	} {
		t.Run(row.fixture, func(t *testing.T) {
			t.Parallel()
			rw, folder := catalogFolderImportWorkspaceAttaching(t, nil)
			layHubEntities(t, folder.Dir, append(slices.Clone(composeHubEntities), modelRuntimeEntity))
			res, dir := writtenImport(t, rw, "compose.yaml", readComposeFixture(t, row.fixture))
			if res.Format != "compose" || res.Halted() {
				t.Fatalf("the source was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
				t.Errorf("the import drafted %v, want the Aggregate and the Release alone", got)
			}
			_, releaseKeys, aggregate, release := mcpDrafts(t, dir)
			want := "\n    " + row.model + ":\n      type: core/ai-model/docker-model-runner\n      values:\n" + row.values
			if !strings.Contains(aggregate, want) {
				t.Errorf("the Aggregate does not hold\n%s\nin\n%s", want, aggregate)
			}
			if !slices.Contains(releaseKeys, row.model) || !strings.Contains(release, "\n    "+row.model+": {}\n") {
				t.Errorf("the Release does not place %s with an empty entry:\n%s", row.model, release)
			}
			referenced := false
			for _, d := range res.Decisions {
				referenced = referenced || (d.SourceNode.ID == row.model && d.SourceNode.Type == "model" && d.Chosen != nil &&
					d.Chosen.Target == "core/ai-model/docker-model-runner" && d.Chosen.Mode == "reference")
			}
			if !referenced {
				t.Errorf("no decision references the runtime for %s: %+v", row.model, res.Decisions)
			}
		})
	}
}

// The key-shaped values the authored application plants: one under a variable
// whose name reads as a credential, one under a variable whose name does not.
const (
	plantedComposeKey    = "sk-planted-key-shaped-needle-8d21f0"
	plantedComposeBanner = "planted-banner-needle-6b47e2"
)

// TestTheEmptyCredentialsSettingDecidesWhatAComposeImportCarries imports the
// authored application with the empty_credentials setting absent, none, marked
// and all, after a needle proves each planted value is in the fixture: where it
// is absent, none or marked, each value is carried as written into release.yaml,
// reaches nothing else, and is named in a credential-carried warning by its
// service and its variable; where it is all, neither reaches anything and each is
// named in a credential-emptied warning. The two variables are treated alike.
//
// 🔴 PLANTED DEFECT: in located (plugins/internal/composeimport/composeimport.go),
// skip a variable whose name holds KEY: the all row carries the key as written.
func TestTheEmptyCredentialsSettingDecidesWhatAComposeImportCarries(t *testing.T) {
	t.Parallel()
	source := readComposeFixture(t, "authored/compose.yaml")
	planted := []string{plantedComposeKey, plantedComposeBanner}
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			var settings map[string]any
			if setting != "absent" {
				settings = map[string]any{"empty_credentials": setting}
			}
			res, dir := writtenImport(t, composeWorkspace(t, settings), "compose.yaml", source)
			code := "credential-carried"
			if setting == "all" {
				code = "credential-emptied"
				assertNoneReaches(t, planted, source, res, dir)
			} else {
				assertCarriedOnlyInTheRelease(t, planted, source, res, dir)
			}
			named := strings.Join(diagnosticsCoded(res.Diagnostics, code), "\n")
			for _, field := range []string{"environment.PAYMENT_API_KEY of node api", "environment.WELCOME_BANNER of node api"} {
				if !strings.Contains(named, field) {
					t.Errorf("no %s names %q:\n%s", code, field, named)
				}
			}
		})
	}
}

// TestAComposeProjectDirectoryIsMergedInComposesOrder imports a project
// directory, naming no format: compose.override.yaml is merged over compose.yaml,
// and docker-compose.yml, which Compose does not read beside compose.yaml, is not
// read; the Aggregate states both.
func TestAComposeProjectDirectoryIsMergedInComposesOrder(t *testing.T) {
	t.Parallel()
	rw := composeWorkspace(t, nil)
	dir := filepath.Join(t.TempDir(), "project")
	for _, name := range []string{"compose.yaml", "compose.override.yaml", "docker-compose.yml"} {
		importopstest.WriteSource(t, filepath.Join(dir, name), string(readComposeFixture(t, "project/"+name)))
	}
	res, err := importDirectory(t, rw, dir, "project", "")
	if err != nil {
		t.Fatalf("the directory was refused: %v", err)
	}
	if res.Format != "compose" || res.Halted() {
		t.Fatalf("the directory was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	var aggregate, release string
	for _, f := range res.Files {
		switch f.Path {
		case "aggregate.yaml":
			aggregate = f.Content
		case "release.yaml":
			release = f.Content
		}
	}
	for _, want := range []string{"#   - compose.override.yaml, merged over compose.yaml\n",
		": docker-compose.yml.\n", "    worker:\n      type: core/compute/server\n"} {
		if !strings.Contains(aggregate, want) {
			t.Errorf("the Aggregate does not hold %q:\n%s", want, aggregate)
		}
	}
	if !strings.Contains(release, "#   image: \"nginx:1.27-alpine\"\n") || strings.Contains(aggregate+release, "ignored") {
		t.Errorf("the override file is not merged in Compose's order:\n%s\n%s", aggregate, release)
	}
}

// TestTwoImportsOfAComposeApplicationAreByteIdentical imports the LangGraph
// application twice, each into a workspace of its own, and requires the two to
// draft the same files, byte for byte.
func TestTwoImportsOfAComposeApplicationAreByteIdentical(t *testing.T) {
	t.Parallel()
	body := readComposeFixture(t, "compose-for-agents/langgraph.compose.yaml")
	first, _ := writtenImport(t, composeWorkspace(t, nil), "compose.yaml", body)
	second, _ := writtenImport(t, composeWorkspace(t, nil), "compose.yaml", body)
	if len(first.Files) == 0 || len(first.Files) != len(second.Files) {
		t.Fatalf("the two imports drafted %d and %d files", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i].Path != second.Files[i].Path || first.Files[i].Content != second.Files[i].Content {
			t.Errorf("the two imports differ at %s", first.Files[i].Path)
		}
	}
}

// TestAComposeFileTheParserRefusesHaltsTheImport imports a compose.yaml cut short,
// naming its format: the YAML parser refuses it, and the import halts under
// parse-failed carrying the parser's reason.
//
// 🔴 PLANTED DEFECT: in Import (plugins/internal/composeimport/composeimport.go),
// report a refused file under importer.NoNodes: the case fails on the code.
func TestAComposeFileTheParserRefusesHaltsTheImport(t *testing.T) {
	t.Parallel()
	rw := composeWorkspace(t, nil)
	path := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "compose.yaml"), "services: [\n")
	res, err := importDirectory(t, rw, path, "compose.yaml", "compose")
	if err != nil {
		t.Fatalf("the import was refused before the importer ran: %v", err)
	}
	failed := diagnosticsCoded(res.Diagnostics, "parse-failed")
	if !res.Halted() || len(failed) != 1 || !strings.Contains(failed[0], "as compose: yaml parse:") {
		t.Errorf("the import reported parse-failed %q (halted %v), want one carrying the parser's reason", failed,
			res.Halted())
	}
}
