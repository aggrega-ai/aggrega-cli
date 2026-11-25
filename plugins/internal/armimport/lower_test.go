// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// lowered reads a 1.0 template declaring variables and one web site whose
// properties hold the values given, as JSON object members, and returns its
// node and the diagnostics.
func lowered(t *testing.T, variables, properties string) (*cloudimport.Node, []string) {
	t.Helper()
	g, diags := readTemplate(oneZero(variables, `
		{"type": "Microsoft.Storage/storageAccounts", "apiVersion": "2023-01-01", "name": "stcore"},
		{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "inner",
			"properties": {"mode": "Incremental", "template": {"resources": [], "outputs": {"endpoint": {
				"type": "string", "value": "[reference('stcore').primaryEndpoints.web]"}, "password": {
				"type": "securestring", "value": "[listKeys('stcore', '2023-01-01').keys[0].value]"}}}}},
		{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web", "properties": {`+properties+`}}`))
	if g == nil {
		t.Fatalf("no graph: %+v", diags)
	}
	return node(t, g, "web"), codes(diags)
}

// TestEscapedBracketIsALiteral reads a string opening with a doubled bracket
// as the literal text without the first bracket, never as an expression.
func TestEscapedBracketIsALiteral(t *testing.T) {
	t.Parallel()
	web, diags := lowered(t, "", `"a": "[[x]", "b": "[[parameters('name')]", "c": "[x"`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	for path, want := range map[string]cloudimport.Value{"a": str("[x]"), "b": str("[parameters('name')]"),
		"c": str("[x")} {
		if got := at(t, web, "properties", path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s reads %+v, want %+v", path, got, want)
		}
	}
}

// TestLowerLiteralsParametersAndVariables reads literals as they are, a
// parameter by its declared name whatever case reads it, and a variable as
// what it holds, followed through other variables; a variable reading itself
// is deploy-time text.
func TestLowerLiteralsParametersAndVariables(t *testing.T) {
	t.Parallel()
	web, diags := lowered(t, `{"prefix": "app", "full": "[concat(variables('prefix'), '-', parameters('Name'))]",
		"tags": {"team": "core"}, "loopA": "[variables('loopB')]", "loopB": "[variables('loopA')]",
		"computed": {"owner": "[parameters('name')]"}}`,
		`"number": 3, "flag": true, "nothing": null, "empty": {}, "none": [],
		"param": "[parameters('NAME')]", "full": "[variables('full')]", "tags": "[variables('tags')]",
		"cycle": "[variables('loopA')]", "computed": "[variables('computed')]", "member": "[variables('computed').owner]"`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	for _, row := range []struct {
		path []string
		want cloudimport.Value
	}{
		{[]string{"number"}, lit("3")},
		{[]string{"flag"}, lit("true")},
		{[]string{"nothing"}, lit("null")},
		{[]string{"empty"}, lit("{}")},
		{[]string{"none"}, lit("[]")},
		{[]string{"param"}, cloudimport.Param{Name: "name"}},
		{[]string{"full"}, cloudimport.Compose{Parts: []cloudimport.Value{str("app-"), cloudimport.Param{Name: "name"}}}},
		{[]string{"tags", "team"}, str("core")},
		{[]string{"cycle"}, cloudimport.Opaque{Text: "[variables('loopA')]", Target: "arm"}},
		{[]string{"computed", "owner"}, cloudimport.Param{Name: "name"}},
		{[]string{"member"}, cloudimport.Param{Name: "name"}},
	} {
		if got := at(t, web, append([]string{"properties"}, row.path...)...); !reflect.DeepEqual(got, row.want) {
			t.Errorf("%v reads %+v, want %+v", row.path, got, row.want)
		}
	}
}

// TestLowerConcatAndFormatCompose reads concat and format of text into
// composed text, the literal text joined, and either joining arrays, or format
// with a specifier, as deploy-time text.
func TestLowerConcatAndFormatCompose(t *testing.T) {
	t.Parallel()
	web, diags := lowered(t, "", `"concat": "[concat('https://', parameters('name'), '.example.com')]",
		"format": "[format('https://{0}.example.com:{1}/{{x}}', parameters('name'), 443)]",
		"arrays": "[concat(createArray('a'), createArray('b'))]",
		"specifier": "[format('{0:N2}', parameters('count'))]",
		"literal": "[concat('a', 'b')]"`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	name := cloudimport.Param{Name: "name"}
	for _, row := range []struct {
		path string
		want cloudimport.Value
	}{
		{"concat", cloudimport.Compose{Parts: []cloudimport.Value{str("https://"), name, str(".example.com")}}},
		{"format", cloudimport.Compose{Parts: []cloudimport.Value{str("https://"), name, str(".example.com:443/{x}")}}},
		{"arrays", cloudimport.Opaque{Text: "[concat(createArray('a'), createArray('b'))]", Target: "arm"}},
		{"specifier", cloudimport.Opaque{Text: "[format('{0:N2}', parameters('count'))]", Target: "arm"}},
		{"literal", str("ab")},
	} {
		if got := at(t, web, "properties", row.path); !reflect.DeepEqual(got, row.want) {
			t.Errorf("%s reads %+v, want %+v", row.path, got, row.want)
		}
	}
}

// TestLowerReferencesNameAResourceAndAnAttribute reads reference,
// resourceInfo and resourceId as references: reference reads below the
// resource's properties unless asked for the full resource, the outputs of a
// Microsoft.Resources/deployments resource below its properties — the value
// the output holds, where the template it deploys is read — and an id or a
// resourceId is the resource's identifier.
func TestLowerReferencesNameAResourceAndAnAttribute(t *testing.T) {
	t.Parallel()
	web, diags := lowered(t, "", `
		"endpoint": "[reference(resourceId('Microsoft.Storage/storageAccounts', 'stcore')).primaryEndpoints.blob]",
		"byName": "[reference('stcore').primaryEndpoints['blob']]",
		"full": "[reference(resourceId('Microsoft.Storage/storageAccounts', 'stcore'), '2023-01-01', 'Full').identity.principalId]",
		"id": "[reference('stcore').id]",
		"resourceId": "[resourceId('Microsoft.Storage/storageAccounts', 'stcore')]",
		"output": "[reference('inner').outputs.endpoint.value]",
		"fullOutput": "[reference('inner', '2022-09-01', 'Full').properties.outputs.endpoint.value]",
		"whole": "[reference('inner', '2022-09-01', 'Full')]",
		"computed": "[reference(parameters('name')).primaryEndpoints.blob]"`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	ref := func(node string, attr ...string) cloudimport.Ref { return cloudimport.Ref{Node: node, Attr: attr} }
	for _, row := range []struct {
		path string
		want cloudimport.Value
	}{
		{"endpoint", ref("stcore", "properties", "primaryEndpoints", "blob")},
		{"byName", ref("stcore", "properties", "primaryEndpoints", "blob")},
		{"full", ref("stcore", "identity", "principalId")},
		{"id", ref("stcore")},
		{"resourceId", ref("stcore")},
		{"output", ref("stcore", "properties", "primaryEndpoints", "web")},
		{"fullOutput", ref("stcore", "properties", "primaryEndpoints", "web")},
		{"whole", cloudimport.Opaque{Text: "[reference('inner', '2022-09-01', 'Full')]", Target: "arm"}},
		{"computed", cloudimport.Opaque{Text: "[reference(parameters('name')).primaryEndpoints.blob]", Target: "arm"}},
	} {
		if got := at(t, web, "properties", row.path); !reflect.DeepEqual(got, row.want) {
			t.Errorf("%s reads %+v, want %+v", row.path, got, row.want)
		}
	}
}

// TestLowerResourceInfoNamesASymbol reads resourceInfo in a 2.0 template: its
// id is the resource's identifier, another member that attribute.
func TestLowerResourceInfoNamesASymbol(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(twoZero(`
		"store": {"type": "Microsoft.Storage/storageAccounts", "apiVersion": "2023-01-01", "name": "stcore"},
		"web": {"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web",
			"properties": {"id": "[resourceInfo('store').id]", "name": "[resourceInfo('store').name]"}}`))
	if len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	web := node(t, g, "web")
	if got := at(t, web, "properties", "id"); !reflect.DeepEqual(got, cloudimport.Ref{Node: "store"}) {
		t.Errorf("the id reads %+v", got)
	}
	if got := at(t, web, "properties", "name"); !reflect.DeepEqual(got, cloudimport.Ref{Node: "store",
		Attr: []string{"name"}}) {
		t.Errorf("the name reads %+v", got)
	}
}

// TestLowerListFunctionsAreCredentials reads each list function as the
// credential it reads from its resource, also where another call holds it; a
// secure output of a template read as its resources is the credential the
// output holds.
func TestLowerListFunctionsAreCredentials(t *testing.T) {
	t.Parallel()
	web, diags := lowered(t, "", `
		"key": "[listKeys(resourceId('Microsoft.Storage/storageAccounts', 'stcore'), '2023-01-01').keys[0].value]",
		"outputs": "[listOutputsWithSecureValues('inner', '2022-09-01').password]",
		"nested": "[first(listKeys(resourceId('Microsoft.Storage/storageAccounts', 'stcore'), '2023-01-01').keys).value]",
		"connection": "[concat('AccountKey=', listKeys('stcore', '2023-01-01').keys[0].value)]"`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	keys := cloudimport.Credential{Node: "stcore", What: "listKeys"}
	for _, row := range []struct {
		path string
		want cloudimport.Value
	}{
		{"key", keys},
		{"outputs", keys},
		{"nested", keys},
		{"connection", cloudimport.Compose{Parts: []cloudimport.Value{str("AccountKey="), keys}}},
	} {
		if got := at(t, web, "properties", row.path); !reflect.DeepEqual(got, row.want) {
			t.Errorf("%s reads %+v, want %+v", row.path, got, row.want)
		}
	}
}

// TestLowerOtherCallsAreOpaque reads every other call as deploy-time text for
// the arm target, printed canonically with the variables it reads written in
// place.
func TestLowerOtherCallsAreOpaque(t *testing.T) {
	t.Parallel()
	web, diags := lowered(t, `{"seed": "[resourceGroup().id]"}`, `
		"location": "[resourceGroup().location]",
		"suffix": "[uniqueString( variables('seed') )]",
		"guid": "[newGuid()]",
		"tenant": "[subscription().tenantId]"`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	for path, want := range map[string]string{"location": "[resourceGroup().location]",
		"suffix": "[uniqueString(resourceGroup().id)]", "guid": "[newGuid()]", "tenant": "[subscription().tenantId]"} {
		if got := at(t, web, "properties", path); !reflect.DeepEqual(got, cloudimport.Opaque{Text: want, Target: "arm"}) {
			t.Errorf("%s reads %+v, want the deploy-time text %s", path, got, want)
		}
	}
}

// TestLowerAStringThatDoesNotParseIsNotCarried reads a string that reads as
// an expression and does not parse: it is not carried, with a
// value-not-carried warning naming where it is written, and the read goes on.
func TestLowerAStringThatDoesNotParseIsNotCarried(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web",
		"properties": {"broken": "[concat('a', ]", "kept": "x"}}`))
	if g == nil || len(diags) != 1 || diags[0].Code != "value-not-carried" || diags[0].Severity != "warn" ||
		diags[0].SourceRef != "main.json#/resources/0/properties/broken" ||
		!strings.Contains(diags[0].Message, "does not parse as one (expected a function call, a string or an integer at offset 13)") {
		t.Fatalf("diagnostics %+v", diags)
	}
	web := node(t, g, "web")
	if got := at(t, web, "properties", "broken"); got != nil {
		t.Errorf("the broken expression is carried as %+v", got)
	}
	if got := at(t, web, "properties", "kept"); !reflect.DeepEqual(got, str("x")) {
		t.Errorf("the property beside it reads %+v", got)
	}
}

// TestLowerCopyLoopsRepeat reads a resource copy loop of a literal count as
// one instance per index, copyIndex read as the index, and of a computed
// count as a repetition holding no keys; a property copy loop of a literal
// count is the property it names, one item per index.
func TestLowerCopyLoopsRepeat(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "Microsoft.Network/networkInterfaces", "apiVersion": "2023-09-01",
			"name": "[concat('nic-', copyIndex(1))]", "copy": {"name": "nics", "count": 2},
			"properties": {"ipConfigurations": [{"name": "[concat('ip', copyIndex())]"}]}},
		{"type": "Microsoft.Network/publicIPAddresses", "apiVersion": "2023-09-01",
			"name": "[concat('pip-', copyIndex())]", "copy": {"name": "pips", "count": "[parameters('count')]"}},
		{"type": "Microsoft.Compute/disks", "apiVersion": "2023-04-02", "name": "data",
			"dependsOn": ["nics"],
			"properties": {"copy": [{"name": "parts", "count": 2, "input": {"lun": "[copyIndex('parts')]"}}]}}`))
	if len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	nic := node(t, g, "networkinterfaces")
	if nic.Repeat == nil || !slices.Equal(nic.Repeat.Keys, []string{"0", "1"}) {
		t.Fatalf("the interfaces repeat %+v", nic.Repeat)
	}
	inst := nic.Repeat.Instance("1")
	if !reflect.DeepEqual(inst.Name, str("nic-2")) || !reflect.DeepEqual(at(t, inst, "properties", "ipConfigurations",
		"0", "name"), str("ip1")) {
		t.Errorf("the second interface reads name %+v", inst.Name)
	}
	if pip := node(t, g, "publicipaddresses"); pip.Repeat == nil || pip.Repeat.Keys != nil {
		t.Errorf("the computed loop repeats %+v, want no keys", pip.Repeat)
	}
	disk := node(t, g, "data")
	if !slices.Equal(disk.DependsOn, []string{"networkinterfaces"}) {
		t.Errorf("the disk depends on %v, want the interfaces its loop name names", disk.DependsOn)
	}
	if got := at(t, disk, "properties", "parts", "1", "lun"); !reflect.DeepEqual(got, lit("1")) {
		t.Errorf("the second part reads %+v", got)
	}
}

// TestLowerAListCallBehindAVariableIsACredential reads a list function reached
// only through a variable inside another call: a variable holding the call as
// text, and one holding it in an object. Each value is the credential the call
// reads, never deploy-time text carrying it.
func TestLowerAListCallBehindAVariableIsACredential(t *testing.T) {
	t.Parallel()
	web, diags := lowered(t, `{
		"key": "[listKeys(resourceId('Microsoft.Storage/storageAccounts', 'stcore'), '2023-01-01').keys[0].value]",
		"settings": {"connection": "[concat('AccountKey=', listKeys('stcore', '2023-01-01').keys[0].value)]"}}`,
		`"chosen": "[if(true(), variables('key'), 'none')]",
		"merged": "[union(variables('settings'), createObject('x', 'y'))]"`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %v", diags)
	}
	keys := cloudimport.Credential{Node: "stcore", What: "listKeys"}
	for _, path := range []string{"chosen", "merged"} {
		if got := at(t, web, "properties", path); !reflect.DeepEqual(got, keys) {
			t.Errorf("%s reads %+v, want %+v", path, got, keys)
		}
	}
}
