// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package langgraphimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// The LangGraph importer on the host toolchain: each case hands Import a request
// and the parse the host would run — the compiler's own JSON parser, a refusal
// read as the import socket reads it — and reads what it drafts.

// hostParse parses source as the host's JSON parser does and refuses it as the
// import socket hands an importer a parser's refusal. Any other syntax fails the
// case: the importer asks for JSON alone.
func hostParse(t *testing.T) func(syntax string, source []byte) (json.RawMessage, error) {
	return func(syntax string, source []byte) (json.RawMessage, error) {
		if syntax != Syntax {
			t.Fatalf("the importer asked for the %q syntax; it reads JSON alone", syntax)
		}
		out := parsercore.ParseJSON(source)
		if err := importersocket.ParseRefusal(out); err != nil {
			return nil, err
		}
		return out.Tree, nil
	}
}

// fixture reads one file of a committed LangGraph application
// (internal/importops/testdata/langgraph).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "importops", "testdata", "langgraph",
		filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return body
}

// catalogLabel is the label a catalog Component's category is read from.
const catalogLabel = "component.aggrega.ai/category"

// catalog is a view of a workspace catalog holding the standard library's
// LangGraph agent and PostgreSQL, with the labels the hub gives them, and no
// MongoDB.
var catalog = &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
	{Name: Component, Version: "1.0.0", Labels: map[string]string{catalogLabel: "ai-agent"}},
	{Name: "core/datastore/postgres", Version: "1.0.0", Labels: map[string]string{catalogLabel: "datastore"}},
}}

// importSources imports sources, named, against catalog, with settings.
func importSources(t *testing.T, settings string, sources ...aggrega.ImportSource) aggrega.ImportOutput {
	t.Helper()
	in := aggrega.ImportInput{Format: Format, Sources: sources, Catalog: catalog}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse(t))
}

// python and envfile are the committed applications' files as a directory import
// hands them: the files the signatures name, by their names inside the
// application, in name order — never the application's code.
func python(t *testing.T) []aggrega.ImportSource {
	t.Helper()
	return []aggrega.ImportSource{{Name: "langgraph.json", Bytes: fixture(t, "python/langgraph.json")}}
}

func envfile(t *testing.T) []aggrega.ImportSource {
	t.Helper()
	return []aggrega.ImportSource{
		{Name: "langgraph.json", Bytes: fixture(t, "envfile/langgraph.json")},
		{Name: "local.env", Bytes: fixture(t, "envfile/local.env")},
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

// componentKeys returns the instance keys of a drafted document's
// spec.components, in the order the document writes them.
func componentKeys(t *testing.T, text string) []string {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("the draft does not read as YAML: %v\n%s", err, text)
	}
	member := func(n *yaml.Node, key string) *yaml.Node {
		for i := 0; n != nil && n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	components := member(member(doc.Content[0], "spec"), "components")
	var keys []string
	for i := 0; components != nil && i+1 < len(components.Content); i += 2 {
		keys = append(keys, components.Content[i].Value)
	}
	return keys
}

// coded returns the messages of out's diagnostics coded code, in order.
func coded(out aggrega.ImportOutput, code string) []string {
	var got []string
	for _, d := range out.Diagnostics {
		if d.Code == code {
			got = append(got, d.Message)
		}
	}
	return got
}

// everything is every text out carries: each file, each decision and each
// diagnostic.
func everything(t *testing.T, out aggrega.ImportOutput) string {
	t.Helper()
	var b strings.Builder
	for _, f := range out.Files {
		b.WriteString(f.Path + "\n" + f.Content + "\n")
	}
	for _, e := range out.Entities {
		b.WriteString(e.Body + "\n")
	}
	decisions, _ := json.Marshal(out.Decisions)
	diagnostics, _ := json.Marshal(out.Diagnostics)
	b.Write(decisions)
	b.Write(diagnostics)
	b.WriteString(out.Error)
	return b.String()
}

// The key-shaped values the python application's inline env plants, under a
// name that reads as a credential's and one that does not. Neither is a real
// key.
const (
	plantedInlineKey   = "sk-planted-langgraph-inline-key-needle-4c1e9a7b"
	plantedInlineQueue = "planted-langgraph-inline-queue-needle-8d2f"
)

// plantedEnvFileValues are the values the envfile application's local.env
// holds, each a key-shaped needle the import must never carry: a plain value, one
// after export, a quoted one, both lines of one quoted across two lines — the
// second reading like a variable of its own — and a line naming no variable.
var plantedEnvFileValues = []string{
	"tvly-planted-langgraph-envfile-key-needle-2b7e",
	"planted-langgraph-envfile-depth-needle-61c0",
	"planted-langgraph-envfile-quoted-needle-93fa",
	"planted-langgraph-envfile-multiline-needle-5a1d",
	"planted-langgraph-envfile-inside-needle-77b3",
	"planted-langgraph-envfile-stray-needle-0e48",
}

// TestAnApplicationIsDraftedAsOneAgentPerGraphWiredToItsDatastore imports the
// python application and requires an Aggregate holding one instance of
// core/ai-agent/langgraph per graph, in the order the configuration writes them —
// triage before support, not sorted — each carrying its import path as
// graph_entrypoint, triage its description, wired on checkpointer_db to one
// instance of core/datastore/postgres, since the checkpointer names no backend;
// no recursion_limit written and no model connection, each left as the comment
// above the graph says; and a Release with an entry per graph leaving its image.
//
// 🔴 PLANTED DEFECT: in readGraphs (config.go), range over sortedKeys of the
// graphs object instead of members: support is drafted before triage, and the
// case fails on the keys.
func TestAnApplicationIsDraftedAsOneAgentPerGraphWiredToItsDatastore(t *testing.T) {
	t.Parallel()
	out := importSources(t, "", python(t)...)
	if out.Error != "" {
		t.Fatalf("the import failed: %s", out.Error)
	}
	d, text := drafted(t, out)
	if got, want := componentKeys(t, text), []string{"triage", "support", "postgres"}; !slices.Equal(got, want) {
		t.Fatalf("the Aggregate holds %v, want %v:\n%s", got, want, text)
	}
	for key, entrypoint := range map[string]string{
		"triage":  "./src/support_desk/triage.py:make_graph",
		"support": "./src/support_desk/agent.py:graph",
	} {
		c := d.Spec.Components[key]
		if c.Type != Component || c.Values["graph_entrypoint"] != entrypoint {
			t.Errorf("%s is %q with graph_entrypoint %#v, want %s with %q", key, c.Type, c.Values["graph_entrypoint"],
				Component, entrypoint)
		}
		if _, written := c.Values["recursion_limit"]; written {
			t.Errorf("%s writes recursion_limit, which the configuration does not state", key)
		}
		if got := c.Uses[checkpointerSocket]["postgres"].Target; got != "postgres" {
			t.Errorf("%s's checkpointer_db connection targets %q, want postgres", key, got)
		}
		if _, wired := c.Uses[modelSocket]; wired {
			t.Errorf("%s's model socket is wired, though the configuration names no model", key)
		}
	}
	if got := d.Spec.Components["triage"].Values["description"]; got != "Routes an incoming ticket to the {team} that owns it." {
		t.Errorf("triage's description is %#v, want the one the configuration gives", got)
	}
	if got := d.Spec.Components["postgres"].Type; got != "core/datastore/postgres" {
		t.Errorf("the datastore is an instance of %q, want core/datastore/postgres", got)
	}
	for _, want := range []string{
		"# The model connection is left for the author, since the configuration names no model",
		"# Not carried, since langgraph build reads them to build the image: dependencies, python_version.",
		"# Not carried, since core/ai-agent/langgraph takes no value for them: http.",
		"# Not carried, since core/ai-agent/langgraph takes no value for them: checkpointer.ttl.",
		"# The store configures the memory store",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Aggregate does not say %q:\n%s", want, text)
		}
	}

	release := files(out)["release.yaml"]
	if got, want := componentKeys(t, release), []string{"triage", "support"}; !slices.Equal(got, want) {
		t.Errorf("the Release holds %v, want %v:\n%s", got, want, release)
	}
	if !strings.Contains(release, "#   - infra.image, the container image running the application, which langgraph "+
		"build builds from its configuration\n") {
		t.Errorf("the Release does not leave the image for the Release:\n%s", release)
	}
	if got := len(out.Files); got != 2 {
		t.Errorf("the import drafted %d files, want the Aggregate and the Release alone: %v", got, files(out))
	}

	var types []string
	for _, dec := range out.Decisions {
		types = append(types, dec.SourceNode.Type+" "+dec.SourceNode.ID)
	}
	if want := []string{"graph triage", "graph support", "checkpointer checkpointer", "store store"}; !slices.Equal(types, want) {
		t.Errorf("the decisions are %v, want %v", types, want)
	}
}

// TestTheInlineEnvironmentIsCarriedAsTheSettingSays imports the python
// application with the empty_credentials setting absent, none, marked and all,
// after a needle proves each planted value is in the configuration: where it is
// not all, each value of the inline env is carried as written into the Release's
// comment, in the order the configuration writes them, and in no other file, no
// decision and no diagnostic; each is named in a credential-carried warning by
// its configuration and its variable — OPENAI_API_KEY and SUPPORT_QUEUE alike,
// since no name is read — and never by its value. Where it is all, no value
// reaches anything and each is named under credential-emptied instead.
//
// 🔴 PLANTED DEFECT: in located (langgraphimport.go), skip a variable whose name
// ends in _KEY: SUPPORT_QUEUE is named and OPENAI_API_KEY is not, and every row
// fails on the warnings.
func TestTheInlineEnvironmentIsCarriedAsTheSettingSays(t *testing.T) {
	t.Parallel()
	source := fixture(t, "python/langgraph.json")
	planted := []string{plantedInlineQueue, plantedInlineKey}
	for _, p := range planted {
		if !strings.Contains(string(source), p) {
			t.Fatalf("the needle: %q is not in the configuration, so the search finds nothing", p)
		}
	}
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			settings := ""
			if setting != "absent" {
				settings = `{"empty_credentials":"` + setting + `"}`
			}
			out := importSources(t, settings, python(t)...)
			release := files(out)["release.yaml"]
			code := "credential-carried"
			if setting == "all" {
				code = "credential-emptied"
				if all := everything(t, out); strings.Contains(all, plantedInlineKey) || strings.Contains(all,
					plantedInlineQueue) {
					t.Errorf("a value the setting writes empty reaches the import's output:\n%s", all)
				}
				if !strings.Contains(release, "    #   SUPPORT_QUEUE\n    #   OPENAI_API_KEY\n") {
					t.Errorf("the Release does not name each emptied variable with no value:\n%s", release)
				}
			} else {
				want := "    #   SUPPORT_QUEUE: \"" + plantedInlineQueue + "\"\n    #   OPENAI_API_KEY: \"" +
					plantedInlineKey + "\"\n    #   MAX_TICKETS: \"25\"\n"
				if !strings.Contains(release, want) {
					t.Errorf("the Release does not carry the environment as written, in the configuration's order:\n%s",
						release)
				}
				rest := out
				rest.Files = slices.DeleteFunc(slices.Clone(out.Files), func(f aggrega.ScaffoldFile) bool {
					return f.Path == "release.yaml"
				})
				rest.Entities = slices.DeleteFunc(slices.Clone(out.Entities), func(e aggrega.EmittedEntity) bool {
					return e.Kind == "Release"
				})
				if all := everything(t, rest); strings.Contains(all, plantedInlineKey) || strings.Contains(all,
					plantedInlineQueue) {
					t.Errorf("a carried value reaches something other than the Release:\n%s", all)
				}
			}
			named := coded(out, code)
			if len(named) != 3 {
				t.Fatalf("the %s warnings are %q, want one per variable", code, named)
			}
			for i, variable := range []string{"env.SUPPORT_QUEUE", "env.OPENAI_API_KEY", "env.MAX_TICKETS"} {
				if !strings.Contains(named[i], variable+" of node langgraph.json") {
					t.Errorf("warning %d is %q, want one naming %s of node langgraph.json", i, named[i], variable)
				}
			}
		})
	}
}

// TestAnEnvironmentFileHasItsNamesReadAndNeverItsValues imports the envfile
// application — its configuration and the local.env it names, as a directory
// hands them — under every value of the empty_credentials setting, after a
// needle proves each planted value is in local.env: the Release names the
// variables the file names, in its order, with no value, and names the line
// naming no variable by its number; the variable inside the value quoted across
// two lines is not named; and no value of the file reaches any file, decision or
// diagnostic, nor is any named in a credential warning, whatever the setting
// says.
//
// 🔴 PLANTED DEFECT: in writeEnvironment (render.go), write each name followed by
// the rest of its line: every row fails on the needles.
func TestAnEnvironmentFileHasItsNamesReadAndNeverItsValues(t *testing.T) {
	t.Parallel()
	env := fixture(t, "envfile/local.env")
	for _, p := range plantedEnvFileValues {
		if !strings.Contains(string(env), p) {
			t.Fatalf("the needle: %q is not in local.env, so the search finds nothing", p)
		}
	}
	for _, setting := range []string{"absent", "none", "marked", "all"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			settings := ""
			if setting != "absent" {
				settings = `{"empty_credentials":"` + setting + `"}`
			}
			out := importSources(t, settings, envfile(t)...)
			if out.Error != "" {
				t.Fatalf("the import failed: %s", out.Error)
			}
			all := everything(t, out)
			for _, p := range plantedEnvFileValues {
				if strings.Contains(all, p) {
					t.Errorf("the value %q of local.env reaches the import's output", p)
				}
			}
			release := files(out)["release.yaml"]
			want := "    #   - TAVILY_API_KEY\n    #   - RESEARCH_DEPTH\n    #   - SEARCH_REGION\n    #   - NOTES\n" +
				"    # Not read, naming no variable: line 8 of local.env.\n"
			if !strings.Contains(release, want) {
				t.Errorf("the Release does not name the file's variables, in its order, with no value:\n%s", release)
			}
			if strings.Contains(release, "HIDDEN_NAME") {
				t.Errorf("a line inside a quoted value is read as a variable:\n%s", release)
			}
			for _, code := range []string{"credential-carried", "credential-emptied"} {
				if named := coded(out, code); len(named) != 0 {
					t.Errorf("the file's variables are named under %s: %q", code, named)
				}
			}
			if _, text := drafted(t, out); !strings.Contains(text, "# Environment files read for their variables' "+
				"names alone, never their values:\n#   - local.env\n") {
				t.Errorf("the Aggregate does not name local.env as read for its names alone:\n%s", text)
			}
		})
	}
}

// TestAnEnvironmentFileIsFoundWhereverTheImportHandsIt imports the envfile
// application's configuration as a file — primary bytes, with no name — with
// its local.env handed as a named source, as an attachment's with.sources hands
// it; then with the configuration naming its file under a directory, as a
// directory import hands both; then with the file not handed at all. The first
// two read the file's names; the third names the file as not read and leaves its
// variables for the Release. No value reaches the output in any of them.
//
// 🔴 PLANTED DEFECT: in envSource (config.go), drop the base-name lookup: the
// with.sources row names local.env as not handed.
func TestAnEnvironmentFileIsFoundWhereverTheImportHandsIt(t *testing.T) {
	t.Parallel()
	config := fixture(t, "envfile/langgraph.json")
	env := fixture(t, "envfile/local.env")
	nested := []byte(strings.Replace(string(config), `"env": "local.env"`, `"env": "./settings/local.env"`, 1))
	for _, row := range []struct {
		name string
		in   aggrega.ImportInput
		read string
	}{
		{"handed under with.sources", aggrega.ImportInput{Format: Format, Bytes: config,
			Sources: []aggrega.ImportSource{{Name: "local.env", Bytes: env}}}, "local.env"},
		{"handed beside the configuration in a directory", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{{Name: "app/langgraph.json", Bytes: nested},
				{Name: "app/settings/local.env", Bytes: env}}}, "app/settings/local.env"},
		{"not handed", aggrega.ImportInput{Format: Format, Bytes: config}, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			row.in.Catalog = catalog
			out := Import(row.in, hostParse(t))
			all := everything(t, out)
			for _, p := range plantedEnvFileValues {
				if strings.Contains(all, p) {
					t.Errorf("the value %q of local.env reaches the import's output", p)
				}
			}
			release := files(out)["release.yaml"]
			_, text := drafted(t, out)
			if row.read == "" {
				if !strings.Contains(text, "# Not read: the environment file local.env, which the import was not "+
					"handed; its variables are left for the Release.\n") {
					t.Errorf("the Aggregate does not name local.env as not handed:\n%s", text)
				}
				if strings.Contains(release, "TAVILY_API_KEY") {
					t.Errorf("the Release names a variable of a file the import was not handed:\n%s", release)
				}
				return
			}
			if !strings.Contains(release, "# The environment file "+row.read+",") ||
				!strings.Contains(release, "    #   - TAVILY_API_KEY\n") {
				t.Errorf("the Release does not name the variables of %s:\n%s", row.read, release)
			}
		})
	}
}

// TestAnEnvironmentFileNoConfigurationNamesIsNotRead imports the python
// application, whose env is inline, with an environment file beside it, as a
// directory hands one: the file is named as not read and none of its values or
// names reaches the output.
func TestAnEnvironmentFileNoConfigurationNamesIsNotRead(t *testing.T) {
	t.Parallel()
	out := importSources(t, "", append(python(t),
		aggrega.ImportSource{Name: "stray.env", Bytes: fixture(t, "envfile/local.env")})...)
	_, text := drafted(t, out)
	if !strings.Contains(text, "# Not read, since no configuration names them: the environment files stray.env.\n") {
		t.Errorf("the Aggregate does not name stray.env as not read:\n%s", text)
	}
	all := everything(t, out)
	for _, p := range append([]string{"TAVILY_API_KEY"}, plantedEnvFileValues...) {
		if strings.Contains(all, p) {
			t.Errorf("%q of a file no configuration names reaches the import's output", p)
		}
	}
}

// TestACheckpointerBackendDecidesTheDatastore imports one configuration per
// checkpointer backend against a catalog view holding PostgreSQL and no MongoDB:
// mongo drafts <target_namespace>/datastore/mongodb beside the Aggregate and
// wires the graph to it; default wires core/datastore/postgres, referenced;
// custom, and a backend the importer maps to nothing, wire no datastore, leaving
// checkpointer_db for the author and saying why, in a comment and in the
// checkpointer's decision.
func TestACheckpointerBackendDecidesTheDatastore(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, checkpointer string
		component          string
		drafted            bool
		why                string
	}{
		{"mongo", `{"backend":"mongo"}`, "imported/datastore/mongodb", true, ""},
		{"default", `{"backend":"default"}`, "core/datastore/postgres", false, ""},
		{"custom", `{"backend":"custom","path":"./src/saver.py:make_saver"}`, "", false,
			"is custom: a checkpointer the application's code defines at ./src/saver.py:make_saver"},
		{"unmapped", `{"backend":"sqlite"}`, "", false,
			"names the backend sqlite, which the import maps to no datastore"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			config := []byte(`{"dependencies":["."],"graphs":{"agent":"./agent.py:graph"},"checkpointer":` +
				row.checkpointer + `}`)
			out := importSources(t, "", aggrega.ImportSource{Name: "langgraph.json", Bytes: config})
			d, text := drafted(t, out)
			agent := d.Spec.Components["agent"]
			if row.component == "" {
				if len(agent.Uses[checkpointerSocket]) != 0 {
					t.Errorf("the agent is wired on checkpointer_db, though the checkpointer names no datastore")
				}
				if !strings.Contains(text, "# The checkpointer_db connection is left for the author, since the "+
					"checkpointer "+row.why) {
					t.Errorf("the Aggregate does not say why checkpointer_db is left:\n%s", text)
				}
				var reported bool
				for _, dec := range out.Decisions {
					if dec.SourceNode.Type == "checkpointer" && dec.Chosen == nil && strings.Contains(dec.Rationale, row.why) {
						reported = true
					}
				}
				if !reported {
					t.Errorf("no decision reports the checkpointer naming no datastore: %+v", out.Decisions)
				}
				return
			}
			key := agent.Uses[checkpointerSocket]
			if len(key) != 1 {
				t.Fatalf("the agent's checkpointer_db connections are %+v, want one", key)
			}
			for alias, conn := range key {
				if got := d.Spec.Components[conn.Target].Type; got != row.component {
					t.Errorf("the connection %s reaches an instance of %q, want %q", alias, got, row.component)
				}
			}
			var draftedFile bool
			for path, content := range files(out) {
				if strings.HasPrefix(path, "components/") && strings.Contains(content, "name: "+row.component+"\n") {
					draftedFile = true
				}
			}
			if draftedFile != row.drafted {
				t.Errorf("the import drafted a Component file for %s: %v, want %v", row.component, draftedFile, row.drafted)
			}
		})
	}
}

// TestTwoImportsAreByteIdentical imports each committed application twice and
// requires the two to return the same output, byte for byte.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	t.Parallel()
	for name, sources := range map[string][]aggrega.ImportSource{"python": python(t), "envfile": envfile(t)} {
		first, _ := json.Marshal(importSources(t, "", sources...))
		second, _ := json.Marshal(importSources(t, "", sources...))
		if string(first) != string(second) {
			t.Errorf("two imports of %s differ:\n%s\n%s", name, first, second)
		}
	}
}

// TestAConfigurationTheParserRefusesHaltsTheImport hands a configuration cut
// short: the import halts under parse-failed carrying the parser's reason and
// drafts nothing; an environment file beside it is never handed to the parser.
func TestAConfigurationTheParserRefusesHaltsTheImport(t *testing.T) {
	t.Parallel()
	out := importSources(t, "", aggrega.ImportSource{Name: "langgraph.json", Bytes: []byte(`{"graphs": {`)},
		aggrega.ImportSource{Name: "local.env", Bytes: []byte("NOT JSON=at all\n")})
	failed := coded(out, "parse-failed")
	if len(failed) != 1 || !strings.Contains(failed[0], `could not parse source "langgraph.json" as langgraph: `) {
		t.Errorf("the import reported parse-failed %q, want one naming langgraph.json alone", failed)
	}
	if len(out.Files) != 0 {
		t.Errorf("the import drafted %v over a configuration the parser refused", files(out))
	}
}

// TestAConfigurationDeclaringNoGraphDraftsNothing hands a configuration whose
// graphs is empty, and a request naming another format: each is reported under
// no-nodes and nothing is drafted.
func TestAConfigurationDeclaringNoGraphDraftsNothing(t *testing.T) {
	t.Parallel()
	empty := Import(aggrega.ImportInput{Format: Format, Bytes: []byte(`{"graphs":{}}`)}, hostParse(t))
	other := Import(aggrega.ImportInput{Format: "crewai", Sources: python(t)}, hostParse(t))
	for name, out := range map[string]aggrega.ImportOutput{"no graph": empty, "another format": other} {
		if got := coded(out, "no-nodes"); len(got) != 1 || len(out.Files) != 0 {
			t.Errorf("%s: no-nodes %q and files %v, want one no-nodes and nothing drafted", name, got, files(out))
		}
	}
}

// TestVariableNamesReadsNamesAlone holds the environment file's reader to its
// contract, line by line: a name before =, after export or not; blank lines and
// comments skipped; a value quoted across lines read as no name; a line naming
// no variable reported by its number; a name named twice kept once.
func TestVariableNamesReadsNamesAlone(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, text string
		names      []string
		skipped    []int
	}{
		{"plain", "A=1\nB_2=two\n", []string{"A", "B_2"}, nil},
		{"export, spaces and a comment", "# c\n\nexport  C = three\n  D=\n", []string{"C", "D"}, nil},
		{"carriage returns", "E=1\r\nF=2\r\n", []string{"E", "F"}, nil},
		{"a double quote across lines", "G=\"one\nH=two\"\nI=3\n", []string{"G", "I"}, nil},
		{"an escaped double quote does not close", "J=\"a\\\"\nK=b\"\nL=c\n", []string{"J", "L"}, nil},
		{"a single quote across lines", "M='one\nN=two'\nO=3\n", []string{"M", "O"}, nil},
		{"a quote closed on its line", "P=\"x\"\nQ='y'\n", []string{"P", "Q"}, nil},
		{"a line naming no variable", "R=1\nnot a variable\n9X=2\n", []string{"R"}, []int{2, 3}},
		{"a name named twice", "S=1\nS=2\n", []string{"S"}, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			names, skipped := variableNames([]byte(row.text))
			if !slices.Equal(names, row.names) || !slices.Equal(skipped, row.skipped) {
				t.Errorf("variableNames(%q) = %v, %v; want %v, %v", row.text, names, skipped, row.names, row.skipped)
			}
		})
	}
}
