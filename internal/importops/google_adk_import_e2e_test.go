// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs of the reference Google ADK importer through the real
// Import, over a workspace whose catalog folder holds the reference importers
// and a JSON parser beside their entities, and the hub's ADK agent and Gemini
// model Components — every configuration is read through the YAML parser the
// binary carries, since the folder serves none:
//
//   - each committed application directory is recognised as the google-adk
//     format with no format named — so no other importer's signature matches it —
//     and, with its sub-agents' configurations handed under the importer's
//     with.sources, drafted as an Aggregate holding one ADK agent per agent tree,
//     wired to the Gemini model the tree names, and a Release;
//   - a directory hands the importer root_agent.yaml alone: with nothing under
//     with.sources each sub-agent's configuration is named as not handed, with
//     how to hand it;
//   - the application's code, its environment file and a stray YAML file are
//     never read: the application imports alike with each made unreadable, and so
//     does a CrewAI project beside an unreadable YAML file;
//   - by default the values a configuration hands its tools and its model
//     requests are carried as written into the Release and named in a warning,
//     and the empty_credentials setting at all writes each empty;
//   - two imports of one application are byte-identical;
//   - a configuration the parser refuses halts the import with the parser's
//     reason;
//   - each draft builds, with a Release supplying only the image and the
//     credential on the model connection, into a Deployment given the model's
//     key and a Service reaching the Gemini API's address.
//
// The fixtures are described in testdata/google-adk/README.md.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// googleADKFixtureDir holds the committed ADK applications, relative to this
// package.
const googleADKFixtureDir = "testdata/google-adk"

// googleADKHubEntities are the hub's entities the import's catalog view holds,
// by their path beneath platformHub: the ADK agent, the Gemini model its trees'
// model is served by, and the schema library their values read.
var googleADKHubEntities = []string{
	"component/core/ai-agent/google-adk/1.0.0/component.yaml",
	"component/core/ai-model/gemini/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// googleADKWorkspace is catalogFolderImportWorkspaceAttaching with
// googleADKHubEntities copied into the catalog folder at the paths the hub keeps
// them under.
func googleADKWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	copyHubEntities(t, folder.Dir, googleADKHubEntities)
	return rw
}

// googleADKWorkspaceHanding is googleADKWorkspace whose attachment of the
// Google ADK importer names under its with.sources the sub-agents'
// configurations of the committed application laid at dir, as the application
// a lists them.
func googleADKWorkspaceHanding(t *testing.T, settings map[string]any, dir string,
	subAgents []string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	sources := make([]string, 0, len(subAgents))
	for _, rel := range subAgents {
		sources = append(sources, filepath.Join(dir, filepath.FromSlash(rel)))
	}
	return workspaceHanding(t, "google-adk", settings, googleADKHubEntities, sources...)
}

// copyHubEntities copies the hub's entities at rels, paths beneath platformHub,
// into the catalog folder dir at the paths the hub keeps them under.
func copyHubEntities(t *testing.T, dir string, rels []string) {
	t.Helper()
	for _, rel := range rels {
		body, err := os.ReadFile(filepath.Join(platformHub, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read the hub's %s: %v", rel, err)
		}
		importopstest.WriteSource(t, filepath.Join(dir, filepath.FromSlash(rel)), string(body))
	}
}

// workspaceHanding lays a workspace whose catalog folder holds the reference
// importers and the hub's entities at hubEntities, every attachment handing
// settings, and the attachment of the importer serving format naming sources
// under its with.sources.
func workspaceHanding(t *testing.T, format string, settings map[string]any, hubEntities []string,
	sources ...string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	folder := importopstest.LayCatalogFolder(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), true)
	attachments := folder.Attachments(settings)
	served := folder.ImporterFor(t, format)
	for i := range attachments {
		if attachments[i].URI == served.Identity {
			attachments[i].Sources = sources
		}
	}
	importopstest.WriteConfiguration(t, wsDir, attachments...)
	copyHubEntities(t, folder.Dir, hubEntities)
	return resolveWorkspaceAt(t, wsDir)
}

// googleADKApplication lays the committed application named app down in a
// directory of its own.
func googleADKApplication(t *testing.T, app string) string {
	t.Helper()
	return layFixtureDirectory(t, googleADKFixtureDir, app)
}

// layFixtureDirectory lays the committed directory name beneath base down in a
// directory of its own, every file at its place, and returns the directory.
func layFixtureDirectory(t *testing.T, base, name string) string {
	t.Helper()
	src := filepath.Join(base, name)
	dst := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		importopstest.WriteSource(t, filepath.Join(dst, rel), string(body))
		return nil
	})
	if err != nil {
		t.Fatalf("lay the committed directory %s down: %v", name, err)
	}
	return dst
}

// googleADKApplications are the committed applications, each with what its
// draft holds: the instance keys of the Aggregate and of the Release, in the
// order each writes them; the tree's instance, its description as the
// configuration writes it and the model part it is wired to; the model that
// part serves; the sub-agents' configurations, by their path inside the
// application, which the attachment's with.sources hands; the configurations the
// Aggregate names as read; and the agents and tools its decisions report as
// becoming no Component, in order.
var googleADKApplications = []googleADKApp{
	{
		app:           "tutor",
		subAgents:     []string{"code_tutor_agent.yaml", "math_tutor_agent.yaml"},
		aggregateKeys: []string{"tutor-root", "gemini-flash-latest"},
		releaseKeys:   []string{"tutor-root", "gemini-flash-latest"},
		key:           "tutor-root",
		description:   "Learning assistant that tutors {{ students }} in code and math.",
		part:          "gemini-flash-latest",
		model:         "gemini-flash-latest",
		files:         []string{"root_agent.yaml", "code_tutor_agent.yaml", "math_tutor_agent.yaml"},
		agents:        []string{"code_tutor_agent", "math_tutor_agent", "tutor_agents.review_agent"},
		tools:         []string{"google_search", "tutor_tools.make_snippet_runner"},
	},
	{
		app:           "pipeline",
		subAgents:     []string{"agents/drafter.yaml", "agents/reviewer.yaml"},
		aggregateKeys: []string{"review-pipeline", "gemini-2-5-flash"},
		releaseKeys:   []string{"review-pipeline", "gemini-2-5-flash"},
		key:           "review-pipeline",
		description:   "Drafts a release note, then reviews it against the house style.",
		part:          "gemini-2-5-flash",
		model:         "gemini-2.5-flash",
		files:         []string{"root_agent.yaml", "drafter.yaml", "reviewer.yaml"},
		agents:        []string{"drafter", "reviewer"},
		tools:         []string{"review_tools.make_style_checker"},
	},
}

// googleADKApp is what the draft of one committed application holds, as
// googleADKApplications lists it.
type googleADKApp struct {
	app                        string
	aggregateKeys, releaseKeys []string
	key, description, part     string
	model                      string
	subAgents                  []string
	files, agents, tools       []string
}

// googleADKApplicationAt returns the committed application named app.
func googleADKApplicationAt(t *testing.T, app string) googleADKApp {
	t.Helper()
	for _, a := range googleADKApplications {
		if a.app == app {
			return a
		}
	}
	t.Fatalf("no committed application %s", app)
	return googleADKApp{}
}

// handedGoogleADKImport lays the committed application named app down, imports
// its directory naming no format, with the sub-agents' configurations named
// under the importer's with.sources and settings handed, writes the drafts, and
// returns the result and the directory they were written under.
func handedGoogleADKImport(t *testing.T, app string, settings map[string]any) (Result, string) {
	t.Helper()
	dir := googleADKApplication(t, app)
	return writtenDirectoryImport(t, googleADKWorkspaceHanding(t, settings, dir,
		googleADKApplicationAt(t, app).subAgents), dir, app)
}

// TestEveryGoogleADKApplicationIsRecognisedAndDraftedAsOneAgentPerTree imports
// each committed application directory, naming no format and with its
// sub-agents' configurations named under the importer's with.sources, and
// requires it read as the google-adk format — exactly one format recognised, so
// no other importer's signature matched any file the directory holds — and drafted as
// aggregate.yaml and release.yaml alone: one instance of
// core/ai-agent/google-adk for the tree, carrying its root agent's description,
// wired on its model socket to an instance of core/ai-model/gemini serving the
// model the tree names; the Release an entry for the instance, leaving its image
// for the Release, and one for the model, so both are placed. The Aggregate names
// the configurations read; every other
// agent and every tool is reported in a decision choosing nothing.
//
// 🔴 PLANTED DEFECT: in detect (plugins/google-adk-importer/main.go), write
// `{Path: "instruction", Is: "absent"}` in the first signature and
// `{Path: "agent_class", Is: "object"}` in the second: both rows are refused
// asking for the format, and the case fails on it.
func TestEveryGoogleADKApplicationIsRecognisedAndDraftedAsOneAgentPerTree(t *testing.T) {
	t.Parallel()
	for _, a := range googleADKApplications {
		t.Run(a.app, func(t *testing.T) {
			t.Parallel()
			res, dir := handedGoogleADKImport(t, a.app, nil)
			if res.Format != "google-adk" {
				t.Fatalf("the application was read as the %q format, want google-adk", res.Format)
			}
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			if got := draftPaths(res); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
				t.Errorf("the import drafted %v, want the Aggregate and the Release", got)
			}
			aggregateKeys, releaseKeys, _, release := mcpDrafts(t, dir)
			if !slices.Equal(aggregateKeys, a.aggregateKeys) || !slices.Equal(releaseKeys, a.releaseKeys) {
				t.Errorf("the Aggregate holds %v and the Release %v, want %v and %v", aggregateKeys, releaseKeys,
					a.aggregateKeys, a.releaseKeys)
			}
			if !strings.Contains(release, "#   - infra.image, the container image running the application built on "+
				"ADK, holding its configurations\n") {
				t.Errorf("the Release does not leave the image for the Release:\n%s", release)
			}

			draft, text := readCrewDraft(t, dir)
			c := draft.Spec.Components[a.key]
			if c.Type != "core/ai-agent/google-adk" {
				t.Errorf("%s is an instance of %q, want core/ai-agent/google-adk", a.key, c.Type)
			}
			if got := c.Uses["model"][a.part].Target; got != a.part {
				t.Errorf("%s's model connection %s targets %q, want %q", a.key, a.part, got, a.part)
			}
			if m := draft.Spec.Components[a.part]; m.Type != "core/ai-model/gemini" || m.Values["model"] != a.model {
				t.Errorf("the model part %s is %q serving %#v, want core/ai-model/gemini serving %q", a.part, m.Type,
					m.Values["model"], a.model)
			}
			for _, f := range a.files {
				if !strings.Contains(text, "#   - "+f+"\n") {
					t.Errorf("the Aggregate does not name %s as read:\n%s", f, text)
				}
			}
			var agents, tools []string
			for _, d := range res.Decisions {
				if d.Chosen != nil {
					continue
				}
				switch d.SourceNode.Type {
				case "agent":
					agents = append(agents, d.SourceNode.ID)
				case "tool":
					tools = append(tools, d.SourceNode.ID)
				}
			}
			if !slices.Equal(agents, a.agents) || !slices.Equal(tools, a.tools) {
				t.Errorf("the decisions report the agents %v and the tools %v as no Component, want %v and %v", agents,
					tools, a.agents, a.tools)
			}
		})
	}
}

// TestTheApplicationsCodeAndEnvironmentFileAreNeverRead imports the tutor
// application with its code, tutor_tools.py, and an environment file beside its
// root configuration, .env, both made unreadable, and requires the import to
// read the application as it does otherwise: recognised as google-adk, not
// halted, and drafting the same files. An import that opened either would be
// refused reading it.
//
// 🔴 PLANTED DEFECT: in detect (plugins/google-adk-importer/main.go), add `.env`
// to a signature's files: the directory read opens it, and the case fails
// refused.
func TestTheApplicationsCodeAndEnvironmentFileAreNeverRead(t *testing.T) {
	skipWhereNoFileIsUnreadable(t)
	t.Parallel()
	dir := googleADKApplication(t, "tutor")
	environment := importopstest.WriteSource(t, filepath.Join(dir, ".env"), "GOOGLE_GENAI_USE_VERTEXAI=0\n")
	makeUnreadable(t, environment)
	makeUnreadable(t, filepath.Join(dir, "tutor_tools.py"))
	res, err := importDirectory(t, googleADKWorkspace(t, nil), dir, "tutor", "")
	if err != nil {
		t.Fatalf("the import was refused with the code and the environment file unreadable: %v", err)
	}
	if res.Format != "google-adk" || res.Halted() {
		t.Fatalf("the application was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	if got := draftPaths(res); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
		t.Errorf("the import drafted %v, want the Aggregate and the Release", got)
	}
}

// TestADirectoryImportReadsTheRootAloneAndNamesItsSubAgentsAsNotHanded imports
// the tutor application's directory, naming no format and handing nothing under
// with.sources: the directory hands the importer root_agent.yaml alone, so the
// tree is drafted from its root — wired to the model it names — and each
// configuration its sub-agents name is reported as not handed, in the Aggregate
// and in its decision, with how to hand it; no value those configurations hold
// reaches anything, and lesson_plan.yaml, which no pattern selects, is not named.
//
// 🔴 PLANTED DEFECT: in detect (plugins/google-adk-importer/main.go), add back a
// signature naming `*.yaml` and `*.yml`: the directory hands the sub-agents'
// configurations, and the case fails on the not-handed lines.
func TestADirectoryImportReadsTheRootAloneAndNamesItsSubAgentsAsNotHanded(t *testing.T) {
	t.Parallel()
	dir := googleADKApplication(t, "tutor")
	res, out := writtenDirectoryImport(t, googleADKWorkspace(t, nil), dir, "tutor")
	if res.Format != "google-adk" || res.Halted() {
		t.Fatalf("the directory was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
	}
	text := readDraftedAggregate(t, out)
	for _, want := range []string{
		"# Google ADK agent configurations read:\n#   - root_agent.yaml\n# Not read: the application's code",
		"# Not read: a sub-agent of tutor_root, which names code_tutor_agent.yaml, which the import was not handed. " +
			"Name the file under the Google ADK importer's with.sources to have it read.\n",
		"# Not read: a sub-agent of tutor_root, which names math_tutor_agent.yaml, which the import was not handed. " +
			"Name the file under the Google ADK importer's with.sources to have it read.\n",
		"            target: gemini-flash-latest\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Aggregate does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "lesson_plan.yaml") {
		t.Errorf("a file no pattern selects is named, so it was handed:\n%s", text)
	}
	if d := decisionNamed(t, res.Decisions, "code_tutor_agent.yaml"); !strings.Contains(d.Rationale,
		"name the file under the Google ADK importer's with.sources to have it read") {
		t.Errorf("the not-handed sub-agent's decision does not say how to hand it: %q", d.Rationale)
	}
	for _, c := range []string{"credential-carried", "credential-emptied"} {
		if got := diagnosticsCoded(res.Diagnostics, c); len(got) != 0 {
			t.Errorf("a value of a configuration the import was not handed is named under %s: %q", c, got)
		}
	}
}

// TestNoDirectoryImportOpensAStrayYAMLFile imports, naming no format, the tutor
// application's directory with lesson_plan.yaml and a stray notes.yaml beside its
// root configuration both made unreadable, and a CrewAI project's directory with
// an unreadable stray notes.yaml: neither import is refused, since no importer's
// patterns select every YAML file, so recognition and the directory read open
// neither.
//
// 🔴 PLANTED DEFECT: in detect (plugins/google-adk-importer/main.go), add back a
// signature naming `*.yaml` and `*.yml`: recognition opens each YAML file, and
// both rows fail refused.
func TestNoDirectoryImportOpensAStrayYAMLFile(t *testing.T) {
	skipWhereNoFileIsUnreadable(t)
	t.Parallel()
	for _, row := range []struct {
		name, format string
		dir          func(t *testing.T) string
		workspace    func(t *testing.T) *workspaceconfig.ResolvedWorkspace
		unreadable   []string
	}{
		{"a Google ADK application", "google-adk", func(t *testing.T) string { return googleADKApplication(t, "tutor") },
			func(t *testing.T) *workspaceconfig.ResolvedWorkspace { return googleADKWorkspace(t, nil) },
			[]string{"lesson_plan.yaml", "notes.yaml"}},
		{"a CrewAI project", "crewai", func(t *testing.T) string { return crewaiProject(t, "classic") },
			func(t *testing.T) *workspaceconfig.ResolvedWorkspace { return crewaiWorkspace(t, nil) },
			[]string{"notes.yaml"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			dir := row.dir(t)
			for _, name := range row.unreadable {
				path := filepath.Join(dir, name)
				if _, err := os.Stat(path); err != nil {
					importopstest.WriteSource(t, path, "note: a file of the application no importer reads\n")
				}
				makeUnreadable(t, path)
			}
			res, err := importDirectory(t, row.workspace(t), dir, "application", "")
			if err != nil {
				t.Fatalf("the import was refused with a stray YAML file unreadable: %v", err)
			}
			if res.Format != row.format || res.Halted() || len(draftPaths(res)) == 0 {
				t.Fatalf("the directory was read as %q, halted %v, drafting %v: %+v", res.Format, res.Halted(),
					draftPaths(res), res.Diagnostics)
			}
		})
	}
}

// The key-shaped values the tutor application plants: the api_key a tool's args
// carries, and the header its math tutor's model requests send. Neither is a
// real key.
const (
	plantedADKToolArg = "AIzaPlantedAdkToolArgNeedle4f2c9b71"
	plantedADKHeader  = "adk-planted-header-needle-8e1d07a3"
)

// TestTheEmptyCredentialsSettingDecidesWhatAGoogleADKImportCarries imports the
// tutor application with the empty_credentials setting absent, none, marked and
// all, after a needle proves each planted value is in its configuration: where
// it is absent, none or marked, each is carried as written into release.yaml,
// reaches nothing else, and is named in a credential-carried warning by its agent
// and its field; where it is all, neither reaches anything and each is named in a
// credential-emptied warning. The numbers beside them carry no key and are named
// in neither.
//
// 🔴 PLANTED DEFECT: in located (plugins/internal/adkimport/adkimport.go), return
// nothing: the all row carries both values as written.
func TestTheEmptyCredentialsSettingDecidesWhatAGoogleADKImportCarries(t *testing.T) {
	t.Parallel()
	var source []byte
	for _, f := range []string{"code_tutor_agent.yaml", "math_tutor_agent.yaml"} {
		body, err := os.ReadFile(filepath.Join(googleADKFixtureDir, "tutor", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		source = append(source, body...)
	}
	fields := []string{"tools.tutor_tools.make_snippet_runner.args.api_key of node code_tutor_agent",
		"tools.tutor_tools.make_snippet_runner.args.sandbox of node code_tutor_agent",
		"generate_content_config.http_options.headers of node math_tutor_agent"}
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			var settings map[string]any
			if setting != "absent" {
				settings = map[string]any{"empty_credentials": setting}
			}
			res, dir := handedGoogleADKImport(t, "tutor", settings)
			planted := []string{plantedADKToolArg, plantedADKHeader}
			code := "credential-carried"
			if setting == "all" {
				code = "credential-emptied"
				assertNoneReaches(t, planted, source, res, dir)
			} else {
				assertCarriedOnlyInTheRelease(t, planted, source, res, dir)
			}
			named := diagnosticsCoded(res.Diagnostics, code)
			if len(named) != len(fields) {
				t.Fatalf("the %s warnings are %q, want one per field %q", code, named, fields)
			}
			for i, field := range fields {
				if !strings.Contains(named[i], field) {
					t.Errorf("the %s warning %q does not name %s", code, named[i], field)
				}
			}
			for _, d := range res.Diagnostics {
				if strings.Contains(d.Message, "timeout") {
					t.Errorf("a number is reported as a value that can carry a key: %+v", d)
				}
			}
		})
	}
}

// TestTwoImportsOfAGoogleADKApplicationAreByteIdentical imports each committed
// application twice, each into a workspace of its own, and requires the two to
// draft the same files and report the same decisions, byte for byte.
func TestTwoImportsOfAGoogleADKApplicationAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, a := range googleADKApplications {
		t.Run(a.app, func(t *testing.T) {
			t.Parallel()
			var runs []Result
			for range 2 {
				dir := googleADKApplication(t, a.app)
				res, err := importDirectory(t, googleADKWorkspaceHanding(t, nil, dir, a.subAgents), dir, a.app, "")
				if err != nil {
					t.Fatalf("an import was refused: %v", err)
				}
				runs = append(runs, res)
			}
			first, second := runs[0], runs[1]
			if len(first.Files) == 0 || len(first.Files) != len(second.Files) {
				t.Fatalf("the two imports drafted %d and %d files", len(first.Files), len(second.Files))
			}
			for i := range first.Files {
				if first.Files[i].Path != second.Files[i].Path || first.Files[i].Content != second.Files[i].Content {
					t.Errorf("the two imports differ at %s", first.Files[i].Path)
				}
			}
			x, _ := json.Marshal(first.Decisions)
			y, _ := json.Marshal(second.Decisions)
			if string(x) != string(y) {
				t.Errorf("the two imports report different decisions:\n%s\n%s", x, y)
			}
		})
	}
}

// TestAGoogleADKConfigurationTheParserRefusesHaltsTheImport imports the tutor
// application with a sub-agent's configuration, handed under the importer's
// with.sources, cut short: the root configuration still recognises the directory
// as google-adk, the YAML parser refuses the sub-agent's, and the import halts
// under parse-failed carrying the parser's reason.
//
// 🔴 PLANTED DEFECT: in reader.tree (plugins/internal/adkimport/agents.go), report
// a refused source under importer.NoNodes: the case fails on the code.
func TestAGoogleADKConfigurationTheParserRefusesHaltsTheImport(t *testing.T) {
	t.Parallel()
	dir := googleADKApplication(t, "tutor")
	importopstest.WriteSource(t, filepath.Join(dir, "math_tutor_agent.yaml"), "name: [cut short\n")
	res, err := importDirectory(t, googleADKWorkspaceHanding(t, nil, dir,
		googleADKApplicationAt(t, "tutor").subAgents), dir, "tutor", "")
	if err != nil {
		t.Fatalf("the import was refused before the importer ran: %v", err)
	}
	failed := diagnosticsCoded(res.Diagnostics, "parse-failed")
	if !res.Halted() || len(failed) != 1 ||
		!strings.Contains(failed[0], `could not parse source "math_tutor_agent.yaml" as google-adk: `) {
		t.Errorf("the import reported parse-failed %q (halted %v), want one naming math_tutor_agent.yaml with the "+
			"parser's reason", failed, res.Halted())
	}
}

// googleADKBuildPaths are the catalog paths a drafted application is built
// against: the ADK agent and the Gemini model with their Kubernetes Recipes, the
// schema library, the secret Feature a connection's credential is attached as
// with its realization, and the provider Plugin entities.
func googleADKBuildPaths() []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog"}
	for _, rel := range []string{
		"component/core/ai-agent/google-adk/1.0.0/component.yaml",
		"component/core/ai-model/gemini/1.0.0/component.yaml",
		"recipe/helm/google-adk/1.0.0/recipe.yaml",
		"recipe/helm/gemini/1.0.0/recipe.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
	} {
		paths = append(paths, platformHub+"/"+rel)
	}
	return paths
}

// writeGoogleADKRelease writes beside the drafted Aggregate in dir a Manifest
// realizing the agent and the model through their Kubernetes Recipes, and a
// Release supplying only what the draft leaves for the Release: the instance's
// image, and a credential on its model connection. It returns the Release's
// path, which replaces the drafted Release.
func writeGoogleADKRelease(t *testing.T, dir, key, part string) string {
	t.Helper()
	manifest := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: imported-google-adk\n" +
		"  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n" +
		"        core/ai-agent/google-adk:1.0.0: helm/google-adk:1.0.0\n        core/ai-model/gemini:1.0.0: helm/gemini:1.0.0\n" +
		"    features:\n      type:\n        core/referenced-secret:1.0.0: helm/k8s-existing-secret:1.0.0\n"
	release := "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: imported-google-adk\n" +
		"  environment: production\n  version: \"1.0.0\"\nspec:\n  aggregate: imported/flow\n" +
		"  manifest: imported-google-adk\n  boundaries:\n    main:\n      directory: ./dist/helm\n      components:\n" +
		"        " + part + ": {}\n" +
		"        " + key + ":\n          infra:\n            image: " + googleADKImage + "\n" +
		"          uses:\n            model:\n              " + part + ":\n                features:\n" +
		"                  credential:\n                    type: core/referenced-secret\n" +
		"                    infra:\n                      origin: gemini-key\n"
	for file, body := range map[string]string{"manifest.yaml": manifest, "release.yaml": release} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatalf("write the %s: %v", file, err)
		}
	}
	return filepath.Join(dir, "release.yaml")
}

// googleADKImage is the image the Release names for every instance.
const googleADKImage = "ghcr.io/example/adk-app:1.0.0"

// geminiAPIHost is the host of the Gemini API, which the hub's Gemini Recipe
// publishes.
const geminiAPIHost = "generativelanguage.googleapis.com"

// TestEachGoogleADKDraftBuildsReachingTheGeminiModel drafts each committed
// application and builds the draft on helm through the build-faithful harness,
// with a Release supplying the instance's image and a credential on its model
// connection and nothing else — the build halts naming the image where the
// Release names none, so its completing is the image's proof: the instance's
// Deployment is given GOOGLE_API_KEY from the connection's secret, which ADK's
// Gemini client sends; the rendered output reaches the Gemini API's host through
// the model's Service; and the compiled graph's values carry the tree's
// description as its configuration writes it — the hub's ADK Recipes render
// none of an agent's values, so the compiled graph is where it is read.
//
// 🔴 PLANTED DEFECT: in writeApplication (plugins/internal/adkimport/render.go),
// skip the uses block: the build halts naming the required model socket.
func TestEachGoogleADKDraftBuildsReachingTheGeminiModel(t *testing.T) {
	t.Parallel()
	for _, a := range googleADKApplications {
		t.Run(a.app, func(t *testing.T) {
			t.Parallel()
			_, dir := handedGoogleADKImport(t, a.app, nil)
			result := pipelinetest.Build(t, pipelinetest.Input{
				ReleasePath:  writeGoogleADKRelease(t, dir, a.key, a.part),
				CatalogPaths: googleADKBuildPaths(),
			})
			rendered := string(renderedEnding(t, result.Artifacts, "/helm/"+a.key+"-deployment.yaml"))
			if !strings.Contains(rendered, "name: GOOGLE_API_KEY") || !strings.Contains(rendered, "secretKeyRef") {
				t.Errorf("the instance is not given GOOGLE_API_KEY from a secret:\n%s", rendered)
			}
			reached := false
			for _, body := range result.Artifacts {
				if strings.Contains(string(body), geminiAPIHost) {
					reached = true
				}
			}
			if !reached {
				t.Errorf("no rendered file reaches %s", geminiAPIHost)
			}
			raw, ok := result.IRGraph.Spec.ValuesMapping.Get(a.key)
			if !ok {
				t.Fatalf("the compiled graph carries no values for %s", a.key)
			}
			var compiled map[string]any
			if err := json.Unmarshal(raw, &compiled); err != nil {
				t.Fatalf("the compiled values of %s do not decode: %v", a.key, err)
			}
			if compiled["description"] != a.description {
				t.Errorf("%s compiles its description as %#v, want %q", a.key, compiled["description"], a.description)
			}
		})
	}
}
