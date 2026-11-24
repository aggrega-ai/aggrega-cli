// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudformation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	ledgerComponent = "catalog/component/imported/component/aws-example-ledger/0.1.0/component.yaml"
	ledgerRecipe    = "catalog/recipe/cloudformation/aws-example-ledger/0.1.0/recipe.yaml"
)

func fileOf(t *testing.T, out aggrega.ImportOutput, path string) string {
	t.Helper()
	var paths []string
	for _, f := range out.Files {
		if f.Path == path {
			return f.Content
		}
		paths = append(paths, f.Path)
	}
	t.Fatalf("no file %s among %v", path, paths)
	return ""
}

// mappingAt returns the node at the mapping keys path below doc.
func mappingAt(t *testing.T, doc string, path ...string) *yaml.Node {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	n := root.Content[0]
	for _, key := range path {
		var next *yaml.Node
		for i := 0; n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
			}
		}
		if next == nil {
			t.Fatalf("no %s in\n%s", strings.Join(path, "."), doc)
		}
		n = next
	}
	return n
}

// entries returns the keys and values of a mapping node, in order.
func entries(n *yaml.Node) (keys, values []string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys, values = append(keys, n.Content[i].Value), append(values, n.Content[i+1].Value)
	}
	return keys, values
}

// pillarKeys lists the keys of the Component's spec.schema.<pillar>, in order.
func pillarKeys(t *testing.T, component, pillar string) []string {
	t.Helper()
	keys, _ := entries(mappingAt(t, component, "spec", "schema", pillar, "properties"))
	return keys
}

// golden compares got with the file testdata/golden/<name>. Run the tests with
// UPDATE_GOLDEN=1 to write got into it instead.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the golden %s: %v (run with UPDATE_GOLDEN=1 to write it)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden (run with UPDATE_GOLDEN=1 to rewrite it):\n%s", path, got)
	}
}

// render evaluates text as the compiler's template surfaces do, over ctx.
func render(text string, ctx map[string]any) (string, error) {
	tmpl, err := template.New("t").Option("missingkey=error").Funcs(gotmpl.FuncMap()).Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	err = tmpl.Execute(&b, ctx)
	return b.String(), err
}

// recipeParts reads the logical id local, the provides in order and the
// template.yaml artifact of a drafted Recipe.
type recipeParts struct {
	logicalID             string
	provideKeys, provides []string
	template              string
	assertionKeys         []string
}

func partsOf(t *testing.T, recipe string) recipeParts {
	t.Helper()
	var p recipeParts
	p.logicalID = mappingAt(t, recipe, "spec", "locals", "logical_id").Value
	p.provideKeys, p.provides = entries(mappingAt(t, recipe, "spec", "provides"))
	p.template = mappingAt(t, recipe, "spec", "artifacts", "template.yaml").Value
	p.assertionKeys, _ = entries(mappingAt(t, recipe, "spec", "assertions"))
	return p
}

// ledgerContext is the context the Ledger's Recipe renders in for the instance
// orders-ledger, with the overrides given.
func ledgerContext(values, overrides map[string]any) map[string]any {
	infra := map[string]any{"capacity": map[string]any{"read_units": 5, "mode": "BURST"}, "subnet_group": "private-a"}
	if overrides != nil {
		infra["overrides"] = overrides
	}
	return map[string]any{
		"context":  map[string]any{"instance_id": "orders-ledger", "inject": map[string]any{}},
		"metadata": map[string]any{"instance_id": "orders-ledger"},
		"values":   values,
		"infra":    infra,
		"features": map[string]any{},
	}
}

// renderFragment renders the Recipe's template.yaml in ctx, with its logical id
// evaluated first as the compiler evaluates locals, and returns the rendered
// fragment and its long form as the provider reads it.
func renderFragment(t *testing.T, parts recipeParts, ctx map[string]any) (string, map[string]any) {
	t.Helper()
	logicalID, err := render(parts.logicalID, ctx)
	if err != nil {
		t.Fatalf("locals.logical_id: %v", err)
	}
	ctx["locals"] = map[string]any{"logical_id": logicalID}
	text, err := render(parts.template, ctx)
	if err != nil {
		t.Fatalf("template.yaml: %v\n%s", err, parts.template)
	}
	long, err := cfncore.NormalizeFragment([]byte(text))
	if err != nil {
		t.Fatalf("the provider refuses the fragment: %v\n%s", err, text)
	}
	var doc map[string]any
	if err := json.Unmarshal(long, &doc); err != nil {
		t.Fatal(err)
	}
	return text, doc
}

// canon writes v as JSON with sorted keys, for comparison.
func canon(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var back any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(back)
	return string(raw)
}

func canonText(t *testing.T, text string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return canon(t, v)
}

func at(doc map[string]any, path ...string) any {
	var v any = doc
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func TestCloudFormationGolden(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	noHalt(t, out.Diagnostics)
	var paths []string
	for _, f := range out.Files {
		paths = append(paths, f.Path)
	}
	if want := []string{ledgerComponent, ledgerRecipe}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("files %v, want %v", paths, want)
	}
	golden(t, "ledger.component.yaml", fileOf(t, out, ledgerComponent))
	golden(t, "ledger.recipe.yaml", fileOf(t, out, ledgerRecipe))

	warned := func(source, words string) bool {
		for _, d := range out.Diagnostics {
			if d.Code == codeDropped && d.Severity == "warn" && d.SourceRef == source && strings.Contains(d.Message, words) {
				return true
			}
		}
		return false
	}
	if !warned("LedgerName", "^(?!reserved-)[a-z][a-z0-9-]*$") {
		t.Errorf("no warning drops the pattern RE2 refuses: %+v", out.Diagnostics)
	}
	if !warned("Rules[].Children[]", "refer back") {
		t.Errorf("no warning reports the cycle cut: %+v", out.Diagnostics)
	}

	// Two runs give the same bytes.
	again := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	if !reflect.DeepEqual(out, again) {
		t.Errorf("two runs differ")
	}
}

func TestCloudFormationDraftsLoad(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	noHalt(t, out.Diagnostics)
	dir := t.TempDir()
	for _, f := range out.Files {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := catalog.LoadAll("", filepath.Join(dir, "catalog"))
	if err != nil {
		t.Fatalf("the catalog refuses the drafts: %v", err)
	}
	if _, ok := cat.Components["imported/component/aws-example-ledger:0.1.0"]; !ok {
		t.Errorf("the catalog holds no drafted Component")
	}
	if _, ok := cat.Recipes["cloudformation/aws-example-ledger:0.1.0"]; !ok {
		t.Errorf("the catalog holds no drafted Recipe")
	}
}

// TestCloudFormationTemplateRenders renders the drafted Recipe over sample
// values through the compiler's template functions, reads the fragment as the
// cloudformation provider does and assembles it into a stack.
func TestCloudFormationTemplateRenders(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	noHalt(t, out.Diagnostics)
	parts := partsOf(t, fileOf(t, out, ledgerRecipe))
	if parts.logicalID != "{{ capitalizedWords .context.instance_id }}" {
		t.Errorf("locals.logical_id %q", parts.logicalID)
	}

	values := map[string]any{"identifier": "orders-1", "ledger_name": "orders"}
	overrides := map[string]any{"KmsKeyId": "!GetAtt Key.Arn", "Rules": []any{map[string]any{"Action": "keep"}}}
	text, doc := renderFragment(t, parts, ledgerContext(values, overrides))

	if got := at(doc, "Resources", "OrdersLedger", "Type"); got != ledger {
		t.Errorf("the resource's type %v\n%s", got, text)
	}
	want := `{"Capacity": {"Mode": "BURST", "ReadUnits": 5}, "Identifier": "orders-1", "KmsKeyId": {"Fn::GetAtt": ` +
		`["Key", "Arn"]}, "LedgerName": "orders", "MasterPassword": {"Ref": "OrdersLedgerMasterPassword"}, ` +
		`"Rules": [{"Action": "keep"}], "SubnetGroup": "private-a"}`
	if got := canon(t, at(doc, "Resources", "OrdersLedger", "Properties")); got != canonText(t, want) {
		t.Errorf("properties %s, want %s\n%s", got, canonText(t, want), text)
	}
	if got := canon(t, at(doc, "Parameters")); got != canonText(t,
		`{"OrdersLedgerMasterPassword": {"Type": "String", "NoEcho": true}}`) {
		t.Errorf("parameters %s", got)
	}
	wantProvides := `{"arn": {"Fn::GetAtt": ["OrdersLedger", "Arn"]}, "endpoint_address": {"Fn::GetAtt": ` +
		`["OrdersLedger", "Endpoint.Address"]}, "endpoint_port": {"Fn::GetAtt": ["OrdersLedger", "Endpoint.Port"]}}`
	if got := canon(t, at(doc, "provides")); got != canonText(t, wantProvides) {
		t.Errorf("fragment provides %s, want %s", got, canonText(t, wantProvides))
	}
	if len(doc) != 3 {
		t.Errorf("the fragment holds %d sections, want Parameters, Resources and provides", len(doc))
	}

	// The Recipe's own provides are the fragment's, in the same order.
	if want := []string{"arn", "endpoint_address", "endpoint_port"}; !reflect.DeepEqual(parts.provideKeys, want) {
		t.Errorf("provides %v, want %v", parts.provideKeys, want)
	}
	for i, value := range parts.provides {
		got, err := render(value, map[string]any{"locals": map[string]any{"logical_id": "OrdersLedger"}})
		attr := map[string]string{"arn": "Arn", "endpoint_address": "Endpoint.Address", "endpoint_port": "Endpoint.Port"}
		if err != nil || got != "!GetAtt OrdersLedger."+attr[parts.provideKeys[i]] {
			t.Errorf("provides.%s renders %q, %v", parts.provideKeys[i], got, err)
		}
	}

	// The provider assembles it into a stack declaring the secret's parameter
	// and an output for each provides key.
	stack, err := cfncore.AssembleStack([]cfncore.NodeFragment{{NodeID: "orders-ledger", Files: [][]byte{[]byte(text)}}})
	if err != nil {
		t.Fatalf("the provider refuses the stack: %v", err)
	}
	var assembled map[string]any
	if err := json.Unmarshal(stack, &assembled); err != nil {
		t.Fatal(err)
	}
	if got := canon(t, at(assembled, "Parameters", "OrdersLedgerMasterPassword")); got != `{"NoEcho":true,"Type":"String"}` {
		t.Errorf("the stack's parameter %s\n%s", got, stack)
	}
	outputs, _ := at(assembled, "Outputs").(map[string]any)
	if len(outputs) != 3 || !strings.Contains(canon(t, outputs), `{"Value":{"Fn::GetAtt":["OrdersLedger","Endpoint.Address"]}}`) {
		t.Errorf("the stack's outputs %v", outputs)
	}

	// The shared assertions come first, then none of the writer's own.
	if want := []string{"overrides_this_recipe_reads", "secrets_are_parameters"}; !reflect.DeepEqual(parts.assertionKeys, want) {
		t.Errorf("assertions %v, want %v", parts.assertionKeys, want)
	}
}

func TestCloudFormationOptionalFalsePasses(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "ledger.json"), Format, parse)
	noHalt(t, out.Diagnostics)
	parts := partsOf(t, fileOf(t, out, ledgerRecipe))

	values := map[string]any{"identifier": "orders-1", "name": ""}
	_, doc := renderFragment(t, parts, ledgerContext(values, map[string]any{"DeletionProtection": false}))
	props, _ := at(doc, "Resources", "OrdersLedger", "Properties").(map[string]any)
	if v, ok := props["DeletionProtection"]; !ok || v != false {
		t.Errorf("an override set to false is written %v, %v: %v", v, ok, props)
	}
	if v, ok := props["Name"]; !ok || v != "" {
		t.Errorf("a values key set empty is written %v, %v: %v", v, ok, props)
	}
	for _, absent := range []string{"KmsKeyId", "LedgerName", "LegacyMode", "Rules"} {
		if _, ok := props[absent]; ok {
			t.Errorf("%s is written though it is not set: %v", absent, props)
		}
	}

	_, doc = renderFragment(t, parts, ledgerContext(map[string]any{"identifier": "orders-1"}, nil))
	props, _ = at(doc, "Resources", "OrdersLedger", "Properties").(map[string]any)
	if _, ok := props["DeletionProtection"]; ok {
		t.Errorf("an override not set is written: %v", props)
	}
}

func str(name string) *schemadraft.Property {
	return &schemadraft.Property{Name: name, Path: []string{name}, Kinds: []schemadraft.Kind{schemadraft.KindString}}
}

// TestCloudFormationWriteInjectIdentifierUnmapped writes a resource as an
// importer drafting from an estate hands it: inputs on the inject pipe, an
// identifier among its provides and a provides key mapped to nothing.
func TestCloudFormationWriteInjectIdentifierUnmapped(t *testing.T) {
	typ := &schemadraft.Type{Name: "AWS::Example::Queue", PrimaryIdentifier: [][]string{{"QueueUrl"}}}
	inject := func(name, key string, required bool) schemadraft.Input {
		return schemadraft.Input{Property: str(name), Required: required,
			Source: schemadraft.InputSource{Pillar: schemadraft.PillarInject, Key: key, Mechanism: "env"}}
	}
	r := schemadraft.Resource{
		Type:   typ,
		Inputs: []schemadraft.Input{inject("TargetArn", "TARGET_ARN", true), inject("DeadLetterArn", "DLQ_ARN", false)},
		Provides: []schemadraft.Provide{{Key: "queue_url", Attribute: []string{"QueueUrl"}, Identifier: true},
			{Key: "arn", Attribute: []string{"Arn"}}},
	}
	d, ds := Write(r)
	if len(ds) != 0 {
		t.Fatalf("diagnostics %+v", ds)
	}
	var keys, values []string
	for _, e := range d.Provides {
		keys, values = append(keys, e.Key), append(values, e.Value)
	}
	if want := []string{"!Ref {{ .locals.logical_id }}", "!GetAtt {{ .locals.logical_id }}.Arn"}; !reflect.DeepEqual(values, want) ||
		!reflect.DeepEqual(keys, []string{"queue_url", "arn"}) {
		t.Errorf("provides %v %v, want %v", keys, values, want)
	}
	if len(d.Locals) != 1 || d.Locals[0].Key != "logical_id" || d.Locals[0].Value != "{{ capitalizedWords .context.instance_id }}" {
		t.Errorf("locals %+v", d.Locals)
	}
	if len(d.Artifacts) != 1 || d.Artifacts[0].Key != "template.yaml" {
		t.Fatalf("artifacts %+v", d.Artifacts)
	}
	parts := recipeParts{logicalID: d.Locals[0].Value, template: d.Artifacts[0].Value}

	// A neighbour's reference on the pipe lands as the intrinsic, whole.
	ctx := map[string]any{"context": map[string]any{"instance_id": "orders-queue", "inject": map[string]any{
		"env": map[string]any{"TARGET_ARN": "!GetAtt Db.DBInstanceArn"}}}}
	text, doc := renderFragment(t, parts, ctx)
	if got := canon(t, at(doc, "Resources", "OrdersQueue", "Properties")); got !=
		`{"TargetArn":{"Fn::GetAtt":["Db","DBInstanceArn"]}}` {
		t.Errorf("properties %s\n%s", got, text)
	}
	if got := canon(t, at(doc, "provides")); got !=
		`{"arn":{"Fn::GetAtt":["OrdersQueue","Arn"]},"queue_url":{"Ref":"OrdersQueue"}}` {
		t.Errorf("fragment provides %s", got)
	}
	if _, ok := doc["Parameters"]; ok {
		t.Errorf("a fragment with no secret declares parameters:\n%s", text)
	}
	ctx["context"].(map[string]any)["inject"] = map[string]any{"env": map[string]any{"TARGET_ARN": "arn:a",
		"DLQ_ARN": "!Ref Dlq"}}
	_, doc = renderFragment(t, parts, ctx)
	if got := canon(t, at(doc, "Resources", "OrdersQueue", "Properties")); got !=
		`{"DeadLetterArn":{"Ref":"Dlq"},"TargetArn":"arn:a"}` {
		t.Errorf("properties %s", got)
	}

	// An unmapped key halts both where the Recipe publishes it and where the
	// fragment projects it, naming the key.
	r.Unmapped = []schemadraft.Unmapped{{Key: "endpoint", Candidates: []string{"Arn"}}}
	d, _ = Write(r)
	last := d.Provides[len(d.Provides)-1]
	if last.Key != "endpoint" || last.Value != r.Unmapped[0].Value(typ) {
		t.Errorf("unmapped provides %+v", last)
	}
	_, err := render(d.Artifacts[0].Value, map[string]any{"locals": map[string]any{"logical_id": "OrdersQueue"},
		"context": ctx["context"]})
	if err == nil || !strings.Contains(err.Error(), "provides.endpoint is not mapped") {
		t.Errorf("the fragment renders past an unmapped key: %v", err)
	}
}

// TestCloudFormationWriteEmptyProperties writes Properties as an empty object
// where no input is set.
func TestCloudFormationWriteEmptyProperties(t *testing.T) {
	typ := &schemadraft.Type{Name: "AWS::Example::Queue"}
	overrides := func(name string) schemadraft.Input {
		return schemadraft.Input{Property: str(name), Source: schemadraft.InputSource{Pillar: schemadraft.PillarOverrides,
			Key: name}}
	}
	ctx := map[string]any{"context": map[string]any{"instance_id": "q"}, "values": map[string]any{},
		"infra": map[string]any{}}
	for _, inputs := range [][]schemadraft.Input{nil, {overrides("DelaySeconds"), overrides("Visibility")}} {
		d, _ := Write(schemadraft.Resource{Type: typ, Inputs: inputs})
		parts := recipeParts{logicalID: d.Locals[0].Value, template: d.Artifacts[0].Value}
		text, doc := renderFragment(t, parts, ctx)
		if got := canon(t, at(doc, "Resources", "Q", "Properties")); got != "{}" {
			t.Errorf("properties %s\n%s", got, text)
		}
	}
	d, _ := Write(schemadraft.Resource{Type: typ, Inputs: []schemadraft.Input{overrides("DelaySeconds"),
		overrides("Visibility")}})
	parts := recipeParts{logicalID: d.Locals[0].Value, template: d.Artifacts[0].Value}
	ctx["infra"] = map[string]any{"overrides": map[string]any{"Visibility": 0}}
	if _, doc := renderFragment(t, parts, ctx); canon(t, at(doc, "Resources", "Q", "Properties")) != `{"Visibility":0}` {
		t.Errorf("properties %v", at(doc, "Resources", "Q", "Properties"))
	}
}

// TestCloudFormationWriteQuotesNames writes a type or property name a plain
// YAML scalar cannot carry, or one holding template text, so it reads back as
// the vendor wrote it.
func TestCloudFormationWriteQuotesNames(t *testing.T) {
	odd := "Odd: {{ name }}"
	typ := &schemadraft.Type{Name: "Example: {{ type }}"}
	in := schemadraft.Input{Property: str(odd), Required: true,
		Source: schemadraft.InputSource{Pillar: schemadraft.PillarOverrides, Key: odd}}
	d, _ := Write(schemadraft.Resource{Type: typ, Inputs: []schemadraft.Input{in},
		Provides: []schemadraft.Provide{{Key: "odd", Attribute: []string{"Odd {{x}}", "Inner"}}}})
	parts := recipeParts{logicalID: d.Locals[0].Value, template: d.Artifacts[0].Value}
	ctx := map[string]any{"context": map[string]any{"instance_id": "q"},
		"infra": map[string]any{"overrides": map[string]any{odd: "v"}}}
	text, doc := renderFragment(t, parts, ctx)
	if got := at(doc, "Resources", "Q", "Type"); got != typ.Name {
		t.Errorf("type %v\n%s", got, text)
	}
	if got := canon(t, at(doc, "Resources", "Q", "Properties")); got != canon(t, map[string]any{odd: "v"}) {
		t.Errorf("properties %s\n%s", got, text)
	}
	if got := canon(t, at(doc, "provides")); got != `{"odd":{"Fn::GetAtt":["Q","Odd {{x}}.Inner"]}}` {
		t.Errorf("provides %s\n%s", got, text)
	}
	got, err := render(d.Provides[0].Value, map[string]any{"locals": map[string]any{"logical_id": "Q"}})
	if err != nil || got != "!GetAtt Q.Odd {{x}}.Inner" {
		t.Errorf("the Recipe's provides renders %q, %v", got, err)
	}
}

// TestCloudFormationWriteRefusesANestedInput halts resource-type-refused on an
// input at a path of more than one segment, naming it, and drafts nothing,
// since the writer sets each input as a member of Properties under its name.
func TestCloudFormationWriteRefusesANestedInput(t *testing.T) {
	typ := &schemadraft.Type{Name: "AWS::Example::Queue", Source: "queue.json"}
	nested := &schemadraft.Property{Name: "Arn", Path: []string{"RedrivePolicy", "Arn"},
		Kinds: []schemadraft.Kind{schemadraft.KindString}}
	d, ds := Write(schemadraft.Resource{Type: typ, Inputs: []schemadraft.Input{
		{Property: str("QueueName"), Required: true,
			Source: schemadraft.InputSource{Pillar: schemadraft.PillarValues, Key: "queue_name"}},
		{Property: nested, Required: true, Source: schemadraft.InputSource{Pillar: schemadraft.PillarInject,
			Key: "redrive_policy_arn", Mechanism: schemadraft.MechanismProperty}},
	}})
	h := onlyHalt(t, ds, codeRefused)
	if len(ds) != 1 || !strings.Contains(h.Message, "RedrivePolicy.Arn") || h.SourceRef != "queue.json" {
		t.Errorf("diagnostics %+v, want one halt naming RedrivePolicy.Arn", ds)
	}
	if !reflect.DeepEqual(d, importer.RecipeDraft{}) {
		t.Errorf("a halted writer drafted %+v", d)
	}
}
