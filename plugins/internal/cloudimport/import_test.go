// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The types of the toy format's resources.
const (
	toyServer  = "Toy.Data/servers"
	toySetting = toyServer + "/settings"
	toyNetwork = "Toy.Network/networks"
	toyQueue   = "Toy.Messaging/queues"
	toySearch  = "Toy.Search/services"
	toyWorker  = "Toy.Compute/workers"
)

// toyModuleURL is the address the toy format gives the module toy/search.
const toyModuleURL = "oci://registry.example/toy/search"

// The toy format is a format of its own, which the core drafts from as it
// drafts from any other: a JSON document listing its parameters, resources and
// outputs, each resource with an id, a type, a name, a parent, the ids it
// depends on, the module it calls and its properties. A property's value is a
// literal, or an object naming a parameter ($param), an attribute of another
// resource ($ref, with its path under attr), a credential read from another
// resource ($credential, with the call reading it under what) or text
// evaluated when the source deploys ($text).

// toyFormat is the toy format: its table maps a server to the catalog's
// PostgreSQL Component, folding each of its settings into its instance, and
// its type rules leave a network to the operator; a resource no row maps is
// drafted from the source, and a module no row maps as a Recipe sourcing it
// whole, the module writer recording each module it is handed in handed,
// where handed is not nil.
func toyFormat(handed *[]schemadraft.WrappedModule) Format {
	row := *postgresRow()
	row.Key, row.Recipe = toyServer, "toy-postgres"
	row.Values = []Placement{{From: []string{"version"}, Key: "version", Conv: "string"}}
	row.Infra = []Placement{{From: []string{"location"}, Key: "location"}}
	row.Secrets = []Placement{{From: []string{"password"}, Key: "admin_password"}}
	row.Provides = []Placement{{From: []string{"host"}, Key: "host"}}
	row.Folds = []Fold{{Type: toySetting, Key: []string{"infra", "overrides", "settings"}, From: []string{"value"}}}
	draft := fixtureDraft(nil)
	draft.Name, draft.Capability, draft.Syntax = "toy-schema", "toy", "json"
	return Format{Name: "toy", Target: "toy", Syntax: "json", Read: readToy,
		Table:   Table{Generated: []Row{row}},
		Types:   []TypeRule{{Type: toyNetwork, Class: ClassSubstrate}},
		Pointer: toyPointer, Draft: draft, Wrap: toyWrap(handed), Module: toyModule}
}

// toyParse parses a toy source: JSON, read as it is.
func toyParse(syntax string, source []byte) (json.RawMessage, error) {
	if syntax != "json" {
		return nil, errors.New("the toy format is parsed as json, not " + syntax)
	}
	if !json.Valid(source) {
		return nil, errors.New("the source is not JSON")
	}
	return json.RawMessage(source), nil
}

// readToy reads the request's sources into one graph, its nodes ordered by ID:
// a source that does not parse halts parse-failed, and whatever a source holds
// of another shape than the toy format's is read as far as it goes.
func readToy(in aggrega.ImportInput, _ Settings, parse ParseFunc) (*Graph, []aggrega.Diagnostic) {
	g := &Graph{Format: "toy"}
	var diags []aggrega.Diagnostic
	for _, src := range importer.Sources(in) {
		named := importer.Named(in, src)
		tree, err := parse("json", src.Bytes)
		if err != nil {
			diags = append(diags, importer.ParseFailed(named, "toy", err))
			continue
		}
		g.Read = append(g.Read, named.Name)
		for _, e := range importer.ObjectEntries(tree) {
			switch e.Key {
			case "parameters":
				for _, raw := range toyItems(e.Value) {
					g.Params = append(g.Params, toyParameter(raw))
				}
			case "resources":
				for i, raw := range toyItems(e.Value) {
					if n := toyNode(raw, fmt.Sprintf("%s#/resources/%d", named.Name, i)); n != nil {
						g.Nodes = append(g.Nodes, n)
					}
				}
			case "outputs":
				for _, raw := range toyItems(e.Value) {
					o := Output{}
					for _, m := range importer.ObjectEntries(raw) {
						switch m.Key {
						case "name":
							o.Name = toyString(m.Value)
						case "value":
							o.Value = toyProp("", m.Value).Value
						}
					}
					g.Outputs = append(g.Outputs, o)
				}
			}
		}
	}
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	return g, diags
}

// toyNode reads one resource, nil where it is not an object.
func toyNode(raw json.RawMessage, source string) *Node {
	if !toyIs(raw, '{') {
		return nil
	}
	n := &Node{Form: FormRaw, Source: source}
	for _, e := range importer.ObjectEntries(raw) {
		switch e.Key {
		case "id":
			n.ID = toyString(e.Value)
		case "type":
			n.Type = toyString(e.Value)
		case "name":
			n.Name = toyProp("name", e.Value).Value
		case "parent":
			n.Parent = toyString(e.Value)
		case "existing":
			n.Existing = string(bytes.TrimSpace(e.Value)) == "true"
		case "dependsOn":
			for _, dep := range toyItems(e.Value) {
				n.DependsOn = append(n.DependsOn, toyString(dep))
			}
		case "module":
			n.Form, n.Module = FormModule, &ModuleRef{}
			for _, m := range importer.ObjectEntries(e.Value) {
				switch m.Key {
				case "path":
					n.Module.Path = toyString(m.Value)
				case "version":
					n.Module.Version = toyString(m.Value)
				case "prefix":
					n.Module.Prefix = toyString(m.Value)
				}
			}
		case "properties":
			n.Body = toyBody(toyProp("", e.Value))
		}
	}
	return n
}

// toyBody is the properties p holds: an object's members, or an array's
// items, each an unnamed property.
func toyBody(p Prop) []Prop {
	if len(p.Props) > 0 {
		return p.Props
	}
	return p.Items
}

// toyParameter reads one parameter.
func toyParameter(raw json.RawMessage) Parameter {
	var p Parameter
	for _, e := range importer.ObjectEntries(raw) {
		switch e.Key {
		case "name":
			p.Name = toyString(e.Value)
		case "type":
			p.Type = toyString(e.Value)
		case "default":
			p.Default = toyProp("", e.Value).Value
		case "secure":
			p.Secure = string(bytes.TrimSpace(e.Value)) == "true"
		}
	}
	return p
}

// toyProp reads the value raw of the property name.
func toyProp(name string, raw json.RawMessage) Prop {
	p := Prop{Name: name}
	raw = bytes.TrimSpace(raw)
	switch {
	case toyIs(raw, '{'):
		entries := importer.ObjectEntries(raw)
		if v := toySpecial(entries); v != nil {
			p.Value = v
			return p
		}
		for _, e := range entries {
			p.Props = append(p.Props, toyProp(e.Key, e.Value))
		}
	case toyIs(raw, '['):
		for _, item := range toyItems(raw) {
			p.Items = append(p.Items, toyProp("", item))
		}
	}
	if len(p.Props) == 0 && len(p.Items) == 0 {
		p.Value = Literal{JSON: slices.Clone(raw)}
	}
	return p
}

// toySpecial reads an object naming a parameter, an attribute, a credential or
// deploy-time text, nil for any other object.
func toySpecial(entries []importer.ObjectEntry) Value {
	var attr []string
	var what string
	for _, e := range entries {
		switch e.Key {
		case "attr":
			for _, seg := range toyItems(e.Value) {
				attr = append(attr, toyString(seg))
			}
		case "what":
			what = toyString(e.Value)
		}
	}
	for _, e := range entries {
		switch e.Key {
		case "$param":
			return Param{Name: toyString(e.Value)}
		case "$ref":
			return Ref{Node: toyString(e.Value), Attr: attr}
		case "$credential":
			return Credential{Node: toyString(e.Value), What: what}
		case "$text":
			return Opaque{Text: toyString(e.Value), Target: "toy"}
		}
	}
	return nil
}

// toyIs reports whether raw opens with the delimiter open.
func toyIs(raw json.RawMessage, open byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == open
}

// toyItems returns the items of a JSON array, none for any other value.
func toyItems(raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

// toyString returns a JSON string, empty for any other value.
func toyString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// toyPointer is the toy target's pointer to an attribute of a resource the
// draft leaves to the operator.
func toyPointer(v Value, _ *Graph) (string, bool) {
	r, ok := v.(Ref)
	if !ok {
		return "", false
	}
	return "toy://" + strings.Join(append([]string{r.Node}, r.Attr...), "/"), true
}

// toyModule locates a module of the toy format under the toy registry, its
// Recipe binding telemetry itself; a module of no path is not located.
func toyModule(ref ModuleRef) ModuleSource {
	if ref.Path == "" {
		return ModuleSource{}
	}
	return ModuleSource{Kind: "toy-module", URL: "oci://registry.example/" + ref.Path,
		Fixed: []importer.Entry{{Key: "telemetry", Value: "false"}}}
}

// toyWrap is the toy format's module writer: the source block, a binding of
// each input under its parameter's name and of each fixed parameter, and each
// output published, each module it is handed recorded in handed, where handed
// is not nil.
func toyWrap(handed *[]schemadraft.WrappedModule) schemadraft.ModuleWriter {
	return func(m schemadraft.WrappedModule) (importer.RecipeDraft, []aggrega.Diagnostic) {
		if handed != nil {
			*handed = append(*handed, m)
		}
		var mapping strings.Builder
		for _, in := range m.Inputs {
			mapping.WriteString(in.Parameter() + ": {{ toJson " + in.Source.Template() + " }}\n")
		}
		for _, e := range m.Fixed {
			mapping.WriteString(e.Key + ": " + e.Value + "\n")
		}
		d := importer.RecipeDraft{ValuesMapping: mapping.String(),
			Source: "kind: " + m.SourceKind + "\nurl: " + m.URL + "\nversion: " + strconv.Quote(m.Version) + "\n"}
		for _, p := range m.Outputs {
			d.Provides = append(d.Provides, importer.Entry{Key: p.Key, Value: "module." + strings.Join(p.Attribute, ".")})
		}
		return d, nil
	}
}

// toyInput is the request importing the toy sources names, read from
// testdata/toy, for target with settings, against the catalog holding the
// PostgreSQL Component and each Component given.
func toyInput(t *testing.T, target, settings string, names []string, more ...aggrega.CatalogComponent) aggrega.ImportInput {
	t.Helper()
	in := request(settings, append([]aggrega.CatalogComponent{component(postgres, datastoreLabels("postgres"))},
		more...)...)
	in.Format, in.Target = "toy", target
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("testdata", "toy", name))
		if err != nil {
			t.Fatal(err)
		}
		in.Sources = append(in.Sources, aggrega.ImportSource{Name: name, Bytes: raw})
	}
	return in
}

// fileOf returns the content of the file of path the import writes, failing
// where it writes none.
func fileOf(t *testing.T, out aggrega.ImportOutput, path string) string {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("the import writes no %s among %q", path, filePaths(out))
	return ""
}

// filePaths returns the paths of the files the import writes, in order.
func filePaths(out aggrega.ImportOutput) []string {
	var paths []string
	for _, f := range out.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

// decisionIDs returns the node each decision names, in order.
func decisionIDs(out aggrega.ImportOutput) []string {
	var ids []string
	for _, d := range out.Decisions {
		ids = append(ids, d.SourceNode.ID)
	}
	return ids
}

// draftsNothing fails unless out carries diagnostics alone, every halt among
// them of code.
func draftsNothing(t *testing.T, out aggrega.ImportOutput, code string) {
	t.Helper()
	if out.Entities != nil || out.Decisions != nil || out.Files != nil {
		t.Errorf("the import drafts %d entities, %d decisions and the files %q, want nothing", len(out.Entities),
			len(out.Decisions), filePaths(out))
	}
	var found []aggrega.Diagnostic
	for _, d := range out.Diagnostics {
		if d.Severity == aggrega.SeverityHalt {
			found = append(found, d)
		}
	}
	if len(found) == 0 || slices.ContainsFunc(found, func(d aggrega.Diagnostic) bool { return d.Code != code }) {
		t.Fatalf("halts %+v, want halts of %s alone", found, code)
	}
}

// TestImportDraftsAggregateReleaseAndCatalog imports a source holding a mapped
// server folding a setting, a network left to the operator and a queue drafted
// from the source: the Aggregate, the Release and its Manifest are laid out as
// the kit's renderer lays them out, and the queue's Component and Recipe under
// catalog/, one decision for each resource.
func TestImportDraftsAggregateReleaseAndCatalog(t *testing.T) {
	out := Import(toyInput(t, "toy", "", []string{"main.json"}), toyFormat(nil), toyParse)
	noHalt(t, out.Diagnostics)
	if out.Error != "" {
		t.Fatalf("the import errs: %s", out.Error)
	}
	queue := schemadraft.NameFor(toyInput(t, "toy", "", nil), toyFormat(nil).Draft, toyQueue)
	recipe := "toy-messaging-queues"
	want := []string{"aggregate.yaml", "release.yaml", "manifest.yaml",
		"catalog/component/" + queue + "/0.1.0/component.yaml", "catalog/recipe/toy/" + recipe + "/0.1.0/recipe.yaml"}
	if got := filePaths(out); !slices.Equal(got, want) {
		t.Fatalf("the import writes %q, want %q", got, want)
	}
	if got, want := decisionIDs(out), []string{"db", "dbSetting", "net", "queue"}; !slices.Equal(got, want) {
		t.Errorf("decisions name %q, want one for each resource in order: %q", got, want)
	}
	var entities []string
	for _, e := range out.Entities {
		entities = append(entities, e.Kind+" "+e.Mode+" "+e.Name)
	}
	draft := importer.DraftName(toyInput(t, "toy", "", nil))
	if want := []string{"Component reference " + postgres, "Component draft " + queue, "Recipe draft " + recipe,
		"Aggregate draft " + draft, "Release draft " + draft, "Manifest draft " + draft}; !slices.Equal(entities, want) {
		t.Errorf("entities %q, want %q", entities, want)
	}

	aggregate := fileOf(t, out, "aggregate.yaml")
	for _, line := range []string{"# Sources read: main.json.\n",
		"#   - dbHost: carried by the instance db, provides key host\n",
		"# Substrate the source creates, left to the operator: net (" + toyNetwork + ").\n"} {
		if !strings.Contains(aggregate, line) {
			t.Errorf("the Aggregate lacks %q:\n%s", line, aggregate)
		}
	}
	wantBlock(t, aggregate, "db", `    db:
      type: core/datastore/postgres
      values:
        version: "16"
`)
	wantBlock(t, aggregate, "queue", `    queue:
      type: `+queue+`
      values:
        name: "orders"
      uses:
        db:
          db:
            target: db
            inject:
              property:
                - in: "host_name"
                  from: host
`)

	release := fileOf(t, out, "release.yaml")
	wantBlock(t, release, "db", `    db:
      infra:
        # From the template parameter region, its literal default.
        location: "westeurope"
        overrides:
          settings: "200"
`)
	wantBlock(t, release, "queue", `    queue:
      infra:
        location: "westeurope"
        overrides:
          retentionDays: 7
          networkId: "toy://net"
`)
	manifest := fileOf(t, out, "manifest.yaml")
	for _, line := range []string{"db: toy/toy-postgres", "queue: toy/" + recipe} {
		if !strings.Contains(manifest, line) {
			t.Errorf("the Manifest lacks %q:\n%s", line, manifest)
		}
	}

	component := fileOf(t, out, "catalog/component/"+queue+"/0.1.0/component.yaml")
	recipeText := fileOf(t, out, "catalog/recipe/toy/"+recipe+"/0.1.0/recipe.yaml")
	if !strings.Contains(component, "name: "+queue) || !strings.Contains(recipeText, "implements: "+queue+":0.1.0") {
		t.Errorf("the drafted Component and Recipe do not name %s:\n%s\n%s", queue, component, recipeText)
	}
	onlyCode(t, out.Diagnostics, CodeSubstrateLeftToOperator, 1)
}

// TestImportUnderTheLLMRenderModeWritesTheDecisionsOnce writes the decisions
// document alone, once, where the render_mode setting is llm, though the
// Aggregate's layout and the catalog's each write it.
func TestImportUnderTheLLMRenderModeWritesTheDecisionsOnce(t *testing.T) {
	out := Import(toyInput(t, "toy", `{"render_mode": "llm"}`, []string{"main.json"}), toyFormat(nil), toyParse)
	noHalt(t, out.Diagnostics)
	if got := filePaths(out); !slices.Equal(got, []string{"import-decisions.json"}) {
		t.Fatalf("the import writes %q, want the decisions document once", got)
	}
	var decisions []aggrega.ImportDecision
	if err := json.Unmarshal([]byte(out.Files[0].Content), &decisions); err != nil || len(decisions) != 4 {
		t.Errorf("the decisions document holds %d decisions (%v), want one for each of the 4 resources", len(decisions),
			err)
	}
}

// TestImportHaltDraftsNothing returns the diagnostics alone wherever a halt is
// raised: by the settings, the reader, the classification, a draft or the
// Release.
func TestImportHaltDraftsNothing(t *testing.T) {
	main, err := os.ReadFile(filepath.Join("testdata", "toy", "main.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		in       func() aggrega.ImportInput
		f        func() Format
		code     string
		mentions []string
	}{
		{"a reader halt", func() aggrega.ImportInput {
			in := toyInput(t, "toy", "", nil)
			in.Sources = []aggrega.ImportSource{{Name: "main.json", Bytes: main[:len(main)/2]}}
			return in
		}, func() Format { return toyFormat(nil) }, "parse-failed", []string{"main.json", "toy"}},
		{"a root setting that is not text", func() aggrega.ImportInput {
			return toyInput(t, "toy", `{"root": 7}`, []string{"main.json"})
		}, func() Format { return toyFormat(nil) }, string(CodeSourceRefused), []string{"root"}},
		{"a format reading no source", func() aggrega.ImportInput {
			return toyInput(t, "toy", "", []string{"main.json"})
		}, func() Format { f := toyFormat(nil); f.Read = nil; return f }, string(CodeSourceRefused),
			[]string{"importer's authors"}},
		{"two resources of one ID", func() aggrega.ImportInput {
			return toyInput(t, "toy", "", []string{"duplicate.json"})
		}, func() Format { return toyFormat(nil) }, string(CodeSourceRefused), []string{"queue", "rename"}},
		{"a credential the setting cannot judge", func() aggrega.ImportInput {
			return toyInput(t, "toy", `{"empty_credentials": "sometimes"}`, []string{"credential.json"})
		}, func() Format { return toyFormat(nil) }, "unknown-empty-credentials", []string{"empty_credentials"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := Import(tc.in(), tc.f(), toyParse)
			draftsNothing(t, out, tc.code)
			for _, d := range out.Diagnostics {
				if d.Severity == aggrega.SeverityHalt {
					mentions(t, "the halt", d.Message, tc.mentions...)
				}
			}
		})
	}
}

// TestImportWarnsNoNodesAndDraftsNothing warns no-nodes for each source where
// the graph read holds no node, or none once its repetitions are expanded, and
// drafts nothing.
func TestImportWarnsNoNodesAndDraftsNothing(t *testing.T) {
	read := func(g *Graph) func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic) {
		return func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic) { return g, nil }
	}
	for _, tc := range []struct {
		name    string
		read    func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic)
		sources []string
		also    Code
	}{
		{"a source holding no resource", readToy, []string{"empty.json"}, ""},
		{"no graph", read(nil), []string{"empty.json", "main.json"}, ""},
		{"no node but nil ones", read(&Graph{Nodes: []*Node{nil}}), []string{"empty.json", "main.json"}, ""},
		{"a repetition of no copy", read(&Graph{Nodes: []*Node{{ID: "queue", Type: toyQueue,
			Repeat: &Repetition{Keys: []string{}}}}}), []string{"main.json"}, CodeResourceNotCarried},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := toyFormat(nil)
			f.Read = tc.read
			in := toyInput(t, "toy", "", tc.sources)
			out := Import(in, f, toyParse)
			if out.Entities != nil || out.Decisions != nil || out.Files != nil {
				t.Errorf("the import drafts %d entities, %d decisions and %q, want nothing", len(out.Entities),
					len(out.Decisions), filePaths(out))
			}
			warned := withCode(out.Diagnostics, "no-nodes")
			if len(warned) != len(in.Sources) {
				t.Fatalf("no-nodes %+v, want one for each of %d sources", warned, len(in.Sources))
			}
			for i, d := range warned {
				if d.Severity != aggrega.SeverityWarn || d.SourceRef != in.Sources[i].Name {
					t.Errorf("no-nodes %+v, want a warning naming %s", d, in.Sources[i].Name)
				}
			}
			if tc.also != "" {
				onlyCode(t, out.Diagnostics, tc.also, 1)
			}
		})
	}
}

// TestImportIsDeterministic imports each source twice to one byte sequence.
func TestImportIsDeterministic(t *testing.T) {
	for _, name := range []string{"main.json", "module.json", "modules.json", "heldback.json"} {
		t.Run(name, func(t *testing.T) {
			held := component(schemadraft.NameFor(toyInput(t, "toy", "", nil), toyFormat(nil).Draft, toyQueue),
				map[string]string{"component.aggrega.ai/category": "component"})
			run := func() []byte {
				out := Import(toyInput(t, "toy", "", []string{name}, held), toyFormat(nil), toyParse)
				noHalt(t, out.Diagnostics)
				raw, err := json.Marshal(out)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			if a, b := run(), run(); !bytes.Equal(a, b) {
				t.Errorf("two runs differ:\n%s\n%s", a, b)
			}
		})
	}
}

// TestImportRealizationOnAnotherTarget names the row's Recipe as the mapped
// instance's realization on any target, and the drafted Recipe as the drafted
// instance's on the format's own target alone: on another, the drafted
// instance is in the Release and the Manifest names no realization for it.
func TestImportRealizationOnAnotherTarget(t *testing.T) {
	out := Import(toyInput(t, "other", "", []string{"main.json"}), toyFormat(nil), toyParse)
	noHalt(t, out.Diagnostics)
	manifest := fileOf(t, out, "manifest.yaml")
	if !strings.Contains(manifest, "db: other/toy-postgres") {
		t.Errorf("the Manifest does not map db to the row's Recipe on the other target:\n%s", manifest)
	}
	if strings.Contains(manifest, "queue:") {
		t.Errorf("the Manifest names a realization for the drafted queue on the other target:\n%s", manifest)
	}
	release := fileOf(t, out, "release.yaml")
	if block := instanceBlock(t, release, "queue"); strings.Contains(block, "toy://") {
		t.Errorf("the queue's entry writes the toy target's pointer on the other target:\n%s", block)
	}

	out = Import(toyInput(t, "toy", "", []string{"main.json"}), toyFormat(nil), toyParse)
	noHalt(t, out.Diagnostics)
	if manifest := fileOf(t, out, "manifest.yaml"); !strings.Contains(manifest, "queue: toy/toy-messaging-queues") {
		t.Errorf("the Manifest does not map the queue to its drafted Recipe on the format's own target:\n%s", manifest)
	}
}

// TestImportNeverPanicsOnMalformedInput imports truncated and wrong-typed
// sources, and graphs a reader should never return, without panicking: a
// source that does not parse halts parse-failed, one holding nothing the
// format reads warns no-nodes, and a halt anywhere drafts nothing.
func TestImportNeverPanicsOnMalformedInput(t *testing.T) {
	type tc struct {
		name   string
		source string
		read   func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic)
		// want is the code the import reports; drafts that it drafts.
		want   string
		drafts bool
	}
	var cases []tc
	for _, name := range []string{"main.json", "module.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", "toy", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, cut := range []int{1, len(raw) / 4, len(raw) / 2, 3 * len(raw) / 4, len(raw) - 2} {
			cases = append(cases, tc{name: fmt.Sprintf("%s cut at %d", name, cut), source: string(raw[:cut]),
				want: "parse-failed"})
		}
	}
	graph := func(g *Graph) func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic) {
		return func(aggrega.ImportInput, Settings, ParseFunc) (*Graph, []aggrega.Diagnostic) { return g, nil }
	}
	cases = append(cases,
		tc{name: "an array", source: `[]`, want: "no-nodes"},
		tc{name: "text", source: `"main"`, want: "no-nodes"},
		tc{name: "resources as an object", source: `{"resources": {"db": {}}}`, want: "no-nodes"},
		tc{name: "resources that are no objects", source: `{"resources": [7, "db", null, []]}`, want: "no-nodes"},
		tc{name: "a resource of nothing", source: `{"resources": [{}]}`,
			want: schemadraft.CodeResourceSchemaUnreadable},
		tc{name: "fields of the wrong type", source: `{"resources": [{"id": "q", "type": "` + toyQueue + `",
			"name": {"a": [1]}, "properties": [1, {"b": 2}, []], "dependsOn": "db", "parent": 3, "existing": "yes"}]}`,
			want: schemadraft.CodeResourceSchemaUnreadable},
		tc{name: "fields of the wrong type around properties", source: `{"resources": [{"id": "q", "type": "` +
			toyQueue + `", "name": {"a": [1]}, "properties": {"b": 2}, "dependsOn": "db", "parent": 3,
			"existing": "yes"}]}`, drafts: true},
		tc{name: "values of the wrong type", source: `{"parameters": [{"name": 1}, 7, {"name": "p", "default": {}}],
			"resources": [{"id": "q", "type": "` + toyQueue + `", "properties": {"a": {"$ref": 7}, "b": {"$param": ["p"]},
			"c": {"$text": {}}, "d": {"$ref": "q", "attr": [1, null]}, "e": {}, "f": []}}],
			"outputs": [{"value": {"$ref": "nowhere"}}, 7]}`, drafts: true},
		tc{name: "a module of no shape", source: `{"resources": [{"id": "m", "type": "` + toySearch + `",
			"module": "toy/search", "properties": "x"}]}`, want: string(CodeLinkedSourceNotRead)},
		tc{name: "a module of nameless parameters", source: `{"resources": [{"id": "m", "type": "` + toySearch + `",
			"module": {"path": "toy/search"}, "properties": [1, {"$ref": "m"}]}]}`, drafts: true},
		tc{name: "a resource reading itself", source: `{"resources": [{"id": "db", "type": "` + toyServer + `",
			"properties": {"location": {"$ref": "db", "attr": ["host"]}}, "dependsOn": ["db", "db", ""]}]}`, drafts: true},
		tc{name: "its own parent", source: `{"resources": [{"id": "s", "type": "` + toySetting + `", "parent": "s"}]}`,
			drafts: true},
		tc{name: "no graph", read: graph(nil), want: "no-nodes"},
		tc{name: "nil nodes", read: graph(&Graph{Nodes: []*Node{nil, nil}}), want: "no-nodes"},
		tc{name: "a module calling no module", read: graph(&Graph{Nodes: []*Node{{ID: "m", Type: toySearch,
			Form: FormAVM}}}), want: string(CodeLinkedSourceNotRead)},
		tc{name: "a module reading inside a parameter of no name", source: `{"resources": [
			{"id": "db", "type": "` + toyServer + `", "properties": {"location": "westeurope"}},
			{"id": "m", "type": "` + toySearch + `", "module": {"path": "toy/search"},
			"properties": [{"$ref": "db", "attr": ["host"]}]}]}`, want: string(CodeReferenceNotWired)},
		tc{name: "a node of no ID beside nil", read: graph(&Graph{Nodes: []*Node{nil, {Type: toyQueue,
			Body: []Prop{{Name: "x", Items: []Prop{{}}}}}}}), drafts: true},
		tc{name: "a member of no name", read: graph(&Graph{Nodes: []*Node{{ID: "q", Type: toyQueue,
			Body: []Prop{{}, {Name: "x"}}}}}), want: schemadraft.CodeResourceSchemaUnreadable},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("the import panics: %v", r)
				}
			}()
			f := toyFormat(nil)
			if c.read != nil {
				f.Read = c.read
			}
			in := toyInput(t, "toy", "", nil)
			in.Sources = []aggrega.ImportSource{{Name: "main.json", Bytes: []byte(c.source)}}
			out := Import(in, f, toyParse)
			if halts(out.Diagnostics) && (out.Entities != nil || out.Decisions != nil || out.Files != nil) {
				t.Errorf("the import halts and drafts %q", filePaths(out))
			}
			paths := filePaths(out)
			for i, p := range paths {
				if slices.Contains(paths[:i], p) {
					t.Errorf("the import writes %s twice", p)
				}
			}
			if c.drafts {
				noHalt(t, out.Diagnostics)
				if len(out.Files) == 0 {
					t.Errorf("the import drafts nothing: %+v", out.Diagnostics)
				}
				return
			}
			if got := withCode(out.Diagnostics, Code(c.want)); len(got) == 0 {
				t.Errorf("diagnostics %+v, want %s", out.Diagnostics, c.want)
			}
		})
	}
}
