// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// semverPattern behaves as the semantic-version grammar: it accepts every value
// isSemanticVersionPattern's accepted list holds and refuses every value on its
// refused list.
const semverPattern = `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`

// compileValuesPillar compiles a Component schema whose values pillar is the
// given YAML body, through the compiler the check itself compiles a Component's
// schema with.
func compileValuesPillar(t *testing.T, body string) map[string]any {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("values:\n"+body), &node); err != nil {
		t.Fatalf("parse the schema: %v", err)
	}
	compiled, err := compactform.Compile(&node)
	if err != nil {
		t.Fatalf("compile the schema: %v", err)
	}
	return compiled
}

// pillarFindings validates the placeholders against the compiled values pillar
// with the schema validator a build applies to authored values, and returns what
// it found. The placeholders travel through JSON first, the shape a decoded
// document reaches the validator in.
func pillarFindings(t *testing.T, compiled, placeholders map[string]any) []jsonvalidator.Finding {
	t.Helper()
	pillar, ok := mapAt(compiled, "properties", "values")
	if !ok {
		t.Fatalf("the compiled schema has no values pillar: %v", compiled)
	}
	schemaBytes, err := json.Marshal(pillar)
	if err != nil {
		t.Fatalf("marshal the pillar schema: %v", err)
	}
	schema := &jsonschema.Schema{}
	if err := json.Unmarshal(schemaBytes, schema); err != nil {
		t.Fatalf("decode the pillar schema: %v", err)
	}
	v, err := jsonvalidator.New(schema)
	if err != nil {
		t.Fatalf("build the validator: %v", err)
	}
	instanceBytes, err := json.Marshal(placeholders)
	if err != nil {
		t.Fatalf("marshal the placeholders: %v", err)
	}
	var instance any
	if err := json.Unmarshal(instanceBytes, &instance); err != nil {
		t.Fatalf("decode the placeholders: %v", err)
	}
	report, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("validate the placeholders: %v", err)
	}
	return report.Findings
}

// TestPlaceholdersHonourTheFieldsOwnConstraints fills a required field with no
// default under each constraint a field's schema can declare and a placeholder
// can satisfy, and holds the placeholder to two things: the value the row names,
// and the verdict of the schema validator a build applies to authored values,
// which must find nothing. The second is the property the fill exists for — a
// placeholder the field's own schema refuses fails a Recipe a build setting the
// field accepts — so a fill that stops honouring a constraint turns its row red
// on the validator's findings whatever value it produced instead.
//
// Each schema is authored as a Component author writes it and compiled through
// the compiler the check uses, so a row also pins which compiled keyword the
// authored spelling becomes: min on an array is minItems, on a string minLength.
func TestPlaceholdersHonourTheFieldsOwnConstraints(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		field string
		body  string
		want  any
	}{
		{
			name:  "minItems: as many elements as it asks, each its items schema's placeholder",
			field: "goals",
			body:  "  goals: { type: array, items: { type: string }, minItems: 1, required: true }\n",
			want:  []any{"example"},
		},
		{
			name:  "min on an array is its minItems",
			field: "goals",
			body:  "  goals: { type: array, items: { type: string }, min: 2, required: true }\n",
			want:  []any{"example", "example"},
		},
		{
			name:  "an array whose elements are objects requiring properties",
			field: "models",
			body: "  models:\n" +
				"    type: array\n" +
				"    min: 1\n" +
				"    required: true\n" +
				"    items:\n" +
				"      name: { type: string, required: true }\n" +
				"      target: { type: string, required: true }\n" +
				"      weight: { type: integer, default: 1, required: true }\n" +
				"      note: { type: string }\n",
			want: []any{map[string]any{"name": "example", "target": "example", "weight": 1}},
		},
		{
			name:  "an array with no minItems is empty",
			field: "tags",
			body:  "  tags: { type: array, items: { type: string }, required: true }\n",
			want:  []any{},
		},
		{
			name:  "minLength lengthens the string",
			field: "token",
			body:  "  token: { type: string, minLength: 12, required: true }\n",
			want:  "examplexxxxx",
		},
		{
			name:  "min on a string is its minLength",
			field: "code",
			body:  "  code: { type: string, min: 10, required: true }\n",
			want:  "examplexxx",
		},
		{
			name:  "maxLength cuts the string",
			field: "tag",
			body:  "  tag: { type: string, max: 3, required: true }\n",
			want:  "exa",
		},
		{
			name:  "enum: its first member",
			field: "tier",
			body:  "  tier: { type: string, enum: [gold, silver], required: true }\n",
			want:  "gold",
		},
		{
			name:  "enum of integers: its first member",
			field: "level",
			body:  "  level: { type: integer, enum: [3, 5], required: true }\n",
			want:  3,
		},
		{
			name:  "const: the constant",
			field: "mode",
			body:  "  mode: { type: string, const: strict, required: true }\n",
			want:  "strict",
		},
		{
			name:  "minimum on an integer",
			field: "replicas",
			body:  "  replicas: { type: integer, min: 2, required: true }\n",
			want:  2,
		},
		{
			name:  "exclusiveMinimum on an integer: the next integer past it",
			field: "limit",
			body:  "  limit: { type: integer, exclusiveMinimum: 0, required: true }\n",
			want:  1,
		},
		{
			name:  "exclusiveMinimum on a number: a value past it",
			field: "ratio",
			body:  "  ratio: { type: number, exclusiveMinimum: 0.5, required: true }\n",
			want:  1.5,
		},
		{
			name:  "a maximum below zero with no lower bound: the maximum",
			field: "offset",
			body:  "  offset: { type: integer, max: -3, required: true }\n",
			want:  -3,
		},
		{
			name:  "exclusiveMaximum excluding zero: the next integer below it",
			field: "below",
			body:  "  below: { type: integer, exclusiveMaximum: 0, required: true }\n",
			want:  -1,
		},
		{
			name:  "multipleOf: the lower bound rounded up to a multiple",
			field: "port",
			body:  "  port: { type: integer, minimum: 1001, multipleOf: 10, required: true }\n",
			want:  1010,
		},
		{
			name:  "format: a value in that format",
			field: "contact",
			body:  "  contact: { type: string, format: email, required: true }\n",
			want:  "user@example.com",
		},
		{
			name:  "a pattern behaving as the semantic-version grammar: a semantic version",
			field: "release",
			body:  "  release: { type: string, pattern: '" + semverPattern + "', required: true }\n",
			want:  "1.0.0",
		},
		{
			name:  "a pattern the plain placeholder satisfies leaves it as it is",
			field: "route",
			body:  "  route: { type: string, pattern: '^[a-z0-9][a-z0-9_-]*$', required: true }\n",
			want:  "example",
		},
		{
			name:  "a type written as a list: its first member",
			field: "either",
			body:  "  either: { type: [integer, string], required: true }\n",
			want:  0,
		},
		{
			name:  "an object: each property it requires, defaults written out",
			field: "database",
			body: "  $jsonschema:\n" +
				"    type: object\n" +
				"    properties:\n" +
				"      database:\n" +
				"        type: object\n" +
				"        properties:\n" +
				"          host: { type: string, minLength: 9 }\n" +
				"          port: { type: integer, default: 5432 }\n" +
				"          tls: { type: boolean }\n" +
				"        required: [host, port]\n" +
				"        additionalProperties: false\n" +
				"    required: [database]\n" +
				"    additionalProperties: false\n",
			want: map[string]any{"host": "examplexx", "port": 5432},
		},
		{
			name:  "an object whose required properties all carry defaults is not left empty",
			field: "options",
			body: "  $jsonschema:\n" +
				"    type: object\n" +
				"    properties:\n" +
				"      options:\n" +
				"        type: object\n" +
				"        properties:\n" +
				"          mode: { type: string, default: fast }\n" +
				"        required: [mode]\n" +
				"    required: [options]\n",
			want: map[string]any{"mode": "fast"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compiled := compileValuesPillar(t, tc.body)
			placeholders := requiredFieldPlaceholders(compiled, "values")
			got, ok := placeholders[tc.field]
			if !ok {
				t.Fatalf("no placeholder for %q; placeholders: %#v", tc.field, placeholders)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("placeholder for %q = %#v, want %#v", tc.field, got, tc.want)
			}
			if findings := pillarFindings(t, compiled, placeholders); len(findings) != 0 {
				t.Errorf("the field's own schema refuses its placeholder %#v: %v", got, findings)
			}
		})
	}
}

// TestPlaceholdersAreAFunctionOfTheSchema fills the same schema, compiled twice,
// and requires the two fills to be equal and to serialize to the same bytes —
// the bytes the synthesized documents carry — so a check run twice over one
// Component builds the same harness.
func TestPlaceholdersAreAFunctionOfTheSchema(t *testing.T) {
	t.Parallel()
	body := "  models:\n" +
		"    type: array\n" +
		"    min: 2\n" +
		"    required: true\n" +
		"    items:\n" +
		"      zeta: { type: string, required: true }\n" +
		"      alpha: { type: integer, exclusiveMinimum: 0, required: true }\n" +
		"      mid: { type: string, enum: [b, a], required: true }\n" +
		"  contact: { type: string, format: uri, required: true }\n" +
		"  token: { type: string, min: 9, required: true }\n"
	first := requiredFieldPlaceholders(compileValuesPillar(t, body), "values")
	second := requiredFieldPlaceholders(compileValuesPillar(t, body), "values")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two fills of one schema differ:\n%#v\n%#v", first, second)
	}
	firstBytes, err := yaml.Marshal(first)
	if err != nil {
		t.Fatalf("marshal the first fill: %v", err)
	}
	secondBytes, err := yaml.Marshal(second)
	if err != nil {
		t.Fatalf("marshal the second fill: %v", err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("two fills of one schema serialize differently:\n%s\n%s", firstBytes, secondBytes)
	}
}

// TestEveryStringFormatHasAPlaceholderItsCheckAccepts holds formatExamples to the
// supported formats in both directions: every format a schema may name has an
// example, the format's own check accepts it, and no example names a format the
// set does not support. A format added to the supported set without an example
// fails here rather than being filled with a value its check refuses.
func TestEveryStringFormatHasAPlaceholderItsCheckAccepts(t *testing.T) {
	t.Parallel()
	supported := map[string]bool{}
	for _, name := range stringformat.Names() {
		supported[name] = true
		example, ok := formatExamples[name]
		if !ok {
			t.Errorf("format %q has no placeholder", name)
			continue
		}
		if err := stringformat.Check(name, example); err != nil {
			t.Errorf("the placeholder for format %q is refused by its check: %v", name, err)
		}
		if got := stringPlaceholder(map[string]any{"type": "string", "format": name}); got != example {
			t.Errorf("a string of format %q is filled with %q, want %q", name, got, example)
		}
	}
	for name := range formatExamples {
		if !supported[name] {
			t.Errorf("a placeholder is kept for %q, which is not a supported format", name)
		}
	}
}
