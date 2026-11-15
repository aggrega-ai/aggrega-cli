// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package langgraphimport

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
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
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

// python and envfile are the committed applications' files as an import hands
// them: the configuration a directory hands, by its name inside the application —
// never the application's code — and, for envfile, the local.env its
// configuration names, as the attachment's with.sources hands it, under its base
// name; a directory hands no environment file.
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
// above the graph says; and a Release stating once, above the first graph, what
// each graph's image holds and the variables it is handed, with an entry per
// graph leaving its image, then one for the datastore.
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
		"# Not read: the application's code, which defines each graph; it runs in the image the Release names for " +
			"each graph.\n",
		"# Not carried, since they configure the server image langgraph build builds, and the image the Release " +
			"names is the application's own: dependencies, python_version.\n",
		"# Not carried, since core/ai-agent/langgraph takes no value for them: http.",
		"# Not carried, since core/ai-agent/langgraph takes no value for them: checkpointer.ttl.",
		"# The store configures the memory store",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the Aggregate does not say %q:\n%s", want, text)
		}
	}

	release := files(out)["release.yaml"]
	if got, want := componentKeys(t, release), []string{"triage", "support", "postgres"}; !slices.Equal(got, want) {
		t.Errorf("the Release holds %v, want %v:\n%s", got, want, release)
	}
	if got := strings.Count(release, "infra.image, the container image running the application\n"+
		"        image: \"\"\n"); got != 2 {
		t.Errorf("the Release leaves the image empty for the Release in %d graphs, want both:\n%s", got, release)
	}
	// The image contract is written once, among the comments above the first
	// graph's entry: the application's own image, and the variables the Recipes
	// hand it, one comment line each.
	contract := "    # " + strings.Join(imageContract, "\n    # ") + "\n"
	entries := strings.SplitN(release, "  components:\n", 2)[1]
	first := strings.Index(entries, "    triage:\n")
	if got := strings.Count(release, contract); got != 1 || first < 0 || !strings.Contains(entries[:first], contract) {
		t.Errorf("the Release states the image contract %d times, want once, above the first graph:\n%s", got, release)
	}
	for _, want := range []string{
		"    # Each graph's image is the application's own, not the server image langgraph build builds: its code, " +
			"with LangGraph installed.\n",
		"    # It is handed, for its code to read, the graph to run as LANGGRAPH_GRAPH_ENTRYPOINT, the model's name " +
			"as OPENAI_MODEL, the checkpointer store's host and port as LANGGRAPH_CHECKPOINTER_HOST and " +
			"LANGGRAPH_CHECKPOINTER_PORT, its user and database, where the store publishes them, as " +
			"LANGGRAPH_CHECKPOINTER_USER and LANGGRAPH_CHECKPOINTER_DATABASE, and a password attached on the " +
			"checkpointer_db connection as LANGGRAPH_CHECKPOINTER_PASSWORD.\n",
		"    # It is also handed the model's address as OPENAI_BASE_URL and a key attached on the model connection " +
			"as OPENAI_API_KEY, which the OpenAI client library reads, and recursion_limit as " +
			"LANGGRAPH_DEFAULT_RECURSION_LIMIT, which LangGraph reads.\n",
		"    # An inject: block on the graph's own instance, through an entry reading graph_entrypoint alone, " +
			"replaces LANGGRAPH_GRAPH_ENTRYPOINT with the name it writes; one on the model connection replaces " +
			"OPENAI_MODEL, and one on the checkpointer_db connection the store's host, port, user and database, " +
			"with the names it writes.\n",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("the Release does not say %q:\n%s", want, release)
		}
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

// TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares imports the
// python application, whose checkpointer names no backend, over a catalog holding
// PostgreSQL and over one holding none, so its datastore's Component is drafted,
// and the envfile application, whose checkpointer names mongo, whose FerretDB
// the catalog does not hold, so no datastore is drafted for it; and requires the
// drafted Release to hold an entry for every instance the drafted Aggregate
// declares, the datastore included: a build places only the instances a Release
// names, so an instance without an entry halts it. Each importer package holds a
// test of this name over the entries it hands the plugin kit's Release writer,
// since each importer decides what its Release holds.
//
// 🔴 PLANTED DEFECT: in langgraphRelease (render.go), drop the loop over the datastore
// parts: every row fails naming its datastore.
func TestTheDraftedReleaseNamesEveryInstanceTheDraftedAggregateDeclares(t *testing.T) {
	t.Parallel()
	agentOnly := &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
		{Name: Component, Version: "1.0.0", Labels: map[string]string{catalogLabel: "ai-agent"}},
	}}
	for _, row := range []struct {
		name, datastore string
		sources         []aggrega.ImportSource
		view            *aggrega.CatalogProjection
	}{
		{"a datastore the catalog holds", "postgres", python(t), catalog},
		{"a datastore whose Component is drafted", "postgres", python(t), agentOnly},
		{"a backend whose datastore the catalog does not hold", "", envfile(t), catalog},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			out := Import(aggrega.ImportInput{Format: Format, Sources: row.sources, Catalog: row.view}, hostParse(t))
			if out.Error != "" {
				t.Fatalf("the import failed: %s", out.Error)
			}
			_, text := drafted(t, out)
			release := files(out)["release.yaml"]
			named := componentKeys(t, release)
			var unplaced []string
			for _, key := range componentKeys(t, text) {
				if !slices.Contains(named, key) {
					unplaced = append(unplaced, key)
				}
			}
			if len(unplaced) > 0 {
				t.Errorf("the Release names no entry for %v, which the Aggregate declares:\n%s", unplaced, release)
			}
			if row.datastore != "" && !slices.Contains(named, row.datastore) {
				t.Errorf("the Release holds no entry %s:\n%s", row.datastore, release)
			}
		})
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
// application — its configuration and the local.env it names, handed as the
// attachment's with.sources hands it — under every value of the empty_credentials setting, after a
// needle proves each planted value is in local.env: the Release names the
// variables the file names, in its order, with no value, and names the line
// naming no variable by its number; the variable inside the value quoted across
// two lines is not named; and no value of the file reaches any file, decision or
// diagnostic, nor is any named in a credential warning, whatever the setting
// says.
//
// 🔴 PLANTED DEFECT: in environmentComment (render.go), write each name followed
// by the rest of its line: every row fails on the needles.
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
// it; then with the configuration naming its file under a directory and
// with.sources handing it under its base name; then with the file under that
// directory beside the configuration, as no import hands one, since a directory
// hands no environment file; then with the file not handed at all. The first two
// read the file's names; the last two name the file as not handed, say how to
// hand it, and leave its variables for the Release. No value reaches the output
// in any of them.
//
// 🔴 PLANTED DEFECT: in envSource (config.go), drop the base-name lookup: the
// with.sources rows name local.env as not handed.
func TestAnEnvironmentFileIsFoundWhereverTheImportHandsIt(t *testing.T) {
	t.Parallel()
	config := fixture(t, "envfile/langgraph.json")
	env := fixture(t, "envfile/local.env")
	nested := []byte(strings.Replace(string(config), `"env": "local.env"`, `"env": "./settings/local.env"`, 1))
	for _, row := range []struct {
		name string
		in   aggrega.ImportInput
		// read is the source the file's names are read from; notHanded the path
		// the Aggregate names as not handed, where read is empty.
		read, notHanded string
	}{
		{"handed under with.sources", aggrega.ImportInput{Format: Format, Bytes: config,
			Sources: []aggrega.ImportSource{{Name: "local.env", Bytes: env}}}, "local.env", ""},
		{"named under a directory, handed under with.sources", aggrega.ImportInput{Format: Format, Bytes: nested,
			Sources: []aggrega.ImportSource{{Name: "local.env", Bytes: env}}}, "local.env", ""},
		{"beside the configuration under a directory", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{{Name: "app/langgraph.json", Bytes: nested},
				{Name: "app/settings/local.env", Bytes: env}}}, "", "./settings/local.env"},
		{"not handed", aggrega.ImportInput{Format: Format, Bytes: config}, "", "local.env"},
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
				if !strings.Contains(text, "# Not read: the environment file "+row.notHanded+", which the import was "+
					"not handed; its variables are left for the Release. Name the file under the LangGraph importer's "+
					"with.sources to have its variables' names read.\n") {
					t.Errorf("the Aggregate does not name %s as not handed, with how to hand it:\n%s", row.notHanded, text)
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
// application, whose env is inline, with an environment file the attachment's
// with.sources hands: the file is named as not read and none of its values or
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

// ferretCatalog is catalog with the standard library's FerretDB and DocumentDB
// beside its Components.
var ferretCatalog = &aggrega.CatalogProjection{Components: append(slices.Clone(catalog.Components),
	aggrega.CatalogComponent{Name: "core/datastore/documentdb", Version: "1.0.0",
		Labels: map[string]string{catalogLabel: "datastore"}},
	aggrega.CatalogComponent{Name: "core/datastore/ferretdb", Version: "1.0.0",
		Labels: map[string]string{catalogLabel: "datastore"}})}

// TestACheckpointerBackendDecidesTheDatastore imports one configuration per
// checkpointer backend against a catalog view holding PostgreSQL and no FerretDB:
// default wires core/datastore/postgres, referenced; mongo, whose FerretDB and
// DocumentDB the catalog does not hold, custom, and a backend the importer maps
// to nothing, wire no datastore, leaving checkpointer_db for the author and saying
// why, in a comment and in the checkpointer's decision. Against a view holding
// both, mongo wires core/datastore/ferretdb, referenced, itself wired to an
// instance of core/datastore/documentdb on its backend socket.
func TestACheckpointerBackendDecidesTheDatastore(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, checkpointer string
		view               *aggrega.CatalogProjection
		component          string
		drafted            bool
		why                string
	}{
		{"mongo", `{"backend":"mongo"}`, ferretCatalog, "core/datastore/ferretdb", false, ""},
		{"mongo over a catalog holding no FerretDB", `{"backend":"mongo"}`, catalog, "", false,
			"names the backend mongo, which the import drafts as core/datastore/ferretdb over " +
				"core/datastore/documentdb, and the workspace's catalog holds no core/datastore/ferretdb and no " +
				"core/datastore/documentdb"},
		{"default", `{"backend":"default"}`, catalog, "core/datastore/postgres", false, ""},
		{"custom", `{"backend":"custom","path":"./src/saver.py:make_saver"}`, catalog, "", false,
			"is custom: a checkpointer the application's code defines at ./src/saver.py:make_saver"},
		{"unmapped", `{"backend":"sqlite"}`, catalog, "", false,
			"names the backend sqlite, which the import maps to no datastore"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			config := []byte(`{"dependencies":["."],"graphs":{"agent":"./agent.py:graph"},"checkpointer":` +
				row.checkpointer + `}`)
			out := Import(aggrega.ImportInput{Format: Format, Catalog: row.view,
				Sources: []aggrega.ImportSource{{Name: "langgraph.json", Bytes: config}}}, hostParse(t))
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
			if row.component == "core/datastore/ferretdb" {
				backend := d.Spec.Components["ferretdb"].Uses["backend"]["documentdb"].Target
				if backend != "documentdb" || d.Spec.Components["documentdb"].Type != "core/datastore/documentdb" {
					t.Errorf("FerretDB is not wired to an instance of core/datastore/documentdb on its backend "+
						"socket:\n%s", text)
				}
			}
		})
	}
}

// TestTheGraphCallsADraftedModelAndPresentsTheStoresPassword imports a
// configuration naming the mongo checkpointer against a catalog view holding the
// OpenAI model, FerretDB and DocumentDB, for compose and for helm, and requires
// every graph wired to one drafted instance of core/ai-model/openai whose
// values.model is written empty for the author, the graph presenting the model a
// key read from OPENAI_API_KEY; and one password, read from FERRETDB_PASSWORD,
// attached to DocumentDB as its own, to FerretDB on its connection to DocumentDB
// and to the graph on its checkpointer_db connection — each declared with the key
// its Recipes read it under for compose, and named by its Secret for helm.
//
// 🔴 PLANTED DEFECT: in draftModel (langgraphimport.go), return nil: the graph's
// model socket is left unwired and the case fails.
func TestTheGraphCallsADraftedModelAndPresentsTheStoresPassword(t *testing.T) {
	t.Parallel()
	view := &aggrega.CatalogProjection{Components: append(slices.Clone(ferretCatalog.Components),
		aggrega.CatalogComponent{Name: modelComponent, Version: "1.0.0", Labels: map[string]string{catalogLabel: "ai-model"}})}
	config := []byte(`{"graphs":{"agent":"./agent.py:graph"},"checkpointer":{"backend":"mongo"}}`)
	for _, target := range []string{"compose", "helm"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			out := Import(aggrega.ImportInput{Format: Format, Catalog: view, Target: target,
				Sources: []aggrega.ImportSource{{Name: "langgraph.json", Bytes: config}}}, hostParse(t))
			d, text := drafted(t, out)
			release := files(out)["release.yaml"]
			if got := d.Spec.Components["agent"].Uses[modelSocket]["model"].Target; got != "model" {
				t.Errorf("the graph's model connection targets %q, want the drafted model:\n%s", got, text)
			}
			if model := d.Spec.Components["model"]; model.Type != modelComponent || model.Values["model"] != "" {
				t.Errorf("the drafted model is %+v, want %s with values.model empty", model, modelComponent)
			}
			if !strings.Contains(text, "        # Left for the author, since the configuration names no model: "+
				"values.model, the name of the model core/ai-model/openai serves.\n        model: \"\"\n") {
				t.Errorf("the Aggregate does not leave values.model for the author:\n%s", text)
			}
			for _, key := range []string{"agent", "ferretdb", "documentdb", "model"} {
				if !slices.Contains(componentKeys(t, release), key) {
					t.Errorf("the Release places no %s:\n%s", key, release)
				}
			}
			if target == "compose" {
				for _, want := range []string{"key: OPENAI_API_KEY\n", "key: FERRETDB_PASSWORD\n", "key: PGPASSWORD\n",
					"key: POSTGRES_PASSWORD\n"} {
					if !strings.Contains(text, want) {
						t.Errorf("for compose the Aggregate does not declare %q:\n%s", want, text)
					}
				}
				if got := strings.Count(release, "origin: \"${FERRETDB_PASSWORD}\"\n"); got != 3 {
					t.Errorf("for compose the password is attached %d times by reference, want three:\n%s", got, release)
				}
				return
			}
			if strings.Contains(text, "values:\n                  key:") || strings.Count(release,
				"type: core/referenced-secret\n") != 4 {
				t.Errorf("for helm each secret is not named by its Secret, undeclared:\n%s\n%s", text, release)
			}
		})
	}
}

// TestEachGraphIsPublishedOnLoopbackForComposeAlone imports a configuration
// naming two graphs for compose, for helm and for no target, and reads the drafted
// Release: for compose each graph publishes a port on the host's loopback address
// beneath the comment the kit writes for one — one on Port and the other on the
// next port, since two ports of one Release never take one number — and no other
// instance publishes one; for helm, whose Recipes refuse infra.published_port,
// and for no target, nothing is published. The comment is written out rather than
// read from the kit, so the case pins the text the draft writes (a deliberate
// second copy).
//
// 🔴 PLANTED DEFECT: in langgraphRelease (render.go), drop the entry's
// Published: the compose row finds no port and fails.
func TestEachGraphIsPublishedOnLoopbackForComposeAlone(t *testing.T) {
	t.Parallel()
	const published = "        # Published on this machine's loopback address, 127.0.0.1: reachable from this machine " +
		"only, by any process running on it. Delete this line to keep the service internal.\n" +
		"        published_port: "
	config := []byte(`{"graphs":{"agent":"./agent.py:graph","review":"./review.py:graph"}}`)
	for _, target := range []string{"compose", "helm", ""} {
		out := Import(aggrega.ImportInput{Format: Format, Catalog: catalog, Target: target,
			Sources: []aggrega.ImportSource{{Name: "langgraph.json", Bytes: config}}}, hostParse(t))
		d, _ := drafted(t, out)
		release := files(out)["release.yaml"]
		var r struct {
			Spec struct {
				Components map[string]struct {
					Infra map[string]any `yaml:"infra"`
				} `yaml:"components"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(release), &r); err != nil {
			t.Fatalf("%q: the drafted Release does not read as YAML: %v\n%s", target, err, release)
		}
		var ports []int
		graphs := 0
		for key, c := range r.Spec.Components {
			port, set := c.Infra["published_port"]
			graph := d.Spec.Components[key].Type == Component
			if graph {
				graphs++
			}
			switch {
			case (target != "compose" || !graph) && set:
				t.Errorf("%q: %s publishes %v, want nothing published:\n%s", target, key, port, release)
			case target == "compose" && graph && !set:
				t.Errorf("compose: the graph %s publishes no port:\n%s", key, release)
			case set:
				n, _ := port.(int)
				ports = append(ports, n)
			}
		}
		slices.Sort(ports)
		want := []int(nil)
		if target == "compose" {
			want = []int{Port, Port + 1}
		}
		if graphs != 2 || !slices.Equal(ports, want) || strings.Count(release, published) != len(want) {
			t.Errorf("%q: the Release places %d graphs publishing %v, want two publishing %v, each beneath the kit's "+
				"comment:\n%s", target, graphs, ports, want, release)
		}
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

// TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments imports a
// configuration whose graph's id holds "{{", written into a comment of the
// Aggregate, and requires the Aggregate to run as a template — as the compiler
// evaluates its whole text, comments included — and the comment to read back
// with the id as the configuration writes it.
//
// 🔴 PLANTED DEFECT: in importer.CommentLine
// (aggrega-pdk/go/pkg/importer/definition.go), return the line unescaped: the
// Aggregate no longer parses as a template, and the case fails on it.
func TestANameHoldingDoubleBracesReadsBackInTheAggregatesComments(t *testing.T) {
	out := importSources(t, "", aggrega.ImportSource{Name: "langgraph.json", Bytes: []byte(
		`{"dependencies":["."],"graphs":{"agent{{x}}":"./agent.py:graph"}}`)})
	_, text := drafted(t, out)
	if got := evaluatedAggregate(t, text); !strings.Contains(got, "# The graph agent{{x}}.\n") {
		t.Errorf("the evaluated Aggregate does not name the graph agent{{x}}:\n%s", got)
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

// hubCatalog is the catalog of the aggrega-hub repository checked out beside
// this one. The check below reads it, and without that checkout fails rather
// than skipping, so check out aggrega-hub beside this repository first.
var hubCatalog = filepath.Join("..", "..", "..", "..", "aggrega-hub", "aggrega-catalog")

// hubFile reads one file of the hub's catalog, by its path beneath it.
func hubFile(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(hubCatalog, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read the hub's %s: %v", rel, err)
	}
	return string(body)
}

// TestTheBackendsTableIsWhatTheHubsEntitiesSay holds the backends table, which
// writes facts the catalog holds, to the hub's entities: each datastore's
// Component declares the password slot the table attaches its password on, and
// its Compose Recipe reads that password under the key the table declares —
// PostgreSQL's under the attachment's key with the table's key as its default,
// DocumentDB's under the table's key; FerretDB's Compose Recipe reads the
// password presented on its backend connection under the table's edge key, and
// its Component declares that socket and a credential slot on it; and the
// catalog's core/ferretdb Aggregate composes FerretDB and DocumentDB, wiring the
// one to the other on that socket and delivering exactly the variables the table
// delivers, in its order.
//
// It reads files outside this module, which the test cache does not track: a
// cached pass says nothing of the hub as it is now, so a run meant to catch the
// hub drifting is made with -count=1.
//
// 🔴 PLANTED DEFECT: in backends (langgraphimport.go), write the edge key as
// "PG_PASSWORD": the case fails naming FerretDB's Recipe.
func TestTheBackendsTableIsWhatTheHubsEntitiesSay(t *testing.T) {
	t.Parallel()
	last := func(component string) string { return component[strings.LastIndex(component, "/")+1:] }
	readsKey := func(recipe, key string, slotLine func(string) bool) bool {
		for _, line := range strings.Split(recipe, "\n") {
			if strings.Contains(line, `"`+key+`"`) && slotLine(line) {
				return true
			}
		}
		return false
	}
	declaresSlot := func(component, slot string) bool {
		return strings.Contains(component, "\n  featuresSchema:\n") && strings.Contains(component, "\n    "+slot+":\n")
	}
	pg := backends["default"]
	recipe := hubFile(t, "recipe/compose/"+last(pg.component)+"/1.0.0/recipe.yaml")
	if !readsKey(recipe, pg.key, func(l string) bool {
		return strings.Contains(l, "default ") && strings.Contains(l, ".features."+pg.slot+".")
	}) || !declaresSlot(hubFile(t, "component/"+pg.component+"/1.0.0/component.yaml"), pg.slot) {
		t.Errorf("%s does not read its %s password under %s by default, or its Component declares no such slot",
			pg.component, pg.slot, pg.key)
	}

	ferret := backends["mongo"]
	bk := ferret.behind
	if !readsKey(hubFile(t, "recipe/compose/"+bk.name+"/1.0.0/recipe.yaml"), bk.key, func(l string) bool {
		return strings.Contains(l, ".context.inject.env")
	}) || !declaresSlot(hubFile(t, "component/"+bk.component+"/1.0.0/component.yaml"), bk.slot) {
		t.Errorf("%s does not read its %s password under %s, or its Component declares no such slot", bk.component,
			bk.slot, bk.key)
	}
	proxy := hubFile(t, "component/"+ferret.component+"/1.0.0/component.yaml")
	if !readsKey(hubFile(t, "recipe/compose/"+ferret.name+"/1.0.0/recipe.yaml"), bk.edgeKey, func(l string) bool {
		return strings.Contains(l, "$env") || strings.Contains(l, "inject.env")
	}) || !strings.Contains(proxy, "\n  usesSchema:\n") || !strings.Contains(proxy, "\n    "+bk.socket+":\n") ||
		!strings.Contains(proxy, "        credential:\n") {
		t.Errorf("%s does not read the password presented on its %s connection under %s, or its Component declares "+
			"no such socket and credential slot", ferret.component, bk.socket, bk.edgeKey)
	}

	var stack struct {
		Spec struct {
			Components map[string]struct {
				Type string `yaml:"type"`
				Uses map[string]map[string]struct {
					Target string `yaml:"target"`
					Inject struct {
						Env []struct {
							In   string `yaml:"in"`
							From string `yaml:"from"`
						} `yaml:"env"`
					} `yaml:"inject"`
				} `yaml:"uses"`
			} `yaml:"components"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(hubFile(t, "aggregate/core/ferretdb/1.0.0/aggregate.yaml")), &stack); err != nil {
		t.Fatalf("the hub's FerretDB stack does not read as YAML: %v", err)
	}
	var delivered []string
	found := false
	for _, c := range stack.Spec.Components {
		if c.Type != ferret.component {
			continue
		}
		for _, edge := range c.Uses[bk.socket] {
			if stack.Spec.Components[edge.Target].Type != bk.component {
				continue
			}
			found = true
			for _, e := range edge.Inject.Env {
				delivered = append(delivered, e.In+"<-"+e.From)
			}
		}
	}
	var want []string
	for _, e := range bk.inject {
		want = append(want, e.In+"<-"+e.From)
	}
	if !found || !slices.Equal(delivered, want) {
		t.Errorf("the hub's FerretDB stack wires %s to %s delivering %v, want the table's %v (wired: %v)",
			ferret.component, bk.component, delivered, want, found)
	}
}
