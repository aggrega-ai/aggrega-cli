// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

func TestFoldChildAtItsKey(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		pgServer(),
		{ID: "logConnections", Type: configType, Form: FormRaw, Parent: "server", Source: "main.json#/config",
			Body: []Prop{leaf(text("on"), "properties", "value")}},
	}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	c := classOf(t, out, "logConnections")
	if c.Class != ClassFolded || c.Into != "server" || c.Row == nil || c.Row.Key != serverType {
		t.Fatalf("logConnections is %s into %q by %v, want folded into server by its row", c.Class, c.Into, c.Row)
	}
	folded := withCode(diags, CodeResourceFolded)
	if len(folded) != 1 || folded[0].Severity != aggrega.SeverityInfo || folded[0].SourceRef != "main.json#/config" {
		t.Fatalf("folded records %+v, want one info on logConnections", folded)
	}
	mentions(t, "the record", folded[0].Message, "logConnections", "server", "infra.overrides.configurations")
	if server := classOf(t, out, "server"); server.Class != ClassMapped {
		t.Errorf("server is %s, want mapped", server.Class)
	}
}

func TestFoldChildNotFoldedIsDraftedWithAUsesEdgeToItsParent(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		pgServer(),
		{ID: "admin", Type: adminType, Form: FormRaw, Parent: "server", Name: text("aad-admin"),
			Body: []Prop{leaf(text("User"), "properties", "principalType")}},
	}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	c := classOf(t, out, "admin")
	if c.Class != ClassDrafted || len(c.Drafts) != 2 {
		t.Fatalf("admin is %s with %d drafts, want drafted as its own instance", c.Class, len(c.Drafts))
	}
	if !slices.ContainsFunc(Edges(g), func(e Edge) bool {
		return e.From == "admin" && e.To == "server" && e.Kind == EdgeDepends
	}) {
		t.Errorf("edges %+v, want admin depending on its parent server", Edges(g))
	}
	if got := ids(Order(g)); slices.Index(got, "server") > slices.Index(got, "admin") {
		t.Errorf("order %q places admin before its parent", got)
	}
	if len(withCode(diags, CodeResourceFolded)) != 0 {
		t.Errorf("admin is reported folded: %+v", diags)
	}
}

func TestFoldLockAndDiagnosticSettingAreNotCarried(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "law", Type: workspaceType, Form: FormRaw, Name: text("law-core")},
		{ID: "lock", Type: lockType, Form: FormRaw, Parent: "server", Source: "main.json#/lock",
			Body: []Prop{leaf(text("CanNotDelete"), "properties", "level")}},
		{ID: "logs", Type: diagnosticType, Form: FormRaw, Parent: "server", Source: "main.json#/logs", Body: []Prop{
			leaf(Ref{Node: "law"}, "properties", "workspaceId"),
			leaf(literal(`[{"category": "PostgreSQLLogs", "enabled": true}]`), "properties", "logs"),
		}},
		pgServer(),
	}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	notCarried := withCode(diags, CodeResourceNotCarried)
	if len(notCarried) != 2 {
		t.Fatalf("not-carried warnings %+v, want one for the lock and one for the diagnostic setting", notCarried)
	}
	for _, want := range []struct {
		id, source, says string
		names            []string
	}{
		{"lock", "main.json#/lock", lockSays, nil},
		{"logs", "main.json#/logs", diagSays, []string{"law at properties.workspaceId"}},
	} {
		if c := classOf(t, out, want.id); c.Class != ClassNotCarried || len(c.Drafts) != 0 {
			t.Errorf("%s is %s with drafts %v, want not carried", want.id, c.Class, c.Drafts)
		}
		i := slices.IndexFunc(notCarried, func(d aggrega.Diagnostic) bool { return d.SourceRef == want.source })
		if i < 0 || notCarried[i].Severity != aggrega.SeverityWarn {
			t.Errorf("no not-carried warning on %s in %+v", want.id, notCarried)
			continue
		}
		mentions(t, "the warning", notCarried[i].Message, append([]string{want.id, want.says}, want.names...)...)
	}
}

func TestFoldRoleAssignmentIsSubstrateNamingPrincipalAndRole(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "contributor", Type: roleDefType, Form: FormRaw, Existing: true},
		{ID: "identity", Type: identityType, Form: FormRaw, Name: text("id-app")},
		{ID: "grant", Type: roleType, Form: FormRaw, Parent: "server", Body: []Prop{
			leaf(Ref{Node: "contributor"}, "properties", "roleDefinitionId"),
			leaf(Ref{Node: "identity", Attr: []string{"properties", "principalId"}}, "properties", "principalId"),
			leaf(text("ServicePrincipal"), "properties", "principalType"),
		}},
		pgServer(),
	}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	c := classOf(t, out, "grant")
	if c.Class != ClassSubstrate || len(c.Drafts) != 0 {
		t.Fatalf("grant is %s with drafts %v, want substrate", c.Class, c.Drafts)
	}
	principal, role := "identity's properties.principalId at properties.principalId",
		"contributor at properties.roleDefinitionId"
	mentions(t, "grant's rationale", c.Decision.Rationale, roleSays, principal, role)
	left := withCode(diags, CodeSubstrateLeftToOperator)
	if len(left) != 1 {
		t.Fatalf("substrate warnings %+v, want one", left)
	}
	mentions(t, "the warning", left[0].Message, "grant ("+roleType+")", principal, role, roleSays)
}

func TestFoldOfOneValueTakesTheFirstByID(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		pgServer(),
		{ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server", Name: text("orders"),
			Source: "main.json#/ordersDb"},
		{ID: "billingDb", Type: databaseType, Form: FormRaw, Parent: "server", Name: text("billing"),
			Source: "main.json#/billingDb"},
	}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	if c := classOf(t, out, "billingDb"); c.Class != ClassFolded || c.Into != "server" {
		t.Errorf("billingDb is %s into %q, want folded into server as the first by ID", c.Class, c.Into)
	}
	c := classOf(t, out, "ordersDb")
	if c.Class != ClassNotCarried {
		t.Fatalf("ordersDb is %s, want not carried", c.Class)
	}
	notCarried := withCode(diags, CodeResourceNotCarried)
	if len(notCarried) != 1 || notCarried[0].SourceRef != "main.json#/ordersDb" {
		t.Fatalf("not-carried warnings %+v, want one on ordersDb", notCarried)
	}
	mentions(t, "the warning", notCarried[0].Message, "ordersDb", "billingDb", "server", "values.database")
	mentions(t, "ordersDb's rationale", c.Decision.Rationale, "billingDb")
}

func TestFoldAccountWithTwoModelDeploymentsIsTwoInstances(t *testing.T) {
	account := &Node{ID: "account", Type: accountType, Form: FormRaw, Name: text("ai"), Source: "main.json#/account",
		Body: []Prop{leaf(text("OpenAI"), "kind"), leaf(text("westeurope"), "location")}}
	model := func(id, name string) *Node {
		return &Node{ID: id, Type: deploymentType, Form: FormRaw, Parent: "account", Name: text(name),
			Body: []Prop{leaf(text(name), "properties", "model", "name")}}
	}
	g := &Graph{Nodes: []*Node{account, model("embed", "text-embedding-3-large"), model("chat", "gpt-4o")}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	for _, id := range []string{"chat", "embed"} {
		c := classOf(t, out, id)
		if c.Class != ClassMapped || c.Row == nil || c.Row.Key != accountType || c.Entity.Name != openai ||
			c.Entity.Mode != "reference" {
			t.Errorf("%s is %s by %v as %+v, want an instance of the account's row referencing %s", id, c.Class,
				c.Row, c.Entity, openai)
		}
		mentions(t, id+"'s rationale", c.Decision.Rationale, "folding account", "creates its own")
		if slices.Contains(c.Node.DependsOn, "account") {
			t.Errorf("%s depends on the account folded into it", id)
		}
	}
	c := classOf(t, out, "account")
	if c.Class != ClassFolded || c.Into != "chat" {
		t.Fatalf("account is %s into %q, want folded, into chat first by ID", c.Class, c.Into)
	}
	mentions(t, "account's rationale", c.Decision.Rationale, "chat and embed", "creates its own")
	folded := withCode(diags, CodeResourceFolded)
	if len(folded) != 1 || folded[0].SourceRef != "main.json#/account" {
		t.Fatalf("folded records %+v, want one on the account", folded)
	}
	mentions(t, "the record", folded[0].Message, "account", "chat and embed", "creates its own")
}

// TestFoldExistingChildStaysExisting keeps a child the source reads out of a
// fold of its parent's row: it is existing, and the fold takes the child the
// source creates.
func TestFoldExistingChildStaysExisting(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		pgServer(),
		{ID: "aDefault", Type: databaseType, Form: FormRaw, Parent: "server", Existing: true, Name: text("postgres")},
		{ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server", Name: text("orders")},
	}}
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	if c := classOf(t, out, "aDefault"); c.Class != ClassExisting {
		t.Errorf("aDefault is %s, want existing: the source reads it and creates none", c.Class)
	}
	if c := classOf(t, out, "ordersDb"); c.Class != ClassFolded || c.Into != "server" {
		t.Errorf("ordersDb is %s into %q, want folded into server", c.Class, c.Into)
	}
	if got := withCode(diags, CodeResourceNotCarried); len(got) != 0 {
		t.Errorf("not-carried warnings %+v, want none", got)
	}
}

// TestFoldChildOfARepeatedParentIsDraftedOnItsOwn classes a child whose
// parent repeats into two instances, or into none, as an instance of its own,
// its decision saying why.
func TestFoldChildOfARepeatedParentIsDraftedOnItsOwn(t *testing.T) {
	pair := &Node{ID: "server", Type: serverType, Form: FormRaw, Repeat: &Repetition{Keys: []string{"0", "1"},
		Instance: func(string) *Node { return pgServer() }}}
	spare := &Node{ID: "spare", Type: serverType, Form: FormRaw, Repeat: &Repetition{Keys: []string{}}}
	g := &Graph{Nodes: []*Node{pair, spare,
		{ID: "logConnections", Type: configType, Form: FormRaw, Parent: "server", Body: []Prop{leaf(text("on"), "properties", "value")}},
		{ID: "orphan", Type: configType, Form: FormRaw, Parent: "spare", Body: []Prop{leaf(text("on"), "properties", "value")}},
	}}
	Expand(g)
	out, diags := Classify(fixtureView(""), fixtureFormat(), g)
	noHalt(t, diags)
	c := classOf(t, out, "logConnections")
	if c.Class != ClassDrafted || !slices.Contains(c.Node.DependsOn, "server-0") ||
		!slices.Contains(c.Node.DependsOn, "server-1") {
		t.Fatalf("logConnections is %s depending on %q, want drafted depending on both servers", c.Class,
			c.Node.DependsOn)
	}
	mentions(t, "logConnections' rationale", c.Decision.Rationale, "server-0 and server-1")
	o := classOf(t, out, "orphan")
	if o.Class != ClassDrafted || len(o.Node.DependsOn) != 0 {
		t.Fatalf("orphan is %s depending on %q, want drafted depending on nothing", o.Class, o.Node.DependsOn)
	}
	mentions(t, "orphan's rationale", o.Decision.Rationale, "spare deploys no instance")
}

// ordersDb is a database of the PostgreSQL server whose name the source
// composes of the server's and its own, leaf its own part, nil where the
// source composes them so it cannot be told apart.
func ordersDb(leaf Value) *Node {
	return &Node{ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server", Source: "main.json#/ordersDb",
		Name: Compose{Parts: []Value{text("pg-main"), text("/orders")}}, Composed: true, Leaf: leaf}
}

// TestFoldReadsTheChildsOwnName writes, where a fold reads a child's name, the
// child's own name within its parent: its Leaf where the source composes its
// name of its parent's and its own, its name where it does not; and where the
// source composes the two so the child's own part cannot be told apart, writes
// nothing and warns value-not-carried, never writing the full name.
func TestFoldReadsTheChildsOwnName(t *testing.T) {
	for _, c := range []struct {
		name  string
		child *Node
		want  string
	}{
		{"composed, with its own part", ordersDb(text("orders")), "        database: \"orders\"\n"},
		{"its own name alone", &Node{ID: "ordersDb", Type: databaseType, Form: FormRaw, Parent: "server",
			Name: text("orders")}, "        database: \"orders\"\n"},
	} {
		_, aggregate, diags := drafted(t, fixtureView(""), fixtureFormat(), &Graph{Nodes: []*Node{pgServer(), c.child}})
		if !strings.Contains(instanceBlock(t, aggregate, "server"), c.want) {
			t.Errorf("%s: the server is written\n%s\nwant it to hold %q", c.name, instanceBlock(t, aggregate, "server"),
				c.want)
		}
		onlyCode(t, diags, CodeValueNotCarried, 0)
	}

	_, aggregate, diags := drafted(t, fixtureView(""), fixtureFormat(), &Graph{Nodes: []*Node{pgServer(), ordersDb(nil)}})
	if block := instanceBlock(t, aggregate, "server"); strings.Contains(block, "database:") {
		t.Errorf("the server is written with a database the source composes of two names:\n%s", block)
	}
	warnsAbout(t, onlyCode(t, diags, CodeValueNotCarried, 1), CodeValueNotCarried, "ordersDb", "values.database",
		"its own name within its parent")
}

// The fixture of an instance a fold makes of a child: a SQL server whose row
// makes each of its databases an instance of its own.
const (
	sqlServerType = "Microsoft.Sql/servers"
	sqlDbType     = sqlServerType + "/databases"
	sqlServer     = "core/datastore/sql-server"
)

// sqlFormat is the fixture's format whose SQL server row makes each database
// an instance folding the server, its placements reading the edition, the
// location, the login and the password.
func sqlFormat() Format {
	f := fixtureFormat()
	f.Table.Generated = append(f.Table.Generated, Row{Key: sqlServerType, Component: sqlServer,
		Category: "datastore", Recipe: "sql-server-azure-sql-database", Form: FormRaw,
		Traits: map[string]string{"component.aggrega.ai/type": "stateful-server"},
		Values: []Placement{{From: []string{"properties", "administratorLogin"}, Key: "user"},
			{From: []string{"sku", "name"}, Key: "edition"}},
		Overrides: []Placement{{From: []string{"sku", "name"}, Key: "sku_name"},
			{From: []string{"location"}, Key: "location"}},
		Secrets: []Placement{{From: []string{"properties", "administratorLoginPassword"}, Key: "admin_password"}},
		Folds: []Fold{{Type: sqlDbType, Key: []string{"values", "database"}, From: []string{"name"},
			Instances: true}}})
	return f
}

// TestFoldInstanceReadsTheChildBeforeItsParent reads each placement of an
// instance a fold makes of a child off the child where it writes the path,
// and off the parent folded into it otherwise, in the Aggregate and in the
// Release alike, the secret included, the child's name its own name within
// its parent.
func TestFoldInstanceReadsTheChildBeforeItsParent(t *testing.T) {
	server := &Node{ID: "sql", Type: sqlServerType, Form: FormRaw, Name: text("sql-main"), Body: merged(
		leaf(text("westeurope"), "location"), leaf(text("Server_Sku"), "sku", "name"),
		leaf(text("sqladmin"), "properties", "administratorLogin"),
		leaf(Param{Name: "sqlPassword"}, "properties", "administratorLoginPassword"))}
	db := &Node{ID: "orders", Type: sqlDbType, Form: FormRaw, Parent: "sql",
		Name: Compose{Parts: []Value{text("sql-main/orders")}}, Composed: true, Leaf: text("orders"),
		Body: []Prop{leaf(text("S0"), "sku", "name")}}
	g := &Graph{Nodes: []*Node{server, db}, Params: []Parameter{{Name: "sqlPassword", Type: "securestring",
		Secure: true, StoreRef: "/subscriptions/s/resourceGroups/g/providers/Microsoft.KeyVault/vaults/v/secrets/p"}}}
	in := fixtureView("", component(sqlServer, map[string]string{"component.aggrega.ai/category": "datastore",
		"component.aggrega.ai/type": "stateful-server"}))
	in.Target = "arm"
	_, aggregate, release, _, diags := released(t, in, sqlFormat(), g)
	wantBlock(t, aggregate, "orders", `    orders:
      type: core/datastore/sql-server
      values:
        user: "sqladmin"
        edition: "S0"
        database: "orders"
`)
	block := instanceBlock(t, release, "orders")
	for _, want := range []string{`location: "westeurope"`, `sku_name: "S0"`, "admin_password:",
		"origin: \"/subscriptions/s/resourceGroups/g/providers/Microsoft.KeyVault/vaults/v/secrets/p\""} {
		if !strings.Contains(block, want) {
			t.Errorf("the Release entry of orders does not hold %s:\n%s", want, block)
		}
	}
	if strings.Contains(release, "Server_Sku") || strings.Contains(aggregate, "Server_Sku") {
		t.Errorf("the server's own edition is read where the database writes one:\n%s\n%s", aggregate, release)
	}
	onlyCode(t, diags, CodeValueNotCarried, 0)
}

// TestFoldInstanceNamesWhatItsFoldedParentWritesUnplaced names, in the
// Release entry of an instance a fold makes of a child, each property the
// parent folded into it writes that its row does not place, as it names a
// folded child's.
func TestFoldInstanceNamesWhatItsFoldedParentWritesUnplaced(t *testing.T) {
	server := &Node{ID: "sql", Type: sqlServerType, Form: FormRaw, Name: text("sql-main"), Body: merged(
		leaf(text("westeurope"), "location"), leaf(text("sqladmin"), "properties", "administratorLogin"),
		leaf(text("1.2"), "properties", "minimalTlsVersion"))}
	db := &Node{ID: "orders", Type: sqlDbType, Form: FormRaw, Parent: "sql",
		Name: Compose{Parts: []Value{text("sql-main/orders")}}, Composed: true, Leaf: text("orders"),
		Body: []Prop{leaf(text("S0"), "sku", "name")}}
	in := fixtureView("", component(sqlServer, map[string]string{"component.aggrega.ai/category": "datastore",
		"component.aggrega.ai/type": "stateful-server"}))
	in.Target = "arm"
	_, _, release, _, _ := released(t, in, sqlFormat(), &Graph{Nodes: []*Node{server, db}})
	want := "    # Properties sql, folded into this instance, writes and the draft does not place: " +
		"properties.minimalTlsVersion.\n    orders:\n"
	if !strings.Contains(release, want) {
		t.Errorf("the Release does not hold\n%s\n%s", want, release)
	}
}

// settingFolds are the folds of the toy server's row writing a setting into
// the server's instance beside the toy format's own: its name under values
// and its unit under infra.overrides, as a row folding one child resource
// writes one fold for each key it places the child under.
func settingFolds() []Fold {
	return []Fold{
		{Type: toySetting, Key: []string{"values", "setting"}, From: []string{"name"}},
		{Type: toySetting, Key: []string{"infra", "overrides", "unit"}, From: []string{"unit"}},
	}
}

// TestFoldEveryFoldOfAChildTypePlacesTheChild folds a setting into the
// server's instance by several folds of its type: the setting is written
// under every fold's key, in the Aggregate and the Release alike, the record
// and the decision name each key and the folds, and the Release names only
// the property no fold places.
func TestFoldEveryFoldOfAChildTypePlacesTheChild(t *testing.T) {
	f := toyFormat(nil)
	f.Table.Generated[0].Folds = append(f.Table.Generated[0].Folds, settingFolds()...)
	out := Import(toyInput(t, "toy", "", []string{"folds.json"}), f, toyParse)
	noHalt(t, out.Diagnostics)
	wantBlock(t, fileOf(t, out, "aggregate.yaml"), "db", `    db:
      type: core/datastore/postgres
      values:
        version: "16"
        setting: "max-connections"
`)
	wantBlock(t, fileOf(t, out, "release.yaml"), "db", `    # Properties dbSetting, folded into this instance, writes and the draft does not place: scope.
    db:
      infra:
        location: "westeurope"
        overrides:
          settings: "200"
          unit: "connections"
`)
	folded := withCode(out.Diagnostics, CodeResourceFolded)
	if len(folded) != 1 {
		t.Fatalf("folded records %+v, want one on dbSetting", folded)
	}
	if want := "dbSetting (" + toySetting + ") is folded into the instance of db, written at " +
		"infra.overrides.settings, values.setting and infra.overrides.unit"; folded[0].Message != want {
		t.Errorf("the record reads %q, want %q", folded[0].Message, want)
	}
	for _, d := range out.Decisions {
		if d.SourceNode.ID != "dbSetting" {
			continue
		}
		if want := "folded: written into the instance of db at infra.overrides.settings, values.setting and " +
			"infra.overrides.unit, by the folds of " + toySetting + " in the row of " + toyServer; !strings.HasPrefix(
			d.Rationale, want) {
			t.Errorf("the decision on dbSetting reads %q, want it to open %q", d.Rationale, want)
		}
	}
}

// TestFoldHandFoldBesideAGeneratedOneOfItsType merges a hand row folding the
// setting's unit into the generated row folding its value: the two folds of
// one type are two folds of the merged row, neither contradicting nor
// replacing the other, and the setting is written under both keys.
func TestFoldHandFoldBesideAGeneratedOneOfItsType(t *testing.T) {
	f := toyFormat(nil)
	gen := f.Table.Generated[0]
	f.Table.Hand = []Row{{Key: gen.Key, Origin: "hand", Folds: settingFolds()[1:]}}
	rows, err := f.Table.Rows()
	if err != nil {
		t.Fatalf("Rows() error = %v", err)
	}
	if want := append(slices.Clone(gen.Folds), settingFolds()[1]); len(rows) != 1 ||
		!slices.EqualFunc(rows[0].Folds, want, foldsSame) {
		t.Fatalf("rows = %+v, want one row folding %+v", rows, want)
	}
	out := Import(toyInput(t, "toy", "", []string{"folds.json"}), f, toyParse)
	noHalt(t, out.Diagnostics)
	wantBlock(t, fileOf(t, out, "release.yaml"), "db", `    # Properties dbSetting, folded into this instance, writes and the draft does not place: scope.
    db:
      infra:
        location: "westeurope"
        overrides:
          settings: "200"
          unit: "connections"
`)
}
