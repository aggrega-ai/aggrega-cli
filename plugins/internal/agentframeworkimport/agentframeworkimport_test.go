// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package agentframeworkimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/mcpimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// The Agent Framework importer on the host toolchain: each case hands Import a
// request and the parse the host would run — the compiler's own YAML parser, a
// refusal read as the import socket reads it — and reads what it drafts.

// hostParse parses source as the host's YAML parser does and refuses it as the
// import socket hands an importer a parser's refusal.
func hostParse(_ string, source []byte) (json.RawMessage, error) {
	out := parsercore.ParseYAML(source)
	if err := importersocket.ParseRefusal(out); err != nil {
		return nil, err
	}
	return out.Tree, nil
}

// fixture reads one committed declarative agent
// (internal/importops/testdata/agent-framework).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "importops", "testdata", "agent-framework",
		name))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return body
}

// categoryLabel is the label a catalog Component's category is read from.
const categoryLabel = "component.aggrega.ai/category"

// catalog is a view of a workspace catalog holding the standard library's Agent
// Framework agent, OpenAI model and MCP tool, with the labels the hub gives them.
var catalog = &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
	{Name: Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-agent"}},
	{Name: modelComponent, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-model",
		"model.aggrega.ai/api": "openai"}},
	{Name: mcpimport.Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-tool",
		"tool.aggrega.ai/transport": "http"}},
}}

// importFile imports one declarative agent as a lone file is handed — as the
// request's primary bytes, with no name — against view, with settings.
func importFile(view *aggrega.CatalogProjection, settings string, body []byte) aggrega.ImportOutput {
	in := aggrega.ImportInput{Format: Format, Bytes: body, Catalog: view}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse)
}

// The key-shaped values learn-agent.yaml plants: the model's key, the MCP
// server's key, and a session its MCP server's url carries in its query. None is
// a real key.
const (
	plantedModelKey = "sk-planted-af-model-key-needle-3c7e91d2"
	plantedToolKey  = "planted-af-tool-key-needle-6d02f5"
	plantedQuery    = "planted-af-query-needle-a41b"
)

// The addresses learn-agent.yaml states: its model's endpoint, and its MCP
// server's address without the query.
const (
	learnEndpoint = "https://llm-gateway.example.com/v1"
	learnServer   = "https://learn.microsoft.com/api/mcp"
)

// draft is the part of a drafted Aggregate the cases read, as the compiler reads
// the YAML.
type draft struct {
	Spec struct {
		Components map[string]struct {
			Type   string         `yaml:"type"`
			Values map[string]any `yaml:"values"`
			Uses   map[string]map[string]struct {
				Target string         `yaml:"target"`
				Values map[string]any `yaml:"values"`
			} `yaml:"uses"`
		} `yaml:"components"`
	} `yaml:"spec"`
}

// releaseDraft is the part of a drafted Release the cases read.
type releaseDraft struct {
	Spec struct {
		Components map[string]struct {
			Infra map[string]any `yaml:"infra"`
		} `yaml:"components"`
	} `yaml:"spec"`
}

// files returns the files out holds, by path.
func files(out aggrega.ImportOutput) map[string]string {
	got := map[string]string{}
	for _, f := range out.Files {
		got[f.Path] = f.Content
	}
	return got
}

// drafted reads the Aggregate and the Release out drafted, failing the test where
// it drafted neither.
func drafted(t *testing.T, out aggrega.ImportOutput) (draft, string, releaseDraft, string) {
	t.Helper()
	fs := files(out)
	text, ok := fs["aggregate.yaml"]
	release, ok2 := fs["release.yaml"]
	if !ok || !ok2 {
		t.Fatalf("the import drafted no Aggregate and Release: %+v (diagnostics %+v)", out.Files, out.Diagnostics)
	}
	var d draft
	if err := yaml.Unmarshal([]byte(text), &d); err != nil {
		t.Fatalf("the drafted Aggregate does not read as YAML: %v\n%s", err, text)
	}
	var r releaseDraft
	if err := yaml.Unmarshal([]byte(release), &r); err != nil {
		t.Fatalf("the drafted Release does not read as YAML: %v\n%s", err, release)
	}
	return d, text, r, release
}

// reported returns out's diagnostics as code: message, in order.
func reported(out aggrega.ImportOutput) []string {
	var got []string
	for _, d := range out.Diagnostics {
		got = append(got, d.Code+": "+d.Message)
	}
	return got
}

// decision returns the decision out reports for the source node id, failing the
// test where it reports none.
func decision(t *testing.T, out aggrega.ImportOutput, id string) aggrega.ImportDecision {
	t.Helper()
	for _, d := range out.Decisions {
		if d.SourceNode.ID == id {
			return d
		}
	}
	t.Fatalf("no decision reports %s: %+v", id, out.Decisions)
	return aggrega.ImportDecision{}
}

// TestEachCommittedAgentIsDraftedAsOneInstanceWiredToItsModelAndServers imports
// each committed declarative agent as a lone file and requires one instance of
// core/ai-agent/agent-framework keyed by its name, carrying its displayName and
// its description — double braces escaped, so it reads back as written — wired
// on its model socket to an instance of core/ai-model/openai serving its id
// where its provider is OpenAI and to nothing otherwise, and on its tools socket
// to an instance of core/ai-tool/mcp per MCP server; the Release carrying the
// model's endpoint and each server's address as their infra.endpoint, the
// server's without its query, which the comment above the server's entry
// carries as written; every other tool reported in a decision choosing nothing;
// and nothing reported but one credential-carried warning, in order, for each
// literal value the draft writes and each part of a url it leaves out.
//
// 🔴 PLANTED DEFECT: in unwiredWhy (agentframeworkimport.go), compare the provider
// with "openai": the learn and docs agents are left unwired and both rows fail.
func TestEachCommittedAgentIsDraftedAsOneInstanceWiredToItsModelAndServers(t *testing.T) {
	cases := []struct {
		fixture, key, part, model string
		values                    map[string]any
		servers                   map[string]string
		releaseEndpoints          map[string]string
		notes                     []string
		unchosen                  []string
		carried                   []string
	}{
		{
			fixture: "learn-agent.yaml", key: "learnagent", part: "gpt-4-1-mini", model: "gpt-4.1-mini",
			values: map[string]any{"display_name": "Learn Assistant",
				"description": "Answers questions about {{`{{`}} product }} from the Microsoft Learn content."},
			servers:          map[string]string{"microsoft-learn": "Get information from Microsoft Learn."},
			releaseEndpoints: map[string]string{"gpt-4-1-mini": learnEndpoint, "microsoft-learn": learnServer},
			notes: []string{"instructions, outputSchema, the options of its model requests (temperature, topP).\n",
				"# Function tools, whose code the application binds, drafted as no Component: GetRelease.\n",
				"# Tools the model's provider runs, drafted as no Component: search.\n",
				"its approval mode, never; the tools of the server it allows, microsoft_docs_search, " +
					"microsoft_docs_fetch.\n"},
			unchosen: []string{"LearnAgent#GetRelease", "LearnAgent#search"},
			carried: []string{"the value of model.connection.key of node LearnAgent is carried as written",
				"the value of tools.microsoft_learn.connection.key of node LearnAgent is carried as written",
				"the value of model.connection.endpoint of node LearnAgent is carried as written",
				"the value of tools.microsoft_learn.url of node LearnAgent is carried as written",
				"tools.microsoft_learn.url of node LearnAgent is carried with its query as written"},
		},
		{
			fixture: "docs-agent.yaml", key: "docsagent", part: "openai-model",
			values:           map[string]any{"description": "Answers questions from the team's documentation."},
			servers:          map[string]string{"team-docs": "Search the team's documentation."},
			releaseEndpoints: map[string]string{},
			notes: []string{"# values.model is left for the author, since the declarative agent reads its model's id " +
				"from the environment variable OPENAI_MODEL"},
		},
		{
			fixture: "research-agent.yaml", key: "researchagent",
			values:           map[string]any{"description": "Researches an open issue and drafts a reply."},
			servers:          map[string]string{"github": "Read and comment on the repository's issues."},
			releaseEndpoints: map[string]string{"github": "https://api.githubcopilot.com/mcp/"},
			notes: []string{"# The model connection is left for the author, since its model names no provider, so " +
				"Agent Framework uses the default provider the application sets"},
			unchosen: []string{"ResearchAgent#model", "ResearchAgent#python"},
			carried:  []string{"the value of tools.github.url of node ResearchAgent is carried as written"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			out := importFile(catalog, "", fixture(t, tc.fixture))
			got := reported(out)
			if len(got) != len(tc.carried) {
				t.Fatalf("the import reported %q, want one credential-carried warning per field %q", got, tc.carried)
			}
			for i, report := range tc.carried {
				if want := "credential-carried: " + report; !strings.HasPrefix(got[i], want) {
					t.Errorf("diagnostic %d is %q, want it to open %q", i, got[i], want)
				}
			}
			d, text, r, release := drafted(t, out)
			agent := d.Spec.Components[tc.key]
			if agent.Type != Component {
				t.Fatalf("%s is an instance of %q, want %s:\n%s", tc.key, agent.Type, Component, text)
			}
			for k, want := range tc.values {
				if agent.Values[k] != want {
					t.Errorf("%s carries %s %#v, want %#v", tc.key, k, agent.Values[k], want)
				}
			}
			want := 1 + len(tc.servers)
			if tc.part != "" {
				want++
				if got := agent.Uses[modelSocket][tc.part].Target; got != tc.part {
					t.Errorf("the model connection %s targets %q:\n%s", tc.part, got, text)
				}
				part := d.Spec.Components[tc.part]
				if part.Type != modelComponent {
					t.Errorf("the model part is an instance of %q, want %s", part.Type, modelComponent)
				}
				if tc.model != "" && part.Values["model"] != tc.model {
					t.Errorf("the model part serves %#v, want %q", part.Values["model"], tc.model)
				}
				if tc.model == "" && part.Values != nil {
					t.Errorf("the model part names a model %#v, where the declarative agent reads it from a variable",
						part.Values)
				}
			} else if _, wired := agent.Uses[modelSocket]; wired {
				t.Errorf("%s is wired to a model, where its model is wired to nothing:\n%s", tc.key, text)
			}
			if len(d.Spec.Components) != want {
				t.Errorf("the Aggregate holds %d instances, want %d:\n%s", len(d.Spec.Components), want, text)
			}
			for key, description := range tc.servers {
				if got := agent.Uses[toolsSocket][key].Target; got != key {
					t.Errorf("the tools connection %s targets %q:\n%s", key, got, text)
				}
				server := d.Spec.Components[key]
				if server.Type != mcpimport.Component || server.Values["description"] != description {
					t.Errorf("%s is %q carrying %#v, want %s carrying %q", key, server.Type, server.Values,
						mcpimport.Component, description)
				}
			}
			for key, endpoint := range tc.releaseEndpoints {
				if got := r.Spec.Components[key].Infra["endpoint"]; got != endpoint {
					t.Errorf("the Release gives %s the endpoint %#v, want %q:\n%s", key, got, endpoint, release)
				}
			}
			for _, note := range tc.notes {
				if !strings.Contains(text, note) {
					t.Errorf("the Aggregate does not say %q:\n%s", note, text)
				}
			}
			for _, id := range tc.unchosen {
				if got := decision(t, out, id); got.Chosen != nil {
					t.Errorf("%s is decided as %+v; it becomes no Component", id, got)
				}
			}
			if strings.Contains(text, plantedQuery) {
				t.Errorf("the MCP server's query reaches the Aggregate")
			}
			if tc.fixture == "learn-agent.yaml" {
				if want := "    #   - query: \"session=" + plantedQuery + "\"\n    microsoft-learn:\n"; !strings.Contains(
					release, want) {
					t.Errorf("the Release does not carry the MCP server's query in the comment above it, %q:\n%s", want,
						release)
				}
			}
		})
	}
}

// TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares imports each
// committed declarative agent, and the docs agent over a catalog holding no
// OpenAI Component, so its model's Component is drafted, and requires the drafted
// Release to hold an entry for every instance the drafted Aggregate declares, the
// model included whether or not its connection states an endpoint: a build places
// only the instances a Release names, so an instance without an entry halts it.
// The docs agent's model, whose connection states no endpoint, has an entry
// holding no infrastructure, the Component's own default being its address. Each
// importer package holds a test of this name over its own renderer, since each
// importer renders its own Release and the plugin kit renders none.
//
// 🔴 PLANTED DEFECT: in writePartEntry (render.go), return with no entry where the
// connection states no endpoint: the docs agent's rows fail naming openai-model.
func TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares(t *testing.T) {
	agentOnly := &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
		{Name: Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-agent"}},
		{Name: mcpimport.Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-tool",
			"tool.aggrega.ai/transport": "http"}},
	}}
	for _, row := range []struct {
		name, fixture string
		view          *aggrega.CatalogProjection
		// empty is the model part whose entry holds no infrastructure, "" where
		// the agent has none such.
		empty string
	}{
		{"a model whose connection states an endpoint", "learn-agent.yaml", catalog, ""},
		{"a model whose connection states none", "docs-agent.yaml", catalog, "openai-model"},
		{"a model whose Component is drafted", "docs-agent.yaml", agentOnly, "openai-model"},
		{"no model wired", "research-agent.yaml", catalog, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			d, _, r, release := drafted(t, importFile(row.view, "", fixture(t, row.fixture)))
			var unplaced []string
			for key := range d.Spec.Components {
				if _, named := r.Spec.Components[key]; !named {
					unplaced = append(unplaced, key)
				}
			}
			if len(unplaced) > 0 {
				slices.Sort(unplaced)
				t.Errorf("the Release names no entry for %v, which the Aggregate declares:\n%s", unplaced, release)
			}
			if row.empty != "" && !strings.Contains(release, "    "+row.empty+":\n      infra: {}\n") {
				t.Errorf("the Release holds no entry %s holding no infrastructure:\n%s", row.empty, release)
			}
		})
	}
}

// TestTheProviderDecidesWhetherTheModelIsWired imports one declarative agent per
// way its model can name its provider and its id, and requires its model wired
// to an instance of the OpenAI Component where the provider is OpenAI, as Agent
// Framework's loader names it, and an id is stated, and to nothing otherwise,
// with the Aggregate and the model's decision saying why.
//
// 🔴 PLANTED DEFECT: in unwiredWhy (agentframeworkimport.go), drop the case for a
// provider other than OpenAI: the AzureOpenAI and Anthropic rows are wired.
func TestTheProviderDecidesWhetherTheModelIsWired(t *testing.T) {
	cases := []struct {
		name, model string
		wired       bool
		why         string
	}{
		{"OpenAI with an id", "  id: gpt-4o\n  provider: OpenAI\n", true, ""},
		{"OpenAI with an id read from a variable", "  id: =Env.MODEL\n  provider: OpenAI\n", true, ""},
		{"AzureOpenAI", "  id: gpt-4o\n  provider: AzureOpenAI\n", false, "its model's provider is AzureOpenAI, while " +
			"the model socket of core/ai-agent/agent-framework admits a model serving the OpenAI API"},
		{"Anthropic", "  id: claude-sonnet\n  provider: Anthropic\n  apiType: Chat\n", false,
			"its model's provider is Anthropic"},
		{"openai in lower case, which the loader serves no client for", "  id: gpt-4o\n  provider: openai\n", false,
			"its model's provider is openai"},
		{"no provider", "  id: gpt-4o\n", false, "its model names no provider, so Agent Framework uses the default " +
			"provider the application sets"},
		{"a provider read from a variable", "  id: gpt-4o\n  provider: =Env.PROVIDER\n", false,
			"its model names its provider through the environment variable PROVIDER"},
		{"a provider computed", "  id: gpt-4o\n  provider: =If(true, \"OpenAI\", \"Foundry\")\n", false,
			"its model names its provider through an expression the application computes"},
		{"no id", "  provider: OpenAI\n", false, "its model names no id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "kind: Prompt\nname: Probe\nmodel:\n" + tc.model
			out := importFile(catalog, "", []byte(body))
			d, text, _, _ := drafted(t, out)
			_, wired := d.Spec.Components["probe"].Uses[modelSocket]
			if wired != tc.wired {
				t.Fatalf("the model is wired %v, want %v:\n%s", wired, tc.wired, text)
			}
			if tc.wired {
				return
			}
			if want := "# The model connection is left for the author, since " + tc.why; !strings.Contains(text, want) {
				t.Errorf("the Aggregate does not say %q:\n%s", want, text)
			}
			if got := decision(t, out, "Probe#model"); got.Chosen != nil || !strings.Contains(got.Rationale, tc.why) {
				t.Errorf("the model's decision is %+v, want no choice saying %q", got, tc.why)
			}
		})
	}
}

// TestACatalogHoldingNoOpenAIComponentDraftsOne imports the learn agent against a
// catalog view holding no core/ai-model/openai: the model part is an instance of
// a Component drafted under the target namespace, labelled with the OpenAI API
// the runtime's model socket selects, and written as a draft entity and a file.
func TestACatalogHoldingNoOpenAIComponentDraftsOne(t *testing.T) {
	out := importFile(nil, "", fixture(t, "learn-agent.yaml"))
	d, text, _, _ := drafted(t, out)
	if got := d.Spec.Components["gpt-4-1-mini"].Type; got != "imported/ai-model/openai" {
		t.Errorf("the model part is an instance of %q, want imported/ai-model/openai:\n%s", got, text)
	}
	body, ok := files(out)["components/imported-ai-model-openai.yaml"]
	if !ok || !strings.Contains(body, "    model.aggrega.ai/api: openai\n") ||
		!strings.Contains(body, "    component.aggrega.ai/category: ai-model\n") {
		t.Errorf("the drafted model Component is missing or mislabelled (%v):\n%s", ok, body)
	}
	if got := decision(t, out, "gpt-4.1-mini"); got.Chosen == nil || got.Chosen.Mode != "draft" {
		t.Errorf("the model part's decision is %+v, want a draft", got)
	}
}

// TestTheEmptyCredentialsSettingDecidesWhatADeclarativeAgentCarries imports the
// learn agent with empty_credentials none, marked and all: none carries every
// literal value as written — the MCP server's query in the comment above its
// entry — and names each in a credential-carried warning; marked writes the
// keys, the MCP server's address and its query empty, naming each under
// credential-emptied, and carries the model's endpoint; all writes all five
// empty. No value reaches a diagnostic or a decision under any setting, and the
// query reaches no file but the Release, and that only under none. The docs
// agent's references are named by their variables under every setting and
// reported under neither code.
//
// 🔴 PLANTED DEFECT: in statedValues (agentframeworkimport.go), mark no value: the
// marked row carries both keys as written.
func TestTheEmptyCredentialsSettingDecidesWhatADeclarativeAgentCarries(t *testing.T) {
	learn := fixture(t, "learn-agent.yaml")
	for _, needle := range []string{plantedModelKey, plantedToolKey, plantedQuery, learnEndpoint, learnServer} {
		if !strings.Contains(string(learn), needle) {
			t.Fatalf("the needle: %q is not in the fixture, so the search finds nothing", needle)
		}
	}
	for _, tc := range []struct {
		setting          string
		carried, emptied []string
	}{
		{"none", []string{plantedModelKey, plantedToolKey, learnEndpoint, learnServer}, nil},
		{"marked", []string{learnEndpoint}, []string{plantedModelKey, plantedToolKey, learnServer}},
		{"all", nil, []string{plantedModelKey, plantedToolKey, learnEndpoint, learnServer}},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			out := importFile(catalog, `{"empty_credentials":"`+tc.setting+`"}`, learn)
			_, text, _, release := drafted(t, out)
			for _, v := range tc.carried {
				if !strings.Contains(release, `"`+v+`"`) {
					t.Errorf("the Release does not carry %q as written:\n%s", v, release)
				}
			}
			for _, v := range tc.emptied {
				if strings.Contains(release+text, v) {
					t.Errorf("the drafts carry %q, which the setting writes empty:\n%s", v, release)
				}
			}
			if got := len(reported(out)); got != 5 {
				t.Errorf("the import reported %d diagnostics, want 5: %q", got, reported(out))
			}
			// The query is a fifth value, reported apart from the four the case lists:
			// carried under none, written empty under marked and all.
			emptiedCount, queryEmptied := 0, tc.setting != "none"
			if queryEmptied {
				emptiedCount--
			}
			for _, d := range out.Diagnostics {
				if d.Code == "credential-emptied" {
					emptiedCount++
				}
			}
			if emptiedCount != len(tc.emptied) {
				t.Errorf("%d values are reported under credential-emptied, want %d and the query: %q", emptiedCount,
					len(tc.emptied), reported(out))
			}
			query := "credential-carried: tools.microsoft_learn.url of node LearnAgent is carried with its query as written "
			if queryEmptied {
				query = "credential-emptied: tools.microsoft_learn.url of node LearnAgent is written with its query empty "
			}
			if got := reported(out); !strings.HasPrefix(got[len(got)-1], query) {
				t.Errorf("the last report is %q, want it to open %q", got[len(got)-1], query)
			}
			reports, _ := json.Marshal(struct {
				D []aggrega.Diagnostic
				E []aggrega.ImportDecision
			}{out.Diagnostics, out.Decisions})
			for _, v := range []string{plantedModelKey, plantedToolKey, plantedQuery} {
				if strings.Contains(string(reports), v) {
					t.Errorf("a diagnostic or a decision carries the planted %q", v)
				}
			}
			for _, f := range out.Files {
				want := f.Path == "release.yaml" && !queryEmptied
				if got := strings.Contains(f.Content, plantedQuery); got != want {
					t.Errorf("%s carries the MCP server's query: %v, want %v", f.Path, got, want)
				}
			}
			queryLine := "    #   - query: \"session=" + plantedQuery + "\"\n"
			if queryEmptied {
				queryLine = "    #   - query, its value left out, as the empty_credentials setting asks\n"
			}
			if !strings.Contains(release, queryLine) {
				t.Errorf("the Release does not hold %q:\n%s", queryLine, release)
			}

			docs := importFile(catalog, `{"empty_credentials":"`+tc.setting+`"}`, fixture(t, "docs-agent.yaml"))
			_, _, _, docsRelease := drafted(t, docs)
			for _, want := range []string{"#   - model.connection.key, read from the environment variable OPENAI_API_KEY\n",
				"#   - infra.endpoint, which the declarative agent reads from the environment variable DOCS_MCP_URL\n"} {
				if !strings.Contains(docsRelease, want) {
					t.Errorf("the docs agent's Release does not say %q:\n%s", want, docsRelease)
				}
			}
			if got := reported(docs); len(got) != 0 {
				t.Errorf("a reference, which holds no value, is reported: %q", got)
			}
		})
	}
}

// TestAnMCPServersAddressCarriesItsLeftOutPartsInTheComment imports a
// declarative agent whose MCP server is reached at an address carrying a planted
// user part, query and fragment, under every value of the empty_credentials
// setting, after a needle proves each planted value is in the source:
//
//   - absent or none: the server's infra.endpoint is the address without the
//     three parts; each planted value is in the Release on one line alone, the
//     line naming its part in the comment above the server's entry, beside the
//     address; each part is named in a credential-carried warning by the agent,
//     the url's field and the part's name; no planted value is in the Aggregate,
//     a diagnostic or a decision;
//   - marked or all: no planted value is in any file, entity, diagnostic or
//     decision, infra.endpoint is left for the Release, the comment names each
//     part alone, and each is reported under credential-emptied.
//
// 🔴 PLANTED DEFECT: in located (agentframeworkimport.go), skip the parts of an
// MCP server's url: no report names a part, and under marked or all the planted
// values are carried in the comment, so the case fails on both.
func TestAnMCPServersAddressCarriesItsLeftOutPartsInTheComment(t *testing.T) {
	const (
		userPart = "planted-af-userpart-needle-81d2"
		query    = "planted-af-query-needle-0f3c"
		fragment = "planted-af-fragment-needle-b6e5"
	)
	body := []byte("kind: Prompt\nname: Probe\nmodel:\n  id: gpt-4o\n  provider: OpenAI\ntools:\n  - kind: mcp\n" +
		"    name: docs\n    url: https://reader:" + userPart + "@docs.example.com/mcp?key=" + query + "#" + fragment +
		"\n")
	parts := []struct{ name, text string }{{"user part", "reader:" + userPart}, {"query", "key=" + query},
		{"fragment", fragment}}
	for _, needle := range []string{userPart, query, fragment} {
		if !strings.Contains(string(body), needle) {
			t.Fatalf("the needle: %q is not in the source, so the search finds nothing", needle)
		}
	}
	for _, settings := range []string{"", `{"empty_credentials":"none"}`, `{"empty_credentials":"marked"}`,
		`{"empty_credentials":"all"}`} {
		t.Run(settings, func(t *testing.T) {
			emptied := strings.Contains(settings, "marked") || strings.Contains(settings, "all")
			out := importFile(catalog, settings, body)
			_, text, r, release := drafted(t, out)
			lead := "    # The address's user part, query and fragment are not written in infra.endpoint, which holds " +
				"the address without any part that carries a key; a consumer reading its key from the address supplies " +
				"the whole address on its own connection to the tool:\n"
			if !strings.Contains(release, lead) {
				t.Errorf("the Release does not open the comment with %q:\n%s", lead, release)
			}
			endpoint, _ := r.Spec.Components["docs"].Infra["endpoint"].(string)
			if want := "https://docs.example.com/mcp"; !emptied && endpoint != want || emptied && endpoint != "" {
				t.Errorf("the server's infra.endpoint is %q under %q:\n%s", endpoint, settings, release)
			}
			got := strings.Join(reported(out), "\n")
			for _, p := range parts {
				line := "    #   - " + p.name + ": " + yamlString(p.text) + "\n"
				report := "credential-carried: tools.docs.url of node Probe is carried with its " + p.name +
					" as written in the Release the draft writes"
				if emptied {
					line = "    #   - " + p.name + ", " + emptiedNote + "\n"
					report = "credential-emptied: tools.docs.url of node Probe is written with its " + p.name +
						" empty in the Release the draft writes"
				}
				if !strings.Contains(release, line) {
					t.Errorf("the Release does not hold %q:\n%s", line, release)
				}
				if !strings.Contains(got, report) {
					t.Errorf("no report says %q:\n%s", report, got)
				}
			}
			reports, _ := json.Marshal(struct {
				D []aggrega.Diagnostic
				E []aggrega.ImportDecision
				N []aggrega.EmittedEntity
			}{out.Diagnostics, out.Decisions, out.Entities})
			for _, needle := range []string{userPart, query, fragment} {
				lines := 0
				for _, l := range strings.Split(release, "\n") {
					if strings.Contains(l, needle) {
						lines++
						if !strings.HasPrefix(l, "    #   - ") {
							t.Errorf("the planted %q is on a line other than its part's in the comment: %q", needle, l)
						}
					}
				}
				want := 1
				if emptied {
					want = 0
				}
				if lines != want {
					t.Errorf("the Release holds the planted %q on %d lines, want %d", needle, lines, want)
				}
				// The Release entity's body is the Release itself; every other entity,
				// every decision and every diagnostic holds no planted value.
				if strings.Contains(text, needle) || strings.Count(string(reports), needle) != want {
					t.Errorf("the planted %q reaches the Aggregate, a diagnostic, a decision or another entity", needle)
				}
			}
		})
	}
}

// TestAnExpressionIsNamedAndNeverWritten imports a declarative agent whose
// model's key and MCP server's address are PowerFx expressions other than a
// reference: each is named as an expression the import does not evaluate,
// written nowhere and reported under no code, whatever the setting.
func TestAnExpressionIsNamedAndNeverWritten(t *testing.T) {
	const secret = "sk-planted-af-expression-needle-77e0"
	body := "kind: Prompt\nname: Probe\nmodel:\n  id: gpt-4o\n  provider: OpenAI\n  connection:\n    kind: key\n" +
		"    apiKey: =Concatenate(\"" + secret + "\", \"\")\ntools:\n  - kind: mcp\n    name: docs\n" +
		"    url: =Concatenate(\"https://docs.example.com/mcp?key=\", \"" + secret + "\")\n"
	for _, setting := range []string{"none", "all"} {
		out := importFile(catalog, `{"empty_credentials":"`+setting+`"}`, []byte(body))
		_, _, _, release := drafted(t, out)
		for _, want := range []string{"#   - model.connection.apiKey, computed by an expression the import does not " +
			"evaluate\n", "#   - infra.endpoint, which the declarative agent computes with an expression the import " +
			"does not evaluate\n"} {
			if !strings.Contains(release, want) {
				t.Errorf("%s: the Release does not say %q:\n%s", setting, want, release)
			}
		}
		for _, f := range out.Files {
			if strings.Contains(f.Content, secret) {
				t.Errorf("%s: %s carries the expression's text", setting, f.Path)
			}
		}
		if got := reported(out); len(got) != 0 {
			t.Errorf("%s: an expression, which the draft never writes, is reported: %q", setting, got)
		}
	}
}

// TestAnAnonymousConnectionIsWrittenUnauthenticated imports a declarative agent
// whose OpenAI model is reached on an anonymous connection: the wired connection
// says its target takes no credential, and the Release asks for none.
func TestAnAnonymousConnectionIsWrittenUnauthenticated(t *testing.T) {
	body := "kind: Prompt\nname: Probe\nmodel:\n  id: llama3\n  provider: OpenAI\n  connection:\n" +
		"    kind: anonymous\n    endpoint: http://ollama:11434/v1\n"
	out := importFile(catalog, "", []byte(body))
	d, text, r, release := drafted(t, out)
	edge := d.Spec.Components["probe"].Uses[modelSocket]["llama3"]
	if edge.Target != "llama3" || edge.Values["unauthenticated"] != true {
		t.Errorf("the anonymous connection is %+v, want it targeting llama3 with unauthenticated true:\n%s", edge, text)
	}
	if strings.Contains(release, "the credential probe presents to the model") {
		t.Errorf("the Release asks for a credential an anonymous connection does not present:\n%s", release)
	}
	if got := r.Spec.Components["llama3"].Infra["endpoint"]; got != "http://ollama:11434/v1" {
		t.Errorf("the model part's endpoint is %#v", got)
	}
}

// TestOnlyAPromptAgentIsRead imports sources of other kinds and spellings: a
// source Agent Framework builds a prompt agent from — kind Prompt in any letter
// case, or Agent — is drafted, and any other, or one that is not an object, is
// reported under no-nodes and drafts nothing.
func TestOnlyAPromptAgentIsRead(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		read       bool
	}{
		{"Prompt", "kind: Prompt\nname: A\nmodel: {}\n", true},
		{"prompt in lower case", "kind: prompt\nname: A\n", true},
		{"Agent", "kind: Agent\nname: A\n", true},
		{"agent in lower case", "kind: agent\nname: A\n", false},
		{"a workflow", "kind: Workflow\nname: A\n", false},
		{"no kind", "name: A\nmodel: {}\n", false},
		{"a list", "- kind: Prompt\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := importFile(catalog, "", []byte(tc.body))
			_, drafts := files(out)["aggregate.yaml"]
			if drafts != tc.read {
				t.Fatalf("the source drafts %v, want %v: %q", drafts, tc.read, reported(out))
			}
			if !tc.read && (len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != "no-nodes") {
				t.Errorf("the source is reported as %q, want one no-nodes", reported(out))
			}
		})
	}
}

// TestASourceTheParserRefusesHaltsTheImport imports a declarative agent cut
// short: the parser refuses it, and the import reports it under parse-failed,
// which halts the import, and drafts nothing.
//
// 🔴 PLANTED DEFECT: in Import (agentframeworkimport.go), report a refused source
// under importer.NoNodes: the case fails on the code.
func TestASourceTheParserRefusesHaltsTheImport(t *testing.T) {
	out := importFile(catalog, "", []byte("kind: Prompt\nname: [cut short\n"))
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != "parse-failed" || out.Diagnostics[0].Severity != "halt" {
		t.Fatalf("the import reported %+v, want one halting parse-failed", out.Diagnostics)
	}
	if len(out.Files) != 0 || len(out.Entities) != 0 {
		t.Errorf("a refused source drafted %d files and %d entities", len(out.Files), len(out.Entities))
	}
}

// TestAnotherFormatReadsNothing imports a declarative agent naming another
// format: the importer reads nothing and reports nothing.
func TestAnotherFormatReadsNothing(t *testing.T) {
	out := Import(aggrega.ImportInput{Format: "langgraph", Bytes: fixture(t, "learn-agent.yaml")}, hostParse)
	if len(out.Files) != 0 || len(out.Diagnostics) != 0 || len(out.Decisions) != 0 {
		t.Errorf("an import of another format drafted %+v", out)
	}
}

// TestSeveralSourcesAreSeveralAgentsSharingOneModel imports two declarative
// agents handed under the attachment's with.sources, both naming one OpenAI
// model at one address: each is an instance of its own, named in a report by its
// file and its name, and both are wired to one model part.
func TestSeveralSourcesAreSeveralAgentsSharingOneModel(t *testing.T) {
	agentOf := func(name string) []byte {
		return []byte("kind: Prompt\nname: " + name + "\nmodel:\n  id: gpt-4o\n  provider: OpenAI\n")
	}
	out := Import(aggrega.ImportInput{Format: Format, Catalog: catalog, Sources: []aggrega.ImportSource{
		{Name: "triage.yaml", Bytes: agentOf("Triage")}, {Name: "writer.yaml", Bytes: agentOf("Writer")}}}, hostParse)
	d, text, _, _ := drafted(t, out)
	for _, key := range []string{"triage", "writer"} {
		if got := d.Spec.Components[key].Uses[modelSocket]["gpt-4o"].Target; got != "gpt-4o" {
			t.Errorf("%s is wired to %q, want the one part gpt-4o:\n%s", key, got, text)
		}
	}
	if len(d.Spec.Components) != 3 {
		t.Errorf("the Aggregate holds %d instances, want two agents and one model:\n%s", len(d.Spec.Components), text)
	}
	for _, want := range []string{"#   - triage.yaml\n#   - writer.yaml\n", "# The declarative agent Triage, of triage.yaml.\n",
		"# The model gpt-4o, which triage.yaml#Triage, writer.yaml#Writer names with the OpenAI provider.\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the Aggregate does not say %q:\n%s", want, text)
		}
	}
	decision(t, out, "writer.yaml#Writer")
}

// TestDoubleBracesReadBackAsWrittenOnceTheAggregateIsEvaluated imports a
// declarative agent holding "{{" in its description, its MCP server's
// description, its tools' names, its model's options and its provider, and
// requires the drafted Aggregate — its comments included — to run as a template
// and read back, once evaluated, with each value and each comment as the
// declarative agent writes it: the compiler evaluates an Aggregate's whole text
// before reading it as YAML.
//
// 🔴 PLANTED DEFECT: in importer.CommentLine
// (aggrega-pdk/go/pkg/importer/definition.go), write the line unescaped: the
// Aggregate no longer parses as a template, and the case fails on it.
func TestDoubleBracesReadBackAsWrittenOnceTheAggregateIsEvaluated(t *testing.T) {
	body := "kind: Prompt\nname: Probe\ndescription: \"Answers {{ question }}\"\nmodel:\n" +
		"  id: gpt-4o\n  provider: \"Open{{AI}}\"\n  options:\n    \"{{temperature}}\": 1\ntools:\n" +
		"  - kind: function\n    name: \"Get{{ release }}\"\n  - kind: mcp\n    name: \"docs {{x}}\"\n" +
		"    description: \"Docs about {{ topic }}\"\n    url: https://docs.example.com/mcp\n"
	out := importFile(catalog, "", []byte(body))
	_, text, _, _ := drafted(t, out)
	tmpl, err := template.New("aggregate").Parse(text)
	if err != nil {
		t.Fatalf("the drafted Aggregate does not parse as a template: %v\n%s", err, text)
	}
	var evaluated strings.Builder
	if err := tmpl.Execute(&evaluated, nil); err != nil {
		t.Fatalf("the drafted Aggregate does not run as a template: %v\n%s", err, text)
	}
	var read draft
	if err := yaml.Unmarshal([]byte(evaluated.String()), &read); err != nil {
		t.Fatalf("the evaluated Aggregate does not read as YAML: %v\n%s", err, evaluated.String())
	}
	if got := read.Spec.Components["probe"].Values["description"]; got != "Answers {{ question }}" {
		t.Errorf("the description reads back as %#v", got)
	}
	if got := read.Spec.Components["docs-x"].Values["description"]; got != "Docs about {{ topic }}" {
		t.Errorf("the MCP server's description reads back as %#v", got)
	}
	for _, want := range []string{"drafted as no Component: Get{{ release }}.\n",
		"the options of its model requests ({{temperature}})", "its model's provider is Open{{AI}}, while",
		"# The MCP server docs {{x}}, which Probe reaches.\n"} {
		if !strings.Contains(evaluated.String(), want) {
			t.Errorf("the evaluated Aggregate does not say %q:\n%s", want, evaluated.String())
		}
	}
}

// TestTwoImportsAreByteIdentical imports each committed declarative agent twice
// and requires the two to return the same output, byte for byte.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	for _, name := range []string{"learn-agent.yaml", "docs-agent.yaml", "research-agent.yaml"} {
		first, _ := json.Marshal(importFile(catalog, "", fixture(t, name)))
		second, _ := json.Marshal(importFile(catalog, "", fixture(t, name)))
		if string(first) != string(second) {
			t.Errorf("two imports of %s differ:\n%s\n%s", name, first, second)
		}
	}
}

// TestTheEntitiesNameEachInstancesComponent requires the learn agent's import to
// emit a reference to each Component its instances are of, in order, beside the
// drafted Aggregate and Release.
func TestTheEntitiesNameEachInstancesComponent(t *testing.T) {
	out := importFile(catalog, "", fixture(t, "learn-agent.yaml"))
	var got []string
	for _, e := range out.Entities {
		got = append(got, e.Kind+" "+e.Name+" "+e.Mode)
	}
	want := []string{"Component " + Component + " reference", "Component " + modelComponent + " reference",
		"Component " + mcpimport.Component + " reference", "Aggregate imported/flow draft", "Release imported/flow draft"}
	if !slices.Equal(got, want) {
		t.Errorf("the import emits %q, want %q", got, want)
	}
}
