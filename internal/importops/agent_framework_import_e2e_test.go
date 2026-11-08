// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end proofs of the reference Agent Framework importer through the real
// Import, over a workspace whose catalog folder holds the reference importers
// and a JSON parser beside their entities, and the hub's Agent Framework agent,
// OpenAI model and MCP tool Components — every declarative agent is read through
// the YAML parser the binary carries, since the folder serves none:
//
//   - each committed declarative agent, imported as a lone file under a name of
//     its author's choosing, is recognised as the agent-framework format with no
//     format named — so no other importer's signature matches it — and drafted as
//     an Aggregate holding one Agent Framework agent, wired to the OpenAI model
//     it names and to nothing else, each MCP server it names written in a
//     comment, since the model's provider reaches it, and a Release;
//   - a directory is never handed to the importer: one naming the format is
//     refused under its own condition before any file is opened, and one naming
//     no format recognises nothing;
//   - by default the literal values a declarative agent states for its model and
//     its MCP servers are carried as written into the Release and named in a
//     warning, an MCP server's url whole, its query included; the
//     empty_credentials setting at marked writes its keys and its MCP server's
//     url empty, and at all its model's endpoint too; a reference to an
//     environment variable is named as written under every setting, with when
//     Agent Framework reads it;
//   - two imports of one declarative agent are byte-identical;
//   - a declarative agent the parser refuses halts the import with the parser's
//     reason;
//   - the learn agent's draft builds, with its Release supplying only what it
//     leaves for the Release, into a Deployment given the model's endpoint and no
//     variable for the MCP server, and a Service reaching the model alone.
//
// The fixtures are described in testdata/agent-framework/README.md.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// agentFrameworkFixtureDir holds the committed declarative agents, relative to
// this package.
const agentFrameworkFixtureDir = "testdata/agent-framework"

// agentFrameworkHubEntities are the hub's entities the import's catalog view
// holds, by their path beneath platformHub: the Agent Framework agent, the OpenAI
// model and the MCP tool its drafts are instances of, and the schema library
// their values read.
var agentFrameworkHubEntities = []string{
	"component/core/ai-agent/agent-framework/1.0.0/component.yaml",
	"component/core/ai-model/openai/1.0.0/component.yaml",
	"component/core/ai-tool/mcp/1.0.0/component.yaml",
	"schemalibrary/base-components/1.0.0/library.yaml",
}

// agentFrameworkWorkspace is catalogFolderImportWorkspaceAttaching with
// agentFrameworkHubEntities copied into the catalog folder at the paths the hub
// keeps them under.
func agentFrameworkWorkspace(t *testing.T, settings map[string]any) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	rw, folder := catalogFolderImportWorkspaceAttaching(t, settings)
	copyHubEntities(t, folder.Dir, agentFrameworkHubEntities)
	return rw
}

// readAgentFrameworkFixture reads one committed declarative agent.
func readAgentFrameworkFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(agentFrameworkFixtureDir, name))
	if err != nil {
		t.Fatalf("read the committed fixture %s: %v", name, err)
	}
	return body
}

// agentFrameworkAgents are the committed declarative agents, each with the name
// an import is handed it under — one its author chose, as Agent Framework loads
// a declarative agent from whatever file its application names — and what its
// draft holds: the instance keys of the Aggregate and of the Release, in the order
// each writes them; the agent's instance and the model part it is wired to; the
// MCP servers it names, by name, each written in the comment above the agent and
// drafted as no instance; and the decisions choosing nothing — the MCP servers'
// and the other tools' among them.
var agentFrameworkAgents = []struct {
	fixture, name              string
	aggregateKeys, releaseKeys []string
	key, part                  string
	servers, unchosen          []string
}{
	{fixture: "learn-agent.yaml", name: "agents/learn.yaml",
		aggregateKeys: []string{"learnagent", "gpt-4-1-mini"}, releaseKeys: []string{"learnagent", "gpt-4-1-mini"},
		key: "learnagent", part: "gpt-4-1-mini", servers: []string{"microsoft_learn"},
		unchosen: []string{"LearnAgent#microsoft_learn", "LearnAgent#GetRelease", "LearnAgent#search"}},
	{fixture: "docs-agent.yaml", name: "docs.agent.yaml",
		aggregateKeys: []string{"docsagent", "openai-model"}, releaseKeys: []string{"docsagent", "openai-model"},
		key: "docsagent", part: "openai-model", servers: []string{"team docs"},
		unchosen: []string{"DocsAgent#team docs"}},
	{fixture: "research-agent.yaml", name: "research.yml",
		aggregateKeys: []string{"researchagent"}, releaseKeys: []string{"researchagent"},
		key: "researchagent", servers: []string{"github"},
		unchosen: []string{"ResearchAgent#model", "ResearchAgent#github", "ResearchAgent#python"}},
}

// TestEveryAgentFrameworkDeclarativeAgentIsRecognisedAndDrafted imports each
// committed declarative agent as a lone file, naming no format, and requires it
// read as the agent-framework format — exactly one format recognised, so no
// other importer's signature matched it — and drafted as aggregate.yaml and
// release.yaml alone: one instance of core/ai-agent/agent-framework, wired on its
// model socket to an instance of core/ai-model/openai where its provider is
// OpenAI and on no other socket; each MCP server it names drafted as no instance
// and named in the comment above the agent, which says the model's provider
// reaches it; the Release an entry for the agent, leaving its image for the
// Release, and one for each model part, so every instance is placed. Every MCP
// server and every other tool is reported in a decision choosing nothing.
//
// 🔴 PLANTED DEFECT: in detect (plugins/agent-framework-importer/main.go), write
// `{Path: "model", Is: "string"}`: every row is refused asking for the format, and
// the case fails on it.
func TestEveryAgentFrameworkDeclarativeAgentIsRecognisedAndDrafted(t *testing.T) {
	t.Parallel()
	for _, a := range agentFrameworkAgents {
		t.Run(a.fixture, func(t *testing.T) {
			t.Parallel()
			res, dir := writtenImport(t, agentFrameworkWorkspace(t, nil), a.name, readAgentFrameworkFixture(t, a.fixture))
			if res.Format != "agent-framework" {
				t.Fatalf("the declarative agent was read as the %q format, want agent-framework", res.Format)
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
				"Agent Framework, holding its declarative agent\n") {
				t.Errorf("the Release does not leave the image for the Release:\n%s", release)
			}
			draft, text := readCrewDraft(t, dir)
			agent := draft.Spec.Components[a.key]
			if agent.Type != "core/ai-agent/agent-framework" {
				t.Errorf("%s is an instance of %q, want core/ai-agent/agent-framework", a.key, agent.Type)
			}
			if a.part != "" {
				if got := agent.Uses["model"][a.part].Target; got != a.part {
					t.Errorf("%s's model connection %s targets %q:\n%s", a.key, a.part, got, text)
				}
				if m := draft.Spec.Components[a.part]; m.Type != "core/ai-model/openai" {
					t.Errorf("the model part %s is %q, want core/ai-model/openai", a.part, m.Type)
				}
			} else if _, wired := agent.Uses["model"]; wired {
				t.Errorf("%s is wired to a model its declarative agent's provider is not served by:\n%s", a.key, text)
			}
			if _, wired := agent.Uses["tools"]; wired {
				t.Errorf("%s is wired on its tools socket, which the model's provider, not the application, "+
					"reaches its MCP servers through:\n%s", a.key, text)
			}
			for _, s := range a.servers {
				if want := "# The MCP server " + s + ", drafted as no Component and no connection, since Agent " +
					"Framework sends it to the model's provider as a hosted tool, and the provider, not the " +
					"application, reaches it at its url"; !strings.Contains(text, want) {
					t.Errorf("the Aggregate does not say %q:\n%s", want, text)
				}
			}
			var unchosen []string
			for _, d := range res.Decisions {
				if d.Chosen == nil {
					unchosen = append(unchosen, d.SourceNode.ID)
				}
			}
			if !slices.Equal(unchosen, a.unchosen) {
				t.Errorf("the decisions choosing nothing are %v, want %v", unchosen, a.unchosen)
			}
		})
	}
}

// TestADirectoryIsNeverHandedToTheAgentFrameworkImporter imports a directory
// holding the committed declarative agents and nothing any importer's patterns
// select, each made unreadable: naming the agent-framework format, it is refused
// under the condition of an importer declaring no file pattern, before any file
// is opened; naming none, no signature recognises it, and nothing was opened
// either, since a signature naming no file applies to none of a directory's.
//
// 🔴 PLANTED DEFECT: in detect (plugins/agent-framework-importer/main.go), name
// `*.yaml` in the signature's files: both imports open the unreadable files, and
// the case fails refused reading one.
func TestADirectoryIsNeverHandedToTheAgentFrameworkImporter(t *testing.T) {
	skipWhereNoFileIsUnreadable(t)
	t.Parallel()
	rw := agentFrameworkWorkspace(t, nil)
	dir := filepath.Join(t.TempDir(), "agents")
	for _, a := range agentFrameworkAgents {
		path := importopstest.WriteSource(t, filepath.Join(dir, a.fixture), string(readAgentFrameworkFixture(t, a.fixture)))
		makeUnreadable(t, path)
	}
	_, err := importDirectory(t, rw, dir, "agents", "agent-framework")
	if !errors.Is(err, ErrImporterDeclaresNoFilePatterns()) {
		t.Fatalf("the directory naming the format was not refused as one the importer cannot read: %v", err)
	}
	if short, _ := cerrors.ShortCodeOf(err); short != "E0341" {
		t.Errorf("the refusal carries short code %q, want E0341", short)
	}
	_, err = importDirectory(t, rw, dir, "agents", "")
	var undetected *UndetectedFormatError
	if !errors.As(err, &undetected) {
		t.Errorf("the directory naming no format was not refused as unrecognised: %v", err)
	}
}

// TestTheEmptyCredentialsSettingDecidesWhatAnAgentFrameworkImportCarries imports
// the learn agent with the empty_credentials setting absent, none, marked and
// all, after a needle proves each planted value is in its declarative agent:
// where it is absent or none, the model's key, the MCP server's key and the
// session the server's url carries in its query are carried as written into
// release.yaml, reach nothing else, and are named in a credential-carried
// warning, the url whole; where it is marked or all, none reaches anything, and
// each is named in a credential-emptied warning, the url whole; the model's
// endpoint is carried under marked and emptied under all; each report opens with
// the exact words the kit gives its class and its fate. The docs agent's
// references are named by their variables, and reported under neither code,
// whatever the setting says.
//
// 🔴 PLANTED DEFECT: in located (plugins/internal/agentframeworkimport/agentframeworkimport.go),
// give every value the class importer.CredentialUnmarked: the marked row carries
// both keys as written.
func TestTheEmptyCredentialsSettingDecidesWhatAnAgentFrameworkImportCarries(t *testing.T) {
	t.Parallel()
	learn := readAgentFrameworkFixture(t, "learn-agent.yaml")
	docs := readAgentFrameworkFixture(t, "docs-agent.yaml")
	const (
		plantedModelKey = "sk-planted-af-model-key-needle-3c7e91d2"
		plantedToolKey  = "planted-af-tool-key-needle-6d02f5"
		plantedQuery    = "planted-af-query-needle-a41b"
	)
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			var settings map[string]any
			if setting != "absent" {
				settings = map[string]any{"empty_credentials": setting}
			}
			emptied := setting == "marked" || setting == "all"
			res, dir := writtenImport(t, agentFrameworkWorkspace(t, settings), "learn.yaml", learn)
			planted := []string{plantedModelKey, plantedToolKey, plantedQuery}
			if emptied {
				assertNoneReaches(t, planted, learn, res, dir)
			} else {
				assertCarriedOnlyInTheRelease(t, planted, learn, res, dir)
			}
			// Each value is reported once, in the order the draft writes them, under the
			// message the kit words its class and its fate with.
			carried := func(f string) string {
				return "credential-carried: the value of " + f + " of node LearnAgent is carried as written "
			}
			emptiedWhole := func(f string) string {
				return "credential-emptied: the whole value of " + f + " of node LearnAgent is written empty "
			}
			emptiedMarked := func(f string) string {
				return "credential-emptied: the value of " + f + ", which node LearnAgent marks as a credential, is " +
					"written empty "
			}
			const (
				modelKey = "model.connection.key"
				toolKey  = "tools.microsoft_learn.connection.key"
				endpoint = "model.connection.endpoint"
				url      = "tools.microsoft_learn.url"
			)
			// The MCP server's url is one value, written whole, its query included:
			// the draft holds no instance of the server for an address to be split
			// off into.
			want := []string{carried(modelKey), carried(url), carried(toolKey), carried(endpoint)}
			switch setting {
			case "marked":
				want = []string{emptiedMarked(modelKey), emptiedWhole(url), emptiedMarked(toolKey), carried(endpoint)}
			case "all":
				want = []string{emptiedMarked(modelKey), emptiedWhole(url), emptiedMarked(toolKey), emptiedWhole(endpoint)}
			}
			var reported []string
			for _, d := range res.Diagnostics {
				if d.Code == "credential-carried" || d.Code == "credential-emptied" {
					reported = append(reported, d.Code+": "+d.Message)
				}
			}
			if len(reported) != len(want) {
				t.Fatalf("the import reports %q, want one report per value opening %q", reported, want)
			}
			for i := range want {
				if !strings.HasPrefix(reported[i], want[i]) {
					t.Errorf("report %d is %q, want it to open %q", i, reported[i], want[i])
				}
			}

			res, dir = writtenImport(t, agentFrameworkWorkspace(t, settings), "docs.yaml", docs)
			release, err := os.ReadFile(filepath.Join(dir, "release.yaml"))
			if err != nil {
				t.Fatalf("read the drafted Release: %v", err)
			}
			// A reference is read from the environment only under the setting and the
			// runtime the comment names, and the comment says so.
			condition := " only where the application loads the declarative agent with AgentFactory(safe_mode=False), " +
				"Agent Framework's own security setting, and a .NET runtime is present; otherwise the value is the " +
				"expression's text as written\n"
			for _, want := range []string{
				"#   - model.connection.key, written as =Env.OPENAI_API_KEY, which Agent Framework reads from the " +
					"environment variable OPENAI_API_KEY" + condition,
				"#   - tools.team docs.url, written as =Env.DOCS_MCP_URL, which Agent Framework reads from the " +
					"environment variable DOCS_MCP_URL" + condition,
			} {
				if !strings.Contains(string(release), want) {
					t.Errorf("the docs agent's Release does not say %q:\n%s", want, release)
				}
			}
			if got := append(diagnosticsCoded(res.Diagnostics, "credential-carried"),
				diagnosticsCoded(res.Diagnostics, "credential-emptied")...); len(got) != 0 {
				t.Errorf("a reference, which holds no value, is reported: %q", got)
			}
		})
	}
}

// TestTwoImportsOfADeclarativeAgentAreByteIdentical imports each committed
// declarative agent twice, each into a workspace of its own, and requires the two
// to draft the same files and report the same decisions, byte for byte.
func TestTwoImportsOfADeclarativeAgentAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, a := range agentFrameworkAgents {
		t.Run(a.fixture, func(t *testing.T) {
			t.Parallel()
			body := readAgentFrameworkFixture(t, a.fixture)
			first, _ := writtenImport(t, agentFrameworkWorkspace(t, nil), a.name, body)
			second, _ := writtenImport(t, agentFrameworkWorkspace(t, nil), a.name, body)
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

// TestADeclarativeAgentTheParserRefusesHaltsTheImport imports a declarative agent
// cut short, naming its format: the YAML parser refuses it, and the import halts
// under parse-failed carrying the parser's reason.
//
// 🔴 PLANTED DEFECT: in Import (plugins/internal/agentframeworkimport/agentframeworkimport.go),
// report a refused source under importer.NoNodes: the case fails on the code.
func TestADeclarativeAgentTheParserRefusesHaltsTheImport(t *testing.T) {
	t.Parallel()
	path := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "cut.yaml"), "kind: Prompt\nname: [cut short\n")
	res, err := importDirectory(t, agentFrameworkWorkspace(t, nil), path, "cut.yaml", "agent-framework")
	if err != nil {
		t.Fatalf("the import was refused before the importer ran: %v", err)
	}
	failed := diagnosticsCoded(res.Diagnostics, "parse-failed")
	if !res.Halted() || len(failed) != 1 || !strings.Contains(failed[0], "as agent-framework: ") {
		t.Errorf("the import reported parse-failed %q (halted %v), want one carrying the parser's reason", failed,
			res.Halted())
	}
}

// agentFrameworkBuildPaths are the catalog paths a drafted declarative agent is
// built against: the Agent Framework agent, the OpenAI model and the MCP tool with
// their Kubernetes Recipes — the MCP tool's realizing a server someone else runs,
// so a draft holding an instance of it would render one — the schema library,
// the secret Feature a connection's credential is attached as with its
// realization, and the provider Plugin entities.
func agentFrameworkBuildPaths() []string {
	paths := []string{"../compiler/testdata/flow-definition-escape/catalog"}
	for _, rel := range []string{
		"component/core/ai-agent/agent-framework/1.0.0/component.yaml",
		"component/core/ai-model/openai/1.0.0/component.yaml",
		"component/core/ai-tool/mcp/1.0.0/component.yaml",
		"recipe/helm/agent-framework/1.0.0/recipe.yaml",
		"recipe/helm/openai/1.0.0/recipe.yaml",
		"recipe/helm/mcp-remote/1.0.0/recipe.yaml",
		"schemalibrary/base-components/1.0.0/library.yaml",
		"feature/core/referenced-secret/1.0.0/feature.yaml",
		"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
	} {
		paths = append(paths, platformHub+"/"+rel)
	}
	return paths
}

// agentFrameworkImage is the image the Release names for the agent.
const agentFrameworkImage = "ghcr.io/example/agent-framework-app:1.0.0"

// writeAgentFrameworkRelease writes beside the drafted Aggregate in dir a
// Manifest realizing the agent, the model and the MCP tool through their
// Kubernetes Recipes, and rewrites the drafted Release with only what it leaves
// for the Release added: an environment, the Manifest, a boundary holding the
// drafted entries — the model's endpoint as the draft writes it — the agent's
// image, and a credential on its model connection. It returns the Release's
// path.
func writeAgentFrameworkRelease(t *testing.T, dir, key, part string) string {
	t.Helper()
	manifest := "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: imported-agent-framework\n" +
		"  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n" +
		"        core/ai-agent/agent-framework:1.0.0: helm/agent-framework:1.0.0\n" +
		"        core/ai-model/openai:1.0.0: helm/openai:1.0.0\n        core/ai-tool/mcp:1.0.0: helm/mcp-remote:1.0.0\n" +
		"    features:\n      type:\n        core/referenced-secret:1.0.0: helm/k8s-existing-secret:1.0.0\n"
	drafted, err := os.ReadFile(filepath.Join(dir, "release.yaml"))
	if err != nil {
		t.Fatalf("read the drafted Release: %v", err)
	}
	release := string(drafted)
	replace := func(old, new string) {
		if !strings.Contains(release, old) {
			t.Fatalf("the drafted Release does not hold %q:\n%s", old, release)
		}
		release = strings.Replace(release, old, new, 1)
	}
	replace("  name: imported/flow\n", "  name: imported-agent-framework\n  environment: production\n")
	replace("  components:\n", "  manifest: imported-agent-framework\n  boundaries:\n    main:\n"+
		"      directory: ./dist/helm\n      components:\n")
	head, entries, _ := strings.Cut(release, "      components:\n")
	var b strings.Builder
	for _, line := range strings.SplitAfter(entries, "\n") {
		if line != "" {
			b.WriteString("    " + line)
		}
	}
	release = head + "      components:\n" + b.String()
	credential := func(name string) string {
		return "                features:\n                  credential:\n" +
			"                    type: core/referenced-secret\n                    infra:\n" +
			"                      origin: " + name + "\n"
	}
	replace("        "+key+":\n          infra: {}\n", "        "+key+":\n          infra:\n            image: "+
		agentFrameworkImage+"\n          uses:\n            model:\n              "+part+":\n"+credential("model-key"))
	for file, body := range map[string]string{"manifest.yaml": manifest, "release.yaml": release} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatalf("write the %s: %v", file, err)
		}
	}
	return filepath.Join(dir, "release.yaml")
}

// TestTheLearnAgentsDraftBuildsReachingItsModelAndNoMCPServer drafts the learn
// agent and builds the draft on helm through the build-faithful harness, the
// drafted Aggregate unchanged and the drafted Release given only what it leaves
// for the Release — the build halts naming the image where the Release names
// none, so its completing is the image's proof: the agent's Deployment is given
// the model's endpoint the declarative agent states as OPENAI_BASE_URL and its id
// as OPENAI_MODEL, and no AGGREGA_TOOL_ variable, since the draft wires no
// connection to the MCP server, which the model's provider, not the application,
// reaches; the model's Service reaches its host, and nothing rendered names the
// MCP server or reaches its host, though the catalog and the Manifest realize the
// MCP tool; and the compiled graph carries the agent's description as the
// declarative agent writes it.
//
// 🔴 PLANTED DEFECT: in writeAgent and renderAggregate
// (plugins/internal/agentframeworkimport/render.go), wire each MCP server on the
// tools socket to an instance of the MCP tool: the build halts, or renders the
// server's variable and Service, and the case fails either way.
func TestTheLearnAgentsDraftBuildsReachingItsModelAndNoMCPServer(t *testing.T) {
	t.Parallel()
	_, dir := writtenImport(t, agentFrameworkWorkspace(t, nil), "learn.yaml",
		readAgentFrameworkFixture(t, "learn-agent.yaml"))
	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  writeAgentFrameworkRelease(t, dir, "learnagent", "gpt-4-1-mini"),
		CatalogPaths: agentFrameworkBuildPaths(),
	})
	for name, want := range map[string]string{
		"OPENAI_BASE_URL": "https://llm-gateway.example.com/v1",
		"OPENAI_MODEL":    "gpt-4.1-mini",
	} {
		if got := platformVariable(t, result.Artifacts, "learnagent", "helm", name); got != want {
			t.Errorf("the agent is given %s=%q, want %q", name, got, want)
		}
	}
	names := helmVariableNames(t, result.Artifacts, "learnagent")
	for _, name := range names {
		if strings.HasPrefix(name, "AGGREGA_TOOL_") {
			t.Errorf("the agent is given %s, a variable nothing in the application reads: %v", name, names)
		}
	}
	if got := string(renderedEnding(t, result.Artifacts, "/helm/gpt-4-1-mini-service.yaml")); !strings.Contains(got,
		"externalName: llm-gateway.example.com\n") {
		t.Errorf("the model's Service does not reach llm-gateway.example.com:\n%s", got)
	}
	for path, body := range result.Artifacts {
		if strings.Contains(path, "microsoft-learn") || strings.Contains(string(body), "learn.microsoft.com") {
			t.Errorf("the rendered %s names the MCP server, which nothing the draft deploys reaches:\n%s", path, body)
		}
	}
	raw, ok := result.IRGraph.Spec.ValuesMapping.Get("learnagent")
	if !ok {
		t.Fatal("the compiled graph carries no values for learnagent")
	}
	var compiled map[string]any
	if err := json.Unmarshal(raw, &compiled); err != nil {
		t.Fatalf("the compiled values of learnagent do not decode: %v", err)
	}
	if want := "Answers questions about {{ product }} from the Microsoft Learn content."; compiled["description"] != want {
		t.Errorf("learnagent compiles its description as %#v, want %q", compiled["description"], want)
	}
}
