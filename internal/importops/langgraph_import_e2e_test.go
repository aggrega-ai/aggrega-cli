// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs of the reference LangGraph importer through the real Import,
// over a workspace whose catalog folder holds the reference importers and a JSON
// parser beside their entities, and the hub's LangGraph agent and PostgreSQL
// Components:
//
//   - each committed application directory is recognised as the langgraph format
//     with no format named — so no other importer's signature matches it — and
//     drafted as an Aggregate holding one LangGraph agent per graph, wired to the
//     datastore its checkpointer keeps state in, and a Release;
//   - the application's code is never read: the application imports alike with
//     its code's files made unreadable;
//   - by default the values of an inline env are carried as written into the
//     Release and named in a warning, a name reading as a credential's and one
//     that does not alike, and the empty_credentials setting at all writes each
//     empty; no value of an environment file an attachment's with.sources hands
//     reaches anything under any setting;
//   - a directory import hands no environment file, of any format: an unreadable
//     one beside the sources refuses nothing, and a configuration naming one
//     names it as not handed, with how to hand it;
//   - two imports of one application are byte-identical;
//   - a configuration the parser refuses halts the import with the parser's reason;
//   - each draft builds, once the author adds a model and wires each graph to it,
//     with a Release supplying each graph's image and the credentials on its
//     connections: each graph is given the address of the datastore its
//     checkpointer names, with its user and database where it publishes them,
//     and exactly the variables the drafted Release's comment on the image names
//     save those two where it publishes neither, and the compiled graph carries
//     its import path.
//
// The fixtures are described in testdata/langgraph/README.md.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// langgraphFixtureDir holds the committed LangGraph applications, relative to
// this package.
const langgraphFixtureDir = "testdata/langgraph"

// langgraphHubEntities are the hub's entities the import's catalog view holds, by
// their path beneath platformHub: the LangGraph agent, the PostgreSQL its
// checkpointer's default backend maps to, and the schema library their values
// read. The catalog holds no MongoDB, so a checkpointer naming mongo drafts one.
var langgraphHubEntities = []string{
	"component/core/ai-agent/langgraph/1.0.0/component.yaml",
	"component/core/datastore/postgres/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// copyLangGraphHubEntities copies langgraphHubEntities into the catalog folder
// dir at the paths the hub keeps them under.
func copyLangGraphHubEntities(t *testing.T, dir string) {
	t.Helper()
	copyHubEntities(t, dir, langgraphHubEntities)
}

// langgraphWorkspace is catalogFolderImportWorkspaceAttaching with
// langgraphHubEntities copied into the catalog folder.
func langgraphWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	copyLangGraphHubEntities(t, folder.Dir)
	return rw
}

// langgraphWorkspaceHanding is langgraphWorkspace whose attachment of the
// LangGraph importer names sources under its with.sources, every attachment
// handing settings.
func langgraphWorkspaceHanding(t *testing.T, settings map[string]any, sources ...string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	return workspaceHanding(t, "langgraph", settings, langgraphHubEntities, sources...)
}

// langgraphApplication lays the committed application named app down in a
// directory of its own, every file at its place in the application, and returns
// the directory.
func langgraphApplication(t *testing.T, app string) string {
	t.Helper()
	return layFixtureDirectory(t, langgraphFixtureDir, app)
}

// readLangGraphFixture reads one file of a committed application.
func readLangGraphFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(langgraphFixtureDir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the committed fixture %s: %v", name, err)
	}
	return body
}

// graphDraft is the part of a drafted Aggregate the cases read, as the compiler
// reads the YAML.
type graphDraft struct {
	Spec struct {
		Components map[string]struct {
			Type   string         `yaml:"type"`
			Values map[string]any `yaml:"values"`
			Uses   map[string]map[string]struct {
				Target string `yaml:"target"`
			} `yaml:"uses"`
		} `yaml:"components"`
	} `yaml:"spec"`
}

// langgraphApplications are the committed applications, each with what its draft
// holds: the drafted paths; the instance keys of the Aggregate and of the
// Release, in the order each writes them; each graph's import path; the
// datastore every graph is wired to on checkpointer_db and the Component it is
// an instance of; and the address, the user and the database the hub's Recipe —
// or, for a drafted datastore, the scratch Recipe the build case writes — gives
// each graph, the host by target, and a user or database left empty where that
// Recipe publishes none.
var langgraphApplications = []struct {
	app                        string
	paths                      []string
	aggregateKeys, releaseKeys []string
	entrypoints                map[string]string
	datastore, component       string
	hosts                      map[string]string
	port                       string
	user, database             string
}{
	{
		app:           "python",
		paths:         []string{"aggregate.yaml", "release.yaml"},
		aggregateKeys: []string{"triage", "support", "postgres"},
		releaseKeys:   []string{"triage", "support", "postgres"},
		entrypoints: map[string]string{"triage": "./src/support_desk/triage.py:make_graph",
			"support": "./src/support_desk/agent.py:graph"},
		datastore: "postgres", component: "core/datastore/postgres",
		hosts: map[string]string{"helm": "postgres-default", "compose": "postgres"}, port: "5432",
		user: "appuser", database: "appdb",
	},
	{
		app:           "envfile",
		paths:         []string{"aggregate.yaml", "components/imported-datastore-mongodb.yaml", "release.yaml"},
		aggregateKeys: []string{"researcher", "mongodb"},
		releaseKeys:   []string{"researcher", "mongodb"},
		entrypoints:   map[string]string{"researcher": "./src/researcher.ts:graph"},
		datastore:     "mongodb", component: "imported/datastore/mongodb",
		hosts: map[string]string{"helm": scratchMongoHost, "compose": scratchMongoHost}, port: "27017",
	},
}

// graphTargets are the targets each draft is built for: Kubernetes through the
// helm Recipes and Docker Compose through the compose Recipes, so the drafted
// Release's comment on the image is checked against what each of the catalog's
// Recipes renders.
var graphTargets = []string{"helm", "compose"}

// TestEveryLangGraphApplicationIsRecognisedAndDraftedAsAgentsWiredToTheirDatastore
// imports each committed application directory, naming no format, and requires
// it read as the langgraph format — exactly one format recognised, so no other
// importer's signature matched any file the directory holds — and drafted as the
// Aggregate, the Release and, where the catalog holds no Component for the
// checkpointer's backend, that Component: an instance of core/ai-agent/langgraph
// per graph, in the order the configuration writes them, carrying its import path
// as graph_entrypoint and wired on checkpointer_db to the datastore, with no
// model connection; the Release an entry per graph, leaving its image, which it
// says is the application's own, and one for the datastore, so every instance is
// placed.
//
// 🔴 PLANTED DEFECT: in detect (plugins/langgraph-importer/main.go), write
// `{Path: "graphs", Is: "array"}` in the langgraph.json signature: both rows are
// refused asking for the format, and the case fails on it.
func TestEveryLangGraphApplicationIsRecognisedAndDraftedAsAgentsWiredToTheirDatastore(t *testing.T) {
	t.Parallel()
	for _, a := range langgraphApplications {
		t.Run(a.app, func(t *testing.T) {
			t.Parallel()
			res, dir := writtenDirectoryImport(t, langgraphWorkspace(t, nil), langgraphApplication(t, a.app), a.app)
			if res.Format != "langgraph" {
				t.Fatalf("the application was read as the %q format, want langgraph", res.Format)
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, a.paths) {
				t.Errorf("the import drafted %v, want %v", got, a.paths)
			}
			aggregateKeys, releaseKeys, _, release := mcpDrafts(t, dir)
			if !slices.Equal(aggregateKeys, a.aggregateKeys) || !slices.Equal(releaseKeys, a.releaseKeys) {
				t.Errorf("the Aggregate holds %v and the Release %v, want %v and %v", aggregateKeys, releaseKeys,
					a.aggregateKeys, a.releaseKeys)
			}
			if !strings.Contains(release, "#   - infra.image, the container image running the application\n") {
				t.Errorf("the Release does not leave the graphs' image for the Release:\n%s", release)
			}
			if !strings.Contains(release, "# Each graph's image is the application's own, not the server image "+
				"langgraph build builds: its code, with LangGraph installed.\n") {
				t.Errorf("the Release does not say what the graphs' image holds:\n%s", release)
			}

			var draft graphDraft
			text := readDraftedAggregate(t, dir)
			if err := yaml.Unmarshal([]byte(text), &draft); err != nil {
				t.Fatalf("the drafted Aggregate does not read as YAML: %v\n%s", err, text)
			}
			for key, entrypoint := range a.entrypoints {
				c := draft.Spec.Components[key]
				if c.Type != "core/ai-agent/langgraph" || c.Values["graph_entrypoint"] != entrypoint {
					t.Errorf("%s is %q with graph_entrypoint %#v, want core/ai-agent/langgraph with %q", key, c.Type,
						c.Values["graph_entrypoint"], entrypoint)
				}
				if got := c.Uses["checkpointer_db"][a.datastore].Target; got != a.datastore {
					t.Errorf("%s's checkpointer_db connection targets %q, want %s", key, got, a.datastore)
				}
				if _, wired := c.Uses["model"]; wired {
					t.Errorf("%s's model socket is wired, though the configuration names no model", key)
				}
			}
			if got := draft.Spec.Components[a.datastore].Type; got != a.component {
				t.Errorf("the datastore %s is an instance of %q, want %s", a.datastore, got, a.component)
			}
		})
	}
}

// TestTheApplicationsCodeIsNeverRead imports each committed application with
// every file of its code made unreadable, and requires the import to read the
// application as it does otherwise: recognised as langgraph, not halted, and
// drafting the same files. An import that opened a file of the code would be
// refused reading it.
//
// 🔴 PLANTED DEFECT: in detect (plugins/langgraph-importer/main.go), add a
// signature naming `*.py`: the directory read opens the python application's
// code, and that row fails refused.
func TestTheApplicationsCodeIsNeverRead(t *testing.T) {
	skipWhereNoFileIsUnreadable(t)
	t.Parallel()
	for app, code := range map[string]string{
		"python":  "src/support_desk/agent.py",
		"envfile": "src/researcher.ts",
	} {
		t.Run(app, func(t *testing.T) {
			t.Parallel()
			dir := langgraphApplication(t, app)
			makeUnreadable(t, filepath.Join(dir, filepath.FromSlash(code)))
			res, err := importDirectory(t, langgraphWorkspace(t, nil), dir, app, "")
			if err != nil {
				t.Fatalf("the import was refused with the code unreadable: %v", err)
			}
			if res.Format != "langgraph" || res.Halted() || len(draftPaths(res)) == 0 {
				t.Fatalf("the application was read as %q, halted %v, drafting %v: %+v", res.Format, res.Halted(),
					draftPaths(res), res.Diagnostics)
			}
		})
	}
}

// TestNoDirectoryImportOpensAnEnvironmentFile imports, naming no format, the
// envfile application's directory with its local.env and a .env beside it both
// made unreadable, and a CrewAI project's directory with an unreadable .env
// beside its declarations: neither import is refused, since no importer's
// patterns select an environment file, so recognition and the directory read
// open none. The LangGraph draft names local.env as not handed, with how to hand
// it, and names none of its variables.
//
// 🔴 PLANTED DEFECT: in detect (plugins/langgraph-importer/main.go), add back a
// signature naming `*.env`: recognition opens each environment file, and both
// rows fail refused.
func TestNoDirectoryImportOpensAnEnvironmentFile(t *testing.T) {
	skipWhereNoFileIsUnreadable(t)
	t.Parallel()
	for _, row := range []struct {
		name, format string
		dir          func(t *testing.T) string
		workspace    func(t *testing.T) *workspaceconfig.ResolvedWorkspace
		unreadable   []string
	}{
		{"a LangGraph application", "langgraph", func(t *testing.T) string { return langgraphApplication(t, "envfile") },
			func(t *testing.T) *workspaceconfig.ResolvedWorkspace { return langgraphWorkspace(t, nil) },
			[]string{"local.env", ".env"}},
		{"a CrewAI project", "crewai", func(t *testing.T) string { return crewaiProject(t, "classic") },
			func(t *testing.T) *workspaceconfig.ResolvedWorkspace { return crewaiWorkspace(t, nil) },
			[]string{".env"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			dir := row.dir(t)
			for _, name := range row.unreadable {
				path := filepath.Join(dir, name)
				if _, err := os.Stat(path); err != nil {
					importopstest.WriteSource(t, path, "PLACEHOLDER=1\n")
				}
				makeUnreadable(t, path)
			}
			res, err := importDirectory(t, row.workspace(t), dir, "application", "")
			if err != nil {
				t.Fatalf("the import was refused with an environment file unreadable: %v", err)
			}
			if res.Format != row.format || res.Halted() || len(draftPaths(res)) == 0 {
				t.Fatalf("the directory was read as %q, halted %v, drafting %v: %+v", res.Format, res.Halted(),
					draftPaths(res), res.Diagnostics)
			}
			if row.format != "langgraph" {
				return
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
			if !strings.Contains(aggregate, notHandedLocalEnv) {
				t.Errorf("the Aggregate does not name local.env as not handed, with how to hand it:\n%s", aggregate)
			}
			if strings.Contains(release, "TAVILY_API_KEY") {
				t.Errorf("the Release names a variable of a file the import was not handed:\n%s", release)
			}
		})
	}
}

// The key-shaped values the python application's inline env plants, under a name
// reading as a credential's and one that does not. Neither is a real key.
const (
	plantedLangGraphInlineKey   = "sk-planted-langgraph-inline-key-needle-4c1e9a7b"
	plantedLangGraphInlineQueue = "planted-langgraph-inline-queue-needle-8d2f"
)

// plantedLangGraphEnvFile are the values the envfile application's local.env
// holds, each a key-shaped needle no draft may carry: a plain value, one after
// export, a quoted one, both lines of one quoted across two lines, and a line
// naming no variable.
var plantedLangGraphEnvFile = []string{
	"tvly-planted-langgraph-envfile-key-needle-2b7e",
	"planted-langgraph-envfile-depth-needle-61c0",
	"planted-langgraph-envfile-quoted-needle-93fa",
	"planted-langgraph-envfile-multiline-needle-5a1d",
	"planted-langgraph-envfile-inside-needle-77b3",
	"planted-langgraph-envfile-stray-needle-0e48",
}

// TestTheEmptyCredentialsSettingDecidesWhatALangGraphImportCarries imports both
// committed applications with the empty_credentials setting absent, none, marked
// and all, after a needle proves each planted value is in its fixture:
//
//   - where the setting is not all, the inline env's values are carried as
//     written into release.yaml and reach nothing else, and each — under
//     OPENAI_API_KEY and SUPPORT_QUEUE alike, since no name is read — is named in
//     a credential-carried warning by its configuration and its variable; where it
//     is all, neither reaches anything and each is named under credential-emptied;
//   - with local.env handed under the LangGraph importer's with.sources beside
//     the envfile application's directory, no value of it reaches any file,
//     decision or diagnostic under any value of the setting, the Release naming
//     the variables alone, and no credential warning names a variable of the file.
//
// 🔴 PLANTED DEFECT: in located (plugins/internal/langgraphimport/langgraphimport.go),
// return nothing: the all row carries both values as written.
func TestTheEmptyCredentialsSettingDecidesWhatALangGraphImportCarries(t *testing.T) {
	t.Parallel()
	config := readLangGraphFixture(t, "python/langgraph.json")
	env := readLangGraphFixture(t, "envfile/local.env")
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			var settings map[string]any
			if setting != "absent" {
				settings = map[string]any{"empty_credentials": setting}
			}

			res, dir := writtenDirectoryImport(t, langgraphWorkspace(t, settings), langgraphApplication(t, "python"),
				"python")
			planted := []string{plantedLangGraphInlineQueue, plantedLangGraphInlineKey}
			code := "credential-carried"
			if setting == "all" {
				code = "credential-emptied"
				assertNoneReaches(t, planted, config, res, dir)
			} else {
				assertCarriedOnlyInTheRelease(t, planted, config, res, dir)
			}
			named := diagnosticsCoded(res.Diagnostics, code)
			for _, variable := range []string{"env.SUPPORT_QUEUE", "env.OPENAI_API_KEY"} {
				if !slices.ContainsFunc(named, func(m string) bool {
					return strings.Contains(m, variable+" of node langgraph.json")
				}) {
					t.Errorf("no %s names %s of node langgraph.json: %q", code, variable, named)
				}
			}

			attached := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "elsewhere", "local.env"), string(env))
			res, dir = writtenDirectoryImport(t, langgraphWorkspaceHanding(t, settings, attached),
				langgraphApplication(t, "envfile"), "envfile")
			assertNoneReaches(t, plantedLangGraphEnvFile, env, res, dir)
			for _, c := range []string{"credential-carried", "credential-emptied"} {
				if got := diagnosticsCoded(res.Diagnostics, c); len(got) != 0 {
					t.Errorf("a variable of local.env is named under %s: %q", c, got)
				}
			}
			_, _, _, release := mcpDrafts(t, dir)
			if !strings.Contains(release, "    #   - TAVILY_API_KEY\n    #   - RESEARCH_DEPTH\n    #   - SEARCH_REGION\n"+
				"    #   - NOTES\n") {
				t.Errorf("the Release does not name local.env's variables, in its order:\n%s", release)
			}
		})
	}
}

// notHandedLocalEnv is the comment an envfile draft carries where the import was
// not handed local.env: the file named, its variables left for the Release, and
// how to hand it.
const notHandedLocalEnv = "# Not read: the environment file local.env, which the import was not handed; its " +
	"variables are left for the Release. Name the file under the LangGraph importer's with.sources to have its " +
	"variables' names read.\n"

// TestAnEnvironmentFileAnAttachmentHandsIsReadForItsNames imports the envfile
// application's configuration as a file of its own, with its local.env named
// under the LangGraph importer's with.sources: the import reads the file's
// variables' names into the Release and no value of it reaches any file,
// decision or diagnostic. Its control imports the same configuration with no
// with.sources: the Aggregate names local.env as not handed.
//
// 🔴 PLANTED DEFECT: in sourcesOf (plugins/internal/langgraphimport/config.go),
// return importer.Sources(in): the named source replaces the primary bytes, no
// configuration is read, and the import drafts nothing.
func TestAnEnvironmentFileAnAttachmentHandsIsReadForItsNames(t *testing.T) {
	t.Parallel()
	config := readLangGraphFixture(t, "envfile/langgraph.json")
	env := readLangGraphFixture(t, "envfile/local.env")
	attached := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "elsewhere", "local.env"), string(env))

	res, dir := writtenImport(t, langgraphWorkspaceHanding(t, nil, attached), "langgraph.json", config)
	if res.Format != "langgraph" || res.Halted() {
		t.Fatalf("the configuration was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	assertNoneReaches(t, plantedLangGraphEnvFile, env, res, dir)
	_, _, _, release := mcpDrafts(t, dir)
	if !strings.Contains(release, "# The environment file local.env, which the configuration names, names these "+
		"variables") || !strings.Contains(release, "    #   - TAVILY_API_KEY\n") {
		t.Errorf("the Release does not name the variables of the attached local.env:\n%s", release)
	}

	res, dir = writtenImport(t, langgraphWorkspace(t, nil), "langgraph.json", config)
	if text := readDraftedAggregate(t, dir); !strings.Contains(text, notHandedLocalEnv) {
		t.Errorf("the Aggregate does not name local.env as not handed, with how to hand it:\n%s", text)
	}
	if res.Halted() {
		t.Errorf("the import halted: %+v", res.Diagnostics)
	}
}

// TestTwoImportsOfALangGraphApplicationAreByteIdentical imports each committed
// application twice, each into a workspace of its own, and requires the two to
// draft the same files and report the same decisions, byte for byte.
func TestTwoImportsOfALangGraphApplicationAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, a := range langgraphApplications {
		t.Run(a.app, func(t *testing.T) {
			t.Parallel()
			first, err := importDirectory(t, langgraphWorkspace(t, nil), langgraphApplication(t, a.app), a.app, "")
			if err != nil {
				t.Fatalf("the first import was refused: %v", err)
			}
			second, err := importDirectory(t, langgraphWorkspace(t, nil), langgraphApplication(t, a.app), a.app, "")
			if err != nil {
				t.Fatalf("the second import was refused: %v", err)
			}
			if len(first.Files) == 0 || len(first.Files) != len(second.Files) {
				t.Fatalf("the two imports drafted %d and %d files", len(first.Files), len(second.Files))
			}
			for i := range first.Files {
				if first.Files[i].Path != second.Files[i].Path || first.Files[i].Content != second.Files[i].Content {
					t.Errorf("the two imports differ at %s", first.Files[i].Path)
				}
			}
			a, _ := json.Marshal(first.Decisions)
			b, _ := json.Marshal(second.Decisions)
			if string(a) != string(b) {
				t.Errorf("the two imports report different decisions:\n%s\n%s", a, b)
			}
		})
	}
}

// TestALangGraphConfigurationTheParserRefusesHaltsTheImport imports the python
// application with its langgraph.json cut short and named so recognition cannot
// read it, naming the format: the JSON parser refuses it, and the import halts
// under parse-failed carrying the parser's reason.
//
// 🔴 PLANTED DEFECT: in readApplications (plugins/internal/langgraphimport/config.go),
// report a refused source under importer.NoNodes: the case fails on the code.
func TestALangGraphConfigurationTheParserRefusesHaltsTheImport(t *testing.T) {
	t.Parallel()
	dir := langgraphApplication(t, "python")
	importopstest.WriteSource(t, filepath.Join(dir, "langgraph.json"), "{\n  \"graphs\": {\n")
	res, err := importDirectory(t, langgraphWorkspace(t, nil), dir, "python", "langgraph")
	if err != nil {
		t.Fatalf("the import was refused before the importer ran: %v", err)
	}
	failed := diagnosticsCoded(res.Diagnostics, "parse-failed")
	if !res.Halted() || len(failed) != 1 ||
		!strings.Contains(failed[0], `could not parse source "langgraph.json" as langgraph: `) {
		t.Errorf("the import reported parse-failed %q (halted %v), want one naming langgraph.json with the parser's "+
			"reason", failed, res.Halted())
	}
}

// scratchMongoHost is the address the scratch Recipe the build case writes for a
// drafted MongoDB publishes.
const scratchMongoHost = "mongo.example.internal"

// scratchMongoRecipe is a reference Recipe realizing the drafted MongoDB on
// target, publishing scratchMongoHost and port 27017: the Recipe the drafted
// Component's comment leaves for the author.
func scratchMongoRecipe(target string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: mongodb-external\n" +
		"  version: 0.1.0\nspec:\n  capability: " + target + "\n  implements: imported/datastore/mongodb:0.1.0\n" +
		"  provides:\n    host: \"" + scratchMongoHost + "\"\n    port: \"27017\"\n  workload: |\n" +
		"    apiVersion: aggrega.ai/v1beta1\n    kind: Workload\n    form: reference\n" +
		"    endpoint: \"" + scratchMongoHost + "\"\n    port: 27017\n"
}

// langgraphBuildPaths are the catalog paths a drafted application is built
// against: the LangGraph agent, PostgreSQL and the OpenAI model with their
// Kubernetes and Docker Compose Recipes, the schema library, the secret Features
// a connection's credential is attached as with their realizations, the provider
// Plugin entities, and local, the directory the case laid a drafted Component and
// its scratch Recipe in.
func langgraphBuildPaths(local string) []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog", local}
	for _, rel := range []string{
		"component/core/ai-agent/langgraph/1.0.0/component.yaml",
		"component/core/datastore/postgres/1.0.0/component.yaml",
		"component/core/ai-model/openai/1.0.0/component.yaml",
		"recipe/helm/langgraph/1.0.0/recipe.yaml",
		"recipe/compose/langgraph/1.0.0/recipe.yaml",
		"recipe/helm/postgres/1.0.0/recipe.yaml",
		"recipe/compose/postgres/1.0.0/recipe.yaml",
		"recipe/helm/openai/1.0.0/recipe.yaml",
		"recipe/compose/openai/1.0.0/recipe.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature/core/secret-value/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
		"feature-recipe/compose/dotenv-secret/1.0.0/recipe.yaml",
	} {
		paths = append(paths, platformHub+"/"+rel)
	}
	return paths
}

// writeGraphRelease makes, beside the drafted Aggregate in dir, the author's act
// the draft leaves — an OpenAI model instance, gpt-4o, and each graph's model
// connection to it, every other line kept as drafted — and writes a Manifest
// realizing every Component on target and a Release supplying only each graph's
// image and a credential on its model connection and on its checkpointer_db
// connection, so every variable the drafted Release's image comment names is
// rendered. On helm each credential is a core/referenced-secret naming a Secret.
// On compose each is a core/secret-value holding a Compose variable reference;
// two attached by the Release alone would both arrive under the key value, so
// the Aggregate attaches the checkpointer_db one, naming its key, and the
// Release gives its origin, as the LangGraph walkthrough's review does. A
// drafted datastore Component, written under dir's components/, is laid with
// the scratch Recipe for target in local. It returns the Release's path, which
// replaces the drafted Release.
func writeGraphRelease(t *testing.T, dir, local, target string, graphs []string, datastore, component string) string {
	t.Helper()
	secret, realization, environment := "core/referenced-secret", "helm/k8s-existing-secret", "production"
	modelOrigin, storeOrigin := "model-key", "checkpointer-password"
	if target == "compose" {
		secret, realization, environment = "core/secret-value", "compose/dotenv-secret", "development"
		modelOrigin, storeOrigin = "${OPENAI_API_KEY}", "${CHECKPOINTER_PASSWORD}"
	}
	aggregate := readDraftedAggregate(t, dir)
	wired := strings.ReplaceAll(aggregate, "      uses:\n",
		"      uses:\n        model:\n          gpt-4o:\n            target: gpt-4o\n")
	if target == "compose" {
		connection := "          " + datastore + ":\n            target: " + datastore + "\n"
		if !strings.Contains(wired, connection) {
			t.Fatalf("the drafted Aggregate holds no checkpointer_db connection to %s:\n%s", datastore, wired)
		}
		wired = strings.ReplaceAll(wired, connection, connection+"            features:\n              credential:\n"+
			"                type: core/secret-value\n                values:\n                  key: checkpointer_password\n")
	}
	wired += "    gpt-4o:\n      type: core/ai-model/openai\n      values:\n        model: gpt-4o\n"
	if err := os.WriteFile(filepath.Join(dir, "aggregate.yaml"), []byte(wired), 0o644); err != nil {
		t.Fatalf("write the wired Aggregate: %v", err)
	}
	mapping := "core/datastore/postgres:1.0.0: " + target + "/postgres:1.0.0"
	if component != "core/datastore/postgres" {
		body, err := os.ReadFile(filepath.Join(dir, "components", "imported-datastore-mongodb.yaml"))
		if err != nil {
			t.Fatalf("read the drafted datastore Component: %v", err)
		}
		importopstest.WriteSource(t, filepath.Join(local, "component", "imported", "datastore", "mongodb", "0.1.0",
			"component.yaml"), string(body))
		importopstest.WriteSource(t, filepath.Join(local, "recipe", target, "mongodb-external", "0.1.0", "recipe.yaml"),
			scratchMongoRecipe(target))
		mapping = component + ":0.1.0: " + target + "/mongodb-external:0.1.0"
	}
	manifest := "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: imported-langgraph\n" +
		"  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n" +
		"        core/ai-agent/langgraph:1.0.0: " + target + "/langgraph:1.0.0\n" +
		"        core/ai-model/openai:1.0.0: " + target + "/openai:1.0.0\n" +
		"        " + mapping + "\n" +
		"    features:\n      type:\n        " + secret + ":1.0.0: " + realization + ":1.0.0\n"
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: imported-langgraph\n" +
		"  environment: " + environment + "\n  version: \"1.0.0\"\nspec:\n  aggregate: imported/flow\n" +
		"  manifest: imported-langgraph\n  boundaries:\n    main:\n      directory: ./dist/" + target + "\n" +
		"      components:\n        gpt-4o: {}\n        " + datastore + ": {}\n")
	for _, g := range graphs {
		b.WriteString("        " + g + ":\n          infra:\n            image: " + graphImage + "\n" +
			"          uses:\n            model:\n              gpt-4o:\n                features:\n" +
			"                  credential:\n                    type: " + secret + "\n" +
			"                    infra:\n                      origin: " + modelOrigin + "\n" +
			"            checkpointer_db:\n              " + datastore + ":\n                features:\n" +
			"                  credential:\n                    type: " + secret + "\n" +
			"                    infra:\n                      origin: " + storeOrigin + "\n")
	}
	for file, body := range map[string]string{"manifest.yaml": manifest, "release.yaml": b.String()} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatalf("write the %s: %v", file, err)
		}
	}
	return filepath.Join(dir, "release.yaml")
}

// graphImage is the image the Release names for every graph.
const graphImage = "ghcr.io/example/support-desk:1.0.0"

// variableName matches an environment variable's name as the drafted Release's
// comments write one: upper case, in at least two parts joined by underscores.
var variableName = regexp.MustCompile(`\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b`)

// imageContractVariables returns, in the order they are first written, the
// variables the drafted Release's comment on each graph's image names: read from
// the line opening that comment, "Each graph's image is the application's own",
// and every comment line after it that is not a list entry, up to the first list
// entry or the first line that is no comment — so the emitted text is the one
// source of the list. It fails the test where the Release states no such comment
// or it names no variable.
func imageContractVariables(t *testing.T, release string) []string {
	t.Helper()
	var names []string
	in := false
	for _, line := range strings.Split(release, "\n") {
		if !in {
			in = strings.HasPrefix(line, "    # Each graph's image is the application's own")
			if !in {
				continue
			}
		} else if !strings.HasPrefix(line, "    # ") || strings.HasPrefix(line, "    #   ") {
			break
		}
		for _, name := range variableName.FindAllString(line, -1) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		t.Fatalf("the drafted Release states no image comment naming a variable:\n%s", release)
	}
	return names
}

// TestEachLangGraphDraftBuildsReachingTheDatastoreItsCheckpointerNames drafts
// each committed application and builds the draft through the build-faithful
// harness, on helm and on compose, once the author's acts the draft leaves are
// made — a model instance each graph is wired to and, for a drafted datastore, a
// Recipe — with a Release supplying each graph's image and a credential on its
// model connection and on its checkpointer_db connection: each graph is given,
// as LANGGRAPH_CHECKPOINTER_HOST and _PORT, the address of the datastore its
// checkpointer names, and as _USER and _DATABASE that datastore's user and
// database where its Recipe publishes them, and neither variable where it
// publishes neither; each graph's Deployment on helm, and its service on
// compose, renders exactly the LangGraph and OpenAI variables the drafted
// Release's comment on the image names, read from that comment's text, save
// those two where nothing fills them; and the compiled graph's values carry each
// graph's import path as graph_entrypoint, and recursion_limit at the
// Component's default, 25 — read from the compiled graph, which carries both
// whatever names the Recipe delivers them under.
//
// 🔴 PLANTED DEFECT: in writeGraph (plugins/internal/langgraphimport/render.go),
// skip the uses block: the build halts naming the required checkpointer_db socket.
// Dropping LANGGRAPH_CHECKPOINTER_USER from the hub's helm Recipe of the agent
// fails the PostgreSQL application's helm case, and dropping it from the compose
// Recipe its compose case, each naming the variable the comment names and the
// build does not render.
func TestEachLangGraphDraftBuildsReachingTheDatastoreItsCheckpointerNames(t *testing.T) {
	t.Parallel()
	for _, a := range langgraphApplications {
		for _, target := range graphTargets {
			t.Run(a.app+"/"+target, func(t *testing.T) {
				t.Parallel()
				_, dir := writtenDirectoryImport(t, langgraphWorkspace(t, nil), langgraphApplication(t, a.app), a.app)
				local := t.TempDir()
				var graphs []string
				for key := range a.entrypoints {
					graphs = append(graphs, key)
				}
				slices.Sort(graphs)
				drafted, err := os.ReadFile(filepath.Join(dir, "release.yaml"))
				if err != nil {
					t.Fatalf("read the drafted Release: %v", err)
				}
				contract := imageContractVariables(t, string(drafted))
				// The variables the comment names only where the store publishes what
				// they carry, each with the value this application's store publishes,
				// empty where it publishes none.
				published := map[string]string{"LANGGRAPH_CHECKPOINTER_USER": a.user,
					"LANGGRAPH_CHECKPOINTER_DATABASE": a.database}
				for name := range published {
					if !slices.Contains(contract, name) {
						t.Errorf("the drafted Release's image comment does not name %s: %v", name, contract)
					}
				}
				result := pipelinetest.Build(t, pipelinetest.Input{
					ReleasePath:  writeGraphRelease(t, dir, local, target, graphs, a.datastore, a.component),
					CatalogPaths: langgraphBuildPaths(local),
				})
				for _, g := range graphs {
					rendered := helmVariableNames
					if target == "compose" {
						rendered = composeVariableNames
					}
					names := rendered(t, result.Artifacts, g)
					// Every variable the drafted Release's image comment names is one the
					// Recipe renders: the build attaches a credential on both connections,
					// so the key and the password render too, and nothing is left out —
					// save the user and the database of a store publishing none, which
					// the Recipe renders no variable for.
					for _, name := range contract {
						value, conditional := published[name]
						switch {
						case conditional && value == "" && slices.Contains(names, name):
							t.Errorf("%s's store publishes no value for %s, and the build renders it: %v", g, name, names)
						case (!conditional || value != "") && !slices.Contains(names, name):
							t.Errorf("the drafted Release says %s is handed %s, and the build renders no such variable: %v",
								g, name, names)
						}
					}
					// Every variable the Recipe renders under the LangGraph or OpenAI names is
					// one the comment names, so the comment leaves none of them out.
					for _, name := range names {
						if (strings.HasPrefix(name, "LANGGRAPH_") || strings.HasPrefix(name, "OPENAI_")) &&
							!slices.Contains(contract, name) {
							t.Errorf("the build hands %s %s, which the drafted Release's image comment does not name: %v",
								g, name, contract)
						}
					}
				}
				for _, g := range graphs {
					raw, ok := result.IRGraph.Spec.ValuesMapping.Get(g)
					if !ok {
						t.Fatalf("the compiled graph carries no values for %s", g)
					}
					var compiled map[string]any
					if err := json.Unmarshal(raw, &compiled); err != nil {
						t.Fatalf("the compiled values of %s do not decode: %v", g, err)
					}
					if compiled["graph_entrypoint"] != a.entrypoints[g] || compiled["recursion_limit"] != float64(25) {
						t.Errorf("%s compiles graph_entrypoint %#v and recursion_limit %#v, want %q and 25", g,
							compiled["graph_entrypoint"], compiled["recursion_limit"], a.entrypoints[g])
					}
					want := map[string]string{"LANGGRAPH_CHECKPOINTER_HOST": a.hosts[target],
						"LANGGRAPH_CHECKPOINTER_PORT": a.port, "OPENAI_BASE_URL": openAIEndpoint}
					for name, value := range published {
						if value != "" {
							want[name] = value
						}
					}
					for name, value := range want {
						if got := platformVariable(t, result.Artifacts, g, target, name); got != value {
							t.Errorf("%s is given %s=%q, want %q", g, name, got, value)
						}
					}
				}
			})
		}
	}
}
