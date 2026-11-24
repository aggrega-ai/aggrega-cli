// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

const (
	serverType = "Microsoft.DBforPostgreSQL/flexibleServers"
	postgres   = "core/datastore/postgres"
	override   = "acme/datastore/postgres-ha"
)

// postgresRow is the row of serverType, with the labels of its Component in
// the hub but the category as its traits.
func postgresRow() *Row {
	return &Row{
		Key: serverType, Component: postgres, Category: "datastore", Recipe: "postgres-flexible-server",
		Traits: map[string]string{
			"component.aggrega.ai/type":   "stateful-server",
			"datastore.aggrega.ai/engine": "postgres",
		},
		Form: FormRaw,
	}
}

func component(name string, labels map[string]string) aggrega.CatalogComponent {
	return aggrega.CatalogComponent{Name: name, Version: "1.0.0", Labels: labels}
}

func datastoreLabels(engine string) map[string]string {
	return map[string]string{
		"component.aggrega.ai/category": "datastore",
		"component.aggrega.ai/type":     "stateful-server",
		"datastore.aggrega.ai/engine":   engine,
	}
}

func server() *Node {
	return &Node{ID: "flexibleServer", Type: serverType, Form: FormRaw}
}

func request(settings string, components ...aggrega.CatalogComponent) aggrega.ImportInput {
	in := aggrega.ImportInput{Format: "arm", Catalog: &aggrega.CatalogProjection{Components: components}}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return in
}

func TestDecideReferencesTheRowsComponent(t *testing.T) {
	in := request("", component("core/datastore/mysql", datastoreLabels("mysql")),
		component(postgres, datastoreLabels("postgres")))
	decision, entity, mapped := Decide(in, server(), postgresRow(), "flexibleServer")
	if !mapped || entity.Mode != "reference" || entity.Name != postgres {
		t.Fatalf("Decide = %+v, mapped %v; want a reference to %s", entity, mapped, postgres)
	}
	if decision.Chosen == nil || decision.Chosen.Target != postgres || decision.Chosen.Tier != "HIGH" {
		t.Fatalf("decision chose %+v, want %s at the high tier", decision.Chosen, postgres)
	}
	if decision.SourceNode.ID != "flexibleServer" || decision.SourceNode.Type != serverType {
		t.Errorf("decision names %+v, want the node flexibleServer of type %s", decision.SourceNode, serverType)
	}
}

func TestDecideHonoursOverrides(t *testing.T) {
	settings := `{"mapping_overrides": {"arm": {"` + serverType + `": {"component": "` + override + `",
		"category": "datastore", "vendor": "postgres-ha",
		"traits": {"component.aggrega.ai/type": "stateful-server", "datastore.aggrega.ai/engine": "postgres"}}}}}`
	in := request(settings, component(override, datastoreLabels("postgres")),
		component(postgres, datastoreLabels("postgres")))
	_, entity, mapped := Decide(in, server(), postgresRow(), "flexibleServer")
	if !mapped || entity.Mode != "reference" || entity.Name != override {
		t.Fatalf("Decide = %+v, mapped %v; want a reference to %s, which the override names", entity, mapped,
			override)
	}
}

func TestDecideWithoutTheComponentDrafts(t *testing.T) {
	in := request("", component("core/datastore/mysql", datastoreLabels("mysql")))
	decision, entity, mapped := Decide(in, server(), postgresRow(), "flexibleServer")
	if mapped || entity.Mode != "draft" {
		t.Fatalf("Decide = %+v, mapped %v; want not mapped, the view lacking %s", entity, mapped, postgres)
	}
	if decision.Chosen == nil || decision.Chosen.Mode != "draft" {
		t.Errorf("decision chose %+v, want a draft", decision.Chosen)
	}
}

func TestDecideUnderHintsOnlyDraftsWithAHint(t *testing.T) {
	in := request(`{"reuse_mode": "hints-only"}`, component(postgres, datastoreLabels("postgres")))
	decision, entity, mapped := Decide(in, server(), postgresRow(), "flexibleServer")
	if mapped || entity.Mode != "draft" {
		t.Fatalf("Decide = %+v, mapped %v; want a draft under hints-only", entity, mapped)
	}
	if !strings.Contains(decision.SwapHint, postgres) {
		t.Errorf("swap hint %q does not name %s", decision.SwapHint, postgres)
	}
}

func TestDecideWithNoRowDrafts(t *testing.T) {
	in := request("", component(postgres, datastoreLabels("postgres")))
	_, entity, mapped := Decide(in, server(), nil, "flexibleServer")
	if mapped || entity.Mode != "draft" {
		t.Fatalf("Decide = %+v, mapped %v; want a draft where no row and no override maps the type", entity, mapped)
	}
	if _, _, mapped := Decide(in, nil, nil, "missing"); mapped {
		t.Errorf("Decide mapped no node")
	}
}

func TestDecideReferencesARowOfTheTypeWrittenInAnotherCase(t *testing.T) {
	n := &Node{ID: "flexibleServer", Type: "Microsoft.DBForPostgreSQL/flexibleServers", Form: FormRaw}
	row, _, ok := Table{Generated: []Row{*postgresRow()}}.Resolve(n, &Graph{})
	if !ok {
		t.Fatalf("Resolve found no row of %s for the type %s", serverType, n.Type)
	}
	in := request("", component(postgres, datastoreLabels("postgres")))
	_, entity, mapped := Decide(in, n, &row, "flexibleServer")
	if !mapped || entity.Mode != "reference" || entity.Name != postgres {
		t.Fatalf("Decide = %+v, mapped %v; want a reference to %s", entity, mapped, postgres)
	}
}
