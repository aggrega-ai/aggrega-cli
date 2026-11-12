// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaFailureOpening is the line the schema library opens every validation
// failure with, for a schema compiled under settingsSchemaResourceID.
const schemaFailureOpening = "jsonschema validation failed with 'mem://settings-schema.json#'"

// metaschemaRefusesThreeTypes is a schema the metaschema refuses at three
// positions — three properties whose "type" is a number — so the refusal lists
// three causes, collected while the library ranges over the schema's own
// "properties" object.
const metaschemaRefusesThreeTypes = `{"type":"object","properties":{"a":{"type":1},"b":{"type":2},"c":{"type":3}}}`

// metaschemaRefusalOfThreeTypes is the refusal of metaschemaRefusesThreeTypes,
// its causes in position order.
const metaschemaRefusalOfThreeTypes = `"mem://settings-schema.json#" is not valid against metaschema: ` +
	"jsonschema validation failed with 'https://json-schema.org/draft/2020-12/schema#'" +
	"\n- at '': 'allOf' failed" +
	"\n  - at '': validation failed" +
	"\n    - at '/properties/a': 'allOf' failed" +
	"\n      - at '/properties/a/type': 'anyOf' failed" +
	"\n        - at '/properties/a/type': value must be one of 'array', 'boolean', 'integer', 'null', 'number', 'object', 'string'" +
	"\n        - at '/properties/a/type': got number, want array" +
	"\n    - at '/properties/b': 'allOf' failed" +
	"\n      - at '/properties/b/type': 'anyOf' failed" +
	"\n        - at '/properties/b/type': value must be one of 'array', 'boolean', 'integer', 'null', 'number', 'object', 'string'" +
	"\n        - at '/properties/b/type': got number, want array" +
	"\n    - at '/properties/c': 'allOf' failed" +
	"\n      - at '/properties/c/type': 'anyOf' failed" +
	"\n        - at '/properties/c/type': value must be one of 'array', 'boolean', 'integer', 'null', 'number', 'object', 'string'" +
	"\n        - at '/properties/c/type': got number, want array"

// TestValidateValueAgainstSchema_ReadsTheSameOnEveryRun proves a failure reads
// the same on every run and lists its causes in position order, then keyword
// order, at every depth. The schema library collects causes while ranging over Go
// maps, whose order the runtime varies from one range to the next, so each row
// validates one value many times: a row whose order came from a map reads
// differently across the runs unless the failure is put in order.
//
// Each row carries one source of order: a list the library builds from a map,
// at the depth or in the place the row names, or — for the two controls — an
// order that is already fixed and must stay as it is.
func TestValidateValueAgainstSchema_ReadsTheSameOnEveryRun(t *testing.T) {
	t.Parallel()

	const runs = 200
	cases := []struct {
		name string
		// call refuses one input; every run calls it afresh.
		call func() error
		// refusal is true where the failure is a refused value, which a caller
		// reads through errors.As and WithheldValidationFailure.
		refusal bool
		want    string
	}{
		{
			// The object's keys are ranged over to check "properties".
			name: "four sibling properties refused",
			call: validating(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"},`+
				`"c":{"type":"string"},"d":{"type":"string"}}}`,
				map[string]any{"a": 1, "b": 2, "c": 3, "d": 4}),
			refusal: true,
			want: schemaFailureOpening +
				"\n- at '/a': got number, want string" +
				"\n- at '/b': got number, want string" +
				"\n- at '/c': got number, want string" +
				"\n- at '/d': got number, want string",
		},
		{
			// The top level lists the array's items in index order, which is
			// already fixed; only the first item's own causes come from a map, so
			// this row holds only where the order reaches every depth.
			name: "a nested list of causes",
			call: validating(`{"type":"array","items":{"type":"object","additionalProperties":{"type":"string"}}}`,
				[]any{map[string]any{"a": 1, "b": 2, "c": 3}, map[string]any{"d": 4}}),
			refusal: true,
			want: schemaFailureOpening +
				"\n- at '/0': validation failed" +
				"\n  - at '/0/a': got number, want string" +
				"\n  - at '/0/b': got number, want string" +
				"\n  - at '/0/c': got number, want string" +
				"\n- at '/1/d': got number, want string",
		},
		{
			// One cause, whose list of undeclared keys is gathered from a map.
			name: "undeclared keys named in one cause",
			call: validating(`{"type":"object","additionalProperties":false}`,
				map[string]any{"a": 1, "b": 2, "c": 3, "d": 4}),
			refusal: true,
			want: schemaFailureOpening +
				"\n- at '': additional properties 'a', 'b', 'c', 'd' not allowed",
		},
		{
			// Three patterns refuse one key at one position; the schema's
			// patterns are ranged over as a map, so only the keyword's place in
			// the schema separates the three causes.
			name: "one key refused by three patterns",
			call: validating(`{"type":"object","patternProperties":{"^a":{"type":"string"},"b$":{"maximum":0},`+
				`"^ab":{"multipleOf":7}}}`,
				map[string]any{"ab": 1}),
			refusal: true,
			want: schemaFailureOpening +
				"\n- at '/ab': got number, want string" +
				"\n- at '/ab': multipleOf: got 1, want 7" +
				"\n- at '/ab': maximum: got 1, want 0",
		},
		{
			// Property names refused by one propertyNames schema share their
			// position and their keyword, so only the sentence each renders
			// separates them.
			name: "three property names refused by one schema",
			call: validating(`{"type":"object","propertyNames":{"maxLength":1}}`,
				map[string]any{"aa": 1, "bb": 2, "cc": 3}),
			refusal: true,
			want: schemaFailureOpening +
				"\n- at '': invalid propertyName 'aa'" +
				"\n  - at '': maxLength: got 2, want 1" +
				"\n- at '': invalid propertyName 'bb'" +
				"\n  - at '': maxLength: got 2, want 1" +
				"\n- at '': invalid propertyName 'cc'" +
				"\n  - at '': maxLength: got 2, want 1",
		},
		{
			// Control: the library reports an array's items in index order, and
			// that order stays — 2 before 10, which a comparison of the indices
			// as text would reverse.
			name: "array items keep index order",
			call: validating(`{"type":"array","items":{"type":"string"}}`,
				[]any{1, "s", 2, "s", "s", "s", "s", "s", "s", "s", 3}),
			refusal: true,
			want: schemaFailureOpening +
				"\n- at '/0': got number, want string" +
				"\n- at '/2': got number, want string" +
				"\n- at '/10': got number, want string",
		},
		{
			// Control: a failure with one cause reads as it always has.
			name:    "a single cause",
			call:    validating(`{"type":"object","properties":{"port":{"type":"integer","maximum":1024}}}`, map[string]any{"port": 8080}),
			refusal: true,
			want:    schemaFailureOpening + "\n- at '/port': maximum: got 8,080, want 1,024",
		},
		{
			// The metaschema's refusal of a schema, reached by validating a
			// value against it.
			name: "a schema the metaschema refuses, validating a value",
			call: validating(metaschemaRefusesThreeTypes, map[string]any{}),
			want: metaschemaRefusalOfThreeTypes,
		},
		{
			// The same refusal, reached when a plugin's settings schema is
			// shape-checked at load.
			name: "a schema the metaschema refuses, checked at load",
			call: func() error {
				return PluginDescriptor{SettingsSchema: json.RawMessage(metaschemaRefusesThreeTypes)}.validateSettingsSchema("acme/policy")
			},
			want: fmt.Sprintf("plugin %q declares a settings_schema that is not a valid JSON Schema: %s: %v",
				"acme/policy", metaschemaRefusalOfThreeTypes, ErrWasmManifestRejected()),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			first := c.call()
			if first == nil {
				t.Fatal("the input must be refused")
			}
			if got := first.Error(); got != c.want {
				t.Fatalf("the failure reads:\n%s\nwant:\n%s", got, c.want)
			}
			if c.refusal {
				var failure *jsonschema.ValidationError
				if !errors.As(first, &failure) {
					t.Errorf("the failure must remain the schema library's validation failure, got %T", first)
				}
				if _, ok := WithheldValidationFailure(first); !ok {
					t.Errorf("the failure must still be described without its value")
				}
			}
			for run := 1; run < runs; run++ {
				err := c.call()
				if err == nil {
					t.Fatalf("run %d: the input must be refused", run)
				}
				if got := err.Error(); got != c.want {
					t.Fatalf("run %d reads differently from run 0:\n%s\nrun 0 and the expected sentence:\n%s", run, got, c.want)
				}
			}
		})
	}
}

// TestValidateValueAgainstSchema_SingleCauseIsTheLibrarysSentence proves the
// order a failure is put in leaves a single-cause refusal byte-identical to the
// sentence the schema library writes for it.
func TestValidateValueAgainstSchema_SingleCauseIsTheLibrarysSentence(t *testing.T) {
	t.Parallel()

	schema := json.RawMessage(`{"type":"object","properties":{"port":{"type":"integer","maximum":1024}},"required":["port"]}`)
	value := map[string]any{"port": 8080}

	compiled, err := compileSettingsSchema(schema)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	canonical, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	library := compiled.Validate(canonical)
	if library == nil {
		t.Fatal("the library must refuse the value")
	}
	ours := ValidateValueAgainstSchema(schema, value)
	if ours == nil {
		t.Fatal("the value must be refused")
	}
	if ours.Error() != library.Error() {
		t.Errorf("a single-cause refusal moved:\n got: %s\nwant: %s", ours.Error(), library.Error())
	}
}

// validating returns a call validating value against schema.
func validating(schema string, value any) func() error {
	return func() error { return ValidateValueAgainstSchema(json.RawMessage(schema), value) }
}
