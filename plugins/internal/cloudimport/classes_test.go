// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The types of the fixture's resources.
const (
	accountType    = "Microsoft.CognitiveServices/accounts"
	deploymentType = accountType + "/deployments"
	databaseType   = serverType + "/databases"
	configType     = serverType + "/configurations"
	adminType      = serverType + "/administrators"
	vnetType       = "Microsoft.Network/virtualNetworks"
	subnetType     = vnetType + "/subnets"
	workspaceType  = "Microsoft.OperationalInsights/workspaces"
	identityType   = "Microsoft.ManagedIdentity/userAssignedIdentities"
	roleType       = "Microsoft.Authorization/roleAssignments"
	roleDefType    = "Microsoft.Authorization/roleDefinitions"
	lockType       = "Microsoft.Authorization/locks"
	diagnosticType = "Microsoft.Insights/diagnosticSettings"
	scriptType     = "Microsoft.Resources/deploymentScripts"
	namespaceType  = "Microsoft.ServiceBus/namespaces"
	cacheType      = "Microsoft.Cache/redis"
	vaultType      = "Microsoft.KeyVault/vaults"
	openai         = "core/ai-model/azure-openai"
)

// The Says of the fixture's type rules.
const (
	roleSays   = "identity and access are the operator's"
	lockSays   = "no catalog Feature locks a resource"
	diagSays   = "no catalog Feature carries a diagnostic setting"
	scriptSays = "the script runs when the template deploys"
)

// fixtureTable holds the PostgreSQL server's row, folding one database and
// every configuration, and the AI account's row, each of whose model children
// is an instance of its own folding the account.
func fixtureTable() Table {
	server := *postgresRow()
	server.Folds = []Fold{
		{Type: databaseType, Key: []string{"values", "database"}, From: []string{"name"}, One: true},
		{Type: configType, Key: []string{"infra", "overrides", "configurations"}, From: []string{"properties"}},
	}
	account := Row{Key: accountType, Component: openai, Category: "ai-model", Recipe: "azure-openai", Form: FormRaw,
		Traits: map[string]string{"component.aggrega.ai/type": "llm"},
		Folds: []Fold{{Type: deploymentType, Key: []string{"values", "model"},
			From: []string{"properties", "model", "name"}, Instances: true}}}
	return Table{Generated: []Row{server, account}}
}

func fixtureTypes() []TypeRule {
	return []TypeRule{
		{Type: vnetType, Class: ClassSubstrate},
		{Type: subnetType, Class: ClassSubstrate},
		{Type: workspaceType, Class: ClassSubstrate},
		{Type: identityType, Class: ClassSubstrate},
		{Type: roleType, Class: ClassSubstrate, Says: roleSays},
		{Type: lockType, Class: ClassNotCarried, Says: lockSays},
		{Type: diagnosticType, Class: ClassNotCarried, Says: diagSays},
		{Type: scriptType, Class: ClassDrafted, Says: scriptSays},
	}
}

// fixtureDraft drafts a resource from its source: name is its identity and
// location its placement, and its writer records each resource it is handed
// in written, where written is not nil.
func fixtureDraft(written *[]schemadraft.Resource) schemadraft.Format {
	return schemadraft.Format{
		Name: "arm-schema", Capability: "arm", Syntax: "json",
		Rules: schemadraft.Rules{
			Placement: func(_ *schemadraft.Type, p *schemadraft.Property) bool {
				return slices.Equal(p.Path, []string{"location"})
			},
			Identity: func(_ *schemadraft.Type, p *schemadraft.Property) bool {
				return slices.Equal(p.Path, []string{"name"})
			},
		},
		Write: func(r schemadraft.Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
			if written != nil {
				*written = append(*written, r)
			}
			var lines []string
			for _, in := range r.Inputs {
				lines = append(lines, strings.Join(in.Property.Path, ".")+": {{ toJson "+in.Source.Template()+" }}")
			}
			return importer.RecipeDraft{
				Artifacts: []importer.Entry{{Key: "resource.txt", Value: strings.Join(lines, "\n") + "\n"}},
			}, nil
		},
	}
}

func fixtureFormat() Format {
	return Format{Name: "arm", Target: "arm", Syntax: "jsonc", Table: fixtureTable(), Types: fixtureTypes(),
		Draft: fixtureDraft(nil)}
}

// fixtureView is the catalog view holding the rows' Components, and beside
// them each Component given.
func fixtureView(settings string, more ...aggrega.CatalogComponent) aggrega.ImportInput {
	return request(settings, append([]aggrega.CatalogComponent{
		component(postgres, datastoreLabels("postgres")),
		component(openai, map[string]string{
			"component.aggrega.ai/category": "ai-model",
			"component.aggrega.ai/type":     "llm",
		}),
	}, more...)...)
}

// text is a string literal.
func text(s string) Literal { return Literal{JSON: json.RawMessage(fmt.Sprintf("%q", s))} }

// leaf is a property path holding v.
func leaf(v Value, path ...string) Prop {
	p := Prop{Name: path[len(path)-1], Value: v}
	for i := len(path) - 2; i >= 0; i-- {
		p = Prop{Name: path[i], Props: []Prop{p}}
	}
	return p
}

// pgServer is a PostgreSQL server the row maps.
func pgServer() *Node {
	return &Node{ID: "server", Type: serverType, Form: FormRaw, Name: text("pg-main"), Source: "main.json#/server",
		Body: []Prop{leaf(text("westeurope"), "location"), leaf(text("16"), "properties", "version")}}
}

// classOf returns the classified node of id in out.
func classOf(t *testing.T, out []Classified, id string) Classified {
	t.Helper()
	var found []Classified
	for _, c := range out {
		if c.Node != nil && c.Node.ID == id {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s is classified %d times in %v, want once", id, len(found), classes(out))
	}
	return found[0]
}

// classes names each classified node with its class.
func classes(out []Classified) []string {
	var names []string
	for _, c := range out {
		id := "<no node>"
		if c.Node != nil {
			id = c.Node.ID
		}
		names = append(names, id+" "+c.Class.String())
	}
	return names
}

// withCode returns the diagnostics of code in diags.
func withCode(diags []aggrega.Diagnostic, code Code) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, d := range diags {
		if d.Code == string(code) {
			out = append(out, d)
		}
	}
	return out
}

// noHalt fails where diags hold a halt.
func noHalt(t *testing.T, diags []aggrega.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == aggrega.SeverityHalt {
			t.Fatalf("halted: %+v", diags)
		}
	}
}

// mentions fails where text names any of names not.
func mentions(t *testing.T, what, text string, names ...string) {
	t.Helper()
	for _, name := range names {
		if !strings.Contains(text, name) {
			t.Errorf("%s %q does not name %s", what, text, name)
		}
	}
}

func TestEveryNodeHasOneClassAndOneDecision(t *testing.T) {
	f := fixtureFormat()
	held := schemadraft.NameFor(fixtureView(""), f.Draft, cacheType)
	in := fixtureView("", component(held, map[string]string{"component.aggrega.ai/category": "component"}))
	g := &Graph{Format: "arm", Nodes: []*Node{
		{ID: "cache", Type: cacheType, Form: FormRaw, Name: text("cache"), Body: []Prop{leaf(text("westeurope"), "location")}},
		{ID: "lock", Type: lockType, Form: FormRaw, Parent: "server", Body: []Prop{leaf(text("CanNotDelete"), "properties", "level")}},
		{ID: "queue", Type: namespaceType, Form: FormRaw, Name: text("orders"), Body: []Prop{leaf(text("westeurope"), "location")}},
		pgServer(),
		{ID: "serverConfig", Type: configType, Form: FormRaw, Parent: "server", Body: []Prop{leaf(text("on"), "properties", "value")}},
		{ID: "vault", Type: vaultType, Form: FormRaw, Existing: true, Name: text("kv-shared")},
		{ID: "vnet", Type: vnetType, Form: FormRaw, Name: text("vnet-core")},
	}}
	out, diags := Classify(in, f, g)
	noHalt(t, diags)
	if len(out) != len(g.Nodes) {
		t.Fatalf("classified %v, want each of the graph's nodes once", classes(out))
	}

	for _, want := range []struct {
		id    string
		class Class
		rule  string
	}{
		{"serverConfig", ClassFolded, "by the fold of " + configType + " in the row of " + serverType},
		{"vnet", ClassSubstrate, "the format's type rule for " + vnetType},
		{"vault", ClassExisting, "as a resource it does not create"},
		{"server", ClassMapped, "the row of " + serverType},
		{"queue", ClassDrafted, "no row of the table maps " + namespaceType},
		{"cache", ClassHeldBack, "the Component " + held},
		{"lock", ClassNotCarried, "the format's type rule for " + lockType},
	} {
		c := classOf(t, out, want.id)
		if c.Class != want.class {
			t.Errorf("%s is %s, want %s", want.id, c.Class, want.class)
		}
		if c.Decision.SourceNode.ID != want.id || c.Decision.SourceNode.Type != c.Node.Type {
			t.Errorf("%s: the decision is of %+v", want.id, c.Decision.SourceNode)
		}
		if !strings.HasPrefix(c.Decision.Rationale, want.class.String()+": ") {
			t.Errorf("%s: the rationale %q does not open with its class %s", want.id, c.Decision.Rationale, want.class)
		}
		mentions(t, want.id+"'s rationale", c.Decision.Rationale, want.rule)
	}

	server := classOf(t, out, "server")
	if server.Row == nil || server.Row.Key != serverType || server.Entity.Mode != "reference" ||
		server.Entity.Name != postgres || server.Name != postgres {
		t.Errorf("server: row %v, entity %+v, name %s; want the row of %s referencing %s", server.Row,
			server.Entity, server.Name, serverType, postgres)
	}
	if c := classOf(t, out, "serverConfig"); c.Into != "server" || c.Row == nil || c.Row.Key != serverType {
		t.Errorf("serverConfig is folded into %q by %v, want into server by its row", c.Into, c.Row)
	}
	queue := classOf(t, out, "queue")
	if queue.Name != schemadraft.NameFor(in, f.Draft, namespaceType) || len(queue.Drafts) != 2 ||
		queue.Entity.Kind != "Component" || queue.Entity.Mode != "draft" || queue.Entity.Name != queue.Name {
		t.Errorf("queue: name %s, drafts %d, entity %+v; want the Component and Recipe drafted for it", queue.Name,
			len(queue.Drafts), queue.Entity)
	}
	if cache := classOf(t, out, "cache"); cache.Name != held || len(cache.Drafts) != 0 || cache.Entity.Name != "" {
		t.Errorf("cache: name %s, drafts %v, entity %+v; want %s taken, nothing drafted or referenced", cache.Name,
			cache.Drafts, cache.Entity, held)
	}
	for _, id := range []string{"serverConfig", "vnet", "vault", "lock"} {
		if c := classOf(t, out, id); len(c.Drafts) != 0 || c.Entity.Name != "" {
			t.Errorf("%s drafts %v and emits %+v, want nothing", id, c.Drafts, c.Entity)
		}
	}
}

// TestEveryNodeOfOneUnmatchedTypeTakesOneDraft drafts the two queues of one
// type no row maps as one Component and one Recipe, from every property
// either writes, so the Recipe reads each instance's properties; each queue
// keeps a decision of its own.
func TestEveryNodeOfOneUnmatchedTypeTakesOneDraft(t *testing.T) {
	var written []schemadraft.Resource
	f := fixtureFormat()
	f.Draft = fixtureDraft(&written)
	g := &Graph{Nodes: []*Node{
		{ID: "orders", Type: namespaceType, APIVersion: "2022-10-01", Form: FormRaw, Name: text("orders"), Body: []Prop{
			leaf(text("westeurope"), "location"), leaf(text("Standard"), "sku", "name"),
		}},
		{ID: "billing", Type: "microsoft.servicebus/Namespaces", APIVersion: "2021-11-01", Form: FormRaw,
			Name: text("billing"), Body: []Prop{
				leaf(text("westeurope"), "location"), leaf(literal("2"), "SKU", "capacity"),
				leaf(literal("true"), "properties", "zoneRedundant"),
			}},
	}}
	out, diags := Classify(fixtureView(""), f, g)
	noHalt(t, diags)
	if len(written) != 1 {
		t.Fatalf("the writer drafted %d Recipes, want one for both queues", len(written))
	}
	pillars := map[schemadraft.Pillar]string{schemadraft.PillarValues: "values", schemadraft.PillarInfra: "infra",
		schemadraft.PillarOverrides: "overrides", schemadraft.PillarSecret: "secret"}
	var paths []string
	for _, in := range written[0].Inputs {
		paths = append(paths, strings.Join(in.Property.Path, ".")+" "+pillars[in.Source.Pillar])
	}
	// The first queue by ID spells the member SKU, and the other queue's
	// member of that name in another case is written under that spelling.
	want := []string{"name values", "location infra", "SKU.capacity overrides", "properties.zoneRedundant overrides",
		"SKU.name overrides"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("the drafted Recipe reads %q, want %q: every property either queue writes, once", paths, want)
	}
	if written[0].Type.APIVersion != "2021-11-01" {
		t.Errorf("drafted at apiVersion %s, want the first queue's by ID", written[0].Type.APIVersion)
	}

	billing, orders := classOf(t, out, "billing"), classOf(t, out, "orders")
	if billing.Name != orders.Name || billing.Class != ClassDrafted || orders.Class != ClassDrafted {
		t.Fatalf("billing %s %s, orders %s %s; want both drafted under one name", billing.Class, billing.Name,
			orders.Class, orders.Name)
	}
	if len(billing.Drafts) != 2 || len(orders.Drafts) != 0 {
		t.Errorf("billing carries %d drafts, orders %d; want the Component and Recipe once, on the first by ID",
			len(billing.Drafts), len(orders.Drafts))
	}
	for _, c := range []Classified{billing, orders} {
		if c.Decision.SourceNode.ID != c.Node.ID || c.Entity.SourceRef != c.Node.ID ||
			c.Entity.DecisionRef != "decision:"+c.Node.ID {
			t.Errorf("%s: decision of %s, entity of %s and %s", c.Node.ID, c.Decision.SourceNode.ID,
				c.Entity.SourceRef, c.Entity.DecisionRef)
		}
		mentions(t, c.Node.ID+"'s rationale", c.Decision.Rationale, "billing and orders", "2022-10-01")
	}
	if got := withCode(diags, CodeResourceDraftedFromSource); len(got) != 1 {
		t.Errorf("drafted-from-source records %+v, want one for the one draft", got)
	}
}

func TestTypeRuleOfClassDraftedAddsItsNote(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "migrate", Type: scriptType, Form: FormRaw, Name: text("migrate"),
		Body: []Prop{leaf(text("AzureCLI"), "kind"), leaf(text("westeurope"), "location")}}}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	c := classOf(t, out, "migrate")
	if c.Class != ClassDrafted || len(c.Drafts) != 2 {
		t.Fatalf("migrate is %s with %d drafts, want drafted", c.Class, len(c.Drafts))
	}
	mentions(t, "the rationale", c.Decision.Rationale, scriptSays)
}

func TestExistingNodeAndUndeclaredReferenceAreExisting(t *testing.T) {
	db := repeated([]string{"0", "1"})
	db.Type = namespaceType
	gone := repeated([]string{})
	gone.ID, gone.Type = "gone", namespaceType
	app := &Node{ID: "app", Type: namespaceType, Form: FormRaw, Source: "main.json#/app", Body: []Prop{
		leaf(Ref{Node: "vnetShared", Attr: []string{"id"}}, "properties", "subnetId"),
		leaf(Ref{Node: "db"}, "properties", "primary"),
		leaf(Credential{Node: "gone", What: "listKeys"}, "properties", "key"),
		leaf(Ref{Node: "vault"}, "properties", "vaultId"),
	}}
	vault := &Node{ID: "vault", Type: vaultType, Form: FormRaw, Existing: true}
	g := &Graph{Nodes: []*Node{app, db, gone, vault}}
	Expand(g)
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)

	if c := classOf(t, out, "vault"); c.Class != ClassExisting {
		t.Errorf("vault is %s, want existing", c.Class)
	}
	shared := classOf(t, out, "vnetShared")
	if shared.Class != ClassExisting || !shared.Node.Existing {
		t.Errorf("vnetShared is %s, want an existing resource the source references", shared.Class)
	}
	mentions(t, "vnetShared's rationale", shared.Decision.Rationale, "app", "does not declare")
	for _, c := range out {
		if c.Node.ID == "db" || c.Node.ID == "gone" {
			t.Errorf("%s is classified %s: a repeated resource is read as its instances, never as existing",
				c.Node.ID, c.Class)
		}
	}
	for _, id := range []string{"db-0", "db-1"} {
		if c := classOf(t, out, id); c.Class != ClassDrafted {
			t.Errorf("%s is %s, want drafted as an instance of the repeated db", id, c.Class)
		}
	}
	if len(out) != 5 {
		t.Errorf("classified %v, want app, vault, vnetShared and db's two instances", classes(out))
	}
}

func TestSubstrateNotDrafted(t *testing.T) {
	var written []schemadraft.Resource
	f := fixtureFormat()
	f.Draft = fixtureDraft(&written)
	g := &Graph{Nodes: []*Node{
		{ID: "vnet", Type: vnetType, Form: FormRaw, Name: text("vnet-core")},
		{ID: "subnet", Type: subnetType, Form: FormRaw, Parent: "vnet", Name: text("snet-db")},
		{ID: "peered", Type: vnetType, Form: FormRaw, Existing: true, Name: text("vnet-hub")},
		pgServer(),
	}}
	out, diags := Classify(fixtureView(""), f, g)
	noHalt(t, diags)
	for _, id := range []string{"vnet", "subnet", "peered"} {
		c := classOf(t, out, id)
		if c.Class != ClassSubstrate || len(c.Drafts) != 0 || c.Entity.Name != "" {
			t.Errorf("%s is %s with drafts %v and entity %+v, want substrate and nothing drafted", id, c.Class,
				c.Drafts, c.Entity)
		}
	}
	if len(written) != 0 {
		t.Errorf("the writer drafted %d Recipes, want none", len(written))
	}
	left := withCode(diags, CodeSubstrateLeftToOperator)
	if len(left) != 1 || left[0].Severity != aggrega.SeverityWarn {
		t.Fatalf("substrate warnings %+v, want one warning", left)
	}
	mentions(t, "the warning", left[0].Message, "vnet ("+vnetType+")", "subnet ("+subnetType+")")
	if strings.Contains(left[0].Message, "peered") {
		t.Errorf("the warning %q names peered, which the source reads and does not create", left[0].Message)
	}
	if subnet := classOf(t, out, "subnet"); slices.Contains(subnet.Node.DependsOn, "vnet") {
		t.Errorf("the substrate subnet depends on %q: only an instance takes an edge to its parent",
			subnet.Node.DependsOn)
	}
}

func TestDiscriminatorUnresolvedIsReported(t *testing.T) {
	f := fixtureFormat()
	f.Table = sitesTable()
	g := &Graph{Params: []Parameter{{Name: "siteKind", Type: "string"}},
		Nodes: []*Node{site(Param{Name: "siteKind"})}}
	out, diags := Classify(fixtureView(""), f, g)
	noHalt(t, diags)
	if c := classOf(t, out, "web"); c.Class != ClassDrafted || c.Row != nil {
		t.Errorf("web is %s by %v, want drafted with no row", c.Class, c.Row)
	}
	got := withCode(diags, CodeDiscriminatorUnresolved)
	if len(got) != 1 || got[0].Severity != aggrega.SeverityWarn || got[0].SourceRef != "" && got[0].SourceRef != "web" {
		t.Fatalf("discriminator warnings %+v, want one warning on web", got)
	}
	mentions(t, "the warning", got[0].Message, "web", "core/compute/function", "core/compute/server",
		"mapping_overrides")
}

func TestClassifyHaltsOnATableThatContradictsItself(t *testing.T) {
	f := fixtureFormat()
	hand := *postgresRow()
	hand.Component, hand.Origin = "core/datastore/postgres-ha", "hand"
	f.Table.Hand = []Row{hand}
	out, diags := Classify(fixtureView(""), f, &Graph{Nodes: []*Node{pgServer()}})
	if out != nil || len(diags) != 1 || diags[0].Code != string(CodeSourceRefused) ||
		diags[0].Severity != aggrega.SeverityHalt {
		t.Fatalf("Classify = %v, %+v; want one source-refused halt and nothing classified", classes(out), diags)
	}
	mentions(t, "the halt", diags[0].Message, serverType, "Component", "Supersedes")
}

func TestClassifyHaltsOnTwoNodesOfOneID(t *testing.T) {
	db := repeated([]string{"0"})
	g := &Graph{Nodes: []*Node{db, {ID: "db-0", Type: namespaceType}}}
	Expand(g)
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	if out != nil || len(diags) != 1 || diags[0].Code != string(CodeSourceRefused) {
		t.Fatalf("Classify = %v, %+v; want one source-refused halt", classes(out), diags)
	}
	mentions(t, "the halt", diags[0].Message, "db-0", "rename")
}

// TestEveryNodeOfAMalformedGraphIsClassedWithoutPanic classifies graphs a
// reader should never write without panicking.
func TestEveryNodeOfAMalformedGraphIsClassedWithoutPanic(t *testing.T) {
	cases := []struct {
		name string
		g    *Graph
	}{
		{"no graph", nil},
		{"no node", &Graph{Nodes: []*Node{nil}}},
		{"no ID and no type", &Graph{Nodes: []*Node{{}}}},
		{"its own parent", &Graph{Nodes: []*Node{{ID: "a", Type: configType, Parent: "a"}}}},
		{"parents in a cycle", &Graph{Nodes: []*Node{
			{ID: "a", Type: configType, Parent: "b"}, {ID: "b", Type: serverType, Parent: "a"},
		}}},
		{"a parent the source does not declare", &Graph{Nodes: []*Node{{ID: "a", Type: databaseType, Parent: "x"}}}},
		{"empty properties", &Graph{Nodes: []*Node{{ID: "a", Type: namespaceType, Body: []Prop{
			{}, {Name: "p", Items: []Prop{{}, {Value: Compose{Parts: []Value{nil, Ref{}, Compose{}}}}}},
		}}}}},
		{"a reference to no node", &Graph{Nodes: []*Node{{ID: "a", Type: namespaceType,
			Body: []Prop{leaf(Ref{}, "p")}, DependsOn: []string{""}}}}},
		{"a repetition not expanded", &Graph{Nodes: []*Node{{ID: "a", Type: namespaceType, Repeat: &Repetition{}}}}},
		{"a fold of no type", &Graph{Nodes: []*Node{pgServer(), {ID: "c", Parent: "server"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := fixtureFormat()
			f.Table.Generated[0].Folds = append(f.Table.Generated[0].Folds, Fold{})
			out, _ := Classify(fixtureView(""), f, tc.g)
			seen := map[string]bool{}
			for _, c := range out {
				if c.Node == nil || seen[c.Node.ID] {
					t.Fatalf("classified %v, want each node once", classes(out))
				}
				seen[c.Node.ID] = true
			}
		})
	}
}

// TestEveryNodeClassesAlikeTwice classifies one graph twice to equal results.
func TestEveryNodeClassesAlikeTwice(t *testing.T) {
	graph := func() *Graph {
		return &Graph{Nodes: []*Node{
			pgServer(),
			{ID: "dbB", Type: databaseType, Parent: "server", Name: text("b")},
			{ID: "dbA", Type: databaseType, Parent: "server", Name: text("a")},
			{ID: "queue", Type: namespaceType, Name: text("q"), Body: []Prop{leaf(Ref{Node: "server", Attr: []string{"fqdn"}}, "p")}},
			{ID: "vnet", Type: vnetType},
		}}
	}
	a, da := Classify(fixtureView(""), fixtureFormat(), graph())
	b, db := Classify(fixtureView(""), fixtureFormat(), graph())
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(da, db) {
		t.Errorf("two runs differ:\n%v\n%v", classes(a), classes(b))
	}
}
