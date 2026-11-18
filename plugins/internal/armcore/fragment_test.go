// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
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
	for _, want := range []string{"tenant scope", AnnotationDeploymentScope, "resource-group, subscription or management-group scope"} {
		if err != nil && !strings.Contains(err.Error(), want) {
			t.Errorf("tenant schema: the halt does not say %q: %v", want, err)
		}
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
		{"bracketed word in a nested deployment's parameters", nestedDeploymentFragment("properties:\n      parameters:\n        tier:\n          value: \"[prod]\"\n"), notRep,
			[]string{"arm-provider:", "/resources/Store/properties/parameters/tier/value", "[["}},
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

// nestedDeploymentFragment is a fragment holding one nested deployment, Store,
// with properties written as given, indented under the resource.
func nestedDeploymentFragment(properties string) string {
	return "resources:\n  Store:\n    type: Microsoft.Resources/deployments\n    apiVersion: '2025-04-01'\n    name: store\n    " + properties
}

// TestReadFragmentCarriesANestedTemplateAsWritten holds that the template a
// nested deployment carries, such as a sourced template the provider wrapped, is
// not read for expressions: it is a template of its own, evaluated in its own
// scope, and reaches the assembled template byte for byte.
func TestReadFragmentCarriesANestedTemplateAsWritten(t *testing.T) {
	t.Parallel()
	content := nestedDeploymentFragment("properties:\n      mode: Incremental\n      template:\n        variables:\n          label: \"[prod]\"\n")
	frag, _, err := ReadFragment([]byte(content))
	if err != nil {
		t.Fatalf("ReadFragment: %v", err)
	}
	store, _ := frag.Sections[sectionResources]["Store"].(map[string]any)
	properties, _ := store["properties"].(map[string]any)
	template, _ := properties["template"].(map[string]any)
	variables, _ := template["variables"].(map[string]any)
	if variables["label"] != "[prod]" {
		t.Errorf("label = %v, want the text as written", variables["label"])
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

func TestReadFragmentReadsLanguageVersion2(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"json string":         `{"languageVersion": "2.0"}`,
		"yaml quoted":         "languageVersion: \"2.0\"\n",
		"yaml unquoted float": "languageVersion: 2.0\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			frag, diags, err := ReadFragment([]byte(content))
			if err != nil || len(diags) != 0 {
				t.Fatalf("err %v diags %v", err, diags)
			}
			if _, carried := frag.Sections["languageVersion"]; carried {
				t.Error("languageVersion was carried")
			}
		})
	}
}

func TestReadFragmentTakesTheFloatSpelling(t *testing.T) {
	t.Parallel()
	var om types.OrderedMap[any]
	if err := parse.TemplateOutput([]byte("languageVersion: 2.0\n"), &om); err != nil {
		t.Fatal(err)
	}
	v, _ := om.Get("languageVersion")
	if _, isFloat := v.(float64); !isFloat {
		t.Fatalf("an unquoted 2.0 decodes to %T, not float64", v)
	}
	if !isLanguageVersion(v) {
		t.Error("the decoded unquoted 2.0 is not read as the stamped version")
	}
	if isLanguageVersion(2.1) || !isLanguageVersion(float64(2)) {
		t.Error("only 2.0 is the stamped version")
	}
}

func TestReadFragmentKeepsBracketedLiteralsUnchanged(t *testing.T) {
	t.Parallel()
	literals := []string{
		"pattern is [a-z] only",
		"[test] value",
		"run [ -f x ] first",
		"element array[0] of it",
		"x [a-z]+ then [0-9]",
	}
	for _, lit := range literals {
		t.Run(lit, func(t *testing.T) {
			t.Parallel()
			content := "variables:\n  v:\n    text: " + `"` + lit + `"` + "\n"
			frag, _, err := ReadFragment([]byte(content))
			if err != nil {
				t.Fatalf("ReadFragment: %v", err)
			}
			got := frag.Sections["variables"]["v"].(map[string]any)["text"]
			if got != lit {
				t.Errorf("literal changed: got %q want %q", got, lit)
			}
		})
	}
}

func TestReadFragmentRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	cannot := plugincondition.ErrPluginCannotExpressInput()
	cases := []struct {
		name, content string
		sentence      []string
	}{
		{"non-string schema", "$schema: 3\n", []string{"arm-provider:", "$schema is not text"}},
		{"unrecognised schema file name", `{"$schema": "https://example.invalid/schemas/other.json#"}`, []string{"arm-provider:", "$schema does not name a template schema"}},
		{"parameters a list", "parameters:\n  - a\n", []string{"arm-provider:", "parameters is not an object"}},
		{"functions an object", "functions:\n  a: b\n", []string{"arm-provider:", "functions is not a list"}},
		{"functions entry without members", "functions:\n  - namespace: contoso\n", []string{"arm-provider:", "/functions/0"}},
		{"functions entry without namespace", "functions:\n  - members: {}\n", []string{"arm-provider:", "/functions/0"}},
		{"function declared twice", "functions:\n  - namespace: contoso\n    members:\n      one: {}\n  - namespace: contoso\n    members:\n      one: {}\n", []string{"arm-provider:", "/functions/1/members/one", "more than once"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ReadFragment([]byte(c.content))
			if !errors.Is(err, cannot) {
				t.Fatalf("want E0288, got %v", err)
			}
			for _, s := range c.sentence {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("sentence lacks %q: %v", s, err)
				}
			}
		})
	}
}

// A parse fault that lands on a bracket which does not open an expression (a
// bad index) is an ordinary parse failure, not a value written inside an
// expression.
func TestReadFragmentBadIndexIsAnOrdinaryParseFault(t *testing.T) {
	t.Parallel()
	_, _, err := ReadFragment([]byte("variables:\n  v: \"[parameters('p')[[1]]]\"\n"))
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("want E0291, got %v", err)
	}
	msg := err.Error()
	for _, s := range []string{"/variables/v", "does not parse", "[["} {
		if !strings.Contains(msg, s) {
			t.Errorf("sentence lacks %q: %v", s, msg)
		}
	}
	if strings.Contains(msg, "inside an expression") {
		t.Errorf("a bad index is reported as a value inside an expression: %v", msg)
	}
}
