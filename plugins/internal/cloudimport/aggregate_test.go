// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
	"go.yaml.in/yaml/v3"
)

// openingGraph holds a mapped server and a drafted site, a subnet the source
// creates and a vault it reads, read from two templates beside a file not
// read, with outputs and parameters of each kind the opening comment names.
func openingGraph() *Graph {
	web := staticSite(leaf(Param{Name: "siteSku"}, "properties", "sku"))
	web.ID = "staticSite"
	subnet := &Node{ID: "subnet", Type: subnetType, Form: FormRaw, Name: Param{Name: "subnetName"}}
	vault := &Node{ID: "vault", Type: vaultType, Form: FormRaw, Existing: true, Name: text("kv-shared")}
	return &Graph{
		Nodes: []*Node{pgServer(), web, subnet, vault},
		Params: []Parameter{
			{Name: "siteSku", Type: "string"}, {Name: "subnetName", Type: "string"}, {Name: "unused", Type: "int"},
		},
		Outputs: []Output{
			{Name: "dbHost", Value: fqdn},
			{Name: "siteId", Value: Ref{Node: "staticSite"}},
			{Name: "subnetId", Value: Ref{Node: "subnet"}},
			{Name: "composed", Value: Compose{Parts: []Value{text("https://"), fqdn}}},
		},
		Read:    []string{"main.json", "nested/db.json"},
		NotRead: []string{"notes.json"},
	}
}

func TestAggregateOpeningComment(t *testing.T) {
	in, f := edgeView(), edgeFormat(nil)
	_, text, _ := drafted(t, in, f, openingGraph())
	want := `# Sources read: main.json, nested/db.json.
# Not read: notes.json.
# Outputs of the source:
#   - dbHost: carried by the instance server, provides key host
#   - siteId: carried by the instance staticsite, provides key id
#   - subnetId: carried by no instance
#   - composed: carried by no instance
# Parameters the draft carries nowhere: subnetName, unused.
# Substrate the source creates, left to the operator: subnet (Microsoft.Network/virtualNetworks/subnets).
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: ` + importer.DraftName(in) + `
  version: 0.1.0
  description: ` + importer.DraftDescription("arm") + `
spec:
  components:
    server:
      type: core/datastore/postgres
    # The resource staticSite.
    staticsite:
      type: ` + schemadraft.NameFor(in, f.Draft, siteType) + `
      values:
        name: "site1"
`
	if text != want {
		t.Errorf("the Aggregate is\n%s\nwant\n%s", text, want)
	}
}

// valuesFormat is edgeFormat whose server row places six properties in
// values, each through a converter or none, and which writes a literal string
// as an ARM template writes one.
func valuesFormat() Format {
	f := edgeFormat(nil)
	f.Text = func(s string) string {
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			return "[" + s
		}
		return s
	}
	pg := f.Table.Generated[0]
	pg.Values = []Placement{
		{From: []string{"properties", "version"}, Key: "version", Conv: "string"},
		{From: []string{"properties", "storage", "storageSizeGB"}, Key: "storage", Conv: "unit:Gi"},
		{From: []string{"properties", "highAvailability", "standbyCount"}, Key: "replicas", Conv: "integer"},
		{From: []string{"properties", "label"}, Key: "label"},
		{From: []string{"location"}, Key: "region"},
		{From: []string{"properties", "tags"}, Key: "tags"},
	}
	f.Table.Generated[0] = pg
	return f
}

// valuesGraph holds a server writing the properties valuesFormat places, and a
// database folded into it.
func valuesGraph() *Graph {
	s := pgServer()
	s.Body = merged(
		leaf(Opaque{Text: "[resourceGroup().location]", Target: "arm"}, "location"),
		leaf(literal("16"), "properties", "version"),
		leaf(literal("32"), "properties", "storage", "storageSizeGB"),
		leaf(text("2"), "properties", "highAvailability", "standbyCount"),
		leaf(text("[x]"), "properties", "label"),
		leaf(literal(`{"team": "[x]", "tier": 2}`), "properties", "tags"),
	)
	return &Graph{Nodes: []*Node{s, {ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server",
		Name: text("orders")}}}
}

// TestAggregateValuesOfAMappedInstance writes the values a mapped instance's
// row places, each through its converter, a literal string as the format's
// own target writes it there, deploy-time text only on its own target, and a
// child folded into the instance at its key.
func TestAggregateValuesOfAMappedInstance(t *testing.T) {
	in := edgeView()
	in.Target = "arm"
	_, text, diags := drafted(t, in, valuesFormat(), valuesGraph())
	wantBlock(t, text, "server", `    server:
      type: core/datastore/postgres
      values:
        version: "16"
        storage: "32Gi"
        replicas: 2
        label: "[[x]"
        region: "[resourceGroup().location]"
        tags: {"team": "[[x]", "tier": 2}
        database: "orders"
`)
	onlyCode(t, diags, CodeValueNotCarried, 0)

	in.Target = "terraform"
	_, text, diags = drafted(t, in, valuesFormat(), valuesGraph())
	wantBlock(t, text, "server", `    server:
      type: core/datastore/postgres
      values:
        version: "16"
        storage: "32Gi"
        replicas: 2
        label: "[x]"
        tags: {"team": "[x]", "tier": 2}
        database: "orders"
`)
	w := onlyCode(t, diags, CodeValueNotCarried, 1)[0]
	mentions(t, "the warning", w.Message, "server", "location", "values.region", "arm", "terraform")
}

// TestAggregateValueHoldingTemplateTextReadsBack writes a value holding
// template text — a mapped instance's, a drafted instance's, and one inside an
// object — through EscapeTemplateText, so once the Aggregate is evaluated as a
// template, as the compiler evaluates it, each reads back as the source wrote
// it.
func TestAggregateValueHoldingTemplateTextReadsBack(t *testing.T) {
	const held = "{{ .Values }}"
	f := edgeFormat(nil)
	pg := f.Table.Generated[0]
	pg.Values = []Placement{{From: []string{"properties", "version"}, Key: "version"},
		{From: []string{"properties", "tags"}, Key: "tags"}}
	f.Table.Generated[0] = pg
	s := pgServer()
	s.Body = merged(leaf(text(held), "properties", "version"),
		leaf(literal(`{"note": "{{ .Values }}"}`), "properties", "tags"))
	named := staticSite()
	named.Name = text(held)
	_, text, _ := drafted(t, edgeView(), f, &Graph{Nodes: []*Node{s, named}})
	if !strings.Contains(text, "version: \"{{`{{`}} .Values }}\"\n") {
		t.Errorf("the value is not written through EscapeTemplateText:\n%s", text)
	}

	tmpl, err := template.New("aggregate").Parse(text)
	if err != nil {
		t.Fatalf("the drafted Aggregate does not parse as a template: %v\n%s", err, text)
	}
	var evaluated strings.Builder
	if err := tmpl.Execute(&evaluated, nil); err != nil {
		t.Fatalf("the drafted Aggregate does not run as a template: %v\n%s", err, text)
	}
	var doc struct {
		Spec struct {
			Components map[string]struct {
				Values map[string]any `yaml:"values"`
			} `yaml:"components"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(evaluated.String()), &doc); err != nil {
		t.Fatalf("the evaluated Aggregate does not read as YAML: %v\n%s", err, evaluated.String())
	}
	server, site := doc.Spec.Components["server"].Values, doc.Spec.Components["site"].Values
	if server["version"] != held || site["name"] != held {
		t.Errorf("the values read back as %q and %q, want %q", server["version"], site["name"], held)
	}
	if tags, _ := server["tags"].(map[string]any); tags["note"] != held {
		t.Errorf("the object's member reads back as %q, want %q", tags["note"], held)
	}
}

// TestAggregateIsDeterministic drafts one source twice, and once with its
// nodes held in another order, into one byte sequence.
func TestAggregateIsDeterministic(t *testing.T) {
	graph := func(reverse bool) *Graph {
		g := openingGraph()
		a := app(envOf(envEntry("DB_HOST", fqdn), envEntry("SITE", Ref{Node: "staticSite"})))
		a.DependsOn = []string{"subnet", "staticSite"}
		g.Nodes = append(g.Nodes, a, &Node{ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server",
			Name: text("orders")})
		if reverse {
			slices.Reverse(g.Nodes)
		}
		return g
	}
	var texts []string
	var diagnostics []string
	for _, reverse := range []bool{false, false, true} {
		_, text, diags := drafted(t, edgeView(), edgeFormat(nil), graph(reverse))
		raw, _ := json.Marshal(diags)
		texts, diagnostics = append(texts, text), append(diagnostics, string(raw))
	}
	for i := 1; i < len(texts); i++ {
		if texts[i] != texts[0] || diagnostics[i] != diagnostics[0] {
			t.Errorf("run %d differs from the first:\n%s\n%s\nwant\n%s\n%s", i, texts[i], diagnostics[i], texts[0],
				diagnostics[0])
		}
	}
}

// TestAggregateNeverPanicsOnMalformedInput writes an Aggregate from inputs no
// reader writes: no graph, nodes with nothing in them, references naming
// nothing or holding empty segments, environment lists of the wrong shape.
func TestAggregateNeverPanicsOnMalformedInput(t *testing.T) {
	cases := map[string]*Graph{
		"no graph":    nil,
		"no node":     {},
		"a nil node":  {Nodes: []*Node{nil, pgServer()}},
		"empty nodes": {Nodes: []*Node{{}, {ID: "x"}, {ID: "y", Type: siteType, Body: []Prop{{}, {Name: "a"}}}}},
		"odd references": {Nodes: []*Node{pgServer(), staticSite(
			leaf(Ref{Node: "server", Attr: []string{"", "x"}}, "properties", "a"),
			leaf(Ref{}, "properties", "b"),
			leaf(Ref{Node: "site", Attr: []string{"properties", "b"}}, "properties", "c"),
			leaf(Credential{}, "properties", "d"),
			leaf(Compose{}, "properties", "e"))}},
		"odd environment": {Nodes: []*Node{pgServer(), app(
			Prop{Name: "properties", Props: []Prop{{Name: "template", Props: []Prop{{Name: "containers",
				Items: []Prop{{Props: []Prop{{Name: "env", Items: []Prop{
					{Value: fqdn},
					{Props: []Prop{{Name: "value", Value: fqdn}}},
					{Props: []Prop{{Name: "name", Value: fqdn}, {Name: "value", Value: fqdn}}},
					{Props: []Prop{{Name: "name", Value: literal("7")}, {Name: "value", Value: fqdn}}},
				}}}}}}}}}})}},
		"odd outputs": {Nodes: []*Node{pgServer()}, Outputs: []Output{{}, {Name: "x", Value: Ref{Node: "nowhere"}},
			{Name: "y", Value: Credential{Node: "server"}}}, Params: []Parameter{{}}},
	}
	for name, g := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked: %v", r)
				}
			}()
			in, f := edgeView(), edgeFormat(nil)
			out, _ := Classify(in, f, g)
			entity, _ := Aggregate(in, f, g, out)
			if !strings.Contains(entity.Body, "kind: Aggregate\n") {
				t.Errorf("no Aggregate written:\n%s", entity.Body)
			}
			Aggregate(in, f, g, []Classified{{}, {Class: ClassMapped}, {Class: ClassDrafted, Node: &Node{}}})
		})
	}
}

// TestAggregateWithNoInstanceWritesNoComponent writes an empty components map
// where the draft holds no instance.
func TestAggregateWithNoInstanceWritesNoComponent(t *testing.T) {
	subnet := &Node{ID: "subnet", Type: subnetType, Form: FormRaw}
	in, f := edgeView(), edgeFormat(nil)
	g := &Graph{Nodes: []*Node{subnet}}
	out, _ := Classify(in, f, g)
	entity, _ := Aggregate(in, f, g, out)
	if !strings.HasSuffix(entity.Body, "spec:\n  components: {}\n") {
		t.Errorf("the Aggregate is\n%s\nwant an empty components map", entity.Body)
	}
}
