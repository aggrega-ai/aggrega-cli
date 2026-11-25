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
// Registry, at the version it states, its telemetry off, and the module's
// output the template reads provided.
func TestArmImportGoldenWrappedModule(t *testing.T) {
	out := importGolden(t, "wrapped-module")
	var recipe string
	for _, f := range out.Files {
		if strings.HasPrefix(f.Path, "catalog/recipe/arm/") {
			recipe = f.Content
		}
	}
	holds(t, "the wrapped module's Recipe", recipe, "kind: bicep-module-oci",
		"url: oci://mcr.microsoft.com/bicep/avm/res/kusto/cluster", "0.11.1", "enableTelemetry: false")
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
		got, ok := pointer(g, c.v)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("pointer(%#v) = %q, %v; want %q", c.v, got, ok, c.want)
		}
	}
	if got, ok := Format.Pointer(cloudimport.Ref{Node: "subnet"}); ok {
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
