// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"
)

// newValidatorFromJSON builds a validator from a schema written as JSON,
// so a test can state the shape it is about in one literal.
func newValidatorFromJSON(t *testing.T, doc string) Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(doc), schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	validator, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return validator
}

// TestContainerValueIsNamedByItsJSONTypeRatherThanFormatted pins the
// second half of the value-naming rule. A null becomes the word the author
// wrote; an object or an array becomes its JSON type name, because a Go
// value formatter renders a container in Go's own syntax and a reader has
// the container in front of them already.
//
// The offending value is stated per case and its formatted text is
// required to be absent, rather than searching for the shapes Go syntax
// tends to take. A container's formatting is not always recognisable —
// a list of strings formats as bracketed words with no other tell — so a
// pattern for the recognisable shapes would pass an array case for the
// wrong reason.
//
// Both the reduced-copy path and an array element are covered, since the
// library walks array elements itself and so may name a value nested
// inside the one the adapter handed it.
func TestContainerValueIsNamedByItsJSONTypeRatherThanFormatted(t *testing.T) {
	cases := []struct {
		name      string
		schema    string
		instance  map[string]any
		offending any
		wantWord  string
	}{
		{
			name:      "an object where an array is declared",
			schema:    `{"type": "object", "properties": {"env": {"type": "array"}}}`,
			instance:  map[string]any{"env": map[string]any{"ZETA": "z", "ALPHA": "a"}},
			offending: map[string]any{"ZETA": "z", "ALPHA": "a"},
			wantWord:  "object",
		},
		{
			name:      "an array where a string is declared",
			schema:    `{"type": "object", "properties": {"name": {"type": "string"}}}`,
			instance:  map[string]any{"name": []any{"first", "second"}},
			offending: []any{"first", "second"},
			wantWord:  "array",
		},
		{
			name:      "an object nested inside an array the library walks itself",
			schema:    `{"type": "object", "properties": {"ports": {"type": "array", "items": {"type": "string"}}}}`,
			instance:  map[string]any{"ports": []any{map[string]any{"containerPort": "8080"}}},
			offending: map[string]any{"containerPort": "8080"},
			wantWord:  "object",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			formatted := fmt.Sprintf("%v", tc.offending)
			report, err := newValidatorFromJSON(t, tc.schema).Validate(tc.instance, nil)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(report.Findings) == 0 {
				t.Fatal("the instance violates its schema, so a finding is required; without one this pin reads clean over nothing")
			}
			for _, finding := range report.Findings {
				if strings.Contains(finding.Message, formatted) {
					t.Errorf("a diagnostic must not render a container as %q: %s", formatted, finding.Message)
				}
				if !strings.Contains(finding.Message, tc.wantWord) {
					t.Errorf("a diagnostic about a %s must name it: %s", tc.wantWord, finding.Message)
				}
			}
		})
	}
}

// TestNestedContainerRenderingsAreOrderedLongestFirst pins the ordering
// the nesting case depends on. An outer container's rendering contains its
// children's, so replacing an outer one first removes the children too;
// replacing a child first would corrupt the outer text. The order must
// also be fully determined, since the walk over an object's values is
// not.
func TestNestedContainerRenderingsAreOrderedLongestFirst(t *testing.T) {
	instance := map[string]any{
		"outer": map[string]any{"inner": []any{"a", "b"}},
		"peer":  []any{"c"},
	}
	renderings := containerRenderings(instance)
	if len(renderings) != 4 {
		t.Fatalf("want a rendering for the instance, its nested object, its nested array and its peer array; got %d: %#v", len(renderings), renderings)
	}
	for i := 1; i < len(renderings); i++ {
		if len(renderings[i-1].text) < len(renderings[i].text) {
			t.Errorf("renderings must be longest first so an outer container is replaced before its children; %q precedes %q", renderings[i-1].text, renderings[i].text)
		}
		if len(renderings[i-1].text) == len(renderings[i].text) && renderings[i-1].text >= renderings[i].text {
			t.Errorf("equal-length renderings must be ordered by their text so the result is the same on every run; %q precedes %q", renderings[i-1].text, renderings[i].text)
		}
	}
	if renderings[0].jsonType != "object" {
		t.Errorf("the outermost rendering is the instance itself, an object; got %q", renderings[0].jsonType)
	}
}

// TestScalarValueSurvivesUnchanged pins the other side of the rule. A
// scalar's rendering is the value that actually arrived — for a template
// expression, the only place a reader can see it — so it is left alone.
func TestScalarValueSurvivesUnchanged(t *testing.T) {
	report, err := newValidatorFromJSON(t,
		`{"type": "object", "properties": {"cpu": {"type": "string"}}}`,
	).Validate(map[string]any{"cpu": float64(100)}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Findings) == 0 {
		t.Fatal("a number where a string is declared must be reported")
	}
	if !strings.Contains(report.Findings[0].Message, "100") {
		t.Errorf("a diagnostic about a scalar must name the value that arrived: %s", report.Findings[0].Message)
	}
}

// TestViolatedDefinitionIsNamed pins the provenance a diagnostic carries.
//
// A position holding only a reference is validated against the definition
// it names, which makes that definition the root of the library call — and
// a library names its own call root as the root, so nothing in the message
// would say which declared shape was violated. The adapter knows the name
// because it made the substitution, and this is the pin that it says so.
//
// The two positions are covered separately because a reference under
// properties and a reference under additionalProperties reach the
// substitution through different index paths, and a regression in one
// would not show in the other.
func TestViolatedDefinitionIsNamed(t *testing.T) {
	const schema = `{
		"type": "object",
		"properties": {
			"inject": {
				"type": "object",
				"properties": {"env": {"$ref": "#/$defs/delivery"}},
				"additionalProperties": {"$ref": "#/$defs/delivery"}
			}
		},
		"$defs": {"delivery": {"type": "array", "items": {"type": "object"}}}
	}`

	for _, mechanism := range []string{"env", "sidecar"} {
		t.Run(mechanism, func(t *testing.T) {
			instance := map[string]any{
				"inject": map[string]any{mechanism: map[string]any{"ZETA": "z"}},
			}
			report, err := newValidatorFromJSON(t, schema).Validate(instance, nil)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(report.Findings) == 0 {
				t.Fatal("a mapping where the definition declares an array must be reported")
			}
			named := false
			for _, finding := range report.Findings {
				if strings.Contains(finding.Message, localDefsPointer+"delivery") {
					named = true
				}
			}
			if !named {
				t.Errorf("the diagnostic must name the definition that was violated; got %#v", report.Findings)
			}
		})
	}
}

// TestLibraryNamesACallRootAsRoot pins the one agreement the provenance
// rewrite rests on: the label a schema library writes for the schema a
// validation call started from. The label is recognised so it can be
// replaced by the name of the definition actually being checked, so a
// library that renames it would silently stop the diagnostic naming the
// violated definition — a loss no other test would notice. This fails
// instead.
func TestLibraryNamesACallRootAsRoot(t *testing.T) {
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(`{"type": "array"}`), schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	err = resolved.Validate(map[string]any{"a": 1})
	if err == nil {
		t.Fatal("an object where an array is declared must fail, otherwise there is no message to read the label from")
	}
	if !strings.HasPrefix(err.Error(), libraryRootLabel) {
		t.Errorf("the call-root label is no longer %q, so a diagnostic can no longer be told which definition was violated; got %q", libraryRootLabel, err.Error())
	}
}

// TestCallRootLabelIsOnlyReplacedWhenADefinitionWasSubstituted pins the
// two ways the rewrite declines to act: no definition was substituted, so
// there is nothing to name; and the label is absent, which is what a
// schema declaring its own identifier produces — already provenance, and
// not to be overwritten.
func TestCallRootLabelIsOnlyReplacedWhenADefinitionWasSubstituted(t *testing.T) {
	const message = libraryRootLabel + `type: object has type "object", want "array"`

	if got := nameCallRoot(message, ""); got != message {
		t.Errorf("with no definition substituted the message must be untouched; got %q", got)
	}

	identified := `validating https://example.test/schema: type: object has type "object", want "array"`
	if got := nameCallRoot(identified, "delivery"); got != identified {
		t.Errorf("a root the library named some other way must be left as it named it; got %q", got)
	}

	want := `validating ` + localDefsPointer + `delivery: type: object has type "object", want "array"`
	if got := nameCallRoot(message, "delivery"); got != want {
		t.Errorf("nameCallRoot = %q, want %q", got, want)
	}
}
