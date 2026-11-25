// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// layer returns the compiled template of the Azure Verified Modules module at
// the path module, published at version, as testdata/avm holds it.
func layer(t *testing.T, module, version string) []byte {
	t.Helper()
	return fixture(t, "avm/"+module+"/"+version+"/main.json")
}

// deployer is a Microsoft.Resources/deployments resource named name
// deploying the template module, written as JSON, evaluated in its own scope
// and given the parameters args, JSON object members.
func deployer(name string, module []byte, args string) string {
	return `{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "` + name + `",
		"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
			"parameters": {` + args + `}, "template": ` + string(module) + `}}`
}

// deploying is a 1.0 template declaring the parameters adminLogin and
// location and holding the resources given, as JSON text.
func deploying(resources ...string) string {
	return `{` + schema + `, "contentVersion": "1.0.0.0",
		"parameters": {"adminLogin": {"type": "string"}, "location": {"type": "string", "defaultValue": "westeurope"}},
		"resources": [` + strings.Join(resources, ",") + `]}`
}

// postgres are the parameters the PostgreSQL flexible server module is given.
const postgres = `"name": {"value": "psql-orders"}, "administratorLogin": {"value": "[parameters('adminLogin')]"},
	"location": {"value": "[parameters('location')]"}, "skuName": {"value": "Standard_B1ms"},
	"tier": {"value": "Burstable"}, "availabilityZone": {"value": -1}, "databases": {"value": [{"name": "orders"}]}`

// bodyNames returns the name of each member of n's body, in order.
func bodyNames(n *cloudimport.Node) []string {
	out := make([]string, len(n.Body))
	for i, p := range n.Body {
		out[i] = p.Name
	}
	return out
}

// TestAVMRecognisedByLiteralAndByVariablePrefix deploys the published
// PostgreSQL flexible server module at a version naming its telemetry by a
// literal readable prefix, at one naming it through a variable holding the
// hashed prefix, and as built from its source, with its telemetry turned on
// and off. Each is recognised alike: one node of the server's type, in the
// module form, calling the module by its path where the module index lists
// its prefix, at the version the replace call in its telemetry's name states
// — none for the module built from its source — its name the one the module
// gives the server, its body the parameters given but enableTelemetry; no
// telemetry is carried; and one avm-module-recognised names prefix, path,
// version and the name the module's metadata gives it.
func TestAVMRecognisedByLiteralAndByVariablePrefix(t *testing.T) {
	t.Parallel()
	const path = "avm/res/db-for-postgre-sql/flexible-server"
	for _, row := range []struct {
		version    string
		want       cloudimport.ModuleRef
		apiVersion string
		says       []string
	}{
		{"0.9.1", cloudimport.ModuleRef{Version: "0.9.1", Prefix: "46d3xbcp.res.dbforpostgresql-flexibleserver"},
			"2024-08-01", []string{"of telemetry prefix 46d3xbcp.res.dbforpostgresql-flexibleserver, for which the " +
				"module index the import holds lists no path", "version 0.9.1"}},
		{"0.16.2", cloudimport.ModuleRef{Path: path, Version: "0.16.2", Prefix: "46d3xbcp.res.568937f"},
			"2026-01-01-preview", []string{path + ", of telemetry prefix 46d3xbcp.res.568937f", "version 0.16.2"}},
		{"main", cloudimport.ModuleRef{Path: path, Prefix: "46d3xbcp.res.568937f"}, "2026-01-01-preview",
			[]string{path + ", of telemetry prefix 46d3xbcp.res.568937f", "stating no version"}},
	} {
		for _, telemetry := range []string{"true", "false"} {
			name := row.version + " telemetry " + telemetry
			g, diags := readTemplate(deploying(deployer("server", layer(t, "db-for-postgre-sql/flexible-server",
				row.version), postgres+`, "enableTelemetry": {"value": `+telemetry+`}`)))
			if g == nil || !slices.Equal(ids(g), []string{"server"}) {
				t.Errorf("%s: nodes %v, diagnostics %+v", name, ids(g), diags)
				continue
			}
			n := g.Nodes[0]
			if n.Form != cloudimport.FormAVM || n.Type != "Microsoft.DBforPostgreSQL/flexibleServers" ||
				n.APIVersion != row.apiVersion || n.Module == nil || *n.Module != row.want {
				t.Errorf("%s: the node reads form %s, type %s, API version %s, module %+v", name, n.Form, n.Type,
					n.APIVersion, n.Module)
			}
			if !reflect.DeepEqual(n.Name, str("psql-orders")) {
				t.Errorf("%s: the server is named %+v", name, n.Name)
			}
			want := []string{"administratorLogin", "availabilityZone", "databases", "location", "name", "skuName", "tier"}
			if !slices.Equal(bodyNames(n), want) {
				t.Errorf("%s: the body holds %v, want %v", name, bodyNames(n), want)
			}
			if got := at(t, n, "administratorLogin"); !reflect.DeepEqual(got, cloudimport.Param{Name: "adminLogin"}) {
				t.Errorf("%s: the administrator's login reads %+v", name, got)
			}
			if got := at(t, n, "databases", "0", "name"); !reflect.DeepEqual(got, str("orders")) {
				t.Errorf("%s: the database reads %+v", name, got)
			}
			if len(diags) != 1 || diags[0].Code != string(cloudimport.CodeAVMModuleRecognised) ||
				diags[0].Severity != aggrega.SeverityInfo || diags[0].SourceRef != "main.json#/resources/0" ||
				!strings.Contains(diags[0].Message, `named "DBforPostgreSQL Flexible Servers" in its metadata`) {
				t.Errorf("%s: diagnostics %+v", name, diags)
				continue
			}
			for _, says := range row.says {
				if !strings.Contains(diags[0].Message, says) {
					t.Errorf("%s: %q does not say %q", name, diags[0].Message, says)
				}
			}
		}
	}
}

// TestAVMChildModulesStayInsideTheModule deploys the published PostgreSQL
// flexible server module whose own child modules each send telemetry under a
// prefix the module index lists, stating no version: the module is one node,
// and no child module is a node or recognised. Read as the root template, the
// same module's child modules are recognised one by one, and no resource
// sending telemetry is a node at any depth, in a module kept one node or in
// one read as its resources.
func TestAVMChildModulesStayInsideTheModule(t *testing.T) {
	t.Parallel()
	module := layer(t, "db-for-postgre-sql/flexible-server", "0.16.2")
	g, diags := readTemplate(deploying(deployer("server", module, postgres)))
	if g == nil || !slices.Equal(ids(g), []string{"server"}) {
		t.Fatalf("nodes %v, diagnostics %+v", ids(g), diags)
	}
	recognised := withCode(diags, cloudimport.CodeAVMModuleRecognised)
	if len(diags) != 1 || len(recognised) != 1 || !strings.Contains(recognised[0].Message, "46d3xbcp.res.568937f") {
		t.Errorf("diagnostics %+v, want the one module recognised", diags)
	}
	if unpinned := withCode(diags, cloudimport.CodeModuleVersionUnpinned); len(unpinned) != 0 {
		t.Errorf("a child module is reported unpinned: %+v", unpinned)
	}

	g, diags = readAll("", src("main.json", module))
	if g == nil {
		t.Fatalf("the module read as the root: diagnostics %+v", diags)
	}
	for _, n := range g.Nodes {
		if n.Type == deploymentsType || strings.HasSuffix(n.ID, "avmTelemetry") {
			t.Errorf("%s is a %s node", n.ID, n.Type)
		}
	}
	databases := node(t, g, "flexibleServer_databases")
	if want := (cloudimport.ModuleRef{Path: "avm/res/db-for-postgre-sql/flexible-server/database",
		Prefix: "46d3xbcp.res.a5f6742"}); databases.Form != cloudimport.FormAVM || databases.Module == nil ||
		*databases.Module != want || databases.Type != "Microsoft.DBforPostgreSQL/flexibleServers/databases" {
		t.Errorf("the databases module reads form %s, type %s, module %+v", databases.Form, databases.Type,
			databases.Module)
	}
	if pe := node(t, g, "server_privateEndpoints/privateEndpoint"); pe.Form != cloudimport.FormRaw ||
		pe.Type != "Microsoft.Network/privateEndpoints" {
		t.Errorf("the private endpoint module's resource reads form %s, type %s", pe.Form, pe.Type)
	}
	if root := withCode(diags, cloudimport.CodeAVMModuleRecognised); len(root) == 0 || root[0].SourceRef != "main.json" {
		t.Errorf("the root template is not reported as the module it is: %+v", root)
	}
}

// twoOwn is a module built by Bicep under a readable telemetry prefix the
// module index does not list, creating a plan and a site of its own beside a
// lock.
const twoOwn = `{` + schema + `, "languageVersion": "2.0", "contentVersion": "1.0.0.0",
	"metadata": {"_generator": {"name": "bicep", "version": "0.30.23.60470", "templateHash": "1"}, "name": "Web Site"},
	"parameters": {"name": {"type": "string"}, "enableTelemetry": {"type": "bool", "defaultValue": true}},
	"resources": {
		"avmTelemetry": {"condition": "[parameters('enableTelemetry')]", "type": "Microsoft.Resources/deployments",
			"apiVersion": "2024-03-01",
			"name": "[format('46d3xbcp.res.web-site.{0}.{1}', replace('0.3.0', '.', '-'), substring(uniqueString(parameters('name')), 0, 4))]",
			"properties": {"mode": "Incremental", "template": {` + schema + `, "contentVersion": "1.0.0.0", "resources": []}}},
		"plan": {"type": "Microsoft.Web/serverfarms", "apiVersion": "2023-01-01", "name": "[format('{0}-plan', parameters('name'))]"},
		"site": {"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "[parameters('name')]",
			"dependsOn": ["plan"]},
		"site_lock": {"type": "Microsoft.Authorization/locks", "apiVersion": "2020-05-01", "name": "lock",
			"scope": "[resourceId('Microsoft.Web/sites', parameters('name'))]", "properties": {"level": "CanNotDelete"}}}}`

// TestAVMResourceTypeFromTheIndexElseItsOneResource deploys a module whose
// hashed prefix the module index lists, which takes the index's resource type
// and is one node though no row maps it; one whose readable prefix it does
// not list, which takes the type of its one resource of its own; and one
// creating two resources of its own, which is read as its resources, with an
// avm-module-recognised saying why.
func TestAVMResourceTypeFromTheIndexElseItsOneResource(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(deploying(deployer("cluster", layer(t, "kusto/cluster", "0.11.1"),
		`"name": {"value": "adx-core"}, "sku": {"value": "Standard_E2ads_v5"}`)))
	if g == nil || !slices.Equal(ids(g), []string{"cluster"}) || len(diags) != 1 {
		t.Fatalf("the cluster: nodes %v, diagnostics %+v", ids(g), diags)
	}
	cluster := g.Nodes[0]
	if want := (cloudimport.ModuleRef{Path: "avm/res/kusto/cluster", Version: "0.11.1",
		Prefix: "46d3xbcp.res.07a6150"}); cluster.Type != "Microsoft.Kusto/clusters" ||
		cluster.APIVersion != "2024-04-13" || cluster.Module == nil || *cluster.Module != want ||
		!reflect.DeepEqual(cluster.Name, str("adx-core")) {
		t.Errorf("the cluster reads type %s, API version %s, module %+v, name %+v", cluster.Type, cluster.APIVersion,
			cluster.Module, cluster.Name)
	}

	g, diags = readTemplate(deploying(deployer("server", layer(t, "db-for-postgre-sql/flexible-server", "0.9.1"),
		postgres)))
	if g == nil || len(g.Nodes) != 1 || g.Nodes[0].Type != "Microsoft.DBforPostgreSQL/flexibleServers" ||
		g.Nodes[0].APIVersion != "2024-08-01" {
		t.Errorf("a module the index does not list: nodes %v, diagnostics %+v", ids(g), diags)
	}

	g, diags = readTemplate(deploying(deployer("web", []byte(twoOwn), `"name": {"value": "app-orders"}`)))
	if want := []string{"web/plan", "web/site", "web/site_lock"}; g == nil || !slices.Equal(ids(g), want) {
		t.Fatalf("a module of two resources: nodes %v, want %v; diagnostics %+v", ids(g), want, diags)
	}
	if site := node(t, g, "web/site"); !reflect.DeepEqual(site.Name, str("app-orders")) {
		t.Errorf("the site is named %+v", site.Name)
	}
	recognised := withCode(diags, cloudimport.CodeAVMModuleRecognised)
	if len(recognised) != 1 || recognised[0].SourceRef != "main.json#/resources/0" ||
		!strings.Contains(recognised[0].Message, "46d3xbcp.res.web-site") ||
		!strings.Contains(recognised[0].Message, "the module creates 2 resources of its own") {
		t.Errorf("a module of two resources: diagnostics %+v", diags)
	}
}

// TestLegacyModuleNoRowMapsIsFlattened deploys the published Azure Maps
// account module, whose readable telemetry prefix the module index does not
// list and whose resource type no row maps: it is read as the resources it
// deploys, each drafted from the source, its telemetry no node, with an
// avm-module-recognised saying why.
func TestLegacyModuleNoRowMapsIsFlattened(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(deploying(deployer("maps", layer(t, "maps/account", "0.2.2"),
		`"name": {"value": "maps-core"}, "location": {"value": "global"}, "enableTelemetry": {"value": false}`)))
	if g == nil {
		t.Fatalf("diagnostics %+v", diags)
	}
	account := node(t, g, "maps/mapsAccount")
	if account.Form != cloudimport.FormRaw || account.Type != "Microsoft.Maps/accounts" ||
		!reflect.DeepEqual(account.Name, str("maps-core")) {
		t.Errorf("the account reads form %s, type %s, name %+v", account.Form, account.Type, account.Name)
	}
	for _, n := range g.Nodes {
		if !strings.HasPrefix(n.ID, "maps/") || n.Type == deploymentsType {
			t.Errorf("%s, of type %s, is a node", n.ID, n.Type)
		}
	}
	recognised := withCode(diags, cloudimport.CodeAVMModuleRecognised)
	if len(recognised) != 1 || recognised[0].Severity != aggrega.SeverityInfo ||
		recognised[0].SourceRef != "main.json#/resources/0" ||
		!strings.Contains(recognised[0].Message, "46d3xbcp.res.maps-account") ||
		!strings.Contains(recognised[0].Message, "version 0.2.2") ||
		!strings.Contains(recognised[0].Message, "no Recipe of the catalog maps Microsoft.Maps/accounts") {
		t.Errorf("diagnostics %+v", diags)
	}
}

// TestAVMPatternAndUtilityModulesAreFlattened deploys a pattern module
// holding a resource module and a resource of its own, and a utility module
// computing an output: each is read as what it deploys, its telemetry no
// node, with an avm-module-recognised naming its kind; the resource module
// inside the pattern module is recognised on its own, its parameters read
// through both modules; and the utility module's output is the value it
// computes.
func TestAVMPatternAndUtilityModulesAreFlattened(t *testing.T) {
	t.Parallel()
	telemetry := func(prefix string) string {
		return `"avmTelemetry": {"condition": "[parameters('enableTelemetry')]", "type": "Microsoft.Resources/deployments",
			"apiVersion": "2024-03-01",
			"name": "[format('` + prefix + `.{0}.{1}', replace('0.4.0', '.', '-'), substring(uniqueString(parameters('name')), 0, 4))]",
			"properties": {"mode": "Incremental", "template": {` + schema + `, "contentVersion": "1.0.0.0", "resources": []}}}`
	}
	module := func(title, resources, outputs string) string {
		return `{` + schema + `, "languageVersion": "2.0", "contentVersion": "1.0.0.0",
			"metadata": {"_generator": {"name": "bicep", "version": "0.30.23.60470", "templateHash": "1"}, "name": "` + title + `"},
			"parameters": {"name": {"type": "string"}, "adminLogin": {"type": "string", "defaultValue": ""},
				"enableTelemetry": {"type": "bool", "defaultValue": true}},
			"resources": {` + resources + `}, "outputs": {` + outputs + `}}`
	}
	stack := module("App Stack", telemetry("46d3xbcp.ptn.app-stack")+`,
		"plan": {"type": "Microsoft.Web/serverfarms", "apiVersion": "2023-01-01", "name": "[format('{0}-plan', parameters('name'))]"},
		"db": `+deployer("[format('{0}-db', parameters('name'))]", layer(t, "db-for-postgre-sql/flexible-server", "0.16.2"),
		`"name": {"value": "[format('{0}-db', parameters('name'))]"}, "administratorLogin": {"value": "[parameters('adminLogin')]"},
			"skuName": {"value": "Standard_B1ms"}, "tier": {"value": "Burstable"}, "availabilityZone": {"value": -1}`), ``)
	naming := module("Naming", telemetry("46d3xbcp.utl.naming"), `"name": {"type": "string",
		"value": "[format('{0}-core', parameters('name'))]"}`)
	g, diags := readTemplate(deploying(
		deployer("stack", []byte(stack), `"name": {"value": "orders"}, "adminLogin": {"value": "[parameters('adminLogin')]"}`),
		deployer("naming", []byte(naming), `"name": {"value": "app"}`),
		`{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web",
			"properties": {"appName": "[reference('naming').outputs.name.value]"}}`))
	if want := []string{"stack/db", "stack/plan", "web"}; g == nil || !slices.Equal(ids(g), want) {
		t.Fatalf("nodes %v, want %v; diagnostics %+v", ids(g), want, diags)
	}
	db := node(t, g, "stack/db")
	if db.Form != cloudimport.FormAVM || db.Module == nil || db.Module.Path != "avm/res/db-for-postgre-sql/flexible-server" ||
		!reflect.DeepEqual(db.Name, str("orders-db")) {
		t.Errorf("the pattern's server reads form %s, module %+v, name %+v", db.Form, db.Module, db.Name)
	}
	if got := at(t, db, "administratorLogin"); !reflect.DeepEqual(got, cloudimport.Param{Name: "adminLogin"}) {
		t.Errorf("the pattern's server's login reads %+v, want the root template's parameter", got)
	}
	if got := at(t, node(t, g, "web"), "properties", "appName"); !reflect.DeepEqual(got, str("app-core")) {
		t.Errorf("the utility's output reads %+v", got)
	}
	recognised := withCode(diags, cloudimport.CodeAVMModuleRecognised)
	var refs []string
	for _, d := range recognised {
		refs = append(refs, d.SourceRef)
	}
	if want := []string{"main.json#/resources/0", "main.json#/resources/0/properties/template/resources/db",
		"main.json#/resources/1"}; !slices.Equal(refs, want) || len(diags) != len(recognised) {
		t.Fatalf("diagnostics %+v, want one avm-module-recognised at each of %v", diags, want)
	}
	for i, says := range []string{"pattern module of telemetry prefix 46d3xbcp.ptn.app-stack",
		"resource module avm/res/db-for-postgre-sql/flexible-server", "utility module of telemetry prefix 46d3xbcp.utl.naming"} {
		if !strings.Contains(recognised[i].Message, says) {
			t.Errorf("%q does not say %q", recognised[i].Message, says)
		}
	}
}

// TestAVMFoldedParametersAreNotCarried deploys a module setting its lock,
// role assignments, diagnostic settings and private endpoints: each is left
// out of the node, with a resource-not-carried warning naming it, unless the
// override header of a row the node may take lists it, and one set to
// nothing is left out unreported.
func TestAVMFoldedParametersAreNotCarried(t *testing.T) {
	t.Parallel()
	args := `"name": {"value": "adx-core"}, "sku": {"value": "Standard_E2ads_v5"},
		"lock": {"value": {"kind": "CanNotDelete"}},
		"roleAssignments": {"value": [{"principalId": "11111111-1111-1111-1111-111111111111", "roleDefinitionIdOrName": "Reader"}]},
		"diagnosticSettings": {"value": [{"workspaceResourceId": "/subscriptions/x/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/logs"}]},
		"privateEndpoints": {"value": [{"subnetResourceId": "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pe"}]}`
	source := deploying(deployer("cluster", layer(t, "kusto/cluster", "0.11.1"), args))
	listing := cloudimport.Table{Hand: []cloudimport.Row{{Key: "Microsoft.Kusto/clusters", Component: "core/bigdata/data-explorer",
		Category: "bigdata", Recipe: "data-explorer", Form: cloudimport.FormAVM, Origin: "hand",
		Overrides: []cloudimport.Placement{{From: []string{"lock"}, Key: "lock"}}}}}
	for _, row := range []struct {
		name    string
		table   cloudimport.Table
		body    []string
		dropped []string
		says    string
	}{
		{"no row", Table(), []string{"name", "sku"},
			[]string{"lock", "roleAssignments", "diagnosticSettings", "privateEndpoints"},
			"no Recipe of the catalog maps the module"},
		{"a row listing the lock", listing, []string{"lock", "name", "sku"},
			[]string{"roleAssignments", "diagnosticSettings", "privateEndpoints"},
			"no Recipe it may map to lists it among the overrides it reads"},
	} {
		g, diags := read(aggrega.ImportInput{Format: formatName, Sources: []aggrega.ImportSource{src("main.json",
			[]byte(source))}}, cloudimport.Settings{}, parse, row.table)
		if g == nil || len(g.Nodes) != 1 {
			t.Errorf("%s: nodes %v, diagnostics %+v", row.name, ids(g), diags)
			continue
		}
		if !slices.Equal(bodyNames(g.Nodes[0]), row.body) {
			t.Errorf("%s: the body holds %v, want %v", row.name, bodyNames(g.Nodes[0]), row.body)
		}
		notCarried := withCode(diags, cloudimport.CodeResourceNotCarried)
		if len(notCarried) != len(row.dropped) || len(diags) != len(row.dropped)+1 {
			t.Errorf("%s: diagnostics %+v, want one resource-not-carried for each of %v", row.name, diags, row.dropped)
			continue
		}
		for i, param := range row.dropped {
			d := notCarried[i]
			if d.SourceRef != "main.json#/resources/0/properties/parameters/"+param ||
				!strings.Contains(d.Message, "sets the parameter "+param+" of") || !strings.Contains(d.Message, row.says) {
				t.Errorf("%s: %s reported %+v", row.name, param, d)
			}
		}
	}

	g, diags := readTemplate(deploying(deployer("cluster", layer(t, "kusto/cluster", "0.11.1"),
		`"name": {"value": "adx-core"}, "sku": {"value": "Standard_E2ads_v5"}, "roleAssignments": {"value": []}`)))
	if g == nil || len(g.Nodes) != 1 || !slices.Equal(bodyNames(g.Nodes[0]), []string{"name", "sku"}) ||
		len(withCode(diags, cloudimport.CodeResourceNotCarried)) != 0 {
		t.Errorf("role assignments set to nothing: nodes %v, diagnostics %+v", ids(g), diags)
	}
}
