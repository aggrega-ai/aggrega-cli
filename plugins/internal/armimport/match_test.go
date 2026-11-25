// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// TestResourceIdWithScopeArgumentsMatchesOnlyItsOwnScope reads resourceId
// calls naming the template's virtual network by type and name: with no scope
// argument, or with resourceGroup().name and subscription().subscriptionId,
// each names the template's own network; with another resource group or
// subscription each names a network the template reads and does not declare,
// never the same-named one it declares; and a network declared in another
// resource group is named by the calls naming that group.
func TestResourceIdWithScopeArgumentsMatchesOnlyItsOwnScope(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(twoZero(`
		"vnet": {"type": "Microsoft.Network/virtualNetworks", "apiVersion": "2023-09-01", "name": "vnet-core"},
		"shared": {"type": "Microsoft.Network/virtualNetworks", "apiVersion": "2023-09-01", "name": "vnet-core",
			"existing": true, "resourceGroup": "rg-net"},
		"app": {"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "app", "properties": {
			"bare": "[resourceId('Microsoft.Network/virtualNetworks', 'vnet-core')]",
			"group": "[resourceId(resourceGroup().name, 'Microsoft.Network/virtualNetworks', 'vnet-core')]",
			"both": "[resourceId(subscription().subscriptionId, resourceGroup().name, 'Microsoft.Network/virtualNetworks', 'vnet-core')]",
			"otherGroup": "[resourceId('rg-net', 'Microsoft.Network/virtualNetworks', 'vnet-core')]",
			"otherSubscription": "[resourceId('00000000-0000-0000-0000-000000000000', resourceGroup().name, 'Microsoft.Network/virtualNetworks', 'vnet-core')]",
			"parameterGroup": "[resourceId(parameters('name'), 'Microsoft.Network/virtualNetworks', 'vnet-core')]"}}`))
	if len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	app := node(t, g, "app")
	for _, row := range []struct{ path, want string }{
		{"bare", "vnet"},
		{"group", "vnet"},
		{"both", "vnet"},
		{"otherGroup", "shared"},
		{"otherSubscription", "resourceId('00000000-0000-0000-0000-000000000000', resourceGroup().name, " +
			"'Microsoft.Network/virtualNetworks', 'vnet-core')"},
		{"parameterGroup", "resourceId(parameters('name'), 'Microsoft.Network/virtualNetworks', 'vnet-core')"},
	} {
		if got := at(t, app, "properties", row.path); !reflect.DeepEqual(got, cloudimport.Ref{Node: row.want}) {
			t.Errorf("%s names %+v, want %s", row.path, got, row.want)
		}
	}
}

// TestMatchDependsOnByNameResourceIdAndSymbol reads dependsOn entries naming
// a resource by its name, by a resourceId call matched on the canonical print
// of its type and name arguments — the variables they read written in place,
// concat and format alike — and, in a 2.0 template, by symbolic name; an entry
// naming no resource of the template names the resource it reads.
func TestMatchDependsOnByNameResourceIdAndSymbol(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero(`{"farm": "[format('farm-{0}', parameters('name'))]"}`, `
		{"type": "Microsoft.Web/serverfarms", "apiVersion": "2023-01-01", "name": "[concat('farm-', parameters('name'))]"},
		{"type": "Microsoft.Storage/storageAccounts", "apiVersion": "2023-01-01", "name": "stcore"},
		{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web", "dependsOn": [
			"stcore",
			"[resourceId('Microsoft.Web/serverfarms', variables('farm'))]",
			"Microsoft.Storage/storageAccounts/stcore",
			"[resourceId('Microsoft.Network/virtualNetworks', 'vnet-hub')]",
			"logs"]}`))
	if len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	want := []string{"stcore", "serverfarms", "resourceId('Microsoft.Network/virtualNetworks', 'vnet-hub')", "logs"}
	if got := node(t, g, "web").DependsOn; !slices.Equal(got, want) {
		t.Errorf("web depends on %v, want %v", got, want)
	}

	g, diags = readTemplate(twoZero(`
		"store": {"type": "Microsoft.Storage/storageAccounts", "apiVersion": "2023-01-01", "name": "stcore"},
		"web": {"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web", "dependsOn": ["store"]}`))
	if len(diags) != 0 || !slices.Equal(node(t, g, "web").DependsOn, []string{"store"}) {
		t.Errorf("the 2.0 web depends on %v, diagnostics %+v", node(t, g, "web").DependsOn, diags)
	}
}

// TestMatchAChildFindsItsParentByTypeAndName reads resources written beside
// their parents: a child's parent is the resource of its type less the last
// segment and its name less the last segment, or, where the template does not
// declare that resource, the resource it reads; an extension resource's
// parent is the resource its scope names.
func TestMatchAChildFindsItsParentByTypeAndName(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "Microsoft.Sql/servers", "apiVersion": "2023-05-01-preview", "name": "[parameters('name')]"},
		{"type": "Microsoft.Sql/servers/databases", "apiVersion": "2023-05-01-preview",
			"name": "[format('{0}/{1}', parameters('name'), 'orders')]"},
		{"type": "Microsoft.Sql/servers/databases", "apiVersion": "2023-05-01-preview",
			"name": "[concat(parameters('rg'), '/billing')]"},
		{"type": "Microsoft.Authorization/locks", "apiVersion": "2020-05-01", "name": "lock-sql",
			"scope": "[format('Microsoft.Sql/servers/{0}', parameters('name'))]"}`))
	if len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	for _, row := range []struct{ id, parent string }{
		{"orders", "servers"},
		{"billing", "resourceId('Microsoft.Sql/servers', parameters('rg'))"},
		{"lock-sql", "servers"},
	} {
		if got := node(t, g, row.id).Parent; got != row.parent {
			t.Errorf("%s's parent is %q, want %q", row.id, got, row.parent)
		}
	}
}
