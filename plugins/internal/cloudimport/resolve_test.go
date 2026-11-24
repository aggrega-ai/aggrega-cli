// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"reflect"
	"testing"
)

// sitesTable holds two rows of sitesType: the row of every kind, first in
// table order, and the row of a kind containing functionapp.
func sitesTable() Table {
	return Table{Generated: []Row{
		{Key: sitesType, Component: "core/compute/server", Recipe: "server-app-service", Form: FormRaw},
		{Key: sitesType, Component: "core/compute/function", Recipe: "function-azure-functions", Form: FormRaw,
			When: []Pred{{Path: []string{"kind"}, Op: OpContains, Value: "functionapp"}}},
		{Key: "Microsoft.Storage/storageAccounts", Component: "core/storage/azure-blob-storage",
			Recipe: "azure-blob-storage", Form: FormRaw},
	}}
}

func literal(text string) Literal { return Literal{JSON: json.RawMessage(text)} }

func site(kind Value) *Node {
	return &Node{ID: "web", Type: sitesType, Form: FormRaw, Body: []Prop{{Name: "kind", Value: kind}}}
}

func recipes(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Recipe)
	}
	return out
}

func TestResolvePicksTheMostSpecificRow(t *testing.T) {
	for _, tc := range []struct {
		kind, recipe string
	}{
		{`"functionapp,linux"`, "function-azure-functions"},
		{`"app,linux"`, "server-app-service"},
	} {
		row, could, ok := sitesTable().Resolve(site(literal(tc.kind)), &Graph{})
		if !ok || row.Recipe != tc.recipe || could != nil {
			t.Errorf("kind %s: Resolve = %q, %q, %v; want %q alone", tc.kind, row.Recipe, recipes(could), ok,
				tc.recipe)
		}
	}

	unrowed := &Node{ID: "vault", Type: "Microsoft.KeyVault/vaults", Form: FormRaw}
	if row, could, ok := sitesTable().Resolve(unrowed, &Graph{}); ok || could != nil {
		t.Errorf("a type no row keys: Resolve = %q, %q, %v; want no row and no candidate", row.Recipe,
			recipes(could), ok)
	}
}

func TestResolveReadsAParameterDefault(t *testing.T) {
	g := &Graph{Params: []Parameter{
		{Name: "siteKind", Type: "string", Default: Param{Name: "defaultKind"}},
		{Name: "defaultKind", Type: "string", Default: literal(`"functionapp"`)},
	}}
	row, could, ok := sitesTable().Resolve(site(Param{Name: "siteKind"}), g)
	if !ok || row.Recipe != "function-azure-functions" || could != nil {
		t.Fatalf("Resolve = %q, %q, %v; want function-azure-functions read off the parameter's default",
			row.Recipe, recipes(could), ok)
	}
}

func TestResolveUnresolved(t *testing.T) {
	g := &Graph{Params: []Parameter{{Name: "siteKind", Type: "string"}}}
	row, could, ok := sitesTable().Resolve(site(Param{Name: "siteKind"}), g)
	if ok || row.Recipe != "" {
		t.Fatalf("Resolve chose %q, want no row for a discriminator set by a parameter with no default", row.Recipe)
	}
	if want := []string{"function-azure-functions", "server-app-service"}; !reflect.DeepEqual(recipes(could), want) {
		t.Fatalf("candidates = %q, want %q", recipes(could), want)
	}
}

// ingressRows are the rows of a container app with and without ingress, their
// When written in raw form.
func ingressRows(eqs []Equivalence) Table {
	ingress := []string{"properties", "configuration", "ingress"}
	return Table{Generated: []Row{
		{Key: "Microsoft.App/containerApps", Component: "core/compute/server", Recipe: "server-container-apps",
			Form: FormRaw, When: []Pred{{Path: ingress, Op: OpPresent}}, Equivalences: eqs},
		{Key: "Microsoft.App/containerApps", Component: "core/compute/worker", Recipe: "worker-container-apps",
			Form: FormRaw, When: []Pred{{Path: ingress, Op: OpAbsent}}, Equivalences: eqs},
	}}
}

func TestResolveReadsAModuleNodeThroughEquivalences(t *testing.T) {
	eqs := []Equivalence{{Raw: []string{"properties", "configuration"}, Module: []string{"configuration"}}}
	module := func(body ...Prop) *Node {
		return &Node{ID: "app", Type: "Microsoft.App/containerApps", Form: FormAVM, Body: body}
	}
	withIngress := module(Prop{Name: "configuration", Props: []Prop{
		{Name: "ingress", Props: []Prop{{Name: "targetPort", Value: literal(`8080`)}}},
	}})

	if row, _, ok := ingressRows(eqs).Resolve(withIngress, &Graph{}); !ok || row.Recipe != "server-container-apps" {
		t.Errorf("a module node with ingress: Resolve = %q, %v; want server-container-apps", row.Recipe, ok)
	}
	withoutIngress := module(Prop{Name: "configuration", Props: []Prop{{Name: "activeRevisionsMode",
		Value: literal(`"Single"`)}}})
	if row, _, ok := ingressRows(eqs).Resolve(withoutIngress, &Graph{}); !ok || row.Recipe != "worker-container-apps" {
		t.Errorf("a module node without ingress: Resolve = %q, %v; want worker-container-apps", row.Recipe, ok)
	}

	// With no equivalence the module form's discriminator cannot be read, so no
	// row is chosen and both are candidates.
	row, could, ok := ingressRows(nil).Resolve(withIngress, &Graph{})
	if ok || !reflect.DeepEqual(recipes(could), []string{"server-container-apps", "worker-container-apps"}) {
		t.Errorf("no equivalence: Resolve = %q, %q, %v; want both rows as candidates", row.Recipe, recipes(could), ok)
	}
}

func TestResolveNeverPanicsOnMalformedInput(t *testing.T) {
	table := sitesTable()
	table.Generated[1].When = append(table.Generated[1].When,
		Pred{Path: []string{"properties", "siteConfig", "0", "x"}, Op: OpEquals, Value: "y"},
		Pred{Path: nil, Op: OpPresent},
		Pred{Path: []string{"kind"}, Op: "unknown-op"})
	cyclic := &Graph{Params: []Parameter{
		{Name: "a", Default: Param{Name: "b"}},
		{Name: "b", Default: Param{Name: "a"}},
	}}
	for _, tc := range []struct {
		name string
		n    *Node
	}{
		{"nil node", nil},
		{"no body", &Node{ID: "web", Type: sitesType}},
		{"invalid literal", site(Literal{JSON: json.RawMessage(`{"unclosed`)})},
		{"object kind", site(literal(`{"a": 1}`))},
		{"cyclic parameter", site(Param{Name: "a"})},
		{"undeclared parameter", site(Param{Name: "missing"})},
		{"composed kind", site(Compose{Parts: []Value{literal(`"function"`), Ref{Node: "x"}, nil}})},
		{"leaf in the path", &Node{ID: "web", Type: sitesType, Body: []Prop{
			{Name: "kind", Value: literal(`"functionapp"`)},
			{Name: "properties", Value: literal(`{"siteConfig": [{"x": "y"}]}`)},
		}}},
		{"items in the path", &Node{ID: "web", Type: sitesType, Body: []Prop{
			{Name: "properties", Props: []Prop{{Name: "siteConfig", Items: []Prop{{Value: literal(`1`)}}}}},
		}}},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: Resolve panicked: %v", tc.name, r)
				}
			}()
			table.Resolve(tc.n, cyclic)
			table.Resolve(tc.n, nil)
		}()
	}
}

func TestRowForAVMForm(t *testing.T) {
	raw := Row{
		Key: "Microsoft.DBforPostgreSQL/flexibleServers", Component: "core/datastore/postgres",
		Recipe: "postgres-flexible-server", Form: FormRaw,
		Values: []Placement{
			{From: []string{"properties", "version"}, Key: "engine_version", Conv: "string"},
			{From: []string{"properties", "storage", "storageSizeGB"}, Key: "storage", Conv: "unit:Gi"},
		},
		Infra:    []Placement{{From: []string{"sku", "name"}, Key: "sku"}},
		Provides: []Placement{{From: []string{"properties", "fullyQualifiedDomainName"}, Key: "host"}},
		Equivalences: []Equivalence{
			{Raw: []string{"properties", "version"}, Module: []string{"version"}},
			{Raw: []string{"properties", "storage"}, Module: []string{"storage"}},
			{Raw: []string{"sku", "name"}, Module: []string{"skuName"}},
			{Raw: []string{"properties", "fullyQualifiedDomainName"}, Module: []string{"fqdn"}},
		},
	}

	avm := raw.For(FormAVM)
	if avm.Form != FormAVM {
		t.Errorf("form = %q, want %q", avm.Form, FormAVM)
	}
	wantValues := []Placement{
		{From: []string{"version"}, Key: "engine_version", Conv: "string"},
		{From: []string{"storage", "storageSizeGB"}, Key: "storage", Conv: "unit:Gi"},
	}
	if !reflect.DeepEqual(avm.Values, wantValues) {
		t.Errorf("values = %+v, want %+v", avm.Values, wantValues)
	}
	if want := []Placement{{From: []string{"skuName"}, Key: "sku"}}; !reflect.DeepEqual(avm.Infra, want) {
		t.Errorf("infra = %+v, want %+v", avm.Infra, want)
	}
	if want := []Placement{{From: []string{"fqdn"}, Key: "host"}}; !reflect.DeepEqual(avm.Provides, want) {
		t.Errorf("provides = %+v, want %+v", avm.Provides, want)
	}

	if back := avm.For(FormRaw); !reflect.DeepEqual(back, raw) {
		t.Errorf("back in raw form = %+v,\nwant %+v", back, raw)
	}
	if same := raw.For(FormRaw); !reflect.DeepEqual(same, raw) {
		t.Errorf("raw row for the raw form = %+v, want the row as is", same)
	}
}

func TestRowForDropsWhatNoEquivalenceCovers(t *testing.T) {
	raw := Row{
		Key: "Microsoft.App/containerApps", Form: FormRaw,
		When:      []Pred{{Path: []string{"properties", "configuration", "ingress"}, Op: OpPresent}},
		Values:    []Placement{{From: []string{"properties", "template", "containers", "0", "image"}, Key: "image"}},
		Overrides: []Placement{{From: []string{"properties", "environmentId"}, Key: "environment_id"}},
		Secrets: []Placement{{From: []string{"properties", "configuration", "registries", "0", "passwordSecretRef"},
			Key: "registry"}},
		Env: &EnvList{Path: []string{"properties", "template", "containers", "0", "env"}, Name: "name",
			Value: "value"},
		Equivalences: []Equivalence{
			{Raw: []string{"properties", "template", "containers"}, Module: []string{"containers"}},
		},
	}
	avm := raw.For(FormAVM)
	if avm.When != nil || avm.Overrides != nil || avm.Secrets != nil {
		t.Errorf("When, Overrides, Secrets = %+v, %+v, %+v; want none, since no equivalence covers them",
			avm.When, avm.Overrides, avm.Secrets)
	}
	want := []Placement{{From: []string{"containers", "0", "image"}, Key: "image"}}
	if !reflect.DeepEqual(avm.Values, want) {
		t.Errorf("values = %+v, want %+v", avm.Values, want)
	}
	wantEnv := &EnvList{Path: []string{"containers", "0", "env"}, Name: "name", Value: "value"}
	if !reflect.DeepEqual(avm.Env, wantEnv) {
		t.Errorf("env = %+v, want %+v", avm.Env, wantEnv)
	}
	if raw.Env.Path[0] != "properties" || raw.Values[0].From[0] != "properties" {
		t.Errorf("For changed the row it was called on: %+v", raw)
	}
}
