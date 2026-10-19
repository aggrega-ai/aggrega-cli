// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package difyimport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk-go/pkg/importer"
)

// The Dify importer on the host toolchain: each case hands Import a request and
// the parse the host would run — the compiler's own YAML parser, its refusal read
// as the import socket reads it — and reads what it drafts.

// hostParse parses source as the host's YAML parser does and refuses it as the
// import socket hands an importer a parser's refusal.
func hostParse(_ string, source []byte) (json.RawMessage, error) {
	out := parsercore.ParseYAML(source)
	if err := importersocket.ParseRefusal(out); err != nil {
		return nil, err
	}
	return out.Tree, nil
}

// fixture reads one committed Dify fixture (internal/importops/testdata/dify).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "importops", "testdata", "dify",
		filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read the fixture %s: %v", name, err)
	}
	return body
}

// catalog is a view of a workspace catalog holding the standard library's Dify
// API server, with the labels the hub gives it.
var catalog = &aggrega.CatalogProjection{Components: []aggrega.CatalogComponent{
	{Name: Server, Version: "1.0.0", Labels: map[string]string{
		"component.aggrega.ai/type": "server", "component.aggrega.ai/category": "ai-agent"},
		Values: []string{"app_definition"}},
}}

// importOne imports body as the one source of a request, against catalog, with
// settings.
func importOne(body []byte, settings string) aggrega.ImportOutput {
	in := aggrega.ImportInput{Format: Format, Bytes: body, Catalog: catalog}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return Import(in, hostParse)
}

// The key-shaped values the workflow fixture plants: in a secret variable, and
// in the key an HTTP request node authenticates with.
const (
	plantedSecret  = "sk-planted-dify-secret-needle-4f9c2a7e"
	plantedRequest = "sk-planted-dify-request-needle-8b3d61f0"
)

// carried reads the drafted Aggregate of out as YAML and returns the application
// the core/dify entry carries in app_definition, as an Aggregate's template
// reading gives it back — every {{"{{"}} read as the two braces — and the
// Aggregate's text; ok is false where the entry carries none.
func carried(t *testing.T, out aggrega.ImportOutput) (app string, ok bool, text string) {
	t.Helper()
	for _, f := range out.Files {
		if f.Path != "aggregate.yaml" {
			continue
		}
		var doc struct {
			Spec struct {
				Components map[string]any `yaml:"components"`
				Aggregates map[string]struct {
					Type       string         `yaml:"type"`
					Parameters map[string]any `yaml:"parameters"`
				} `yaml:"aggregates"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal([]byte(f.Content), &doc); err != nil {
			t.Fatalf("the drafted Aggregate does not read as YAML: %v\n%s", err, f.Content)
		}
		if len(doc.Spec.Components) != 0 {
			t.Errorf("the draft holds Components %v; the module form drafts none", doc.Spec.Components)
		}
		entry, found := doc.Spec.Aggregates["dify"]
		if !found || entry.Type != Module {
			t.Fatalf("the draft composes no %s entry keyed dify:\n%s", Module, f.Content)
		}
		definition, ok := entry.Parameters[DefinitionParameter].(string)
		return strings.ReplaceAll(definition, `{{"{{"}}`, "{{"), ok, f.Content
	}
	t.Fatalf("the import wrote no aggregate.yaml: %+v (diagnostics %+v)", out.Files, out.Diagnostics)
	return "", false, ""
}

// codesAndRefs lists each diagnostic as its code and the source node it names.
func codesAndRefs(out aggrega.ImportOutput) []string {
	var got []string
	for _, d := range out.Diagnostics {
		got = append(got, d.Code+" "+d.SourceRef)
	}
	return got
}

// TestAWorkflowApplicationIsDraftedAsTheInstallationCarryingIt imports the
// authored workflow application with no setting: the draft is aggregate.yaml
// alone, composing core/dify with the application carried byte for byte in its
// app_definition — Dify's {{#…#}} references and the double quotes included — and
// the comment naming the owner account the Release supplies and that the import
// does not check the catalog for core/dify. No Component is drafted and nothing
// is wired. The secret variable and the HTTP request node's key, each holding a
// value, are carried as written and named in a credential-carried warning, as
// are the request's headers; the secret written empty raises nothing. The tool,
// the knowledge base, the model and the two plugins are each reported as
// configured inside Dify, in a warning and in the node's decision; every node
// is kept in the flow.
//
// 🔴 PLANTED DEFECT: in configuredOf (walk.go), return "" for a knowledge-retrieval
// node: the warning naming 1760000000003 is missing, and the case fails.
func TestAWorkflowApplicationIsDraftedAsTheInstallationCarryingIt(t *testing.T) {
	t.Parallel()
	source := fixture(t, "authored/workflow.yml")
	out := importOne(source, "")

	if len(out.Files) != 1 {
		t.Fatalf("the import wrote %+v, want aggregate.yaml alone", out.Files)
	}
	app, ok, text := carried(t, out)
	if !ok || app != string(source) {
		t.Errorf("the carried application differs from the source:\n%s", app)
	}
	for _, want := range []string{
		"  aggregates:\n",
		"    # An import sees the Components of the workspace's catalog and none of its Aggregates, so it does not " +
			"check that the catalog holds core/dify.\n",
		"    #   - infra.owner_email on the api component of core/dify, ",
		"    #   - the owner_password slot on that api component, ",
		"    dify:\n      type: core/dify\n      parameters:\n        app_definition: |\n          app:\n",
		`'Answer {{"{{"}}#1760000000001.question#}} from {{"{{"}}#context#}} only, or`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the draft does not hold %q:\n%s", want, text)
		}
	}
	wantDiags := []string{
		"credential-carried workflow",
		"credential-carried 1760000000005",
		"credential-carried 1760000000005",
		"part-configured-in-platform 1760000000002",
		"part-configured-in-platform 1760000000003",
		"part-configured-in-platform 1760000000004",
		"part-configured-in-platform dependencies.0",
		"part-configured-in-platform dependencies.1",
	}
	if got := codesAndRefs(out); !reflect.DeepEqual(got, wantDiags) {
		t.Errorf("the import raised %v, want %v", got, wantDiags)
	}
	for _, d := range out.Diagnostics {
		if strings.Contains(d.Message, plantedSecret) || strings.Contains(d.Message, plantedRequest) {
			t.Errorf("a diagnostic names a planted value: %q", d.Message)
		}
	}
	for i, want := range []string{
		"the value of environment_variables.ticketing_api_key of node workflow is carried as written",
		"the value of authorization.config.api_key of node 1760000000005 is carried as written",
		"the value of headers of node 1760000000005 is carried as written",
		`node 1760000000002 (tool) names the tool "current_time" of the "builtin" tool provider ` +
			`"langgenius/time/time", whose credentials are set inside Dify, and core/ai-agent/dify configures that`,
		"node 1760000000003 (knowledge-retrieval) names a knowledge base, named by an identifier of the Dify " +
			"installation",
		`node 1760000000004 (llm) names the model "gpt-4o-mini" of the model provider "langgenius/openai/openai"`,
		`node dependencies.0 (marketplace) names the plugin "langgenius/openai:0.2.6@`,
	} {
		if !strings.HasPrefix(out.Diagnostics[i].Message, want) {
			t.Errorf("diagnostic %d reads %q, want it to open with %q", i, out.Diagnostics[i].Message, want)
		}
	}
	if len(out.Decisions) != 8 {
		t.Errorf("the import recorded %d decisions, want one per node and dependency", len(out.Decisions))
	}
	for _, d := range out.Decisions {
		if d.Chosen != nil || !strings.HasPrefix(d.Rationale, "kept in the flow core/ai-agent/dify carries") {
			t.Errorf("node %s is decided %+v: %q, want it kept in the flow", d.SourceNode.ID, d.Chosen, d.Rationale)
		}
	}
	var entities []string
	for _, e := range out.Entities {
		entities = append(entities, e.Kind+" "+e.Mode+" "+e.Name)
	}
	if want := []string{"Aggregate reference core/dify", "Aggregate draft imported/flow"}; !reflect.DeepEqual(entities, want) {
		t.Errorf("the import emitted %v, want %v", entities, want)
	}
}

// TestAChatApplicationIsDraftedAsTheInstallationCarryingIt imports the authored
// chat application: the application carried byte for byte, its model, its
// knowledge base and its plugin reported as configured inside Dify, and nothing
// reported of a credential, since it holds none.
func TestAChatApplicationIsDraftedAsTheInstallationCarryingIt(t *testing.T) {
	t.Parallel()
	source := fixture(t, "authored/chat.yml")
	out := importOne(source, "")
	app, ok, text := carried(t, out)
	if !ok || app != string(source) {
		t.Errorf("the carried application differs from the source:\n%s", app)
	}
	if !strings.Contains(text, `'Answer from {{"{{"}}#context#}} only, and quote the policy "as written".'`) {
		t.Errorf("the draft does not carry the prompt escaped:\n%s", text)
	}
	want := []string{
		"part-configured-in-platform model_config.model",
		"part-configured-in-platform model_config.dataset_configs.datasets.0",
		"part-configured-in-platform dependencies.0",
	}
	if got := codesAndRefs(out); !reflect.DeepEqual(got, want) {
		t.Errorf("the import raised %v, want %v", got, want)
	}
	if msg := out.Diagnostics[0].Message; !strings.Contains(msg, `the model "claude-3-5-haiku-20241022" of the model `+
		`provider "langgenius/anthropic/anthropic"`) {
		t.Errorf("the model is reported as %q", msg)
	}
}

// TestADSLNewerThanThePinnedEngineIsReported imports the chat application with
// its version rewritten: a version newer than DSLVersion is reported first,
// under dsl-version-newer; the pinned version, an older one and one that does not
// read as a version are not.
func TestADSLNewerThanThePinnedEngineIsReported(t *testing.T) {
	t.Parallel()
	source := fixture(t, "authored/chat.yml")
	for _, tc := range []struct {
		version string
		newer   bool
	}{
		{"0.7.0", false}, {"0.7.1", true}, {"0.8.0", true}, {"1.0.0", true}, {"0.6.9", false}, {"0.7", false},
		{"'next'", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			body := bytes.Replace(source, []byte("\nversion: 0.7.0\n"), []byte("\nversion: "+tc.version+"\n"), 1)
			out := importOne(body, "")
			got := len(out.Diagnostics) > 0 && out.Diagnostics[0].Code == "dsl-version-newer"
			if got != tc.newer {
				t.Fatalf("version %s reported newer %v, want %v: %+v", tc.version, got, tc.newer, out.Diagnostics)
			}
			if got && !strings.Contains(out.Diagnostics[0].Message, `version "`+tc.version+`" of the Dify DSL, newer than `+
				DSLVersion+`, the version Dify `+EngineVersion+` writes`) {
				t.Errorf("the version is reported as %q", out.Diagnostics[0].Message)
			}
		})
	}
}

// TestTheEmptyCredentialsSettingDecidesWhatTheApplicationCarries imports the
// workflow application under each value of empty_credentials: with none, every
// value is carried byte for byte; with marked, the secret variable and the
// request's key are written empty in the carried application, each reported
// under credential-emptied, and every other byte is as exported; with all, the
// request's headers are written empty too. No planted value reaches a diagnostic
// or a decision, and under marked and all none reaches the draft.
//
// 🔴 PLANTED DEFECT: in credentialsOf (credentials.go), skip the environment
// variables: under marked the secret is carried, and the case fails.
func TestTheEmptyCredentialsSettingDecidesWhatTheApplicationCarries(t *testing.T) {
	t.Parallel()
	source := string(fixture(t, "authored/workflow.yml"))
	for _, needle := range []string{plantedSecret, plantedRequest} {
		if !strings.Contains(source, needle) {
			t.Fatalf("the needle: the search does not find %s in the fixture, so it finds nothing", needle)
		}
	}
	marked := strings.Replace(strings.Replace(source, "value: "+plantedSecret, "value: ''", 1),
		"api_key: "+plantedRequest, "api_key: ''", 1)
	all := strings.Replace(marked, "headers: X-Source:dify", "headers: ''", 1)
	for _, tc := range []struct {
		setting, want string
		codes         []string
	}{
		{"none", source, []string{"credential-carried", "credential-carried", "credential-carried"}},
		{"marked", marked, []string{"credential-emptied", "credential-emptied", "credential-carried"}},
		{"all", all, []string{"credential-emptied", "credential-emptied", "credential-emptied"}},
	} {
		t.Run(tc.setting, func(t *testing.T) {
			t.Parallel()
			out := importOne([]byte(source), `{"empty_credentials":"`+tc.setting+`"}`)
			app, ok, text := carried(t, out)
			if !ok || app != tc.want {
				t.Errorf("the carried application is\n%s\nwant\n%s", app, tc.want)
			}
			var got []string
			for _, d := range out.Diagnostics {
				if strings.HasPrefix(d.Code, "credential-") {
					got = append(got, d.Code)
				}
			}
			if !reflect.DeepEqual(got, tc.codes) {
				t.Errorf("the import reported %v, want %v", got, tc.codes)
			}
			record, _ := json.Marshal([]any{out.Diagnostics, out.Decisions})
			for _, needle := range []string{plantedSecret, plantedRequest} {
				if bytes.Contains(record, []byte(needle)) {
					t.Errorf("a diagnostic or a decision names %s", needle)
				}
				if tc.setting != "none" && strings.Contains(text, needle) {
					t.Errorf("the draft carries %s under %s", needle, tc.setting)
				}
			}
		})
	}
}

// TestAWrittenSourceReadingDifferentlyIsNotCarried plants, in the workflow
// application, an alias reading the secret variable's value through an anchor
// on it, and imports it under empty_credentials marked: written empty, the
// value the alias reads is emptied too, so the host's parser reads the written
// application as a tree differing beyond the values asked about. The
// application is not carried, app_definition is left unset, a
// definition-not-carried warning says why, and no planted value reaches the
// draft, a diagnostic or a decision. With no setting the same application is
// carried as written.
//
// 🔴 PLANTED DEFECT: in sourceWriter.write (aggrega-pdk/go/pkg/importer/credentials.go),
// replace `if !sameTree(w.reparse, out, want) {` with `if false {`: the
// application is carried, and the case fails.
func TestAWrittenSourceReadingDifferentlyIsNotCarried(t *testing.T) {
	t.Parallel()
	source := string(fixture(t, "authored/workflow.yml"))
	planted := strings.Replace(strings.Replace(source, "value: "+plantedSecret, "value: &ticketing "+plantedSecret, 1),
		"value: Hello", "value: *ticketing", 1)
	if planted == source || strings.Count(planted, "ticketing") != strings.Count(source, "ticketing")+2 {
		t.Fatal("the plant did not land in the fixture")
	}

	out := importOne([]byte(planted), `{"empty_credentials":"marked"}`)
	_, ok, text := carried(t, out)
	if ok || !strings.Contains(text, "      # parameters.app_definition is left unset: the import's diagnostics say why "+
		"the flow is not carried.\n") {
		t.Errorf("the application is carried though it does not read back as asked:\n%s", text)
	}
	want := "the flow is not carried in parameters.app_definition: its bytes with its credential values written empty " +
		"do not read back as its tree with exactly those values emptied, so a credential it holds could not be kept out " +
		"of the draft"
	if len(out.Diagnostics) == 0 || out.Diagnostics[0].Code != "definition-not-carried" || out.Diagnostics[0].Message != want {
		t.Errorf("the import raised %+v, want first definition-not-carried: %q", out.Diagnostics, want)
	}
	record, _ := json.Marshal([]any{out.Diagnostics, out.Decisions, out.Files})
	for _, needle := range []string{plantedSecret, plantedRequest} {
		if bytes.Contains(record, []byte(needle)) {
			t.Errorf("the import's output names %s", needle)
		}
	}

	if app, ok, _ := carried(t, importOne([]byte(planted), "")); !ok || app != planted {
		t.Errorf("with no setting the application is not carried as written:\n%s", app)
	}
}

// TestTwoImportsAreByteIdentical imports each fixture twice and requires the two
// outputs to be one.
func TestTwoImportsAreByteIdentical(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"authored/workflow.yml", "authored/chat.yml"} {
		for _, settings := range []string{"", `{"empty_credentials":"all"}`} {
			first, _ := json.Marshal(importOne(fixture(t, name), settings))
			second, _ := json.Marshal(importOne(fixture(t, name), settings))
			if !bytes.Equal(first, second) {
				t.Errorf("two imports of %s under %q differ", name, settings)
			}
		}
	}
}

// TestTheCredentialLocator lists what credentialsOf locates in the workflow
// application, in the order the DSL writes it, each of its class: both secret
// variables, the key and the headers and query parameters of the HTTP request
// node — and not the plain variable.
func TestTheCredentialLocator(t *testing.T) {
	t.Parallel()
	tree, err := hostParse(Syntax, fixture(t, "authored/workflow.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range credentialsOf(tree) {
		got = append(got, c.Node+" "+c.Field+" "+c.Pointer+" "+classOf(c.Class))
	}
	want := []string{
		"workflow environment_variables.support_api_token /workflow/environment_variables/0/value marked",
		"workflow environment_variables.ticketing_api_key /workflow/environment_variables/1/value marked",
		"1760000000005 authorization.config.api_key /workflow/graph/nodes/4/data/authorization/config/api_key marked",
		"1760000000005 headers /workflow/graph/nodes/4/data/headers unmarked",
		"1760000000005 params /workflow/graph/nodes/4/data/params unmarked",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the locator found\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func classOf(c importer.CredentialClass) string {
	switch c {
	case importer.CredentialMarked:
		return "marked"
	case importer.CredentialUnmarked:
		return "unmarked"
	}
	return "server"
}

// TestEmptyInSourceWritesOnlyTheValue writes one value empty in each layout the
// writer reads and holds the result byte for byte — and, read through the
// host's parser, to the source's tree with that value alone written "" — and
// refuses each layout it does not read.
func TestEmptyInSourceWritesOnlyTheValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, pointer, want string
	}{
		{"a plain scalar", "a:\n  value: sk-x\n  other: y\n", "/a/value", "a:\n  value: ''\n  other: y\n"},
		{"a quoted scalar and its comment", "value: 'sk-x' # note\nb: 1\n", "/value", "value: ''\nb: 1\n"},
		{"a quoted scalar continued", "k:\n  value: 'abc\n    def'\n  n: 2\n", "/k/value", "k:\n  value: ''\n  n: 2\n"},
		{"a plain scalar continued", "k:\n  value: abc\n    def\n\n  n: 2\n", "/k/value", "k:\n  value: ''\n\n  n: 2\n"},
		{"a block scalar whose line opens with a hash", "value: |\n  abc\n  #tail\nn: 1\n", "/value",
			"value: ''\nn: 1\n"},
		{"an anchored scalar", "value: &k sk-x\nother: y\n", "/value", "value: &k ''\nother: y\n"},
		{"an item of a sequence at its key's column", "list:\n- name: a\n  value: x\n- name: b\n  value: y\n",
			"/list/1/value", "list:\n- name: a\n  value: x\n- name: b\n  value: ''\n"},
		{"an item of an indented sequence, on its dash", "list:\n  - value: x\n    name: a\n", "/list/0/value",
			"list:\n  - value: ''\n    name: a\n"},
		{"a quoted key", "'odd key': x\nb: 1\n", "/odd key", "'odd key': ''\nb: 1\n"},
		{"line breaks of two bytes", "value: x\r\nb: 1\r\n", "/value", "value: ''\r\nb: 1\r\n"},
		{"a document-start marker", "---\nvalue: x\n", "/value", "---\nvalue: ''\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := emptyInSource([]byte(tc.source), []importer.Credential{{Pointer: tc.pointer}})
			if err != nil {
				t.Fatalf("the writer refused: %v", err)
			}
			if string(out) != tc.want {
				t.Fatalf("the writer wrote %q, want %q", out, tc.want)
			}
			before, err1 := hostParse(Syntax, []byte(tc.source))
			after, err2 := hostParse(Syntax, out)
			if err1 != nil || err2 != nil {
				t.Fatalf("the parser refused: %v, %v", err1, err2)
			}
			want, err := importer.Rewrite(before, []importer.Edit{{Pointer: tc.pointer, Value: json.RawMessage(`""`)}})
			if err != nil {
				t.Fatal(err)
			}
			if !jsonEqual(t, after, want) {
				t.Errorf("the written source reads as %s, want %s", after, want)
			}
		})
	}
	for _, tc := range []struct{ name, source, pointer string }{
		{"a key the mapping does not hold", "a: 1\n", "/b"},
		{"a value inside a flow collection", "value: {a: b}\n", "/value/a"},
		{"a flow collection", "value: {a: b}\n", "/value"},
		{"an item past the sequence's end", "list:\n- a: 1\n", "/list/3/a"},
		{"a value opening on the next line", "value:\n  x\n", "/value"},
		{"an item of a sequence itself", "list:\n- a\n", "/list/0"},
	} {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			t.Parallel()
			if out, err := emptyInSource([]byte(tc.source), []importer.Credential{{Pointer: tc.pointer}}); err == nil {
				t.Errorf("the writer wrote %q; it reads no such layout", out)
			}
		})
	}
}

// hubDifyComponent is the hub's core/ai-agent/dify, in the aggrega-hub
// repository checked out beside this one, relative to this package.
var hubDifyComponent = filepath.Join("..", "..", "..", "..", "aggrega-hub", "aggrega-catalog", "component", "core",
	"ai-agent", "dify", "1.0.0", "component.yaml")

// TestTheEnginePinIsTheCatalogs holds EngineVersion, the engine the importer
// compares a DSL's version against, to the default of infra.engine_version the
// hub's core/ai-agent/dify declares, so the catalog moving its default fails here
// rather than leaving the importer comparing against an engine no Release runs by
// default. Without the aggrega-hub checkout beside this repository it fails
// rather than skipping.
func TestTheEnginePinIsTheCatalogs(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(hubDifyComponent)
	if err != nil {
		t.Fatalf("read the hub's core/ai-agent/dify: %v", err)
	}
	var component struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Schema struct {
				Infra map[string]struct {
					Default any `yaml:"default"`
				} `yaml:"infra"`
			} `yaml:"schema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(body, &component); err != nil {
		t.Fatalf("the hub's core/ai-agent/dify does not read as YAML: %v", err)
	}
	if component.Metadata.Name != Server {
		t.Fatalf("the hub's file names %q, want %s", component.Metadata.Name, Server)
	}
	pin, ok := component.Spec.Schema.Infra["engine_version"]
	if !ok {
		t.Fatalf("the hub's %s declares no infra.engine_version under spec.schema.infra", Server)
	}
	if pin.Default != EngineVersion {
		t.Errorf("the hub's %s runs Dify %v by default, and the importer compares against %s", Server, pin.Default,
			EngineVersion)
	}
}

// jsonEqual reports whether two JSON documents hold one value.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
