// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// goldenView is the catalog view the goldens import against: each Component
// a row of the arm table names, labelled as its row labels it, so a resource a
// row maps references the Component the row names.
func goldenView(t *testing.T) *aggrega.CatalogProjection {
	t.Helper()
	rows, err := Table().Rows()
	if err != nil {
		t.Fatal(err)
	}
	var out []aggrega.CatalogComponent
	for _, r := range rows {
		if slices.ContainsFunc(out, func(c aggrega.CatalogComponent) bool { return c.Name == r.Component }) {
			continue
		}
		labels := map[string]string{"component.aggrega.ai/category": r.Category}
		for k, v := range r.Traits {
			labels[k] = v
		}
		out = append(out, aggrega.CatalogComponent{Name: r.Component, Version: "1.0.0", Labels: labels})
	}
	slices.SortFunc(out, func(a, b aggrega.CatalogComponent) int { return strings.Compare(a.Name, b.Name) })
	return &aggrega.CatalogProjection{Components: out}
}

// avmLayer is a member standing for the compiled template of an Azure
// Verified Modules module a golden source deploys, written in its place from
// testdata/avm.
var avmLayer = regexp.MustCompile(`\{\s*"\$avm":\s*"([^"]+)"\s*\}`)

// goldenSources returns the sources of testdata/golden/<name>/source, by name,
// each module a $avm member names written in its place.
func goldenSources(t *testing.T, name string) []aggrega.ImportSource {
	t.Helper()
	dir := filepath.Join("testdata", "golden", name, "source")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []aggrega.ImportSource
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		raw = avmLayer.ReplaceAllFunc(raw, func(m []byte) []byte {
			return fixture(t, "avm/"+string(avmLayer.FindSubmatch(m)[1])+"/main.json")
		})
		out = append(out, src(e.Name(), raw))
	}
	return out
}

// goldenFiles returns what the golden of out holds: each file the import
// writes by its path, its diagnostics in diagnostics.txt and its decisions in
// decisions.txt.
func goldenFiles(out aggrega.ImportOutput) map[string]string {
	files := map[string]string{}
	for _, f := range out.Files {
		files[f.Path] = f.Content
	}
	var diags strings.Builder
	for _, d := range out.Diagnostics {
		fmt.Fprintf(&diags, "%s %s: %s\n", d.Severity, d.Code, d.Message)
	}
	files["diagnostics.txt"] = diags.String()
	var decisions strings.Builder
	for _, d := range out.Decisions {
		chosen := "nothing"
		if d.Chosen != nil {
			chosen = d.Chosen.Target + " (" + d.Chosen.Mode + ")"
		}
		fmt.Fprintf(&decisions, "%s (%s) -> %s: %s\n", d.SourceNode.ID, d.SourceNode.Type, chosen, d.Rationale)
	}
	files["decisions.txt"] = decisions.String()
	return files
}

// importGolden imports the sources of testdata/golden/<name>/source for the
// arm target against goldenView, twice, failing unless both runs give one
// output, and compares each file the import writes, its diagnostics and its
// decisions with those under testdata/golden/<name>/want, no file missing and
// none left over. Run the tests with UPDATE_GOLDEN=1 to write them there
// instead.
func importGolden(t *testing.T, name string) aggrega.ImportOutput {
	t.Helper()
	in := aggrega.ImportInput{Format: formatName, Target: target, Sources: goldenSources(t, name),
		Catalog: goldenView(t)}
	out := Import(in, parse)
	if again := Import(in, parse); fmt.Sprint(goldenFiles(again)) != fmt.Sprint(goldenFiles(out)) {
		t.Fatalf("two imports of %s differ", name)
	}
	got := goldenFiles(out)
	dir := filepath.Join("testdata", "golden", name, "want")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		for path, content := range got {
			file := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	held := map[string]bool{}
	err := filepath.WalkDir(dir, func(file string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		path := filepath.ToSlash(rel)
		held[path] = true
		want, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		content, written := got[path]
		switch {
		case !written:
			t.Errorf("%s holds %s, which the import does not write (run with UPDATE_GOLDEN=1 to rewrite it)", dir,
				path)
		case content != string(want):
			t.Errorf("%s differs from its golden in %s (run with UPDATE_GOLDEN=1 to rewrite it):\n%s", path, dir,
				content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the golden %s: %v (run with UPDATE_GOLDEN=1 to write it)", dir, err)
	}
	for path := range got {
		if !held[path] {
			t.Errorf("the import writes %s, which %s does not hold (run with UPDATE_GOLDEN=1 to write it)", path, dir)
		}
	}
	return out
}

// fileText returns the content of the file of path out writes, failing where
// it writes none.
func fileText(t *testing.T, out aggrega.ImportOutput, path string) string {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("the import writes no %s", path)
	return ""
}

// holds fails unless text holds each of parts.
func holds(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("%s does not hold %q:\n%s", what, part, text)
		}
	}
}

// coded returns the diagnostics of out of code.
func coded(out aggrega.ImportOutput, code cloudimport.Code) []aggrega.Diagnostic {
	var found []aggrega.Diagnostic
	for _, d := range out.Diagnostics {
		if d.Code == string(code) {
			found = append(found, d)
		}
	}
	return found
}

// decisionOn returns the decision on the node id, failing where out holds
// none.
func decisionOn(t *testing.T, out aggrega.ImportOutput, id string) aggrega.ImportDecision {
	t.Helper()
	for _, d := range out.Decisions {
		if d.SourceNode.ID == id {
			return d
		}
	}
	t.Fatalf("the import decides nothing on %s", id)
	return aggrega.ImportDecision{}
}

// TestArmImportGoldenRawTopology imports a PostgreSQL flexible server holding
// a database nested in its resources and a container app reading the server's
// address into its environment: the server references the PostgreSQL
// Component, the database is folded into its values under its own name, the
// app references the server Component and takes the address off the server's
// host on its connection, the subnet the server reads is left for the operator
// to name, and the virtual network the template creates is substrate.
func TestArmImportGoldenRawTopology(t *testing.T) {
	out := importGolden(t, "raw-topology")
	aggregate := fileText(t, out, "aggregate.yaml")
	holds(t, "the Aggregate", aggregate, "type: core/datastore/postgres", `database: "appdb"`,
		"type: core/compute/server", "- in: \"DB_HOST\"\n                  from: host")
	if strings.Contains(aggregate, "/appdb") {
		t.Errorf("the Aggregate writes the database's full name:\n%s", aggregate)
	}
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, `image: "ghcr.io/example/web:1.4.2"`, "port: 8080",
		`engine_version: "16"`, "delegated_subnet_id: \"\"",
		"[resourceId('Microsoft.Network/virtualNetworks/subnets', 'vnet-core', 'snet-db')]")
	if len(coded(out, cloudimport.CodeSubstrateLeftToOperator)) != 1 {
		t.Errorf("the virtual network is not reported substrate: %+v", out.Diagnostics)
	}
}

// TestArmImportGoldenAVMTopology imports the topology of the raw golden with
// the server deployed through the Azure Verified Modules module: one node of
// the module form references the PostgreSQL Component, its parameters
// translated, its database its values', its telemetry not carried, and the
// app takes the module's fqdn output off the server's host.
func TestArmImportGoldenAVMTopology(t *testing.T) {
	out := importGolden(t, "avm-topology")
	aggregate := fileText(t, out, "aggregate.yaml")
	holds(t, "the Aggregate", aggregate, "type: core/datastore/postgres", `database: "appdb"`,
		"- in: \"DB_HOST\"\n                  from: host")
	if strings.Contains(fileText(t, out, "release.yaml"), "enableTelemetry") {
		t.Errorf("the Release carries the module's telemetry")
	}
	if len(coded(out, cloudimport.CodeAVMModuleRecognised)) != 1 {
		t.Errorf("the module is not reported recognised: %+v", out.Diagnostics)
	}
}

// TestArmImportGoldenDraftedFromSource drafts a resource no row maps from the
// source, as a Component and a Recipe of its own, and a deployment script
// whose decision says when its script runs, reading the mapped vault's
// address off its provides.
func TestArmImportGoldenDraftedFromSource(t *testing.T) {
	out := importGolden(t, "drafted-from-source")
	if len(coded(out, cloudimport.CodeResourceDraftedFromSource)) != 2 {
		t.Errorf("the site and the script are not both drafted from the source: %+v", out.Diagnostics)
	}
	holds(t, "the script's decision", decisionOn(t, out, "seed-vault").Rationale, scriptSays)
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "type: core/security/azure-key-vault",
		"from: endpoint")
}

// TestArmImportGoldenWrappedModule drafts an Azure Verified Modules module no
// row maps as a Recipe sourcing the module whole from the Microsoft Container
// Registry, at the version it states, its telemetry off, providing the
// module's output a container app reads, which the app's connection delivers
// into its environment.
func TestArmImportGoldenWrappedModule(t *testing.T) {
	out := importGolden(t, "wrapped-module")
	var recipe string
	for _, f := range out.Files {
		if strings.HasPrefix(f.Path, "catalog/recipe/arm/") {
			recipe = f.Content
		}
	}
	holds(t, "the wrapped module's Recipe", recipe, "kind: bicep-module-oci",
		"url: oci://mcr.microsoft.com/bicep/avm/res/kusto/cluster", "0.11.1", "enableTelemetry: false",
		"outputs_resource_id_value: \"[reference('{{ .locals.symbol }}').outputs.resourceId.value]\"")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "- in: \"CLUSTER_ID\"")
}

// containerAppModule is a template in the shape Bicep compiles an Azure
// Verified Modules container app module to, written for these tests: Bicep's
// metadata, the module's parameters, the container app, and the telemetry
// resource named by the prefix the module index lists for
// avm/res/app/container-app at version 0.23.1.
const containerAppModule = `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
	"contentVersion": "1.0.0.0",
	"metadata": {"_generator": {"name": "bicep", "version": "0.38.33.27573"}, "name": "Container Apps"},
	"parameters": {"name": {"type": "string"}, "location": {"type": "string", "defaultValue": "[resourceGroup().location]"},
		"environmentResourceId": {"type": "string"}, "containers": {"type": "array"},
		"disableIngress": {"type": "bool", "defaultValue": false}, "enableTelemetry": {"type": "bool", "defaultValue": true}},
	"resources": [
		{"condition": "[parameters('enableTelemetry')]", "type": "Microsoft.Resources/deployments",
			"apiVersion": "2024-03-01",
			"name": "[format('46d3xbcp.res.b757ba7.{0}.{1}', replace('0.23.1', '.', '-'), substring(uniqueString(deployment().name, parameters('location')), 0, 4))]",
			"properties": {"mode": "Incremental", "template": {"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
				"contentVersion": "1.0.0.0", "resources": []}}},
		{"type": "Microsoft.App/containerApps", "apiVersion": "2025-01-01", "name": "[parameters('name')]",
			"location": "[parameters('location')]",
			"properties": {"environmentId": "[parameters('environmentResourceId')]",
				"template": {"containers": "[parameters('containers')]"}}}
	]}`

// TestArmContainerAppModuleIsUndecidedAndWrapped imports a container app
// deployed through the Azure Verified Modules module, which states its
// ingress by parameters neither container app row reads: the import warns
// discriminator-unresolved, naming the server and the worker Components the
// app could take, and drafts it as a Recipe sourcing the module whole.
func TestArmContainerAppModuleIsUndecidedAndWrapped(t *testing.T) {
	template := deploying(deployer("app", []byte(containerAppModule), `"name": {"value": "web"},
		"environmentResourceId": {"value": "/subscriptions/s/resourceGroups/g/providers/Microsoft.App/managedEnvironments/e"},
		"containers": {"value": [{"name": "web", "image": "ghcr.io/example/web:1.0.0"}]},
		"disableIngress": {"value": true}`))
	out := Import(aggrega.ImportInput{Format: formatName, Target: target, Catalog: goldenView(t),
		Sources: []aggrega.ImportSource{src("main.json", []byte(template))}}, parse)
	unresolved := coded(out, cloudimport.CodeDiscriminatorUnresolved)
	if len(unresolved) != 1 {
		t.Fatalf("the module is warned discriminator-unresolved %d times, want once: %+v", len(unresolved),
			out.Diagnostics)
	}
	holds(t, "the warning", unresolved[0].Message, "core/compute/server", "core/compute/worker")
	if d := decisionOn(t, out, "app"); d.Chosen == nil || d.Chosen.Mode != "draft" {
		t.Errorf("the module is decided %+v, want drafted", d.Chosen)
	}
	var recipe string
	for _, f := range out.Files {
		if strings.HasPrefix(f.Path, "catalog/recipe/arm/") {
			recipe = f.Content
		}
	}
	holds(t, "the wrapped Recipe", recipe, "kind: bicep-module-oci",
		"url: oci://mcr.microsoft.com/bicep/avm/res/app/container-app", "version: 0.23.1", "enableTelemetry: false")
}

// TestArmImportGoldenExisting reads resources the template does not create:
// the instance the catalog maps leaves each pointer to them for the operator,
// naming the template's resourceId expression, and the instance drafted from
// the source holds that expression whole.
func TestArmImportGoldenExisting(t *testing.T) {
	out := importGolden(t, "existing")
	release := fileText(t, out, "release.yaml")
	holds(t, "the Release", release, "delegated_subnet_id: \"\"", "workspace_id: \"\"",
		`sourceId: "[resourceId('Microsoft.OperationalInsights/workspaces', 'log-shared')]"`)
	for _, id := range []string{"subnet", "workspace"} {
		if decisionOn(t, out, id).Rationale == "" {
			t.Errorf("the decision on %s gives no rationale", id)
		}
	}
}

// TestArmImportGoldenCopy writes a resource repeated a literal number of
// times as one instance per copy, and one repeated as many times as a
// parameter says as one instance, warning repetition-not-expanded.
func TestArmImportGoldenCopy(t *testing.T) {
	out := importGolden(t, "copy")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "    storageaccounts-0:\n",
		"    storageaccounts-1:\n", "    namespaces:\n")
	if len(coded(out, cloudimport.CodeRepetitionNotExpanded)) != 1 {
		t.Errorf("the namespaces' repetition is not reported: %+v", out.Diagnostics)
	}
}

// TestArmImportGoldenCondition writes a resource deployed where one boolean
// parameter is true as an instance active where that parameter is, and one
// deployed under any other condition active, warning condition-not-carried.
func TestArmImportGoldenCondition(t *testing.T) {
	out := importGolden(t, "condition")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), `active: "{{ .parameters.deploy_cache }}"`)
	if len(coded(out, cloudimport.CodeConditionNotCarried)) != 1 {
		t.Errorf("the vault's condition is not reported: %+v", out.Diagnostics)
	}
}

// TestArmImportGoldenListKeys reports a key read with listKeys credential-flow
// and never writes it, while the account's endpoint the app reads is taken
// off its provides.
func TestArmImportGoldenListKeys(t *testing.T) {
	out := importGolden(t, "list-keys")
	if len(coded(out, cloudimport.CodeCredentialFlow)) != 1 {
		t.Errorf("the key is not reported credential-flow: %+v", out.Diagnostics)
	}
	for _, f := range out.Files {
		if strings.Contains(f.Content, "listKeys") {
			t.Errorf("%s carries the listKeys call:\n%s", f.Path, f.Content)
		}
	}
}

// TestArmImportGoldenKeyVaultReference attaches the administrator's password,
// a secure parameter the parameters file reads from a Key Vault secret, as a
// core/referenced-secret naming that secret, and writes the server's database
// as an instance of its own folding the server, under its own name and
// edition.
func TestArmImportGoldenKeyVaultReference(t *testing.T) {
	out := importGolden(t, "key-vault-reference")
	holds(t, "the Release", fileText(t, out, "release.yaml"), "type: core/referenced-secret",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-shared/providers/"+
			"Microsoft.KeyVault/vaults/kv-shared/secrets/sql-admin-password", `sku_name: "S0"`,
		"public_network_access_enabled: false")
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "type: core/datastore/sql-server",
		`database: "orders"`)
}

// TestArmImportOfAMalformedSourceHalts halts parse-failed on a template that
// is not JSON, drafting nothing and never panicking.
func TestArmImportOfAMalformedSourceHalts(t *testing.T) {
	out := Import(aggrega.ImportInput{Format: formatName, Target: target, Catalog: goldenView(t),
		Sources: []aggrega.ImportSource{src("main.json", []byte(`{"$schema": `+"\x00"))}}, parse)
	if len(out.Files) > 0 || len(out.Entities) > 0 {
		t.Errorf("a malformed source drafts %d files", len(out.Files))
	}
	halted := false
	for _, d := range out.Diagnostics {
		halted = halted || d.Severity == aggrega.SeverityHalt && d.Code == "parse-failed"
	}
	if !halted {
		t.Fatalf("the import does not halt parse-failed: %+v", out.Diagnostics)
	}
}

// TestArmPointerNamesAResourceByItsTypeAndName writes, for a reference to a
// resource's identifier, the resourceId expression naming it: a resource the
// template does not declare by the call it is written as, a parameter given a
// value written as that value; a resource it declares by its type and its
// name. Any other value, and a name the template computes when it deploys,
// has none.
func TestArmPointerNamesAResourceByItsTypeAndName(t *testing.T) {
	g := &cloudimport.Graph{
		Nodes: []*cloudimport.Node{
			{ID: "subnet", Type: "Microsoft.Network/virtualNetworks/subnets",
				Name: cloudimport.Compose{Parts: []cloudimport.Value{cloudimport.Param{Name: "vnet"}, str("/snet-db")}}},
			{ID: "vault", Type: "Microsoft.KeyVault/vaults",
				Name: cloudimport.Opaque{Text: "[uniqueString(resourceGroup().id)]", Target: target}},
		},
		Params: []cloudimport.Parameter{
			{Name: "vnet", Type: "string", Default: str("vnet-core")},
			{Name: "zone", Type: "string", Default: str("example.com"), Supplied: str("example.org")},
			{Name: "unset", Type: "string"},
		},
	}
	for _, c := range []struct {
		v    cloudimport.Value
		want string
	}{
		{cloudimport.Ref{Node: "subnet"}, "[resourceId('Microsoft.Network/virtualNetworks/subnets', 'vnet-core', 'snet-db')]"},
		{cloudimport.Ref{Node: "resourceId('Microsoft.Network/dnsZones', parameters('zone'))"},
			"[resourceId('Microsoft.Network/dnsZones', 'example.org')]"},
		{cloudimport.Ref{Node: "resourceId('Microsoft.Network/dnsZones', 'example.net')"},
			"[resourceId('Microsoft.Network/dnsZones', 'example.net')]"},
		{cloudimport.Ref{Node: "resourceId('Microsoft.Network/dnsZones', parameters('unset'))"}, ""},
		{cloudimport.Ref{Node: "vault"}, ""},
		{cloudimport.Ref{Node: "subnet", Attr: []string{"properties", "addressPrefix"}}, ""},
		{str("vnet-core"), ""},
	} {
		got, ok := pointer(c.v, g)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("pointer(%#v) = %q, %v; want %q", c.v, got, ok, c.want)
		}
	}
	if got, ok := Format.Pointer(cloudimport.Ref{Node: "subnet"}, nil); ok {
		t.Errorf("Format's pointer, reading no template, names the declared subnet as %q", got)
	}
}

// TestArmModuleIsSourcedFromTheRegistry locates an Azure Verified Modules
// module by its path in the Microsoft Container Registry, its telemetry bound
// off, and a module of no path nowhere.
func TestArmModuleIsSourcedFromTheRegistry(t *testing.T) {
	got := module(cloudimport.ModuleRef{Path: "avm/res/kusto/cluster", Version: "0.11.1"})
	if got.Kind != "bicep-module-oci" || got.URL != "oci://mcr.microsoft.com/bicep/avm/res/kusto/cluster" ||
		len(got.Fixed) != 1 || got.Fixed[0].Key != "enableTelemetry" || got.Fixed[0].Value != "false" {
		t.Errorf("module = %+v, want the registry's address with enableTelemetry false", got)
	}
	if got := module(cloudimport.ModuleRef{Prefix: "46d3xbcp.res.legacy"}); got.Kind != "" || got.URL != "" {
		t.Errorf("a module of no path is located at %+v", got)
	}
}

// staticSite is a 1.0 template declaring the parameters given, as JSON object
// members, and one static web app, a type no row maps, whose name and whose
// build's app location are the deploy-time text given.
func staticSite(params, name, appLocation string) string {
	return `{` + schema + `, "contentVersion": "1.0.0.0", "parameters": {` + params + `},
		"resources": [{"type": "Microsoft.Web/staticSites", "apiVersion": "2023-01-01", "name": "` + name + `",
			"location": "westeurope", "properties": {"buildProperties": {"appLocation": "` + appLocation + `"}}}]}`
}

// importOf imports sources for the arm target against goldenView.
func importOf(t *testing.T, sources ...aggrega.ImportSource) aggrega.ImportOutput {
	t.Helper()
	return Import(aggrega.ImportInput{Format: formatName, Target: target, Catalog: goldenView(t), Sources: sources},
		parse)
}

// TestArmDeployTimeTextWritesTheTemplateParametersInPlace imports deploy-time
// text reading a parameter of the template, which no drafted template
// declares: the text is written with the parameter's literal value in its
// place — the parameters file's where it supplies one, else the template's
// default — beneath a comment naming the parameter, in the Aggregate and in
// the Release alike, and the parameter is carried.
func TestArmDeployTimeTextWritesTheTemplateParametersInPlace(t *testing.T) {
	t.Parallel()
	template := staticSite(`"appName": {"type": "string", "defaultValue": "Docs"}`,
		"[toLower(parameters('appName'))]", "[toUpper(parameters('appName'))]")
	file := `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0", "parameters": {"appName": {"value": "Site"}}}`
	for _, row := range []struct {
		scenario      string
		sources       []aggrega.ImportSource
		value, source string
	}{
		{"the default", []aggrega.ImportSource{src("main.json", []byte(template))}, "Docs", "its literal default"},
		{"the parameters file", []aggrega.ImportSource{src("main.json", []byte(template)),
			src("main.parameters.json", []byte(file))}, "Site", "the value the parameters file supplies"},
	} {
		out := importOf(t, row.sources...)
		comment := "# From the template parameter appName, " + row.source + ".\n"
		aggregate := fileText(t, out, "aggregate.yaml")
		if want := "      values:\n        " + comment + "        name: \"[toLower('" + row.value + "')]\"\n"; !strings.Contains(aggregate, want) {
			t.Errorf("%s: the Aggregate does not hold\n%s\n%s", row.scenario, want, aggregate)
		}
		if strings.Contains(aggregate, "carries nowhere") {
			t.Errorf("%s: the Aggregate names the parameter carried nowhere:\n%s", row.scenario, aggregate)
		}
		release := fileText(t, out, "release.yaml")
		if want := "          " + comment + "          appLocation: \"[toUpper('" + row.value + "')]\"\n"; !strings.Contains(release, want) {
			t.Errorf("%s: the Release does not hold\n%s\n%s", row.scenario, want, release)
		}
		if got := coded(out, cloudimport.CodeValueNotCarried); len(got) != 0 {
			t.Errorf("%s: the import warns %+v", row.scenario, got)
		}
	}
}

// TestArmDeployTimeTextReadingAParameterWithNoLiteralIsLeftForTheAnswers
// imports deploy-time text reading a parameter of the template given no value
// by a parameters file and no default: the text is not written, the Release's
// key is left for its answers file, and value-not-carried warns of the key in
// the Aggregate and in the Release, naming the parameter.
func TestArmDeployTimeTextReadingAParameterWithNoLiteralIsLeftForTheAnswers(t *testing.T) {
	t.Parallel()
	out := importOf(t, src("main.json", []byte(staticSite(`"appName": {"type": "string"}`,
		"[toLower(parameters('appName'))]", "[toUpper(parameters('appName'))]"))))
	for _, f := range out.Files {
		if strings.Contains(f.Content, "parameters('appName')") {
			t.Errorf("%s writes the text reading the parameter:\n%s", f.Path, f.Content)
		}
	}
	holds(t, "the Release", fileText(t, out, "release.yaml"), "# Left for the Release, since the source cannot "+
		"supply it: infra.overrides.appLocation, the source's value, which the draft does not write\n"+
		"          appLocation: \"\"\n")
	warned := map[string]bool{}
	for _, d := range coded(out, cloudimport.CodeValueNotCarried) {
		if strings.Contains(d.Message, "it reads the template parameter appName, which neither the parameters "+
			"file nor a literal default gives a value") {
			warned[strings.Fields(d.Message[strings.Index(d.Message, "takes as ")+len("takes as "):])[0]] = true
		}
	}
	if !warned["values.name,"] || !warned["infra.overrides.appLocation,"] {
		t.Errorf("value-not-carried does not name the parameter at values.name and infra.overrides.appLocation: %+v",
			out.Diagnostics)
	}
}

// TestArmDeployTimeTextReadingAVariableHoldingAnObjectIsNotWritten imports
// deploy-time text reading a variable that holds an object, which no drafted
// template declares: the text is not written, and value-not-carried names the
// variable.
func TestArmDeployTimeTextReadingAVariableHoldingAnObjectIsNotWritten(t *testing.T) {
	t.Parallel()
	template := strings.Replace(staticSite("", "site", "[string(variables('build'))]"), `"resources"`,
		`"variables": {"build": {"root": "app"}}, "resources"`, 1)
	out := importOf(t, src("main.json", []byte(template)))
	if release := fileText(t, out, "release.yaml"); strings.Contains(release, "variables('build')") {
		t.Errorf("the Release writes the text reading the variable:\n%s", release)
	}
	got := coded(out, cloudimport.CodeValueNotCarried)
	if len(got) != 1 || !strings.Contains(got[0].Message, "infra.overrides.appLocation") ||
		!strings.Contains(got[0].Message, "it reads the variable build,") {
		t.Errorf("value-not-carried does not name the variable once: %+v", out.Diagnostics)
	}
}

// TestArmDeployTimeTextReadingASecureParameterIsNeverWrittenInPlace imports
// deploy-time text reading a secure parameter with a default: the default is
// never written, and the text is not written, as text naming a secure
// parameter never is.
func TestArmDeployTimeTextReadingASecureParameterIsNeverWrittenInPlace(t *testing.T) {
	t.Parallel()
	out := importOf(t, src("main.json", []byte(staticSite(`"token": {"type": "securestring", "defaultValue": "s3cret"}`,
		"site", "[toUpper(parameters('token'))]"))))
	for _, f := range out.Files {
		if strings.Contains(f.Content, "s3cret") || strings.Contains(f.Content, "S3CRET") {
			t.Errorf("%s writes the secure parameter's default:\n%s", f.Path, f.Content)
		}
	}
	got := coded(out, cloudimport.CodeValueNotCarried)
	if len(got) != 1 || !strings.Contains(got[0].Message, "it reads the secure parameter token inside text") {
		t.Errorf("value-not-carried does not name the secure parameter once: %+v", out.Diagnostics)
	}
}

// TestArmWrapReadsAnOutputsValueAndNothingElse drafts the Recipe sourcing a
// module whose outputs neighbours read: an output's value is published as the
// output, a member or an item inside it below the output's value, as ARM reads
// it, and an attribute naming no output's value is not published,
// reference-not-wired naming it and its provides key.
func TestArmWrapReadsAnOutputsValueAndNothingElse(t *testing.T) {
	t.Parallel()
	m := schemadraft.WrappedModule{
		Type: &schemadraft.Type{Name: "Microsoft.Kusto/clusters"}, SourceKind: "bicep-module-oci",
		URL: "oci://mcr.microsoft.com/bicep/avm/res/kusto/cluster", Version: "0.11.1",
		Outputs: []schemadraft.Provide{
			{Key: "outputs_resource_id_value", Attribute: []string{"outputs", "resourceId", "value"}},
			{Key: "outputs_resource_id_value_member", Attribute: []string{"outputs", "resourceId", "value", "member"}},
			{Key: "outputs_resource_id_value_0", Attribute: []string{"outputs", "resourceId", "value", "0"}},
			{Key: "outputs_resource_id_value_my_member", Attribute: []string{"outputs", "resourceId", "value", "my-member"}},
			{Key: "outputs_resource_id", Attribute: []string{"outputs", "resourceId"}},
			{Key: "provisioning_state", Attribute: []string{"provisioningState"}},
		},
	}
	d, diags := wrap(m)
	want := map[string]string{
		"outputs_resource_id_value":           "[reference('{{ .locals.symbol }}').outputs.resourceId.value]",
		"outputs_resource_id_value_member":    "[reference('{{ .locals.symbol }}').outputs.resourceId.value.member]",
		"outputs_resource_id_value_0":         "[reference('{{ .locals.symbol }}').outputs.resourceId.value[0]]",
		"outputs_resource_id_value_my_member": "[reference('{{ .locals.symbol }}').outputs.resourceId.value['my-member']]",
	}
	if len(d.Provides) != len(want) {
		t.Errorf("wrap publishes %+v, want %v", d.Provides, want)
	}
	for _, p := range d.Provides {
		if want[p.Key] != p.Value {
			t.Errorf("wrap publishes %s as %q, want %q", p.Key, p.Value, want[p.Key])
		}
	}
	if len(diags) != 2 {
		t.Fatalf("wrap reports %+v, want a reference-not-wired warning on each attribute naming no output's value",
			diags)
	}
	for i, named := range [][2]string{{"outputs.resourceId", "outputs_resource_id"},
		{"provisioningState", "provisioning_state"}} {
		if d := diags[i]; d.Code != string(cloudimport.CodeReferenceNotWired) || d.Severity != aggrega.SeverityWarn ||
			!strings.Contains(d.Message, "reads "+named[0]+" of the module") ||
			!strings.Contains(d.Message, "the provides key "+named[1]+", ") {
			t.Errorf("wrap reports %+v, want reference-not-wired naming %s and %s", d, named[0], named[1])
		}
	}
}

// TestArmCopyOfAResourceTakesAnIDNoOtherResourceTakes imports a resource
// copied three times beside another resource of the same type, whose ID is the
// one the third copy would take: the import drafts all four, the copy taking
// an ID of its own.
func TestArmCopyOfAResourceTakesAnIDNoOtherResourceTakes(t *testing.T) {
	t.Parallel()
	out := importOf(t, src("main.json", []byte(`{`+schema+`, "contentVersion": "1.0.0.0",
		"parameters": {"a": {"type": "string", "defaultValue": "x"}, "b": {"type": "string", "defaultValue": "y"}},
		"resources": [
			{"type": "Microsoft.Web/staticSites", "apiVersion": "2023-01-01",
				"name": "[concat(parameters('a'), copyIndex())]", "location": "westeurope", "copy": {"name": "loop", "count": 3}},
			{"type": "Microsoft.Web/staticSites", "apiVersion": "2023-01-01", "name": "[parameters('b')]",
				"location": "westeurope"}]}`)))
	if halts := coded(out, cloudimport.CodeSourceRefused); len(halts) != 0 {
		t.Fatalf("the import halts %+v", halts)
	}
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "    staticsites-0:\n", "    staticsites-1:\n",
		"    staticsites-2:\n", "    staticsites-2-2:\n")
}

// TestTheArmValuesHaveOneHome reads every Go source of the plugins, generated
// and test files aside, for the literal each value below is written as, and
// requires it in its home alone: the registry serving the Azure Verified
// Modules, the type of a nested deployment, and the telemetry prefix opening a
// resource module's. The scan is first run over a planted source holding all
// three, so a scan that reads nothing fails.
func TestTheArmValuesHaveOneHome(t *testing.T) {
	t.Parallel()
	homes := []struct{ literal, home string }{
		{`"oci://mcr.microsoft.com/bicep/`, "internal/armimport/import.go"},
		{`"Microsoft.Resources/deployments"`, "internal/armcore/references.go"},
		{`"46d3xbcp.`, "internal/armimport/avm.go"},
	}
	scan := func(root string) map[string][]string {
		found := map[string][]string{}
		err := filepath.WalkDir(root, func(file string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") ||
				strings.HasSuffix(file, "_gen.go") {
				return err
			}
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, file)
			for _, h := range homes {
				if strings.Contains(string(raw), h.literal) {
					found[h.literal] = append(found[h.literal], filepath.ToSlash(rel))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	planted := t.TempDir()
	var body strings.Builder
	body.WriteString("package p\n\nconst (\n")
	for i, h := range homes {
		fmt.Fprintf(&body, "\tc%d = %s\"\n", i, strings.TrimSuffix(h.literal, `"`))
	}
	body.WriteString(")\n")
	if err := os.WriteFile(filepath.Join(planted, "p.go"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := scan(planted); len(got) != len(homes) {
		t.Fatalf("the scan finds %v in the planted source, want each value", got)
	}
	found := scan(filepath.Join("..", ".."))
	for _, h := range homes {
		if got := found[h.literal]; !slices.Equal(got, []string{h.home}) {
			t.Errorf("%s is written in %v, want in %s alone", h.literal, got, h.home)
		}
	}
}

// TestArmReadingNoOutputsValueOfAWrappedModuleSaysWhatToRemove imports a site
// reading a wrapped module's output whole, which is no output's value: the
// warning says to remove the site's inject entry and the drafted Component's
// provides key, and the site's connection carries a comment saying so.
func TestArmReadingNoOutputsValueOfAWrappedModuleSaysWhatToRemove(t *testing.T) {
	t.Parallel()
	template := `{` + schema + `, "contentVersion": "1.0.0.0",
		"parameters": {"location": {"type": "string", "defaultValue": "westeurope"}},
		"resources": [
			{"type": "Microsoft.Web/staticSites", "apiVersion": "2023-01-01", "name": "site", "location": "westeurope",
				"dependsOn": ["[resourceId('Microsoft.Resources/deployments', 'cluster')]"],
				"properties": {"buildProperties": {"appLocation": "[reference('cluster').outputs.resourceId]"}}},
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "cluster",
				"properties": {"mode": "Incremental", "expressionEvaluationOptions": {"scope": "inner"},
					"parameters": {"name": {"value": "adx-core"}, "location": {"value": "[parameters('location')]"},
						"sku": {"value": "Standard_E2ads_v5"}},
					"template": {"$avm": "kusto/cluster/0.11.1"}}}]}`
	raw := avmLayer.ReplaceAllFunc([]byte(template), func(m []byte) []byte {
		return fixture(t, "avm/"+string(avmLayer.FindSubmatch(m)[1])+"/main.json")
	})
	out := importOf(t, src("main.json", raw))
	warned := coded(out, cloudimport.CodeReferenceNotWired)
	if len(warned) != 1 || !strings.Contains(warned[0].Message, "remove the inject entry reading "+
		"outputs_resource_id from the connection delivering it and the provides key outputs_resource_id from the "+
		"drafted Component") {
		t.Errorf("reference-not-wired does not say once what to remove: %+v", out.Diagnostics)
	}
	holds(t, "the Aggregate", fileText(t, out, "aggregate.yaml"), "        # Not wired: the value this connection "+
		"delivers from outputs_resource_id is cluster's outputs.resourceId, which the Recipe drafted for cluster "+
		"publishes under no provides key; remove the inject entry reading outputs_resource_id from this connection "+
		"and the provides key outputs_resource_id from the Component ")
}
