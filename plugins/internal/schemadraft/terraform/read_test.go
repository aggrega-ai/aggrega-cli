// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package terraform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest/exportgen"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// The codes the tests read, written out rather than taken from the core, so a
// renamed code fails here as it would for a reader of the diagnostics.
const (
	codeUnreadable  = "resource-schema-unreadable"
	codeAmbiguous   = "resource-type-ambiguous"
	codeBeta        = "beta-provider-passed-over"
	codeParseFailed = "parse-failed"
	terraformHost   = "registry.terraform.io"
	opentofuHost    = "registry.opentofu.org"
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

func bytesInput(name string, body []byte) aggrega.ImportInput {
	return aggrega.ImportInput{Format: Format.Name, Name: name, Bytes: body}
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

// listed lists the types of body, which must raise no diagnostic.
func listed(t *testing.T, body []byte) []schemadraft.TypeRef {
	t.Helper()
	refs, diags := Format.List(bytesInput("export.json", body), parse)
	if len(diags) != 0 {
		t.Fatalf("listing raised %+v", diags)
	}
	return refs
}

// lifted lists body and lifts the type name, which must raise no diagnostic.
func lifted(t *testing.T, body []byte, name string) *schemadraft.Type {
	t.Helper()
	for _, ref := range listed(t, body) {
		if ref.Name == name {
			ty, diags := Format.Lift(ref)
			if len(diags) != 0 {
				t.Fatalf("lifting %s raised %+v", name, diags)
			}
			return ty
		}
	}
	t.Fatalf("no type %s listed", name)
	return nil
}

func find(t *testing.T, props []*schemadraft.Property, name string) *schemadraft.Property {
	t.Helper()
	for _, p := range props {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no property %s among %v", name, names(props))
	return nil
}

func names(props []*schemadraft.Property) []string {
	var out []string
	for _, p := range props {
		out = append(out, p.Name)
	}
	return out
}

// export writes a providers schema export holding, for each address, the
// resource types named.
func export(providers ...[]string) []byte {
	var b strings.Builder
	b.WriteString(`{"format_version":"1.0","provider_schemas":{`)
	for i, p := range providers {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%q:{\"resource_schemas\":{", p[0])
		for j, name := range p[1:] {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `%q:{"version":0,"block":{"attributes":{"name":{"type":"string","required":true}}}}`, name)
		}
		b.WriteString("}}")
	}
	b.WriteString("}}")
	return []byte(b.String())
}

func TestTerraformFormat(t *testing.T) {
	f := Format
	if f.Name != "terraform-schema" || f.Capability != "terraform" || f.Syntax != "json" || !f.CountOnly || f.FoldCase {
		t.Errorf("the format is %+v", f)
	}
	if f.Write == nil {
		t.Errorf("the format has no writer")
	}
	if !f.Rules.SecretSlots {
		t.Errorf("a secret is not delivered on a slot")
	}
}

func TestTerraformListsResourcesOnly(t *testing.T) {
	refs := listed(t, fixture(t, "ledger.json"))
	var got []string
	for _, r := range refs {
		got = append(got, r.Name)
		want := &schemadraft.ProviderAddress{Host: terraformHost, Namespace: "example", Type: "ledgers"}
		if r.Provider == nil || *r.Provider != *want {
			t.Errorf("%s has the provider %+v, want %+v", r.Name, r.Provider, want)
		}
		if r.Source != "export.json" {
			t.Errorf("%s has the source %q", r.Name, r.Source)
		}
	}
	if want := []string{"ex_journal", "ex_ledger"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v: a data source, an ephemeral resource or a function is listed", got, want)
	}

	// A body no lift could read is listed all the same: listing decodes none.
	broken := []byte(`{"format_version":"1.0","provider_schemas":{"registry.terraform.io/example/x":` +
		`{"resource_schemas":{"x_broken":5,"x_fine":{"block":{}}}}}}`)
	if got := listed(t, broken); len(got) != 2 || got[0].Name != "x_broken" {
		t.Errorf("a body no lift reads is not listed: %+v", got)
	}
}

func TestTerraformUnreadableExports(t *testing.T) {
	for _, tc := range []struct{ name, body, code string }{
		{"empty object", `{}`, codeUnreadable},
		{"no provider_schemas", `{"format_version":"1.0"}`, codeUnreadable},
		{"no format_version", `{"provider_schemas":{}}`, codeUnreadable},
		{"format_version not a string", `{"format_version":1,"provider_schemas":{}}`, codeUnreadable},
		{"provider_schemas not an object", `{"format_version":"1.0","provider_schemas":[]}`, codeUnreadable},
		{"another format", `{"typeName":"AWS::Example::Ledger","properties":{}}`, codeUnreadable},
		{"resource_schemas not an object", `{"format_version":"1.0","provider_schemas":` +
			`{"registry.terraform.io/example/x":{"resource_schemas":[]}}}`, codeUnreadable},
		{"not json", `{"format_version":`, codeParseFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs, diags := Format.List(bytesInput("export.json", []byte(tc.body)), parse)
			halts := haltsOf(diags)
			if len(refs) != 0 || len(halts) != 1 || halts[0].Code != tc.code || len(diags) != 1 {
				t.Fatalf("listed %+v with %+v, want the one halt %s", refs, diags, tc.code)
			}
			if tc.code == codeUnreadable && !strings.Contains(halts[0].Message, "export.json") {
				t.Errorf("the halt does not name the source: %q", halts[0].Message)
			}
		})
	}
}

// betaRef is the type name of the provider address, as a listing holds it.
func refsOf(t *testing.T, providers ...[]string) []schemadraft.TypeRef {
	t.Helper()
	return listed(t, export(providers...))
}

func TestTerraformBetaSiblingPassedOver(t *testing.T) {
	for _, host := range []string{terraformHost, opentofuHost} {
		t.Run(host, func(t *testing.T) {
			sky, beta := host+"/example/sky", host+"/example/sky-beta"
			for _, order := range [][]string{{sky, beta}, {beta, sky}} {
				refs := refsOf(t, []string{order[0], "sky_widget"}, []string{order[1], "sky_widget"})
				sel, diags := schemadraft.Select(refs, []schemadraft.Request{{Type: "sky_widget"}}, false, true, Format.Resolve)
				if len(sel) != 1 || sel[0].Ref.Provider.Type != "sky" || sel[0].Ref.Provider.Host != host {
					t.Fatalf("selected %+v, want the non-beta provider", sel)
				}
				if len(diags) != 1 || diags[0].Severity != "info" || diags[0].Code != codeBeta ||
					!strings.Contains(diags[0].Message, beta) {
					t.Errorf("diagnostics %+v, want the info %s naming %s", diags, codeBeta, beta)
				}
			}
		})
	}
}

func TestTerraformAmbiguousNamesProviders(t *testing.T) {
	a, b, aBeta := terraformHost+"/example/a", terraformHost+"/example/b", terraformHost+"/example/a-beta"
	for _, tc := range []struct {
		name      string
		providers []string
	}{
		{"three providers", []string{a, b, aBeta}},
		{"two unrelated providers", []string{a, b}},
		{"a beta of another namespace", []string{terraformHost + "/example/a", terraformHost + "/other/a-beta"}},
		{"a beta of another host", []string{terraformHost + "/example/a", opentofuHost + "/example/a-beta"}},
		{"two betas of different providers", []string{aBeta, terraformHost + "/example/b-beta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var providers [][]string
			for _, p := range tc.providers {
				providers = append(providers, []string{p, "thing_one"})
			}
			refs := refsOf(t, providers...)
			sel, diags := schemadraft.Select(refs, []schemadraft.Request{{Type: "thing_one"}}, false, true, Format.Resolve)
			halts := haltsOf(diags)
			if len(sel) != 0 || len(halts) != 1 || halts[0].Code != codeAmbiguous || len(diags) != 1 {
				t.Fatalf("selected %+v with %+v, want the one halt %s", sel, diags, codeAmbiguous)
			}
			for _, p := range tc.providers {
				if !strings.Contains(halts[0].Message, p) {
					t.Errorf("the halt does not name %s: %q", p, halts[0].Message)
				}
			}
			if !strings.Contains(halts[0].Message, "requiring") {
				t.Errorf("the halt does not give the fix: %q", halts[0].Message)
			}
		})
	}
}

func TestTerraformLiftsBlocksAndNesting(t *testing.T) {
	ty := lifted(t, fixture(t, "ledger.json"), "ex_ledger")
	if ty.Name != "ex_ledger" || ty.Description != "A ledger that records entries." || ty.Source != "export.json" {
		t.Errorf("the type is %q %q %q", ty.Name, ty.Description, ty.Source)
	}
	if ty.Provider == nil || ty.Provider.Version != "" || ty.Provider.Type != "ledgers" {
		t.Errorf("the provider is %+v", ty.Provider)
	}
	if !slices.IsSorted(names(ty.Properties)) {
		t.Errorf("the properties are not in the tree's order: %v", names(ty.Properties))
	}

	kinds := func(p *schemadraft.Property) []schemadraft.Kind { return p.Kinds }
	for name, want := range map[string]schemadraft.Kind{
		"name": schemadraft.KindString, "capacity": schemadraft.KindNumber, "enabled": schemadraft.KindBoolean,
		"tags": schemadraft.KindObject, "zones": schemadraft.KindArray, "limits": schemadraft.KindObject,
		"pair": schemadraft.KindArray, "payload": schemadraft.KindAny, "rows": schemadraft.KindArray,
	} {
		if got := kinds(find(t, ty.Properties, name)); !slices.Equal(got, []schemadraft.Kind{want}) {
			t.Errorf("%s has the kinds %v, want %v", name, got, want)
		}
	}
	if p := find(t, ty.Properties, "tags"); !p.Open {
		t.Errorf("a map is not open")
	}
	zones := find(t, ty.Properties, "zones")
	if zones.Items == nil || !slices.Equal(zones.Items.Kinds, []schemadraft.Kind{schemadraft.KindString}) {
		t.Errorf("a set of strings has the items %+v", zones.Items)
	}
	rows := find(t, ty.Properties, "rows")
	if rows.Items == nil || !slices.Equal(rows.Items.Kinds, []schemadraft.Kind{schemadraft.KindArray}) ||
		rows.Items.Items == nil || rows.Items.Items.Kinds[0] != schemadraft.KindString {
		t.Errorf("a list of lists has the items %+v", rows.Items)
	}
	limits := find(t, ty.Properties, "limits")
	if got := names(limits.Properties); !slices.Equal(got, []string{"burst", "rate"}) ||
		limits.Properties[0].Kinds[0] != schemadraft.KindNumber || limits.Properties[1].Path[1] != "rate" {
		t.Errorf("an object has the members %v", got)
	}
	if d := find(t, ty.Properties, "name").Description; d != "The ledger's name." {
		t.Errorf("the description is %q", d)
	}

	name := find(t, ty.Properties, "name")
	if !name.Required || name.ReadOnly {
		t.Errorf("name: required %v read-only %v", name.Required, name.ReadOnly)
	}
	if p := find(t, ty.Properties, "identifier"); p.ReadOnly || p.Required {
		t.Errorf("a computed and optional attribute is read back or required")
	}
	if p := find(t, ty.Properties, "arn"); !p.ReadOnly || p.Required || p.Sensitive {
		t.Errorf("arn: %+v", p)
	}
	if p := find(t, ty.Properties, "token"); !p.ReadOnly || !p.Sensitive {
		t.Errorf("token: %+v", p)
	}
	if p := find(t, ty.Properties, "password"); p.ReadOnly || !p.Sensitive {
		t.Errorf("password: %+v", p)
	}
	if p := find(t, ty.Properties, "legacy_mode"); !p.Deprecated {
		t.Errorf("legacy_mode is not deprecated")
	}
	if p := find(t, ty.Properties, "one_time"); !p.WriteOnly || p.ReadOnly {
		t.Errorf("one_time: %+v", p)
	}

	settings := find(t, ty.Properties, "settings")
	if settings.Block == nil || settings.Block.Nesting != "list" || settings.Block.MaxItems != 2 ||
		settings.Items == nil || find(t, settings.Items.Properties, "level") == nil {
		t.Errorf("a nested-type attribute is %+v", settings)
	}

	retention := find(t, ty.Properties, "retention")
	if !retention.Required || retention.Block == nil ||
		*retention.Block != (schemadraft.Block{Nesting: "list", MinItems: 1, MaxItems: 3}) {
		t.Errorf("retention: required %v block %+v", retention.Required, retention.Block)
	}
	if !slices.Equal(retention.Kinds, []schemadraft.Kind{schemadraft.KindArray}) || retention.Items == nil ||
		!slices.Equal(retention.Items.Kinds, []schemadraft.Kind{schemadraft.KindObject}) {
		t.Fatalf("a list block is %+v", retention)
	}
	if retention.Description != "How long entries stay." {
		t.Errorf("the block's description is %q", retention.Description)
	}
	days := find(t, retention.Items.Properties, "days")
	if !days.Required || !slices.Equal(days.Path, []string{"retention", "days"}) {
		t.Errorf("days: %+v", days)
	}
	archive := find(t, retention.Items.Properties, "archive")
	if archive.Required || archive.Block == nil || archive.Block.Nesting != "single" ||
		!slices.Equal(archive.Kinds, []schemadraft.Kind{schemadraft.KindObject}) ||
		find(t, archive.Properties, "target").Path[2] != "target" {
		t.Errorf("a single block is %+v", archive)
	}
	if alert := find(t, ty.Properties, "alert"); alert.Required || alert.Block.Nesting != "set" ||
		alert.Kinds[0] != schemadraft.KindArray {
		t.Errorf("a set block is %+v", alert)
	}
	if label := find(t, ty.Properties, "label"); label.Block.Nesting != "map" || label.Kinds[0] != schemadraft.KindObject ||
		!label.Open {
		t.Errorf("a map block is %+v", label)
	}
	if window := find(t, ty.Properties, "window"); window.Block.Nesting != "single" || window.Block.MaxItems != 0 {
		t.Errorf("a single block is %+v", window)
	}
}

func TestTerraformCutsDepth(t *testing.T) {
	body := exportgen.Generate(exportgen.Spec{Providers: []string{terraformHost + "/example/deep"},
		TypesPerProvider: 1, AttributesPerType: 2, BlockDepth: schemadraft.MaxDepth + 3})
	refs := listed(t, body)
	ty, diags := Format.Lift(refs[0])
	if len(diags) != 0 {
		t.Fatal(diags)
	}
	p := find(t, ty.Properties, "nested_1")
	depth := 1
	for p.Cut == "" {
		if p.Items == nil {
			t.Fatalf("the nesting ends at depth %d without a cut", depth)
		}
		depth++
		p = find(t, p.Items.Properties, fmt.Sprintf("nested_%d", depth))
	}
	if p.Cut != "depth" || depth != schemadraft.MaxDepth+1 || !p.Open || p.Items != nil || len(p.Properties) != 0 {
		t.Errorf("cut %q at depth %d: %+v", p.Cut, depth, p)
	}
}

func TestTerraformUnreadableTypeBody(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a body that is not an object", `5`},
		{"an attribute that is not an object", `{"block":{"attributes":{"a":5}}}`},
		{"an attribute without a type", `{"block":{"attributes":{"a":{"required":true}}}}`},
		{"a type that is unknown", `{"block":{"attributes":{"a":{"type":["fraction","string"]}}}}`},
		{"a block that is not an object", `{"block":{"block_types":{"b":5}}}`},
		{"a block without a nesting mode", `{"block":{"block_types":{"b":{"block":{}}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := schemadraft.TypeRef{Name: "x_thing", Source: "export.json", Body: json.RawMessage(tc.body),
				Provider: &schemadraft.ProviderAddress{Host: terraformHost, Namespace: "example", Type: "x"}}
			ty, diags := Format.Lift(ref)
			halts := haltsOf(diags)
			if ty != nil || len(halts) != 1 || halts[0].Code != codeUnreadable || !strings.Contains(halts[0].Message, "x_thing") {
				t.Errorf("lifted %+v with %+v, want the one halt %s naming the type", ty, diags, codeUnreadable)
			}
		})
	}
}

func TestOpenTofuAddressesReadAsTheirShortSource(t *testing.T) {
	for _, host := range []string{terraformHost, opentofuHost} {
		refs := refsOf(t, []string{host + "/hashicorp/google", "google_thing"}, []string{host + "/hashicorp/google-beta", "google_thing"})
		var got []schemadraft.ProviderAddress
		for _, r := range refs {
			got = append(got, *r.Provider)
		}
		want := []schemadraft.ProviderAddress{
			{Host: host, Namespace: "hashicorp", Type: "google"},
			{Host: host, Namespace: "hashicorp", Type: "google-beta"},
		}
		if !slices.Equal(got, want) {
			t.Errorf("under %s the addresses read %+v, want %+v", host, got, want)
		}
		sel, diags := schemadraft.Select(refs, nil, false, true, Format.Resolve)
		if len(sel) != 1 || sel[0].Ref.Provider.Type != "google" || len(diags) != 1 || diags[0].Code != codeBeta {
			t.Errorf("under %s selected %+v with %+v, want google and %s", host, sel, diags, codeBeta)
		}
	}
	// What a mirror writes in full reads in full, not as a short source.
	refs := refsOf(t, []string{"mirror.example.com/team/aws", "aws_thing"})
	if want := (schemadraft.ProviderAddress{Host: "mirror.example.com", Namespace: "team", Type: "aws"}); *refs[0].Provider != want {
		t.Errorf("the address reads %+v, want %+v", *refs[0].Provider, want)
	}
}

// typeOf is a lifted type of the provider type pt, named name.
func typeOf(pt, name string) *schemadraft.Type {
	return &schemadraft.Type{Name: name, Provider: &schemadraft.ProviderAddress{Host: terraformHost,
		Namespace: "example", Type: pt}}
}

func TestTerraformPlacementPerProvider(t *testing.T) {
	place := Format.Rules.Placement
	for _, tc := range []struct {
		provider string
		want     []string
	}{
		{"aws", []string{"region"}},
		{"azurerm", []string{"location", "resource_group_name"}},
		{"google", []string{"project", "region", "zone", "location"}},
		{"google-beta", []string{"project", "region", "zone", "location"}},
		{"random", nil},
	} {
		ty := typeOf(tc.provider, tc.provider+"_thing")
		for _, name := range []string{"region", "location", "resource_group_name", "project", "zone", "name", "bucket"} {
			got := place(ty, &schemadraft.Property{Name: name})
			if want := slices.Contains(tc.want, name); got != want {
				t.Errorf("%s: placement of %s is %v, want %v", tc.provider, name, got, want)
			}
		}
	}
	if place(&schemadraft.Type{Name: "aws_thing"}, &schemadraft.Property{Name: "region"}) {
		t.Errorf("a type without a provider has a placement")
	}
}

func TestTerraformIdentity(t *testing.T) {
	identity := Format.Rules.Identity
	for _, tc := range []struct {
		typ, prop string
		want      bool
	}{
		{"aws_s3_bucket", "bucket", true},
		{"aws_s3_bucket", "name", true},
		{"aws_s3_bucket", "identifier", true},
		{"aws_s3_bucket", "queue", false},
		{"aws_s3_bucket", "bucket_prefix", false},
		{"aws_sqs_queue", "queue", true},
		{"azurerm_storage_account", "account", true},
		{"ex_ledger", "ledger", true},
		{"ex_ledger", "region", false},
	} {
		if got := identity(typeOf("aws", tc.typ), &schemadraft.Property{Name: tc.prop}); got != tc.want {
			t.Errorf("%s: identity of %s is %v, want %v", tc.typ, tc.prop, got, tc.want)
		}
	}
}

func TestTerraformSecret(t *testing.T) {
	secret := Format.Rules.Secret
	ty := typeOf("aws", "aws_thing")
	for _, tc := range []struct {
		name string
		p    schemadraft.Property
		want bool
	}{
		{"sensitive and writable", schemadraft.Property{Sensitive: true}, true},
		{"sensitive and write-only", schemadraft.Property{Sensitive: true, WriteOnly: true}, true},
		{"sensitive and read back", schemadraft.Property{Sensitive: true, ReadOnly: true}, false},
		{"not sensitive", schemadraft.Property{Name: "password"}, false},
	} {
		if got := secret(ty, &tc.p); got != tc.want {
			t.Errorf("%s: secret is %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTerraformCategories(t *testing.T) {
	for name, want := range map[string]string{
		"aws_db_instance":              "datastore",
		"aws_sqs_queue":                "messaging",
		"aws_s3_bucket":                "storage",
		"aws_cloudwatch_event_rule":    "messaging",
		"aws_cloudwatch_log_group":     "observability",
		"azurerm_storage_account":      "storage",
		"azurerm_servicebus_queue":     "messaging",
		"google_sql_database_instance": "datastore",
		"google_pubsub_topic":          "messaging",
		"aws_vpc":                      "",
		"ex_ledger":                    "",
		"":                             "",
		"azurerm_resource_group":       "",
		"google_compute_instance":      "",
	} {
		if got := Format.Category(&schemadraft.Type{Name: name}); got != want {
			t.Errorf("the category of %q is %q, want %q", name, got, want)
		}
	}
	for _, e := range categoryPrefixes {
		if !slices.Contains(schemadraft.Categories, e.category) {
			t.Errorf("the prefix %q names the category %q, which is not one of the categories", e.prefix, e.category)
		}
	}
}

func TestTerraformListsLargeExport(t *testing.T) {
	body := exportgen.Generate(exportgen.Spec{Providers: []string{terraformHost + "/example/big", terraformHost + "/example/big-beta"},
		TypesPerProvider: 900, AttributesPerType: 40, BlockDepth: 2})
	refs := listed(t, body)
	if len(refs) != 1800 {
		t.Fatalf("listed %d types, want 1800", len(refs))
	}
	ty, diags := Format.Lift(refs[0])
	if len(diags) != 0 || len(ty.Properties) != 41 {
		t.Errorf("lifted %d properties with %+v", len(ty.Properties), diags)
	}
}

// liftAttribute lifts a type of one attribute, typed as the cty type typ.
func liftAttribute(t *testing.T, typ string) *schemadraft.Property {
	t.Helper()
	ref := schemadraft.TypeRef{Name: "x_thing", Source: "export.json", Body: json.RawMessage(
		`{"block":{"attributes":{"a":{"optional":true,"type":` + typ + `}}}}`)}
	ty, diags := Format.Lift(ref)
	if len(diags) != 0 {
		t.Fatalf("lifting %s raised %+v", typ, diags)
	}
	return ty.Properties[0]
}

func TestTerraformReportsDroppedMapElementType(t *testing.T) {
	p := liftAttribute(t, `["map","string"]`)
	want := []string{"map element type: the draft carries a map as an open object, without the type of its values"}
	if !slices.Equal(p.Dropped, want) {
		t.Errorf("dropped %q, want %q", p.Dropped, want)
	}
	// An element of a list is reported on the element.
	if q := liftAttribute(t, `["list",["map","number"]]`); len(q.Dropped) != 0 || len(q.Items.Dropped) != 1 {
		t.Errorf("a list of maps reports %q on the list and %q on its items", q.Dropped, q.Items.Dropped)
	}
}

func TestTerraformReportsDroppedTupleElementTypes(t *testing.T) {
	p := liftAttribute(t, `["tuple",["string","number"]]`)
	want := []string{"tuple element types: the draft carries a tuple as an array, without the type of each position"}
	if !slices.Equal(p.Dropped, want) {
		t.Errorf("dropped %q, want %q", p.Dropped, want)
	}
}

func TestTerraformReportsDroppedObjectRequirements(t *testing.T) {
	p := liftAttribute(t, `["object",{"a":"string","b":"number","c":"bool"},["b","c"]]`)
	want := []string{"object optional attributes: the type marks b, c optional and requires its other members, " +
		"and the draft marks no member required"}
	if !slices.Equal(p.Dropped, want) {
		t.Errorf("dropped %q, want %q", p.Dropped, want)
	}
	p = liftAttribute(t, `["object",{"a":"string"}]`)
	want = []string{"object required attributes: the type requires every member, and the draft marks no member required"}
	if !slices.Equal(p.Dropped, want) {
		t.Errorf("dropped %q, want %q", p.Dropped, want)
	}
	// A primitive drops nothing.
	if p := liftAttribute(t, `"string"`); len(p.Dropped) != 0 {
		t.Errorf("a string drops %q", p.Dropped)
	}
}

func TestTerraformDepthCutReadsNoDiscardedSubtree(t *testing.T) {
	// A block nested beyond the depth limit holds a member no lift could read.
	inner := `{"nesting_mode":"list","block":{"description":"Too deep.","attributes":{"bad":{"type":["fraction","x"]}}}}`
	body := inner
	for i := schemadraft.MaxDepth; i >= 1; i-- {
		body = fmt.Sprintf(`{"nesting_mode":"list","block":{"block_types":{"n%d":%s}}}`, i+1, body)
	}
	ref := schemadraft.TypeRef{Name: "x_thing", Source: "export.json",
		Body: json.RawMessage(`{"block":{"block_types":{"n1":` + body + `}}}`)}
	ty, diags := Format.Lift(ref)
	if len(diags) != 0 || ty == nil {
		t.Fatalf("lifted %+v with %+v, want no halt", ty, diags)
	}
	p := find(t, ty.Properties, "n1")
	for depth := 1; p.Cut == ""; depth++ {
		if p.Items == nil {
			t.Fatalf("no cut at depth %d", depth)
		}
		p = p.Items.Properties[0]
	}
	if p.Cut != "depth" || p.Description != "Too deep." || len(p.Path) != schemadraft.MaxDepth+1 {
		t.Errorf("the cut is %+v", p)
	}
}
