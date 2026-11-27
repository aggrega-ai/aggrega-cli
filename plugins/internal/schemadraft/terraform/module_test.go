// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest/exportgen"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// The names the tests read, written out rather than taken from the writer, so a
// renamed one fails here as it would for a reader of the draft.
const (
	codeUnconstrained = "provider-version-unconstrained"
	moduleFilePath    = "module/main.tf.json"
	ledgerRecipePath  = "catalog/recipe/terraform/ex-ledger/0.1.0/recipe.yaml"
	ledgerModulePath  = "catalog/recipe/terraform/ex-ledger/0.1.0/module/main.tf.json"
	wantAzurermBlock  = "provider \"azurerm\" {\n  features {}\n}\n"
)

var (
	kString = []schemadraft.Kind{schemadraft.KindString}
	kNumber = []schemadraft.Kind{schemadraft.KindNumber}
	kBool   = []schemadraft.Kind{schemadraft.KindBoolean}
	kArray  = []schemadraft.Kind{schemadraft.KindArray}
	kObject = []schemadraft.Kind{schemadraft.KindObject}
	kAny    = []schemadraft.Kind{schemadraft.KindAny}
)

// prop is an attribute of the kinds given at path, named after its last segment.
func prop(kinds []schemadraft.Kind, path ...string) *schemadraft.Property {
	return &schemadraft.Property{Name: path[len(path)-1], Path: path, Kinds: kinds}
}

// block is a nested block of the nesting mode given at path, holding members,
// shaped as the reader lifts one.
func block(nesting string, path []string, members ...*schemadraft.Property) *schemadraft.Property {
	p := &schemadraft.Property{Name: path[len(path)-1], Path: path, Block: &schemadraft.Block{Nesting: nesting}}
	element := &schemadraft.Property{Name: p.Name, Path: path, Kinds: kObject, Properties: members}
	switch nesting {
	case nestingList, nestingSet:
		p.Kinds, p.Items = kArray, element
	case nestingMap:
		p.Kinds, p.Open, p.Items = kObject, true, element
	default:
		p.Kinds, p.Properties = kObject, members
	}
	return p
}

// in is the input of p read from pillar under key.
func in(p *schemadraft.Property, pillar schemadraft.Pillar, key string, required bool) schemadraft.Input {
	return schemadraft.Input{Property: p, Required: required, Secret: pillar == schemadraft.PillarSecret,
		Source: schemadraft.InputSource{Pillar: pillar, Key: key}}
}

// injected is the input of p read off the inject pipe's mechanism under key.
func injected(p *schemadraft.Property, mechanism, key string, required bool) schemadraft.Input {
	return schemadraft.Input{Property: p, Required: required,
		Source: schemadraft.InputSource{Pillar: schemadraft.PillarInject, Key: key, Mechanism: mechanism}}
}

// address is the provider address host/namespace/type at the version given.
func address(host, namespace, typ, version string) schemadraft.ProviderAddress {
	return schemadraft.ProviderAddress{Host: host, Namespace: namespace, Type: typ, Version: version}
}

// moduleOf is a module of the resource type name, of the provider at a, with
// inputs and outputs.
func moduleOf(name string, a schemadraft.ProviderAddress, inputs []schemadraft.Input, outputs ...schemadraft.Provide) Module {
	t := &schemadraft.Type{Name: name, Source: "export.json", Provider: &a}
	for _, i := range inputs {
		t.Properties = append(t.Properties, i.Property)
	}
	return Module{Provider: a, Type: t, Inputs: inputs, Outputs: outputs}
}

// written drafts m, which must raise no halt.
func written(t *testing.T, m Module) (importer.RecipeDraft, []aggrega.Diagnostic) {
	t.Helper()
	d, diags := WriteModule(m)
	if halts := haltsOf(diags); len(halts) != 0 {
		t.Fatalf("writing raised %+v", halts)
	}
	return d, diags
}

// mainTF reads the module/main.tf.json d writes beside its Recipe.
func mainTF(t *testing.T, d importer.RecipeDraft) map[string]any {
	t.Helper()
	if len(d.Files) != 1 || d.Files[0].Path != moduleFilePath {
		t.Fatalf("the files beside the Recipe are %+v, want %s alone", d.Files, moduleFilePath)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(d.Files[0].Content), &doc); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", moduleFilePath, err, d.Files[0].Content)
	}
	return doc
}

// at is the value at the keys path below doc, nil where there is none.
func at(doc any, path ...string) any {
	v := doc
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

// keysOf lists the keys of the object at path below doc, sorted.
func keysOf(doc any, path ...string) []string {
	m, _ := at(doc, path...).(map[string]any)
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
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

// canonText is the JSON text canonicalized as canon writes it.
func canonText(t *testing.T, text string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return canon(t, v)
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

// mappingContext is the context a valuesMapping renders in for the instance
// orders.
func mappingContext(values, infra, inject, features map[string]any) map[string]any {
	return map[string]any{
		"context":  map[string]any{"instance_id": "orders", "inject": inject},
		"values":   values,
		"infra":    infra,
		"features": features,
	}
}

// renderMapping evaluates the valuesMapping text as the compiler's values
// mapping surface does — the compiler's template functions, an absent key read
// as zero — over ctx, and reads its output as the YAML map of module arguments.
func renderMapping(t *testing.T, text string, ctx map[string]any) (map[string]any, error) {
	t.Helper()
	tmpl, err := template.New("valuesMapping").Option("missingkey=zero").Funcs(gotmpl.FuncMap()).Parse(text)
	if err != nil {
		t.Fatalf("the valuesMapping does not parse: %v\n%s", err, text)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, ctx); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal([]byte(b.String()), &out); err != nil {
		t.Fatalf("the valuesMapping renders text that is not YAML: %v\n%s", err, b.String())
	}
	return out, nil
}

// vaultModule is a resource of the synthetic type vault_store: a required and
// an optional attribute, a required secret, a list block holding a nested
// single block, an optional single block with a member read back, and
// attributes read back, each published.
func vaultModule() Module {
	name := prop(kString, "name")
	name.Required = true
	capacity := prop(kNumber, "capacity")
	password := prop(kString, "password")
	password.Required, password.Sensitive = true, true
	days := prop(kNumber, "retention", "days")
	days.Required = true
	archive := block(nestingSingle, []string{"retention", "archive"}, prop(kString, "retention", "archive", "target"))
	retention := block(nestingList, []string{"retention"}, archive, days)
	retention.Required, retention.Block.MinItems = true, 1
	opened := prop(kString, "window", "opened")
	opened.ReadOnly = true
	window := block(nestingSingle, []string{"window"}, opened, prop(kString, "window", "start"))

	m := moduleOf("vault_store", address(terraformHost, "example", "vault", ""), []schemadraft.Input{
		in(name, schemadraft.PillarValues, "name", true),
		in(capacity, schemadraft.PillarOverrides, "capacity", false),
		in(password, schemadraft.PillarSecret, "password", true),
		in(retention, schemadraft.PillarInfra, "retention", true),
		in(window, schemadraft.PillarOverrides, "window", false),
	}, schemadraft.Provide{Key: "arn", Attribute: []string{"arn"}},
		schemadraft.Provide{Key: "endpoint", Attribute: []string{"endpoint"}})
	for _, n := range []string{"arn", "endpoint"} {
		p := prop(kString, n)
		p.ReadOnly = true
		m.Type.Properties = append(m.Type.Properties, p)
	}
	return m
}

// TestModuleGolden drafts vault_store as a local module and compares the module
// and the Recipe with their goldens.
func TestModuleGolden(t *testing.T) {
	m := vaultModule()
	d, diags := written(t, m)
	if d.Source != "./module" {
		t.Errorf("the source is %q, want ./module", d.Source)
	}
	if len(diags) != 1 || diags[0].Code != codeUnconstrained || diags[0].Severity != "warn" {
		t.Errorf("diagnostics %+v, want the one warning %s", diags, codeUnconstrained)
	}
	doc := mainTF(t, d)

	// The keys are sorted: the text is what encoding the parsed document again
	// with sorted keys writes.
	var again bytes.Buffer
	enc := json.NewEncoder(&again)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	if again.String() != d.Files[0].Content {
		t.Errorf("%s does not hold its keys sorted:\n%s", moduleFilePath, d.Files[0].Content)
	}
	if want := []string{"output", "resource", "terraform", "variable"}; !slices.Equal(keysOf(doc), want) {
		t.Errorf("the module holds %v, want %v", keysOf(doc), want)
	}
	if want := []string{"arn", "endpoint"}; !slices.Equal(keysOf(doc, "output"), want) {
		t.Errorf("the outputs are %v, want %v", keysOf(doc, "output"), want)
	}
	if got := at(doc, "output", "arn"); canon(t, got) != `{"value":"${vault_store.this.arn}"}` {
		t.Errorf("output.arn is %s", canon(t, got))
	}
	var keys, values []string
	for _, e := range d.Provides {
		keys, values = append(keys, e.Key), append(values, e.Value)
	}
	if want := []string{"${module.{{ .context.instance_id }}.arn}", "${module.{{ .context.instance_id }}.endpoint}"}; !slices.Equal(values, want) || !slices.Equal(keys, []string{"arn", "endpoint"}) {
		t.Errorf("provides %v %v, want arn and endpoint as %v", keys, values, want)
	}

	d.Name, d.Version, d.Capability, d.Implements = "vault-store", "0.1.0", "terraform", "imported/component/vault-store:0.1.0"
	golden(t, "vault.main.tf.json", d.Files[0].Content)
	golden(t, "vault.recipe.yaml", importer.DraftRecipe(d).Entity.Body)

	// The valuesMapping fills every variable that has a value, a structured
	// one whole.
	ctx := mappingContext(map[string]any{"name": "orders"},
		map[string]any{"retention": []any{map[string]any{"days": 7, "archive": map[string]any{"target": "cold"}}},
			"overrides": map[string]any{"capacity": 0}},
		map[string]any{"env": map[string]any{"value": "origin-of-the-password"}},
		map[string]any{"password": map[string]any{"values": map[string]any{"key": "value"}}})
	got, err := renderMapping(t, d.ValuesMapping, ctx)
	if err != nil {
		t.Fatalf("the valuesMapping halts: %v", err)
	}
	want := `{"capacity":0,"name":"orders","password":"origin-of-the-password","retention":[{"archive":{"target":"cold"},"days":7}]}`
	if canon(t, got) != want {
		t.Errorf("the valuesMapping renders %s, want %s", canon(t, got), want)
	}
}

// TestModuleImportGolden imports ex_ledger and compares its Recipe and module
// with their goldens: blocks of each nesting mode, a nested-type attribute and a
// secret on a featuresSchema slot, as the reader lifts them.
func TestModuleImportGolden(t *testing.T) {
	input := bytesInput("ledger.json", fixture(t, "ledger.json"))
	input.Settings = json.RawMessage(`{"resource_types":["ex_ledger"]}`)
	out := schemadraft.Import(input, Format, parse)
	if halts := haltsOf(out.Diagnostics); len(halts) != 0 {
		t.Fatalf("the import halts: %+v", halts)
	}
	var paths []string
	for _, f := range out.Files {
		paths = append(paths, f.Path)
	}
	want := []string{"catalog/component/imported/component/ex-ledger/0.1.0/component.yaml", ledgerRecipePath, ledgerModulePath}
	if !slices.Equal(paths, want) {
		t.Fatalf("files %v, want %v", paths, want)
	}
	content := func(path string) string {
		for _, f := range out.Files {
			if f.Path == path {
				return f.Content
			}
		}
		return ""
	}
	golden(t, "ledger.recipe.yaml", content(ledgerRecipePath))
	golden(t, "ledger.main.tf.json", content(ledgerModulePath))

	unconstrained := 0
	for _, d := range out.Diagnostics {
		if d.Code == codeUnconstrained {
			unconstrained++
			if !strings.Contains(d.Message, "example/ledgers") || !strings.Contains(d.Message, "terraform.required_providers.ledgers.version") {
				t.Errorf("the warning does not name the provider and where its version goes: %s", d.Message)
			}
		}
	}
	if unconstrained != 1 {
		t.Errorf("%d warnings %s, want one: %+v", unconstrained, codeUnconstrained, out.Diagnostics)
	}

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
	if _, ok := cat.Recipes["terraform/ex-ledger:0.1.0"]; !ok {
		t.Errorf("the catalog holds no drafted Recipe")
	}

	if again := schemadraft.Import(input, Format, parse); !reflect.DeepEqual(out, again) {
		t.Errorf("two imports differ")
	}
}

func TestModuleVariableTypes(t *testing.T) {
	strings2 := prop(kArray, "rows")
	strings2.Items = prop(kArray, "rows")
	strings2.Items.Items = prop(kString, "rows")
	set := prop(kArray, "zones")
	set.Items = prop(kString, "zones")
	tags := prop(kObject, "tags")
	tags.Open, tags.Dropped = true, []string{"map element type: not carried"}
	pair := prop(kArray, "pair")
	pair.Dropped = []string{"tuple element types: not carried"}
	limits := prop(kObject, "limits")
	limits.Properties = []*schemadraft.Property{prop(kNumber, "limits", "rate")}
	limits.Dropped = []string{"object required attributes: not carried"}
	objects := prop(kArray, "rules")
	objects.Items = prop(kObject, "rules")
	objects.Items.Properties = []*schemadraft.Property{prop(kString, "rules", "action")}
	union := prop([]schemadraft.Kind{schemadraft.KindString, schemadraft.KindNumber}, "either")
	nested := block(nestingList, []string{"settings"}, prop(kString, "settings", "level"))
	nested.Block.Attribute = true

	want := map[string]string{
		"name": "string", "capacity": "number", "count_of": "number", "enabled": "bool", "payload": "any",
		"zones": "list(string)", "rows": "list(list(string))", "tags": "any", "pair": "any", "limits": "any",
		"rules": "any", "either": "any", "window": "any", "settings": "any",
	}
	integer := prop([]schemadraft.Kind{schemadraft.KindInteger}, "count_of")
	var inputs []schemadraft.Input
	for _, p := range []*schemadraft.Property{prop(kString, "name"), prop(kNumber, "capacity"), integer,
		prop(kBool, "enabled"), prop(kAny, "payload"), set, strings2, tags, pair, limits, objects, union,
		block(nestingSingle, []string{"window"}, prop(kString, "window", "start")), nested} {
		inputs = append(inputs, in(p, schemadraft.PillarOverrides, p.Name, false))
	}
	d, _ := written(t, moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), inputs))
	doc := mainTF(t, d)
	for name, typ := range want {
		if got := at(doc, "variable", name, "type"); got != typ {
			t.Errorf("variable %s has the type %v, want %s", name, got, typ)
		}
	}
}

func TestValuesMappingWritesOnlyInputsWithValues(t *testing.T) {
	name := prop(kString, "name")
	name.Required = true
	subnet := prop(kString, "subnet_group")
	subnet.Required = true
	m := moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), []schemadraft.Input{
		in(name, schemadraft.PillarValues, "name", true),
		in(prop(kString, "label"), schemadraft.PillarValues, "label", false),
		in(subnet, schemadraft.PillarInfra, "subnet_group", true),
		in(prop(kString, "zone"), schemadraft.PillarInfra, "zone", false),
		in(prop(kBool, "deletion_protection"), schemadraft.PillarOverrides, "deletion_protection", false),
		in(prop(kString, "kms_key_id"), schemadraft.PillarOverrides, "kms_key_id", false),
	})
	d, diags := written(t, m)
	if len(diags) != 0 {
		t.Errorf("a provider with a version raises %+v", diags)
	}
	doc := mainTF(t, d)
	for _, v := range []string{"label", "zone", "deletion_protection", "kms_key_id"} {
		if def, ok := at(doc, "variable", v).(map[string]any)["default"]; !ok || def != nil {
			t.Errorf("the optional variable %s has the default %v, %v; want null", v, def, ok)
		}
	}
	for _, v := range []string{"name", "subnet_group"} {
		if _, ok := at(doc, "variable", v).(map[string]any)["default"]; ok {
			t.Errorf("the required variable %s has a default", v)
		}
	}
	for _, v := range []string{"name", "label", "subnet_group", "zone", "deletion_protection", "kms_key_id"} {
		if got := at(doc, "resource", "vault_store", "this", v); got != "${var."+v+"}" {
			t.Errorf("the attribute %s is %v", v, got)
		}
	}

	for _, tc := range []struct {
		name          string
		values, infra map[string]any
		want          string
	}{
		{"only the required inputs set",
			map[string]any{"name": "orders"}, map[string]any{"subnet_group": "private-a"},
			`{"name":"orders","subnet_group":"private-a"}`},
		{"an override set to false and a values key set empty",
			map[string]any{"name": "", "label": "x"},
			map[string]any{"subnet_group": "private-a", "overrides": map[string]any{"deletion_protection": false}},
			`{"deletion_protection":false,"label":"x","name":"","subnet_group":"private-a"}`},
		{"every input set",
			map[string]any{"name": "orders", "label": "x"},
			map[string]any{"subnet_group": "private-a", "zone": "z1",
				"overrides": map[string]any{"deletion_protection": true, "kms_key_id": "${module.key.arn}"}},
			`{"deletion_protection":true,"kms_key_id":"$${module.key.arn}","label":"x","name":"orders","subnet_group":"private-a","zone":"z1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderMapping(t, d.ValuesMapping, mappingContext(tc.values, tc.infra, nil, nil))
			if err != nil {
				t.Fatalf("the valuesMapping halts: %v\n%s", err, d.ValuesMapping)
			}
			if canon(t, got) != tc.want {
				t.Errorf("the valuesMapping renders %s, want %s\n%s", canon(t, got), tc.want, d.ValuesMapping)
			}
		})
	}

	// A required input absent halts naming it.
	_, err := renderMapping(t, d.ValuesMapping, mappingContext(map[string]any{}, map[string]any{"subnet_group": "a"}, nil, nil))
	if err == nil || !strings.Contains(err.Error(), `"name"`) {
		t.Errorf("an absent required input renders past: %v", err)
	}
}

func TestValuesMappingReadsInject(t *testing.T) {
	m := moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), []schemadraft.Input{
		injected(prop(kString, "target_arn"), "env", "TARGET_ARN", true),
		injected(prop(kString, "dead_letter_arn"), "env", "DLQ_ARN", false),
		injected(prop(kObject, "tags"), "all", "TAGS", false),
	})
	d, _ := written(t, m)
	for _, read := range []string{`.context.inject) "env" "TARGET_ARN"`, `.context.inject) "env" "DLQ_ARN"`, `.context.inject) "all" "TAGS"`} {
		if !strings.Contains(d.ValuesMapping, read) {
			t.Errorf("the valuesMapping does not read %s:\n%s", read, d.ValuesMapping)
		}
	}

	// A neighbour's published reference passes whole, in a map and in text.
	ctx := mappingContext(nil, nil, map[string]any{
		"env": map[string]any{"TARGET_ARN": "${module.db.arn}"},
		"all": map[string]any{"TAGS": map[string]any{"address": "${module.db.address}", "spliced": "db-at-${module.db.address}"}},
	}, nil)
	got, err := renderMapping(t, d.ValuesMapping, ctx)
	if err != nil {
		t.Fatalf("the valuesMapping halts: %v", err)
	}
	if want := `{"tags":{"address":"${module.db.address}","spliced":"db-at-${module.db.address}"},"target_arn":"${module.db.arn}"}`; canon(t, got) != want {
		t.Errorf("the valuesMapping renders %s, want %s", canon(t, got), want)
	}

	ctx["context"].(map[string]any)["inject"] = map[string]any{"env": map[string]any{"TARGET_ARN": "a", "DLQ_ARN": "b"}}
	if got, err := renderMapping(t, d.ValuesMapping, ctx); err != nil || canon(t, got) != `{"dead_letter_arn":"b","target_arn":"a"}` {
		t.Errorf("the valuesMapping renders %v, %v", got, err)
	}
}

// TestValuesMappingReadsSecrets reads a secret where the attachment on its slot
// delivers it on the env mechanism, under the key the attachment names, and
// halts naming the slot where the attachment delivers nothing there.
func TestValuesMappingReadsSecrets(t *testing.T) {
	password := prop(kString, "password")
	password.Required, password.Sensitive = true, true
	apiKey := prop(kString, "api_key")
	apiKey.Sensitive = true
	m := moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), []schemadraft.Input{
		in(password, schemadraft.PillarSecret, "master_password", true),
		in(apiKey, schemadraft.PillarSecret, "api_key", false),
	})
	d, _ := written(t, m)
	doc := mainTF(t, d)
	if got := canon(t, at(doc, "variable", "password")); got != `{"sensitive":true,"type":"string"}` {
		t.Errorf("the required secret's variable is %s", got)
	}
	if got := canon(t, at(doc, "variable", "api_key")); got != `{"default":null,"sensitive":true,"type":"string"}` {
		t.Errorf("the optional secret's variable is %s", got)
	}
	if strings.Contains(d.Files[0].Content, "origin") {
		t.Errorf("the module holds a value")
	}
	if !strings.Contains(d.ValuesMapping, `{{- with (optional . "features" dict).api_key }}`) {
		t.Errorf("the optional secret is not read inside its slot:\n%s", d.ValuesMapping)
	}

	secretValue := func(key string) map[string]any { return map[string]any{"values": map[string]any{"key": key}} }
	// core/secret-value delivers its origin verbatim on env under the key its
	// values name: value where unwritten.
	ctx := mappingContext(nil, nil, map[string]any{
		"env": map[string]any{"value": "release-origin", "OTHER_KEY": "aggregate-origin"},
		"all": map[string]any{"value": "release-origin", "OTHER_KEY": "aggregate-origin"},
	}, map[string]any{"master_password": secretValue("value"), "api_key": secretValue("OTHER_KEY")})
	got, err := renderMapping(t, d.ValuesMapping, ctx)
	if err != nil {
		t.Fatalf("the valuesMapping halts: %v\n%s", err, d.ValuesMapping)
	}
	if want := `{"api_key":"aggregate-origin","password":"release-origin"}`; canon(t, got) != want {
		t.Errorf("the valuesMapping renders %s, want %s", canon(t, got), want)
	}

	// An optional slot holding no attachment leaves its variable unset.
	delete(ctx["features"].(map[string]any), "api_key")
	if got, err := renderMapping(t, d.ValuesMapping, ctx); err != nil || canon(t, got) != `{"password":"release-origin"}` {
		t.Errorf("the valuesMapping renders %v, %v", got, err)
	}

	// An attachment delivering nothing on env, as core/random-password does,
	// halts naming the slot and the mechanism.
	for _, slot := range []string{"master_password", "api_key"} {
		features := map[string]any{"master_password": secretValue("value"), "api_key": secretValue("value")}
		features[slot] = map[string]any{"values": map[string]any{"key": "value"},
			"provides": map[string]any{"name": "module.db--master_password", "key": "value"}}
		inject := map[string]any{"env": map[string]any{"value": "release-origin"}}
		if slot == "master_password" {
			inject = map[string]any{}
		} else {
			features[slot].(map[string]any)["values"] = map[string]any{"key": "ABSENT"}
		}
		_, err := renderMapping(t, d.ValuesMapping, mappingContext(nil, nil, inject, features))
		if err == nil || !strings.Contains(err.Error(), "slot "+slot) || !strings.Contains(err.Error(), "env") ||
			!strings.Contains(err.Error(), "core/secret-value") {
			t.Errorf("an attachment on %s delivering nothing renders past, or halts unnamed: %v", slot, err)
		}
	}
}

func TestReservedAttributeNamesGetDistinctVariables(t *testing.T) {
	var inputs []schemadraft.Input
	for _, n := range []string{"provider", "source", "source_value", "version"} {
		inputs = append(inputs, in(prop(kString, n), schemadraft.PillarOverrides, n, false))
	}
	d, _ := written(t, moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), inputs))
	doc := mainTF(t, d)
	if want := []string{"provider_value", "source_value", "source_value_2", "version_value"}; !slices.Equal(keysOf(doc, "variable"), want) {
		t.Errorf("the variables are %v, want %v", keysOf(doc, "variable"), want)
	}
	for attr, v := range map[string]string{"provider": "provider_value", "source": "source_value_2",
		"source_value": "source_value", "version": "version_value"} {
		if got := at(doc, "resource", "vault_store", "this", attr); got != "${var."+v+"}" {
			t.Errorf("the attribute %s is %v, want ${var.%s}", attr, got, v)
		}
	}
	ctx := mappingContext(nil, map[string]any{"overrides": map[string]any{"provider": "p", "source": "s",
		"source_value": "sv", "version": "v"}}, nil, nil)
	got, err := renderMapping(t, d.ValuesMapping, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"provider_value":"p","source_value":"sv","source_value_2":"s","version_value":"v"}`; canon(t, got) != want {
		t.Errorf("the module arguments are %s, want %s", canon(t, got), want)
	}
	for _, line := range []string{"source_value_2", "version_value", "provider_value"} {
		if !strings.Contains(strings.Join(d.Header, "\n"), line) {
			t.Errorf("the header does not name the variable %s: %q", line, d.Header)
		}
	}

	// Every name Terraform and OpenTofu reserve for a module argument.
	for _, name := range []string{"source", "version", "providers", "count", "for_each", "lifecycle", "depends_on",
		"locals", "provider"} {
		if got := VariableName(name, map[string]bool{}); got != name+"_value" {
			t.Errorf("VariableName(%q) is %q, want %s_value", name, got, name)
		}
	}
	used := map[string]bool{}
	for _, tc := range []struct{ vendor, want string }{
		{"name", "name"}, {"name", "name_2"}, {"count", "count_value"}, {"count_value", "count_value_2"},
	} {
		if got := VariableName(tc.vendor, used); got != tc.want {
			t.Errorf("VariableName(%q) is %q, want %q", tc.vendor, got, tc.want)
		}
	}
}

func TestRequiredProvidersSource(t *testing.T) {
	for _, tc := range []struct {
		a      schemadraft.ProviderAddress
		local  string
		source string
	}{
		{address(terraformHost, "hashicorp", "aws", ""), "aws", "hashicorp/aws"},
		{address(opentofuHost, "hashicorp", "aws", ""), "aws", "hashicorp/aws"},
		{address("mirror.example.com", "team", "aws", ""), "aws", "mirror.example.com/team/aws"},
	} {
		d, diags := written(t, moduleOf("aws_thing", tc.a, []schemadraft.Input{in(prop(kString, "name"), schemadraft.PillarValues, "name", true)}))
		doc := mainTF(t, d)
		if got := canon(t, at(doc, "terraform", "required_providers")); got != `{"`+tc.local+`":{"source":"`+tc.source+`"}}` {
			t.Errorf("%+v requires %s, want %s from %s alone", tc.a, got, tc.local, tc.source)
		}
		if len(diags) != 1 || diags[0].Code != codeUnconstrained || !strings.Contains(diags[0].Message, tc.source) ||
			!strings.Contains(diags[0].Message, "terraform.required_providers.aws.version") ||
			!strings.Contains(diags[0].Message, moduleFilePath) {
			t.Errorf("%+v warns %+v, want %s naming %s and where its version goes", tc.a, diags, codeUnconstrained, tc.source)
		}
		if _, ok := at(doc, "resource", "aws_thing", "this").(map[string]any)["provider"]; ok {
			t.Errorf("a resource of its provider's own prefix names its provider")
		}
	}

	// A version the source states is written, and nothing warns.
	d, diags := written(t, moduleOf("aws_thing", address(terraformHost, "hashicorp", "aws", "6.73.0"), nil))
	if got := canon(t, at(mainTF(t, d), "terraform", "required_providers", "aws")); got != `{"source":"hashicorp/aws","version":"6.73.0"}` || len(diags) != 0 {
		t.Errorf("a versioned provider is required as %s with %+v", got, diags)
	}

	// A resource whose name does not open with its provider's name is bound to
	// it, as a type the beta sibling alone carries is.
	d, _ = written(t, moduleOf("google_thing", address(terraformHost, "hashicorp", "google-beta", "7.0.0"), nil))
	doc := mainTF(t, d)
	if got := at(doc, "resource", "google_thing", "this", "provider"); got != "google-beta" {
		t.Errorf("the resource is bound to %v, want google-beta", got)
	}
	if got := canon(t, at(doc, "terraform", "required_providers")); got != `{"google-beta":{"source":"hashicorp/google-beta","version":"7.0.0"}}` {
		t.Errorf("the module requires %s", got)
	}
}

func TestModuleAzurermBoundary(t *testing.T) {
	inputs := []schemadraft.Input{in(prop(kString, "name"), schemadraft.PillarValues, "name", true)}
	d, _ := written(t, moduleOf("azurerm_thing", address(terraformHost, "hashicorp", "azurerm", "4.0.0"), inputs))
	want := []importer.Entry{{Key: "provider-azurerm.tf", Value: wantAzurermBlock}}
	if len(d.BoundaryArtifacts) != 1 || d.BoundaryArtifacts[0].Key != want[0].Key || d.BoundaryArtifacts[0].Value != want[0].Value {
		t.Errorf("the boundary artifacts are %+v, want %+v", d.BoundaryArtifacts, want)
	}
	d, _ = written(t, moduleOf("aws_thing", address(terraformHost, "hashicorp", "aws", "6.0.0"), inputs))
	if len(d.BoundaryArtifacts) != 0 {
		t.Errorf("an aws module writes the boundary artifacts %+v", d.BoundaryArtifacts)
	}
}

// TestModuleMapAndGroupNesting writes a map of blocks keyed by the map's keys,
// a group block as a single one, a nested-type attribute as an argument, a map
// block nested in a list block over the iterator of its depth, and leaves out a
// block the reader cut.
func TestModuleMapAndGroupNesting(t *testing.T) {
	label := block(nestingMap, []string{"label"}, prop(kString, "label", "value"))
	stage := block(nestingGroup, []string{"stage"}, prop(kNumber, "stage", "order"))
	settings := block(nestingList, []string{"settings"}, prop(kString, "settings", "level"))
	settings.Block.Attribute = true
	deep := &schemadraft.Property{Name: "deep", Path: []string{"rules", "deep"}, Kinds: kObject, Open: true, Cut: "depth",
		Block: &schemadraft.Block{Nesting: nestingList}}
	cutAttr := &schemadraft.Property{Name: "shape", Path: []string{"rules", "shape"}, Kinds: kObject, Open: true, Cut: "depth"}
	tags := block(nestingMap, []string{"rules", "tags"}, prop(kString, "rules", "tags", "v"))
	rules := block(nestingSet, []string{"rules"}, deep, cutAttr, tags)
	d, _ := written(t, moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), []schemadraft.Input{
		in(label, schemadraft.PillarOverrides, "label", false),
		in(stage, schemadraft.PillarOverrides, "stage", false),
		in(settings, schemadraft.PillarOverrides, "settings", false),
		in(rules, schemadraft.PillarOverrides, "rules", false),
	}))
	doc := mainTF(t, d)
	body := at(doc, "resource", "vault_store", "this")
	for path, want := range map[string]string{
		"label": `{"content":{"value":"${try(it1.value.value, null)}"},"for_each":"${merge(var.label)}","iterator":"it1","labels":["${it1.key}"]}`,
		"stage": `{"content":{"order":"${try(it1.value.order, null)}"},"for_each":"${var.stage[*]}","iterator":"it1"}`,
		"rules": `{"content":{"dynamic":{"tags":{"content":{"v":"${try(it2.value.v, null)}"},"for_each":"${merge(try(it1.value.tags, null))}","iterator":"it2","labels":["${it2.key}"]}},` +
			`"shape":"${try(it1.value.shape, null)}"},"for_each":"${var.rules[*]}","iterator":"it1"}`,
	} {
		if got := canon(t, at(body, "dynamic", path)); got != want {
			t.Errorf("dynamic.%s is\n%s\nwant\n%s", path, got, want)
		}
	}
	if got := at(body, "settings"); got != "${var.settings}" {
		t.Errorf("the nested-type attribute is written %v, want an argument", got)
	}
	if at(body, "dynamic", "settings") != nil {
		t.Errorf("the nested-type attribute is written as a block")
	}
	for _, v := range []string{"label", "stage", "settings", "rules"} {
		if got := at(doc, "variable", v, "type"); got != "any" {
			t.Errorf("variable %s has the type %v, want any", v, got)
		}
	}
	if !strings.Contains(strings.Join(d.Header, "\n"), "rules.deep") {
		t.Errorf("the header does not name the block left out: %q", d.Header)
	}
}

// TestModuleWriteUnmappedProvides writes a resource as the core hands it: each
// provides key mapped to no attribute halts the build naming it, and the module
// declares no output for it.
func TestModuleWriteUnmappedProvides(t *testing.T) {
	typ := &schemadraft.Type{Name: "vault_store", Provider: &schemadraft.ProviderAddress{Host: terraformHost,
		Namespace: "example", Type: "vault", Version: "1.0.0"}}
	r := schemadraft.Resource{Type: typ, Provides: []schemadraft.Provide{{Key: "arn", Attribute: []string{"arn"}}},
		Unmapped: []schemadraft.Unmapped{{Key: "endpoint", Candidates: []string{"arn"}}}}
	d, diags := Write(r)
	if len(diags) != 0 {
		t.Errorf("diagnostics %+v", diags)
	}
	if len(d.Provides) != 2 || d.Provides[1].Key != "endpoint" || d.Provides[1].Value != r.Unmapped[0].Value(typ) {
		t.Errorf("provides %+v, want arn then the unmapped endpoint", d.Provides)
	}
	if got := keysOf(mainTF(t, d), "output"); !slices.Equal(got, []string{"arn"}) {
		t.Errorf("the outputs are %v, want arn alone", got)
	}
	tmpl := template.Must(template.New("provides").Funcs(gotmpl.FuncMap()).Parse(d.Provides[1].Value))
	if err := tmpl.Execute(&strings.Builder{}, map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "provides.endpoint is not mapped") {
		t.Errorf("the unmapped key evaluates past: %v", err)
	}
}

// draftedModule imports the one type body holds and returns its drafted
// Recipe's text and module.
func draftedModule(t *testing.T, body []byte) (string, map[string]any) {
	t.Helper()
	out := schemadraft.Import(bytesInput("export.json", body), Format, parse)
	if halts := haltsOf(out.Diagnostics); len(halts) != 0 {
		t.Fatalf("the import halts: %+v", halts)
	}
	var recipe string
	var doc map[string]any
	for _, f := range out.Files {
		switch {
		case strings.HasSuffix(f.Path, "/recipe.yaml"):
			recipe = f.Content
		case strings.HasSuffix(f.Path, "/"+moduleFilePath):
			if err := json.Unmarshal([]byte(f.Content), &doc); err != nil {
				t.Fatal(err)
			}
		}
	}
	if recipe == "" || doc == nil {
		t.Fatalf("the import drafts no Recipe with a module: %+v", out.Files)
	}
	return recipe, doc
}

// TestModuleLeavesTheLegacyIDUnset reads back the id a provider built on the
// Terraform plugin SDK declares optional and computed on every resource, and
// refuses in configuration: the module publishes it and never sets it, while an
// id nested in a block is set as any member is.
func TestModuleLeavesTheLegacyIDUnset(t *testing.T) {
	_, doc := draftedModule(t, []byte(`{"format_version":"1.0","provider_schemas":{"registry.terraform.io/hashicorp/aws":`+
		`{"resource_schemas":{"aws_thing":{"version":0,"block":{"attributes":{`+
		`"id":{"type":"string","optional":true,"computed":true},"name":{"type":"string","required":true}},`+
		`"block_types":{"rule":{"nesting_mode":"list","block":{"attributes":{`+
		`"id":{"type":"string","optional":true,"computed":true}}}}}}}}}}}`))
	if got := keysOf(doc, "variable"); !slices.Equal(got, []string{"name", "rule"}) {
		t.Errorf("the variables are %v, want name and rule", got)
	}
	if got := at(doc, "resource", "aws_thing", "this", "id"); got != nil {
		t.Errorf("the module sets id to %v", got)
	}
	if got := canon(t, at(doc, "output")); got != `{"id":{"value":"${aws_thing.this.id}"}}` {
		t.Errorf("the outputs are %s, want id", got)
	}
	if got := at(doc, "resource", "aws_thing", "this", "dynamic", "rule", "content", "id"); got != "${try(it1.value.id, null)}" {
		t.Errorf("the id nested in rule is set to %v", got)
	}
}

// TestModuleLeavesOutACutBlock drafts blocks nested deeper than the reader
// reads: each block down to the cut is set, the block cut is not, and the
// Recipe's header names it.
func TestModuleLeavesOutACutBlock(t *testing.T) {
	recipe, doc := draftedModule(t, exportgen.Generate(exportgen.Spec{Providers: []string{terraformHost + "/example/deep"},
		TypesPerProvider: 1, AttributesPerType: 2, BlockDepth: schemadraft.MaxDepth + 1}))
	content := at(doc, "resource", "deep_resource_0000", "this")
	var path []string
	for depth := 1; depth <= schemadraft.MaxDepth; depth++ {
		name := fmt.Sprintf("nested_%d", depth)
		path = append(path, name)
		block := at(content, "dynamic", name)
		if block == nil {
			t.Fatalf("the block %s is not set", strings.Join(path, "."))
		}
		if got := at(block, "iterator"); got != fmt.Sprintf("it%d", depth) {
			t.Errorf("the block %s iterates as %v", name, got)
		}
		content = at(block, "content")
	}
	if got := at(content, "dynamic"); got != nil {
		t.Errorf("the block cut is set: %v", got)
	}
	cut := strings.Join(append(path, fmt.Sprintf("nested_%d", schemadraft.MaxDepth+1)), ".")
	if !strings.Contains(recipe, "does not set the block "+cut+":") {
		t.Errorf("the header does not name the block cut, %s:\n%s", cut, recipe)
	}
}

// TestModuleSensitiveOutput declares sensitive the output of an attribute the
// vendor marks sensitive, as Terraform requires of an output reading one, and
// no other.
func TestModuleSensitiveOutput(t *testing.T) {
	m := moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), nil,
		schemadraft.Provide{Key: "token", Attribute: []string{"token"}},
		schemadraft.Provide{Key: "arn", Attribute: []string{"arn"}})
	token, arn := prop(kString, "token"), prop(kString, "arn")
	token.ReadOnly, token.Sensitive, arn.ReadOnly = true, true, true
	m.Type.Properties = []*schemadraft.Property{arn, token}
	d, _ := written(t, m)
	want := `{"arn":{"value":"${vault_store.this.arn}"},"token":{"sensitive":true,"value":"${vault_store.this.token}"}}`
	if got := canon(t, at(mainTF(t, d), "output")); got != want {
		t.Errorf("the outputs are %s, want %s", got, want)
	}
}

// TestModuleOutputNamesDoNotCollide publishes a nested attribute, a.b, whose
// path joined with underscores is the name of a top-level attribute, a_b. Listed
// in either order, the top-level attribute keeps its output name and the nested
// one takes the next name free, each provides value reads its own output, and
// the Header names the output that does not take its path's name.
func TestModuleOutputNamesDoNotCollide(t *testing.T) {
	for _, nestedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested first %v", nestedFirst), func(t *testing.T) {
			top := schemadraft.Provide{Key: "a_b", Attribute: []string{"a_b"}}
			nested := schemadraft.Provide{Key: "a_inner", Attribute: []string{"a", "b"}}
			outputs := []schemadraft.Provide{top, nested}
			if nestedFirst {
				outputs = []schemadraft.Provide{nested, top}
			}
			m := moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), nil, outputs...)
			ab, a, b := prop(kString, "a_b"), prop(kObject, "a"), prop(kString, "a", "b")
			ab.ReadOnly, b.ReadOnly = true, true
			a.Properties = []*schemadraft.Property{b}
			m.Type.Properties = []*schemadraft.Property{a, ab}
			d, _ := written(t, m)
			want := `{"a_b":{"value":"${vault_store.this.a_b}"},"a_b_2":{"value":"${vault_store.this.a.b}"}}`
			if got := canon(t, at(mainTF(t, d), "output")); got != want {
				t.Errorf("the outputs are %s, want %s", got, want)
			}
			provides := map[string]string{}
			for _, e := range d.Provides {
				provides[e.Key] = e.Value
			}
			if provides["a_b"] != "${module.{{ .context.instance_id }}.a_b}" ||
				provides["a_inner"] != "${module.{{ .context.instance_id }}.a_b_2}" {
				t.Errorf("the provides read %v, want each its own output", provides)
			}
			if !slices.ContainsFunc(d.Header, func(line string) bool {
				return strings.Contains(line, "a_b_2") && strings.Contains(line, "a.b")
			}) {
				t.Errorf("the Header does not name the output a_b_2 publishing a.b: %q", d.Header)
			}
		})
	}
}

// TestVariableNamesAreTheModulesVariables reads the variable of each input of a
// module whose inputs take a reserved name and the name it is renamed to: each
// is the variable the module declares and the valuesMapping sets.
func TestVariableNamesAreTheModulesVariables(t *testing.T) {
	m := moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), []schemadraft.Input{
		in(prop(kString, "source"), schemadraft.PillarOverrides, "source", false),
		in(prop(kString, "source_value"), schemadraft.PillarOverrides, "source_value", false),
		in(prop(kString, "name"), schemadraft.PillarValues, "name", true),
	})
	names := VariableNames(m)
	if want := []string{"source_value_2", "source_value", "name"}; !slices.Equal(names, want) {
		t.Fatalf("the variables are %v, want %v", names, want)
	}
	d, _ := written(t, m)
	for _, name := range names {
		if at(mainTF(t, d), "variable", name) == nil {
			t.Errorf("the module declares no variable %s", name)
		}
		if !strings.Contains(d.ValuesMapping, name+":") {
			t.Errorf("the valuesMapping sets no %s:\n%s", name, d.ValuesMapping)
		}
	}
}

// appResource is an app of the synthetic type example_app, as the core hands it
// to the writer: its name, its registry block written whole as an override
// less the server, and that server, which a neighbour supplies at its path
// inside the block's first item.
func appResource() schemadraft.Resource {
	name := prop(kString, "name")
	name.Required = true
	registry := block(nestingList, []string{"registry"}, prop(kString, "registry", "identity"),
		prop(kString, "registry", "server"))
	server := prop(kString, "registry", "0", "server")
	m := moduleOf("example_app", address(terraformHost, "example", "app", "1.0.0"), []schemadraft.Input{
		in(name, schemadraft.PillarValues, "name", true),
		in(registry, schemadraft.PillarOverrides, "registry", false),
		injected(server, schemadraft.MechanismProperty, "registry_0_server", true),
	})
	return schemadraft.Resource{Type: m.Type, Inputs: m.Inputs}
}

// TestInjectedPropertyOnTerraformIsAModuleVariable drafts a property a
// neighbour supplies inside a block as one module variable named after its
// inject key, set at its path inside the block's item, and its valuesMapping
// entry reading .context.inject.property under that key.
func TestInjectedPropertyOnTerraformIsAModuleVariable(t *testing.T) {
	d, diags := Write(appResource())
	if halts := haltsOf(diags); len(halts) != 0 {
		t.Fatalf("writing raised %+v", halts)
	}
	doc := mainTF(t, d)
	if got := keysOf(doc, "variable"); !reflect.DeepEqual(got, []string{"name", "registry", "registry_0_server"}) {
		t.Fatalf("variables %v, want one for the injected server", got)
	}
	if got := canon(t, at(doc, "variable", "registry_0_server")); got != `{"type":"string"}` {
		t.Errorf("variable registry_0_server is %s, want a required string", got)
	}
	want := `{"content":{"identity":"${try(it1.value.identity, null)}",` +
		`"server":"${it1.key == 0 ? var.registry_0_server : try(it1.value.server, null)}"},` +
		`"for_each":"${var.registry[*]}","iterator":"it1"}`
	if got := canon(t, at(doc, "resource", "example_app", "this", "dynamic", "registry")); got != want {
		t.Errorf("the registry block is\n%s\nwant\n%s", got, want)
	}
	if at(doc, "resource", "example_app", "this", "registry_0_server") != nil {
		t.Errorf("the injected server is set as an attribute of the resource")
	}
	const line = `registry_0_server: {{ toJson (index (default dict .context.inject) "property" "registry_0_server") }}`
	if !strings.Contains(d.ValuesMapping, line) {
		t.Errorf("the valuesMapping has no line %q:\n%s", line, d.ValuesMapping)
	}
	if !slices.ContainsFunc(d.Header, func(h string) bool {
		return strings.Contains(h, "registry_0_server") && strings.Contains(h, "registry.0.server")
	}) {
		t.Errorf("the header does not name the variable and the path it sets: %q", d.Header)
	}

	// The neighbour's published reference passes whole into the variable.
	ctx := mappingContext(map[string]any{"name": "orders"},
		map[string]any{"overrides": map[string]any{"registry": []any{map[string]any{"identity": "system"}}}},
		map[string]any{"property": map[string]any{"registry_0_server": "${module.registry.login_server}"}}, nil)
	got, err := renderMapping(t, d.ValuesMapping, ctx)
	if err != nil {
		t.Fatalf("the valuesMapping halts: %v", err)
	}
	if want := `{"name":"orders","registry":[{"identity":"system"}],"registry_0_server":"${module.registry.login_server}"}`; canon(t, got) != want {
		t.Errorf("the valuesMapping renders %s, want %s", canon(t, got), want)
	}

	// A Release leaving out the block the server is set inside halts naming it.
	ctx["infra"] = map[string]any{}
	if _, err := renderMapping(t, d.ValuesMapping, ctx); err == nil || !strings.Contains(err.Error(), "registry.0.server") {
		t.Errorf("an absent registry renders %v, want a halt naming registry.0.server", err)
	}
}

// TestModuleNestedInputOutsideABlockHalts refuses an input at a nested path
// that no block the module writes holds, naming it.
func TestModuleNestedInputOutsideABlockHalts(t *testing.T) {
	r := appResource()
	tags := prop(kObject, "tags")
	r.Inputs = append(r.Inputs, in(tags, schemadraft.PillarOverrides, "tags", false),
		injected(prop(kString, "tags", "owner"), schemadraft.MechanismProperty, "tags_owner", true))
	d, diags := Write(r)
	halts := haltsOf(diags)
	if len(halts) != 1 || halts[0].Code != schemadraft.CodeResourceTypeRefused || !strings.Contains(halts[0].Message, "tags.owner") {
		t.Fatalf("diagnostics %+v, want one resource-type-refused naming tags.owner", diags)
	}
	if len(d.Files) != 0 || d.ValuesMapping != "" {
		t.Errorf("a halted writer drafted %+v", d)
	}
}

// TestFromWrittenCarriesTheProvider drafts a resource from the source with the
// provider the source names: the module requires it at the locked version, and
// the decision's swap hint offers the schema import with the command writing its
// input, which holds no type name.
func TestFromWrittenCarriesTheProvider(t *testing.T) {
	w := schemadraft.WrittenResource{Type: "aws_sqs_queue",
		Provider:   &schemadraft.ProviderAddress{Namespace: "hashicorp", Type: "aws", Version: "~> 5.40"},
		Properties: []schemadraft.WrittenProperty{{Path: []string{"name"}, Value: json.RawMessage(`"jobs"`), Identity: true}}}
	if typ, _ := schemadraft.FromWritten(w); typ.Provider == nil || typ.Provider.Type != "aws" {
		t.Fatalf("FromWritten drops the provider: %+v", typ.Provider)
	}
	drafted, diags := schemadraft.DraftResource(wrappedInput(), Format, w)
	if halts := haltsOf(diags); len(halts) != 0 {
		t.Fatalf("halts %+v", halts)
	}
	for _, d := range diags {
		if d.Code == codeUnconstrained {
			t.Errorf("a provider with a version warns %+v", d)
		}
	}
	var module string
	for _, e := range drafted.Entities {
		for _, f := range e.Files {
			if strings.HasSuffix(f.Path, moduleFilePath) {
				module = f.Content
			}
		}
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(module), &doc); err != nil {
		t.Fatalf("%s is not JSON: %v ", moduleFilePath, err)
	}
	want := map[string]any{"aws": map[string]any{"source": "hashicorp/aws", "version": "~> 5.40"}}
	if got := at(doc, "terraform", "required_providers"); !reflect.DeepEqual(got, want) {
		t.Errorf("required_providers is %v, want %v", got, want)
	}
	hint := drafted.Decision.SwapHint
	for _, want := range []string{"aggrega import --format terraform-schema", "terraform providers schema -json"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the swap hint lacks %q: %s", want, hint)
		}
	}
	if strings.Contains(hint, "%!") {
		t.Errorf("the swap hint is malformed: %s", hint)
	}
}

// writtenOf is a resource written in a source, of the properties given.
func writtenOf(props ...schemadraft.WrittenProperty) schemadraft.WrittenResource {
	return schemadraft.WrittenResource{Type: "aws_lambda_function",
		Provider:   &schemadraft.ProviderAddress{Namespace: "hashicorp", Type: "aws", Version: "5.40.0"},
		Properties: props}
}

// TestWrittenPropertyInsideABlockIsSetAtItsPath sets a property drafted from
// the source at its path inside the object or list the source writes, each from
// its own variable, and passes a list of objects the core hands whole as one
// variable.
func TestWrittenPropertyInsideABlockIsSetAtItsPath(t *testing.T) {
	w := writtenOf(
		schemadraft.WrittenProperty{Path: []string{"environment", "variables", "MODE"}, Value: json.RawMessage(`"fast"`)},
		schemadraft.WrittenProperty{Path: []string{"environment", "variables", "DB_HOST"}, Inject: "environment_variables_db_host"},
		schemadraft.WrittenProperty{Path: []string{"ingress", "0", "from_port"}, Inject: "ingress_0_from_port"},
		schemadraft.WrittenProperty{Path: []string{"egress"}, Value: json.RawMessage(`[{"to_port":0}]`)},
	)
	typ, inputs := schemadraft.FromWritten(w)
	m := Module{Provider: *typ.Provider, Type: typ, Inputs: inputs}
	d, diags := WriteModule(m)
	if len(diags) != 0 {
		t.Fatalf("diagnostics %+v", diags)
	}
	doc := mainTF(t, d)
	names := VariableNames(m)
	variable := func(path ...string) string {
		for i, in := range inputs {
			if strings.Join(in.Property.Path, ".") == strings.Join(path, ".") {
				return names[i]
			}
		}
		t.Fatalf("no input at %v", path)
		return ""
	}
	body := at(doc, "resource", "aws_lambda_function", "this")
	want := `{"egress":"${var.` + variable("egress") + `}",` +
		`"environment":{"variables":{"DB_HOST":"${var.` + variable("environment", "variables", "DB_HOST") +
		`}","MODE":"${var.` + variable("environment", "variables", "MODE") + `}"}},` +
		`"ingress":[{"from_port":"${var.` + variable("ingress", "0", "from_port") + `}"}]}`
	if got := canon(t, body); got != want {
		t.Errorf("the resource is\n%s\nwant\n%s", got, want)
	}
	if got := canon(t, at(doc, "variable", variable("egress"))); got != `{"default":null,"type":"any"}` {
		t.Errorf("the egress variable is %s", got)
	}
	ctx := mappingContext(nil, map[string]any{"overrides": map[string]any{"MODE": "fast", "egress": []any{map[string]any{"to_port": 0}}}},
		map[string]any{"property": map[string]any{"environment_variables_db_host": "${module.db.address}", "ingress_0_from_port": 443}}, nil)
	got, err := renderMapping(t, d.ValuesMapping, ctx)
	if err != nil {
		t.Fatalf("the valuesMapping halts: %v\n%s", err, d.ValuesMapping)
	}
	if want := `{"egress":[{"to_port":0}],"environment_variables_db_host":"${module.db.address}","ingress_0_from_port":443,"mode":"fast"}`; canon(t, got) != want {
		t.Errorf("the valuesMapping renders %s, want %s", canon(t, got), want)
	}
}

// TestWrittenPropertyAtAPathUnderAWholeValueHalts refuses a property set below
// a value the module sets whole from one variable.
func TestWrittenPropertyAtAPathUnderAWholeValueHalts(t *testing.T) {
	w := writtenOf(
		schemadraft.WrittenProperty{Path: []string{"tags"}, Value: json.RawMessage(`{"a":"b"}`)},
		schemadraft.WrittenProperty{Path: []string{"tags", "owner"}, Inject: "tags_owner"},
	)
	typ, inputs := schemadraft.FromWritten(w)
	_, diags := WriteModule(Module{Provider: *typ.Provider, Type: typ, Inputs: inputs})
	halts := haltsOf(diags)
	if len(halts) != 1 || halts[0].Code != schemadraft.CodeResourceTypeRefused || !strings.Contains(halts[0].Message, "tags.owner") {
		t.Fatalf("diagnostics %+v, want one resource-type-refused naming tags.owner", diags)
	}
}

// TestValuesMappingWritesAnOwnValueInterpolationAsText escapes a dollar-brace
// in a value of the author's, so the root configuration reads it as text, and
// leaves a neighbour's reference a reference.
func TestValuesMappingWritesAnOwnValueInterpolationAsText(t *testing.T) {
	m := moduleOf("vault_store", address(terraformHost, "example", "vault", "1.0.0"), []schemadraft.Input{
		in(prop(kString, "name"), schemadraft.PillarValues, "name", true),
		in(prop(kString, "zone"), schemadraft.PillarInfra, "zone", true),
		in(prop(kString, "label"), schemadraft.PillarOverrides, "label", false),
		injected(prop(kString, "target_arn"), schemadraft.MechanismProperty, "target_arn", true),
	})
	d, _ := written(t, m)
	ctx := mappingContext(map[string]any{"name": "n-${a}"},
		map[string]any{"zone": "z-${b}", "overrides": map[string]any{"label": "a-${b}"}},
		map[string]any{"property": map[string]any{"target_arn": "${module.db.arn}"}}, nil)
	got, err := renderMapping(t, d.ValuesMapping, ctx)
	if err != nil {
		t.Fatalf("the valuesMapping halts: %v\n%s", err, d.ValuesMapping)
	}
	if want := `{"label":"a-$${b}","name":"n-$${a}","target_arn":"${module.db.arn}","zone":"z-$${b}"}`; canon(t, got) != want {
		t.Errorf("the valuesMapping renders %s, want %s", canon(t, got), want)
	}
}

// writtenBody drafts the resource of the properties given and returns the
// body of its resource, or the halts.
func writtenBody(t *testing.T, props ...schemadraft.WrittenProperty) (any, []aggrega.Diagnostic) {
	t.Helper()
	typ, inputs := schemadraft.FromWritten(writtenOf(props...))
	d, diags := WriteModule(Module{Provider: *typ.Provider, Type: typ, Inputs: inputs})
	if len(haltsOf(diags)) != 0 {
		return nil, diags
	}
	return at(mainTF(t, d), "resource", "aws_lambda_function", "this"), diags
}

// TestWrittenMemberNamedByDigitsIsAMemberKey sets a member of an object named
// by digits, such as an account id, as that key, never as a list index, and
// makes no list.
func TestWrittenMemberNamedByDigitsIsAMemberKey(t *testing.T) {
	body, diags := writtenBody(t,
		schemadraft.WrittenProperty{Path: []string{"tags", "123456789012"}, Inject: "tags_account"},
		schemadraft.WrittenProperty{Path: []string{"labels", "3"}, Inject: "labels_3"})
	if body == nil {
		t.Fatalf("halts %+v", diags)
	}
	got := canon(t, body)
	for _, want := range []string{`"tags":{"123456789012":"${var.tags_account}"}`, `"labels":{"3":"${var.labels_3}"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("the resource is %s, want it to hold %s", got, want)
		}
	}
}

// TestWrittenListItemsAreNeverPadded refuses an item at an index past the next
// one rather than writing items the source never declared.
func TestWrittenListItemsAreNeverPadded(t *testing.T) {
	body, diags := writtenBody(t,
		schemadraft.WrittenProperty{Path: []string{"ingress", "0", "from_port"}, Inject: "ingress_0_from_port"},
		schemadraft.WrittenProperty{Path: []string{"ingress", "2", "from_port"}, Inject: "ingress_2_from_port"})
	halts := haltsOf(diags)
	if body != nil || len(halts) != 1 || halts[0].Code != schemadraft.CodeResourceTypeRefused ||
		!strings.Contains(halts[0].Message, "ingress.2.from_port") {
		t.Fatalf("body %v, diagnostics %+v, want one resource-type-refused naming ingress.2.from_port", body, diags)
	}
	body, _ = writtenBody(t,
		schemadraft.WrittenProperty{Path: []string{"ingress", "0", "from_port"}, Inject: "ingress_0_from_port"},
		schemadraft.WrittenProperty{Path: []string{"ingress", "1", "from_port"}, Inject: "ingress_1_from_port"})
	if got := canon(t, body); got != `{"ingress":[{"from_port":"${var.ingress_0_from_port}"},{"from_port":"${var.ingress_1_from_port}"}]}` {
		t.Errorf("contiguous items give %s", got)
	}
}
