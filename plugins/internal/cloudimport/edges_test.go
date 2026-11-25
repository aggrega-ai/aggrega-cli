// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The types and the Component the edge fixtures add.
const (
	appType       = "Microsoft.App/containerApps"
	siteType      = "Microsoft.Web/staticSites"
	registryType  = "Microsoft.ContainerRegistry/registries"
	policyType    = accountType + "/raiPolicies"
	computeServer = "core/compute/server"
)

// fqdn is the reference to the PostgreSQL server's host name, which its row
// provides under host.
var fqdn = Ref{Node: "server", Attr: []string{"properties", "fullyQualifiedDomainName"}}

// edgeTable is the fixture's table with the server's row providing its host
// name, and a container app's row listing its environment and taking a
// credential in a slot.
func edgeTable() Table {
	t := fixtureTable()
	pg := t.Generated[0]
	pg.Provides = []Placement{{From: []string{"properties", "fullyQualifiedDomainName"}, Key: "host"}}
	t.Generated[0] = pg
	t.Generated = append(t.Generated, Row{Key: appType, Component: computeServer, Category: "compute",
		Recipe: "server-container-apps", Form: FormRaw, Traits: map[string]string{"component.aggrega.ai/type": "server"},
		Env: &EnvList{Path: []string{"properties", "template", "containers", "0", "env"}, Name: "name", Value: "value"},
		Secrets: []Placement{{From: []string{"properties", "configuration", "secrets", "0", "value"},
			Key: "storage_key"}}})
	return t
}

// edgeFormat is the fixture's format over edgeTable, its writer recording each
// resource it drafts in written, where written is not nil.
func edgeFormat(written *[]schemadraft.Resource) Format {
	f := fixtureFormat()
	f.Table = edgeTable()
	f.Draft = fixtureDraft(written)
	return f
}

// edgeView is the catalog view holding the rows' Components, each Component
// given beside them.
func edgeView(more ...aggrega.CatalogComponent) aggrega.ImportInput {
	return fixtureView("", append([]aggrega.CatalogComponent{component(computeServer, map[string]string{
		"component.aggrega.ai/category": "compute",
		"component.aggrega.ai/type":     "server",
	})}, more...)...)
}

// merged returns props with the members of one name merged into one, in the
// order first met, so a body is written member by member.
func merged(props ...Prop) []Prop {
	var out []Prop
	for _, p := range props {
		i := slices.IndexFunc(out, func(o Prop) bool { return o.Name == p.Name && p.Name != "" })
		if i < 0 || p.Value != nil || out[i].Value != nil {
			out = append(out, p)
			continue
		}
		out[i].Props = merged(append(slices.Clone(out[i].Props), p.Props...)...)
	}
	return out
}

// envOf is a container app's environment list holding entries.
func envOf(entries ...Prop) Prop {
	env := Prop{Name: "env", Items: entries}
	return Prop{Name: "properties", Props: []Prop{{Name: "template", Props: []Prop{
		{Name: "containers", Items: []Prop{{Props: []Prop{env}}}},
	}}}}
}

// envEntry is one variable of an environment list, named name and set to v.
func envEntry(name string, v Value) Prop {
	return Prop{Props: []Prop{{Name: "name", Value: text(name)}, {Name: "value", Value: v}}}
}

// envValue is the path of the value of the environment list's item i.
func envValue(i string) string { return "properties.template.containers.0.env." + i + ".value" }

// app is a container app the table maps, writing body.
func app(body ...Prop) *Node {
	return &Node{ID: "app", Type: appType, Form: FormRaw, Name: text("web"), Source: "main.json#/app",
		Body: merged(body...)}
}

// staticSite is a static site no row maps, drafted from the source, writing body.
func staticSite(body ...Prop) *Node {
	return &Node{ID: "site", Type: siteType, APIVersion: "2023-01-01", Form: FormRaw, Name: text("site1"),
		Source: "main.json#/site", Body: merged(body...)}
}

// drafted classifies g in the view in with f and writes its Aggregate,
// failing on a halt, and returns the classified nodes, the Aggregate's text
// and the diagnostics of both.
func drafted(t *testing.T, in aggrega.ImportInput, f Format, g *Graph) ([]Classified, string, []aggrega.Diagnostic) {
	t.Helper()
	out, diags := Classify(in, f, g)
	noHalt(t, diags)
	entity, more := Aggregate(in, f, g, out)
	diags = append(diags, more...)
	noHalt(t, diags)
	if entity.Kind != "Aggregate" || entity.Mode != "draft" {
		t.Fatalf("Aggregate is a %s in %s mode, want a drafted Aggregate", entity.Kind, entity.Mode)
	}
	return out, entity.Body, diags
}

// instanceBlock returns the lines of the Aggregate text writing the instance
// key, the comment lines above it included.
func instanceBlock(t *testing.T, text, key string) string {
	t.Helper()
	_, components, ok := strings.Cut(text, "  components:\n")
	if !ok {
		t.Fatalf("the Aggregate writes no components:\n%s", text)
	}
	var blocks []string
	var block strings.Builder
	keyed := false
	for _, line := range strings.SplitAfter(components, "\n") {
		if line == "" {
			continue
		}
		top := strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ")
		if top && keyed {
			blocks = append(blocks, block.String())
			block.Reset()
			keyed = false
		}
		block.WriteString(line)
		if top && !strings.HasPrefix(line, "    #") {
			keyed = true
		}
	}
	blocks = append(blocks, block.String())
	for _, b := range blocks {
		if strings.Contains("\n"+b, "\n    "+key+":\n") {
			return b
		}
	}
	t.Fatalf("the Aggregate writes no instance %s:\n%s", key, text)
	return ""
}

// wantBlock fails where the Aggregate does not write the instance key as want.
func wantBlock(t *testing.T, text, key, want string) {
	t.Helper()
	if got := instanceBlock(t, text, key); got != want {
		t.Errorf("the instance %s is written\n%s\nwant\n%s\nin\n%s", key, got, want, text)
	}
}

// onlyCode fails unless diags hold exactly n diagnostics of code, and returns
// them.
func onlyCode(t *testing.T, diags []aggrega.Diagnostic, code Code, n int) []aggrega.Diagnostic {
	t.Helper()
	got := withCode(diags, code)
	if len(got) != n {
		t.Fatalf("%s diagnostics %+v, want %d", code, got, n)
	}
	return got
}

// TestEdgeEnvEntryInjectsTheProvidesKey: an environment entry of a compute
// instance set to an attribute its producer's row provides is delivered on the
// env mechanism, under the variable's name, from that provides key.
func TestEdgeEnvEntryInjectsTheProvidesKey(t *testing.T) {
	g := &Graph{Nodes: []*Node{pgServer(), app(envOf(envEntry("DB_HOST", fqdn)))}}
	_, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", `    app:
      type: core/compute/server
      uses:
        server:
          server:
            target: server
            inject:
              env:
                - in: "DB_HOST"
                  from: host
`)
	wantBlock(t, text, "server", "    server:\n      type: core/datastore/postgres\n")
	onlyCode(t, diags, CodeReferenceNotWired, 0)
}

// TestEdgeComposedEnvValueIsNotWired: an environment entry composing the
// producer's attribute with other text draws the connection, names the
// variable in the comment above it, and warns reference-not-wired.
func TestEdgeComposedEnvValueIsNotWired(t *testing.T) {
	url := Compose{Parts: []Value{text("postgres://"), fqdn, text(":5432/app")}}
	g := &Graph{Nodes: []*Node{pgServer(), app(envOf(envEntry("DB_URL", url)))}}
	_, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", `    app:
      type: core/compute/server
      uses:
        # Not wired: app sets the variable DB_URL at `+envValue("0")+` from server's properties.fullyQualifiedDomainName composed with other text; an inject delivers a provides key whole.
        server:
          server:
            target: server
`)
	w := onlyCode(t, diags, CodeReferenceNotWired, 1)[0]
	if w.SourceRef != "main.json#/app" {
		t.Errorf("the warning points at %q, want the consumer's source", w.SourceRef)
	}
	mentions(t, "the warning", w.Message, "app", "DB_URL", envValue("0"), "server",
		"properties.fullyQualifiedDomainName")
}

// TestEdgeIntoADraftedPropertyInjectsIt: a property of a node drafted from the
// source set to an attribute its producer provides is delivered on the
// property mechanism under the snake_case of its path, and the drafted Recipe
// reads it there; the drafted Component declares no socket for it.
func TestEdgeIntoADraftedPropertyInjectsIt(t *testing.T) {
	var written []schemadraft.Resource
	g := &Graph{Nodes: []*Node{pgServer(), staticSite(leaf(text("westeurope"), "location"),
		leaf(fqdn, "properties", "databaseHost"))}}
	out, text, diags := drafted(t, edgeView(), edgeFormat(&written), g)
	wantBlock(t, text, "site", `    site:
      type: `+schemadraft.NameFor(edgeView(), edgeFormat(nil).Draft, siteType)+`
      values:
        name: "site1"
      uses:
        server:
          server:
            target: server
            inject:
              property:
                - in: "properties_database_host"
                  from: host
`)
	onlyCode(t, diags, CodeReferenceNotWired, 0)
	if len(written) != 1 {
		t.Fatalf("the writer drafted %d Recipes, want the site's", len(written))
	}
	want := schemadraft.InputSource{Pillar: schemadraft.PillarInject, Key: "properties_database_host",
		Mechanism: schemadraft.MechanismProperty}
	var injected []schemadraft.Input
	for _, in := range written[0].Inputs {
		if in.Source.Pillar == schemadraft.PillarInject {
			injected = append(injected, in)
		}
	}
	if len(injected) != 1 || injected[0].Source != want || !injected[0].Required ||
		!slices.Equal(injected[0].Property.Path, []string{"properties", "databaseHost"}) {
		t.Fatalf("the drafted Recipe reads off the inject pipe %+v, want properties.databaseHost from %+v", injected, want)
	}
	s := classOf(t, out, "site")
	if !reflect.DeepEqual(s.Inputs, written[0].Inputs) {
		t.Errorf("the site's inputs %+v, want those of its drafted Recipe %+v", s.Inputs, written[0].Inputs)
	}
	if body := s.Drafts[0].Entity.Body; s.Drafts[0].Entity.Kind != "Component" || strings.Contains(body, "usesSchema") {
		t.Errorf("the drafted Component declares a socket:\n%s", body)
	}
}

// TestEdgeIntoADraftedPropertyFromADraftedProducer: an attribute another node
// reads of a node drafted from the source is a provides key of the producer's
// draft, the snake_case of the attribute's path, and its identifier is its id.
func TestEdgeIntoADraftedPropertyFromADraftedProducer(t *testing.T) {
	var written []schemadraft.Resource
	registry := &Node{ID: "registry", Type: registryType, Form: FormRaw, Name: text("acr1"), Source: "main.json#/registry",
		Body: []Prop{leaf(text("westeurope"), "location")}}
	g := &Graph{Nodes: []*Node{registry, staticSite(
		leaf(Ref{Node: "registry", Attr: []string{"properties", "loginServer"}}, "properties", "registryServer"),
		leaf(Ref{Node: "registry"}, "properties", "registryId"))}}
	out, text, diags := drafted(t, edgeView(), edgeFormat(&written), g)
	onlyCode(t, diags, CodeReferenceNotWired, 0)
	if got := instanceBlock(t, text, "site"); !strings.HasSuffix(got, `      uses:
        registry:
          registry:
            target: registry
            inject:
              property:
                - in: "properties_registry_server"
                  from: properties_login_server
                - in: "properties_registry_id"
                  from: id
`) {
		t.Errorf("the site is written\n%s\nwant it to read the registry's login server and identifier", got)
	}
	wantProvides := []schemadraft.Provide{
		{Key: "id", Attribute: []string{"id"}, Identifier: true},
		{Key: "name", Attribute: []string{"name"}},
		{Key: "properties_login_server", Attribute: []string{"properties", "loginServer"}},
	}
	if r := classOf(t, out, "registry"); !reflect.DeepEqual(r.Provides, wantProvides) {
		t.Errorf("the registry provides %+v, want %+v", r.Provides, wantProvides)
	}
	for _, r := range written {
		if r.Type.Name == registryType {
			var keys []string
			for _, p := range r.Provides {
				keys = append(keys, p.Key)
			}
			if !slices.Equal(keys, []string{"id", "name", "properties_login_server"}) {
				t.Errorf("the registry's drafted Recipe provides %q", keys)
			}
		}
	}
}

// TestEdgeIntoAnUntranslatedPropertyOrAnUnprovidedAttributeIsNotWired: a
// reference into a property of a mapped instance its row does not read from a
// neighbour, and one to an attribute its producer provides under no key, draw
// the connection for the dependency alone, each named in a comment and warned
// reference-not-wired naming consumer, path and attribute.
func TestEdgeIntoAnUntranslatedPropertyOrAnUnprovidedAttributeIsNotWired(t *testing.T) {
	port := Ref{Node: "server", Attr: []string{"properties", "port"}}
	g := &Graph{Nodes: []*Node{pgServer(), app(leaf(fqdn, "properties", "configuration", "ingress", "customDomain"),
		envOf(envEntry("DB_PORT", port)))}}
	_, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", `    app:
      type: core/compute/server
      uses:
        # Not wired: app reads server's properties.fullyQualifiedDomainName at properties.configuration.ingress.customDomain; the Recipe of core/compute/server reads no neighbour's value there.
        # Not wired: app sets the variable DB_PORT at `+envValue("0")+` from server's properties.port; server publishes no provides key for it.
        server:
          server:
            target: server
`)
	warnings := onlyCode(t, diags, CodeReferenceNotWired, 2)
	mentions(t, "the first warning", warnings[0].Message, "app", "properties.configuration.ingress.customDomain",
		"properties.fullyQualifiedDomainName")
	mentions(t, "the second warning", warnings[1].Message, "app", envValue("0"), "properties.port")
}

// TestEdgeDependsOnOnlyDrawsATarget: a dependency the source states draws a
// connection carrying its target alone.
func TestEdgeDependsOnOnlyDrawsATarget(t *testing.T) {
	a := app()
	a.DependsOn = []string{"server"}
	g := &Graph{Nodes: []*Node{pgServer(), a}}
	_, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", `    app:
      type: core/compute/server
      uses:
        server:
          server:
            target: server
`)
	onlyCode(t, diags, CodeReferenceNotWired, 0)
}

// TestEdgeToSubstrateOrExistingDrawsNone: a reference or a dependency to a
// substrate resource, to one the source reads and does not create, or to one
// it does not declare draws no connection. A dependency warns nothing, since
// the Release holds a pointer; an environment entry reading one warns
// reference-not-wired, since the Recipe takes its environment only through
// inject and no Release key holds the pointer.
func TestEdgeToSubstrateOrExistingDrawsNone(t *testing.T) {
	subnet := &Node{ID: "subnet", Type: subnetType, Form: FormRaw, Name: text("snet")}
	vault := &Node{ID: "vault", Type: vaultType, Form: FormRaw, Existing: true, Name: text("kv-shared")}
	a := app(envOf(envEntry("SUBNET_ID", Ref{Node: "subnet"}),
		envEntry("VAULT_URI", Ref{Node: "vault", Attr: []string{"properties", "vaultUri"}}),
		envEntry("LOGS", Ref{Node: "workspace", Attr: []string{"properties", "customerId"}})))
	a.DependsOn = []string{"subnet", "vault"}
	g := &Graph{Nodes: []*Node{subnet, vault, a}}
	_, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", "    app:\n      type: core/compute/server\n")
	warned := onlyCode(t, diags, CodeReferenceNotWired, 3)
	for i, variable := range []string{"SUBNET_ID", "VAULT_URI", "LOGS"} {
		if !strings.Contains(warned[i].Message, variable) || !strings.Contains(warned[i].Message, "only through inject") {
			t.Errorf("warning %d is %q, want it naming %s and saying the environment takes inject only", i,
				warned[i].Message, variable)
		}
	}
}

// TestEdgeCredentialFlowsToAReferencedSecret: a credential read from another
// resource draws no connection and carries no value: a credential-flow warning
// names it, and the consumer's slot is left for a core/referenced-secret the
// Release attaches.
func TestEdgeCredentialFlowsToAReferencedSecret(t *testing.T) {
	storage := &Node{ID: "storage", Type: registryType, Form: FormRaw, Name: text("st1")}
	g := &Graph{Nodes: []*Node{storage, app(leaf(Credential{Node: "storage", What: "listKeys"},
		"properties", "configuration", "secrets", "0", "value"))}}
	out, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", "    app:\n      type: core/compute/server\n")
	w := onlyCode(t, diags, CodeCredentialFlow, 1)[0]
	if w.Severity != aggrega.SeverityWarn || w.SourceRef != "main.json#/app" {
		t.Errorf("credential-flow %+v, want a warning on the consumer's source", w)
	}
	mentions(t, "the warning", w.Message, "app", "storage", "listKeys", "properties.configuration.secrets.0.value",
		"storage_key", "core/referenced-secret")
	want := []CredentialRead{{Path: []string{"properties", "configuration", "secrets", "0", "value"}, From: "storage",
		What: "listKeys", Slot: "storage_key"}}
	if a := classOf(t, out, "app"); !reflect.DeepEqual(a.Credentials, want) {
		t.Errorf("the app reads credentials %+v, want %+v", a.Credentials, want)
	}
}

// TestEdgeSocketAndAliasAreTheProducersKey: a connection is written under the
// socket and the alias of its producer's instance key, the key the kit's
// UniqueKey gives the producer, and a key differing from its symbol is named
// above its instance.
func TestEdgeSocketAndAliasAreTheProducersKey(t *testing.T) {
	upper, lower := pgServer(), pgServer()
	upper.ID, lower.ID = "DB", "db"
	a := app()
	a.DependsOn = []string{"db"}
	g := &Graph{Nodes: []*Node{upper, lower, a}}
	out, text, _ := drafted(t, edgeView(), edgeFormat(nil), g)
	if k := classOf(t, out, "db").Key; k != "db-2" {
		t.Fatalf("db is keyed %q, want db-2: DB, first by ID, takes db", k)
	}
	wantBlock(t, text, "app", `    app:
      type: core/compute/server
      uses:
        db-2:
          db-2:
            target: db-2
`)
	wantBlock(t, text, "db-2", "    # The resource db.\n    db-2:\n      type: core/datastore/postgres\n")
	wantBlock(t, text, "db", "    # The resource DB.\n    db:\n      type: core/datastore/postgres\n")
}

// TestEdgeToAFoldedNodeTargetsItsInstance: a dependency on a child folded
// into its parent's instance draws a connection to that instance.
func TestEdgeToAFoldedNodeTargetsItsInstance(t *testing.T) {
	a := app()
	a.DependsOn = []string{"ordersDb"}
	g := &Graph{Nodes: []*Node{pgServer(), a,
		{ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server", Name: text("orders")}}}
	_, text, _ := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", `    app:
      type: core/compute/server
      uses:
        server:
          server:
            target: server
`)
}

// TestEdgeToAnInstanceFoldedParentReachesEveryInstance: a parent written into
// each instance its children take is reached through each of them: a
// dependency on it connects to every one, a reference to it is wired to none
// and warned, and an instance it is written into depends on no other.
func TestEdgeToAnInstanceFoldedParentReachesEveryInstance(t *testing.T) {
	account := &Node{ID: "account", Type: accountType, Form: FormRaw, Name: text("ai"),
		Body: []Prop{leaf(text("OpenAI"), "kind")}}
	model := func(id string) *Node {
		return &Node{ID: id, Type: deploymentType, Form: FormRaw, Parent: "account", Name: text(id),
			DependsOn: []string{"account"}, Body: []Prop{leaf(text(id), "properties", "model", "name")}}
	}
	policy := &Node{ID: "policy", Type: policyType, Form: FormRaw, Parent: "account", Name: text("strict")}
	a := app(envOf(envEntry("AI_ENDPOINT", Ref{Node: "account", Attr: []string{"properties", "endpoint"}})))
	a.DependsOn = []string{"account"}
	g := &Graph{Nodes: []*Node{account, model("chat"), model("embed"), policy, a}}
	_, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	notWired := "        # Not wired: app sets the variable AI_ENDPOINT at " + envValue("0") + " from account's " +
		"properties.endpoint; account deploys as chat and embed, and an inject delivers the value of one.\n"
	wantBlock(t, text, "app", "    app:\n      type: core/compute/server\n      uses:\n"+
		notWired+"        chat:\n          chat:\n            target: chat\n"+
		notWired+"        embed:\n          embed:\n            target: embed\n")
	if block := instanceBlock(t, text, "policy"); !strings.HasSuffix(block, "      uses:\n"+
		"        chat:\n          chat:\n            target: chat\n"+
		"        embed:\n          embed:\n            target: embed\n") {
		t.Errorf("policy is written\n%s\nwant it connected to chat and to embed", block)
	}
	for _, key := range []string{"chat", "embed"} {
		if block := instanceBlock(t, text, key); strings.Contains(block, "uses:") {
			t.Errorf("%s, into which the account is written, is connected:\n%s", key, block)
		}
	}
	w := onlyCode(t, diags, CodeReferenceNotWired, 1)[0]
	mentions(t, "the warning", w.Message, "app", "account", "properties.endpoint", "chat and embed")
}

// TestEdgeToAHeldBackResourceIsAComment: a connection to a resource held back
// is written as a comment above the consumer's instance, naming it.
func TestEdgeToAHeldBackResourceIsAComment(t *testing.T) {
	f := edgeFormat(nil)
	held := schemadraft.NameFor(edgeView(), f.Draft, cacheType)
	cache := &Node{ID: "cache", Type: cacheType, Form: FormRaw, Name: text("cache-main")}
	a := app(envOf(envEntry("CACHE_HOST", Ref{Node: "cache", Attr: []string{"properties", "hostName"}})))
	a.DependsOn = []string{"cache"}
	_, text, diags := drafted(t, edgeView(component(held, cacheLabels)), f, &Graph{Nodes: []*Node{cache, a}})
	wantBlock(t, text, "app", "    # Not connected to cache, held back: the catalog holds the Component "+held+
		" its draft takes.\n    app:\n      type: core/compute/server\n")
	w := onlyCode(t, diags, CodeReferenceNotWired, 1)[0]
	mentions(t, "the warning", w.Message, "app", "cache", "properties.hostName", "held back")
}

// TestEdgeToARepeatedNodeReachesItsInstances: a dependency on a repeated
// resource connects to each instance it deploys, and a reference to it is
// wired to none of them and warned.
func TestEdgeToARepeatedNodeReachesItsInstances(t *testing.T) {
	pair := &Node{ID: "server", Type: serverType, Form: FormRaw, Repeat: &Repetition{Keys: []string{"0", "1"},
		Instance: func(string) *Node { return pgServer() }}}
	a := app(envOf(envEntry("DB_HOST", fqdn)))
	a.DependsOn = []string{"server"}
	g := &Graph{Nodes: []*Node{pair, a}}
	Expand(g)
	_, text, diags := drafted(t, edgeView(), edgeFormat(nil), g)
	wantBlock(t, text, "app", `    app:
      type: core/compute/server
      uses:
        # Not wired: app sets the variable DB_HOST at `+envValue("0")+` from server's properties.fullyQualifiedDomainName; server deploys as server-0 and server-1, and an inject delivers the value of one.
        server-0:
          server-0:
            target: server-0
        # Not wired: app sets the variable DB_HOST at `+envValue("0")+` from server's properties.fullyQualifiedDomainName; server deploys as server-0 and server-1, and an inject delivers the value of one.
        server-1:
          server-1:
            target: server-1
`)
	onlyCode(t, diags, CodeReferenceNotWired, 1)
}

// TestEdgeIntoAPropertyAnotherInstanceWritesItselfIsNotWired: two resources of
// one type draft one Recipe, so a property one of them reads from a neighbour
// and the other writes itself stays an override, and the reference is not
// wired.
func TestEdgeIntoAPropertyAnotherInstanceWritesItselfIsNotWired(t *testing.T) {
	var written []schemadraft.Resource
	other := staticSite(leaf(text("db.example.org"), "properties", "databaseHost"))
	other.ID, other.Name = "blog", text("site2")
	g := &Graph{Nodes: []*Node{pgServer(), staticSite(leaf(fqdn, "properties", "databaseHost")), other}}
	_, text, diags := drafted(t, edgeView(), edgeFormat(&written), g)
	if got := instanceBlock(t, text, "site"); !strings.HasSuffix(got, `      uses:
        # Not wired: site reads server's properties.fullyQualifiedDomainName at properties.databaseHost; blog, another instance of the same drafted Component, writes that property itself, so the Recipe reads it from infra.overrides.
        server:
          server:
            target: server
`) {
		t.Errorf("the site is written\n%s", got)
	}
	onlyCode(t, diags, CodeReferenceNotWired, 1)
	for _, in := range written[0].Inputs {
		if in.Source.Pillar == schemadraft.PillarInject {
			t.Errorf("the drafted Recipe reads %v off the inject pipe", in.Property.Path)
		}
	}
}

// TestEdgeIntoAPropertyTheFormatCannotInjectIsNotWired: a property the
// format's writer cannot write a neighbour's value at stays an override, and
// the reference is not wired, the format's reason named.
func TestEdgeIntoAPropertyTheFormatCannotInjectIsNotWired(t *testing.T) {
	var written []schemadraft.Resource
	f := edgeFormat(&written)
	f.Draft.Rules.NotInjected = func(_ *schemadraft.Type, p *schemadraft.Property) string {
		if p.Path[0] != "properties" {
			return "the format writes a neighbour's value under properties alone"
		}
		return ""
	}
	g := &Graph{Nodes: []*Node{pgServer(), staticSite(leaf(fqdn, "tags", "database"))}}
	_, text, diags := drafted(t, edgeView(), f, g)
	if got := instanceBlock(t, text, "site"); !strings.Contains(got, "        # Not wired: site reads server's "+
		"properties.fullyQualifiedDomainName at tags.database; the format writes a neighbour's value under "+
		"properties alone.\n        server:\n          server:\n            target: server\n") {
		t.Errorf("the site is written\n%s", got)
	}
	onlyCode(t, diags, CodeReferenceNotWired, 1)
	for _, in := range written[0].Inputs {
		if in.Source.Pillar == schemadraft.PillarInject {
			t.Errorf("the drafted Recipe reads %v off the inject pipe", in.Property.Path)
		}
	}
}

// TestEdgeEnvListOfAnInstanceIsReadOffItsParent delivers a reference the
// parent folded into an instance made of its child writes in the row's
// environment list, where the child writes none, as it delivers one the
// instance's own node writes: the environment list is read as the row's
// placements are.
func TestEdgeEnvListOfAnInstanceIsReadOffItsParent(t *testing.T) {
	const hostType, revisionType = "Toy.Compute/hosts", "Toy.Compute/hosts/revisions"
	f := edgeFormat(nil)
	f.Table.Generated = append(f.Table.Generated, Row{Key: hostType, Component: computeServer, Category: "compute",
		Recipe: "server-host", Form: FormRaw, Traits: map[string]string{"component.aggrega.ai/type": "server"},
		Env:   &EnvList{Path: []string{"properties", "env"}, Name: "name", Value: "value"},
		Folds: []Fold{{Type: revisionType, Key: []string{"values", "revision"}, From: []string{"name"}, Instances: true}}})
	host := &Node{ID: "host", Type: hostType, Form: FormRaw, Name: text("h"), Body: []Prop{{Name: "properties",
		Props: []Prop{{Name: "env", Items: []Prop{envEntry("DB_HOST", fqdn)}}}}}}
	revision := &Node{ID: "blue", Type: revisionType, Form: FormRaw, Parent: "host", Name: text("blue")}
	_, aggregate, diags := drafted(t, edgeView(), f, &Graph{Nodes: []*Node{pgServer(), host, revision}})
	wantBlock(t, aggregate, "blue", `    blue:
      type: core/compute/server
      values:
        revision: "blue"
      uses:
        server:
          server:
            target: server
            inject:
              env:
                - in: "DB_HOST"
                  from: host
`)
	onlyCode(t, diags, CodeReferenceNotWired, 0)
}
