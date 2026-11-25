// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// parse reads a source as the host's parser for JSON with comments does, and
// refuses it as the import socket hands an importer a parser's refusal: the
// message of its halting diagnostic. A request for another syntax fails, so
// the reader asking for one is caught.
func parse(syntax string, source []byte) (json.RawMessage, error) {
	if syntax != "jsonc" {
		return nil, fmt.Errorf("the reader asked for the %q syntax", syntax)
	}
	out := parsercore.ParseJSONC(source)
	for _, d := range out.Diagnostics {
		return nil, errors.New(d.Message)
	}
	return out.Tree, nil
}

// fixture returns the bytes of a file under testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// src is a named source of a request.
func src(name string, body []byte) aggrega.ImportSource {
	return aggrega.ImportSource{Name: name, Bytes: body}
}

// readAll reads sources with the root setting root.
func readAll(root string, sources ...aggrega.ImportSource) (*cloudimport.Graph, []aggrega.Diagnostic) {
	return Read(aggrega.ImportInput{Format: formatName, Sources: sources}, cloudimport.Settings{Root: root}, parse)
}

// readTemplate reads one template, held in main.json, written as text.
func readTemplate(body string) (*cloudimport.Graph, []aggrega.Diagnostic) {
	return readAll("", src("main.json", []byte(body)))
}

// codes returns the code of each diagnostic, in order.
func codes(diags []aggrega.Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		out[i] = d.Code
	}
	return out
}

// node returns the node of g whose ID is id, failing where there is none.
func node(t *testing.T, g *cloudimport.Graph, id string) *cloudimport.Node {
	t.Helper()
	if g == nil {
		t.Fatalf("no graph holds %s", id)
	}
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	ids := make([]string, len(g.Nodes))
	for i, n := range g.Nodes {
		ids[i] = n.ID
	}
	t.Fatalf("the graph holds no node %s, only %v", id, ids)
	return nil
}

// at returns the value of n's body at path, failing where there is none.
func at(t *testing.T, n *cloudimport.Node, path ...string) cloudimport.Value {
	t.Helper()
	props := n.Body
	var p cloudimport.Prop
	for _, seg := range path {
		found := false
		for _, m := range props {
			if m.Name == seg {
				p, found = m, true
				break
			}
		}
		if !found {
			var index int
			if _, err := fmt.Sscan(seg, &index); err != nil || index < 0 || index >= len(p.Items) {
				t.Fatalf("%s holds nothing at %v", n.ID, path)
			}
			p = p.Items[index]
		}
		props = p.Props
		if len(p.Items) > 0 {
			props = nil
		}
	}
	return p.Value
}

// lit is the literal JSON text.
func lit(text string) cloudimport.Literal {
	return cloudimport.Literal{JSON: json.RawMessage(text)}
}

// template is a 1.0 template holding the resources and variables given, as
// JSON text.
func oneZero(variables, resources string) string {
	if variables == "" {
		variables = "{}"
	}
	return `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"contentVersion": "1.0.0.0",
		"parameters": {"name": {"type": "string"}, "count": {"type": "int"}, "rg": {"type": "string"}},
		"variables": ` + variables + `, "resources": [` + resources + `]}`
}

// twoZero is a 2.0 template holding the resources given, keyed by symbolic
// name, as JSON text.
func twoZero(resources string) string {
	return `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"languageVersion": "2.0", "contentVersion": "1.0.0.0",
		"parameters": {"name": {"type": "string"}, "count": {"type": "int"}},
		"resources": {` + resources + `}}`
}

// TestReadKeysTwoZeroResourcesBySymbolicName reads a 2.0 template: each
// resource is the node of its symbolic name, with its existing flag, its
// condition, its copy loop, the parent its symbol nests it under and the
// resource its scope names.
func TestReadKeysTwoZeroResourcesBySymbolicName(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(twoZero(`
		"vault": {"type": "Microsoft.KeyVault/vaults", "apiVersion": "2023-07-01", "name": "kv-core", "existing": true},
		"vault::key": {"type": "Microsoft.KeyVault/vaults/keys", "apiVersion": "2023-07-01",
			"name": "[format('{0}/{1}', 'kv-core', 'cmk')]", "properties": {"kty": "RSA"}},
		"store": {"type": "Microsoft.Storage/storageAccounts", "apiVersion": "2023-01-01", "name": "stcore",
			"condition": "[equals(parameters('name'), 'on')]", "kind": "StorageV2"},
		"lock": {"type": "Microsoft.Authorization/locks", "apiVersion": "2020-05-01", "name": "lock-store",
			"scope": "[resourceId('Microsoft.Storage/storageAccounts', 'stcore')]", "properties": {"level": "CanNotDelete"}},
		"queues": {"type": "Microsoft.Storage/storageAccounts/queueServices/queues", "apiVersion": "2023-01-01",
			"name": "[format('stcore/default/q{0}', copyIndex())]", "copy": {"name": "queueLoop", "count": 2}}`))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	var ids []string
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
	}
	if want := []string{"lock", "queues", "store", "vault", "vault::key"}; !slices.Equal(ids, want) {
		t.Fatalf("nodes %v, want %v in ID order", ids, want)
	}
	if v := node(t, g, "vault"); !v.Existing || v.Type != "Microsoft.KeyVault/vaults" || v.APIVersion != "2023-07-01" ||
		!reflect.DeepEqual(v.Name, str("kv-core")) {
		t.Errorf("vault reads %+v", v)
	}
	if k := node(t, g, "vault::key"); k.Parent != "vault" || !reflect.DeepEqual(k.Name, str("kv-core/cmk")) ||
		!reflect.DeepEqual(at(t, k, "properties", "kty"), str("RSA")) {
		t.Errorf("vault::key reads parent %q, name %+v", k.Parent, k.Name)
	}
	if s := node(t, g, "store"); !reflect.DeepEqual(s.Condition, cloudimport.Opaque{
		Text: "[equals(parameters('name'), 'on')]", Target: "arm"}) {
		t.Errorf("store's condition reads %+v", s.Condition)
	}
	if l := node(t, g, "lock"); l.Parent != "store" || l.Scope != "" {
		t.Errorf("the lock reads parent %q, scope %q; want the store it is scoped to", l.Parent, l.Scope)
	}
	q := node(t, g, "queues")
	if q.Repeat == nil || !slices.Equal(q.Repeat.Keys, []string{"0", "1"}) {
		t.Fatalf("queues repeats %+v, want two instances", q.Repeat)
	}
	if inst := q.Repeat.Instance("1"); inst == nil || !reflect.DeepEqual(inst.Name, str("stcore/default/q1")) {
		t.Errorf("the second queue is named %+v", inst)
	}
	if want := "resourceId('Microsoft.Storage/storageAccounts/queueServices', 'stcore', 'default')"; q.Parent != want {
		t.Errorf("queues' parent is %q, want the queue service the template does not declare, %q", q.Parent, want)
	}
}

// TestReadFlattensOneZeroChildResources reads a 1.0 template: a child nested
// in its parent's resources is a node of its own with the full type and name,
// and each resource's symbol is the literal text its name ends with, else the
// last segment of its type, made unique as the importer kit keys a node.
func TestReadFlattensOneZeroChildResources(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "Microsoft.Sql/servers", "apiVersion": "2023-05-01-preview", "name": "[parameters('name')]",
			"resources": [{"type": "databases", "apiVersion": "2023-05-01-preview", "name": "orders",
				"properties": {"collation": "SQL_Latin1_General_CP1_CI_AS"},
				"resources": [{"type": "Microsoft.Sql/servers/databases/backupShortTermRetentionPolicies",
					"apiVersion": "2023-05-01-preview", "name": "default"}]}]},
		{"type": "Microsoft.Sql/servers", "apiVersion": "2023-05-01-preview", "name": "[parameters('rg')]"},
		{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "[concat(parameters('name'), '-api')]"},
		{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "API"}`))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	var ids []string
	for _, n := range g.Nodes {
		ids = append(ids, n.ID)
	}
	if want := []string{"api", "api-2", "default", "orders", "servers", "servers-2"}; !slices.Equal(ids, want) {
		t.Fatalf("nodes %v, want %v", ids, want)
	}
	db := node(t, g, "orders")
	if db.Type != "Microsoft.Sql/servers/databases" || db.Parent != "servers" || db.Source != "main.json#/resources/0/resources/0" ||
		!reflect.DeepEqual(db.Name, cloudimport.Compose{Parts: []cloudimport.Value{cloudimport.Param{Name: "name"},
			str("/orders")}}) {
		t.Errorf("the nested database reads type %q, parent %q, source %q, name %+v", db.Type, db.Parent, db.Source, db.Name)
	}
	policy := node(t, g, "default")
	if policy.Type != "Microsoft.Sql/servers/databases/backupShortTermRetentionPolicies" || policy.Parent != "orders" {
		t.Errorf("the policy nested with its full type reads type %q, parent %q", policy.Type, policy.Parent)
	}
	if n := node(t, g, "servers-2"); n.Source != "main.json#/resources/1" {
		t.Errorf("the second server is read from %s", n.Source)
	}
}

// TestReadRefusesTemplatesItCannotRead hands each template the import refuses,
// one fault apiece, and requires a source-refused halt naming the template and
// why, and no graph.
func TestReadRefusesTemplatesItCannotRead(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ file, why string }{
		{"refused/tenant.json", "its $schema is the tenant template schema"},
		{"refused/experimental.json", "its languageVersion is neither 1.0 nor 2.0"},
		{"refused/keyed-resources.json", "its resources are not an array"},
	} {
		g, diags := readAll("", src("main.json", fixture(t, row.file)))
		if g != nil || len(diags) != 1 || diags[0].Code != "source-refused" || diags[0].Severity != "halt" ||
			!strings.HasPrefix(diags[0].Message, "the template main.json is refused: "+row.why) {
			t.Errorf("%s: graph %v, diagnostics %+v; want one source-refused halt saying %q", row.file, g != nil,
				diags, row.why)
		}
	}
	g, diags := readTemplate(`{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"languageVersion": "2.0", "resources": [{"type": "Microsoft.Web/sites", "name": "x"}]}`)
	if g != nil || len(diags) != 1 || diags[0].Code != "source-refused" || !strings.HasPrefix(diags[0].Message,
		"the template main.json is refused: its languageVersion is 2.0 and its resources are not an object") {
		t.Errorf("a 2.0 template listing its resources: graph %v, diagnostics %+v", g != nil, diags)
	}
}

// TestMultiLineStringHaltsParseFailed reads a template holding a raw line
// break inside a string: the parser refuses it, and the import halts with the
// importer kit's parse-failed naming the source, the line and the column.
func TestMultiLineStringHaltsParseFailed(t *testing.T) {
	t.Parallel()
	g, diags := readAll("", src("main.json", fixture(t, "refused/multiline.json")))
	want := `could not parse source "main.json" as arm: json parse: invalid character '\n' in string literal at line 9, column 71`
	if g != nil || len(diags) != 1 || diags[0].Code != "parse-failed" || diags[0].Severity != "halt" ||
		diags[0].Message != want {
		t.Fatalf("graph %v, diagnostics %+v; want one parse-failed halt reading %q", g != nil, diags, want)
	}
}

// TestTemplateWithByteOrderMarkAndCRLFIsRead reads the 1.0 topology saved as a
// Windows tool saves it — opening with a UTF-8 byte-order mark, each line
// ending in a carriage return and a line feed — and requires the graph the
// same template saved plainly gives.
func TestTemplateWithByteOrderMarkAndCRLFIsRead(t *testing.T) {
	t.Parallel()
	windows := fixture(t, "windows/main.json")
	if !bytes.HasPrefix(windows, []byte("\xef\xbb\xbf")) || !bytes.Contains(windows, []byte("\r\n")) {
		t.Fatal("the fixture does not open with a byte-order mark and end its lines in CRLF")
	}
	saved, savedDiags := readAll("", src("main.json", windows))
	plain, plainDiags := readAll("", src("main.json", fixture(t, "topology/one.json")))
	if saved == nil || len(savedDiags) != 0 || len(plainDiags) != 0 {
		t.Fatalf("diagnostics %+v and %+v", savedDiags, plainDiags)
	}
	if !reflect.DeepEqual(saved, plain) {
		t.Fatalf("the template saved by a Windows tool reads\n%s\nand saved plainly\n%s", dump(saved), dump(plain))
	}
}

// TestOneAndTwoDraftTheSameGraph reads one topology written in language
// version 1.0 — a child nested in its parent, references through resourceId
// and variables, concat — and in 2.0 — symbolic names, format — and requires
// one graph, but for where each node is read from.
func TestOneAndTwoDraftTheSameGraph(t *testing.T) {
	t.Parallel()
	one, oneDiags := readAll("", src("main.json", fixture(t, "topology/one.json")))
	two, twoDiags := readAll("", src("main.json", fixture(t, "topology/two.json")))
	if one == nil || two == nil || len(oneDiags) != 0 || len(twoDiags) != 0 {
		t.Fatalf("diagnostics %+v and %+v", oneDiags, twoDiags)
	}
	for _, g := range []*cloudimport.Graph{one, two} {
		for _, n := range g.Nodes {
			n.Source = ""
		}
	}
	if !reflect.DeepEqual(one, two) {
		t.Fatalf("1.0 reads\n%s\nand 2.0\n%s", dump(one), dump(two))
	}
	web := node(t, one, "web")
	if got := at(t, web, "properties", "template", "containers", "0", "env", "1", "value"); !reflect.DeepEqual(got,
		cloudimport.Compose{Parts: []cloudimport.Value{str("postgres://"), cloudimport.Ref{Node: "flexibleservers",
			Attr: []string{"properties", "fullyQualifiedDomainName"}}, str(":5432/appdb")}}) {
		t.Errorf("DB_URL reads %+v", got)
	}
	if !slices.Equal(web.DependsOn, []string{"appdb", "storageaccounts"}) {
		t.Errorf("web depends on %v", web.DependsOn)
	}
}

// dump writes g as indented JSON, for a comparison and a failure message: a
// repeated node by its keys, as a function does not print.
func dump(g *cloudimport.Graph) string {
	if g == nil {
		return "null"
	}
	shown := *g
	shown.Nodes = make([]*cloudimport.Node, len(g.Nodes))
	keys := map[string][]string{}
	for i, n := range g.Nodes {
		c := *n
		if c.Repeat != nil {
			keys[c.ID] = append([]string{"repeated over"}, c.Repeat.Keys...)
			c.Repeat = nil
		}
		shown.Nodes[i] = &c
	}
	raw, err := json.MarshalIndent(struct {
		Graph   cloudimport.Graph
		Repeats map[string][]string
	}{shown, keys}, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
}

// TestSourcesAreClassifiedRootedAndLinked imports a directory: the entry
// template, the template it links by a relative path, a parameters file and a
// JSON file of the directory that is no template. The entry template is the
// root, the linked template and the parameters file are read with it — the
// root's resources and the linked template's are the graph's nodes — and the
// other file is named not read.
func TestSourcesAreClassifiedRootedAndLinked(t *testing.T) {
	t.Parallel()
	g, diags := readAll("",
		src("notes.json", fixture(t, "linked/notes.json")),
		src("modules/network.json", fixture(t, "linked/modules/network.json")),
		src("main.parameters.json", fixture(t, "linked/main.parameters.json")),
		src("main.json", fixture(t, "linked/main.json")))
	if g == nil || len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	if want := []string{"main.json", "main.parameters.json", "modules/network.json"}; !slices.Equal(g.Read, want) {
		t.Errorf("read %v, want %v", g.Read, want)
	}
	if want := []string{"notes.json"}; !slices.Equal(g.NotRead, want) {
		t.Errorf("not read %v, want %v", g.NotRead, want)
	}
	node(t, g, "vaults")
	node(t, g, "network/vnet-core")
	if len(g.Params) != 1 || !reflect.DeepEqual(g.Params[0].Supplied, lit(`"kv-core"`)) {
		t.Errorf("the parameters are %+v, want vaultName supplied by the parameters file", g.Params)
	}
}

// TestSourcesSeveralRootsHaltWithoutTheRootSetting hands two templates neither
// links: without the root setting the import halts source-refused naming both;
// with it, the one it names is read and the other is not.
func TestSourcesSeveralRootsHaltWithoutTheRootSetting(t *testing.T) {
	t.Parallel()
	a := src("a.json", []byte(oneZero("", `{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "a"}`)))
	b := src("b.json", []byte(oneZero("", `{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "b"}`)))
	g, diags := readAll("", a, b)
	if g != nil || len(diags) != 1 || diags[0].Code != "source-refused" ||
		!strings.Contains(diags[0].Message, "a.json, b.json") || !strings.Contains(diags[0].Message, "root") {
		t.Errorf("two candidates and no root setting: graph %v, diagnostics %+v", g != nil, diags)
	}
	g, diags = readAll("b.json", a, b)
	if g == nil || len(diags) != 0 || !slices.Equal(g.Read, []string{"b.json"}) || !slices.Equal(g.NotRead, []string{"a.json"}) {
		t.Fatalf("root b.json: diagnostics %+v", diags)
	}
	node(t, g, "b")
}

// TestSourcesLinkedTemplatesTheImportDoesNotHoldWarn reads a template linking,
// one way apiece, a template the import does not hold: each is reported
// linked-source-not-read on its node, nothing is fetched, and an output of it
// the template reads is deploy-time text.
func TestSourcesLinkedTemplatesTheImportDoesNotHoldWarn(t *testing.T) {
	t.Parallel()
	notes := src("modules/notes.json", fixture(t, "linked/notes.json"))
	for _, row := range []struct {
		link, how string
		beside    []aggrega.ImportSource
	}{
		{`{"uri": "https://example.com/templates/net.json"}`, "its URI", nil},
		{`{"id": "/subscriptions/0000/resourceGroups/rg/providers/Microsoft.Resources/templateSpecs/net/versions/1.0"}`,
			"its template spec ID", nil},
		{`{"relativePath": "../shared/net.json"}`, "a relative path leaving the imported directory", nil},
		{`{"relativePath": "modules/net.json"}`, "a relative path naming no file the import holds", nil},
		{`{"relativePath": "modules/notes.json"}`, "a relative path naming a file the import holds that is no " +
			"template", []aggrega.ImportSource{notes}},
		{`{"relativePath": "modules\\net.json"}`, "a relative path written with a backslash, which the import " +
			"does not read as a path separator", nil},
	} {
		main := src("main.json", []byte(oneZero("", `
			{"type": "Microsoft.Resources/deployments", "apiVersion": "2022-09-01", "name": "net",
				"properties": {"mode": "Incremental", "templateLink": `+row.link+`}},
			{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web",
				"properties": {"subnet": "[reference('net').outputs.subnetId.value]"}}`)))
		g, diags := readAll("", append([]aggrega.ImportSource{main}, row.beside...)...)
		if g == nil || len(diags) != 1 || diags[0].Code != "linked-source-not-read" || diags[0].Severity != "warn" ||
			!strings.HasPrefix(diags[0].Message, "net is a Microsoft.Resources/deployments resource of main.json whose "+
				"template is linked by "+row.how+",") || diags[0].SourceRef != "main.json#/resources/0" {
			t.Errorf("%s: diagnostics %+v", row.how, diags)
			continue
		}
		if got := at(t, node(t, g, "web"), "properties", "subnet"); !reflect.DeepEqual(got,
			cloudimport.Opaque{Text: "[reference('net').outputs.subnetId.value]", Target: "arm"}) {
			t.Errorf("%s: the output read reads %+v", row.how, got)
		}
	}
}

// TestSourcesSeveralParametersFilesAreNoneRead hands two parameters files: the
// import reads neither, warns parameter-files-not-read naming both, and fills
// no parameter.
func TestSourcesSeveralParametersFilesAreNoneRead(t *testing.T) {
	t.Parallel()
	params := fixture(t, "parameters/one.parameters.json")
	g, diags := readAll("", src("main.json", fixture(t, "topology/one.json")), src("dev.parameters.json", params),
		src("prod.parameters.json", params))
	if g == nil || len(diags) != 1 || diags[0].Code != "parameter-files-not-read" || diags[0].Severity != "warn" ||
		!strings.Contains(diags[0].Message, "dev.parameters.json, prod.parameters.json") {
		t.Fatalf("diagnostics %+v", diags)
	}
	if !slices.Equal(g.Read, []string{"main.json"}) ||
		!slices.Equal(g.NotRead, []string{"dev.parameters.json", "prod.parameters.json"}) {
		t.Errorf("read %v, not read %v", g.Read, g.NotRead)
	}
	for _, p := range g.Params {
		if p.Supplied != nil || p.StoreRef != "" {
			t.Errorf("the parameter %s is filled: %+v", p.Name, p)
		}
	}
}

// TestSourcesWithoutATemplateHalt hands a parameters file and a JSON file that
// is no template: the import halts source-refused, saying it holds no template.
func TestSourcesWithoutATemplateHalt(t *testing.T) {
	t.Parallel()
	g, diags := readAll("", src("main.parameters.json", fixture(t, "parameters/one.parameters.json")),
		src("notes.json", fixture(t, "linked/notes.json")))
	if g != nil || len(diags) != 1 || diags[0].Code != "source-refused" ||
		!strings.HasPrefix(diags[0].Message, "the import holds no Azure Resource Manager template") {
		t.Fatalf("graph %v, diagnostics %+v", g != nil, diags)
	}
}

// TestSourcesAFileTheParserRefusesBesideATemplate hands a template with a file
// the parser refuses: a file naming no template schema is named not read, and
// one naming it halts parse-failed, being a template that does not parse.
func TestSourcesAFileTheParserRefusesBesideATemplate(t *testing.T) {
	t.Parallel()
	template := src("main.json", fixture(t, "topology/one.json"))
	g, diags := readAll("", template, src("broken.json", []byte(`{"owner": `)))
	if g == nil || len(diags) != 0 || !slices.Equal(g.NotRead, []string{"broken.json"}) {
		t.Errorf("a broken file of the directory: diagnostics %+v", diags)
	}
	g, diags = readAll("", template, src("other.json", fixture(t, "refused/multiline.json")))
	if g != nil || len(diags) != 1 || diags[0].Code != "parse-failed" || diags[0].SourceRef != "other.json" {
		t.Errorf("a broken template beside the root: graph %v, diagnostics %+v", g != nil, diags)
	}
}

// TestReadIsDeterministic reads each fixture template twice and requires one
// graph and one list of diagnostics.
func TestReadIsDeterministic(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"topology/one.json", "topology/two.json", "linked/main.json"} {
		g1, d1 := readAll("", src("main.json", fixture(t, name)))
		g2, d2 := readAll("", src("main.json", fixture(t, name)))
		if g1 == nil || len(g1.Nodes) == 0 {
			t.Errorf("%s reads no node: %+v", name, d1)
		}
		if dump(g1) != dump(g2) || !reflect.DeepEqual(d1, d2) {
			t.Errorf("%s reads differently twice", name)
		}
	}
}

// TestReadAResourceWithoutALiteralTypeIsNotCarried reads a template one of
// whose resources writes its type as an expression: that resource is not
// drafted, with a resource-not-carried warning naming where it is written, and
// the others are read.
func TestReadAResourceWithoutALiteralTypeIsNotCarried(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `
		{"type": "[parameters('name')]", "apiVersion": "2023-01-01", "name": "computed"},
		{"type": "Microsoft.Web/sites", "apiVersion": "2023-01-01", "name": "web"}`))
	if g == nil || len(diags) != 1 || diags[0].Code != "resource-not-carried" || diags[0].Severity != "warn" ||
		diags[0].SourceRef != "main.json#/resources/0" {
		t.Fatalf("diagnostics %+v", diags)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].ID != "web" {
		t.Errorf("the graph holds %d nodes, want web alone", len(g.Nodes))
	}
}

// TestReadChildrenItCannotReachAreNotCarried reads resources whose children the
// import cannot reach, one fault apiece: a resources member that is not an
// array, and children nested deeper than the import follows. Each is not
// carried, with a resource-not-carried warning naming where it is written.
func TestReadChildrenItCannotReachAreNotCarried(t *testing.T) {
	t.Parallel()
	g, diags := readTemplate(oneZero("", `{"type": "Microsoft.Sql/servers", "apiVersion": "2023-05-01-preview",
		"name": "sql", "resources": {"db": {"type": "databases", "name": "db"}}}`))
	if g == nil || len(diags) != 1 || diags[0].Code != "resource-not-carried" ||
		diags[0].SourceRef != "main.json#/resources/0/resources" ||
		!strings.Contains(diags[0].Message, "child resources are not written as an array") {
		t.Errorf("children keyed by name: diagnostics %+v", diags)
	}

	nested := fmt.Sprintf(`{"type": "c", "apiVersion": "2024-01-01", "name": "l%d"}`, maxNesting+1)
	for depth := maxNesting; depth >= 1; depth-- {
		nested = fmt.Sprintf(`{"type": "c", "apiVersion": "2024-01-01", "name": "l%d", "resources": [%s]}`, depth, nested)
	}
	nested = `{"type": "Microsoft.Example/level", "apiVersion": "2024-01-01", "name": "l0", "resources": [` + nested + `]}`
	g, diags = readTemplate(oneZero("", nested))
	cut := "main.json#" + strings.Repeat("/resources/0", maxNesting+1) + "/resources"
	if g == nil || len(diags) != 1 || diags[0].Code != "resource-not-carried" || diags[0].SourceRef != cut ||
		!strings.Contains(diags[0].Message, "nested deeper than") {
		t.Errorf("children nested too deep: diagnostics %+v, want one at %s", diags, cut)
	}
	if g != nil && len(g.Nodes) != maxNesting+1 {
		t.Errorf("the graph holds %d nodes, want the %d levels the import follows", len(g.Nodes), maxNesting+1)
	}
}

// TestReadKeepsAChildsOwnName keeps, beside the full name the template
// composes for a child resource, its own name within its parent: the name it
// writes nested in its parent's resources; for one written in full beside its
// parent, the part after its last slash written as literal text, none where
// no literal text holds the slash. A child of language version 2.0 naming its
// parent writes its own name alone.
func TestReadKeepsAChildsOwnName(t *testing.T) {
	g, diags := readTemplate(`{` + schema + `, "contentVersion": "1.0.0.0",
		"parameters": {"serverName": {"type": "string"}, "dbName": {"type": "string"}, "fullName": {"type": "string"}},
		"variables": {"databaseName": "appdb"},
		"resources": [
			{"type": "Microsoft.DBforPostgreSQL/flexibleServers", "apiVersion": "2022-12-01",
				"name": "[parameters('serverName')]",
				"resources": [{"type": "databases", "apiVersion": "2022-12-01", "name": "[variables('databaseName')]"}]},
			{"type": "Microsoft.DBforPostgreSQL/flexibleServers/databases", "apiVersion": "2022-12-01",
				"name": "[concat(parameters('serverName'), '/orders')]"},
			{"type": "Microsoft.DBforPostgreSQL/flexibleServers/databases", "apiVersion": "2022-12-01",
				"name": "[format('{0}/{1}', parameters('serverName'), parameters('dbName'))]"},
			{"type": "Microsoft.DBforPostgreSQL/flexibleServers/databases", "apiVersion": "2022-12-01",
				"name": "[parameters('fullName')]"}
		]}`)
	if len(diags) > 0 {
		t.Fatalf("the template reads with %+v", diags)
	}
	for id, want := range map[string]cloudimport.Value{
		"appdb":     str("appdb"),
		"orders":    str("orders"),
		"databases": cloudimport.Param{Name: "dbName"},
	} {
		n := node(t, g, id)
		if !n.Composed || !reflect.DeepEqual(n.Leaf, want) {
			t.Errorf("%s keeps its own name %#v (composed %v), want %#v", id, n.Leaf, n.Composed, want)
		}
	}
	if n := node(t, g, "databases-2"); !n.Composed || n.Leaf != nil {
		t.Errorf("databases-2 keeps its own name %#v (composed %v), want none told apart", n.Leaf, n.Composed)
	}
	if n := node(t, g, "flexibleservers"); n.Composed || n.Leaf != nil {
		t.Errorf("the server keeps an own name %#v (composed %v), want its name its own", n.Leaf, n.Composed)
	}

	g2, diags := readTemplate(`{` + schema + `, "languageVersion": "2.0", "contentVersion": "1.0.0.0",
		"resources": {
			"server": {"type": "Microsoft.DBforPostgreSQL/flexibleServers", "apiVersion": "2022-12-01", "name": "pg"},
			"db": {"type": "Microsoft.DBforPostgreSQL/flexibleServers/databases", "apiVersion": "2022-12-01",
				"parent": "server", "name": "appdb"}}}`)
	if len(diags) > 0 {
		t.Fatalf("the 2.0 template reads with %+v", diags)
	}
	if n := node(t, g2, "db"); n.Composed || n.Leaf != nil || !reflect.DeepEqual(n.Name, str("appdb")) {
		t.Errorf("the 2.0 child naming its parent is %#v, composed %v, own %#v; want its own name alone", n.Name,
			n.Composed, n.Leaf)
	}
}
