// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

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
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

const (
	vaultComponent = "catalog/component/imported/component/microsoft-example-vaults/0.1.0/component.yaml"
	vaultRecipe    = "catalog/recipe/arm/microsoft-example-vaults/0.1.0/recipe.yaml"
	tenantID       = "00000000-0000-0000-0000-000000000000"
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

// recipeParts are the parts of a drafted Recipe the tests read.
type recipeParts struct {
	symbol                string
	provideKeys, provides []string
	template              string
	assertionKeys         []string
	assertions            []string
}

func partsOf(t *testing.T, recipe string) recipeParts {
	t.Helper()
	var p recipeParts
	p.symbol = mappingAt(t, recipe, "spec", "locals", "symbol").Value
	p.provideKeys, p.provides = entries(mappingAt(t, recipe, "spec", "provides"))
	p.template = mappingAt(t, recipe, "spec", "artifacts", "template.yaml").Value
	p.assertionKeys, p.assertions = entries(mappingAt(t, recipe, "spec", "assertions"))
	return p
}

// partsOfDraft reads the parts of a Recipe the writer drafted, before the core
// completes it.
func partsOfDraft(t *testing.T, r schemadraft.Resource) (recipeParts, []string) {
	t.Helper()
	d, ds := Write(r)
	noHalt(t, ds)
	var p recipeParts
	for _, e := range d.Locals {
		if e.Key == "symbol" {
			p.symbol = e.Value
		}
	}
	for _, e := range d.Provides {
		p.provideKeys, p.provides = append(p.provideKeys, e.Key), append(p.provides, e.Value)
	}
	for _, e := range d.Artifacts {
		if e.Key == "template.yaml" {
			p.template = e.Value
		}
	}
	if p.symbol == "" || p.template == "" || len(d.Artifacts) != 1 {
		t.Fatalf("the draft holds no symbol local or no template.yaml alone: %+v", d)
	}
	return p, d.Header
}

// vaultContext is the context the vault's Recipe renders in for the instance
// orders-vault, with the overrides given.
func vaultContext(overrides map[string]any) map[string]any {
	infra := map[string]any{"location": "westeurope", "sku": map[string]any{"name": "Standard", "max_capacity": 3},
		"tenant_id": tenantID}
	if overrides != nil {
		infra["overrides"] = overrides
	}
	return map[string]any{
		"context":  map[string]any{"instance_id": "orders-vault", "inject": map[string]any{}},
		"metadata": map[string]any{"instance_id": "orders-vault"},
		"values":   map[string]any{"name": "orders-vault"},
		"infra":    infra,
		"features": map[string]any{},
	}
}

// renderFragment renders the Recipe's template.yaml in ctx, with its symbol
// evaluated first as the compiler evaluates locals, reads it as the arm provider
// reads a fragment, and returns its text and its content.
func renderFragment(t *testing.T, parts recipeParts, ctx map[string]any) (string, map[string]any) {
	t.Helper()
	symbol, err := render(parts.symbol, ctx)
	if err != nil {
		t.Fatalf("locals.symbol: %v", err)
	}
	ctx["locals"] = map[string]any{"symbol": symbol}
	text, err := render(parts.template, ctx)
	if err != nil {
		t.Fatalf("template.yaml: %v\n%s", err, parts.template)
	}
	if _, _, err := armcore.ReadFragment([]byte(text)); err != nil {
		t.Fatalf("the provider refuses the fragment: %v\n%s", err, text)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	return text, doc
}

// renderProvides renders each provides value of parts in ctx, which holds the
// symbol local already.
func renderProvides(t *testing.T, parts recipeParts, ctx map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for i, value := range parts.provides {
		got, err := render(value, ctx)
		if err != nil {
			t.Fatalf("provides.%s: %v", parts.provideKeys[i], err)
		}
		out[parts.provideKeys[i]] = got
	}
	return out
}

func resourceOf(t *testing.T, doc map[string]any, symbol string) map[string]any {
	t.Helper()
	resources, _ := doc["resources"].(map[string]any)
	res, ok := resources[symbol].(map[string]any)
	if !ok {
		t.Fatalf("no resource %s in %v", symbol, doc)
	}
	return res
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

// assemble assembles the fragments given, each the one fragment of the node of
// its name, into one boundary annotated as given, and returns the template.
func assemble(t *testing.T, annotations map[string]string, nodes []string, texts ...string) map[string]any {
	t.Helper()
	var contribs []armcore.Contribution
	for i, text := range texts {
		contribs = append(contribs, armcore.Contribution{NodeID: nodes[i], Files: [][]byte{[]byte(text)},
			Paths: []string{"template.yaml"}, Annotations: annotations})
	}
	out, err := armcore.Assemble(contribs)
	if err != nil {
		t.Fatalf("the provider refuses the template: %v", err)
	}
	if len(out.Files) != 1 || out.Files[0].Path != armcore.TemplateFileName {
		t.Fatalf("assembled %+v", out.Files)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Files[0].Content, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// reader is a fragment declaring a resource that reads each value given under
// its properties, keyed as given.
func reader(keys, values []string) string {
	var b strings.Builder
	b.WriteString("resources:\n  Reader:\n    type: Microsoft.Example/readers\n    apiVersion: \"2025-01-01\"\n" +
		"    name: reader\n    properties:\n")
	for i, key := range keys {
		quoted, _ := json.Marshal(values[i])
		b.WriteString("      " + key + ": " + string(quoted) + "\n")
	}
	return b.String()
}

func TestArmGolden(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "vault.json", vault), Format, parse)
	noHalt(t, out.Diagnostics)
	var paths []string
	for _, f := range out.Files {
		paths = append(paths, f.Path)
	}
	if want := []string{vaultComponent, vaultRecipe}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("files %v, want %v", paths, want)
	}
	golden(t, "vault.component.yaml", fileOf(t, out, vaultComponent))
	golden(t, "vault.recipe.yaml", fileOf(t, out, vaultRecipe))

	again := schemadraft.Import(fileInput(t, "vault.json", vault), Format, parse)
	if !reflect.DeepEqual(out, again) {
		t.Errorf("two runs differ")
	}
}

func TestArmDraftsLoad(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "vault.json", vault, vault+"/keys"), Format, parse)
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
	for _, name := range []string{"imported/component/microsoft-example-vaults:0.1.0",
		"imported/component/microsoft-example-vaults-keys:0.1.0"} {
		if _, ok := cat.Components[name]; !ok {
			t.Errorf("the catalog holds no Component %s", name)
		}
	}
	for _, name := range []string{"arm/microsoft-example-vaults:0.1.0", "arm/microsoft-example-vaults-keys:0.1.0"} {
		if _, ok := cat.Recipes[name]; !ok {
			t.Errorf("the catalog holds no Recipe %s", name)
		}
	}
}

// TestArmTemplateRenders renders the drafted Recipe over sample values through
// the compiler's template functions, reads the fragment as the arm provider
// does, and assembles it into a template beside a resource reading each value
// the Recipe provides.
func TestArmTemplateRenders(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "vault.json", vault), Format, parse)
	noHalt(t, out.Diagnostics)
	parts := partsOf(t, fileOf(t, out, vaultRecipe))
	if parts.symbol != "{{ capitalizedWords .context.instance_id }}" {
		t.Errorf("locals.symbol %q", parts.symbol)
	}

	overrides := map[string]any{"tags": map[string]any{"team": "a"}, "enabledForDeployment": false,
		"accessPolicies": []any{map[string]any{"objectId": "o", "permissions": []any{"get"}}}}
	ctx := vaultContext(overrides)
	text, doc := renderFragment(t, parts, ctx)
	want := `{"type": "Microsoft.Example/vaults", "apiVersion": "2025-01-01", "name": "orders-vault", ` +
		`"location": "westeurope", "sku": {"name": "Standard", "maxCapacity": 3}, "tags": {"team": "a"}, ` +
		`"properties": {"tenantId": "` + tenantID + `", "adminPassword": "[parameters('OrdersVaultAdminPassword')]", ` +
		`"enabledForDeployment": false, "accessPolicies": [{"objectId": "o", "permissions": ["get"]}]}}`
	if got := canon(t, resourceOf(t, doc, "OrdersVault")); got != canonText(t, want) {
		t.Errorf("resource %s, want %s\n%s", got, canonText(t, want), text)
	}
	if got := canon(t, doc["parameters"]); got != `{"OrdersVaultAdminPassword":{"type":"securestring"}}` {
		t.Errorf("parameters %s", got)
	}
	if len(doc) != 2 {
		t.Errorf("the fragment holds %d sections, want parameters and resources alone:\n%s", len(doc), text)
	}

	wantKeys := []string{"etag", "id", "identity_principal_id", "identity_tenant_id", "endpoints_primary",
		"endpoints_secondary", "provisioning_state", "vault_uri"}
	if !reflect.DeepEqual(parts.provideKeys, wantKeys) {
		t.Fatalf("provides %v, want %v", parts.provideKeys, wantKeys)
	}
	provided := renderProvides(t, parts, ctx)
	for key, want := range map[string]string{
		"etag":                  "[reference('OrdersVault', '2025-01-01', 'Full').etag]",
		"id":                    "[resourceId('Microsoft.Example/vaults', 'orders-vault')]",
		"identity_principal_id": "[reference('OrdersVault', '2025-01-01', 'Full').identity.principalId]",
		"identity_tenant_id":    "[reference('OrdersVault', '2025-01-01', 'Full').identity.tenantId]",
		"endpoints_primary":     "[reference('OrdersVault').endpoints.primary]",
		"endpoints_secondary":   "[reference('OrdersVault').endpoints.secondary]",
		"provisioning_state":    "[reference('OrdersVault').provisioningState]",
		"vault_uri":             "[reference('OrdersVault').vaultUri]",
	} {
		if provided[key] != want {
			t.Errorf("provides.%s renders %q, want %q", key, provided[key], want)
		}
	}

	// The provider assembles it beside a resource reading every provided value
	// under its properties, each whole.
	var values []string
	for _, key := range wantKeys {
		values = append(values, provided[key])
	}
	template := assemble(t, nil, []string{"orders-vault", "reader"}, text, reader(wantKeys, values))
	resources, _ := template["resources"].(map[string]any)
	if _, ok := resources["OrdersVault"]; !ok {
		t.Errorf("the template holds no OrdersVault: %v", template)
	}
	readerProps, _ := resources["Reader"].(map[string]any)["properties"].(map[string]any)
	for i, key := range wantKeys {
		if readerProps[key] != values[i] {
			t.Errorf("the reader's %s is %v, want %q", key, readerProps[key], values[i])
		}
	}
	if _, ok := template["outputs"]; ok {
		t.Errorf("the template holds outputs: %v", template["outputs"])
	}
	if got := canon(t, template["parameters"]); got != `{"OrdersVaultAdminPassword":{"type":"securestring"}}` {
		t.Errorf("the template's parameters %s", got)
	}

	if want := []string{"overrides_this_recipe_reads", "secrets_are_parameters"}; !reflect.DeepEqual(parts.assertionKeys, want) {
		t.Errorf("assertions %v, want %v", parts.assertionKeys, want)
	}
}

func TestArmOptionalFalsePasses(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "vault.json", vault), Format, parse)
	noHalt(t, out.Diagnostics)
	parts := partsOf(t, fileOf(t, out, vaultRecipe))
	ctx := vaultContext(map[string]any{"enabledForDeployment": false, "softDeleteRetentionInDays": 0, "createMode": ""})
	ctx["infra"].(map[string]any)["sku"] = map[string]any{"name": "Premium"}
	_, doc := renderFragment(t, parts, ctx)
	res := resourceOf(t, doc, "OrdersVault")
	props, _ := res["properties"].(map[string]any)
	for key, want := range map[string]any{"enabledForDeployment": false, "softDeleteRetentionInDays": 0, "createMode": ""} {
		if v, ok := props[key]; !ok || v != want {
			t.Errorf("an override set to %v is written %v, %v: %v", want, v, ok, props)
		}
	}
	for _, absent := range []string{"accessPolicies"} {
		if _, ok := props[absent]; ok {
			t.Errorf("%s is written though it is not set: %v", absent, props)
		}
	}
	for _, absent := range []string{"tags", "identity"} {
		if _, ok := res[absent]; ok {
			t.Errorf("%s is written though it is not set: %v", absent, res)
		}
	}
	if got := canon(t, res["sku"]); got != `{"name":"Premium"}` {
		t.Errorf("sku %s, want the member set alone", got)
	}
}

func TestArmSecretIsSecurestringParameter(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "vault.json", vault), Format, parse)
	noHalt(t, out.Diagnostics)
	recipe := fileOf(t, out, vaultRecipe)
	parts := partsOf(t, recipe)
	ctx := vaultContext(nil)
	_, doc := renderFragment(t, parts, ctx)
	if got := canon(t, doc["parameters"]); got != `{"OrdersVaultAdminPassword":{"type":"securestring"}}` {
		t.Errorf("parameters %s", got)
	}
	props, _ := resourceOf(t, doc, "OrdersVault")["properties"].(map[string]any)
	if got := props["adminPassword"]; got != "[parameters('OrdersVaultAdminPassword')]" {
		t.Errorf("adminPassword %v, want the parameter read", got)
	}
	if !strings.Contains(recipe, "parameter <instance>AdminPassword") {
		t.Errorf("the Recipe's header does not name the parameter:\n%s", recipe)
	}
	if strings.Contains(fileOf(t, out, vaultComponent), "featuresSchema") {
		t.Errorf("the Component declares a slot for a secret the Recipe declares as a parameter")
	}

	// An attachment on the secret's key halts, naming the parameter.
	for i, key := range parts.assertionKeys {
		if key != "secrets_are_parameters" {
			continue
		}
		ctx["features"] = map[string]any{"admin_password": map[string]any{}}
		_, err := render(parts.assertions[i], ctx)
		if err == nil || !strings.Contains(err.Error(), "OrdersVaultAdminPassword") {
			t.Errorf("an attachment on admin_password renders %v", err)
		}
	}

	// A secret object is a secure object.
	typ := &schemadraft.Type{Name: vault, APIVersion: "2025-01-01", Scopes: []string{"resourceGroup"}}
	settings := &schemadraft.Property{Name: "settings", Path: []string{"properties", "settings"},
		Kinds: []schemadraft.Kind{schemadraft.KindObject}, Sensitive: true}
	draft, _ := partsOfDraft(t, schemadraft.Resource{Type: typ, Inputs: []schemadraft.Input{{Property: settings,
		Source: schemadraft.InputSource{Pillar: schemadraft.PillarSecret, Key: "settings"}, Secret: true}}})
	_, doc = renderFragment(t, draft, map[string]any{"context": map[string]any{"instance_id": "v"}})
	if got := canon(t, doc["parameters"]); got != `{"VSettings":{"type":"secureObject"}}` {
		t.Errorf("parameters %s", got)
	}
}

func TestArmWiderScopeWritesItsSchema(t *testing.T) {
	for _, c := range []struct {
		typ, schema, id string
		scope           armcore.Scope
	}{
		{"Microsoft.Example/subscriptionPolicies", armcore.SchemaSubscription,
			"[subscriptionResourceId('Microsoft.Example/subscriptionPolicies', 'p1')]", armcore.ScopeSubscription},
		{"Microsoft.Example/managementGroupPolicies", armcore.SchemaManagementGroup,
			"[extensionResourceId(managementGroup().id, 'Microsoft.Example/managementGroupPolicies', 'p1')]",
			armcore.ScopeManagementGroup},
		{"Microsoft.Example/widePolicies", armcore.SchemaSubscription,
			"[subscriptionResourceId('Microsoft.Example/widePolicies', 'p1')]", armcore.ScopeSubscription},
		{"Microsoft.Example/policies", "", "[resourceId('Microsoft.Example/policies', 'p1')]",
			armcore.ScopeResourceGroup},
	} {
		t.Run(c.typ, func(t *testing.T) {
			out := schemadraft.Import(fileInput(t, "scopes.json", c.typ), Format, parse)
			noHalt(t, out.Diagnostics)
			slug := strings.ToLower(strings.ReplaceAll(c.typ, "/", "-"))
			slug = strings.ReplaceAll(slug, ".", "-")
			recipe := fileOf(t, out, "catalog/recipe/arm/"+slug+"/0.1.0/recipe.yaml")
			parts := partsOf(t, recipe)
			ctx := map[string]any{"context": map[string]any{"instance_id": "p"}, "values": map[string]any{"name": "p1"},
				"infra": map[string]any{"level": "strict"}}
			text, doc := renderFragment(t, parts, ctx)
			schema, written := doc["$schema"]
			if c.schema == "" && written || c.schema != "" && schema != c.schema {
				t.Errorf("$schema %v, want %q\n%s", schema, c.schema, text)
			}
			frag, _, err := armcore.ReadFragment([]byte(text))
			if err != nil || frag.Scope != c.scope {
				t.Errorf("the provider reads the fragment at %s, %v, want %s", frag.Scope, err, c.scope)
			}
			names := strings.Contains(recipe, armcore.AnnotationDeploymentScope)
			if names != (c.scope != armcore.ScopeResourceGroup) {
				t.Errorf("the Recipe names %s: %v, want %v\n%s", armcore.AnnotationDeploymentScope, names,
					c.scope != armcore.ScopeResourceGroup, recipe)
			}
			provided := renderProvides(t, parts, ctx)
			if provided["id"] != c.id {
				t.Errorf("provides.id renders %q, want %q", provided["id"], c.id)
			}
			if provided["state"] != "[reference('P').state]" {
				t.Errorf("provides.state renders %q", provided["state"])
			}

			// The fragment assembles in a boundary deployed at its scope, and a
			// fragment wider than a resource group halts in a resource group
			// boundary, naming the annotation.
			assemble(t, map[string]string{armcore.AnnotationDeploymentScope: string(c.scope)}, []string{"p"}, text)
			if c.scope != armcore.ScopeResourceGroup {
				_, err := armcore.Assemble([]armcore.Contribution{{NodeID: "p", Files: [][]byte{[]byte(text)},
					Paths: []string{"template.yaml"}}})
				if err == nil || !strings.Contains(err.Error(), armcore.AnnotationDeploymentScope) {
					t.Errorf("a resource group boundary assembles the fragment: %v", err)
				}
			}
		})
	}
}

// keyType is a child resource type written at resource group scope, whose name
// is read from values.
func keyType() (*schemadraft.Type, schemadraft.Input) {
	typ := &schemadraft.Type{Name: vault + "/keys", APIVersion: "2025-01-01", Scopes: []string{"resourceGroup"},
		PrimaryIdentifier: [][]string{{"id"}}}
	name := schemadraft.Input{Property: &schemadraft.Property{Name: "name", Path: []string{"name"},
		Kinds: []schemadraft.Kind{schemadraft.KindString}}, Required: true,
		Source: schemadraft.InputSource{Pillar: schemadraft.PillarValues, Key: "name"}}
	return typ, name
}

func TestArmProvidesForms(t *testing.T) {
	typ, name := keyType()
	r := schemadraft.Resource{Type: typ, Inputs: []schemadraft.Input{name},
		Provides: []schemadraft.Provide{
			{Key: "id", Attribute: []string{"id"}, Identifier: true},
			{Key: "odd", Attribute: []string{"properties", "odd-name", "it's"}},
			{Key: "root_nested", Attribute: []string{"identity", "principalId"}},
			{Key: "template", Attribute: []string{"properties", "{{ x }}"}},
		},
		Unmapped: []schemadraft.Unmapped{{Key: "endpoint", Candidates: []string{"properties.keyUri"}}},
	}
	parts, _ := partsOfDraft(t, r)
	ctx := map[string]any{"locals": map[string]any{"symbol": "Key"}, "values": map[string]any{"name": "orders-vault/it's"}}
	wantKeys := []string{"id", "odd", "root_nested", "template", "endpoint"}
	if !reflect.DeepEqual(parts.provideKeys, wantKeys) {
		t.Fatalf("provides %v, want %v", parts.provideKeys, wantKeys)
	}
	for i, want := range []string{
		"[resourceId('Microsoft.Example/vaults/keys', 'orders-vault', 'it''s')]",
		"[reference('Key')['odd-name']['it''s']]",
		"[reference('Key', '2025-01-01', 'Full').identity.principalId]",
		"[reference('Key')['{{ x }}']]",
	} {
		got, err := render(parts.provides[i], ctx)
		if err != nil || got != want {
			t.Errorf("provides.%s renders %q, %v, want %q", wantKeys[i], got, err, want)
			continue
		}
		if _, err := armexpr.Parse(got); err != nil {
			t.Errorf("provides.%s renders %q, which ARM cannot read: %v", wantKeys[i], got, err)
		}
	}
	if _, err := render(parts.provides[4], ctx); err == nil || !strings.Contains(err.Error(), "provides.endpoint is not mapped") {
		t.Errorf("an unmapped key renders %v", err)
	}

	// Where no input holds the resource's name, the identifier halts saying so.
	r.Inputs = nil
	parts, _ = partsOfDraft(t, r)
	if _, err := render(parts.provides[0], ctx); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("an identifier with no name renders %v", err)
	}
}

func TestArmInjectOnlyUnderProperties(t *testing.T) {
	typ, name := keyType()
	inject := func(path []string, key string, required bool) schemadraft.Input {
		return schemadraft.Input{Property: &schemadraft.Property{Name: path[len(path)-1], Path: path,
			Kinds: []schemadraft.Kind{schemadraft.KindString}}, Required: required,
			Source: schemadraft.InputSource{Pillar: schemadraft.PillarInject, Key: key, Mechanism: "env"}}
	}
	r := schemadraft.Resource{Type: typ, Inputs: []schemadraft.Input{name,
		inject([]string{"properties", "targetId"}, "TARGET_ID", true),
		inject([]string{"properties", "backupId"}, "BACKUP_ID", false)}}
	parts, _ := partsOfDraft(t, r)
	reference := "[reference('Store').primaryEndpoints.blob]"
	ctx := map[string]any{"context": map[string]any{"instance_id": "key", "inject": map[string]any{
		"env": map[string]any{"TARGET_ID": reference}}}, "values": map[string]any{"name": "v/k"}}
	text, doc := renderFragment(t, parts, ctx)
	if got := canon(t, resourceOf(t, doc, "Key")["properties"]); got != canon(t, map[string]any{"targetId": reference}) {
		t.Errorf("properties %s\n%s", got, text)
	}
	ctx["context"].(map[string]any)["inject"] = map[string]any{"env": map[string]any{"TARGET_ID": "a", "BACKUP_ID": "b"}}
	if _, doc := renderFragment(t, parts, ctx); canon(t, resourceOf(t, doc, "Key")["properties"]) !=
		`{"backupId":"b","targetId":"a"}` {
		t.Errorf("properties %v", resourceOf(t, doc, "Key")["properties"])
	}

	// An inject input anywhere else halts naming it.
	r.Inputs = append(r.Inputs, inject([]string{"tags"}, "TAGS", false))
	d, ds := Write(r)
	h := onlyHalt(t, ds, codeRefused)
	if !strings.Contains(h.Message, "tags") || !strings.Contains(h.Message, "TAGS") || !strings.Contains(h.Message, "properties") {
		t.Errorf("halt %q does not name the input and where it may be written", h.Message)
	}
	if len(d.Artifacts) != 0 || len(d.Provides) != 0 {
		t.Errorf("a halted writer drafted %+v", d)
	}
}

// TestArmRealFileDrafts drafts the two types of the file from
// Azure/bicep-types-az the provider deploys, renders them over sample values
// and assembles them into one template.
func TestArmRealFileDrafts(t *testing.T) {
	out := schemadraft.Import(fileInput(t, managedIdentity, userIdentity, credential), Format, parse)
	noHalt(t, out.Diagnostics)
	if len(out.Files) != 4 {
		t.Fatalf("files %d, want a Component and a Recipe for each type", len(out.Files))
	}
	identityComponent := fileOf(t, out,
		"catalog/component/imported/security/microsoft-managedidentity-userassignedidentities/0.1.0/component.yaml")
	if !strings.Contains(identityComponent, schemadraft.VendorTextMarker+" The geo-location where the resource lives") {
		t.Errorf("the vendor's description of location is not marked:\n%s", identityComponent)
	}

	// A child's name is its parent's and its own, joined by a slash, which the
	// pattern of its own name does not admit, so the draft drops it.
	dropped := false
	for _, d := range out.Diagnostics {
		if d.Code == codeDropped && d.SourceRef == "name" && strings.Contains(d.Message, "pattern") &&
			strings.Contains(d.Message, "parent") {
			dropped = true
		}
	}
	if !dropped {
		t.Errorf("no warning drops the pattern of the child's name: %+v", out.Diagnostics)
	}
	credentialComponent := fileOf(t, out, "catalog/component/imported/security/"+
		"microsoft-managedidentity-userassignedidentities-federatedidentitycredentials/0.1.0/component.yaml")
	if strings.Contains(credentialComponent, "a-zA-Z0-9-_") {
		t.Errorf("the child's name carries the pattern of its own name:\n%s", credentialComponent)
	}

	identity := partsOf(t, fileOf(t, out, "catalog/recipe/arm/microsoft-managedidentity-userassignedidentities/0.1.0/recipe.yaml"))
	ctx := map[string]any{"context": map[string]any{"instance_id": "orders-identity"},
		"values": map[string]any{"name": "orders-identity"}, "infra": map[string]any{"location": "westeurope"}}
	identityText, doc := renderFragment(t, identity, ctx)
	if got := canon(t, resourceOf(t, doc, "OrdersIdentity")); got != canonText(t, `{"type": "`+userIdentity+`", `+
		`"apiVersion": "2024-11-30", "name": "orders-identity", "location": "westeurope"}`) {
		t.Errorf("identity %s\n%s", got, identityText)
	}
	provided := renderProvides(t, identity, ctx)
	if provided["principal_id"] != "[reference('OrdersIdentity').principalId]" ||
		provided["id"] != "[resourceId('"+userIdentity+"', 'orders-identity')]" {
		t.Errorf("provides %v", provided)
	}

	cred := partsOf(t, fileOf(t, out, "catalog/recipe/arm/"+
		"microsoft-managedidentity-userassignedidentities-federatedidentitycredentials/0.1.0/recipe.yaml"))
	ctx = map[string]any{"context": map[string]any{"instance_id": "orders-github"},
		"values": map[string]any{"name": "orders-identity/github"},
		"infra":  map[string]any{"issuer": "https://issuer.example", "subject": "repo:example", "audiences": []any{"api"}}}
	credText, doc := renderFragment(t, cred, ctx)
	if got := canon(t, resourceOf(t, doc, "OrdersGithub")); got != canonText(t, `{"type": "`+credential+`", `+
		`"apiVersion": "2024-11-30", "name": "orders-identity/github", "properties": {"issuer": "https://issuer.example", `+
		`"subject": "repo:example", "audiences": ["api"]}}`) {
		t.Errorf("credential %s\n%s", got, credText)
	}
	if got := renderProvides(t, cred, ctx)["id"]; got != "[resourceId('"+credential+"', 'orders-identity', 'github')]" {
		t.Errorf("provides.id %q", got)
	}
	assemble(t, nil, []string{"orders-identity", "orders-github"}, identityText, credText)
}
