// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"testing"
)

// reasonSentence is the description the rows below write on an excluded
// branch. It is deliberately not a sentence any other message carries, so a
// finding holding it can only have taken it from the description.
const reasonSentence = "reserved is the name of the merged view, so it cannot name an entry"

// TestNotReportsTheDescriptionItsExcludedBranchDeclares pins the rule that a
// value refused by `not` is refused with the reason the schema writes on the
// excluded branch, wherever the walk meets that branch: at a declared
// property, in an object's key names, in an array's elements and inside a
// combinator branch. It also pins where the rule stops — a branch declaring
// no description keeps the message it always had, and a description on a
// definition the branch only references does not speak for the branch — so a
// schema that never wrote a reason on an exclusion prints exactly what it
// printed before.
//
// Each refusing row has a twin that leaves the excluded value out and must
// report nothing, so a refusal is attributable to the exclusion rather than
// to the shape around it.
func TestNotReportsTheDescriptionItsExcludedBranchDeclares(t *testing.T) {
	cases := []struct {
		name      string
		schema    string
		instance  map[string]any
		wantPath  string
		wantMsg   string
		wantClean map[string]any
	}{
		{
			name:      "an excluded value at a declared property",
			schema:    `{"type": "object", "properties": {"name": {"type": "string", "not": {"const": "reserved", "description": "` + reasonSentence + `"}}}}`,
			instance:  map[string]any{"name": "reserved"},
			wantPath:  "name",
			wantMsg:   reasonSentence,
			wantClean: map[string]any{"name": "custom"},
		},
		{
			name:      "an excluded key name, through propertyNames",
			schema:    `{"type": "object", "propertyNames": {"not": {"const": "reserved", "description": "` + reasonSentence + `"}}, "additionalProperties": {"type": "string"}}`,
			instance:  map[string]any{"custom": "x", "reserved": "y"},
			wantPath:  "",
			wantMsg:   `property name "reserved" is not allowed here: ` + reasonSentence,
			wantClean: map[string]any{"custom": "x", "RESERVED": "y"},
		},
		{
			name:      "an excluded array element",
			schema:    `{"type": "object", "properties": {"names": {"type": "array", "items": {"type": "string", "not": {"const": "reserved", "description": "` + reasonSentence + `"}}}}}`,
			instance:  map[string]any{"names": []any{"env", "reserved"}},
			wantPath:  "names[1]",
			wantMsg:   reasonSentence,
			wantClean: map[string]any{"names": []any{"env", "custom"}},
		},
		{
			name:      "an exclusion inside an allOf branch",
			schema:    `{"type": "object", "properties": {"name": {"allOf": [{"type": "string"}, {"not": {"const": "reserved", "description": "` + reasonSentence + `"}}]}}}`,
			instance:  map[string]any{"name": "reserved"},
			wantPath:  "name",
			wantMsg:   reasonSentence,
			wantClean: map[string]any{"name": "custom"},
		},
		{
			name:      "a reference-only branch carrying its own description",
			schema:    `{"type": "object", "$defs": {"word": {"const": "reserved"}}, "properties": {"name": {"not": {"$ref": "#/$defs/word", "description": "` + reasonSentence + `"}}}}`,
			instance:  map[string]any{"name": "reserved"},
			wantPath:  "name",
			wantMsg:   reasonSentence,
			wantClean: map[string]any{"name": "custom"},
		},
		{
			// A description wrapped across lines, as a YAML block scalar or a long
			// JSON string writes it, is printed on one line.
			name:      "a description wrapped across lines is printed on one line",
			schema:    `{"type": "object", "properties": {"name": {"not": {"const": "reserved", "description": "reserved is the name of the merged view,\n   so it cannot\tname an entry\n"}}}}`,
			instance:  map[string]any{"name": "reserved"},
			wantPath:  "name",
			wantMsg:   reasonSentence,
			wantClean: map[string]any{"name": "custom"},
		},
		{
			name:      "an excluded branch declaring no description keeps its message",
			schema:    `{"type": "object", "properties": {"name": {"not": {"const": "reserved"}}}}`,
			instance:  map[string]any{"name": "reserved"},
			wantPath:  "name",
			wantMsg:   "value matches a shape that is not allowed here",
			wantClean: map[string]any{"name": "custom"},
		},
		{
			name:      "a referenced definition's description does not speak for the branch",
			schema:    `{"type": "object", "$defs": {"word": {"const": "reserved", "description": "` + reasonSentence + `"}}, "properties": {"name": {"not": {"$ref": "#/$defs/word"}}}}`,
			instance:  map[string]any{"name": "reserved"},
			wantPath:  "name",
			wantMsg:   `value matches "word", which is not allowed here`,
			wantClean: map[string]any{"name": "custom"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newValidatorFromJSON(t, tc.schema)

			report, err := v.Validate(tc.instance, nil)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			var found []string
			matched := false
			for _, f := range report.Findings {
				found = append(found, f.FieldPath+": "+f.Message)
				if f.FieldPath != tc.wantPath {
					continue
				}
				if f.Message == tc.wantMsg {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("want a finding at %q reading %q; got %q", tc.wantPath, tc.wantMsg, found)
			}

			clean, err := v.Validate(tc.wantClean, nil)
			if err != nil {
				t.Fatalf("Validate (clean twin): %v", err)
			}
			if len(clean.Findings) != 0 {
				t.Fatalf("the twin without the excluded value must report nothing; got %+v", clean.Findings)
			}
		})
	}
}
