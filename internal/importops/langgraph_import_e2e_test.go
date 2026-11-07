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
//     with a Release supplying each graph's image and the credential on its model
//     connection: each graph is given the address of the datastore its
//     checkpointer names, and the compiled graph carries its import path.
//
// The fixtures are described in testdata/langgraph/README.md.

import (
	"encoding/json"
	"os"
	"path/filepath"
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
// an instance of; and the address the hub's Recipe — or, for a drafted
// datastore, the scratch Recipe the build case writes — gives each graph.
var langgraphApplications = []struct {
	app                        string
	paths                      []string
	aggregateKeys, releaseKeys []string
	entrypoints                map[string]string
	datastore, component       string
	host, port                 string
}{
	{
		app:           "python",
		paths:         []string{"aggregate.yaml", "release.yaml"},
		aggregateKeys: []string{"triage", "support", "postgres"},
		releaseKeys:   []string{"triage", "support", "postgres"},
		entrypoints: map[string]string{"triage": "./src/support_desk/triage.py:make_graph",
			"support": "./src/support_desk/agent.py:graph"},
		datastore: "postgres", component: "core/datastore/postgres",
		host: "postgres-default", port: "5432",
	},
	{
		app:           "envfile",
		paths:         []string{"aggregate.yaml", "components/imported-datastore-mongodb.yaml", "release.yaml"},
		aggregateKeys: []string{"researcher", "mongodb"},
		releaseKeys:   []string{"researcher", "mongodb"},
		entrypoints:   map[string]string{"researcher": "./src/researcher.ts:graph"},
		datastore:     "mongodb", component: "imported/datastore/mongodb",
		host: scratchMongoHost, port: "27017",
	},
}

// TestEveryLangGraphApplicationIsRecognisedAndDraftedAsAgentsWiredToTheirDatastore
// imports each committed application directory, naming no format, and requires
// it read as the langgraph format — exactly one format recognised, so no other
// importer's signature matched any file the directory holds — and drafted as the
// Aggregate, the Release and, where the catalog holds no Component for the
// checkpointer's backend, that Component: an instance of core/ai-agent/langgraph
// per graph, in the order the configuration writes them, carrying its import path
// as graph_entrypoint and wired on checkpointer_db to the datastore, with no
// model connection; the Release an entry per graph, leaving its image, and one
// for the datastore, so every instance is placed.
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
			if !strings.Contains(release, "#   - infra.image, the container image running the application, which "+
				"langgraph build builds from its configuration\n") {
				t.Errorf("the Release does not leave the graphs' image for the Release:\n%s", release)
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

// scratchMongoRecipe is a reference Recipe realizing the drafted MongoDB on helm,
// publishing scratchMongoHost and port 27017: the Recipe the drafted Component's
// comment leaves for the author.
const scratchMongoRecipe = "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: mongodb-external\n" +
	"  version: 0.1.0\nspec:\n  capability: helm\n  implements: imported/datastore/mongodb:0.1.0\n  provides:\n" +
	"    host: \"" + scratchMongoHost + "\"\n    port: \"27017\"\n  workload: |\n" +
	"    apiVersion: aggrega.ai/v1alpha1\n    kind: Workload\n    form: reference\n" +
	"    endpoint: \"" + scratchMongoHost + "\"\n    port: 27017\n"

// langgraphBuildPaths are the catalog paths a drafted application is built
// against: the LangGraph agent, PostgreSQL and the OpenAI model with their
// Kubernetes Recipes, the schema library, the secret Feature a connection's
// credential is attached as with its realization, the provider Plugin entities,
// and local, the directory the case laid a drafted Component and its scratch
// Recipe in.
func langgraphBuildPaths(local string) []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog", local}
	for _, rel := range []string{
		"component/core/ai-agent/langgraph/1.0.0/component.yaml",
		"component/core/datastore/postgres/1.0.0/component.yaml",
		"component/core/ai-model/openai/1.0.0/component.yaml",
		"recipe/helm/langgraph/1.0.0/recipe.yaml",
		"recipe/helm/postgres/1.0.0/recipe.yaml",
		"recipe/helm/openai/1.0.0/recipe.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
	} {
		paths = append(paths, platformHub+"/"+rel)
	}
	return paths
}

// writeGraphRelease makes, beside the drafted Aggregate in dir, the author's act
// the draft leaves — an OpenAI model instance, gpt-4o, and each graph's model
// connection to it, every other line kept as drafted — and writes a Manifest
// realizing every Component on helm and a Release supplying only each graph's
// image and a credential on its model connection. A drafted datastore Component,
// written under dir's components/, is laid with the scratch Recipe in local. It
// returns the Release's path, which replaces the drafted Release.
func writeGraphRelease(t *testing.T, dir, local string, graphs []string, datastore, component string) string {
	t.Helper()
	aggregate := readDraftedAggregate(t, dir)
	wired := strings.ReplaceAll(aggregate, "      uses:\n",
		"      uses:\n        model:\n          gpt-4o:\n            target: gpt-4o\n")
	wired += "    gpt-4o:\n      type: core/ai-model/openai\n      values:\n        model: gpt-4o\n"
	if err := os.WriteFile(filepath.Join(dir, "aggregate.yaml"), []byte(wired), 0o644); err != nil {
		t.Fatalf("write the wired Aggregate: %v", err)
	}
	mapping := "core/datastore/postgres:1.0.0: helm/postgres:1.0.0"
	if component != "core/datastore/postgres" {
		body, err := os.ReadFile(filepath.Join(dir, "components", "imported-datastore-mongodb.yaml"))
		if err != nil {
			t.Fatalf("read the drafted datastore Component: %v", err)
		}
		importopstest.WriteSource(t, filepath.Join(local, "component", "imported", "datastore", "mongodb", "0.1.0",
			"component.yaml"), string(body))
		importopstest.WriteSource(t, filepath.Join(local, "recipe", "helm", "mongodb-external", "0.1.0", "recipe.yaml"),
			scratchMongoRecipe)
		mapping = component + ":0.1.0: helm/mongodb-external:0.1.0"
	}
	manifest := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: imported-langgraph\n" +
		"  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n" +
		"        core/ai-agent/langgraph:1.0.0: helm/langgraph:1.0.0\n        core/ai-model/openai:1.0.0: helm/openai:1.0.0\n" +
		"        " + mapping + "\n" +
		"    features:\n      type:\n        core/referenced-secret:1.0.0: helm/k8s-existing-secret:1.0.0\n"
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: imported-langgraph\n" +
		"  environment: production\n  version: \"1.0.0\"\nspec:\n  aggregate: imported/flow\n" +
		"  manifest: imported-langgraph\n  boundaries:\n    main:\n      directory: ./dist/helm\n      components:\n" +
		"        gpt-4o: {}\n        " + datastore + ": {}\n")
	for _, g := range graphs {
		b.WriteString("        " + g + ":\n          infra:\n            image: " + graphImage + "\n" +
			"          uses:\n            model:\n              gpt-4o:\n                features:\n" +
			"                  credential:\n                    type: core/referenced-secret\n" +
			"                    infra:\n                      origin: model-key\n")
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

// TestEachLangGraphDraftBuildsReachingTheDatastoreItsCheckpointerNames drafts
// each committed application and builds the draft on helm through the
// build-faithful harness, once the author's acts the draft leaves are made — a
// model instance each graph is wired to and, for a drafted datastore, a Recipe —
// with a Release supplying each graph's image and a credential on its model
// connection: each graph is given, as LANGGRAPH_CHECKPOINTER_HOST and _PORT, the
// address of the datastore its checkpointer names; and the compiled graph's
// values carry each graph's import path as graph_entrypoint, and recursion_limit
// at the Component's default, 25 — the hub's LangGraph Recipes render only the
// second, so the compiled graph is where both are read.
//
// 🔴 PLANTED DEFECT: in writeGraph (plugins/internal/langgraphimport/render.go),
// skip the uses block: the build halts naming the required checkpointer_db socket.
func TestEachLangGraphDraftBuildsReachingTheDatastoreItsCheckpointerNames(t *testing.T) {
	t.Parallel()
	for _, a := range langgraphApplications {
		t.Run(a.app, func(t *testing.T) {
			t.Parallel()
			_, dir := writtenDirectoryImport(t, langgraphWorkspace(t, nil), langgraphApplication(t, a.app), a.app)
			local := t.TempDir()
			var graphs []string
			for key := range a.entrypoints {
				graphs = append(graphs, key)
			}
			slices.Sort(graphs)
			result := pipelinetest.Build(t, pipelinetest.Input{
				ReleasePath:  writeGraphRelease(t, dir, local, graphs, a.datastore, a.component),
				CatalogPaths: langgraphBuildPaths(local),
			})
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
				for name, want := range map[string]string{"LANGGRAPH_CHECKPOINTER_HOST": a.host,
					"LANGGRAPH_CHECKPOINTER_PORT": a.port, "OPENAI_BASE_URL": openAIEndpoint} {
					if got := platformVariable(t, result.Artifacts, g, "helm", name); got != want {
						t.Errorf("%s is given %s=%q, want %q", g, name, got, want)
					}
				}
			}
		})
	}
}
