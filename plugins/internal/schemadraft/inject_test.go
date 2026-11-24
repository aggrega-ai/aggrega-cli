// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
)

// TestInjectedPropertyKeyIsTheSnakeCaseOfItsPath spells the key of a property a
// neighbour supplies as the snake_case of its whole path, an array item's
// index a word of its own.
func TestInjectedPropertyKeyIsTheSnakeCaseOfItsPath(t *testing.T) {
	for _, c := range []struct {
		path []string
		want string
	}{
		{[]string{"properties", "configuration", "registries", "0", "server"}, appInject},
		{[]string{"properties", "targetResourceId"}, "properties_target_resource_id"},
		{[]string{"properties", "ipConfigurations", "12", "properties", "subnet", "id"},
			"properties_ip_configurations_12_properties_subnet_id"},
		{[]string{"KMSKeyId"}, "kms_key_id"},
	} {
		if got := InjectKey(c.path); got != c.want {
			t.Errorf("InjectKey(%v) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestInjectedPropertyIsReadOnThePropertyMechanism reads an injected property
// off .context.inject.property under its key, required, so a Recipe rendered
// without it halts naming the key.
func TestInjectedPropertyIsReadOnThePropertyMechanism(t *testing.T) {
	_, inputs := FromWritten(appWritten())
	var in *Input
	for i := range inputs {
		if inputs[i].Source.Pillar == PillarInject {
			in = &inputs[i]
		}
	}
	if in == nil {
		t.Fatal("no input is read off the inject pipe")
	}
	if in.Source.Mechanism != MechanismProperty || MechanismProperty != "property" || in.Source.Key != appInject ||
		!in.Required {
		t.Fatalf("the injected input is %+v", *in)
	}
	run := func(ctx map[string]any) (string, error) {
		tmpl := template.Must(template.New("t").Option("missingkey=error").Funcs(gotmpl.FuncMap()).
			Parse("{{ " + in.Source.Template() + " }}"))
		var b strings.Builder
		err := tmpl.Execute(&b, ctx)
		return b.String(), err
	}
	reference := "[reference('Registry').loginServer]"
	got, err := run(map[string]any{"context": map[string]any{"inject": map[string]any{
		"property": map[string]any{appInject: reference}}}})
	if err != nil || got != reference {
		t.Errorf("the input reads %q, %v, want %q", got, err, reference)
	}
	if _, err := run(map[string]any{"context": map[string]any{"inject": map[string]any{"property": map[string]any{}}}}); err == nil || !strings.Contains(err.Error(), appInject) {
		t.Errorf("an absent key reads %v, want a halt naming %s", err, appInject)
	}
}
