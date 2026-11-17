// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// loadDrafted writes the Component d describes through the kit's writer, loads
// it in a catalog of its own, failing where the catalog refuses it, and returns
// the written body read as YAML.
func loadDrafted(t *testing.T, d importer.ComponentDraft) *yaml.Node {
	t.Helper()
	draft := importer.DraftComponent(d)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "component.yaml"), []byte(draft.Entity.Body), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.LoadAll("", dir)
	if err != nil {
		t.Fatalf("the catalog refuses the drafted Component: %v\n%s", err, draft.Entity.Body)
	}
	if _, ok := cat.Components[d.Name+":"+d.Version]; !ok {
		t.Fatalf("the catalog holds no %s:%s\n%s", d.Name, d.Version, draft.Entity.Body)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(draft.Entity.Body), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

// at returns the value under the keys path of the mapping n, or nil.
func at(n *yaml.Node, path ...string) *yaml.Node {
	for _, key := range path {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
			}
		}
		n = next
	}
	return n
}

// keysOf returns the keys of the mapping n, in order.
func keysOf(n *yaml.Node) []string {
	var out []string
	for i := 0; n != nil && i+1 < len(n.Content); i += 2 {
		out = append(out, n.Content[i].Value)
	}
	return out
}

func pillarKeys(t *testing.T, body *yaml.Node, pillar string) []string {
	t.Helper()
	return keysOf(at(body, "spec", "schema", pillar, "properties"))
}

// sampleType holds a property for each rule, each described by the vendor but
// the identity.
func sampleType() *Type {
	name := prop("QueueName", KindString)
	location := prop("location", KindString)
	location.Required, location.Description = true, "Where it runs."
	class := prop("InstanceClass", KindString)
	class.Required, class.Description = true, "The size."
	password := prop("MasterUserPassword", KindString)
	password.Required, password.Description = true, "The password."
	arn := prop("Arn", KindString)
	arn.ReadOnly, arn.Description = true, "The ARN."
	delay := prop("DelaySeconds", KindInteger)
	delay.Description = "The delay."
	typ := typeOf(arn, delay, class, password, name, location)
	typ.Description = "Provides a thing."
	return typ
}

func draftOf(t *testing.T, name string, r Rules) (importer.ComponentDraft, []Placed) {
	t.Helper()
	typ := sampleType()
	placed, _ := Split(typ, r)
	return ComponentFor(name, typ, placed, "test-schema", r), placed
}

func TestComponentForLabelsAndDescription(t *testing.T) {
	d, _ := draftOf(t, "imported/messaging/test-service-thing", testRules)
	want := []importer.Label{
		{Key: "component.aggrega.ai/type", Value: "resource"},
		{Key: "component.aggrega.ai/category", Value: "messaging"},
		{Key: "component.aggrega.ai/hosting", Value: "managed"},
	}
	if !reflect.DeepEqual(d.Labels, want) {
		t.Errorf("labels %+v, want %+v", d.Labels, want)
	}
	paragraphs := strings.Split(d.Description, "\n\n")
	if len(paragraphs) != 2 || paragraphs[0] != Marked("Provides a thing.") ||
		!strings.Contains(paragraphs[1], "test-schema") || !strings.Contains(paragraphs[1], "Test::Service::Thing") {
		t.Errorf("description %q, want the marked type description and a sentence naming the format and the type",
			d.Description)
	}
	loadDrafted(t, d)
}

func TestComponentForCategoryOutsideTheClosedSetIsNotLabelled(t *testing.T) {
	d, _ := draftOf(t, "imported/component/test-service-thing", testRules)
	for _, l := range d.Labels {
		if l.Key == "component.aggrega.ai/category" {
			t.Fatalf("labels %+v carry a category for a name outside the closed set", d.Labels)
		}
	}
}

func TestComponentForPillars(t *testing.T) {
	body := loadDrafted(t, mustDraft(t, testRules))
	if got := pillarKeys(t, body, "values"); !reflect.DeepEqual(got, []string{"queue_name"}) {
		t.Errorf("values %v, want [queue_name]", got)
	}
	if got := pillarKeys(t, body, "infra"); !reflect.DeepEqual(got, []string{"instance_class", "location", "overrides"}) {
		t.Errorf("infra %v, want [instance_class location overrides]", got)
	}
	if got := pillarKeys(t, body, "provides"); !reflect.DeepEqual(got, []string{"arn"}) {
		t.Errorf("provides %v, want [arn]", got)
	}
	overrides := at(body, "spec", "schema", "infra", "properties", "overrides")
	if at(overrides, "additionalProperties").Value != "true" || at(overrides, "description") == nil {
		t.Errorf("overrides is not an open map with a description: %+v", keysOf(overrides))
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		for i := 0; n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "default" {
				t.Errorf("the draft carries a default at line %d", n.Content[i].Line)
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(body)
}

func mustDraft(t *testing.T, r Rules) importer.ComponentDraft {
	d, _ := draftOf(t, "imported/messaging/test-service-thing", r)
	return d
}

func TestComponentForInfraAlwaysHoldsOverrides(t *testing.T) {
	typ := typeOf()
	d := ComponentFor("imported/messaging/empty", typ, nil, "test-schema", testRules)
	body := loadDrafted(t, d)
	if got := pillarKeys(t, body, "infra"); !reflect.DeepEqual(got, []string{"overrides"}) {
		t.Fatalf("infra %v, want [overrides]", got)
	}
}

func TestComponentForCommentsNameTheRule(t *testing.T) {
	d, placed := draftOf(t, "imported/messaging/test-service-thing", testRules)
	for _, n := range slices.Concat(d.Values, d.Infra, d.Provides) {
		if n.Name == "overrides" {
			continue
		}
		var pl Placed
		for _, p := range placed {
			if p.Key == n.Name && p.Pillar != PillarOverrides {
				pl = p
			}
		}
		if len(n.Comment) != 2 || n.Comment[0] != pl.Rule {
			t.Errorf("%s comment %q, want the rule %q and a description line", n.Name, n.Comment, pl.Rule)
			continue
		}
		if pl.Property.Description != "" {
			if n.Comment[1] != Marked(pl.Property.Description) || n.Description != Marked(pl.Property.Description) {
				t.Errorf("%s comment %q, description %q, want the vendor description marked", n.Name, n.Comment, n.Description)
			}
		} else if !strings.Contains(n.Comment[1], "no description") || strings.Contains(n.Comment[1], VendorTextMarker) {
			t.Errorf("%s comment %q, want a composed line saying the vendor gives no description", n.Name, n.Comment)
		}
	}
}

func TestComponentForSecretSlots(t *testing.T) {
	slots := testRules
	slots.SecretSlots = true
	d := mustDraft(t, slots)
	if len(d.Slots) != 1 {
		t.Fatalf("slots %+v, want one", d.Slots)
	}
	s := d.Slots[0]
	if s.Name != "master_user_password" || s.Selector != "feature.aggrega.ai/kind=secret" || !s.Sensitive ||
		!s.Required || !reflect.DeepEqual(s.Inject, []string{"env"}) {
		t.Fatalf("slot %+v, want a required sensitive secret slot delivered on env", s)
	}
	body := loadDrafted(t, d)
	if got := keysOf(at(body, "spec", "featuresSchema")); !reflect.DeepEqual(got, []string{"master_user_password"}) {
		t.Fatalf("featuresSchema %v, want [master_user_password]", got)
	}

	if d := mustDraft(t, testRules); len(d.Slots) != 0 {
		t.Fatalf("slots %+v drafted for a format taking secrets as deploy-time parameters", d.Slots)
	}
}

// vendorFixtures are the vendor descriptions of the marker test, each written
// for the test and each carrying a token no composed text holds, some holding
// text a YAML or template reader could misread.
var vendorFixtures = []string{
	"vendor-text-01 describes the type.",
	"vendor-text-02 uses {{ .Values }} braces.",
	"vendor-text-03 # holds a hash.",
	"[vendor-text-04] opens with a bracket.",
	"vendor-text-05 ---",
	"vendor-text-06 key: value",
	"vendor-text-07 carriage\r\nreturn",
	"vendor-text-08 ends with a backslash\\",
	"vendor-text-09 describes a nested member.",
	"vendor-text-10 describes the secret.",
	"vendor-text-11 describes the read-back value.",
}

// fixtureToken returns the token of the fixture text holds, or "".
func fixtureToken(text string) string {
	for _, f := range vendorFixtures {
		token := strings.Trim(strings.Fields(f)[0], "[]")
		if strings.Contains(text, token) {
			return token
		}
	}
	return ""
}

// TestVendorTextMarkerOpensEveryVendorText walks every description and every
// comment line of a drafted Component, read back as YAML: each text holding a
// vendor description opens with the marker, and no composed text holds it.
func TestVendorTextMarkerOpensEveryVendorText(t *testing.T) {
	if Marked("") != "" {
		t.Fatalf("Marked(\"\") = %q, want empty", Marked(""))
	}
	desc := func(p *Property, i int) *Property { p.Description = vendorFixtures[i]; return p }
	settings := desc(prop("Settings", KindObject), 2)
	settings.Required = true
	settings.Properties = []*Property{desc(member(settings.Path, "Mode", KindString), 8)}
	name := desc(prop("QueueName", KindString), 1)
	location := desc(prop("location", KindString), 3)
	class := desc(prop("InstanceClass", KindString), 4)
	class.Required = true
	size := desc(prop("Size", KindInteger), 5)
	size.Required = true
	tier := desc(prop("Tier", KindString), 6)
	tier.Required = true
	port := desc(prop("Port", KindInteger), 7)
	port.Required = true
	bare := prop("Bare", KindString)
	bare.Required = true
	password := desc(prop("MasterUserPassword", KindString), 9)
	arn := desc(prop("Arn", KindString), 10)
	arn.ReadOnly = true
	typ := typeOf(arn, bare, class, password, port, name, settings, size, tier, location)
	typ.Description = vendorFixtures[0]
	slots := testRules
	slots.SecretSlots = true
	placed, _ := Split(typ, slots)
	body := loadDrafted(t, ComponentFor("imported/messaging/test-service-thing", typ, placed, "test-schema", slots))

	seen := map[string]bool{}
	check := func(where, text string) {
		token := fixtureToken(text)
		switch {
		case token != "" && !strings.HasPrefix(text, VendorTextMarker+" "):
			t.Errorf("%s %q holds vendor text and does not open with the marker", where, text)
		case token == "" && strings.Contains(text, VendorTextMarker):
			t.Errorf("%s %q is composed and holds the marker", where, text)
		}
		if token != "" {
			seen[where+" "+token] = true
		}
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		for _, comment := range []string{n.HeadComment, n.LineComment, n.FootComment} {
			for line := range strings.SplitSeq(comment, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					check("comment", strings.TrimPrefix(line, "# "))
				}
			}
		}
		for i := 0; n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "description" {
				for paragraph := range strings.SplitSeq(n.Content[i+1].Value, "\n\n") {
					check("description", paragraph)
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(body)

	for i, f := range vendorFixtures {
		token := strings.Trim(strings.Fields(f)[0], "[]")
		if !seen["description "+token] {
			t.Errorf("no description carries the vendor text %q", f)
		}
		// The type's description, a nested member's and a slot's are written as
		// descriptions alone; every other is quoted in its key's comment too.
		if i != 0 && i != 8 && i != 9 && !seen["comment "+token] {
			t.Errorf("no comment quotes the vendor text %q", f)
		}
	}
}
