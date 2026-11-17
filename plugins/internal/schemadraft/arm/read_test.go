// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The codes the tests read, written out rather than taken from the core, so a
// renamed code fails here as it would for a reader of the diagnostics.
const (
	codeUnreadable  = "resource-schema-unreadable"
	codeRefused     = "resource-type-refused"
	codeParseFailed = "parse-failed"
	codeNotFound    = "resource-type-not-found"
	codeNotNamed    = "resource-type-not-named"
	codeDropped     = "constraint-dropped"

	managedIdentity = "managedidentity-types.json"
	userIdentity    = "Microsoft.ManagedIdentity/userAssignedIdentities"
	credential      = "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials"
	systemIdentity  = "Microsoft.ManagedIdentity/identities"
	vault           = "Microsoft.Example/vaults"
	shapes          = "Microsoft.Example/shapes"
	scripts         = "Microsoft.Example/scripts"
)

// parse reads JSON source as the host's json parser does, into the canonical
// tree whose object keys are sorted.
func parse(syntax string, source []byte) (json.RawMessage, error) {
	if syntax != "json" {
		return nil, fmt.Errorf("no parser for %s", syntax)
	}
	out := parsercore.ParseJSON(source)
	for _, d := range out.Diagnostics {
		return nil, errors.New(d.Message)
	}
	return out.Tree, nil
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fileInput is the import of the fixture name, handed as a file, requesting the
// types given; none requests none.
func fileInput(t *testing.T, name string, types ...string) aggrega.ImportInput {
	t.Helper()
	in := aggrega.ImportInput{Format: Format.Name, Name: name, Bytes: fixture(t, name)}
	if len(types) > 0 {
		raw, err := json.Marshal(map[string][]string{"resource_types": types})
		if err != nil {
			t.Fatal(err)
		}
		in.Settings = raw
	}
	return in
}

// bytesInput is the import of one file named types.json holding body,
// requesting the types given.
func bytesInput(body string, types ...string) aggrega.ImportInput {
	in := aggrega.ImportInput{Format: Format.Name, Name: "types.json", Bytes: []byte(body)}
	if len(types) > 0 {
		raw, _ := json.Marshal(map[string][]string{"resource_types": types})
		in.Settings = raw
	}
	return in
}

func haltsOf(ds []aggrega.Diagnostic) []aggrega.Diagnostic {
	var out []aggrega.Diagnostic
	for _, d := range ds {
		if d.Severity == "halt" {
			out = append(out, d)
		}
	}
	return out
}

func noHalt(t *testing.T, ds []aggrega.Diagnostic) {
	t.Helper()
	if h := haltsOf(ds); len(h) > 0 {
		t.Fatalf("halted: %+v", h)
	}
}

// onlyHalt returns the one halt ds holds, failing unless it is the one halt and
// carries code.
func onlyHalt(t *testing.T, ds []aggrega.Diagnostic, code string) aggrega.Diagnostic {
	t.Helper()
	h := haltsOf(ds)
	if len(h) != 1 || h[0].Code != code {
		t.Fatalf("halts %+v, want one %s", h, code)
	}
	return h[0]
}

// lifted lists the fixture name and lifts the type typeName of it.
func lifted(t *testing.T, name, typeName string) *schemadraft.Type {
	t.Helper()
	refs, ds := Format.List(fileInput(t, name), parse)
	noHalt(t, ds)
	for _, ref := range refs {
		if ref.Name == typeName {
			typ, ds := Format.Lift(ref)
			noHalt(t, ds)
			if typ == nil {
				t.Fatalf("lifted no type for %s", typeName)
			}
			return typ
		}
	}
	t.Fatalf("listed no %s among %+v", typeName, refs)
	return nil
}

// propertyAt returns the property of typ at the vendor path, dotted, searching
// the members of objects and of an array's items.
func propertyAt(t *testing.T, typ *schemadraft.Type, path string) *schemadraft.Property {
	t.Helper()
	var find func([]*schemadraft.Property) *schemadraft.Property
	find = func(props []*schemadraft.Property) *schemadraft.Property {
		for _, p := range props {
			if strings.Join(p.Path, ".") == path {
				return p
			}
			if found := find(p.Properties); found != nil {
				return found
			}
			if p.Items != nil {
				if found := find(p.Items.Properties); found != nil {
					return found
				}
			}
		}
		return nil
	}
	p := find(typ.Properties)
	if p == nil {
		t.Fatalf("no property %s in %s", path, typ.Name)
	}
	return p
}

func paths(props []*schemadraft.Property) []string {
	var out []string
	for _, p := range props {
		out = append(out, strings.Join(p.Path, "."))
	}
	return out
}

func enumOf(t *testing.T, p *schemadraft.Property) []string {
	t.Helper()
	var out []string
	for _, raw := range p.Enum {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("enum value %s of %s is not text", raw, p.Name)
		}
		out = append(out, s)
	}
	return out
}

// droppedOf returns the constraint each Dropped entry of p names, before its
// colon.
func droppedOf(p *schemadraft.Property) []string {
	var out []string
	for _, entry := range p.Dropped {
		what, _, _ := strings.Cut(entry, ": ")
		out = append(out, what)
	}
	return out
}

func TestArmListsResourceTypes(t *testing.T) {
	if Format.Name != "arm-schema" || Format.Capability != "arm" || Format.Syntax != "json" || !Format.FoldCase ||
		Format.CountOnly || Format.Rules.SecretSlots {
		t.Errorf("format %s on %s from %s, folding case %v, counting only %v, secrets on slots %v", Format.Name,
			Format.Capability, Format.Syntax, Format.FoldCase, Format.CountOnly, Format.Rules.SecretSlots)
	}

	refs, ds := Format.List(fileInput(t, managedIdentity), parse)
	noHalt(t, ds)
	var got []string
	for _, r := range refs {
		got = append(got, r.Name+" "+r.APIVersion+" "+r.Source)
	}
	want := []string{userIdentity + " 2024-11-30 " + managedIdentity, credential + " 2024-11-30 " + managedIdentity,
		systemIdentity + " 2024-11-30 " + managedIdentity}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}

	// With no request, the halt lists the types the file holds.
	out := schemadraft.Import(fileInput(t, managedIdentity), Format, parse)
	d := onlyHalt(t, out.Diagnostics, codeNotNamed)
	for _, name := range []string{userIdentity, credential, systemIdentity} {
		if !strings.Contains(d.Message, name) {
			t.Errorf("the halt %q does not list %s", d.Message, name)
		}
	}

	// A request is matched without regard to case, and drafts the file's
	// spelling at the file's apiVersion.
	out = schemadraft.Import(fileInput(t, managedIdentity, "microsoft.managedidentity/USERASSIGNEDIDENTITIES@2024-11-30"),
		Format, parse)
	noHalt(t, out.Diagnostics)
	if len(out.Decisions) != 1 || out.Decisions[0].SourceNode.ID != userIdentity+"@2024-11-30" {
		t.Errorf("decisions %+v, want one for %s at its apiVersion", out.Decisions, userIdentity)
	}

	// An apiVersion the file does not hold halts naming the one it holds.
	out = schemadraft.Import(fileInput(t, managedIdentity, userIdentity+"@2023-01-31"), Format, parse)
	if d := onlyHalt(t, out.Diagnostics, codeNotFound); !strings.Contains(d.Message, "2024-11-30") {
		t.Errorf("the halt %q does not name the apiVersion the file holds", d.Message)
	}
}

// TestArmListsWithoutLiftingBodies holds List to the type list alone: a body no
// reader could lift halts only once its type is selected.
func TestArmListsWithoutLiftingBodies(t *testing.T) {
	body := `[{"$type": "StringType"}, {"$type": "ResourceType", "name": "Microsoft.Example/broken@2025-01-01", ` +
		`"body": {"$ref": "#/9"}, "writableScopes": 8}, {"$type": "ResourceType", "name": ` +
		`"Microsoft.Example/fine@2025-01-01", "body": {"$ref": "#/3"}, "writableScopes": 8}, {"$type": "ObjectType", ` +
		`"name": "Fine", "properties": {"name": {"type": {"$ref": "#/0"}, "flags": 9}}}]`
	refs, ds := Format.List(bytesInput(body), parse)
	noHalt(t, ds)
	if len(refs) != 2 {
		t.Fatalf("listed %+v", refs)
	}
	noHalt(t, schemadraft.Import(bytesInput(body, "Microsoft.Example/fine"), Format, parse).Diagnostics)
	out := schemadraft.Import(bytesInput(body, "Microsoft.Example/broken"), Format, parse)
	if d := onlyHalt(t, out.Diagnostics, codeUnreadable); !strings.Contains(d.Message, "#/9") {
		t.Errorf("the halt %q does not name the reference", d.Message)
	}
}

func TestArmUnreadableNamesMember(t *testing.T) {
	const str = `{"$type": "StringType"}`
	resource := func(fields string) string {
		return `{"$type": "ResourceType", "name": "Microsoft.Example/things@2025-01-01"` + fields + `}`
	}
	body := func(props string) string {
		return `[` + str + `, ` + resource(`, "body": {"$ref": "#/2"}, "writableScopes": 8`) +
			`, {"$type": "ObjectType", "name": "Thing", "properties": {"name": {"type": {"$ref": "#/0"}, "flags": 9}` +
			props + `}}, {"$type": "IntegerType"}, {"name": "no kind"}]`
	}
	for _, c := range []struct{ name, body, code, member string }{
		{"an object", `{"types": []}`, codeUnreadable, "0.$type"},
		{"an empty list", `[]`, codeUnreadable, "0.$type"},
		{"a list of numbers", `[7]`, codeUnreadable, "0.$type"},
		{"a $type of no text", `[{"$type": 7}]`, codeUnreadable, "0.$type"},
		{"a name of no apiVersion", `[` + str + `, {"$type": "ResourceType", "name": "Microsoft.Example/things", ` +
			`"body": {"$ref": "#/0"}, "writableScopes": 8}]`, codeUnreadable, "1.name"},
		{"a name of no text", `[` + str + `, {"$type": "ResourceType", "name": 7}]`, codeUnreadable, "1.name"},
		{"no writableScopes", `[` + str + `, ` + resource(`, "body": {"$ref": "#/0"}`) + `]`, codeUnreadable,
			"1.writableScopes"},
		{"writableScopes of no number", `[` + str + `, ` + resource(`, "body": {"$ref": "#/0"}, "writableScopes": "8"`) +
			`]`, codeUnreadable, "1.writableScopes"},
		{"a body of no reference", `[` + str + `, ` + resource(`, "body": 2, "writableScopes": 8`) + `]`,
			codeUnreadable, "1.body"},
		{"a body outside the file", `[` + str + `, ` + resource(`, "body": {"$ref": "#/7"}, "writableScopes": 8`) + `]`,
			codeUnreadable, "#/7"},
		{"a body of another file", `[` + str + `, ` + resource(`, "body": {"$ref": "other.json#/0"}, "writableScopes": 8`) +
			`]`, codeUnreadable, "other.json#/0"},
		{"a body that is no object", `[` + str + `, ` + resource(`, "body": {"$ref": "#/0"}, "writableScopes": 8`) + `]`,
			codeUnreadable, "StringType"},
		{"a property outside the file", body(`, "size": {"type": {"$ref": "#/99"}, "flags": 0}`), codeUnreadable, "#/99"},
		{"a property of no type", body(`, "size": {"type": 3, "flags": 0}`), codeUnreadable, "size"},
		{"a property of no flags number", body(`, "size": {"type": {"$ref": "#/3"}, "flags": "1"}`), codeUnreadable,
			"flags"},
		{"a property of a type of no kind", body(`, "size": {"type": {"$ref": "#/4"}, "flags": 0}`), codeUnreadable,
			"#/4"},
		{"not JSON", `[{"$type": `, codeParseFailed, "types.json"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := schemadraft.Import(bytesInput(c.body, "Microsoft.Example/things"), Format, parse)
			d := onlyHalt(t, out.Diagnostics, c.code)
			if !strings.Contains(d.Message, c.member) || d.SourceRef != "types.json" {
				t.Errorf("halt %+v does not name %q and the source", d, c.member)
			}
			if out.Entities != nil || out.Files != nil {
				t.Errorf("an unreadable source drafted %+v", out)
			}
		})
	}
}

func TestArmLiftsFlagsAndUnions(t *testing.T) {
	typ := lifted(t, "shapes.json", shapes)
	if typ.Name != shapes || typ.APIVersion != "2025-02-01" || typ.Source != "shapes.json" {
		t.Errorf("type %s at %s from %s", typ.Name, typ.APIVersion, typ.Source)
	}
	if want := []string{"resourceGroup", "subscription"}; !reflect.DeepEqual(typ.Scopes, want) {
		t.Errorf("scopes %v, want %v", typ.Scopes, want)
	}

	at := func(name string) *schemadraft.Property { return propertyAt(t, typ, "properties."+name) }
	flags := func(p *schemadraft.Property) string {
		return fmt.Sprintf("required %v read-only %v write-only %v create-only %v identifier %v", p.Required,
			p.ReadOnly, p.WriteOnly, p.CreateOnly, p.Identifier)
	}
	// Every member of properties, a deploy-time constant, is replaced on change.
	for name, want := range map[string]string{
		"required":   "required true read-only false write-only false create-only true identifier false",
		"readBack":   "required false read-only true write-only false create-only true identifier false",
		"writeOnly":  "required false read-only false write-only true create-only true identifier false",
		"createOnly": "required false read-only false write-only false create-only true identifier false",
		"identifier": "required false read-only false write-only false create-only true identifier true",
	} {
		if got := flags(at(name)); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	if p := at("required"); p.Description != "Set on every shape." || !reflect.DeepEqual(p.Path, []string{"properties", "required"}) {
		t.Errorf("required: %q at %v", p.Description, p.Path)
	}

	kinds := func(p *schemadraft.Property) string { return fmt.Sprint(p.Kinds) }
	secret := at("secret")
	if !secret.Sensitive || kinds(secret) != "[string]" || *secret.MinLength != 8 || *secret.MaxLength != 64 ||
		secret.Pattern != "^[^ ]+$" {
		t.Errorf("secret %+v", secret)
	}
	if count := at("count"); kinds(count) != "[integer]" || count.Min.String() != "1" || count.Max.String() != "5" {
		t.Errorf("count %+v", count)
	}
	if enabled := at("enabled"); kinds(enabled) != "[boolean]" {
		t.Errorf("enabled %+v", enabled)
	}
	list := at("list")
	if kinds(list) != "[array]" || list.Items == nil || kinds(list.Items) != "[string]" || list.Min.String() != "1" ||
		list.Max.String() != "3" {
		t.Errorf("list %+v", list)
	}
	if mode := at("mode"); kinds(mode) != "[string]" || !reflect.DeepEqual(enumOf(t, mode), []string{"A", "B"}) ||
		len(mode.Dropped) != 0 {
		t.Errorf("mode %+v", mode)
	}
	if literal := at("literal"); kinds(literal) != "[string]" || !reflect.DeepEqual(enumOf(t, literal), []string{"A"}) {
		t.Errorf("literal %+v", literal)
	}
	if tier := at("tier"); kinds(tier) != "[string]" || tier.Enum != nil ||
		!reflect.DeepEqual(droppedOf(tier), []string{"known values A, B"}) {
		t.Errorf("tier %+v", tier)
	}
	if mixed := at("mixed"); kinds(mixed) != "[string integer]" || len(mixed.Dropped) != 1 {
		t.Errorf("mixed %+v", mixed)
	}
	if shape := at("shape"); kinds(shape) != "[object]" || !shape.Open || len(shape.Properties) != 0 ||
		!reflect.DeepEqual(droppedOf(shape), []string{"members of each kind"}) {
		t.Errorf("shape %+v", shape)
	}
	if anything := at("anything"); kinds(anything) != "[any]" {
		t.Errorf("anything %+v", anything)
	}
	if future := at("future"); kinds(future) != "[any]" || !reflect.DeepEqual(droppedOf(future), []string{"type FutureType"}) {
		t.Errorf("future %+v", future)
	}
	if labels := at("labels"); kinds(labels) != "[object]" || !labels.Open ||
		!reflect.DeepEqual(droppedOf(labels), []string{"type of the members it admits beyond those it names"}) {
		t.Errorf("labels %+v", labels)
	}

	// A reference back into an object the property is nested in is cut, and so
	// is an object nested deeper than a draft lifts.
	if label := propertyAt(t, typ, "properties.node.label"); kinds(label) != "[string]" {
		t.Errorf("node.label %+v", label)
	}
	if child := propertyAt(t, typ, "properties.node.child"); child.Cut != "cycle" || !child.Open ||
		kinds(child) != "[object]" || child.Description != "The node below." {
		t.Errorf("node.child %+v", child)
	}
	deep := propertyAt(t, typ, "properties.deep.next.next.next.next.next")
	if deep.Cut != "depth" || !deep.Open || deep.Description != "Too deep." || len(deep.Path) != schemadraft.MaxDepth+1 ||
		deep.Properties != nil {
		t.Errorf("the deepest next %+v", deep)
	}

	// A required discriminated object reaches infra, and its import warns that
	// the members of each kind are not carried.
	out := schemadraft.Import(fileInput(t, "shapes.json", shapes), Format, parse)
	noHalt(t, out.Diagnostics)
	warned := false
	for _, d := range out.Diagnostics {
		if d.Code == codeDropped && d.SourceRef == "properties.shape" && strings.Contains(d.Message, "members of each kind") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no constraint-dropped warning names properties.shape: %+v", out.Diagnostics)
	}
}

// TestArmLiftsDiscriminatedBody lifts a type whose body is a discriminated
// object: the base properties, the discriminator as the enum of its kinds, and
// a member the kinds give different shapes passed whole.
func TestArmLiftsDiscriminatedBody(t *testing.T) {
	typ := lifted(t, "shapes.json", scripts)
	if want := []string{"location", "name", "kind", "properties"}; !reflect.DeepEqual(paths(typ.Properties), want) {
		t.Fatalf("properties %v, want %v", paths(typ.Properties), want)
	}
	kind := propertyAt(t, typ, "kind")
	if !kind.Required || !reflect.DeepEqual(enumOf(t, kind), []string{"cli", "shell"}) || fmt.Sprint(kind.Kinds) != "[string]" {
		t.Errorf("kind %+v", kind)
	}
	props := propertyAt(t, typ, "properties")
	if !props.Required || fmt.Sprint(props.Kinds) != "[any]" || len(props.Properties) != 0 ||
		!reflect.DeepEqual(droppedOf(props), []string{"shape of each kind"}) {
		t.Errorf("properties %+v", props)
	}
	out := schemadraft.Import(fileInput(t, "shapes.json", scripts), Format, parse)
	noHalt(t, out.Diagnostics)
}

func TestArmRootAndPropertiesPlacement(t *testing.T) {
	typ := lifted(t, "vault.json", vault)
	want := []string{"etag", "id", "identity", "location", "name", "sku", "tags", "properties.accessPolicies",
		"properties.adminPassword", "properties.createMode", "properties.enabledForDeployment", "properties.endpoints",
		"properties.provisioningState", "properties.softDeleteRetentionInDays", "properties.tenantId",
		"properties.vaultUri"}
	if got := paths(typ.Properties); !reflect.DeepEqual(got, want) {
		t.Fatalf("properties %v, want %v: root keys first, then the members of properties", got, want)
	}
	if !reflect.DeepEqual(typ.PrimaryIdentifier, [][]string{{"id"}}) {
		t.Errorf("primary identifier %v, want the resource ID", typ.PrimaryIdentifier)
	}
	if sku := propertyAt(t, typ, "sku"); sku.Description != "The pricing tier of the vault." || !sku.Required {
		t.Errorf("sku %+v", sku)
	}
	if name := propertyAt(t, typ, "name"); name.Pattern != "^[a-z][a-z0-9-]{2,23}$" || *name.MaxLength != 24 {
		t.Errorf("name %+v", name)
	}

	placed, _ := schemadraft.Split(typ, Format.Rules)
	var got []string
	for _, pl := range placed {
		got = append(got, fmt.Sprintf("%s %d %s", strings.Join(pl.Property.Path, "."), pl.Pillar, pl.Key))
	}
	wantPlaced := []string{
		fmt.Sprintf("etag %d etag", schemadraft.PillarProvides),
		fmt.Sprintf("id %d id", schemadraft.PillarProvides),
		fmt.Sprintf("identity %d identity", schemadraft.PillarOverrides),
		fmt.Sprintf("identity.principalId %d identity_principal_id", schemadraft.PillarProvides),
		fmt.Sprintf("identity.tenantId %d identity_tenant_id", schemadraft.PillarProvides),
		fmt.Sprintf("location %d location", schemadraft.PillarInfra),
		fmt.Sprintf("name %d name", schemadraft.PillarValues),
		fmt.Sprintf("sku %d sku", schemadraft.PillarInfra),
		fmt.Sprintf("tags %d tags", schemadraft.PillarOverrides),
		fmt.Sprintf("properties.accessPolicies %d accessPolicies", schemadraft.PillarOverrides),
		fmt.Sprintf("properties.adminPassword %d admin_password", schemadraft.PillarSecret),
		fmt.Sprintf("properties.createMode %d createMode", schemadraft.PillarOverrides),
		fmt.Sprintf("properties.enabledForDeployment %d enabledForDeployment", schemadraft.PillarOverrides),
		fmt.Sprintf("properties.endpoints.primary %d endpoints_primary", schemadraft.PillarProvides),
		fmt.Sprintf("properties.endpoints.secondary %d endpoints_secondary", schemadraft.PillarProvides),
		fmt.Sprintf("properties.provisioningState %d provisioning_state", schemadraft.PillarProvides),
		fmt.Sprintf("properties.softDeleteRetentionInDays %d softDeleteRetentionInDays", schemadraft.PillarOverrides),
		fmt.Sprintf("properties.tenantId %d tenant_id", schemadraft.PillarInfra),
		fmt.Sprintf("properties.vaultUri %d vault_uri", schemadraft.PillarProvides),
	}
	if !reflect.DeepEqual(got, wantPlaced) {
		t.Errorf("placed\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(wantPlaced, "\n"))
	}

	// The drafted fragment writes each root key on the resource and each member
	// of properties under its properties.
	out := schemadraft.Import(fileInput(t, "vault.json", vault), Format, parse)
	noHalt(t, out.Diagnostics)
	parts := partsOf(t, fileOf(t, out, vaultRecipe))
	_, doc := renderFragment(t, parts, vaultContext(map[string]any{"tags": map[string]any{"team": "a"},
		"enabledForDeployment": true}))
	res := resourceOf(t, doc, "OrdersVault")
	var rootKeys []string
	for key := range res {
		rootKeys = append(rootKeys, key)
	}
	for _, key := range []string{"type", "apiVersion", "name", "location", "sku", "tags", "properties"} {
		if _, ok := res[key]; !ok {
			t.Errorf("the resource holds no %s: %v", key, rootKeys)
		}
	}
	for _, key := range []string{"tenantId", "enabledForDeployment", "adminPassword", "createMode"} {
		if _, ok := res[key]; ok {
			t.Errorf("the member %s of properties is written on the resource", key)
		}
	}
	props, _ := res["properties"].(map[string]any)
	for _, key := range []string{"tenantId", "enabledForDeployment", "adminPassword"} {
		if _, ok := props[key]; !ok {
			t.Errorf("properties holds no %s: %v", key, props)
		}
	}
	if _, ok := props["location"]; ok {
		t.Errorf("a root key is written under properties: %v", props)
	}
}

func TestArmRefusesTenantOnly(t *testing.T) {
	for _, c := range []struct {
		typ   string
		named []string
	}{
		{"Microsoft.Example/tenantPolicies", []string{"tenant"}},
		{"Microsoft.Example/tenantAndExtensionPolicies", []string{"tenant", "extension"}},
	} {
		t.Run(c.typ, func(t *testing.T) {
			out := schemadraft.Import(fileInput(t, "scopes.json", c.typ), Format, parse)
			d := onlyHalt(t, out.Diagnostics, codeRefused)
			if d.SourceRef != "scopes.json" || !strings.Contains(d.Message, c.typ+"@2025-03-01") {
				t.Errorf("refusal %+v does not name the source and the type", d)
			}
			for _, scope := range c.named {
				if !strings.Contains(d.Message, scope) {
					t.Errorf("refusal %q does not name the scope %s", d.Message, scope)
				}
			}
			if !strings.Contains(d.Message, "resource group, subscription or management group") {
				t.Errorf("refusal %q does not name the scopes the provider deploys at", d.Message)
			}
			if out.Entities != nil || out.Files != nil || out.Decisions != nil {
				t.Errorf("a refused type drafted %+v", out)
			}
		})
	}
	// A type of the same file writable at a scope the provider serves drafts.
	noHalt(t, schemadraft.Import(fileInput(t, "scopes.json", "Microsoft.Example/policies"), Format, parse).Diagnostics)
}

func TestArmRefusesExtensionOnly(t *testing.T) {
	out := schemadraft.Import(fileInput(t, "scopes.json", "Microsoft.Example/extensionPolicies"), Format, parse)
	d := onlyHalt(t, out.Diagnostics, codeRefused)
	if !strings.Contains(d.Message, "Microsoft.Example/extensionPolicies@2025-03-01") || !strings.Contains(d.Message, "extension") ||
		strings.Contains(d.Message, "tenant") {
		t.Errorf("refusal %q, want it to name the type and the extension scope alone", d.Message)
	}
	if out.Entities != nil || out.Files != nil {
		t.Errorf("a refused type drafted %+v", out)
	}
}

// TestArmRefusesTypeWritableNowhere refuses a type writable at no scope, which
// Azure Resource Manager reads and never deploys.
func TestArmRefusesTypeWritableNowhere(t *testing.T) {
	for _, c := range []struct{ file, typ string }{
		{managedIdentity, systemIdentity + "@2024-11-30"},
		{"scopes.json", "Microsoft.Example/readOnlyPolicies@2025-03-01"},
	} {
		out := schemadraft.Import(fileInput(t, c.file, c.typ), Format, parse)
		d := onlyHalt(t, out.Diagnostics, codeRefused)
		if !strings.Contains(d.Message, c.typ) || !strings.Contains(d.Message, "writable at no scope") {
			t.Errorf("refusal %q, want it to name %s writable at no scope", d.Message, c.typ)
		}
	}
}

func TestArmRules(t *testing.T) {
	typ := &schemadraft.Type{Name: vault}
	root := func(name string) *schemadraft.Property {
		return &schemadraft.Property{Name: name, Path: []string{name}}
	}
	inner := func(name string) *schemadraft.Property {
		return &schemadraft.Property{Name: name, Path: []string{"properties", name}}
	}
	r := Format.Rules
	if !r.Placement(typ, root("location")) || r.Placement(typ, inner("location")) {
		t.Errorf("placement takes the root location alone")
	}
	if !r.Identity(typ, root("name")) || r.Identity(typ, inner("name")) {
		t.Errorf("identity takes the root name alone")
	}
	sensitive := inner("password")
	sensitive.Sensitive = true
	if !r.Secret(typ, sensitive) || r.Secret(typ, inner("passwordHint")) {
		t.Errorf("a secret is a sensitive property, whatever its name")
	}
	if !r.NestedProvides {
		t.Errorf("a nested read-back scalar is not provided, though reference reads it by a dotted path")
	}
	if r.NotProvided(typ, root("systemData")) == "" || r.NotProvided(typ, inner("systemData")) != "" ||
		r.NotProvided(typ, root("etag")) != "" {
		t.Errorf("the root systemData alone is left out of provides")
	}
}

// TestArmLeavesSystemDataOutOfProvides drafts a tracked type of the real file
// with none of the metadata Azure Resource Manager keeps on it in its provides,
// the decision naming systemData and why.
func TestArmLeavesSystemDataOutOfProvides(t *testing.T) {
	out := schemadraft.Import(fileInput(t, managedIdentity, userIdentity), Format, parse)
	noHalt(t, out.Diagnostics)
	for _, f := range out.Files {
		if strings.Contains(f.Content, "system_data") || strings.Contains(f.Content, "systemData") {
			t.Errorf("%s drafts the resource's system metadata:\n%s", f.Path, f.Content)
		}
	}
	if len(out.Decisions) != 1 || !strings.Contains(out.Decisions[0].Rationale,
		"systemData is read back and not provided: it is the metadata Azure Resource Manager keeps") {
		t.Errorf("decisions %+v, want the rationale to name systemData and why", out.Decisions)
	}
	// The resource's own read-back values are still provided.
	recipe := fileOf(t, out, "catalog/recipe/arm/microsoft-managedidentity-userassignedidentities/0.1.0/recipe.yaml")
	for _, key := range []string{"principal_id:", "client_id:", "tenant_id:", "id:"} {
		if !strings.Contains(recipe, "    "+key) {
			t.Errorf("the Recipe does not provide %s\n%s", key, recipe)
		}
	}
}

func TestArmCategories(t *testing.T) {
	for name, want := range map[string]string{
		"Microsoft.Storage/storageAccounts":                "storage",
		"microsoft.storage/storageaccounts":                "storage",
		"Microsoft.DBforPostgreSQL/flexibleServers":        "datastore",
		"Microsoft.CognitiveServices/accounts":             "ai-model",
		"Microsoft.ManagedIdentity/userAssignedIdentities": "security",
		"Microsoft.ApiManagement/service":                  "gateway",
		"Microsoft.Network/applicationGateways":            "gateway",
		"Microsoft.Network/virtualNetworks":                "network",
		"Microsoft.ServiceBus/namespaces":                  "messaging",
		"Microsoft.Example/vaults":                         "",
		"Storage/storageAccounts":                          "",
	} {
		got := Format.Category(&schemadraft.Type{Name: name})
		if got != want {
			t.Errorf("category of %s %q, want %q", name, got, want)
		}
		if got != "" && !slicesContains(schemadraft.Categories, got) {
			t.Errorf("category of %s %q is not a catalog category", name, got)
		}
	}
	out := schemadraft.Import(fileInput(t, managedIdentity, userIdentity), Format, parse)
	noHalt(t, out.Diagnostics)
	fileOf(t, out, "catalog/component/imported/security/microsoft-managedidentity-userassignedidentities/0.1.0/component.yaml")
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestArmNeverPanicsOnMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty":                 "",
		"null":                  "null",
		"a string":              `"types"`,
		"a list of null":        `[null]`,
		"a body of null":        `[{"$type": "ResourceType", "name": "A.B/c@1", "body": null, "writableScopes": 8}]`,
		"a self body":           `[{"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/0"}, "writableScopes": 8}]`,
		"a union of itself":     `[{"$type": "UnionType", "elements": [{"$ref": "#/0"}]}, {"$type": "ObjectType", "name": "x", "properties": {"u": {"type": {"$ref": "#/0"}}}}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/1"}, "writableScopes": 8}]`,
		"elements an object":    `[{"$type": "UnionType", "elements": {}}, {"$type": "ObjectType", "name": "x", "properties": {"u": {"type": {"$ref": "#/0"}}}}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/1"}, "writableScopes": 8}]`,
		"properties a list":     `[{"$type": "ObjectType", "name": "x", "properties": []}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/0"}, "writableScopes": 8}]`,
		"a negative reference":  `[{"$type": "ObjectType", "name": "x", "properties": {"u": {"type": {"$ref": "#/-1"}}}}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/0"}, "writableScopes": 8}]`,
		"a huge reference":      `[{"$type": "ObjectType", "name": "x", "properties": {"u": {"type": {"$ref": "#/99999999999999999999"}}}}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/0"}, "writableScopes": 8}]`,
		"minLength a fraction":  `[{"$type": "StringType", "minLength": 1.5}, {"$type": "ObjectType", "name": "x", "properties": {"u": {"type": {"$ref": "#/0"}}}}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/1"}, "writableScopes": 8}]`,
		"a discriminated body":  `[{"$type": "DiscriminatedObjectType", "name": "x", "discriminator": "kind", "baseProperties": [], "elements": {"a": {"$ref": "#/0"}}}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/0"}, "writableScopes": 8}]`,
		"elements of no object": `[{"$type": "StringType"}, {"$type": "DiscriminatedObjectType", "name": "x", "discriminator": "kind", "baseProperties": {}, "elements": {"a": {"$ref": "#/0"}}}, {"$type": "ResourceType", "name": "A.B/c@1", "body": {"$ref": "#/1"}, "writableScopes": 8}]`,
	}
	for _, name := range []string{"vault.json", "shapes.json", managedIdentity} {
		src := string(fixture(t, name))
		for i := 0; i < len(src); i += 211 {
			cases[fmt.Sprintf("%s truncated at %d", name, i)] = src[:i]
		}
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("the import panicked: %v", r)
				}
			}()
			for _, request := range [][]string{nil, {"A.B/c"}, {vault}, {shapes}, {scripts}, {userIdentity}} {
				out := schemadraft.Import(bytesInput(body, request...), Format, parse)
				if len(haltsOf(out.Diagnostics)) > 0 && (out.Entities != nil || out.Files != nil) {
					t.Errorf("a halted import drafted %+v", out)
				}
			}
		})
	}
}
