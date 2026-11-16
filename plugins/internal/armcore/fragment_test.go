// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

func TestReadFragmentScopeFromSchema(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content string
		want          Scope
	}{
		{"absent", "resources: {}\n", ScopeResourceGroup},
		{"resource group", `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"}`, ScopeResourceGroup},
		{"resource group other folder", `{"$schema": "https://schema.management.azure.com/schemas/2015-01-01/deploymentTemplate.json#"}`, ScopeResourceGroup},
		{"subscription", `{"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#"}`, ScopeSubscription},
		{"subscription other folder", `{"$schema": "https://schema.management.azure.com/schemas/2019-04-01/subscriptionDeploymentTemplate.json#"}`, ScopeSubscription},
		{"management group", `{"$schema": "https://schema.management.azure.com/schemas/2019-08-01/managementGroupDeploymentTemplate.json#"}`, ScopeManagementGroup},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			frag, _, err := ReadFragment([]byte(c.content))
			if err != nil {
				t.Fatalf("ReadFragment: %v", err)
			}
			if frag.Scope != c.want {
				t.Errorf("Scope = %q, want %q", frag.Scope, c.want)
			}
		})
	}
	_, _, err := ReadFragment([]byte(`{"$schema": "https://schema.management.azure.com/schemas/2019-08-01/tenantDeploymentTemplate.json#"}`))
	if !errors.Is(err, plugincondition.ErrPluginCannotExpressInput()) {
		t.Errorf("tenant schema: want E0288, got %v", err)
	}
}

func TestReadFragmentRefuses(t *testing.T) {
	t.Parallel()
	cannot := plugincondition.ErrPluginCannotExpressInput()
	notRep := plugincondition.ErrPluginValueNotRepresentable()
	cases := []struct {
		name, content string
		cond          error
		sentence      []string
	}{
		{"resources a list", "resources:\n  - type: Microsoft.Storage/storageAccounts\n", cannot, []string{"arm-provider:", "key resources by symbolic name"}},
		{"language version 1.0", "languageVersion: \"1.0\"\n", cannot, []string{"arm-provider:", "languageVersion"}},
		{"experimental language version", "languageVersion: \"2.0-experimental\"\n", cannot, []string{"arm-provider:", "languageVersion"}},
		{"apiProfile", "apiProfile: 2019-03-01-hybrid\n", cannot, []string{"arm-provider:", "apiProfile"}},
		{"symbol with a hyphen", "resources:\n  my-db:\n    type: x\n", notRep, []string{"arm-provider:", "/resources/my-db"}},
		{"unfinished expression", "variables:\n  v: \"[concat('a', ]\"\n", notRep, []string{"arm-provider:", "/variables/v", "[["}},
		{"bracketed word", "variables:\n  v: \"[prod]\"\n", notRep, []string{"arm-provider:", "/variables/v", "[["}},
		{"value inside an expression", "variables:\n  v: \"[concat('x', [reference('Db').outputs.h.value])]\"\n", notRep,
			[]string{"arm-provider:", "/variables/v", "inside an expression", "literal text"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ReadFragment([]byte(c.content))
			if !errors.Is(err, c.cond) {
				t.Fatalf("want %v, got %v", c.cond, err)
			}
			for _, s := range c.sentence {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("sentence lacks %q: %v", s, err)
				}
			}
		})
	}
}

func TestReadFragmentCarriesSections(t *testing.T) {
	t.Parallel()
	frag, diags, err := ReadFragment([]byte(`
resources:
  db:
    type: Microsoft.Storage/storageAccounts
    properties:
      name: "[[literal]"
      note: array[0]
parameters:
  location: {type: string}
functions:
  - namespace: contoso
    members:
      one: {output: {type: string, value: x}}
`))
	if err != nil || len(diags) != 0 {
		t.Fatalf("err %v diags %v", err, diags)
	}
	if _, ok := frag.Sections["resources"]["db"]; !ok {
		t.Error("resource db not carried")
	}
	if _, ok := frag.Sections["parameters"]["location"]; !ok {
		t.Error("parameter location not carried")
	}
	if _, ok := frag.Sections["functions"]["contoso.one"]; !ok {
		t.Errorf("function not carried by namespace.member: %v", frag.Sections["functions"])
	}
	if _, ok := frag.Sections["outputs"]; ok {
		t.Error("an unwritten section is present")
	}
}

func TestReadFragmentWarns(t *testing.T) {
	t.Parallel()
	frag, diags, err := ReadFragment([]byte("contentVersion: 1.0.0.0\nmystery: 1\nresources: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"contentVersion", "mystery"}
	if len(diags) != len(want) {
		t.Fatalf("got %d diagnostics, want %d: %v", len(diags), len(want), diags)
	}
	for i, k := range want {
		if diags[i] != rendercore.UnsupportedFieldDiagnostic("arm", k) {
			t.Errorf("diagnostic %d = %v", i, diags[i])
		}
		if _, carried := frag.Sections[k]; carried {
			t.Errorf("%s was carried", k)
		}
	}
}

func TestReadFragmentRefusesARepeatedKey(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, content, key, line string }{
		{"yaml top level", "resources: {}\nresources: {}\n", "resources", "line 2"},
		{"yaml nested", "resources:\n  a:\n    type: x\n    type: y\n", "type", "line 4"},
		{"json top level", "{\n\"resources\": {},\n\"resources\": {}\n}", "resources", "line 3"},
		{"json nested", "{\n\"resources\": {\n\"a\": 1,\n\"a\": 2\n}\n}", "a", "line 4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ReadFragment([]byte(c.content))
			if err == nil {
				t.Fatal("a repeated key was read")
			}
			for _, s := range []string{"arm-provider: parse fragment:", `"` + c.key + `"`, c.line} {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error lacks %q: %v", s, err)
				}
			}
		})
	}
}

func TestReadFragmentRefusesAnAliasCycle(t *testing.T) {
	t.Parallel()
	_, _, err := ReadFragment([]byte("resources: &loop\n  nested: *loop\n"))
	if err == nil {
		t.Fatal("a self-referencing anchor was read")
	}
	for _, s := range []string{"arm-provider: parse fragment:", "&loop", "*loop"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error lacks %q: %v", s, err)
		}
	}
}
