// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

// TestDeclaresSensitiveCountsOnlyTheBooleanTrue pins the predicate every reader of
// the sensitivity declaration shares: only the boolean true declares it, and the
// same keyword spelled with any other value — the entity name a composed schema
// records beside a keyword, a false, a null — does not.
func TestDeclaresSensitiveCountsOnlyTheBooleanTrue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		keywords map[string]any
		want     bool
	}{
		{"the boolean true", map[string]any{SensitiveKeyword: true}, true},
		{"the boolean false", map[string]any{SensitiveKeyword: false}, false},
		{"an entity name recorded beside the keyword", map[string]any{SensitiveKeyword: "parent"}, false},
		{"a null", map[string]any{SensitiveKeyword: nil}, false},
		{"no keyword at all", map[string]any{"type": "string"}, false},
		{"no keywords", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DeclaresSensitive(tc.keywords); got != tc.want {
				t.Errorf("DeclaresSensitive(%v) = %v; want %v", tc.keywords, got, tc.want)
			}
		})
	}
}

// sensitiveFixtureSchema declares, in every shape the walk reaches a position by, a
// value the schema says is sensitive, beside values it says nothing about:
//
//   - key: a leaf declaring it, with a pattern and a length bound;
//   - entries[].secret: a property of a reusable definition an array's elements
//     reference, the shape a registry list takes;
//   - vault: an object declaring it, whose own property inherits it;
//   - aliased: a declaration written beside a reference, which the walk
//     substitutes for the schema it names;
//   - wrapped: a position whose reference carries a constraint of its own beside
//     it, so the schema it names is checked whole and a sensitive property inside
//     it is reported at the referring position;
//   - address: a format beneath a declaration, which the format sweep judges;
//   - plain and entries[].label: neighbours that declare nothing.
const sensitiveFixtureSchema = `{
  "type": "object",
  "properties": {
    "key": {"type": "string", "pattern": "^PUBLIC:", "maxLength": 64, "x-aggrega-sensitive": true},
    "plain": {"type": "string", "pattern": "^ok$"},
    "entries": {"type": "array", "items": {"$ref": "#/$defs/Entry"}},
    "vault": {
      "type": "object",
      "x-aggrega-sensitive": true,
      "properties": {"token": {"type": "string", "pattern": "^t-"}}
    },
    "aliased": {"$ref": "#/$defs/Token", "x-aggrega-sensitive": true},
    "wrapped": {"$ref": "#/$defs/Holder", "minProperties": 1},
    "address": {"type": "string", "format": "uri", "x-aggrega-sensitive": true}
  },
  "$defs": {
    "Entry": {
      "type": "object",
      "properties": {
        "label": {"type": "string", "pattern": "^[a-z]+$"},
        "secret": {"type": "string", "pattern": "^PUBLIC:", "x-aggrega-sensitive": true}
      }
    },
    "Token": {"type": "string", "pattern": "^t-"},
    "Holder": {
      "type": "object",
      "properties": {"inner": {"type": "string", "pattern": "^PUBLIC:", "x-aggrega-sensitive": true}}
    }
  }
}`

// validateFixture builds a validator over sensitiveFixtureSchema with opts and
// returns the findings one instance produces.
func validateFixture(t *testing.T, instance map[string]any, opts ...Option) []Finding {
	t.Helper()
	s := &jsonschema.Schema{}
	if err := json.Unmarshal([]byte(sensitiveFixtureSchema), s); err != nil {
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

// TestAValueDeclaredSensitiveIsWithheldFromEveryFindingAtItsPosition drives one
// violating value per row through every shape the walk reaches a position by, and
// holds the finding to three things: it lands where it did without the option, the
// value appears nowhere in it, and it states what the schema expects there.
//
// Each row is also run without the option, as its control: the same finding at the
// same path quotes the value, which is both the proof that the row's value reaches
// a message at all — so its absence under the option means something — and the
// proof that a validator built without the option reports exactly as before.
//
// Planted defects each fail a row: dropping the inheritance fails the object row;
// reading the declaration only after a reference is substituted fails the row
// written beside one; reading only the position a finding lands on, rather than the
// failure's location chain, fails the row checked whole behind a reference; and
// leaving the format sweep unaware fails the format row.
func TestAValueDeclaredSensitiveIsWithheldFromEveryFindingAtItsPosition(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		instance map[string]any
		path     string
		value    string
		// expects is the sentence the withheld finding must carry, or empty where
		// only the value's absence and the placeholder are asserted.
		expects string
	}{
		{
			name:     "a leaf declaring it, refused by its pattern",
			instance: map[string]any{"key": "PRIVATE-9f8e7d6c"},
			path:     "key",
			value:    "PRIVATE-9f8e7d6c",
			expects:  `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "^PUBLIC:"` + withheldNotice,
		},
		{
			name:     "a leaf declaring it, refused by its length bound",
			instance: map[string]any{"key": "PUBLIC:" + strings.Repeat("x", 80)},
			path:     "key",
			value:    strings.Repeat("x", 80),
			expects:  `maxLength: ` + redact.Placeholder() + ` does not satisfy maxLength 64` + withheldNotice,
		},
		{
			name:     "a leaf declaring it, refused by its type",
			instance: map[string]any{"key": float64(424242)},
			path:     "key",
			value:    "424242",
			expects:  `type: ` + redact.Placeholder() + ` does not satisfy type "string"` + withheldNotice,
		},
		{
			name:     "a property of a definition an array's elements reference",
			instance: map[string]any{"entries": []any{map[string]any{"label": "ok", "secret": "PRIVATE-a1b2c3"}}},
			path:     "entries[0].secret",
			value:    "PRIVATE-a1b2c3",
			expects:  `against /$defs/Entry: pattern: ` + redact.Placeholder() + ` does not satisfy pattern "^PUBLIC:"` + withheldNotice,
		},
		{
			name:     "a property inside an object declaring it",
			instance: map[string]any{"vault": map[string]any{"token": "x-5a4b3c"}},
			path:     "vault.token",
			value:    "x-5a4b3c",
			expects:  `pattern: ` + redact.Placeholder() + ` does not satisfy pattern "^t-"` + withheldNotice,
		},
		{
			name:     "a declaration written beside a reference",
			instance: map[string]any{"aliased": "x-6d5e4f"},
			path:     "aliased",
			value:    "x-6d5e4f",
		},
		{
			name:     "a sensitive property inside a schema a reference names and the library checks whole",
			instance: map[string]any{"wrapped": map[string]any{"inner": "PRIVATE-7a8b9c"}},
			path:     "wrapped",
			value:    "PRIVATE-7a8b9c",
		},
		{
			name:     "a format beneath a declaration",
			instance: map[string]any{"address": "not a uri 3e2d1c"},
			path:     "address",
			value:    "3e2d1c",
			expects:  `format: ` + redact.Placeholder() + ` does not satisfy format "uri"` + withheldNotice,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			quoted := findingAt(validateFixture(t, tc.instance), tc.path)
			if quoted == nil {
				t.Fatalf("control: no finding at %s without the option", tc.path)
			}
			if !strings.Contains(quoted.Message, tc.value) {
				t.Fatalf("control: the finding at %s does not quote the value without the option, so the row proves nothing: %q", tc.path, quoted.Message)
			}

			withheld := findingAt(validateFixture(t, tc.instance, WithholdSensitiveValues()), tc.path)
			if withheld == nil {
				t.Fatalf("no finding at %s with the option; the refusal must still be reported", tc.path)
			}
			if strings.Contains(withheld.Message, tc.value) {
				t.Errorf("the finding at %s quotes the value: %q", tc.path, withheld.Message)
			}
			if !strings.Contains(withheld.Message, redact.Placeholder()) {
				t.Errorf("the finding at %s does not show the placeholder: %q", tc.path, withheld.Message)
			}
			if tc.expects != "" && withheld.Message != tc.expects {
				t.Errorf("the finding at %s\n got: %q\nwant: %q", tc.path, withheld.Message, tc.expects)
			}
			if withheld.Keyword != quoted.Keyword {
				t.Errorf("the finding's keyword moved from %q to %q", quoted.Keyword, withheld.Keyword)
			}
		})
	}
}

// TestAValueNothingDeclaresSensitiveReadsAsItDidBefore holds the neighbours of the
// sensitive values to the report a validator built without the option produces,
// finding for finding: a top-level value declaring nothing, and a sibling of a
// sensitive property inside the same array element.
func TestAValueNothingDeclaresSensitiveReadsAsItDidBefore(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		instance map[string]any
	}{
		{"a top-level value declaring nothing", map[string]any{"plain": "nope-1"}},
		{"a sibling of a sensitive property", map[string]any{"entries": []any{map[string]any{"label": "Not-Lower"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			before := validateFixture(t, tc.instance)
			after := validateFixture(t, tc.instance, WithholdSensitiveValues())
			if len(before) == 0 {
				t.Fatal("control: the row produced no finding, so it proves nothing")
			}
			if !reflect.DeepEqual(before, after) {
				t.Errorf("the report moved\nbefore: %+v\n after: %+v", before, after)
			}
		})
	}
}

// findingAt returns the first finding reported at path, or nil.
func findingAt(findings []Finding, path string) *Finding {
	for i := range findings {
		if findings[i].FieldPath == path {
			return &findings[i]
		}
	}
	return nil
}
