// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"slices"
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
