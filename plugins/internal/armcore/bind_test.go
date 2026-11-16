// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// template2 returns a 2.0 template declaring the given parameters (a JSON object
// body) and the given definitions.
func template2(parameters, definitions string) []byte {
	if definitions == "" {
		definitions = "{}"
	}
	return []byte(`{"$schema": "` + schemaRG + `", "languageVersion": "2.0", "contentVersion": "1.0.0.0",
		"definitions": ` + definitions + `, "parameters": ` + parameters + `, "resources": {}}`)
}

func mapping(pairs ...any) types.OrderedMap[any] {
	var m types.OrderedMap[any]
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// bound renders the template with the mapping and returns the parameters block.
func bound(t *testing.T, content []byte, values types.OrderedMap[any], sensitive bool) (map[string]any, error) {
	t.Helper()
	out, _, _, err := RenderSourced(SourcedNode{Identity: "net", Content: content, Values: values, ValuesMapped: true, SensitiveValues: sensitive})
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	w := doc["resources"].(map[string]any)["Net"].(map[string]any)
	return w["properties"].(map[string]any)["parameters"].(map[string]any), nil
}

func wantCondition(t *testing.T, err error, cond error, absent ...string) {
	t.Helper()
	if !errors.Is(err, cond) {
		t.Fatalf("want %v, got %v", cond, err)
	}
	if !strings.Contains(err.Error(), "arm-provider:") {
		t.Errorf("message %q does not open arm-provider:", err)
	}
	for _, a := range absent {
		if strings.Contains(err.Error(), a) {
			t.Errorf("message %q quotes the value %q", err, a)
		}
	}
}

const stringParam = `{"name": {"type": "string"}}`

func TestBindKeyNamingNoParameterHalts(t *testing.T) {
	t.Parallel()
	_, err := bound(t, template2(`{"name": {"type": "string"}, "size": {"type": "int", "defaultValue": 1}}`, ""), mapping("nmae", "x", "name", "y"), false)
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{`"nmae"`, `"name"`, `"size"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %s", err, want)
		}
	}
}

func TestBindExpressionPassesThrough(t *testing.T) {
	t.Parallel()
	p, err := bound(t, template2(stringParam, ""), mapping("name", "[reference('Net').outputs.subnetId.value]"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, p["name"]); got != `{"value":"[reference('Net').outputs.subnetId.value]"}` {
		t.Errorf("name = %s", got)
	}
	_, err = bound(t, template2(stringParam, ""), mapping("name", "[reference('Net']"), false)
	wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "reference('Net']")
	// Text whole in brackets that ARM would read as an expression but is not one.
	_, err = bound(t, template2(stringParam, ""), mapping("name", "[prod]"), false)
	wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "prod")
}

const kvID = "/subscriptions/0000/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/vlt"

func kvObject(withVersion bool) map[string]any {
	ref := map[string]any{"keyVault": map[string]any{"id": kvID}, "secretName": "db-password"}
	if withVersion {
		ref["secretVersion"] = "abc"
	}
	return map[string]any{"reference": ref}
}

func TestBindKeyVaultReference(t *testing.T) {
	t.Parallel()
	defs := `{"secretText": {"type": "securestring"}, "plain": {"type": "string"}}`
	for name, params := range map[string]string{
		"securestring":    `{"p": {"type": "securestring"}}`,
		"secureObject":    `{"p": {"type": "secureObject"}}`,
		"ref hop":         `{"p": {"$ref": "#/definitions/secretText"}}`,
		"mixed-case type": `{"p": {"type": "SecureString"}}`,
	} {
		for _, version := range []bool{false, true} {
			p, err := bound(t, template2(params, defs), mapping("p", kvObject(version)), false)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got, want := mustJSON(t, p["p"]), mustJSON(t, kvObject(version)); got != want {
				t.Errorf("%s: p = %s, want %s", name, got, want)
			}
		}
	}
	for name, params := range map[string]string{
		"string":     `{"p": {"type": "string"}}`,
		"object":     `{"p": {"type": "object"}}`,
		"plain hop":  `{"p": {"$ref": "#/definitions/plain"}}`,
		"unresolved": `{"p": {"$ref": "#/definitions/absent"}}`,
	} {
		_, err := bound(t, template2(params, defs), mapping("p", kvObject(false)), false)
		wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), kvID, "db-password")
		if !strings.Contains(err.Error(), `"p"`) {
			t.Errorf("%s: message %q does not name the parameter", name, err)
		}
	}
}

func TestBindLiteralConversion(t *testing.T) {
	t.Parallel()
	params := `{
		"s": {"type": "string"}, "n": {"type": "int"}, "b": {"type": "bool"},
		"a": {"type": "array"}, "o": {"type": "object"}, "r": {"$ref": "#/definitions/anything"}
	}`
	p, err := bound(t, template2(params, `{"anything": {"type": "string", "allowedValues": ["only"]}}`), mapping(
		"s", "text",
		"n", json.Number("7"),
		"b", true,
		"a", []any{"x", json.Number("2"), "[x]"},
		"o", map[string]any{"k": "[x]", "n": []any{"[y]"}},
		"r", "anything-at-all",
	), false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"s": `{"value":"text"}`,
		"n": `{"value":7}`,
		"b": `{"value":true}`,
		"a": `{"value":["x",2,"[[x]"]}`,
		"o": `{"value":{"k":"[[x]","n":["[[y]"]}}`,
		"r": `{"value":"anything-at-all"}`,
	}
	for k, w := range want {
		if got := mustJSON(t, p[k]); got != w {
			t.Errorf("%s = %s, want %s", k, got, w)
		}
	}

	t.Run("scalars are read as the declared type", func(t *testing.T) {
		t.Parallel()
		p, err := bound(t, template2(`{"s": {"type": "string"}, "n": {"type": "int"}, "b": {"type": "bool"}}`, ""),
			mapping("s", json.Number("12"), "n", "42", "b", "TRUE"), false)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustJSON(t, p); got != `{"b":{"value":true},"n":{"value":42},"s":{"value":"12"}}` {
			t.Errorf("parameters = %s", got)
		}
	})

	t.Run("bracketed escape stays as written", func(t *testing.T) {
		t.Parallel()
		p, err := bound(t, template2(stringParam, ""), mapping("name", "[[x]"), false)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustJSON(t, p["name"]); got != `{"value":"[[x]"}` {
			t.Errorf("name = %s", got)
		}
	})

	refusals := []struct {
		name, param string
		value       any
		constraint  string
		secret      string
	}{
		{"string given a list", `{"type": "string"}`, []any{"zq9"}, "type", "zq9"},
		{"int given text", `{"type": "int"}`, "zq9", "type", "zq9"},
		{"bool given text", `{"type": "bool"}`, "zq9", "type", "zq9"},
		{"array given text", `{"type": "array"}`, "zq9", "type", "zq9"},
		{"object given text", `{"type": "object"}`, "zq9", "type", "zq9"},
		{"allowedValues", `{"type": "string", "allowedValues": ["aa", "bb"]}`, "zq9", "allowedValues", "zq9"},
		{"allowedValues int", `{"type": "int", "allowedValues": [1, 2]}`, json.Number("987"), "allowedValues", "987"},
		{"minLength", `{"type": "string", "minLength": 5}`, "zq9", "minLength", "zq9"},
		{"maxLength", `{"type": "string", "maxLength": 2}`, "zq9", "maxLength", "zq9"},
		{"maxLength array", `{"type": "array", "maxLength": 1}`, []any{"zq9", "zq8"}, "maxLength", "zq9"},
		{"minValue", `{"type": "int", "minValue": 10}`, json.Number("3"), "minValue", "3"},
		{"maxValue", `{"type": "int", "maxValue": 10}`, json.Number("987"), "maxValue", "987"},
	}
	for _, r := range refusals {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			_, err := bound(t, template2(`{"p": `+r.param+`}`, ""), mapping("p", r.value), false)
			wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), r.secret)
			for _, want := range []string{`"p"`, r.constraint} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q lacks %s", err, want)
				}
			}
		})
	}
}

func TestBindLiteralIntoSecureParameterHalts(t *testing.T) {
	t.Parallel()
	for name, params := range map[string]string{
		"securestring": `{"p": {"type": "securestring"}}`,
		"secureObject": `{"p": {"type": "secureObject"}}`,
		"ref hop":      `{"p": {"$ref": "#/definitions/s"}}`,
	} {
		_, err := bound(t, template2(params, `{"s": {"type": "securestring"}}`), mapping("p", "zq9"), false)
		wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "zq9")
		if !strings.Contains(err.Error(), `"p"`) {
			t.Errorf("%s: message %q does not name the parameter", name, err)
		}
	}
	// An expression is evaluated at deploy and never written as text.
	if _, err := bound(t, template2(`{"p": {"type": "securestring"}}`, ""), mapping("p", "[parameters('pw')]"), false); err != nil {
		t.Errorf("expression into a secure parameter: %v", err)
	}
}

func TestBindSensitiveMappingRefusesEveryLiteral(t *testing.T) {
	t.Parallel()
	params := `{"a": {"type": "string"}, "b": {"type": "int"}, "c": {"type": "string"}, "d": {"type": "securestring"}, "e": {"type": "string"}}`
	_, err := bound(t, template2(params, ""), mapping(
		"a", "zq9", "b", json.Number("987"), "c", "[parameters('c')]", "d", kvObject(false), "e", "zq8"), true)
	wantCondition(t, err, plugincondition.ErrPluginValueNotRepresentable(), "zq9", "987", "zq8", kvID)
	for _, want := range []string{`"a"`, `"b"`, `"e"`, "expression", "Key Vault reference"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %s", err, want)
		}
	}
	for _, not := range []string{`"c"`, `"d"`} {
		if strings.Contains(err.Error(), not) {
			t.Errorf("message %q names %s, which a literal does not bind", err, not)
		}
	}
	// Without a literal the mapping passes.
	if _, err := bound(t, template2(params, ""), mapping("a", "[parameters('a')]", "b", "[parameters('b')]", "c", "[parameters('c')]", "d", kvObject(false), "e", "[parameters('e')]"), true); err != nil {
		t.Errorf("expressions and Key Vault objects under a sensitive mapping: %v", err)
	}
}

func TestBindRequiredParameterUnboundHalts(t *testing.T) {
	t.Parallel()
	params := `{"need": {"type": "string"}, "dflt": {"type": "string", "defaultValue": "x"}, "nul": {"type": "string", "nullable": true}, "bound": {"type": "string"}}`
	_, err := bound(t, template2(params, ""), mapping("bound", "v"), false)
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved())
	for _, want := range []string{`"need"`, "[parameters('need')]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %s", err, want)
		}
	}
	for _, not := range []string{`"dflt"`, `"nul"`, `"bound"`} {
		if strings.Contains(err.Error(), not) {
			t.Errorf("message %q names %s", err, not)
		}
	}
	// An empty mapping leaves a required parameter unbound too.
	_, err = bound(t, template2(stringParam, ""), mapping(), false)
	wantCondition(t, err, plugincondition.ErrPluginRequiredInputUnresolved())
}

func TestBindMatchesParameterNamesCaseInsensitively(t *testing.T) {
	t.Parallel()
	params := `{"location": {"type": "string", "defaultValue": "x"}}`
	p, err := bound(t, template2(params, ""), mapping("Location", "westeurope"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, p); got != `{"location":{"value":"westeurope"}}` {
		t.Errorf("parameters = %s", got)
	}
	_, err = bound(t, template2(params, ""), mapping("location", "a", "Location", "b"), false)
	wantCondition(t, err, plugincondition.ErrPluginOutputNameCollision(), `"a"`, `"b"`)
	for _, want := range []string{`"location"`, `"Location"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %s", err, want)
		}
	}
}
