// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// schema is the $schema of a resource group template.
const schema = `"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"`

// ids returns the ID of each node of g, in order.
func ids(g *cloudimport.Graph) []string {
	if g == nil {
		return nil
	}
	out := make([]string, len(g.Nodes))
	for i, n := range g.Nodes {
		out[i] = n.ID
	}
	return out
}

// withCode returns the diagnostics of diags whose code is code.
func withCode(diags []aggrega.Diagnostic, code cloudimport.Code) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, d := range diags {
		if d.Code == string(code) {
			out = append(out, d)
		}
	}
	return out
}

// network is a 1.0 template held inline by a Microsoft.Resources/deployments
// resource and evaluated in its own scope: a virtual network named by the
// parameter vnetName, its address prefix a parameter with a default, its tags
// a parameter of type object, and a subnet of it; it outputs the subnet's
// identifier.
const network = `{` + schema + `, "contentVersion": "1.0.0.0",
	"parameters": {"vnetName": {"type": "string"}, "prefix": {"type": "string", "defaultValue": "10.0.0.0/16"},
		"tags": {"type": "object"}},
	"resources": [
		{"type": "Microsoft.Network/virtualNetworks", "apiVersion": "2023-09-01", "name": "[parameters('vnetName')]",
			"tags": "[parameters('tags')]",
			"properties": {"addressSpace": {"addressPrefixes": ["[parameters('prefix')]"]}}},
		{"type": "Microsoft.Network/virtualNetworks/subnets", "apiVersion": "2023-09-01",
			"name": "[concat(parameters('vnetName'), '/apps')]",
			"dependsOn": ["[resourceId('Microsoft.Network/virtualNetworks', parameters('vnetName'))]"]}],
	"outputs": {"subnetId": {"type": "string",
		"value": "[resourceId('Microsoft.Network/virtualNetworks/subnets', parameters('vnetName'), 'apps')]"}}}`

// TestFlattenInnerResourcesJoinTheGraph reads a template deploying a template
// held inline: its resources are nodes of the graph, each symbol behind the
// deploying resource's and a slash, and the deploying resource is no node.
// The inner template's parameters read the values the deploying resource
// gives them, lowered in the outer template, else their defaults; the outer
// template's reference to the inner template's output reads the value the
// output holds; and a dependency on the deploying resource is one on each
// node its template yields.
func TestFlattenInnerResourcesJoinTheGraph(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(`{` + schema + `, "contentVersion": "1.0.0.0",
		"parameters": {"name": {"type": "string"}, "env": {"type": "string"}},
		"resources": [
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "net",
				"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
					"parameters": {"vnetName": {"value": "[concat(parameters('name'), '-vnet')]"},
						"tags": {"value": {"env": "[parameters('env')]"}}},
					"template": ` + network + `}},
			{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web", "dependsOn": ["net"],
				"properties": {"subnet": "[reference('net').outputs.subnetId.value]"}}]}`)
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	if want := []string{"net/apps", "net/vnet", "web"}; !slices.Equal(ids(g), want) {
		t.Fatalf("nodes %v, want %v", ids(g), want)
	}
	vnet := node(t, g, "net/vnet")
	if want := (cloudimport.Compose{Parts: []cloudimport.Value{cloudimport.Param{Name: "name"}, str("-vnet")}}); !reflect.DeepEqual(vnet.Name, want) {
		t.Errorf("the network is named %+v, want %+v", vnet.Name, want)
	}
	if vnet.Source != "main.json#/resources/0/properties/template/resources/0" {
		t.Errorf("the network is read from %s", vnet.Source)
	}
	if got := at(t, vnet, "tags", "env"); !reflect.DeepEqual(got, cloudimport.Param{Name: "env"}) {
		t.Errorf("the network's tag reads %+v, want the outer parameter env", got)
	}
	if got := at(t, vnet, "properties", "addressSpace", "addressPrefixes", "0"); !reflect.DeepEqual(got,
		str("10.0.0.0/16")) {
		t.Errorf("the address prefix reads %+v, want the parameter's default", got)
	}
	subnet := node(t, g, "net/apps")
	if subnet.Parent != "net/vnet" || !slices.Equal(subnet.DependsOn, []string{"net/vnet"}) {
		t.Errorf("the subnet reads parent %q, depends on %v", subnet.Parent, subnet.DependsOn)
	}
	web := node(t, g, "web")
	if got := at(t, web, "properties", "subnet"); !reflect.DeepEqual(got, cloudimport.Ref{Node: "net/apps"}) {
		t.Errorf("the output read reads %+v, want the subnet's identifier", got)
	}
	if want := []string{"net/vnet", "net/apps"}; !slices.Equal(web.DependsOn, want) {
		t.Errorf("web depends on %v, want %v", web.DependsOn, want)
	}
}

// TestFlattenOuterScopeResolvesInTheOuterTemplate reads templates held inline
// that are evaluated in the outer scope — under language version 1.0, by
// default and by name — whose expressions read the outer template's
// parameters and variables, the inner template's own declarations and the
// value the deploying resource gives unread. One evaluated in its own scope —
// by name, or by default where the holding or the nested template is of
// language version 2.0, under which outer is not admitted — reads its own and
// the value given.
func TestFlattenOuterScopeResolvesInTheOuterTemplate(t *testing.T) {
	t.Parallel()
	outer := cloudimport.Compose{Parts: []cloudimport.Value{str("app-"), cloudimport.Param{Name: "name"}}}
	inner := str("inner-bound")
	nested := func(options, version string) string {
		resources := `[{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01",
			"name": "[concat(variables('prefix'), '-', parameters('name'))]"}]`
		if version != "" {
			resources = `{"site": {"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01",
				"name": "[concat(variables('prefix'), '-', parameters('name'))]"}}`
		}
		return `"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "nest",
			"properties": {"mode": "Incremental", ` + options + `
				"parameters": {"name": {"value": "bound"}},
				"template": {` + schema + `, ` + version + ` "contentVersion": "1.0.0.0",
					"parameters": {"name": {"type": "string", "defaultValue": "inner-default"}},
					"variables": {"prefix": "inner"},
					"resources": ` + resources + `}}`
	}
	twoZeroHolding := func(resource string) string {
		return `{` + schema + `, "languageVersion": "2.0", "contentVersion": "1.0.0.0",
			"parameters": {"name": {"type": "string"}}, "variables": {"prefix": "app"},
			"resources": {"nest": {` + resource + `}}}`
	}
	for _, row := range []struct {
		name, source string
		want         cloudimport.Value
	}{
		{"1.0 in 1.0, by default", oneZero(`{"prefix": "app"}`, `{`+nested(``, ``)+`}`), outer},
		{"1.0 in 1.0, outer", oneZero(`{"prefix": "app"}`,
			`{`+nested(`"expressionEvaluationOptions": {"scope": "outer"},`, ``)+`}`), outer},
		{"1.0 in 1.0, inner", oneZero(`{"prefix": "app"}`,
			`{`+nested(`"expressionEvaluationOptions": {"scope": "inner"},`, ``)+`}`), inner},
		{"2.0 in 1.0, by default", oneZero(`{"prefix": "app"}`, `{`+nested(``, `"languageVersion": "2.0",`)+`}`),
			inner},
		{"1.0 in 2.0, by default", twoZeroHolding(nested(``, ``)), inner},
		{"2.0 in 2.0, outer", twoZeroHolding(nested(`"expressionEvaluationOptions": {"scope": "outer"},`,
			`"languageVersion": "2.0",`)), inner},
	} {
		g, diags := readTemplate(row.source)
		if g == nil || len(diags) != 0 {
			t.Errorf("%s: diagnostics %+v", row.name, diags)
			continue
		}
		if len(g.Nodes) != 1 || !strings.HasPrefix(g.Nodes[0].ID, "nest/") {
			t.Errorf("%s: nodes %v, want the inner site alone", row.name, ids(g))
			continue
		}
		if !reflect.DeepEqual(g.Nodes[0].Name, row.want) {
			t.Errorf("%s: the site is named %+v, want %+v", row.name, g.Nodes[0].Name, row.want)
		}
	}
}

// TestDeploymentScopeMergedNamesTheAnnotation reads templates deployed into
// another resource group, into another subscription, and into the template's
// own resource group, one apiece: each is flattened, its nodes taking the
// scope as report text; the first two are reported deployment-scope-merged,
// naming the annotation a boundary of their own would take, and the third
// is not.
func TestDeploymentScopeMergedNamesTheAnnotation(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		scope, says, nodeScope string
	}{
		{`"resourceGroup": "rg-net",`, "the resource group 'rg-net'", "resource group rg-net"},
		{`"subscriptionId": "00000000-0000-0000-0000-000000000001",`,
			"the subscription '00000000-0000-0000-0000-000000000001'",
			"subscription 00000000-0000-0000-0000-000000000001"},
		{`"resourceGroup": "[resourceGroup().name]",`, "", ""},
	} {
		g, diags := readTemplate(oneZero("", `
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "net", `+row.scope+`
				"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
					"template": {`+schema+`, "contentVersion": "1.0.0.0", "resources": [
						{"type": "Microsoft.Network/virtualNetworks", "apiVersion": "2023-09-01", "name": "vnet"}]}}}`))
		if g == nil {
			t.Errorf("%s: no graph: %+v", row.scope, diags)
			continue
		}
		vnet := node(t, g, "net/vnet")
		if vnet.Scope != row.nodeScope {
			t.Errorf("%s: the network's scope reads %q, want %q", row.scope, vnet.Scope, row.nodeScope)
		}
		merged := withCode(diags, cloudimport.CodeDeploymentScopeMerged)
		if row.says == "" {
			if len(diags) != 0 {
				t.Errorf("%s: diagnostics %+v, want none", row.scope, diags)
			}
			continue
		}
		if len(diags) != 1 || len(merged) != 1 || merged[0].Severity != aggrega.SeverityWarn ||
			merged[0].SourceRef != "main.json#/resources/0" || !strings.Contains(merged[0].Message, row.says) ||
			!strings.Contains(merged[0].Message, "aggrega.ai/azure-resource-group") {
			t.Errorf("%s: diagnostics %+v", row.scope, diags)
		}
	}

	cluster := strings.Replace(deployer("cluster", layer(t, "kusto/cluster", "0.11.1"),
		`"name": {"value": "adx-core"}, "sku": {"value": "Standard_E2ads_v5"}`), `"name": "cluster",`,
		`"name": "cluster", "resourceGroup": "rg-other",`, 1)
	g, diags := readTemplate(deploying(cluster))
	if g == nil || !slices.Equal(ids(g), []string{"cluster"}) || g.Nodes[0].Form != cloudimport.FormAVM ||
		g.Nodes[0].Scope != "resource group rg-other" {
		t.Fatalf("a module kept one node in another resource group: nodes %v, diagnostics %+v", ids(g), diags)
	}
	merged := withCode(diags, cloudimport.CodeDeploymentScopeMerged)
	if len(merged) != 1 || merged[0].SourceRef != "main.json#/resources/0" ||
		!strings.Contains(merged[0].Message, "the resource group 'rg-other'") ||
		!strings.Contains(merged[0].Message, "aggrega.ai/azure-resource-group") {
		t.Errorf("a module kept one node in another resource group: diagnostics %+v", diags)
	}
}

// TestFlattenALinkedTemplateJoinsTheGraph reads a template linking another by
// a relative path the import holds: the linked template's resources join the
// graph behind the linking resource's symbol, the output the root template
// reads is the value the output holds, and a dependency on the linking
// resource is one on each node the linked template yields.
func TestFlattenALinkedTemplateJoinsTheGraph(t *testing.T) {
	t.Parallel()
	g, diags := readAll("", src("main.json", fixture(t, "linked/main.json")),
		src("modules/network.json", fixture(t, "linked/modules/network.json")))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	if want := []string{"network/vnet-core", "vaults"}; !slices.Equal(ids(g), want) {
		t.Fatalf("nodes %v, want %v", ids(g), want)
	}
	if vnet := node(t, g, "network/vnet-core"); vnet.Source != "modules/network.json#/resources/0" {
		t.Errorf("the linked network is read from %s", vnet.Source)
	}
	vault := node(t, g, "vaults")
	rule := at(t, vault, "properties", "networkAcls", "virtualNetworkRules", "0", "id")
	want := cloudimport.Ref{Node: "resourceId('Microsoft.Network/virtualNetworks/subnets', 'vnet-core', 'snet-app')"}
	if !reflect.DeepEqual(rule, want) {
		t.Errorf("the output the vault reads is %+v, want %+v", rule, want)
	}
	if !slices.Equal(vault.DependsOn, []string{"network/vnet-core"}) {
		t.Errorf("the vault depends on %v", vault.DependsOn)
	}
}

// TestNestedConditionsAndDependenciesReachTheInnerResources reads a template
// deployed under a condition and after another resource: each inner resource
// takes the deploying resource's condition beside its own, their and, and its
// dependencies beside its own; a deploying resource known never deployed
// makes its resources never deployed.
func TestNestedConditionsAndDependenciesReachTheInnerResources(t *testing.T) {
	t.Parallel()
	nested := func(name, condition string) string {
		return `{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "` + name + `",
			"condition": ` + condition + `, "dependsOn": ["store"],
			"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
				"parameters": {"mode": {"value": "on"}},
				"template": {` + schema + `, "contentVersion": "1.0.0.0",
					"parameters": {"mode": {"type": "string"}},
					"resources": [
						{"type": "Microsoft.Web/serverfarms", "apiVersion": "2023-01-01", "name": "plan"},
						{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "site",
							"condition": "[equals(parameters('mode'), 'on')]", "dependsOn": ["plan"]}]}}}`
	}
	g, diags := readTemplate(`{` + schema + `, "contentVersion": "1.0.0.0",
		"parameters": {"deployNet": {"type": "bool"}},
		"resources": [
			{"type": "Microsoft.Storage/storageAccounts", "apiVersion": "2023-01-01", "name": "store"},
			` + nested("app", `"[parameters('deployNet')]"`) + `,
			` + nested("off", `false`) + `]}`)
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	plan, site := node(t, g, "app/plan"), node(t, g, "app/site")
	if !reflect.DeepEqual(plan.Condition, cloudimport.Param{Name: "deployNet"}) {
		t.Errorf("the plan's condition reads %+v, want the deploying resource's", plan.Condition)
	}
	if want := (cloudimport.Opaque{Text: "[and(parameters('deployNet'), equals('on', 'on'))]", Target: "arm"}); !reflect.DeepEqual(site.Condition, want) {
		t.Errorf("the site's condition reads %+v, want %+v", site.Condition, want)
	}
	if !slices.Equal(plan.DependsOn, []string{"store"}) || !slices.Equal(site.DependsOn, []string{"app/plan", "store"}) {
		t.Errorf("the plan depends on %v, the site on %v", plan.DependsOn, site.DependsOn)
	}
	for _, id := range []string{"off/plan", "off/site"} {
		if n := node(t, g, id); !reflect.DeepEqual(n.Condition, lit("false")) {
			t.Errorf("%s's condition reads %+v, want false", id, n.Condition)
		}
	}
}

// TestNestedCopyRepeatsTheInnerResources reads templates deployed by copy
// loops: a literal count repeats each inner resource once per index, the
// deploying resource's copyIndex() read where its values are lowered; a count
// the template computes repeats it as many times as it computes, unexpanded.
func TestNestedCopyRepeatsTheInnerResources(t *testing.T) {
	t.Parallel()
	nested := func(count string) string {
		return twoZero(`"nets": {"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01",
			"name": "[format('nets-{0}', copyIndex())]", "copy": {"name": "netLoop", "count": ` + count + `},
			"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
				"parameters": {"vnetName": {"value": "[format('vnet-{0}', copyIndex())]"}},
				"template": {` + schema + `, "languageVersion": "2.0", "contentVersion": "1.0.0.0",
					"parameters": {"vnetName": {"type": "string"}},
					"resources": {"vnet": {"type": "Microsoft.Network/virtualNetworks", "apiVersion": "2023-09-01",
						"name": "[parameters('vnetName')]"}}}}}`)
	}
	g, diags := readTemplate(nested("2"))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	vnet := node(t, g, "nets/vnet")
	if vnet.Repeat == nil || !slices.Equal(vnet.Repeat.Keys, []string{"0", "1"}) {
		t.Fatalf("the network repeats %+v, want once per index", vnet.Repeat)
	}
	if inst := vnet.Repeat.Instance("1"); inst == nil || !reflect.DeepEqual(inst.Name, str("vnet-1")) {
		t.Errorf("the second network reads %+v", inst)
	}
	g, diags = readTemplate(nested(`"[parameters('count')]"`))
	if g == nil || len(diags) != 0 {
		t.Fatalf("a computed count: diagnostics %+v", diags)
	}
	if vnet := node(t, g, "nets/vnet"); vnet.Repeat == nil || vnet.Repeat.Keys != nil {
		t.Errorf("a computed count repeats %+v, want a repetition of no keys", vnet.Repeat)
	}
}

// TestNestedExistingResourceNamesTheOneTheOuterTemplateCreates reads a nested
// template that reads as existing the server the outer template creates, by a
// name the deploying resource gives it: the existing resource is no node, and
// its child names the outer server as its parent and dependency.
func TestNestedExistingResourceNamesTheOneTheOuterTemplateCreates(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(twoZero(`
		"server": {"type": "Microsoft.DBforPostgreSQL/flexibleServers", "apiVersion": "2024-08-01",
			"name": "[parameters('name')]"},
		"dbs": {"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "dbs",
			"dependsOn": ["server"],
			"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
				"parameters": {"serverName": {"value": "[parameters('name')]"}},
				"template": {` + schema + `, "languageVersion": "2.0", "contentVersion": "1.0.0.0",
					"parameters": {"serverName": {"type": "string"}},
					"resources": {
						"server": {"existing": true, "type": "Microsoft.DBforPostgreSQL/flexibleServers",
							"apiVersion": "2024-08-01", "name": "[parameters('serverName')]"},
						"db": {"type": "Microsoft.DBforPostgreSQL/flexibleServers/databases", "apiVersion": "2024-08-01",
							"name": "[format('{0}/{1}', parameters('serverName'), 'orders')]", "dependsOn": ["server"]}}}}}`))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	if want := []string{"dbs/db", "server"}; !slices.Equal(ids(g), want) {
		t.Fatalf("nodes %v, want %v", ids(g), want)
	}
	if db := node(t, g, "dbs/db"); db.Parent != "server" || !slices.Equal(db.DependsOn, []string{"server"}) {
		t.Errorf("the database reads parent %q, depends on %v", db.Parent, db.DependsOn)
	}
}

// TestNestedParameterNeitherGivenNorDefaultedIsNotCarried reads a nested
// template whose parameter the deploying resource gives no value and which
// declares no default: what reads it is not carried, with a value-not-carried
// warning naming the parameter's declaration.
func TestNestedParameterNeitherGivenNorDefaultedIsNotCarried(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "nest",
			"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
				"template": {`+schema+`, "contentVersion": "1.0.0.0", "parameters": {"sku": {"type": "string"}},
					"resources": [{"type": "Microsoft.Web/serverfarms", "apiVersion": "2023-01-01", "name": "plan",
						"sku": {"name": "[parameters('sku')]"}}]}}}`))
	if g == nil || len(diags) != 1 || diags[0].Code != string(cloudimport.CodeValueNotCarried) ||
		diags[0].SourceRef != "main.json#/resources/0/properties/template/parameters/sku" ||
		!strings.Contains(diags[0].Message, "the parameter sku with no default") {
		t.Fatalf("diagnostics %+v", diags)
	}
	if got := at(t, node(t, g, "nest/plan"), "sku", "name"); got != nil {
		t.Errorf("the sku reads %+v, want nothing carried", got)
	}
}

// TestNestedKeyVaultReferenceIsACredential reads a nested template whose
// parameter the deploying resource gives as a Key Vault reference: what reads
// it is the credential of the secret the reference names, read from the vault.
func TestNestedKeyVaultReferenceIsACredential(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "nest",
			"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
				"parameters": {"password": {"reference": {
					"keyVault": {"id": "[resourceId('Microsoft.KeyVault/vaults', 'kv-core')]"}, "secretName": "db-password"}}},
				"template": {`+schema+`, "contentVersion": "1.0.0.0",
					"parameters": {"password": {"type": "securestring"}},
					"resources": [{"type": "Microsoft.DBforPostgreSQL/flexibleServers", "apiVersion": "2024-08-01",
						"name": "pg", "properties": {"administratorLoginPassword": "[parameters('password')]"}}]}}}`))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	want := cloudimport.Credential{Node: "resourceId('Microsoft.KeyVault/vaults', 'kv-core')",
		What: "Key Vault secret db-password"}
	if got := at(t, node(t, g, "nest/pg"), "properties", "administratorLoginPassword"); !reflect.DeepEqual(got, want) {
		t.Errorf("the password reads %+v, want %+v", got, want)
	}
}

// TestNestedTemplatesTheImportCannotReadAreNotFlattened reads, one fault
// apiece, a nested template of an experimental language version, which halts
// source-refused naming where it is written, and a linked template linking
// back to the template linking it, which is read once and reported
// linked-source-not-read where it links back.
func TestNestedTemplatesTheImportCannotReadAreNotFlattened(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "nest",
			"properties": {"mode": "Incremental", "template": {"languageVersion": "2.1-experimental",
				"resources": {}}}}`))
	if g != nil || len(diags) != 1 || diags[0].Code != string(cloudimport.CodeSourceRefused) ||
		diags[0].SourceRef != "main.json#/resources/0/properties/template" ||
		!strings.Contains(diags[0].Message, "languageVersion") {
		t.Errorf("an experimental nested template: graph %v, diagnostics %+v", g != nil, diags)
	}

	link := func(name, to string) string {
		return oneZero("", `{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "`+name+`",
			"properties": {"mode": "Incremental", "templateLink": {"relativePath": "`+to+`"}}},
			{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "site-`+name+`"}`)
	}
	g, diags = readAll("a.json", src("a.json", []byte(link("tob", "b.json"))), src("b.json", []byte(link("toa", "a.json"))))
	if g == nil || len(diags) != 1 || diags[0].Code != string(cloudimport.CodeLinkedSourceNotRead) ||
		diags[0].SourceRef != "b.json#/resources/0" {
		t.Fatalf("templates linking each other: diagnostics %+v", diags)
	}
	if want := []string{"site-tob", "tob/site-toa", "tob/toa"}; !slices.Equal(ids(g), want) {
		t.Errorf("nodes %v, want %v", ids(g), want)
	}
}

// TestNestedReadsAreDeterministic reads templates nesting templates and
// modules — a module read as the root, a module kept one node, a module read
// as its resources, a linked template — twice each, and requires one graph and
// one list of diagnostics.
func TestNestedReadsAreDeterministic(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		sources []aggrega.ImportSource
	}{
		{"a module as the root", []aggrega.ImportSource{src("main.json",
			layer(t, "db-for-postgre-sql/flexible-server", "0.16.2"))}},
		{"a module kept one node", []aggrega.ImportSource{src("main.json", []byte(deploying(deployer("server",
			layer(t, "db-for-postgre-sql/flexible-server", "0.9.1"), postgres))))}},
		{"a module read as its resources", []aggrega.ImportSource{src("main.json", []byte(deploying(
			deployer("maps", layer(t, "maps/account", "0.2.2"), `"name": {"value": "maps-core"}`))))}},
		{"a linked template", []aggrega.ImportSource{src("main.json", fixture(t, "linked/main.json")),
			src("modules/network.json", fixture(t, "linked/modules/network.json"))}},
	} {
		g1, d1 := readAll("", row.sources...)
		g2, d2 := readAll("", row.sources...)
		if g1 == nil || len(g1.Nodes) == 0 {
			t.Errorf("%s reads no node: %+v", row.name, d1)
		}
		if dump(g1) != dump(g2) || !reflect.DeepEqual(d1, d2) {
			t.Errorf("%s reads differently twice", row.name)
		}
	}
}

// TestFlattenedResourcesAreNoNodes reads what other templates read of a
// resource whose template is flattened: an output a sibling template reads —
// one evaluated in the outer scope, and one reading the secure outputs — is
// the value the output holds; the resource's identifier, read by reference,
// resourceId or resourceInfo, and its credential read whole, are no reference
// to a node, each with a value-not-carried warning, the identifier kept as
// deploy-time text.
func TestFlattenedResourcesAreNoNodes(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "net",
			"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
				"template": {`+schema+`, "contentVersion": "1.0.0.0", "resources": [
					{"type": "Microsoft.Network/virtualNetworks", "apiVersion": "2023-09-01", "name": "vnet"}],
					"outputs": {"vnetId": {"type": "string",
						"value": "[resourceId('Microsoft.Network/virtualNetworks', 'vnet')]"}}}}},
		{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "reader",
			"properties": {"mode": "Incremental", "template": {`+schema+`, "contentVersion": "1.0.0.0", "resources": [
				{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "site",
					"properties": {"vnet": "[reference('net').outputs.vnetId.value]",
						"secure": "[listOutputsWithSecureValues('net', '2022-09-01').vnetId]"}}]}}},
		{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web", "properties": {
			"byReference": "[reference('net').id]",
			"byResourceId": "[resourceId('Microsoft.Resources/deployments', 'net')]",
			"whole": "[listOutputsWithSecureValues('net', '2022-09-01')]"}}`))
	if g == nil {
		t.Fatalf("diagnostics %+v", diags)
	}
	if want := []string{"net/vnet", "reader/site", "web"}; !slices.Equal(ids(g), want) {
		t.Fatalf("nodes %v, want %v", ids(g), want)
	}
	site := node(t, g, "reader/site")
	for _, path := range []string{"vnet", "secure"} {
		if got := at(t, site, "properties", path); !reflect.DeepEqual(got, cloudimport.Ref{Node: "net/vnet"}) {
			t.Errorf("the sibling's %s reads %+v, want the network's identifier", path, got)
		}
	}
	web := node(t, g, "web")
	for _, row := range []struct {
		path string
		want cloudimport.Value
	}{
		{"byReference", cloudimport.Opaque{Text: "[reference('net').id]", Target: "arm"}},
		{"byResourceId", cloudimport.Opaque{Text: "[resourceId('Microsoft.Resources/deployments', 'net')]",
			Target: "arm"}},
		{"whole", nil},
	} {
		if got := at(t, web, "properties", row.path); !reflect.DeepEqual(got, row.want) {
			t.Errorf("%s reads %+v, want %+v", row.path, got, row.want)
		}
	}
	notCarried := withCode(diags, cloudimport.CodeValueNotCarried)
	if len(diags) != 3 || len(notCarried) != 3 {
		t.Fatalf("diagnostics %+v, want three value-not-carried", diags)
	}
	for i, says := range []string{"the identifier of net", "the identifier of net", "a credential of net"} {
		if notCarried[i].SourceRef != "main.json#/resources/2" || !strings.Contains(notCarried[i].Message, says) {
			t.Errorf("diagnostic %d is %+v, want it to say %q", i, notCarried[i], says)
		}
	}

	g, diags = readTemplate(twoZero(`
		"net": {"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "net",
			"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
				"template": {` + schema + `, "contentVersion": "1.0.0.0", "resources": []}}},
		"web": {"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web",
			"properties": {"byInfo": "[resourceInfo('net').id]"}}`))
	if got := at(t, node(t, g, "web"), "properties", "byInfo"); !reflect.DeepEqual(got,
		cloudimport.Opaque{Text: "[resourceInfo('net').id]", Target: "arm"}) || len(diags) != 1 ||
		!strings.Contains(diags[0].Message, "the identifier of net") {
		t.Errorf("resourceInfo of a flattened resource reads %+v; diagnostics %+v", got, diags)
	}
}

// TestNestedOutputsComposingEachOtherAreBounded reads thirty nested templates,
// each output composing the output of the template it deploys twice over:
// each output is lowered once, and the read ends promptly, the outputs past
// the size the draft carries not carried, with a value-not-carried warning.
func TestNestedOutputsComposingEachOtherAreBounded(t *testing.T) {
	t.Parallel()
	const levels = 30
	template := `{` + schema + `, "contentVersion": "1.0.0.0", "resources": [],
		"outputs": {"x": {"type": "string", "value": "abcd"}}}`
	for i := 0; i < levels; i++ {
		template = `{` + schema + `, "contentVersion": "1.0.0.0", "resources": [
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "d",
				"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
					"template": ` + template + `}}],
			"outputs": {"x": {"type": "string",
				"value": "[concat(reference('d').outputs.x.value, reference('d').outputs.x.value)]"}}}`
	}
	source := oneZero("", `{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "top",
		"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
			"template": `+template+`}},
		{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web",
			"properties": {"x": "[reference('top').outputs.x.value]"}}`)
	start := time.Now()
	g, diags := readTemplate(source)
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the read took %s", elapsed)
	}
	if g == nil {
		t.Fatalf("diagnostics %+v", diags)
	}
	if got := at(t, node(t, g, "web"), "properties", "x"); got != nil {
		t.Errorf("the outermost output reads a value of %d bytes, want none carried", size(got))
	}
	notCarried := withCode(diags, cloudimport.CodeValueNotCarried)
	if len(notCarried) != 1 || !strings.Contains(notCarried[0].Message, "larger than") {
		t.Errorf("diagnostics %+v, want the one output past the size the draft carries", diags)
	}
}

// TestNestedParametersComposingEachOtherAreBounded reads thirty nested
// templates, each giving the template it deploys a parameter composing its
// own parameter twice over: each parameter is lowered once, and the read ends
// promptly, the values past the size the draft carries not carried, with a
// value-not-carried warning.
func TestNestedParametersComposingEachOtherAreBounded(t *testing.T) {
	t.Parallel()
	const levels = 30
	template := `{` + schema + `, "contentVersion": "1.0.0.0", "parameters": {"p": {"type": "string"}},
		"resources": [{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "site",
			"properties": {"p": "[parameters('p')]"}}]}`
	for i := 0; i < levels; i++ {
		template = `{` + schema + `, "contentVersion": "1.0.0.0", "parameters": {"p": {"type": "string"}},
			"resources": [{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "d",
				"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
					"parameters": {"p": {"value": "[concat(parameters('p'), parameters('p'))]"}},
					"template": ` + template + `}}]}`
	}
	start := time.Now()
	g, diags := readTemplate(oneZero("", `{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01",
		"name": "top", "properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
			"parameters": {"p": {"value": "abcd"}}, "template": `+template+`}}`))
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the read took %s", elapsed)
	}
	if g == nil || len(g.Nodes) != 1 {
		t.Fatalf("nodes %v, diagnostics %+v", ids(g), diags)
	}
	if got := at(t, g.Nodes[0], "properties", "p"); got != nil {
		t.Errorf("the innermost parameter reads a value of %d bytes, want none carried", size(got))
	}
	notCarried := withCode(diags, cloudimport.CodeValueNotCarried)
	if len(notCarried) != 1 || !strings.Contains(notCarried[0].Message, "larger than") {
		t.Errorf("diagnostics %+v, want the one parameter past the size the draft carries", diags)
	}
}
