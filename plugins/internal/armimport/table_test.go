// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// TestArmTableMergesItsHandRows merges the hand rows into the generated rows
// with no contradiction, no duplicate and no malformed converter, and every
// hand row names its Component's category, which the importer kit weighs a
// candidate by.
func TestArmTableMergesItsHandRows(t *testing.T) {
	if _, err := Table().Rows(); err != nil {
		t.Fatalf("the arm table does not merge: %v", err)
	}
	for _, r := range handRows {
		if r.Category == "" {
			t.Errorf("the hand row of %s%s names no category", r.Key, cloudimport.WhenText(r.When))
		}
	}
}

// resolved returns the Component and the Recipe of the row the arm table
// resolves for n, empty where it resolves none, and the Components of the
// rows n could take where which one depends on a value set when the template
// deploys.
func resolved(n *cloudimport.Node) (string, string, []string) {
	row, could, ok := Table().Resolve(n, &cloudimport.Graph{Nodes: []*cloudimport.Node{n}})
	var couldTake []string
	for _, r := range could {
		couldTake = append(couldTake, r.Component)
	}
	if !ok {
		return "", "", couldTake
	}
	return row.Component, row.Recipe, couldTake
}

// prop is a member of a node's body holding the JSON raw at the path given.
func prop(raw string, path ...string) cloudimport.Prop {
	p := cloudimport.Prop{Name: path[len(path)-1], Value: cloudimport.Literal{JSON: json.RawMessage(raw)}}
	for i := len(path) - 2; i >= 0; i-- {
		p = cloudimport.Prop{Name: path[i], Props: []cloudimport.Prop{p}}
	}
	return p
}

// TestArmContainerAppIngressChoosesItsComponent maps a container app taking
// ingress to the server Component and one taking none to the worker; a
// container app deployed through the Azure Verified Modules module, which
// states its ingress by parameters the rows do not read, is decided by
// neither, both rows named.
func TestArmContainerAppIngressChoosesItsComponent(t *testing.T) {
	const appType = "Microsoft.App/containerApps"
	server := &cloudimport.Node{ID: "web", Type: appType, Form: cloudimport.FormRaw,
		Body: []cloudimport.Prop{prop(`{"targetPort": 8080}`, "properties", "configuration", "ingress")}}
	if c, r, _ := resolved(server); c != "core/compute/server" || r != "server-container-apps" {
		t.Errorf("a container app taking ingress maps to %s by %s", c, r)
	}
	worker := &cloudimport.Node{ID: "worker", Type: appType, Form: cloudimport.FormRaw,
		Body: []cloudimport.Prop{prop(`{}`, "properties", "template")}}
	if c, r, _ := resolved(worker); c != "core/compute/worker" || r != "worker-container-apps" {
		t.Errorf("a container app taking no ingress maps to %s by %s", c, r)
	}
	module := &cloudimport.Node{ID: "app", Type: appType, Form: cloudimport.FormAVM,
		Body: []cloudimport.Prop{prop(`true`, "disableIngress")}}
	if c, _, could := resolved(module); c != "" || len(could) != 2 {
		t.Errorf("a container app module maps to %q, could take %q; want undecided between both rows", c, could)
	}
}

// TestArmSiteKindChoosesItsComponent maps an App Service site whose kind
// names a function app to the function Component, and a site of any other
// kind to the server Component, but a Logic Apps Standard site, whose kind
// names a workflow app, to none: it is drafted from the source.
func TestArmSiteKindChoosesItsComponent(t *testing.T) {
	for kind, want := range map[string]string{
		"functionapp,linux,container":   "function-azure-functions",
		"functionapp,linux":             "function-azure-functions",
		"FunctionApp":                   "function-azure-functions",
		"app,linux,container":           "server-app-service",
		"app,linux":                     "server-app-service",
		"app":                           "server-app-service",
		"functionapp,workflowapp":       "",
		"functionapp,linux,WorkflowApp": "",
	} {
		n := &cloudimport.Node{ID: "site", Type: "Microsoft.Web/sites", Form: cloudimport.FormRaw,
			Body: []cloudimport.Prop{prop(`"`+kind+`"`, "kind")}}
		if _, r, _ := resolved(n); r != want {
			t.Errorf("a site of kind %s maps by %q, want %s", kind, r, want)
		}
	}
}

// TestArmRedisSwitchesAreReadAsBooleans reads the Azure Managed Redis
// switches Azure writes Enabled or Disabled, and the eviction policy it names
// its own way, through the converters the Recipe's keys need.
func TestArmRedisSwitchesAreReadAsBooleans(t *testing.T) {
	rows, err := Table().Rows()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]cloudimport.Conv{
		"high_availability_enabled":                           enabledBool,
		"default_database_access_keys_authentication_enabled": enabledBool,
		"maxmemory_policy":                                    evictionPolicy,
	}
	for _, r := range rows {
		if r.Key != "Microsoft.Cache/redisEnterprise" {
			continue
		}
		for _, p := range append(append([]cloudimport.Placement(nil), r.Values...), r.Overrides...) {
			if conv, ok := want[p.Key]; ok && p.Conv != conv {
				t.Errorf("%s is read with %q, want %q", p.Key, p.Conv, conv)
			}
			delete(want, p.Key)
		}
	}
	if len(want) > 0 {
		t.Errorf("the redis row places none of %v", want)
	}
}

// TestArmFrontDoorSkuChoosesTheRow maps a CDN profile of a Front Door SKU to
// the Front Door Component, and a classic CDN profile to none: it is drafted
// from the source.
func TestArmFrontDoorSkuChoosesTheRow(t *testing.T) {
	for sku, want := range map[string]string{
		"Standard_AzureFrontDoor": "azure-front-door",
		"Premium_AzureFrontDoor":  "azure-front-door",
		"Standard_Microsoft":      "",
		"Standard_Verizon":        "",
	} {
		n := &cloudimport.Node{ID: "profile", Type: "Microsoft.Cdn/profiles", Form: cloudimport.FormRaw,
			Body: []cloudimport.Prop{prop(`"`+sku+`"`, "sku", "name")}}
		if _, r, _ := resolved(n); r != want {
			t.Errorf("a profile of the SKU %s maps by %q, want %q", sku, r, want)
		}
	}
}
