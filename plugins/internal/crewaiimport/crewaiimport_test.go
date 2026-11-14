// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package crewaiimport

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

// The CrewAI importer on the host toolchain: each case hands Import a request
// and the parse the host would run — the compiler's own YAML parser and its parser
// for JSON with comments, a refusal read as the import socket reads it — and reads
// what it drafts.

// hostParse parses source as the host's parser for syntax does and refuses it as
// the import socket hands an importer a parser's refusal.
func hostParse(syntax string, source []byte) (json.RawMessage, error) {
	out := parsercore.ParseYAML(source)
	if syntax == SyntaxJSONC {
		out = parsercore.ParseJSONC(source)
	}
	if err := importersocket.ParseRefusal(out); err != nil {
		return nil, err
	}
	return out.Tree, nil
}

// fixture reads one file of a committed CrewAI project
// (internal/importops/testdata/crewai).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "importops", "testdata", "crewai",
		filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return body
}

// catalog is a view of a workspace catalog holding the standard library's CrewAI
// agent, OpenAI and Azure OpenAI models and search tool, with the labels the hub
// gives them.
var catalog = &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
	{Name: Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-agent"}},
	{Name: "core/ai-model/openai", Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-model"}},
	{Name: "core/ai-model/azure-openai", Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-model"}},
	{Name: "core/ai-tool/search", Version: "1.0.0", Labels: map[string]string{categoryLabel: toolCategory}},
}}

// importSources imports sources, named, against catalog, with settings.
func importSources(settings string, sources ...aggrega.ImportSource) aggrega.ImportOutput {
	in := aggrega.ImportInput{Format: Format, Sources: sources, Catalog: catalog}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse)
}

// classic and jsonc are the committed projects' declaration files, as a
// directory import hands them: by their names inside the project, in name order.
func classic(t *testing.T) []aggrega.ImportSource {
	t.Helper()
	return []aggrega.ImportSource{
		{Name: "src/market_brief/config/agents.yaml", Bytes: fixture(t, "classic/src/market_brief/config/agents.yaml")},
		{Name: "src/market_brief/config/tasks.yaml", Bytes: fixture(t, "classic/src/market_brief/config/tasks.yaml")},
	}
}

func jsonc(t *testing.T) []aggrega.ImportSource {
	t.Helper()
	return []aggrega.ImportSource{
		{Name: "agents/analyst.jsonc", Bytes: fixture(t, "jsonc/agents/analyst.jsonc")},
		{Name: "agents/researcher.jsonc", Bytes: fixture(t, "jsonc/agents/researcher.jsonc")},
		{Name: "crew.jsonc", Bytes: fixture(t, "jsonc/crew.jsonc")},
	}
}

// jsonSpelled is the committed project in the layout's strict-JSON spelling, as a
// directory import hands it.
func jsonSpelled(t *testing.T) []aggrega.ImportSource {
	t.Helper()
	return []aggrega.ImportSource{
		{Name: "agents/analyst.json", Bytes: fixture(t, "json/agents/analyst.json")},
		{Name: "agents/researcher.json", Bytes: fixture(t, "json/agents/researcher.json")},
		{Name: "crew.json", Bytes: fixture(t, "json/crew.json")},
	}
}

// draft is the part of a drafted Aggregate the cases read, as the compiler reads
// the YAML.
type draft struct {
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

// files returns the files out holds, by path.
func files(out aggrega.ImportOutput) map[string]string {
	got := map[string]string{}
	for _, f := range out.Files {
		got[f.Path] = f.Content
	}
	return got
}

// drafted reads the Aggregate out drafted, failing the test where it drafted
// none.
func drafted(t *testing.T, out aggrega.ImportOutput) (draft, string) {
	t.Helper()
	text, ok := files(out)["aggregate.yaml"]
	if !ok {
		t.Fatalf("the import drafted no Aggregate: %+v (diagnostics %+v)", out.Files, out.Diagnostics)
	}
	var d draft
	if err := yaml.Unmarshal([]byte(text), &d); err != nil {
		t.Fatalf("the drafted Aggregate does not read as YAML: %v\n%s", err, text)
	}
	return d, text
}

// codes lists each diagnostic as its code and the source node it names.
func codes(out aggrega.ImportOutput) []string {
	var got []string
	for _, d := range out.Diagnostics {
		got = append(got, d.Code+" "+d.SourceRef)
	}
	return got
}

// TestAClassicProjectIsDraftedAsOneAgentPerDeclaration imports the classic
// project's two files: one core/ai-agent/crewai per agent, in the order
// agents.yaml writes them, its role, goal and backstory as declared, its
// allow_delegation as declared or false, and log_level DEBUG for the verbose agent
// and INFO for the other; both wired, on the model socket, to one instance of
// core/ai-model/openai serving gpt-4o; the Release leaving each agent's image for
// the Release; and a decision per agent, the model, the tool — carrying the hint
// naming core/ai-tool/search — and each task, the tool and the tasks choosing
// nothing.
//
// 🔴 PLANTED DEFECT: in writeAgent (render.go), write log_level DEBUG whatever the
// agent declares: the brief writer's row fails.
func TestAClassicProjectIsDraftedAsOneAgentPerDeclaration(t *testing.T) {
	t.Parallel()
	out := importSources("", classic(t)...)
	if len(out.Diagnostics) != 0 {
		t.Errorf("the import reported %+v, want nothing", out.Diagnostics)
	}
	if got := slices.Sorted(maps.Keys(files(out))); !slices.Equal(got, []string{"aggregate.yaml", "release.yaml"}) {
		t.Errorf("the import drafted %v, want the Aggregate and the Release", got)
	}
	d, text := drafted(t, out)
	if order := strings.Index(text, "    researcher:\n") < strings.Index(text, "    brief-writer:\n"); !order {
		t.Errorf("the agents are not drafted in the order agents.yaml writes them:\n%s", text)
	}
	for key, want := range map[string]map[string]any{
		"researcher": {"role": "{topic} Market Researcher\n", "allow_delegation": false, "log_level": "DEBUG"},
		"brief-writer": {"role": "{topic} Brief Writer\n", "allow_delegation": true, "log_level": "INFO",
			"goal": "Turn the research notes into a one-page brief\n"},
	} {
		c := d.Spec.Components[key]
		if c.Type != Component {
			t.Errorf("%s is an instance of %q, want %s", key, c.Type, Component)
		}
		for value, w := range want {
			if c.Values[value] != w {
				t.Errorf("%s's %s is %#v, want %#v", key, value, c.Values[value], w)
			}
		}
		if c.Uses["model"]["gpt-4o"].Target != "gpt-4o" {
			t.Errorf("%s is not wired to gpt-4o on its model socket: %+v", key, c.Uses)
		}
	}
	if m := d.Spec.Components["gpt-4o"]; m.Type != "core/ai-model/openai" || m.Values["model"] != "gpt-4o" {
		t.Errorf("the model is %q serving %#v, want core/ai-model/openai serving gpt-4o", m.Type, m.Values["model"])
	}
	if !strings.Contains(text, "takes no value for it: max_iter.\n") {
		t.Errorf("the Aggregate does not name max_iter as not carried:\n%s", text)
	}
	if !strings.Contains(files(out)["release.yaml"], "    brief-writer: {}\n") {
		t.Errorf("the Release holds no entry for the brief writer:\n%s", files(out)["release.yaml"])
	}

	var kinds []string
	for _, dec := range out.Decisions {
		kinds = append(kinds, dec.SourceNode.Type+" "+dec.SourceNode.ID)
		switch dec.SourceNode.Type {
		case "tool":
			if dec.Chosen != nil || !strings.Contains(dec.SwapHint, "core/ai-tool/search") {
				t.Errorf("the tool's decision is %+v, want no choice and the hint naming core/ai-tool/search", dec)
			}
		case "task":
			if dec.Chosen != nil {
				t.Errorf("the task %s is drafted as %+v; it becomes no Component", dec.SourceNode.ID, dec.Chosen)
			}
		}
	}
	if want := []string{"agent researcher", "llm openai/gpt-4o", "agent brief_writer", "tool search_tool",
		"task research_task", "task brief_task"}; !slices.Equal(kinds, want) {
		t.Errorf("the decisions are %v, want %v", kinds, want)
	}
}

// TestAJSONCProjectIsDraftedAsOneAgentPerDeclaration imports the JSON-first
// project's three files, written with comments and trailing commas: the agents in
// the order crew.jsonc lists them; allow_delegation and verbose read under
// settings as well as at the top of a declaration; the researcher wired to a
// shared gpt-4o and the analyst, whose llm is an object, to a model of its own;
// the object's members other than its model written in a comment above that
// model's Release entry, the key-shaped one named in a credential-carried warning
// and the number beside it in none.
func TestAJSONCProjectIsDraftedAsOneAgentPerDeclaration(t *testing.T) {
	t.Parallel()
	out := importSources("", jsonc(t)...)
	if got := codes(out); !slices.Equal(got, []string{"credential-carried analyst"}) {
		t.Errorf("the import reported %v, want one credential-carried warning naming the analyst", got)
	}
	d, text := drafted(t, out)
	if strings.Index(text, "    researcher:\n") > strings.Index(text, "    analyst:\n") {
		t.Errorf("the agents are not drafted in the order crew.jsonc lists them:\n%s", text)
	}
	researcher, analyst := d.Spec.Components["researcher"], d.Spec.Components["analyst"]
	if researcher.Values["log_level"] != "DEBUG" || researcher.Values["allow_delegation"] != false {
		t.Errorf("the researcher's settings are not read: %+v", researcher.Values)
	}
	if analyst.Values["log_level"] != "INFO" || analyst.Values["allow_delegation"] != true {
		t.Errorf("the analyst's declaration is not read: %+v", analyst.Values)
	}
	if researcher.Uses["model"]["gpt-4o"].Target != "gpt-4o" || analyst.Uses["model"]["gpt-4o-mini"].Target != "gpt-4o-mini" {
		t.Errorf("the agents are wired %+v and %+v", researcher.Uses, analyst.Uses)
	}
	release := files(out)["release.yaml"]
	for _, want := range []string{"    #   api_key: \"sk-planted-crewai-llm-needle-7c41e9d2\"\n", "    #   temperature: 0.2\n",
		"    gpt-4o-mini: {}\n"} {
		if !strings.Contains(release, want) {
			t.Errorf("the Release does not hold %q:\n%s", want, release)
		}
	}
	if !strings.Contains(text, "settings.max_iter") {
		t.Errorf("the Aggregate does not name settings.max_iter as not carried:\n%s", text)
	}
}

// TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares imports the
// classic project, whose agents name their model as a <provider>/<model> string,
// the JSON-first project, whose analyst declares an llm object, and the classic
// project over a catalog holding no OpenAI Component, so its model's Component is
// drafted, and requires the drafted Release to hold an entry for every instance
// the drafted Aggregate declares, the models included: a build places only the
// instances a Release names, so an instance without an entry halts it. A model's
// entry holds no infrastructure, since no declaration states any. Each importer
// package holds a test of this name over the entries it hands the plugin kit's
// Release writer, since each importer decides what its Release holds.
//
// 🔴 PLANTED DEFECT: in crewaiRelease (render.go), skip a model part whose llm
// states no member besides its model: the classic rows fail naming gpt-4o.
func TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares(t *testing.T) {
	t.Parallel()
	agentOnly := &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
		{Name: Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-agent"}},
	}}
	for _, row := range []struct {
		name    string
		sources []aggrega.ImportSource
		view    *aggrega.CatalogProjection
		models  []string
	}{
		{"a model named as a string", classic(t), catalog, []string{"gpt-4o"}},
		{"a model declared as an llm object beside one named as a string", jsonc(t), catalog,
			[]string{"gpt-4o", "gpt-4o-mini"}},
		{"a model whose Component is drafted", classic(t), agentOnly, []string{"gpt-4o"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := Import(aggrega.ImportInput{Format: Format, Sources: row.sources, Catalog: row.view}, hostParse)
			aggregate, _ := drafted(t, out)
			text := files(out)["release.yaml"]
			var release draft
			if err := yaml.Unmarshal([]byte(text), &release); err != nil {
				t.Fatalf("the drafted Release does not read as YAML: %v\n%s", err, text)
			}
			var unplaced []string
			for key := range aggregate.Spec.Components {
				if _, named := release.Spec.Components[key]; !named {
					unplaced = append(unplaced, key)
				}
			}
			if len(unplaced) > 0 {
				slices.Sort(unplaced)
				t.Errorf("the Release names no entry for %v, which the Aggregate declares:\n%s", unplaced, text)
			}
			for _, model := range row.models {
				if !strings.Contains(text, "    "+model+": {}\n") {
					t.Errorf("the Release holds no entry %s holding no infrastructure:\n%s", model, text)
				}
			}
		})
	}
}

// TestTheStrictJSONSpellingIsReadAsTheJSONCOne imports the project spelled
// crew.json and agents/<name>.json: it is read as the JSON-first layout is — the
// agents in the order crew.json lists them, the researcher's settings read, both
// wired to their models — and its analyst's own role and key are what it drafts.
//
// 🔴 PLANTED DEFECT: in declarationOf (crews.go), drop
// `|| strings.HasSuffix(base, ".json")`: an agent's .json file is read by its
// shape into a crew of its own, the crew crew.json declares lists agents it holds
// none of and warns no-nodes, and the case fails on the diagnostics.
func TestTheStrictJSONSpellingIsReadAsTheJSONCOne(t *testing.T) {
	t.Parallel()
	out := importSources("", jsonSpelled(t)...)
	if got := codes(out); !slices.Equal(got, []string{"credential-carried analyst"}) {
		t.Errorf("the import reported %v, want one credential-carried warning naming the analyst", got)
	}
	d, text := drafted(t, out)
	if strings.Index(text, "    researcher:\n") > strings.Index(text, "    analyst:\n") {
		t.Errorf("the agents are not drafted in the order crew.json lists them:\n%s", text)
	}
	if got := d.Spec.Components["analyst"].Values["role"]; got != "{product} Release Notes Writer" {
		t.Errorf("the analyst's role is %#v, want the one agents/analyst.json declares", got)
	}
	if d.Spec.Components["researcher"].Values["log_level"] != "DEBUG" ||
		d.Spec.Components["analyst"].Uses["model"]["gpt-4o-mini"].Target != "gpt-4o-mini" {
		t.Errorf("the strict-JSON declarations are not read as the JSON-first layout's: %+v", d.Spec.Components)
	}
	if !strings.Contains(files(out)["release.yaml"], "sk-planted-crewai-json-needle-3a90b1e5") {
		t.Errorf("the Release does not carry the strict-JSON analyst's key as written:\n%s", files(out)["release.yaml"])
	}
	for _, f := range []string{"agents/analyst.json", "agents/researcher.json", "crew.json"} {
		if !strings.Contains(text, "#   - "+f+"\n") {
			t.Errorf("the Aggregate does not name %s as read:\n%s", f, text)
		}
	}
}

// TestTheJSONCSpellingWinsWhereBothArePresent imports the strict-JSON project
// with the JSON-first project's analyst.jsonc and crew.jsonc handed beside its own
// files: each .json file whose .jsonc twin was handed is not read, as CrewAI does
// not read it, and is named in the Aggregate as not read; the analyst drafted is
// the .jsonc one — its role and its key — and the .json analyst's key reaches
// nothing.
//
// 🔴 PLANTED DEFECT: in readCrews (crews.go), drop the check skipping a .json file
// whose .jsonc twin was handed: two analysts are drafted, the second keyed
// analyst-2, and the case fails.
func TestTheJSONCSpellingWinsWhereBothArePresent(t *testing.T) {
	t.Parallel()
	const jsonKey, jsoncKey = "sk-planted-crewai-json-needle-3a90b1e5", "sk-planted-crewai-llm-needle-7c41e9d2"
	sources := append(jsonSpelled(t),
		aggrega.ImportSource{Name: "agents/analyst.jsonc", Bytes: fixture(t, "jsonc/agents/analyst.jsonc")},
		aggrega.ImportSource{Name: "crew.jsonc", Bytes: fixture(t, "jsonc/crew.jsonc")})
	slices.SortFunc(sources, func(a, b aggrega.ImportSource) int { return strings.Compare(a.Name, b.Name) })
	out := importSources("", sources...)
	d, text := drafted(t, out)
	if got := d.Spec.Components["analyst"].Values["role"]; got != "{product} Release Writer" {
		t.Errorf("the analyst's role is %#v, want the one agents/analyst.jsonc declares", got)
	}
	if _, twice := d.Spec.Components["analyst-2"]; twice {
		t.Errorf("the analyst is drafted twice:\n%s", text)
	}
	want := "# Not read, since CrewAI reads the .jsonc file where a crew or an agent is written in both spellings: " +
		"agents/analyst.json, crew.json.\n"
	if !strings.Contains(text, want) {
		t.Errorf("the Aggregate does not name the .json files not read, %q:\n%s", want, text)
	}
	all, _ := json.Marshal(out)
	if !strings.Contains(string(all), jsoncKey) || strings.Contains(string(all), jsonKey) {
		t.Errorf("the import carries the .jsonc analyst's key: %v, the .json analyst's: %v; want only the first",
			strings.Contains(string(all), jsoncKey), strings.Contains(string(all), jsonKey))
	}
}

// TestTheEmptyCredentialsSettingDecidesWhatTheLLMObjectCarries imports the
// JSON-first project under each value of empty_credentials: the key-shaped member
// is carried as written under none and marked, since CrewAI marks no value as a
// credential, and written with no value under all, reported under
// credential-emptied; a value the setting does not take halts and drafts nothing.
//
// 🔴 PLANTED DEFECT: in located (crewaiimport.go), give the members the class
// importer.CredentialMarked: the marked row writes the key empty, and fails.
func TestTheEmptyCredentialsSettingDecidesWhatTheLLMObjectCarries(t *testing.T) {
	t.Parallel()
	const key = "sk-planted-crewai-llm-needle-7c41e9d2"
	for _, row := range []struct {
		setting, code string
		carried       bool
	}{
		{`{"empty_credentials":"none"}`, "credential-carried", true},
		{`{"empty_credentials":"marked"}`, "credential-carried", true},
		{`{"empty_credentials":"all"}`, "credential-emptied", false},
	} {
		out := importSources(row.setting, jsonc(t)...)
		release := files(out)["release.yaml"]
		if got := codes(out); !slices.Equal(got, []string{row.code + " analyst"}) {
			t.Errorf("%s: the import reported %v, want %s naming the analyst", row.setting, got, row.code)
		}
		if strings.Contains(release, key) != row.carried {
			t.Errorf("%s: the Release carries the key: %v, want %v\n%s", row.setting, !row.carried, row.carried, release)
		}
		if !row.carried && !strings.Contains(release, "    #   api_key\n") {
			t.Errorf("%s: the Release does not name api_key with no value:\n%s", row.setting, release)
		}
	}
	halted := importSources(`{"empty_credentials":"some"}`, jsonc(t)...)
	if len(halted.Files) != 0 || len(halted.Diagnostics) != 1 || halted.Diagnostics[0].Severity != "halt" {
		t.Errorf("a value the setting does not take drafted %+v and reported %+v", halted.Files, halted.Diagnostics)
	}
}

// TestHowAnAgentsLLMIsWired drafts one agent per row of a classic agents.yaml and
// requires each llm wired, or left, as the importer reads it: a provider and a
// model wired to the catalog's Component of the provider — an underscore read as
// a hyphen, azure as azure-openai; a provider the catalog lacks drafted as a
// Component under the target namespace; and an llm naming no provider, a
// reference into the program, and none at all, each left unwired with the reason
// written above the agent.
func TestHowAnAgentsLLMIsWired(t *testing.T) {
	t.Parallel()
	agents := strings.Join([]string{
		"openai:\n  role: a\n  goal: b\n  backstory: c\n  llm: openai/gpt-4o\n",
		"azure:\n  role: a\n  goal: b\n  backstory: c\n  llm: azure/my-deployment\n",
		"groq:\n  role: a\n  goal: b\n  backstory: c\n  llm: groq/llama-3.1-8b-instant\n",
		"bare:\n  role: a\n  goal: b\n  backstory: c\n  llm: gpt-4o\n",
		"none:\n  role: a\n  goal: b\n  backstory: c\n",
	}, "")
	out := importSources("", aggrega.ImportSource{Name: "config/agents.yaml", Bytes: []byte(agents)})
	d, text := drafted(t, out)
	for agent, want := range map[string]struct{ part, component string }{
		"openai": {"gpt-4o", "core/ai-model/openai"},
		"azure":  {"my-deployment", "core/ai-model/azure-openai"},
		"groq":   {"llama-3-1-8b-instant", "imported/ai-model/groq"},
	} {
		if got := d.Spec.Components[agent].Uses["model"][want.part].Target; got != want.part {
			t.Errorf("%s is wired to %q, want %s", agent, got, want.part)
		}
		if got := d.Spec.Components[want.part].Type; got != want.component {
			t.Errorf("%s's model is an instance of %q, want %s", agent, got, want.component)
		}
	}
	for _, agent := range []string{"bare", "none"} {
		if uses := d.Spec.Components[agent].Uses; len(uses) != 0 {
			t.Errorf("%s is wired %+v, want nothing", agent, uses)
		}
	}
	for _, why := range []string{"names no provider before a slash", "is not declared"} {
		if !strings.Contains(text, "since the agent's llm "+why) {
			t.Errorf("the Aggregate does not say the agent's llm %s:\n%s", why, text)
		}
	}
	component, ok := files(out)["components/imported-ai-model-groq.yaml"]
	if !ok || !strings.Contains(component, "name: imported/ai-model/groq\n") ||
		!strings.Contains(component, "model.aggrega.ai/api label") {
		t.Errorf("the groq Component is not drafted, its label left for the author: %v\n%s", ok, component)
	}
	var draftedGroq bool
	for _, dec := range out.Decisions {
		draftedGroq = draftedGroq || (dec.Chosen != nil && dec.Chosen.Target == "imported/ai-model/groq" &&
			dec.Chosen.Mode == "draft")
	}
	if !draftedGroq {
		t.Errorf("no decision reports the groq Component drafted: %+v", out.Decisions)
	}

	ref := importSources("", aggrega.ImportSource{Name: "agents/x.jsonc",
		Bytes: []byte(`{"role": "a", "goal": "b", "backstory": "c", "llm": {"python": "crew.models.llm"}}`)})
	if _, text := drafted(t, ref); !strings.Contains(text, "is a reference into the crew's program") {
		t.Errorf("an llm referring into the program is not left unwired with its reason:\n%s", text)
	}
}

// TestATextIsCarriedAsWrittenThroughTheTemplateReading drafts an agent whose role
// holds two opening braces and a placeholder, and requires the drafted Aggregate to
// read as YAML as drafted and, once its text is evaluated as a template — as the
// compiler evaluates an Aggregate before reading it as YAML — to carry the role as
// written.
//
// 🔴 PLANTED DEFECT: make EscapeTemplateText (aggrega-pdk/go/pkg/importer) write
// {{"{{"}}: the double-quoted scalar escapes its quotes with backslashes, and the
// template engine refuses the text.
func TestATextIsCarriedAsWrittenThroughTheTemplateReading(t *testing.T) {
	t.Parallel()
	role := "Answer {{question}} about {topic}"
	out := importSources("", aggrega.ImportSource{Name: "config/agents.yaml",
		Bytes: []byte("a:\n  role: \"" + role + "\"\n  goal: g\n  backstory: b\n  llm: openai/gpt-4o\n")})
	d, text := drafted(t, out)
	if got, _ := d.Spec.Components["a"].Values["role"].(string); got == role {
		t.Errorf("the role is drafted unescaped, %q, so the template reading would read its braces as an action", got)
	}
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
	if got := read.Spec.Components["a"].Values["role"]; got != role {
		t.Errorf("the role reads back as %#v once evaluated, want %q", got, role)
	}
}

// TestWhatTheDeclarationsLeaveUndraftedIsSaid drafts a crew whose crew.jsonc lists
// an agent the import was not handed, and an agent whose verbose is not true or
// false: the missing agent is named in the Aggregate and its decision chooses
// nothing; the other's log_level is left to the Component's default and named.
func TestWhatTheDeclarationsLeaveUndraftedIsSaid(t *testing.T) {
	t.Parallel()
	out := importSources("",
		aggrega.ImportSource{Name: "agents/writer.jsonc",
			Bytes: []byte(`{"role": "w", "goal": "g", "backstory": "b", "llm": "openai/gpt-4o", "verbose": 2}`)},
		aggrega.ImportSource{Name: "crew.jsonc", Bytes: []byte(`{"agents": ["planner", "writer"], "tasks": []}`)})
	d, text := drafted(t, out)
	if _, ok := d.Spec.Components["writer"].Values["log_level"]; ok {
		t.Errorf("a verbose that is not true or false is drafted as a log level: %+v", d.Spec.Components["writer"].Values)
	}
	for _, want := range []string{"# Not drafted: the agent planner,", "verbose, since it is not true or false"} {
		if !strings.Contains(text, want) {
			t.Errorf("the Aggregate does not say %q:\n%s", want, text)
		}
	}
	var missing bool
	for _, dec := range out.Decisions {
		missing = missing || (dec.SourceNode.ID == "planner" && dec.Chosen == nil)
	}
	if !missing {
		t.Errorf("no decision reports the planner left undrafted: %+v", out.Decisions)
	}
}

// TestASourceHandedWithNoNameIsReadByItsShape imports a classic agents.yaml and
// an agent's JSON-with-comments file, each as the primary bytes of a request, as
// an import of a lone file hands it: each is read by its shape and drafted — the
// agents of agents.yaml under their names, and the agent of a file whose name the
// import was not handed under agent.
func TestASourceHandedWithNoNameIsReadByItsShape(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		fixture, key string
	}{
		{"classic/src/market_brief/config/agents.yaml", "researcher"},
		{"jsonc/agents/researcher.jsonc", "agent"},
	} {
		out := Import(aggrega.ImportInput{Format: Format, Bytes: fixture(t, row.fixture), Catalog: catalog}, hostParse)
		d, _ := drafted(t, out)
		if d.Spec.Components[row.key].Type != Component {
			t.Errorf("%s handed with no name drafts %+v, want an agent keyed %s", row.fixture, d.Spec.Components, row.key)
		}
	}
}

// TestASourceTheParserRefusesHaltsAndACrewDeclaringNoAgentWarns imports a
// crew.jsonc cut short, which halts under parse-failed with the parser's reason,
// and a tasks.yaml alone, which declares no agent, warns no-nodes and drafts
// nothing; a request for another format reads nothing.
func TestASourceTheParserRefusesHaltsAndACrewDeclaringNoAgentWarns(t *testing.T) {
	t.Parallel()
	refused := importSources("", aggrega.ImportSource{Name: "crew.jsonc", Bytes: []byte("{\"agents\": [\n")})
	if got := codes(refused); !slices.Equal(got, []string{"parse-failed crew.jsonc"}) ||
		!strings.Contains(refused.Diagnostics[0].Message, "json parse:") {
		t.Errorf("a source the parser refuses reported %+v, want parse-failed with the parser's reason", refused.Diagnostics)
	}
	tasks := importSources("", aggrega.ImportSource{Name: "config/tasks.yaml",
		Bytes: fixture(t, "classic/src/market_brief/config/tasks.yaml")})
	if got := codes(tasks); !slices.Equal(got, []string{"no-nodes config/tasks.yaml"}) || len(tasks.Files) != 0 {
		t.Errorf("a crew declaring no agent reported %v and drafted %v", got, tasks.Files)
	}
	other := Import(aggrega.ImportInput{Format: "mcp", Sources: classic(t), Catalog: catalog}, hostParse)
	if len(other.Files) != 0 {
		t.Errorf("a request for another format drafted %v", other.Files)
	}
}

// TestTwoImportsAreByteIdentical imports each committed project twice and
// requires the same output, byte for byte.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	t.Parallel()
	for name, sources := range map[string][]aggrega.ImportSource{"classic": classic(t), "jsonc": jsonc(t),
		"json": jsonSpelled(t)} {
		first, _ := json.Marshal(importSources("", sources...))
		second, _ := json.Marshal(importSources("", sources...))
		if string(first) != string(second) {
			t.Errorf("two imports of %s differ", name)
		}
	}
}

// TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments imports a classic
// project whose agent and tool are named with "{{", each name written into a
// comment of the Aggregate, and requires the Aggregate to run as a template — as
// the compiler evaluates its whole text, comments included — and each comment to
// read back with the name as the declaration writes it.
//
// 🔴 PLANTED DEFECT: in importer.CommentLine
// (aggrega-pdk/go/pkg/importer/definition.go), return the line unescaped: the
// Aggregate no longer parses as a template, and the case fails on it.
func TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments(t *testing.T) {
	out := importSources("", aggrega.ImportSource{Name: "config/agents.yaml", Bytes: []byte(
		"\"writer{{x}}\":\n  role: r\n  goal: g\n  backstory: b\n  llm: openai/gpt-4o\n  tools:\n    - \"search{{ y }}\"\n")})
	_, text := drafted(t, out)
	got := evaluatedAggregate(t, text)
	for _, want := range []string{"# The agent writer{{x}}.\n", "drafted as no Component: search{{ y }}.\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the evaluated Aggregate does not say %q:\n%s", want, got)
		}
	}
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
