// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package adkimport

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
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// The Google ADK importer on the host toolchain: each case hands Import a
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

// fixture reads one file of a committed ADK application
// (internal/importops/testdata/google-adk).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "importops", "testdata", "google-adk",
		filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return body
}

// categoryLabel is the label a catalog Component's category is read from.
const categoryLabel = "component.aggrega.ai/category"

// catalog is a view of a workspace catalog holding the standard library's ADK
// agent and Gemini model, with the labels the hub gives them.
var catalog = &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
	{Name: Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-agent"}},
	{Name: modelComponent, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-model",
		"model.aggrega.ai/api": "gemini"}},
}}

// importSources imports sources, named, against view, with settings.
func importSources(view *aggrega.CatalogProjection, settings string, sources ...aggrega.ImportSource) aggrega.ImportOutput {
	in := aggrega.ImportInput{Format: Format, Sources: sources, Catalog: view}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse)
}

// tutor and pipeline are the committed applications' configurations as an
// import of the application's directory hands them with its sub-agents'
// configurations named under the attachment's with.sources: the root_agent.yaml
// the directory hands, by its name inside the application, and each file
// with.sources names, under its base name — for tutor, lesson_plan.yaml too, a
// file no configuration names — in name order.
func tutor(t *testing.T) []aggrega.ImportSource {
	t.Helper()
	var out []aggrega.ImportSource
	for _, name := range []string{"code_tutor_agent.yaml", "lesson_plan.yaml", "math_tutor_agent.yaml", "root_agent.yaml"} {
		out = append(out, aggrega.ImportSource{Name: name, Bytes: fixture(t, "tutor/"+name)})
	}
	return out
}

func pipeline(t *testing.T) []aggrega.ImportSource {
	t.Helper()
	return []aggrega.ImportSource{
		{Name: "drafter.yaml", Bytes: fixture(t, "pipeline/agents/drafter.yaml")},
		{Name: "reviewer.yaml", Bytes: fixture(t, "pipeline/agents/reviewer.yaml")},
		{Name: "root_agent.yaml", Bytes: fixture(t, "pipeline/root_agent.yaml")},
	}
}

// The key-shaped values the tutor application plants: the api_key a tool's args
// carries, and the header its math tutor's model requests send. Neither is a
// real key.
const (
	plantedArg    = "AIzaPlantedAdkToolArgNeedle4f2c9b71"
	plantedHeader = "adk-planted-header-needle-8e1d07a3"
)

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

// coded returns the messages of out's diagnostics of code.
func coded(out aggrega.ImportOutput, code string) []string {
	var got []string
	for _, d := range out.Diagnostics {
		if d.Code == code {
			got = append(got, d.Message)
		}
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

// TestEachCommittedApplicationIsDraftedAsOneInstancePerTree imports each
// committed application as a directory hands it and requires one instance of
// core/ai-agent/google-adk for its tree, keyed by its root agent and carrying its
// description — its double braces escaped, so it reads back as written — wired
// on its model socket to an instance of core/ai-model/gemini serving the model
// the first agent naming one names; every other agent, every sub-agent not
// read, and every tool reported in a decision choosing nothing; the class ADK
// deprecates named; the handed file no configuration names named as not read;
// and nothing reported but one credential-carried warning, in order, for each
// value the configurations hand their tools and their model requests.
//
// 🔴 PLANTED DEFECT: in modelAgent (adkimport.go), return app.root: the pipeline's
// root names no model, so its tree is left unwired and the case fails on it.
func TestEachCommittedApplicationIsDraftedAsOneInstancePerTree(t *testing.T) {
	cases := []struct {
		name                 string
		sources              []aggrega.ImportSource
		key, model, part     string
		description          string
		agents, tools, notes []string
		// carried are the fields, by their agent, whose values the fixture's
		// configurations hand their tools and model requests: each raises one
		// credential-carried warning, and nothing else is reported.
		carried []string
	}{
		{
			name: "tutor", sources: tutor(t), key: "tutor-root", model: "gemini-flash-latest",
			part:        "gemini-flash-latest",
			description: "Learning assistant that tutors {{`{{`}} students }} in code and math.",
			agents:      []string{"code_tutor_agent", "math_tutor_agent", "tutor_agents.review_agent"},
			tools:       []string{"google_search", "tutor_tools.make_snippet_runner"},
			notes: []string{"# Not read, since no agent configuration names it: lesson_plan.yaml.\n",
				"#   - code_tutor_agent.yaml\n", "#   - math_tutor_agent.yaml\n",
				"# ADK states that its Agent Config is experimental. An agent's model is a Gemini model by default; " +
					"ADK serves some other providers' models by name through classes of its own, such as " +
					"claude-sonnet-4-5 and gpt-4o, and others through a LiteLLM model string.\n",
				"# Models other agents of the tree ask for, each with the family ADK serves it through; one whose " +
					"requests ADK sends to the Gemini API is sent with the key this instance's model connection " +
					"delivers: gemini-pro-latest (math_tutor_agent, served through Gemini).\n",
				"# Not read: a sub-agent of tutor_root, which is built by the application's code at " +
					"tutor_agents.review_agent"},
			carried: []string{"tools.tutor_tools.make_snippet_runner.args.api_key of node code_tutor_agent",
				"tools.tutor_tools.make_snippet_runner.args.sandbox of node code_tutor_agent",
				"generate_content_config.http_options.headers of node math_tutor_agent"},
		},
		{
			name: "pipeline", sources: pipeline(t), key: "review-pipeline", model: "gemini-2.5-flash",
			part:        "gemini-2-5-flash",
			description: "Drafts a release note, then reviews it against the house style.",
			agents:      []string{"drafter", "reviewer"},
			tools:       []string{"review_tools.make_style_checker"},
			notes: []string{"# Deprecated by ADK in favour of Workflow: the SequentialAgent review_pipeline.\n",
				"#   - drafter.yaml\n", "#   - reviewer.yaml\n"},
			carried: []string{"tools.review_tools.make_style_checker.args.style_guide of node reviewer"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := importSources(catalog, "", tc.sources...)
			var reported []string
			for _, d := range out.Diagnostics {
				reported = append(reported, d.Code+": "+d.Message)
			}
			if len(reported) != len(tc.carried) {
				t.Fatalf("the import reported %q, want one credential-carried warning per field %q", reported,
					tc.carried)
			}
			for i, field := range tc.carried {
				if want := "credential-carried: the value of " + field + " is carried as written"; !strings.HasPrefix(
					reported[i], want) {
					t.Errorf("diagnostic %d is %q, want it to open %q", i, reported[i], want)
				}
			}
			d, text := drafted(t, out)
			root := d.Spec.Components[tc.key]
			if root.Type != Component {
				t.Fatalf("%s is an instance of %q, want %s:\n%s", tc.key, root.Type, Component, text)
			}
			if !strings.Contains(text, "description: "+string(must(json.Marshal(tc.description)))) {
				t.Errorf("the instance does not carry the description %q escaped:\n%s", tc.description, text)
			}
			if got := root.Uses[modelSocket][tc.part].Target; got != tc.part {
				t.Errorf("the model connection %s targets %q, want %q:\n%s", tc.part, got, tc.part, text)
			}
			part := d.Spec.Components[tc.part]
			if part.Type != modelComponent || part.Values["model"] != tc.model {
				t.Errorf("the model part is %q serving %#v, want %s serving %s", part.Type, part.Values["model"],
					modelComponent, tc.model)
			}
			if len(d.Spec.Components) != 2 {
				t.Errorf("the Aggregate holds %d instances, want the tree's and its model's:\n%s",
					len(d.Spec.Components), text)
			}
			for _, note := range tc.notes {
				if !strings.Contains(text, note) {
					t.Errorf("the Aggregate does not say %q:\n%s", note, text)
				}
			}
			for _, id := range tc.agents {
				if got := decision(t, out, id); got.Chosen != nil || got.SourceNode.Type != "agent" {
					t.Errorf("the agent %s is decided as %+v; it becomes no instance of its own", id, got)
				}
			}
			for _, id := range tc.tools {
				if got := decision(t, out, id); got.Chosen != nil || got.SourceNode.Type != "tool" {
					t.Errorf("the tool %s is decided as %+v; it becomes no Component", id, got)
				}
			}
			if got := decision(t, out, rootName(tc.name)); got.Chosen == nil || got.Chosen.Target != Component {
				t.Errorf("the root agent is decided as %+v, want an instance of %s", got, Component)
			}
			var models int
			for _, dec := range out.Decisions {
				if dec.SourceNode.Type != "model" {
					continue
				}
				models++
				if want := "ADK serves the model " + tc.model + " through its Gemini class, which sends its requests to " +
					"the Gemini API, so it is drafted as an instance of the Gemini Component; the catalog holds " +
					modelComponent + ", so " + tc.part + " is an instance of it"; !strings.HasPrefix(dec.Rationale, want) {
					t.Errorf("the model %s is decided as %q, want it to open %q", dec.SourceNode.ID, dec.Rationale, want)
				}
			}
			if models != 1 {
				t.Errorf("the import reports %d model decisions, want the tree's model's alone", models)
			}
			if !strings.Contains(files(out)["release.yaml"], "#   - infra.image, the container image running the "+
				"application built on ADK, holding its configurations\n") {
				t.Errorf("the Release does not leave the image for the Release:\n%s", files(out)["release.yaml"])
			}
		})
	}
}

// TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares imports each
// committed application, and the tutor over a catalog holding no Gemini
// Component, so its model's Component is drafted, and requires the drafted
// Release to hold an entry for every instance the drafted Aggregate declares, the
// model included: a build places only the instances a Release names, so an
// instance without an entry halts it. The model's entry holds no infrastructure,
// since no configuration states any. Each importer package holds a test of this
// name over its own renderer, since each importer renders its own Release and
// the plugin kit renders none.
//
// 🔴 PLANTED DEFECT: in renderRelease (render.go), drop the loop over the model
// parts: every row fails naming its model.
func TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares(t *testing.T) {
	agentOnly := &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
		{Name: Component, Version: "1.0.0", Labels: map[string]string{categoryLabel: "ai-agent"}},
	}}
	for _, row := range []struct {
		name, model string
		sources     []aggrega.ImportSource
		view        *aggrega.CatalogProjection
	}{
		{"tutor", "gemini-flash-latest", tutor(t), catalog},
		{"pipeline", "gemini-2-5-flash", pipeline(t), catalog},
		{"tutor, its model's Component drafted", "gemini-flash-latest", tutor(t), agentOnly},
	} {
		t.Run(row.name, func(t *testing.T) {
			out := importSources(row.view, "", row.sources...)
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
			if !strings.Contains(text, "    "+row.model+":\n      infra: {}\n") {
				t.Errorf("the Release holds no entry %s holding no infrastructure:\n%s", row.model, text)
			}
		})
	}
}

// rootName is the name of the root agent of the committed application app.
func rootName(app string) string {
	if app == "tutor" {
		return "tutor_root"
	}
	return "review_pipeline"
}

// must returns v, panicking on err.
func must[V any](v V, err error) V {
	if err != nil {
		panic(err)
	}
	return v
}

// TestTheEmptyCredentialsSettingDecidesWhatIsCarried imports the tutor
// application with the empty_credentials setting absent, none, marked and all,
// after a needle proves each planted value is in its configuration: where it is
// absent, none or marked, each is carried as written into release.yaml, reaches
// no other file and no diagnostic, and is named in a credential-carried warning by
// its agent and its field, as is the other text the tool's args carry; where it
// is all, neither reaches anything and each is named in a credential-emptied
// warning. The numbers beside them carry no key and are named in neither.
//
// 🔴 PLANTED DEFECT: in located (adkimport.go), return nothing: the all row
// carries both values as written.
func TestTheEmptyCredentialsSettingDecidesWhatIsCarried(t *testing.T) {
	sources := tutor(t)
	for _, needle := range []struct{ file, value string }{{"code_tutor_agent.yaml", plantedArg},
		{"math_tutor_agent.yaml", plantedHeader}} {
		if !strings.Contains(string(fixture(t, "tutor/"+needle.file)), needle.value) {
			t.Fatalf("the needle %s is not in %s, so nothing here can show where it reaches", needle.value, needle.file)
		}
	}
	fields := []string{"tools.tutor_tools.make_snippet_runner.args.api_key of node code_tutor_agent",
		"tools.tutor_tools.make_snippet_runner.args.sandbox of node code_tutor_agent",
		"generate_content_config.http_options.headers of node math_tutor_agent"}
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			settings := ""
			if setting != "absent" {
				settings = `{"empty_credentials":"` + setting + `"}`
			}
			out := importSources(catalog, settings, sources...)
			code := "credential-carried"
			if setting == "all" {
				code = "credential-emptied"
			}
			named := coded(out, code)
			if len(named) != len(fields) {
				t.Fatalf("the %s warnings are %q, want one per field %q", code, named, fields)
			}
			for i, field := range fields {
				if !strings.Contains(named[i], field) {
					t.Errorf("the %s warning %q does not name %s", code, named[i], field)
				}
			}
			for _, d := range out.Diagnostics {
				for _, planted := range []string{plantedArg, plantedHeader} {
					if strings.Contains(d.Message, planted) {
						t.Errorf("a diagnostic shows a planted value: %+v", d)
					}
				}
				if strings.Contains(d.Message, "timeout") {
					t.Errorf("a diagnostic names a number, which carries no key: %+v", d)
				}
			}
			for path, content := range files(out) {
				for _, planted := range []string{plantedArg, plantedHeader} {
					reaches := strings.Contains(content, planted)
					if want := setting != "all" && path == "release.yaml"; reaches != want {
						t.Errorf("the planted %s reaches %s: %v, want %v", planted, path, reaches, want)
					}
				}
			}
			for _, d := range out.Decisions {
				raw, _ := json.Marshal(d)
				for _, planted := range []string{plantedArg, plantedHeader} {
					if strings.Contains(string(raw), planted) {
						t.Errorf("a decision shows a planted value: %s", raw)
					}
				}
			}
		})
	}
}

// TestASubAgentTheImportWasNotHandedIsReported imports the tutor's root
// configuration handed alone — as a file imported on its own is, and as a
// directory import hands it, its sub-agents' configurations named under no
// with.sources: the tree is drafted from its root, each sub-agent it names by
// config_path is reported as not handed, in its decision and in the Aggregate,
// with how to hand it, and the one it names by code as the application's.
//
// 🔴 PLANTED DEFECT: in readApplication (agents.go), leave notHanded unset: the
// case fails on the missing hint.
func TestASubAgentTheImportWasNotHandedIsReported(t *testing.T) {
	root := fixture(t, "tutor/root_agent.yaml")
	for name, in := range map[string]aggrega.ImportInput{
		"a file imported on its own": {Format: Format, Bytes: root, Catalog: catalog},
		"a directory's root_agent.yaml": {Format: Format, Catalog: catalog,
			Sources: []aggrega.ImportSource{{Name: "tutor/root_agent.yaml", Bytes: root}}},
	} {
		t.Run(name, func(t *testing.T) {
			out := Import(in, hostParse)
			d, text := drafted(t, out)
			if d.Spec.Components["tutor-root"].Type != Component {
				t.Fatalf("the tree is not drafted from its root:\n%s", text)
			}
			for id, why := range map[string]string{
				"code_tutor_agent.yaml": "names code_tutor_agent.yaml, which the import was not handed, so it becomes " +
					"no Component; name the file under the Google ADK importer's with.sources to have it read",
				"math_tutor_agent.yaml": "names math_tutor_agent.yaml, which the import was not handed, so it becomes " +
					"no Component; name the file under the Google ADK importer's with.sources to have it read",
				"tutor_agents.review_agent": "is built by the application's code at tutor_agents.review_agent",
			} {
				if got := decision(t, out, id); !strings.Contains(got.Rationale, why) {
					t.Errorf("the sub-agent %s is reported as %q, want it to say it %s", id, got.Rationale, why)
				}
			}
			for _, f := range []string{"code_tutor_agent.yaml", "math_tutor_agent.yaml"} {
				if want := "# Not read: a sub-agent of tutor_root, which names " + f + ", which the import was not " +
					"handed. Name the file under the Google ADK importer's with.sources to have it read.\n"; !strings.Contains(
					text, want) {
					t.Errorf("the Aggregate does not say %q:\n%s", want, text)
				}
			}
		})
	}
}

// TestARequestHoldingNoRootReadsNoTree hands the importer configurations under
// with.sources alone, none of them a root_agent.yaml and no file imported on its
// own: a source with.sources hands is read only as a configuration a root's tree
// names, so no tree is read, nothing is drafted, and each source is reported
// under no-nodes.
//
// 🔴 PLANTED DEFECT: in reader.roots (agents.go), treat every named source as a
// root: the case fails on the drafted files.
func TestARequestHoldingNoRootReadsNoTree(t *testing.T) {
	out := importSources(catalog, "",
		aggrega.ImportSource{Name: "helper.yaml", Bytes: []byte("name: helper\ninstruction: Help.\n")},
		aggrega.ImportSource{Name: "main.yaml",
			Bytes: []byte("name: main\ninstruction: Lead.\nsub_agents:\n  - config_path: helper.yaml\n")})
	if len(out.Files) != 0 || len(out.Entities) != 0 {
		t.Errorf("a request holding no root drafted %d files and %d entities", len(out.Files), len(out.Entities))
	}
	if got := coded(out, "no-nodes"); len(got) != 2 {
		t.Errorf("a request holding no root reports %q under no-nodes, want one per source", got)
	}
}

// TestAConfigPathResolvesAsADKResolvesIt holds resolve to ADK's reading of a
// config_path: from the directory of the configuration naming it, refusing an
// absolute path and one leading out of that directory.
func TestAConfigPathResolvesAsADKResolvesIt(t *testing.T) {
	for _, tc := range []struct {
		file, configPath, want string
		refused                bool
	}{
		{"root_agent.yaml", "code.yaml", "code.yaml", false},
		{"root_agent.yaml", "agents/code.yaml", "agents/code.yaml", false},
		{"apps/tutor/root_agent.yaml", "agents/code.yaml", "apps/tutor/agents/code.yaml", false},
		{"apps/tutor/root_agent.yaml", "../tutor/code.yaml", "apps/tutor/code.yaml", false},
		{"", "code.yaml", "code.yaml", false},
		{"root_agent.yaml", "../code.yaml", "", true},
		{"apps/tutor/root_agent.yaml", "../other/code.yaml", "", true},
		{"apps/tutor/root_agent.yaml", "/etc/code.yaml", "", true},
		{"apps/tutor/root_agent.yaml", "..", "", true},
	} {
		got, why := resolve(tc.file, tc.configPath)
		if (why != "") != tc.refused || got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, %q; want %q, refused %v", tc.file, tc.configPath, got, why, tc.want,
				tc.refused)
		}
	}
}

// TestHowATreesModelIsWired drives each way a tree's model is decided: a model
// whose requests ADK sends to the Gemini API wired, and its decision naming the
// API its class reaches; a tree naming none, one whose model the code builds,
// and one naming a model ADK sends elsewhere, left unwired and saying why; a
// sub-agent's model under an unwired root; two trees naming one model sharing
// one part; and,
// where the catalog view holds no Gemini Component, one drafted under the target
// namespace carrying the label the agent's model socket selects.
//
// 🔴 PLANTED DEFECT: in wire (adkimport.go), drop the shared lookup: the two
// trees draft two parts and the sharing row fails.
func TestHowATreesModelIsWired(t *testing.T) {
	named := func(name, body string) aggrega.ImportSource {
		return aggrega.ImportSource{Name: name, Bytes: []byte(body)}
	}
	t.Run("a tree naming no model", func(t *testing.T) {
		out := importSources(catalog, "", named("root_agent.yaml", "name: quiet\ninstruction: Answer.\n"))
		d, text := drafted(t, out)
		if len(d.Spec.Components["quiet"].Uses) != 0 || !strings.Contains(text,
			"since no agent of the tree names a model, so ADK uses its default model") {
			t.Errorf("a tree naming no model is drafted as:\n%s", text)
		}
	})
	t.Run("a model the code builds", func(t *testing.T) {
		out := importSources(catalog, "", named("root_agent.yaml",
			"name: coded\ninstruction: Answer.\nmodel_code:\n  name: models.lite_llm\n"))
		d, text := drafted(t, out)
		if len(d.Spec.Components["coded"].Uses) != 0 || !strings.Contains(text,
			"built by the application's code at models.lite_llm") {
			t.Errorf("a tree whose model the code builds is drafted as:\n%s", text)
		}
	})
	// A model whose requests ADK does not send to the Gemini API is left unwired,
	// the draft naming the family ADK serves it through: a LiteLLM model string,
	// and a Vertex AI resource name, which ADK's Gemini class sends to Vertex AI.
	// token is a part of the model string a Release entry for it would carry.
	for _, row := range []struct{ name, model, family, token string }{
		{"a LiteLLM model string", "openai/gpt-4o", "LiteLLM", "gpt-4o"},
		{"a Vertex AI resource name", "projects/acme/locations/us-central1/publishers/google/models/gemini-2.5-pro",
			"Vertex AI", "acme"},
	} {
		t.Run(row.name, func(t *testing.T) {
			out := importSources(catalog, "", named("root_agent.yaml",
				"name: routed\ninstruction: Answer.\nmodel: "+row.model+"\n"))
			d, text := drafted(t, out)
			why := "the model " + row.model + ", which routed names, is one ADK serves through " + row.family +
				", not through the Gemini API, while core/ai-agent/google-adk takes a Gemini model on its model " +
				"socket and reads no model name from its environment, so the author wires a Gemini model there and " +
				"names that model in routed's configuration; the build halts naming the socket until one is wired"
			if len(d.Spec.Components) != 1 || len(d.Spec.Components["routed"].Uses) != 0 {
				t.Errorf("a tree naming %s is drafted wired, or with a model part:\n%s", row.model, text)
			}
			if want := "    # The model connection is left for the author, since " + why + ".\n    routed:\n"; !strings.Contains(
				text, want) {
				t.Errorf("the Aggregate does not say %q:\n%s", want, text)
			}
			if got := decision(t, out, "routed"); !strings.Contains(got.Rationale,
				"; its model socket is left for the author, since "+why) {
				t.Errorf("the root agent is decided as %q, want it to say %q", got.Rationale, why)
			}
			for _, dec := range out.Decisions {
				if dec.SourceNode.Type == "model" {
					t.Errorf("the model %s is decided as a model part: %+v", row.model, dec)
				}
			}
			if release := files(out)["release.yaml"]; strings.Contains(release, row.token) ||
				strings.Contains(release, "the credential routed presents to the model") {
				t.Errorf("the Release names an entry or a credential for the unwired model:\n%s", release)
			}
		})
	}
	// A tree whose root model is unwired has no connection whose key another
	// agent's model could be sent with, so the Aggregate names no such line, and a
	// sub-agent's model is decided with the family ADK serves it through alone,
	// saying nothing of a connection the author has yet to wire.
	t.Run("a sub-agent's model under an unwired root", func(t *testing.T) {
		out := importSources(catalog, "",
			named("helper.yaml", "name: helper\ninstruction: Help.\nmodel: gemini-flash-latest\n"),
			named("root_agent.yaml", "name: routed\ninstruction: Answer.\nmodel: openai/gpt-4o\nsub_agents:\n"+
				"  - config_path: helper.yaml\n"))
		_, text := drafted(t, out)
		if strings.Contains(text, "Models other agents of the tree ask for") {
			t.Errorf("the Aggregate names the key of a connection the unwired root does not have:\n%s", text)
		}
		want := "; it asks for the model gemini-flash-latest, which ADK serves through Gemini"
		got := decision(t, out, "helper").Rationale
		if !strings.Contains(got, want) || strings.Contains(got, "connection") {
			t.Errorf("the sub-agent is decided as %q, want it to say %q and name no connection", got, want)
		}
	})
	// A model whose requests ADK sends to the Gemini API is wired, serving the
	// name its class is handed: a Gemma 3 model, served by ADK's Gemma class,
	// which supports the Gemini API only, and a model written as gemini:model,
	// served by its Gemini class, which reaches the Gemini API or Vertex AI as the
	// application's environment chooses.
	gemmaOnly := "; ADK's Gemma class supports the Gemini API only, not Vertex AI, and the model socket of " +
		Component + " admits the Gemini API alone"
	environment := "; the application reaches Gemini through the Gemini API or through Vertex AI as its " +
		"environment chooses"
	for _, row := range []struct{ name, model, part, served, family, backend, not string }{
		{"a Gemma 3 model", "gemma-3-27b-it", "gemma-3-27b-it", "gemma-3-27b-it", "Gemma", gemmaOnly, environment},
		{"a model named with ADK's Gemini class", "gemini:gemini-2.5-flash", "gemini-2-5-flash", "gemini-2.5-flash",
			"Gemini", environment, gemmaOnly},
	} {
		t.Run(row.name, func(t *testing.T) {
			out := importSources(catalog, "", named("root_agent.yaml",
				"name: served\ninstruction: Answer.\nmodel: "+row.model+"\n"))
			d, text := drafted(t, out)
			if got := d.Spec.Components["served"].Uses[modelSocket][row.part].Target; got != row.part {
				t.Errorf("the model connection %s targets %q:\n%s", row.part, got, text)
			}
			if got := d.Spec.Components[row.part].Values["model"]; got != row.served {
				t.Errorf("the model part serves %#v, want %q:\n%s", got, row.served, text)
			}
			want := "ADK serves the model " + row.served + " through its " + row.family + " class, which sends its " +
				"requests to the Gemini API"
			got := decision(t, out, row.model).Rationale
			if !strings.HasPrefix(got, want) {
				t.Errorf("the model is decided as %q, want it to open %q", got, want)
			}
			if !strings.Contains(got, row.backend) || strings.Contains(got, row.not) {
				t.Errorf("the model is decided as %q, want it to say %q and not %q", got, row.backend, row.not)
			}
		})
	}
	t.Run("two trees naming one model share one part", func(t *testing.T) {
		out := importSources(catalog, "",
			named("a/root_agent.yaml", "name: first\ninstruction: Answer.\nmodel: gemini-flash-latest\n"),
			named("b/root_agent.yaml", "name: second\ninstruction: Answer.\nmodel: gemini-flash-latest\n"))
		d, text := drafted(t, out)
		if len(d.Spec.Components) != 3 || d.Spec.Components["first"].Uses[modelSocket]["gemini-flash-latest"].Target !=
			"gemini-flash-latest" || d.Spec.Components["second"].Uses[modelSocket]["gemini-flash-latest"].Target !=
			"gemini-flash-latest" {
			t.Errorf("two trees naming one model are drafted as:\n%s", text)
		}
		if decision(t, out, "a#first").Chosen == nil || decision(t, out, "b#second").Chosen == nil {
			t.Errorf("the trees are not reported by their directories: %+v", out.Decisions)
		}
	})
	t.Run("a catalog holding no Gemini Component", func(t *testing.T) {
		view := &aggrega.CatalogProjection{Components: catalog.Components[:1]}
		out := importSources(view, "", named("root_agent.yaml",
			"name: drafted\ninstruction: Answer.\nmodel: gemini-flash-latest\n"))
		d, text := drafted(t, out)
		if got := d.Spec.Components["gemini-flash-latest"].Type; got != "imported/ai-model/gemini" {
			t.Errorf("the model part is an instance of %q, want the drafted imported/ai-model/gemini:\n%s", got, text)
		}
		component, ok := files(out)["components/imported-ai-model-gemini.yaml"]
		if !ok {
			var paths []string
			for p := range files(out) {
				paths = append(paths, p)
			}
			slices.Sort(paths)
			t.Fatalf("the drafted Gemini Component is not written: %v", paths)
		}
		if !strings.Contains(component, "    model.aggrega.ai/api: gemini\n") {
			t.Errorf("the drafted Gemini Component does not carry the label the model socket selects:\n%s", component)
		}
	})
}

// TestServedByMirrorsADKsModelRegistry resolves model strings as ADK 2.13.0's
// models registry does, and requires for each the family ADK serves it through,
// whether ADK sends its requests to the Gemini API — the condition a model is
// wired on — and the model name the class is handed: one row per pattern of its
// Gemini class, whose two Vertex AI resource names ADK sends to Vertex AI; a
// Gemma 3 name, served by its Gemma class through the Gemini API too; class:model
// forms, one naming a Vertex AI resource; and rows for the strings ADK serves
// any other way — a LiteLLM provider form, LiteLLM's own Gemini form, an
// Anthropic Vertex AI resource name, an Anthropic, a Gemma-through-Ollama, an
// Apigee and an OpenAI name — and a string ADK resolves no class for. The rows
// are written out here, apart from the table they check, so a pattern dropped
// from or added to that table fails a row.
//
// 🔴 PLANTED DEFECT: in modelClasses (models.go), drop the Gemini class's
// gemma-4.* pattern: the Gemma 4 row resolves to the Gemma class and fails.
func TestServedByMirrorsADKsModelRegistry(t *testing.T) {
	const liteLLM = "LiteLLM, if LiteLLM knows the provider it names"
	for _, tc := range []struct {
		model, family string
		geminiAPI     bool
		name          string
	}{
		{"gemini-2.5-flash", "Gemini", true, "gemini-2.5-flash"},
		{"gemma-4-27b-it", "Gemini", true, "gemma-4-27b-it"},
		{"model-optimizer-default", "Gemini", true, "model-optimizer-default"},
		{"projects/acme/locations/us-central1/endpoints/1234567890", "Vertex AI", false,
			"projects/acme/locations/us-central1/endpoints/1234567890"},
		{"projects/acme/locations/us-central1/publishers/google/models/gemini-2.5-pro", "Vertex AI", false,
			"projects/acme/locations/us-central1/publishers/google/models/gemini-2.5-pro"},
		{"gemini:gemini-2.5-flash", "Gemini", true, "gemini-2.5-flash"},
		{"gemini:projects/acme/locations/us-central1/endpoints/1234567890", "Vertex AI", false,
			"projects/acme/locations/us-central1/endpoints/1234567890"},
		{"gemma-3-27b", "Gemma", true, "gemma-3-27b"},
		{"openai/gpt-4o", "LiteLLM", false, "openai/gpt-4o"},
		{"vertex_ai/claude-3-sonnet", "LiteLLM", false, "vertex_ai/claude-3-sonnet"},
		{"ollama/llama3", "LiteLLM", false, "ollama/llama3"},
		{"gemini/gemini-2.5-flash", liteLLM, false, "gemini/gemini-2.5-flash"},
		{"projects/acme/locations/us-east5/publishers/anthropic/models/claude-sonnet-4", liteLLM, false,
			"projects/acme/locations/us-east5/publishers/anthropic/models/claude-sonnet-4"},
		{"claude-sonnet-4", "Anthropic", false, "claude-sonnet-4"},
		{"ollama/gemma3:4b", "Gemma, through Ollama", false, "ollama/gemma3:4b"},
		{"apigee/x", "Apigee", false, "apigee/x"},
		{"gpt-4o", "OpenAI", false, "gpt-4o"},
		{"my-own-model", "", false, "my-own-model"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := servedBy(tc.model)
			if got.family != tc.family || got.geminiAPI != tc.geminiAPI || got.name != tc.name {
				t.Errorf("servedBy(%q) is %+v; want family %q, Gemini API %v, name %q", tc.model, got, tc.family,
					tc.geminiAPI, tc.name)
			}
		})
	}
}

// TestTwoImportsAreByteIdentical imports each committed application twice and
// requires the same files and the same decisions, byte for byte.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	for name, sources := range map[string][]aggrega.ImportSource{"tutor": tutor(t), "pipeline": pipeline(t)} {
		first := must(json.Marshal(importSources(catalog, "", sources...)))
		second := must(json.Marshal(importSources(catalog, "", sources...)))
		if string(first) != string(second) {
			t.Errorf("two imports of %s differ:\n%s\n%s", name, first, second)
		}
	}
}

// TestWhatTheImportRefusesOrReadsNothingOf drives the cases that draft nothing:
// a configuration the parser refuses halts the import under parse-failed with the
// parser's reason; a root configuration that is not a mapping is reported under
// no-nodes; and a request naming another format reads nothing.
//
// 🔴 PLANTED DEFECT: in reader.tree (agents.go), report a refusal under
// importer.NoNodes: the first row fails on the code.
func TestWhatTheImportRefusesOrReadsNothingOf(t *testing.T) {
	refused := importSources(catalog, "", aggrega.ImportSource{Name: "root_agent.yaml", Bytes: []byte("name: [cut\n")})
	if got := coded(refused, "parse-failed"); len(got) != 1 ||
		!strings.Contains(got[0], `could not parse source "root_agent.yaml" as google-adk: `) {
		t.Errorf("a refused configuration reports %q, want one parse-failed naming it", got)
	}
	empty := importSources(catalog, "", aggrega.ImportSource{Name: "root_agent.yaml", Bytes: []byte("- a list\n")})
	if got := coded(empty, "no-nodes"); len(got) != 1 || len(empty.Files) != 0 {
		t.Errorf("a root configuration declaring no agent reports %q and drafts %d files", got, len(empty.Files))
	}
	other := Import(aggrega.ImportInput{Format: "crewai", Sources: tutor(t)}, hostParse)
	if len(other.Files) != 0 || len(other.Diagnostics) != 0 {
		t.Errorf("a request naming another format drafted %d files and reported %+v", len(other.Files),
			other.Diagnostics)
	}
}

// TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments imports a root
// configuration whose agent and tool are named with "{{", each name written into a
// comment of the Aggregate, and requires the Aggregate to run as a template — as
// the compiler evaluates its whole text, comments included — and each comment to
// read back with the name as the configuration writes it.
//
// 🔴 PLANTED DEFECT: in importer.CommentLine
// (aggrega-pdk/go/pkg/importer/definition.go), return the line unescaped: the
// Aggregate no longer parses as a template, and the case fails on it.
func TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments(t *testing.T) {
	out := importSources(catalog, "", aggrega.ImportSource{Name: RootFile, Bytes: []byte(
		"name: \"tutor{{ root }}\"\ninstruction: Teach.\nmodel: gemini-flash-latest\ntools:\n  - name: \"make{{ tool }}\"\n")})
	_, text := drafted(t, out)
	got := evaluatedAggregate(t, text)
	for _, want := range []string{"served from its root agent tutor{{ root }}.\n",
		"drafted as no Component: make{{ tool }}.\n"} {
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
