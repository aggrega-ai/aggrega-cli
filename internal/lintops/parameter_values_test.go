// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestLinterDocumentWithholdsEveryParameterValue pins what the one conversion
// every linter payload goes through withholds, per shape a parameter value takes
// and for a key outside the declaration vocabulary, and what it keeps beside it.
//
// Each row plants a value and asserts its absence from the converted document,
// beside a fragment the conversion must keep — so a row cannot pass by the
// conversion dropping everything. The kept fragments are written as the encoder
// writes them, keys in sorted order, so a declaration asserted present is also
// asserted to carry nothing but its contract.
func TestLinterDocumentWithholdsEveryParameterValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		doc    string
		absent []string
		kept   []string
	}{
		{
			name: "an Aggregate keeps each declaration and loses its default",
			doc: `kind: Aggregate
spec:
  parameters:
    region:
      type: string
      description: where it runs
      enum: [eu, us]
      required: true
      default: VALUE-TOP-DEFAULT
`,
			absent: []string{"VALUE-TOP-DEFAULT"},
			kept:   []string{`"region":{"description":"where it runs","enum":["eu","us"],"required":true,"type":"string"}`},
		},
		{
			name: "a default under properties, items or valuesType is withheld",
			doc: `kind: Aggregate
spec:
  parameters:
    db:
      type: object
      properties:
        password: {type: string, default: VALUE-PROPERTY-DEFAULT}
        default: {type: string, default: VALUE-FIELD-NAMED-DEFAULT}
    hosts:
      type: array
      items: {type: string, default: VALUE-ITEMS-DEFAULT}
    labels:
      type: object
      valuesType: {type: string, default: VALUE-VALUESTYPE-DEFAULT}
`,
			absent: []string{"VALUE-PROPERTY-DEFAULT", "VALUE-FIELD-NAMED-DEFAULT", "VALUE-ITEMS-DEFAULT", "VALUE-VALUESTYPE-DEFAULT"},
			kept: []string{
				`"db":{"properties":{"default":{"type":"string"},"password":{"type":"string"}},"type":"object"}`,
				`"hosts":{"items":{"type":"string"},"type":"array"}`,
				`"labels":{"type":"object","valuesType":{"type":"string"}}`,
			},
		},
		{
			name: "a key outside the declaration vocabulary is withheld with what it holds, at every level",
			doc: `kind: Aggregate
spec:
  parameters:
    api:
      type: object
      description: the upstream
      token: VALUE-UNKNOWN-TOP
      secret: {password: VALUE-UNKNOWN-MAPPING}
      properties:
        token: {type: string, passwd: VALUE-UNKNOWN-PROPERTY}
    hosts:
      type: array
      items: {type: string, value: VALUE-UNKNOWN-ITEMS}
    labels:
      type: object
      valuesType: {type: string, example: VALUE-UNKNOWN-VALUESTYPE}
`,
			absent: []string{
				"VALUE-UNKNOWN-TOP", "VALUE-UNKNOWN-MAPPING", "VALUE-UNKNOWN-PROPERTY", "VALUE-UNKNOWN-ITEMS",
				"VALUE-UNKNOWN-VALUESTYPE", `"secret"`, `"passwd"`, `"value"`, `"example"`,
			},
			kept: []string{
				`"api":{"description":"the upstream","properties":{"token":{"type":"string"}},"type":"object"}`,
				`"hosts":{"items":{"type":"string"},"type":"array"}`,
				`"labels":{"type":"object","valuesType":{"type":"string"}}`,
			},
		},
		{
			name: "every key of the declaration vocabulary but the default is kept, at every level",
			doc: `kind: Aggregate
spec:
  parameters:
    port:
      type: integer
      description: the port
      default: VALUE-ALL-KEYS-DEFAULT
      enum: [80, 443]
      min: 1
      max: 65535
      required: true
      validate: "self != 0"
      validateMessage: must not be zero
    name:
      type: string
      minLength: 1
      maxLength: 63
      pattern: "^[a-z]+$"
      format: hostname
    tags:
      type: array
      items:
        type: object
        properties:
          key: {type: string, minLength: 1, default: VALUE-DEEP-DEFAULT}
      valuesType: {type: string}
`,
			absent: []string{"VALUE-ALL-KEYS-DEFAULT", "VALUE-DEEP-DEFAULT"},
			kept: []string{
				`"port":{"description":"the port","enum":[80,443],"max":65535,"min":1,"required":true,"type":"integer","validate":"self != 0","validateMessage":"must not be zero"}`,
				`"name":{"format":"hostname","maxLength":63,"minLength":1,"pattern":"^[a-z]+$","type":"string"}`,
				`"tags":{"items":{"properties":{"key":{"minLength":1,"type":"string"}},"type":"object"},"type":"array","valuesType":{"type":"string"}}`,
			},
		},
		{
			name: "a parameter named default keeps its place and loses its own default",
			doc: `kind: Aggregate
spec:
  parameters:
    default: {type: string, default: VALUE-PARAMETER-NAMED-DEFAULT}
`,
			absent: []string{"VALUE-PARAMETER-NAMED-DEFAULT"},
			kept:   []string{`"parameters":{"default":{"type":"string"}}`},
		},
		{
			name: "an entry that is not a declaration is withheld whole",
			doc: `kind: Aggregate
spec:
  parameters:
    api_key: {type: string}
    token: VALUE-BARE-ENTRY
`,
			absent: []string{"VALUE-BARE-ENTRY", `"token"`},
			kept:   []string{`"parameters":{"api_key":{"type":"string"}}`},
		},
		{
			name: "a nested position that holds no declaration is withheld",
			doc: `kind: Aggregate
spec:
  parameters:
    db:
      type: object
      properties: VALUE-BARE-PROPERTIES
      items: VALUE-BARE-ITEMS
`,
			absent: []string{"VALUE-BARE-PROPERTIES", "VALUE-BARE-ITEMS"},
			kept:   []string{`"db":{"type":"object"}`},
		},
		{
			name: "parameters that are not a mapping of declarations are withheld whole",
			doc: `kind: Aggregate
metadata:
  name: bare
spec:
  parameters: VALUE-BARE-PARAMETERS
`,
			absent: []string{"VALUE-BARE-PARAMETERS", `"parameters"`},
			kept:   []string{`"name":"bare"`},
		},
		{
			name: "each module loses the values passed to it and keeps the rest",
			doc: `kind: Aggregate
spec:
  aggregates:
    edge:
      type: probe-module
      active: "{{ .parameters.edge_on }}"
      parameters:
        zone: VALUE-MODULE
`,
			absent: []string{"VALUE-MODULE"},
			kept:   []string{`"edge":{"active":"{{ .parameters.edge_on }}","type":"probe-module"}`},
		},
		{
			name: "a Release loses the values it supplies inline",
			doc: `kind: Release
spec:
  parameters:
    region: VALUE-RELEASE-INLINE
    db: {password: VALUE-RELEASE-NESTED}
  boundaries:
    app:
      directory: ./dist/app
`,
			absent: []string{"VALUE-RELEASE-INLINE", "VALUE-RELEASE-NESTED", `"parameters"`},
			kept:   []string{`"boundaries":{"app":{"directory":"./dist/app"}}`},
		},
		{
			name: "a Release loses the file it imports its values from",
			doc: `kind: Release
metadata:
  name: imported
spec:
  parameters:
    $import: ./VALUE-RELEASE-IMPORT.yaml
`,
			absent: []string{"VALUE-RELEASE-IMPORT", `"parameters"`},
			kept:   []string{`"name":"imported"`},
		},
		{
			name: "any other kind loses a parameters block whole",
			doc: `kind: Component
spec:
  description: a component
  parameters:
    region: {type: string, default: VALUE-OTHER-KIND}
`,
			absent: []string{"VALUE-OTHER-KIND", `"parameters"`},
			kept:   []string{`"description":"a component"`},
		},
		{
			name: "a document naming no kind is held to the stricter reading",
			doc: `spec:
  description: unnamed
  parameters:
    region: {type: string, description: VALUE-NO-KIND}
`,
			absent: []string{"VALUE-NO-KIND", `"parameters"`},
			kept:   []string{`"description":"unnamed"`},
		},
		{
			name: "template text that reads a parameter is kept as written",
			doc: `kind: Aggregate
spec:
  components:
    web:
      type: core/compute/web-ui
      values:
        region: "{{ .parameters.region }}"
`,
			kept: []string{`"values":{"region":"{{ .parameters.region }}"}`},
		},
		{
			name: "a document with no spec is left as it is",
			doc: `kind: Aggregate
metadata:
  name: no-spec
`,
			kept: []string{`{"kind":"Aggregate","metadata":{"name":"no-spec"}}`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, value := range tc.absent {
				if !strings.Contains(tc.doc, strings.Trim(value, `"`)) {
					t.Fatalf("the row's document does not carry %s, so its absence below would prove nothing", value)
				}
			}
			out, _, err := yamlToLinterJSON([]byte(tc.doc))
			if err != nil {
				t.Fatalf("yamlToLinterJSON: %v", err)
			}
			for _, value := range tc.absent {
				if strings.Contains(string(out), value) {
					t.Errorf("the converted document carries %s; no parameter value may reach a linter plugin:\n%s", value, out)
				}
			}
			for _, fragment := range tc.kept {
				if !strings.Contains(string(out), fragment) {
					t.Errorf("the converted document lost %s, which is not a parameter value:\n%s", fragment, out)
				}
			}
		})
	}
}

// TestDeclarationVocabularyIsTheAggregateSchemas holds the declaration vocabulary
// the conversion derives from the declaration type equal to the keys the Aggregate
// schema admits on a parameter declaration. A key on one side only is named, in
// each direction: one the schema admits and the type lacks is withheld from every
// plugin although a valid Aggregate may carry it, and one the type decodes and the
// schema lacks is handed to plugins although no valid Aggregate carries it.
//
// The schema describes a parameter's own declaration only — a declaration nested
// under properties, items or valuesType is left open there — so this is the one
// level at which the two can be compared.
func TestDeclarationVocabularyIsTheAggregateSchemas(t *testing.T) {
	t.Parallel()
	var root map[string]any
	if err := json.Unmarshal([]byte(schemas.AggregateV1alpha1), &root); err != nil {
		t.Fatalf("unmarshal the Aggregate schema: %v", err)
	}
	node := any(root)
	for _, key := range []string{"properties", "spec", "properties", "parameters", "additionalProperties", "properties"} {
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("aggregate-v1alpha1.schema.json: no %q on the path to a parameter declaration's properties", key)
		}
		node = m[key]
	}
	admitted, ok := node.(map[string]any)
	if !ok || len(admitted) == 0 {
		t.Fatal("aggregate-v1alpha1.schema.json: a parameter declaration admits no properties, so there is nothing to compare")
	}
	for _, key := range slices.Sorted(maps.Keys(admitted)) {
		if !declarationKeys[key] {
			t.Errorf("the schema admits %q on a parameter declaration, but the declaration type decodes no such key, so it never reaches a plugin", key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(declarationKeys)) {
		if _, ok := admitted[key]; !ok {
			t.Errorf("the declaration type decodes %q, but the schema admits no such key on a parameter declaration", key)
		}
	}
}

// TestDeclarationWalkClassifiesEveryFieldThatCanHoldAMapping holds the keys the
// conversion treats by name to the declaration type's own field tags, and fails for
// a field of that type whose value can hold a mapping and that is not classified
// here. Such a field is where a value or a nested declaration can sit, so a field
// added to the type stays red until someone says which it holds: kept as written,
// withheld as a value, or walked as nested declarations.
func TestDeclarationWalkClassifiesEveryFieldThatCanHoldAMapping(t *testing.T) {
	t.Parallel()
	declaration := reflect.TypeFor[types.ParameterContract]()
	keyOf := func(field string) string {
		t.Helper()
		f, ok := declaration.FieldByName(field)
		if !ok {
			t.Fatalf("the declaration type has no field %s", field)
		}
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		return name
	}
	if got := keyOf("Default"); got != declarationValueKey {
		t.Errorf("the declaration's value decodes from %q, but the conversion withholds %q", got, declarationValueKey)
	}
	if got := keyOf("Properties"); got != declarationFieldsKey {
		t.Errorf("the declaration's named nested declarations decode from %q, but the conversion walks %q", got, declarationFieldsKey)
	}
	if got := []string{keyOf("Items"), keyOf("ValuesType")}; !slices.Equal(got, nestedDeclarationKeys) {
		t.Errorf("the declaration's nested declarations decode from %v, but the conversion walks %v", got, nestedDeclarationKeys)
	}
	classified := map[string]string{
		"Default":    "withheld",
		"Enum":       "kept as written",
		"Min":        "kept as written",
		"Max":        "kept as written",
		"Items":      "walked",
		"ValuesType": "walked",
		"Properties": "walked",
	}
	for i := range declaration.NumField() {
		field := declaration.Field(i)
		_, named := classified[field.Name]
		switch open := holdsAnyValue(field.Type, map[reflect.Type]bool{}); {
		case open && !named:
			t.Errorf("field %s can hold a mapping and is not classified; say whether the conversion keeps, withholds or walks it", field.Name)
		case !open && named:
			t.Errorf("field %s is classified but cannot hold a mapping; remove it from the classification", field.Name)
		}
	}
}

// holdsAnyValue reports whether a value of type t can hold a value of any type,
// which is what lets a field carry a mapping the declaration type does not
// describe.
func holdsAnyValue(t reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return holdsAnyValue(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			if holdsAnyValue(t.Field(i).Type, seen) {
				return true
			}
		}
	}
	return false
}
