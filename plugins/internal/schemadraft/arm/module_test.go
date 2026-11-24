// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const (
	moduleURL     = "oci://mcr.microsoft.com/bicep/avm/res/app/container-app"
	moduleVersion = "0.23.1"
)

// wrappedApp is the container app module as a template sets it: a required
// environment, a tag map, the registry a neighbour supplies, and the FQDN a
// neighbour reads.
func wrappedApp(version string) schemadraft.WrappedModule {
	prop := func(name string, kind schemadraft.Kind) *schemadraft.Property {
		return &schemadraft.Property{Name: name, Path: []string{name}, Kinds: []schemadraft.Kind{kind}, Written: true}
	}
	override := func(name string, kind schemadraft.Kind, required bool) schemadraft.Input {
		return schemadraft.Input{Property: prop(name, kind),
			Source:   schemadraft.InputSource{Pillar: schemadraft.PillarOverrides, Key: name},
			Required: required}
	}
	return schemadraft.WrappedModule{
		Type:       &schemadraft.Type{Name: containerApps},
		SourceKind: "bicep-module-oci",
		URL:        moduleURL,
		Version:    version,
		Inputs: []schemadraft.Input{
			override("environmentResourceId", schemadraft.KindString, true),
			override("tags", schemadraft.KindObject, false),
			{Property: prop("registryServer", schemadraft.KindString),
				Source: schemadraft.InputSource{Pillar: schemadraft.PillarInject, Key: registryInject,
					Mechanism: schemadraft.MechanismProperty},
				Required: true},
		},
		Fixed:   []importer.Entry{{Key: "enableTelemetry", Value: "false"}},
		Outputs: []schemadraft.Provide{{Key: "fqdn", Attribute: []string{"fqdn"}}},
	}
}

// TestWriteWrappedGolden writes the Recipe parts that source a published
// module: the source with its kind, url and version, each input bound by its
// parameter name and enableTelemetry bound false, the allowlist admitting
// exactly the drafted overrides, and the output published as a reference
// expression.
func TestWriteWrappedGolden(t *testing.T) {
	m := wrappedApp(moduleVersion)
	drafted, diags := schemadraft.DraftWrapped(templateInput(), Format, m, WriteWrapped)
	noHalt(t, diags)
	if len(drafted.Entities) != 2 {
		t.Fatalf("drafted %+v, want a Component and a Recipe", drafted.Entities)
	}
	recipe := drafted.Entities[1].Entity.Body
	golden(t, "wrapped.recipe.yaml", recipe)

	for _, c := range []struct {
		path []string
		want string
	}{
		{[]string{"spec", "capability"}, "arm"},
		{[]string{"spec", "source", "kind"}, "bicep-module-oci"},
		{[]string{"spec", "source", "url"}, moduleURL},
		{[]string{"spec", "source", "version"}, moduleVersion},
		{[]string{"spec", "provides", "fqdn"}, "[reference('{{ .locals.symbol }}').outputs.fqdn.value]"},
	} {
		if got := mappingAt(t, recipe, c.path...).Value; got != c.want {
			t.Errorf("%s = %q, want %q", strings.Join(c.path, "."), got, c.want)
		}
	}
	assertion := mappingAt(t, recipe, "spec", "assertions", "overrides_this_recipe_reads").Value
	if !strings.Contains(assertion, `(list "environmentResourceId" "tags")`) {
		t.Errorf("the allowlist does not admit exactly the drafted overrides:\n%s", assertion)
	}

	// The mapping renders: the required input and the one present are bound,
	// the telemetry is off, and an absent optional input is not written.
	mapping := mappingAt(t, recipe, "spec", "valuesMapping").Value
	ctx := appContext(map[string]any{"environmentResourceId": "/subscriptions/s/env", "tags": map[string]any{"a": "b"}})
	out, err := render(mapping, ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`environmentResourceId: "/subscriptions/s/env"`, `tags: {"a":"b"}`,
		"registryServer: ", "enableTelemetry: false"} {
		if !strings.Contains(out, want) {
			t.Errorf("the mapping renders\n%s\nwithout %s", out, want)
		}
	}
	out, err = render(mapping, appContext(map[string]any{"environmentResourceId": "/e"}))
	if err != nil || strings.Contains(out, "tags") {
		t.Errorf("an absent tags renders %q, %v", out, err)
	}
}

// TestWriteWrappedTelemetryIsNotAnOverride keeps enableTelemetry out of the
// overrides the Release may set, even where the source sets it.
func TestWriteWrappedTelemetryIsNotAnOverride(t *testing.T) {
	m := wrappedApp(moduleVersion)
	m.Inputs = append(m.Inputs, schemadraft.Input{
		Property: &schemadraft.Property{Name: "enableTelemetry", Path: []string{"enableTelemetry"},
			Kinds: []schemadraft.Kind{schemadraft.KindBoolean}, Written: true},
		Source: schemadraft.InputSource{Pillar: schemadraft.PillarOverrides, Key: "enableTelemetry"}})
	drafted, diags := schemadraft.DraftWrapped(templateInput(), Format, m, WriteWrapped)
	noHalt(t, diags)
	recipe := drafted.Entities[1].Entity.Body
	assertion := mappingAt(t, recipe, "spec", "assertions", "overrides_this_recipe_reads").Value
	mapping := mappingAt(t, recipe, "spec", "valuesMapping").Value
	if !strings.Contains(assertion, `(list "environmentResourceId" "tags")`) {
		t.Errorf("the allowlist admits enableTelemetry:\n%s", assertion)
	}
	if strings.Count(mapping, "enableTelemetry") != 1 || !strings.Contains(mapping, "enableTelemetry: false") {
		t.Errorf("the mapping does not bind enableTelemetry once, to false:\n%s", mapping)
	}
}
