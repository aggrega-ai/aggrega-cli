// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

func TestSnakeKey(t *testing.T) {
	for _, c := range []struct{ vendor, want string }{
		{"KmsKeyId", "kms_key_id"},
		{"DBInstanceIdentifier", "db_instance_identifier"},
		{"administratorLoginPassword", "administrator_login_password"},
		{"VPCSecurityGroupIds", "vpc_security_group_ids"},
		{"already_snake", "already_snake"},
		{"Name", "name"},
		{"KMSKeyId", "kms_key_id"},
		{"S3Bucket", "s3_bucket"},
		{"ipv6Address", "ipv6_address"},
		{"Endpoint.Address", "endpoint_address"},
		{"with-hyphen  and space", "with_hyphen_and_space"},
	} {
		if got := SnakeKey(c.vendor); got != c.want {
			t.Errorf("SnakeKey(%q) = %q, want %q", c.vendor, got, c.want)
		}
	}
}

// TestSnakeKeyCollisionsDraftDistinctKeys holds two vendor names reaching one
// snake_case key, and a vendor name reaching the overrides map infra always
// holds, to distinct keys whose rule names both, and the Component drafted from
// them, with keys named like schema keywords, to loading in the catalog.
func TestSnakeKeyCollisionsDraftDistinctKeys(t *testing.T) {
	var props []*Property
	for _, name := range []string{"KMSKeyId", "KmsKeyId", "Overrides", "Required", "Type"} {
		p := prop(name, KindString)
		p.Required = true
		props = append(props, p)
	}
	typ := typeOf(props...)
	placed, _ := Split(typ, testRules)
	for vendor, key := range map[string]string{
		"KMSKeyId": "kms_key_id", "KmsKeyId": "kms_key_id_2", "Overrides": "overrides_2",
		"Required": "required", "Type": "type",
	} {
		if got := placedAs(t, placed, vendor); got.Pillar != PillarInfra || got.Key != key {
			t.Errorf("%s placed %+v, want infra key %s", vendor, got, key)
		}
	}
	if rule := placedAs(t, placed, "KmsKeyId").Rule; !strings.Contains(rule, "KmsKeyId") || !strings.Contains(rule, "KMSKeyId") {
		t.Errorf("KmsKeyId rule %q, want it to name both vendor names", rule)
	}
	if rule := placedAs(t, placed, "Overrides").Rule; !strings.Contains(rule, "Overrides") ||
		!strings.Contains(rule, "overrides_2") || !strings.Contains(rule, "overrides ") {
		t.Errorf("Overrides rule %q, want it to name the vendor name and the overrides map", rule)
	}

	draft := ComponentFor("imported/component/test-service-thing", typ, placed, "test-schema", testRules)
	body := loadDrafted(t, draft)
	want := []string{"kms_key_id", "kms_key_id_2", "overrides_2", "required", "type", "overrides"}
	if got := pillarKeys(t, body, "infra"); !reflect.DeepEqual(got, want) {
		t.Fatalf("infra keys %v, want %v", got, want)
	}
}

// respelled runs the text Respell writes for p over value, read at .infra.v as
// a Recipe template reads a pillar, through the compiler's template functions
// with a missing key halting, and returns the value it writes.
func respelled(t *testing.T, p *Property, value any) any {
	t.Helper()
	text := Respell(p, ".infra.v")
	if strings.ContainsAny(text, "\n") || strings.Contains(text, "{{-") || strings.Contains(text, "-}}") {
		t.Fatalf("Respell wrote %q, want one line with no trim marker, to stand after a key", text)
	}
	tpl, err := template.New("respell").Option("missingkey=error").Funcs(gotmpl.FuncMap()).Parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	if len(tpl.Templates()) != 1 {
		t.Fatalf("Respell wrote %q, which defines a template", text)
	}
	var out strings.Builder
	if err := tpl.Execute(&out, map[string]any{"infra": map[string]any{"v": value}}); err != nil {
		t.Fatalf("execute %q: %v", text, err)
	}
	var got any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("Respell wrote %q, which is not JSON: %v", out.String(), err)
	}
	return got
}

func jsonValue(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRespellRebuildsANestedObject(t *testing.T) {
	p := prop("Settings", KindObject)
	key := member(p.Path, "KmsKeyId", KindString)
	limits := member(p.Path, "Limits", KindObject)
	limits.Properties = []*Property{member(limits.Path, "MaxSize", KindInteger), member(limits.Path, "Unit", KindString)}
	p.Properties = []*Property{key, limits}
	got := respelled(t, p, jsonValue(t, `{"kms_key_id": "k", "limits": {"max_size": 3, "unit": "GiB"}}`))
	if want := jsonValue(t, `{"KmsKeyId": "k", "Limits": {"MaxSize": 3, "Unit": "GiB"}}`); !reflect.DeepEqual(got, want) {
		t.Fatalf("respelled %v, want %v", got, want)
	}
}

func TestRespellRebuildsAListOfObjects(t *testing.T) {
	p := prop("Rules", KindArray)
	item := &Property{Name: "Rules", Kinds: []Kind{KindObject}}
	item.Properties = []*Property{member(nil, "CidrIp", KindString), member(nil, "FromPort", KindInteger)}
	p.Items = item
	got := respelled(t, p, jsonValue(t, `[{"cidr_ip": "10.0.0.0/8", "from_port": 443}, {"cidr_ip": "192.0.2.0/24"}]`))
	if want := jsonValue(t, `[{"CidrIp": "10.0.0.0/8", "FromPort": 443}, {"CidrIp": "192.0.2.0/24"}]`); !reflect.DeepEqual(got, want) {
		t.Fatalf("respelled %v, want %v", got, want)
	}
}

func TestRespellPassesFalseAndZero(t *testing.T) {
	p := prop("Settings", KindObject)
	p.Properties = []*Property{
		member(p.Path, "Count", KindInteger), member(p.Path, "Enabled", KindBoolean), member(p.Path, "Name", KindString),
	}
	got := respelled(t, p, jsonValue(t, `{"count": 0, "enabled": false, "name": ""}`))
	if want := jsonValue(t, `{"Count": 0, "Enabled": false, "Name": ""}`); !reflect.DeepEqual(got, want) {
		t.Fatalf("respelled %v, want %v", got, want)
	}
}

func TestRespellLeavesAnAbsentKeyOut(t *testing.T) {
	p := prop("Settings", KindObject)
	p.Properties = []*Property{member(p.Path, "Count", KindInteger), member(p.Path, "Name", KindString)}
	got := respelled(t, p, jsonValue(t, `{"name": "n"}`))
	if want := jsonValue(t, `{"Name": "n"}`); !reflect.DeepEqual(got, want) {
		t.Fatalf("respelled %v, want %v", got, want)
	}
}

func TestRespellPassesAnOpenObjectWhole(t *testing.T) {
	p := prop("Settings", KindObject)
	tags := member(p.Path, "Tags", KindObject)
	tags.Open = true
	tags.Properties = []*Property{member(tags.Path, "Team", KindString)}
	p.Properties = []*Property{tags}
	got := respelled(t, p, jsonValue(t, `{"tags": {"Team": "a", "cost_center": "b"}}`))
	if want := jsonValue(t, `{"Tags": {"Team": "a", "cost_center": "b"}}`); !reflect.DeepEqual(got, want) {
		t.Fatalf("respelled %v, want %v", got, want)
	}
}

func TestRespellOfAScalarIsItsValue(t *testing.T) {
	if got, want := Respell(prop("Name", KindString), ".infra.name"), "{{ toJson .infra.name }}"; got != want {
		t.Fatalf("Respell = %q, want %q", got, want)
	}
}

// TestRespellReadsTheKeysCarryWrites holds the template to the member keys the
// Component describes, a suffixed one included.
func TestRespellReadsTheKeysCarryWrites(t *testing.T) {
	p := prop("Settings", KindObject)
	p.Properties = []*Property{member(p.Path, "KMSKeyId", KindString), member(p.Path, "KmsKeyId", KindString)}
	n, _ := Carry(p, "settings")
	if len(n.Properties) != 2 || n.Properties[0].Name != "kms_key_id" || n.Properties[1].Name != "kms_key_id_2" {
		t.Fatalf("carried members %+v, want kms_key_id and kms_key_id_2", n.Properties)
	}
	got := respelled(t, p, jsonValue(t, `{"kms_key_id": "a", "kms_key_id_2": "b"}`))
	if want := jsonValue(t, `{"KMSKeyId": "a", "KmsKeyId": "b"}`); !reflect.DeepEqual(got, want) {
		t.Fatalf("respelled %v, want %v", got, want)
	}
}

// TestRespellerValueBuildsIntoADict sets the values one Respeller rebuilds into
// a dict of one template text, as a writer building a whole body does: the
// statements of two values never share a variable, they write nothing, and the
// dict holds each value under the vendor's names.
func TestRespellerValueBuildsIntoADict(t *testing.T) {
	settings := prop("Settings", KindObject)
	limits := member(settings.Path, "Limits", KindObject)
	limits.Properties = []*Property{member(limits.Path, "MaxSize", KindInteger)}
	settings.Properties = []*Property{member(settings.Path, "KmsKeyId", KindString), limits}
	rules := prop("Rules", KindArray)
	rules.Items = &Property{Name: "Rules", Kinds: []Kind{KindObject}}
	rules.Items.Properties = []*Property{member(nil, "CidrIp", KindString)}

	var r Respeller
	s1, v1 := r.Value(settings, `(index .infra "settings")`)
	s2, v2 := r.Value(rules, `(index .infra "rules")`)
	if v1 == v2 {
		t.Fatalf("two values are held by one variable %s", v1)
	}
	for _, s := range []string{s1, s2} {
		if s == "" || strings.ContainsAny(s, "\n") || strings.Contains(s, "{{-") || strings.Contains(s, "-}}") {
			t.Fatalf("statements %q, want one line of actions with no trim marker", s)
		}
	}
	text := `{{ $body := dict }}` + s1 + `{{ $_ := set $body "Settings" ` + v1 + ` }}` + s2 +
		`{{ $_ := set $body "Rules" ` + v2 + ` }}{{ toJson $body }}`
	tpl, err := template.New("dict").Option("missingkey=error").Funcs(gotmpl.FuncMap()).Parse(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	var out strings.Builder
	infra := jsonValue(t, `{"settings": {"kms_key_id": "k", "limits": {"max_size": 3}}, "rules": [{"cidr_ip": "10.0.0.0/8"}]}`)
	if err := tpl.Execute(&out, map[string]any{"infra": infra}); err != nil {
		t.Fatalf("execute %q: %v", text, err)
	}
	want := jsonValue(t, `{"Settings": {"KmsKeyId": "k", "Limits": {"MaxSize": 3}}, "Rules": [{"CidrIp": "10.0.0.0/8"}]}`)
	if got := jsonValue(t, out.String()); !reflect.DeepEqual(got, want) {
		t.Fatalf("built %v, want %v", got, want)
	}

	// A value passed whole takes no statement and is its own expression.
	if s, v := r.Value(prop("Name", KindString), ".infra.name"); s != "" || v != ".infra.name" {
		t.Errorf("a scalar gives %q and %q, want no statement and its expression", s, v)
	}
	// Respell is a fresh Respeller's statements followed by toJson of its value.
	var fresh Respeller
	s, v := fresh.Value(settings, ".infra.v")
	if got := Respell(settings, ".infra.v"); got != s+"{{ toJson "+v+" }}" {
		t.Errorf("Respell wrote %q, want the statements and toJson of %s", got, v)
	}
}
