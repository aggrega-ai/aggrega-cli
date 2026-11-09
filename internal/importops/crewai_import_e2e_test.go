// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs of the reference CrewAI importer through the real Import,
// over a workspace whose catalog folder holds the reference importers and a JSON
// parser beside their entities, and the hub's CrewAI agent, OpenAI model and
// search tool Components — a classic project's YAML is read through the YAML
// parser the binary carries, and a JSON-first project's JSON with comments
// through the binary's parser for that syntax, since the folder serves neither:
//
//   - each committed project directory is recognised as the crewai format with no
//     format named — so no other importer's signature matches it — and drafted as
//     an Aggregate holding one CrewAI agent per declaration, wired to its model,
//     and a Release;
//   - the crew's program is never read: the project imports alike with its
//     program's files made unreadable;
//   - by default the members of an llm object are carried as written into the
//     Release and named in a warning, and the empty_credentials setting at all
//     writes each empty;
//   - two imports of one project are byte-identical;
//   - a declaration the parser refuses halts the import with the parser's reason;
//   - each draft builds, with a Release supplying only each agent's image and the
//     credential on its model connection, into Deployments reaching the model
//     the declarations name, the agents' values compiled as declared.
//
// The fixtures are described in testdata/crewai/README.md.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// crewaiFixtureDir holds the committed CrewAI projects, relative to this package.
const crewaiFixtureDir = "testdata/crewai"

// crewaiHubEntities are the hub's entities the import's catalog view holds, by
// their path beneath platformHub: the CrewAI agent, the OpenAI model its agents'
// llm names, a tool a declared tool's name matches, and the schema library their
// values read.
var crewaiHubEntities = []string{
	"component/core/ai-agent/crewai/1.0.0/component.yaml",
	"component/core/ai-model/openai/1.0.0/component.yaml",
	"component/core/ai-tool/search/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// crewaiWorkspace is catalogFolderImportWorkspaceAttaching with crewaiHubEntities
// copied into the catalog folder at the paths the hub keeps them under.
func crewaiWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	for _, rel := range crewaiHubEntities {
		body, err := os.ReadFile(filepath.Join(platformHub, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the hub's %s: %v", rel, err)
		}
		importopstest.WriteSource(t, filepath.Join(folder.Dir, filepath.FromSlash(rel)), string(body))
	}
	return rw
}

// crewaiProject lays the committed project named project down in a directory of
// its own, every file at its place in the project, and returns the directory.
func crewaiProject(t *testing.T, project string) string {
	t.Helper()
	return layFixtureDirectory(t, crewaiFixtureDir, project)
}

// writtenDirectoryImport imports the directory dir, named name, through rw,
// naming no format, and writes the drafts; it returns the result and the
// directory they were written under.
func writtenDirectoryImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, dir, name string) (Result, string) {
	t.Helper()
	outputDir := filepath.Join(rw.Root(), "drafts")
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    dir,
		SourceName:    name,
		OutputDir:     outputDir,
		Delivery:      authoredcontent.DeliveryFor(false),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("the import of %s was refused: %v", name, err)
	}
	if len(res.Written) == 0 {
		t.Fatal("the import wrote nothing")
	}
	return res, outputDir
}

// crewDraft is the part of a drafted Aggregate the cases read, as the compiler
// reads the YAML.
type crewDraft struct {
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

// readCrewDraft reads the Aggregate an import wrote under dir.
func readCrewDraft(t *testing.T, dir string) (crewDraft, string) {
	t.Helper()
	text := readDraftedAggregate(t, dir)
	var draft crewDraft
	if err := yaml.Unmarshal([]byte(text), &draft); err != nil {
		t.Fatalf("the drafted Aggregate does not read as YAML: %v\n%s", err, text)
	}
	return draft, text
}

// crewAgent is what a drafted agent is expected to carry.
type crewAgent struct {
	key, role, goal string
	delegation      bool
	logLevel, model string
}

// crewaiProjects are the committed projects, each with what its draft holds: the
// instance keys of the Aggregate and of the Release, in the order each writes
// them; each agent's values and the model part it is wired to; each model part's
// Component and model; the declaration files the Aggregate names as read; the
// tasks and the tools its decisions report, in order; and a member of a
// declaration the Aggregate names as not carried.
var crewaiProjects = []struct {
	project                    string
	aggregateKeys, releaseKeys []string
	agents                     []crewAgent
	models                     map[string]string
	files, tasks, tools        []string
	notCarried                 string
}{
	{
		project:       "classic",
		aggregateKeys: []string{"researcher", "brief-writer", "gpt-4o"},
		releaseKeys:   []string{"researcher", "brief-writer", "gpt-4o"},
		agents: []crewAgent{
			{"researcher", "{topic} Market Researcher\n",
				"Gather the facts about {topic} a buyer needs before a first meeting\n", false, "DEBUG", "gpt-4o"},
			{"brief-writer", "{topic} Brief Writer\n", "Turn the research notes into a one-page brief\n", true, "INFO",
				"gpt-4o"},
		},
		models:     map[string]string{"gpt-4o": "gpt-4o"},
		files:      []string{"src/market_brief/config/agents.yaml", "src/market_brief/config/tasks.yaml"},
		tasks:      []string{"research_task", "brief_task"},
		tools:      []string{"search_tool"},
		notCarried: "max_iter",
	},
	{
		project:       "jsonc",
		aggregateKeys: []string{"researcher", "analyst", "gpt-4o", "gpt-4o-mini"},
		releaseKeys:   []string{"researcher", "analyst", "gpt-4o", "gpt-4o-mini"},
		agents: []crewAgent{
			{"researcher", "{product} Change Researcher",
				"Find every change merged into {product} since the last release.", false, "DEBUG", "gpt-4o"},
			{"analyst", "{product} Release Writer",
				"Turn the list of changes into release notes a user reads in two minutes.", true, "INFO", "gpt-4o-mini"},
		},
		models:     map[string]string{"gpt-4o": "gpt-4o", "gpt-4o-mini": "gpt-4o-mini"},
		files:      []string{"agents/analyst.jsonc", "agents/researcher.jsonc", "crew.jsonc"},
		tasks:      []string{"collect", "write"},
		tools:      []string{"GithubSearchTool", "custom:changelog_reader"},
		notCarried: "settings.max_iter",
	},
	{
		project:       "json",
		aggregateKeys: []string{"researcher", "analyst", "gpt-4o", "gpt-4o-mini"},
		releaseKeys:   []string{"researcher", "analyst", "gpt-4o", "gpt-4o-mini"},
		agents: []crewAgent{
			{"researcher", "{product} Change Researcher",
				"Find every change merged into {product} since the last release.", false, "DEBUG", "gpt-4o"},
			{"analyst", "{product} Release Notes Writer",
				"Turn the list of changes into release notes a user reads in two minutes.", true, "INFO", "gpt-4o-mini"},
		},
		models:     map[string]string{"gpt-4o": "gpt-4o", "gpt-4o-mini": "gpt-4o-mini"},
		files:      []string{"agents/analyst.json", "agents/researcher.json", "crew.json"},
		tasks:      []string{"collect", "write"},
		tools:      []string{"GithubSearchTool", "custom:changelog_reader"},
		notCarried: "settings.max_iter",
	},
}

// TestEveryCrewAIProjectIsRecognisedAndDraftedAsAgentsWiredToTheirModels imports
// each committed project directory, naming no format, and requires it read as the
// crewai format — exactly one format recognised, so no other importer's signature
// matched any file the directory holds — and drafted as aggregate.yaml and
// release.yaml alone: an instance of core/ai-agent/crewai per agent, in the order
// the declarations write them, carrying its role, goal, allow_delegation and the
// log_level its verbose maps to, wired on its model socket to an instance of
// core/ai-model/openai serving the model its llm names; the Release an entry per
// agent, leaving its image for the Release, and one per model, so every instance
// is placed. The Aggregate names the files read and a member it does not carry; every
// tool and task is reported in a decision choosing nothing, the classic project's
// search tool carrying the hint naming core/ai-tool/search.
//
// 🔴 PLANTED DEFECT: in detect (plugins/crewai-importer/main.go), write
// `{Path: "agents", Is: "object"}` in the crew.jsonc signature and
// `{Path: "role", Is: "absent"}` in the agents/*.jsonc one: the jsonc row is
// refused asking for the format, and the case fails on it.
func TestEveryCrewAIProjectIsRecognisedAndDraftedAsAgentsWiredToTheirModels(t *testing.T) {
	t.Parallel()
	for _, p := range crewaiProjects {
		t.Run(p.project, func(t *testing.T) {
			t.Parallel()
			res, dir := writtenDirectoryImport(t, crewaiWorkspace(t, nil), crewaiProject(t, p.project), p.project)
			if res.Format != "crewai" {
				t.Fatalf("the project was read as the %q format, want crewai", res.Format)
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
				t.Errorf("the import drafted %v, want the Aggregate and the Release", got)
			}
			aggregateKeys, releaseKeys, _, release := mcpDrafts(t, dir)
			if !slices.Equal(aggregateKeys, p.aggregateKeys) || !slices.Equal(releaseKeys, p.releaseKeys) {
				t.Errorf("the Aggregate holds %v and the Release %v, want %v and %v", aggregateKeys, releaseKeys,
					p.aggregateKeys, p.releaseKeys)
			}
			if !strings.Contains(release, "#   - infra.image, the container image running the crew's program\n") {
				t.Errorf("the Release does not leave the agents' image for the Release:\n%s", release)
			}

			draft, text := readCrewDraft(t, dir)
			for _, a := range p.agents {
				c := draft.Spec.Components[a.key]
				if c.Type != "core/ai-agent/crewai" {
					t.Errorf("%s is an instance of %q, want core/ai-agent/crewai", a.key, c.Type)
				}
				for key, want := range map[string]any{"role": a.role, "goal": a.goal, "allow_delegation": a.delegation,
					"log_level": a.logLevel} {
					if got := c.Values[key]; got != want {
						t.Errorf("%s's %s is %#v, want %#v", a.key, key, got, want)
					}
				}
				if got := c.Uses["model"][a.model].Target; got != a.model {
					t.Errorf("%s's model connection %s targets %q, want %q", a.key, a.model, got, a.model)
				}
			}
			for key, model := range p.models {
				c := draft.Spec.Components[key]
				if c.Type != "core/ai-model/openai" || c.Values["model"] != model {
					t.Errorf("the model part %s is %q serving %#v, want core/ai-model/openai serving %q", key, c.Type,
						c.Values["model"], model)
				}
			}
			for _, f := range p.files {
				if !strings.Contains(text, "#   - "+f+"\n") {
					t.Errorf("the Aggregate does not name %s as read:\n%s", f, text)
				}
			}
			if !strings.Contains(text, p.notCarried) {
				t.Errorf("the Aggregate does not name %s as not carried:\n%s", p.notCarried, text)
			}

			var tasks, tools []string
			for _, d := range res.Decisions {
				switch d.SourceNode.Type {
				case "task", "tool":
					if d.Chosen != nil {
						t.Errorf("the %s %s is drafted as %+v; it becomes no Component", d.SourceNode.Type,
							d.SourceNode.ID, d.Chosen)
					}
					if d.SourceNode.Type == "task" {
						tasks = append(tasks, d.SourceNode.ID)
					} else {
						tools = append(tools, d.SourceNode.ID)
					}
				}
			}
			if !slices.Equal(tasks, p.tasks) || !slices.Equal(tools, p.tools) {
				t.Errorf("the decisions report the tasks %v and the tools %v, want %v and %v", tasks, tools, p.tasks,
					p.tools)
			}
			if p.project == "classic" {
				if d := decisionNamed(t, res.Decisions, "search_tool"); !strings.Contains(d.SwapHint,
					"core/ai-tool/search") {
					t.Errorf("the search tool's decision carries the hint %q, want one naming core/ai-tool/search",
						d.SwapHint)
				}
			}
		})
	}
}

// TestTheCrewsProgramIsNeverRead imports each committed project with every file
// of its program — the classic project's crew.py, the JSON-first project's custom
// tool — made unreadable, and requires the import to read the project as it does
// otherwise: recognised as crewai, not halted, and drafting the same files. An
// import that opened a file of the program would be refused reading it.
//
// 🔴 PLANTED DEFECT: in detect (plugins/crewai-importer/main.go), add a signature
// naming `*.py`: the directory read opens crew.py, and the case fails refused.
func TestTheCrewsProgramIsNeverRead(t *testing.T) {
	skipWhereNoFileIsUnreadable(t)
	t.Parallel()
	for project, program := range map[string]string{
		"classic": "src/market_brief/crew.py",
		"jsonc":   "tools/changelog_reader.py",
	} {
		t.Run(project, func(t *testing.T) {
			t.Parallel()
			dir := crewaiProject(t, project)
			makeUnreadable(t, filepath.Join(dir, filepath.FromSlash(program)))
			res, err := importDirectory(t, crewaiWorkspace(t, nil), dir, project, "")
			if err != nil {
				t.Fatalf("the import was refused with the program unreadable: %v", err)
			}
			if res.Format != "crewai" || res.Halted() {
				t.Fatalf("the project was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
				t.Errorf("the import drafted %v, want the Aggregate and the Release", got)
			}
		})
	}
}

// TestTheJSONCSpellingOfAnAgentWinsOverItsJSONSpelling imports the strict-JSON
// project's directory with the JSON-first project's analyst.jsonc laid beside its
// agents/analyst.json, naming no format: the directory is read as crewai, the
// analyst drafted is the .jsonc one — its role, and its key carried — the .json
// analyst's key reaches nothing, and the Aggregate names agents/analyst.json as
// not read.
//
// 🔴 PLANTED DEFECT: in readCrews (plugins/internal/crewaiimport/crews.go), drop
// the check skipping a .json file whose .jsonc twin was handed: two analysts are
// drafted, the first the .json one, and the case fails on the role.
func TestTheJSONCSpellingOfAnAgentWinsOverItsJSONSpelling(t *testing.T) {
	t.Parallel()
	dir := crewaiProject(t, "json")
	jsoncAnalyst, err := os.ReadFile(filepath.Join(crewaiFixtureDir, "jsonc", "agents", "analyst.jsonc"))
	if err != nil {
		t.Fatalf("read the JSON-first analyst: %v", err)
	}
	importopstest.WriteSource(t, filepath.Join(dir, "agents", "analyst.jsonc"), string(jsoncAnalyst))
	jsonAnalyst, err := os.ReadFile(filepath.Join(dir, "agents", "analyst.json"))
	if err != nil {
		t.Fatalf("read the strict-JSON analyst: %v", err)
	}
	res, out := writtenDirectoryImport(t, crewaiWorkspace(t, nil), dir, "json")
	if res.Format != "crewai" || res.Halted() {
		t.Fatalf("the project was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	draft, text := readCrewDraft(t, out)
	if got := draft.Spec.Components["analyst"].Values["role"]; got != "{product} Release Writer" {
		t.Errorf("the analyst's role is %#v, want the one agents/analyst.jsonc declares", got)
	}
	if _, twice := draft.Spec.Components["analyst-2"]; twice {
		t.Errorf("the analyst is drafted twice:\n%s", text)
	}
	if !strings.Contains(text, "written in both spellings: agents/analyst.json.\n") {
		t.Errorf("the Aggregate does not name agents/analyst.json as not read:\n%s", text)
	}
	assertNoneReaches(t, []string{"sk-planted-crewai-json-needle-3a90b1e5"}, jsonAnalyst, res, out)
	assertCarriedOnlyInTheRelease(t, []string{plantedCrewAIKey}, jsoncAnalyst, res, out)
}

// plantedCrewAIKey is the key-shaped value the JSON-first project plants under
// api_key in the analyst's llm object. It is no real key.
const plantedCrewAIKey = "sk-planted-crewai-llm-needle-7c41e9d2"

// TestTheEmptyCredentialsSettingDecidesWhatACrewAIImportCarries imports the
// JSON-first project with the empty_credentials setting absent, none, marked and
// all, after a needle proves the planted value is in the analyst's declaration:
// where it is absent, none or marked, the value is carried as written into
// release.yaml, reaches nothing else, and is named in a credential-carried warning
// by its agent and its member; where it is all, it reaches nothing and is named
// in a credential-emptied warning. The temperature beside it, a number, carries
// no key and is named in neither.
//
// 🔴 PLANTED DEFECT: in located (plugins/internal/crewaiimport/crewaiimport.go),
// return nothing: the all row carries the key as written.
func TestTheEmptyCredentialsSettingDecidesWhatACrewAIImportCarries(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile(filepath.Join(crewaiFixtureDir, "jsonc", "agents", "analyst.jsonc"))
	if err != nil {
		t.Fatalf("read the analyst's declaration: %v", err)
	}
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			var settings map[string]any
			if setting != "absent" {
				settings = map[string]any{"empty_credentials": setting}
			}
			res, dir := writtenDirectoryImport(t, crewaiWorkspace(t, settings), crewaiProject(t, "jsonc"), "jsonc")
			planted := []string{plantedCrewAIKey}
			code := "credential-carried"
			if setting == "all" {
				code = "credential-emptied"
				assertNoneReaches(t, planted, source, res, dir)
			} else {
				assertCarriedOnlyInTheRelease(t, planted, source, res, dir)
			}
			named := diagnosticsCoded(res.Diagnostics, code)
			if len(named) != 1 || !strings.Contains(named[0], "llm.api_key of node analyst") {
				t.Errorf("the %s warnings are %q, want one naming llm.api_key of node analyst", code, named)
			}
			for _, d := range res.Diagnostics {
				if strings.Contains(d.Message, "temperature") {
					t.Errorf("a number is reported as a value that can carry a key: %+v", d)
				}
			}
		})
	}
}

// TestTwoImportsOfACrewAIProjectAreByteIdentical imports each committed project
// twice, each into a workspace of its own, and requires the two to draft the same
// files and report the same decisions, byte for byte.
func TestTwoImportsOfACrewAIProjectAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, p := range crewaiProjects {
		t.Run(p.project, func(t *testing.T) {
			t.Parallel()
			first, err := importDirectory(t, crewaiWorkspace(t, nil), crewaiProject(t, p.project), p.project, "")
			if err != nil {
				t.Fatalf("the first import was refused: %v", err)
			}
			second, err := importDirectory(t, crewaiWorkspace(t, nil), crewaiProject(t, p.project), p.project, "")
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

// TestACrewAIDeclarationTheParserRefusesHaltsTheImport imports the JSON-first
// project with its crew.jsonc cut short: the agents' files still recognise the
// directory as crewai, the parser for JSON with comments refuses crew.jsonc, and
// the import halts under parse-failed carrying the parser's reason.
//
// 🔴 PLANTED DEFECT: in readCrews (plugins/internal/crewaiimport/crews.go), report
// a refused source under importer.NoNodes: the case fails on the code.
func TestACrewAIDeclarationTheParserRefusesHaltsTheImport(t *testing.T) {
	t.Parallel()
	dir := crewaiProject(t, "jsonc")
	importopstest.WriteSource(t, filepath.Join(dir, "crew.jsonc"), "{\n  // cut short\n  \"agents\": [\n")
	res, err := importDirectory(t, crewaiWorkspace(t, nil), dir, "jsonc", "")
	if err != nil {
		t.Fatalf("the import was refused before the importer ran: %v", err)
	}
	failed := diagnosticsCoded(res.Diagnostics, "parse-failed")
	if !res.Halted() || len(failed) != 1 || !strings.Contains(failed[0], `could not parse source "crew.jsonc" as crewai: `) {
		t.Errorf("the import reported parse-failed %q (halted %v), want one naming crew.jsonc with the parser's reason",
			failed, res.Halted())
	}
}

// crewaiBuildPaths are the catalog paths a drafted crew is built against: the
// CrewAI agent and the OpenAI model with their Kubernetes Recipes, the schema
// library, the secret Feature a connection's credential is attached as with its
// realization, and the provider Plugin entities.
func crewaiBuildPaths() []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog"}
	for _, rel := range []string{
		"component/core/ai-agent/crewai/1.0.0/component.yaml",
		"component/core/ai-model/openai/1.0.0/component.yaml",
		"recipe/helm/crewai/1.0.0/recipe.yaml",
		"recipe/helm/openai/1.0.0/recipe.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
	} {
		paths = append(paths, platformHub+"/"+rel)
	}
	return paths
}

// writeCrewRelease writes beside the drafted Aggregate in dir a Manifest
// realizing the agent and the model through their Kubernetes Recipes, and a
// Release supplying only what the draft leaves for the Release: each agent's
// image, and a credential on each agent's model connection. It returns the
// Release's path, which replaces the drafted Release.
func writeCrewRelease(t *testing.T, dir string, agents []crewAgent, models []string) string {
	t.Helper()
	manifest := "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: imported-crewai\n" +
		"  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n" +
		"        core/ai-agent/crewai:1.0.0: helm/crewai:1.0.0\n        core/ai-model/openai:1.0.0: helm/openai:1.0.0\n" +
		"    features:\n      type:\n        core/referenced-secret:1.0.0: helm/k8s-existing-secret:1.0.0\n"
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: imported-crewai\n" +
		"  environment: production\n  version: \"1.0.0\"\nspec:\n  aggregate: imported/flow\n" +
		"  manifest: imported-crewai\n  boundaries:\n    main:\n      directory: ./dist/helm\n      components:\n")
	for _, m := range models {
		b.WriteString("        " + m + ": {}\n")
	}
	for _, a := range agents {
		b.WriteString("        " + a.key + ":\n          infra:\n            image: " + crewImage + "\n" +
			"          uses:\n            model:\n              " + a.model + ":\n                features:\n" +
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

// crewImage is the image the Release names for every agent.
const crewImage = "ghcr.io/example/crew:1.0.0"

// TestEachCrewAIDraftBuildsReachingTheModelItsDeclarationsName drafts each
// committed project and builds the draft on helm through the build-faithful
// harness, with a Release supplying each agent's image and a credential on its
// model connection and nothing else — the build halts naming the image where the
// Release names none, so its completing is the image's proof: each agent is given,
// as OPENAI_BASE_URL and MODEL, the endpoint and the model of the part its
// declaration's llm wired it to; and the compiled
// graph's values carry each agent's role, goal, allow_delegation and log_level as
// drafted — the hub's CrewAI Recipes render none of an agent's values, so the
// compiled graph is where they are read.
//
// 🔴 PLANTED DEFECT: in writeAgent (plugins/internal/crewaiimport/render.go), skip
// the uses block: the build halts naming the required model socket.
func TestEachCrewAIDraftBuildsReachingTheModelItsDeclarationsName(t *testing.T) {
	t.Parallel()
	for _, p := range crewaiProjects {
		t.Run(p.project, func(t *testing.T) {
			t.Parallel()
			_, dir := writtenDirectoryImport(t, crewaiWorkspace(t, nil), crewaiProject(t, p.project), p.project)
			var models []string
			for key := range p.models {
				models = append(models, key)
			}
			slices.Sort(models)
			result := pipelinetest.Build(t, pipelinetest.Input{
				ReleasePath:  writeCrewRelease(t, dir, p.agents, models),
				CatalogPaths: crewaiBuildPaths(),
			})
			for _, a := range p.agents {
				raw, ok := result.IRGraph.Spec.ValuesMapping.Get(a.key)
				if !ok {
					t.Fatalf("the compiled graph carries no values for %s", a.key)
				}
				var compiled map[string]any
				if err := json.Unmarshal(raw, &compiled); err != nil {
					t.Fatalf("the compiled values of %s do not decode: %v", a.key, err)
				}
				for key, want := range map[string]any{"role": a.role, "goal": a.goal, "allow_delegation": a.delegation,
					"log_level": a.logLevel} {
					if compiled[key] != want {
						t.Errorf("%s compiles %s as %#v, want %#v", a.key, key, compiled[key], want)
					}
				}
				for name, want := range map[string]string{"OPENAI_BASE_URL": openAIEndpoint, "MODEL": p.models[a.model]} {
					if got := platformVariable(t, result.Artifacts, a.key, "helm", name); got != want {
						t.Errorf("%s is given %s=%q, want %q", a.key, name, got, want)
					}
				}
			}
		})
	}
}
