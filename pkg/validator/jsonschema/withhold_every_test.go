// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// everyValueFixtureSchema closes an object to four properties: two that
// constrain a value, one whose schema declares it sensitive, and one that must be
// present.
const everyValueFixtureSchema = `{
  "type": "object",
  "required": ["name"],
  "additionalProperties": false,
  "properties": {
    "name": {"type": "string", "pattern": "^[a-z]+$"},
    "policy": {"type": "string", "enum": ["Always", "Never"]},
    "key": {"type": "string", "pattern": "^PUBLIC:", "x-aggrega-sensitive": true}
  }
}`

// everyValueNotice is the caller's reason the option closes each sentence with.
const everyValueNotice = " (the value is withheld because the caller says so)"

func validateEveryValueFixture(t *testing.T, instance map[string]any, opts ...Option) []Finding {
	t.Helper()
	s := &jsonschema.Schema{}
	if err := json.Unmarshal([]byte(everyValueFixtureSchema), s); err != nil {
		t.Fatalf("decode the fixture schema: %v", err)
	}
	v, err := New(s, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rpt, err := v.Validate(instance, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return rpt.Findings
}

// TestWithholdEveryValue_ComposesEveryValueBearingFindingFromTheSchema: a finding
// at a position no schema declares sensitive is composed from the schema and
// closed by the caller's notice, and a position the schema does declare keeps the
// schema's own notice where both options are set. Each row's control is the same
// instance validated without the option, which must quote the value.
func TestWithholdEveryValue_ComposesEveryValueBearingFindingFromTheSchema(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		instance map[string]any
		path     string
		value    string
		opts     []Option
		expects  string
	}{
		{
			name:     "a pattern on a position declaring nothing",
			instance: map[string]any{"name": "Rendered-9c8b"},
			path:     "name",
			value:    "Rendered-9c8b",
			opts:     []Option{WithholdEveryValue(everyValueNotice)},
			expects:  `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "^[a-z]+$"` + everyValueNotice,
		},
		{
			name:     "an enumeration on a position declaring nothing",
			instance: map[string]any{"name": "ok", "policy": "rendered-7d6e"},
			path:     "policy",
			value:    "rendered-7d6e",
			opts:     []Option{WithholdEveryValue(everyValueNotice)},
			expects:  `enum: ` + redact.Placeholder() + ` does not satisfy enum ["Always","Never"]` + everyValueNotice,
		},
		{
			name:     "a position the schema declares sensitive, with only the caller's option",
			instance: map[string]any{"name": "ok", "key": "PRIVATE-1a2b"},
			path:     "key",
			value:    "PRIVATE-1a2b",
			opts:     []Option{WithholdEveryValue(everyValueNotice)},
			expects:  `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "^PUBLIC:"` + everyValueNotice,
		},
		{
			name:     "a position the schema declares sensitive, with both options",
			instance: map[string]any{"name": "ok", "key": "PRIVATE-3c4d"},
			path:     "key",
			value:    "PRIVATE-3c4d",
			opts:     []Option{WithholdSensitiveValues(), WithholdEveryValue(everyValueNotice)},
			expects:  `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "^PUBLIC:"` + withheldNotice,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			quoted := findingAt(validateEveryValueFixture(t, tc.instance), tc.path)
			if quoted == nil || !strings.Contains(quoted.Message, tc.value) {
				t.Fatalf("control: the finding at %s does not quote the value without the option, so the row proves nothing: %+v", tc.path, quoted)
			}
			withheld := findingAt(validateEveryValueFixture(t, tc.instance, tc.opts...), tc.path)
			if withheld == nil {
				t.Fatalf("no finding at %s with the option; the refusal must still be reported", tc.path)
			}
			if withheld.Message != tc.expects {
				t.Errorf("the finding at %s\n got: %q\nwant: %q", tc.path, withheld.Message, tc.expects)
			}
			if withheld.Keyword != quoted.Keyword {
				t.Errorf("the finding's keyword moved from %q to %q", quoted.Keyword, withheld.Keyword)
			}
		})
	}
}

// TestWithholdEveryValue_KeepsASentenceQuotingNoValue: a missing property and a
// refused key are reported from the schema and the document's shape, so the
// option leaves those findings exactly as a validator built without it reports
// them.
func TestWithholdEveryValue_KeepsASentenceQuotingNoValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		instance map[string]any
	}{
		{"a required property missing", map[string]any{}},
		{"a key the object does not admit", map[string]any{"name": "ok", "strayKey": "rendered-5f4e"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := validateEveryValueFixture(t, tc.instance)
			after := validateEveryValueFixture(t, tc.instance, WithholdEveryValue(everyValueNotice))
			if len(before) == 0 {
				t.Fatal("control: the row produced no finding, so it proves nothing")
			}
			if !reflect.DeepEqual(before, after) {
				t.Errorf("the report moved\nbefore: %+v\n after: %+v", before, after)
			}
		})
	}
}
