// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// appInject is the key a neighbour supplies the registry server of appWritten
// under.
const appInject = "properties_configuration_registries_0_server"

// appWritten is an app as a template writes it: its name and location, tags,
// a few settings of each kind under properties, a configuration object less
// the registry server a neighbour supplies, that server, a secret fed by a
// secure parameter, the provisioning state an export writes, and the keys its
// neighbours read.
func appWritten() WrittenResource {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	return WrittenResource{
		ID: "app", Type: "Microsoft.Example/apps", APIVersion: "2025-01-01",
		Properties: []WrittenProperty{
			{Path: []string{"name"}, Value: raw(`"orders-app"`), Identity: true},
			{Path: []string{"location"}, Value: raw(`"westeurope"`), Placement: true},
			{Path: []string{"tags"}, Value: raw(`{"team": "orders"}`)},
			{Path: []string{"properties", "provisioningState"}, Value: raw(`"Succeeded"`)},
			{Path: []string{"properties", "tier"}, Value: raw(`"Standard"`)},
			{Path: []string{"properties", "replicas"}, Value: raw(`2`)},
			{Path: []string{"properties", "weight"}, Value: raw(`0.5`)},
			{Path: []string{"properties", "enabled"}, Value: raw(`true`)},
			{Path: []string{"properties", "zones"}, Value: raw(`["1", "2"]`)},
			{Path: []string{"properties", "configuration"},
				Value: raw(`{"registries": [{"identity": "system"}], "ingress": {"external": true, "targetPort": 8080}}`)},
			{Path: []string{"properties", "configuration", "registries", "0", "server"}, Inject: appInject},
			{Path: []string{"properties", "adminPassword"}, Secret: true},
		},
		Provides: []Provide{
			{Key: "id", Attribute: []string{"id"}, Identifier: true},
			{Key: "name", Attribute: []string{"name"}},
			{Key: "fqdn", Attribute: []string{"properties", "configuration", "ingress", "fqdn"}},
		},
	}
}

// inputLine describes one input as a test reads it: where it is written, where
// it is read, its kinds and its flags.
func inputLine(in Input) string {
	line := strings.Join(in.Property.Path, ".") + " <- " + in.Source.readFromText() + " " + kindNames(in.Property.Kinds)
	if in.Required {
		line += " required"
	}
	if in.Secret {
		line += " secret"
	}
	return line
}

// readFromText names an input's source for a test, the mechanism included.
func (s InputSource) readFromText() string {
	switch s.Pillar {
	case PillarValues:
		return "values." + s.Key
	case PillarInfra:
		return "infra." + s.Key
	case PillarOverrides:
		return "infra.overrides." + s.Key
	case PillarSecret:
		return "secret." + s.Key
	case PillarInject:
		return "context.inject." + s.Mechanism + "." + s.Key
	}
	return "?" + s.Key
}

// TestFromWrittenPlacesEachRule reads each written property by its rule:
// identity into values and placement into typed infra under their snake_case
// keys, a secret as a secret, an injected property off the property mechanism
// under its key, and every other under infra.overrides in the vendor's
// spelling, each typed from the value written.
func TestFromWrittenPlacesEachRule(t *testing.T) {
	typ, inputs := FromWritten(appWritten())
	if typ.Name != "Microsoft.Example/apps" || typ.APIVersion != "2025-01-01" {
		t.Errorf("type %s@%s", typ.Name, typ.APIVersion)
	}
	var got []string
	for _, in := range inputs {
		got = append(got, inputLine(in))
	}
	want := []string{
		"name <- values.name string required",
		"location <- infra.location string required",
		"tags <- infra.overrides.tags object",
		"properties.tier <- infra.overrides.tier string",
		"properties.replicas <- infra.overrides.replicas integer",
		"properties.weight <- infra.overrides.weight number",
		"properties.enabled <- infra.overrides.enabled boolean",
		"properties.zones <- infra.overrides.zones array",
		"properties.configuration <- infra.overrides.configuration object",
		"properties.adminPassword <- secret.admin_password string required secret",
		"properties.configuration.registries.0.server <- context.inject.property." + appInject + " string required",
	}
	if !slices.Equal(got, want) {
		t.Errorf("inputs\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Each property is named by the last segment of its path, which a writer
	// sets it under, and marked as one a source writes; an object written is
	// passed whole.
	for _, in := range inputs {
		if in.Property.Name != in.Property.Path[len(in.Property.Path)-1] || !in.Property.Written {
			t.Errorf("%v is named %s, written %v", in.Property.Path, in.Property.Name, in.Property.Written)
		}
		if kindAlone(in.Property, KindObject) && !in.Property.Open {
			t.Errorf("the object %v is closed", in.Property.Path)
		}
	}

	// The type holds every written property it carries and, read back, each
	// attribute the neighbours read that the source does not write; the
	// identifier provided is its primary identifier.
	var paths []string
	for _, p := range typ.Properties {
		path := strings.Join(p.Path, ".")
		if p.ReadOnly {
			path += " (read back)"
		}
		paths = append(paths, path)
	}
	wantPaths := []string{"name", "location", "tags", "properties.tier", "properties.replicas", "properties.weight",
		"properties.enabled", "properties.zones", "properties.configuration",
		"properties.configuration.registries.0.server", "properties.adminPassword", "id (read back)",
		"properties.configuration.ingress.fqdn (read back)"}
	if !slices.Equal(paths, wantPaths) {
		t.Errorf("properties %v, want %v", paths, wantPaths)
	}
	if !reflect.DeepEqual(typ.PrimaryIdentifier, [][]string{{"id"}}) {
		t.Errorf("primary identifier %v", typ.PrimaryIdentifier)
	}
}

// TestFromWrittenDropsProvisioningStateOnly leaves out the provisioning state
// an export writes under properties, in any case, and carries every other
// written property, one of that name elsewhere included.
func TestFromWrittenDropsProvisioningStateOnly(t *testing.T) {
	w := appWritten()
	w.Properties = append(w.Properties,
		WrittenProperty{Path: []string{"Properties", "ProvisioningState"}, Value: json.RawMessage(`"Succeeded"`)},
		WrittenProperty{Path: []string{"provisioningState"}, Value: json.RawMessage(`"kept"`)},
		WrittenProperty{Path: []string{"properties", "status", "provisioningState"}, Value: json.RawMessage(`"kept"`)},
		WrittenProperty{Path: []string{"properties", "provisioningStates"}, Value: json.RawMessage(`"kept"`)})
	typ, inputs := FromWritten(w)
	read := map[string]bool{}
	for _, in := range inputs {
		read[strings.Join(in.Property.Path, ".")] = true
	}
	for _, p := range typ.Properties {
		if strings.EqualFold(strings.Join(p.Path, "."), "properties.provisioningState") {
			t.Errorf("the type carries %v", p.Path)
		}
	}
	for _, wp := range w.Properties {
		path := strings.Join(wp.Path, ".")
		dropped := strings.EqualFold(path, "properties.provisioningState")
		if read[path] == dropped {
			t.Errorf("%s is read: %v, want %v", path, read[path], !dropped)
		}
	}
}

// TestFromWrittenNeverPanicsOnMalformedInput reads a resource whose properties
// and provides are malformed: a property with no path is left out, a value that
// is not JSON is of any kind, and a provide naming no attribute reads its key.
func TestFromWrittenNeverPanicsOnMalformedInput(t *testing.T) {
	cases := []WrittenResource{
		{},
		{Properties: []WrittenProperty{{}}},
		{Properties: []WrittenProperty{{Path: []string{}, Inject: "x"}}},
		{Properties: []WrittenProperty{{Path: []string{"a"}, Value: json.RawMessage(`{not json`)}}},
		{Provides: []Provide{{Key: "id", Identifier: true}}},
		{Provides: []Provide{{}}},
	}
	for i, w := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("case %d panics: %v", i, r)
				}
			}()
			typ, inputs := FromWritten(w)
			for _, in := range inputs {
				if len(in.Property.Path) == 0 {
					t.Errorf("case %d reads an input at no path: %+v", i, in)
				}
			}
			if i == 3 && (len(inputs) != 1 || !kindAlone(inputs[0].Property, KindAny)) {
				t.Errorf("case %d reads %+v, want one input of any kind", i, inputs)
			}
			if i == 4 && (len(typ.Properties) != 1 || !slices.Equal(typ.Properties[0].Path, []string{"id"})) {
				t.Errorf("case %d reads the attribute %+v, want its key", i, typ.Properties)
			}
		}()
	}
}
