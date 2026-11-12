// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// newFromJSON builds a validator over a schema written as JSON, returning New's
// error rather than failing, for the rows that expect a refusal.
func newFromJSON(t *testing.T, schemaJSON string) (Validator, error) {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaJSON), schema); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	return New(schema)
}

// formatFindings returns the findings a validation raised for the format
// keyword, rendered one per line.
func formatFindings(t *testing.T, v Validator, instance any, ctx *Context) []Finding {
	t.Helper()
	rpt, err := v.Validate(instance, ctx)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	var out []Finding
	for _, f := range rpt.Findings {
		if f.Keyword == "format" {
			out = append(out, f)
		}
	}
	return out
}

// TestNewRefusesAnUnsupportedFormatWhereverItSits drives a name outside the
// supported set into every kind of position a schema can carry one in —
// including a definition nothing references and a branch no instance reaches —
// and pins that New refuses each, naming the name and listing the supported set.
func TestNewRefusesAnUnsupportedFormatWhereverItSits(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name     string
		schema   string
		wantPath string
	}{
		{"a declared property", `{"type":"object","properties":{"secret":{"type":"string","format":"password"}}}`, "secret"},
		{"a nested property", `{"type":"object","properties":{"db":{"type":"object","properties":{"url":{"format":"url"}}}}}`, "db.url"},
		{"the document root", `{"type":"string","format":"emial"}`, ""},
		{"array elements", `{"type":"array","items":{"type":"string","format":"hostnme"}}`, ""},
		{"a oneOf branch", `{"oneOf":[{"type":"string","format":"email"},{"type":"string","format":"ipv5"}]}`, ""},
		{"a definition nothing references", `{"$defs":{"Unused":{"type":"string","format":"password"}},"type":"object"}`, ""},
		{"a then: branch", `{"if":true,"then":{"properties":{"a":{"format":"credit-card"}}}}`, "a"},
		{"pattern properties", `{"patternProperties":{"^x-":{"format":"password"}}}`, ""},
		{"additional properties", `{"additionalProperties":{"format":"password"}}`, ""},
		{"a not: branch", `{"not":{"format":"password"}}`, ""},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			_, err := newFromJSON(t, r.schema)
			if err == nil {
				t.Fatal("expected New to refuse an unsupported format; got nil")
			}
			if !errors.Is(err, ErrSchemaResolve()) {
				t.Errorf("want errors.Is(err, ErrSchemaResolve()); got %v", err)
			}
			var ae *AdapterError
			if !errors.As(err, &ae) {
				t.Fatalf("want *AdapterError; got %T: %v", err, err)
			}
			if ae.Keyword != "format" || ae.Path != r.wantPath {
				t.Errorf("Keyword=%q Path=%q; want format, %q", ae.Keyword, ae.Path, r.wantPath)
			}
			if !strings.Contains(err.Error(), strings.Join(stringformat.Names(), ", ")) {
				t.Errorf("the refusal does not list the supported formats: %v", err)
			}
		})
	}
}

// TestNewAcceptsEverySupportedFormat is the positive control for the refusals
// above.
func TestNewAcceptsEverySupportedFormat(t *testing.T) {
	t.Parallel()
	for _, name := range stringformat.Names() {
		if _, err := newFromJSON(t, `{"type":"object","properties":{"f":{"type":"string","format":"`+name+`"}}}`); err != nil {
			t.Errorf("format %q: New refused a supported name: %v", name, err)
		}
	}
}

// TestValidateChecksFormatAtEveryWalkedPosition drives a non-conforming and a
// conforming value into each kind of position the walk reaches itself, and pins
// the finding a failure produces: at the value's own path, under the format
// keyword, naming the value and the format.
func TestValidateChecksFormatAtEveryWalkedPosition(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name     string
		schema   string
		bad      any
		good     any
		wantPath string
	}{
		{"a declared property", `{"type":"object","properties":{"endpoint":{"type":"string","format":"uri"}}}`,
			map[string]any{"endpoint": "::::"}, map[string]any{"endpoint": "https://example.com"}, "endpoint"},
		{"a nested property", `{"type":"object","properties":{"db":{"type":"object","properties":{"host":{"type":"string","format":"hostname"}}}}}`,
			map[string]any{"db": map[string]any{"host": "*"}}, map[string]any{"db": map[string]any{"host": "db.local"}}, "db.host"},
		{"the document root", `{"type":"string","format":"email"}`, "not an email {\n}", "a@example.com", ""},
		{"an array element", `{"type":"object","properties":{"hosts":{"type":"array","items":{"type":"string","format":"hostname"}}}}`,
			map[string]any{"hosts": []any{"ok.example.com", "*"}}, map[string]any{"hosts": []any{"ok.example.com"}}, "hosts[1]"},
		{"an additional property", `{"type":"object","additionalProperties":{"type":"string","format":"date"}}`,
			map[string]any{"when": "2026-02-30"}, map[string]any{"when": "2026-02-28"}, "when"},
		{"a pattern property", `{"type":"object","patternProperties":{"^id_":{"type":"string","format":"uuid"}}}`,
			map[string]any{"id_a": "nope"}, map[string]any{"id_a": "2eb8aa08-aa98-11ea-b4aa-73b441d16380"}, "id_a"},
		{"a reference to a definition", `{"$defs":{"Stamp":{"type":"string","format":"date-time"}},"type":"object","properties":{"at":{"$ref":"#/$defs/Stamp"}}}`,
			map[string]any{"at": "yesterday"}, map[string]any{"at": "2026-09-28T00:00:00Z"}, "at"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			v := mustValidator(t, r.schema)
			got := formatFindings(t, v, r.bad, nil)
			if len(got) != 1 {
				t.Fatalf("want one format finding; got %v", got)
			}
			if got[0].FieldPath != r.wantPath {
				t.Errorf("FieldPath = %q; want %q", got[0].FieldPath, r.wantPath)
			}
			if !strings.Contains(got[0].Message, "is not a valid ") {
				t.Errorf("message does not name the format: %q", got[0].Message)
			}
			if rpt, err := v.Validate(r.good, nil); err != nil || len(rpt.Findings) != 0 {
				t.Errorf("a conforming value was refused: %v %v", err, rpt)
			}
		})
	}
}

// TestValidateFormatMessageNamesValueAndFormat pins the exact wording of a value
// refusal, and that a definition the value was checked against is named.
func TestValidateFormatMessageNamesValueAndFormat(t *testing.T) {
	t.Parallel()
	v := mustValidator(t, `{"type":"object","properties":{"endpoint":{"type":"string","format":"uri"}}}`)
	got := formatFindings(t, v, map[string]any{"endpoint": "::::"}, nil)
	want := `value "::::" is not a valid uri: a URI begins with a scheme followed by a colon, such as https:`
	if len(got) != 1 || got[0].Message != want {
		t.Fatalf("got %v; want one finding %q", got, want)
	}
}

// TestValidateFormatPassesAValueThatIsNotAString pins that format judges strings
// only: a value of another type is its type keyword's to judge.
func TestValidateFormatPassesAValueThatIsNotAString(t *testing.T) {
	t.Parallel()
	v := mustValidator(t, `{"type":"object","properties":{"n":{"format":"email"}}}`)
	for _, value := range []any{42.0, true, nil, map[string]any{}, []any{"x"}} {
		if got := formatFindings(t, v, map[string]any{"n": value}, nil); len(got) != 0 {
			t.Errorf("value %v: want no format finding; got %v", value, got)
		}
	}
}

// TestFormatTakesPartInCombinatorMatching pins that a format failure inside a
// branch is a reason the branch does not match, as any other failure is.
func TestFormatTakesPartInCombinatorMatching(t *testing.T) {
	t.Parallel()
	oneOf := mustValidator(t, `{"oneOf":[{"type":"string","format":"email"},{"type":"string","format":"ipv4"}]}`)
	for value, wantRefused := range map[string]bool{"a@example.com": false, "192.0.2.1": false, "::::": true} {
		rpt, err := oneOf.Validate(value, nil)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if refused := len(rpt.Findings) > 0; refused != wantRefused {
			t.Errorf("oneOf over %q: refused=%v; want %v (%v)", value, refused, wantRefused, rpt.Findings)
		}
	}
	not := mustValidator(t, `{"type":"string","not":{"format":"ipv4"}}`)
	for value, wantRefused := range map[string]bool{"192.0.2.1": true, "db.local": false} {
		rpt, err := not.Validate(value, nil)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if refused := len(rpt.Findings) > 0; refused != wantRefused {
			t.Errorf("not over %q: refused=%v; want %v (%v)", value, refused, wantRefused, rpt.Findings)
		}
	}
}

// TestFormatIsCheckedWhereTheLibraryEvaluatesAWholeSubtree drives the regions the
// walk hands whole to the library, where the sweep checks format, and pins one row
// of the sweep's stated bound.
func TestFormatIsCheckedWhereTheLibraryEvaluatesAWholeSubtree(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name        string
		schema      string
		instance    any
		ctx         *Context
		wantRefused []string // the FieldPaths of the expected format findings
	}{
		{
			name: "the definition a reference names, beside a constraint of its own",
			schema: `{"$defs":{"Url":{"type":"string","format":"uri"}},"type":"object",` +
				`"properties":{"endpoint":{"$ref":"#/$defs/Url","minLength":1}}}`,
			instance:    map[string]any{"endpoint": "::::"},
			wantRefused: []string{"endpoint"},
		},
		{
			name: "a pointer into a declared property",
			schema: `{"type":"object","properties":{` +
				`"primary":{"type":"object","properties":{"url":{"type":"string","format":"uri"}}},` +
				`"secondary":{"$ref":"#/properties/primary"}}}`,
			instance: map[string]any{
				"primary":   map[string]any{"url": "https://example.com"},
				"secondary": map[string]any{"url": "::::"},
			},
			wantRefused: []string{"secondary.url"},
		},
		{
			name: "the then: branch of a when-gated node whose predicate holds",
			schema: `{"type":"object","properties":{"on":{"type":"boolean"},"tls":{"type":"object",` +
				`"x-aggrega-when":"{{ eq $.on true }}","if":true,` +
				`"then":{"properties":{"ca":{"type":"string","format":"uri"}}}}}}`,
			instance:    map[string]any{"on": true, "tls": map[string]any{"ca": "::::"}},
			wantRefused: []string{"tls.ca"},
		},
		{
			name: "the then: branch of a when-gated node whose predicate does not hold",
			schema: `{"type":"object","properties":{"on":{"type":"boolean"},"tls":{"type":"object",` +
				`"x-aggrega-when":"{{ eq $.on true }}","if":true,` +
				`"then":{"properties":{"ca":{"type":"string","format":"uri"}}}}}}`,
			instance:    map[string]any{"on": false, "tls": map[string]any{"ca": "::::"}},
			wantRefused: nil,
		},
		{
			name: "the else: branch of a when-gated node whose predicate does not hold",
			schema: `{"type":"object","properties":{"on":{"type":"boolean"},"tls":{"type":"object",` +
				`"x-aggrega-when":"{{ eq $.on true }}","if":true,"then":{},` +
				`"else":{"properties":{"ca":{"type":"string","format":"uri"}}}}}}`,
			instance:    map[string]any{"on": false, "tls": map[string]any{"ca": "::::"}},
			wantRefused: []string{"tls.ca"},
		},
		{
			name: "coverage bound: a oneOf inside a region the library evaluates whole",
			schema: `{"$defs":{"Either":{"oneOf":[{"type":"string","format":"uri"},{"type":"integer"}]}},` +
				`"type":"object","properties":{"v":{"$ref":"#/$defs/Either","minLength":1}}}`,
			instance:    map[string]any{"v": "::::"},
			wantRefused: nil,
		},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			v := mustValidator(t, r.schema)
			got := formatFindings(t, v, r.instance, r.ctx)
			var paths []string
			for _, f := range got {
				paths = append(paths, f.FieldPath)
			}
			if strings.Join(paths, ",") != strings.Join(r.wantRefused, ",") {
				t.Errorf("format findings at %v; want %v (%v)", paths, r.wantRefused, got)
			}
		})
	}
}

// TestRefuseUnsupportedAuthoredFormatsPassesOverContributedPositions pins the
// narrowing the SchemaLibrary load reads: a position a cross-document reference
// contributed is judged where it is authored, while the same name authored in the
// document itself is refused.
func TestRefuseUnsupportedAuthoredFormatsPassesOverContributedPositions(t *testing.T) {
	t.Parallel()
	contributed := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"secret": map[string]any{"type": "string", "format": "password", librarySourceKey: "Other#/schemas/Secret"},
		},
	}
	if err := RefuseUnsupportedAuthoredFormats(contributed); err != nil {
		t.Errorf("a contributed position was judged here: %v", err)
	}
	authored := map[string]any{
		"type":       "object",
		"properties": map[string]any{"secret": map[string]any{"type": "string", "format": "password"}},
	}
	err := RefuseUnsupportedAuthoredFormats(authored)
	if !errors.Is(err, ErrSchemaResolve()) || !strings.Contains(err.Error(), `format "password" is not supported`) {
		t.Errorf("an authored unsupported name was not refused: %v", err)
	}
	if _, err := New(mustSchemaFromMap(t, contributed)); err == nil {
		t.Errorf("New passed over a contributed position; it judges the whole schema it is handed")
	}
}

func mustSchemaFromMap(t *testing.T, m map[string]any) *libschema.Schema {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := &libschema.Schema{}
	if err := json.Unmarshal(raw, s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return s
}

// TestSubSchemasVisitsEverySubSchemaField holds the name refusal's traversal to
// the schema type: every field whose type can hold a sub-schema must be read, so
// a keyword the library adds cannot carry a name nothing judges.
func TestSubSchemasVisitsEverySubSchemaField(t *testing.T) {
	t.Parallel()
	leaf := func() *libschema.Schema { return &libschema.Schema{Format: "password"} }
	s := &libschema.Schema{
		Defs:                  map[string]*libschema.Schema{"a": leaf()},
		Definitions:           map[string]*libschema.Schema{"b": leaf()},
		DependencySchemas:     map[string]*libschema.Schema{"c": leaf()},
		PrefixItems:           []*libschema.Schema{leaf()},
		Items:                 leaf(),
		AdditionalItems:       leaf(),
		Contains:              leaf(),
		UnevaluatedItems:      leaf(),
		Properties:            map[string]*libschema.Schema{"d": leaf()},
		PatternProperties:     map[string]*libschema.Schema{"e": leaf()},
		AdditionalProperties:  leaf(),
		PropertyNames:         leaf(),
		UnevaluatedProperties: leaf(),
		AllOf:                 []*libschema.Schema{leaf()},
		AnyOf:                 []*libschema.Schema{leaf()},
		OneOf:                 []*libschema.Schema{leaf()},
		Not:                   leaf(),
		If:                    leaf(),
		Then:                  leaf(),
		Else:                  leaf(),
		DependentSchemas:      map[string]*libschema.Schema{"f": leaf()},
		ContentSchema:         leaf(),
	}
	got := map[string]bool{}
	for _, child := range subSchemas(s) {
		got[strings.SplitN(child.segment, "/", 2)[0]] = true
	}
	for _, keyword := range []string{"$defs", "definitions", "dependencies", "prefixItems", "items",
		"additionalItems", "contains", "unevaluatedItems", "properties", "patternProperties",
		"additionalProperties", "propertyNames", "unevaluatedProperties", "allOf", "anyOf", "oneOf",
		"not", "if", "then", "else", "dependentSchemas", "contentSchema"} {
		if !got[keyword] {
			t.Errorf("subSchemas does not visit %s", keyword)
		}
	}
	s2 := &libschema.Schema{ItemsArray: []*libschema.Schema{leaf()}}
	if children := subSchemas(s2); len(children) != 1 || children[0].segment != "items/0" {
		t.Errorf("subSchemas does not visit the tuple form of items: %v", children)
	}
}
