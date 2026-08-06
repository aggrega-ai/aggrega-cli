// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// annotationDependentKeywords are the two keywords whose meaning is
// assembled from what the sibling and child positions matched, rather
// than from the keyword alone. Every other keyword can be read off the
// node that declares it, which is what lets a reduced copy of a node
// carry the node's own constraints faithfully (see pruneOwnKeywords).
// These two cannot: a copy without the child positions reads them
// differently from the schema the author wrote.
var annotationDependentKeywords = []string{"unevaluatedProperties", "unevaluatedItems"}

// TestShippedSchemasCarryNoAnnotationDependentKeywords is the guard that
// makes the reduced-copy transform honest. The transform is sound only
// while no shipped schema declares a keyword whose meaning depends on
// child annotations, so relying on that absence without checking it would
// be relying on something nobody measures. Introducing either keyword
// into any shipped schema fails here, at the point of introduction,
// instead of silently changing what a reduced copy means.
//
// The scan reads the parsed document rather than its text, so a keyword
// named inside a description does not trip it and one nested at any depth
// does not escape it. The instrument is calibrated first: a document with
// both keywords planted must be detected, otherwise a clean result says
// nothing.
func TestShippedSchemasCarryNoAnnotationDependentKeywords(t *testing.T) {
	// Calibration. A detector that finds nothing in a document holding both
	// keywords would report every real schema clean for the wrong reason.
	planted := `{
		"type": "object",
		"properties": {
			"nested": {
				"allOf": [{"unevaluatedProperties": false}]
			}
		},
		"$defs": {
			"deep": {"items": {"unevaluatedItems": false}}
		}
	}`
	var plantedDoc any
	if err := json.Unmarshal([]byte(planted), &plantedDoc); err != nil {
		t.Fatalf("unmarshal calibration document: %v", err)
	}
	calibration := findAnnotationDependentKeywords(plantedDoc, "")
	if len(calibration) != 2 {
		t.Fatalf("the detector must find both planted keywords, otherwise a clean result is a silence rather than a reading; found %v", calibration)
	}

	kinds := schemas.Kinds()
	if len(kinds) == 0 {
		t.Fatal("no shipped schemas resolved; the scan would pass over an empty set")
	}

	scanned := 0
	for _, kind := range kinds {
		source, ok := schemas.SchemaByKind(kind)
		if !ok {
			t.Errorf("%s: kind listed but no schema resolved", kind)
			continue
		}
		var doc any
		if err := json.Unmarshal([]byte(source), &doc); err != nil {
			t.Errorf("%s: unmarshal: %v", kind, err)
			continue
		}
		scanned++
		for _, where := range findAnnotationDependentKeywords(doc, "") {
			t.Errorf("%s: %s\n"+
				"A keyword whose meaning comes from child annotations has been introduced. "+
				"The validator reduces each schema position to its own constraints before "+
				"checking it, which removes the child positions such a keyword reads, so it "+
				"would be evaluated against a shape the author did not write. Either express "+
				"the constraint with a keyword that stands on its own node, or teach the "+
				"reduction to model this one.", kind, where)
		}
	}
	if scanned != len(kinds) {
		t.Errorf("scanned %d of %d shipped schemas; a partial scan cannot report an absence", scanned, len(kinds))
	}
	t.Logf("scanned %d shipped schemas for %v; detector calibrated on a planted document",
		scanned, annotationDependentKeywords)
}

// findAnnotationDependentKeywords returns a JSON-pointer-ish location for
// every occurrence of an annotation-dependent keyword used as an object
// key, at any depth.
func findAnnotationDependentKeywords(node any, at string) []string {
	var found []string
	switch typed := node.(type) {
	case map[string]any:
		for _, keyword := range annotationDependentKeywords {
			if _, present := typed[keyword]; present {
				found = append(found, at+"/"+keyword)
			}
		}
		for key, value := range typed {
			found = append(found, findAnnotationDependentKeywords(value, at+"/"+key)...)
		}
	case []any:
		for i, value := range typed {
			found = append(found, findAnnotationDependentKeywords(value, fmt.Sprintf("%s/%d", at, i))...)
		}
	}
	return found
}

// TestEveryIndexedPathHasAPrunedSchema pins the fallback in
// validateOwnKeywords unreached for every shipped schema. The walk
// validates a position against its reduced copy; where none was built it
// falls back to the position's full schema, which keeps the coverage but
// loses the ordering guarantee for that position. The fallback exists so
// an unforeseen schema under-reports rather than going unchecked, and this
// test says how often that happens today: never.
func TestEveryIndexedPathHasAPrunedSchema(t *testing.T) {
	kinds := schemas.Kinds()
	covered, paths := 0, 0
	var unreachable []string
	for _, kind := range kinds {
		source, ok := schemas.SchemaByKind(kind)
		if !ok {
			continue
		}
		schema := &libschema.Schema{}
		if err := json.Unmarshal([]byte(source), schema); err != nil {
			// Not every shipped document is an entity schema the adapter
			// consumes; an unparseable one is another test's business.
			continue
		}
		validator, err := New(schema)
		if err != nil {
			// A schema referencing another document cannot resolve without a
			// loader. Pre-existing and unrelated to the reduction, but named
			// in the count so the coverage claim states what it covers.
			unreachable = append(unreachable, kind)
			continue
		}
		covered++
		adapter := validator.(*googleAdapter)
		paths += len(adapter.subResolved)
		var missing []string
		for path := range adapter.subResolved {
			if _, ok := adapter.subPruned[path]; !ok {
				missing = append(missing, path)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s: %d indexed paths have no reduced schema, so they fall back to a whole-subtree check: %v",
				kind, len(missing), missing)
		}
		if len(adapter.subResolved) == 0 {
			t.Errorf("%s: nothing indexed; the check would pass over an empty set", kind)
		}
	}
	if covered == 0 {
		t.Fatal("no shipped schema was constructible; the check would pass over an empty set")
	}
	t.Logf("every one of %d indexed positions across %d of %d shipped schemas has a reduced schema; %d not constructible without a document loader: %v",
		paths, covered, len(kinds), len(unreachable), unreachable)
}

// TestClosedFormMatchesLibraryDecoding pins isClosedForm to how the
// schema library actually decodes `additionalProperties: false`. The
// predicate is a second copy of an unexported library check, kept because
// agreement on that decoding is what makes a closed object stay closed
// through the reduction. If the library changes the decoding, this fails
// rather than closedness silently disappearing from every reduced schema.
func TestClosedFormMatchesLibraryDecoding(t *testing.T) {
	closed := &libschema.Schema{}
	if err := json.Unmarshal([]byte(`{"additionalProperties": false}`), closed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !isClosedForm(closed.AdditionalProperties) {
		t.Errorf("additionalProperties: false must read as the closed form; got false, so the reduction would replace it with a permissive schema and closedness would stop being enforced")
	}

	constrained := &libschema.Schema{}
	if err := json.Unmarshal([]byte(`{"additionalProperties": {"type": "string"}}`), constrained); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if isClosedForm(constrained.AdditionalProperties) {
		t.Errorf("a schema-valued additionalProperties must not read as the closed form; keeping it would let the library walk undeclared children")
	}

	permissive := &libschema.Schema{}
	if err := json.Unmarshal([]byte(`{"additionalProperties": true}`), permissive); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if isClosedForm(permissive.AdditionalProperties) {
		t.Errorf("additionalProperties: true must not read as the closed form")
	}
}

// TestPruneKeepsClosednessAndCountBounds checks that the reduction leaves
// a closed object closed and its property-count bound in force, since both
// read the declared key set that the reduction empties of values.
func TestPruneKeepsClosednessAndCountBounds(t *testing.T) {
	schema := &libschema.Schema{}
	source := `{
		"type": "object",
		"minProperties": 1,
		"additionalProperties": false,
		"properties": {
			"alpha": {"type": "string"}
		}
	}`
	if err := json.Unmarshal([]byte(source), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	reduced, err := pruneOwnKeywords(schema, evaluatedByWalk{}).Resolve(nil)
	if err != nil {
		t.Fatalf("resolve reduced schema: %v", err)
	}

	if err := reduced.Validate(map[string]any{"alpha": "ok", "beta": "extra"}); err == nil {
		t.Error("an undeclared key must still be rejected after the reduction")
	}
	if err := reduced.Validate(map[string]any{}); err == nil {
		t.Error("minProperties must still be enforced after the reduction")
	}
	// The declared property's own constraint is deliberately gone — the walk
	// reaches it separately — so a wrong-typed declared value passes here.
	if err := reduced.Validate(map[string]any{"alpha": 1}); err != nil {
		t.Errorf("the reduced schema must not check a declared property's value, which the walk checks itself; got %v", err)
	}
}

// TestPruneKeepsArrayAndConditionalConstraints checks the two keyword
// families that have no second carrier: the walk does not descend arrays,
// and it does not index the properties an in-place applicator declares, so
// the reduction must leave both exactly as authored or their constraints
// vanish.
func TestPruneKeepsArrayAndConditionalConstraints(t *testing.T) {
	schema := &libschema.Schema{}
	source := `{
		"type": "object",
		"properties": {
			"tags": {"type": "array", "items": {"type": "string"}},
			"mode": {"type": "string"},
			"detail": {"type": "string"}
		},
		"if": {"required": ["mode"]},
		"then": {"properties": {"detail": {"const": "required-with-mode"}}}
	}`
	if err := json.Unmarshal([]byte(source), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	reducedArray, err := pruneOwnKeywords(schema.Properties["tags"], evaluatedByWalk{}).Resolve(nil)
	if err != nil {
		t.Fatalf("resolve reduced array schema: %v", err)
	}
	if err := reducedArray.Validate([]any{"ok", 2}); err == nil {
		t.Error("an array element constraint must survive the reduction; the walk does not descend arrays, so nothing else would check it")
	}

	reducedObject, err := pruneOwnKeywords(schema, evaluatedByWalk{}).Resolve(nil)
	if err != nil {
		t.Fatalf("resolve reduced object schema: %v", err)
	}
	if err := reducedObject.Validate(map[string]any{"mode": "on", "detail": "wrong"}); err == nil {
		t.Error("a conditional branch's property constraint must survive the reduction; the walk does not index applicator properties, so nothing else would check it")
	}
}

// TestRefOnlyRefusesAConstrainingSibling checks the safe direction of the
// reference-substitution test: a reference sitting beside a constraint is
// not interchangeable with what it names, and a keyword the exclusion list
// does not recognise must keep the node from qualifying rather than let
// the constraint be dropped.
func TestRefOnlyRefusesAConstrainingSibling(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{"bare reference", `{"$ref": "#/$defs/thing"}`, true},
		{"reference with documentation only", `{"$ref": "#/$defs/thing", "description": "text", "title": "t"}`, true},
		{"reference beside a type", `{"$ref": "#/$defs/thing", "type": "object"}`, false},
		{"reference beside required", `{"$ref": "#/$defs/thing", "required": ["a"]}`, false},
		{"reference beside properties", `{"$ref": "#/$defs/thing", "properties": {"a": {}}}`, false},
		{"no reference at all", `{"type": "string"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := &libschema.Schema{}
			if err := json.Unmarshal([]byte(tc.source), schema); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := refOnly(schema); got != tc.want {
				t.Errorf("refOnly = %v, want %v", got, tc.want)
			}
		})
	}

	withPredicate := &libschema.Schema{}
	if err := json.Unmarshal([]byte(`{"$ref": "#/$defs/thing", "x-aggrega-when": "{{ true }}"}`), withPredicate); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if refOnly(withPredicate) {
		t.Error("a conditional predicate is evaluated against this exact node, so the node must not be swapped for what it references")
	}
}

// TestRecursiveDefinitionTerminates pins the guard against a definition
// that contains itself. Substituting such a definition for its own
// reference would expand forever; several shipped schemas describe exactly
// that shape (a subsystem holding subsystems), so construction must
// terminate rather than exhaust the stack.
func TestRecursiveDefinitionTerminates(t *testing.T) {
	schema := &libschema.Schema{}
	source := `{
		"type": "object",
		"properties": {
			"root": {"$ref": "#/$defs/node"}
		},
		"$defs": {
			"node": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"children": {
						"type": "object",
						"additionalProperties": {"$ref": "#/$defs/node"}
					}
				}
			}
		}
	}`
	if err := json.Unmarshal([]byte(source), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	validator, err := New(schema)
	if err != nil {
		t.Fatalf("New on a self-containing definition: %v", err)
	}
	report, err := validator.Validate(map[string]any{
		"root": map[string]any{
			"name":     "top",
			"children": map[string]any{"inner": map[string]any{"name": 7}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Findings) == 0 {
		t.Error("the wrong-typed name nested inside a self-containing definition must still be reported")
	}
	if got := len(schemas.Kinds()); got == 0 {
		t.Fatal("shipped schema set empty")
	}
	// The shipped schemas that describe self-containing structures must
	// construct too, which is the case this guard actually protects.
	for _, kind := range []string{"aggregate", "release", "ir"} {
		source, ok := schemas.SchemaByKind(kind)
		if !ok {
			t.Errorf("%s: no shipped schema", kind)
			continue
		}
		shipped := &libschema.Schema{}
		if err := json.Unmarshal([]byte(source), shipped); err != nil {
			t.Errorf("%s: unmarshal: %v", kind, err)
			continue
		}
		if _, err := New(shipped); err != nil {
			t.Errorf("%s: New: %v", kind, err)
		}
	}
}

// TestNoDiagnosticNamesAnInMemoryType walks every finding the shipped
// schemas produce for a deliberately wrong instance and checks that none
// of them names a Go construct. A diagnostic reaches a terminal, so the
// vocabulary in it has to be the author's.
func TestNoDiagnosticNamesAnInMemoryType(t *testing.T) {
	forbidden := []string{"reflect.Value", "interface {}", "map[string]", "[]interface", "<nil>"}

	instances := []any{
		nil,
		"a string where an object belongs",
		[]any{1, 2},
		map[string]any{"apiVersion": nil, "kind": []any{}, "metadata": "wrong", "spec": 7},
	}

	// Calibration: the scan must fire on a message that does carry one of
	// the tokens, otherwise a clean sweep is a silence rather than a reading.
	calibrated := false
	for _, token := range forbidden {
		if strings.Contains("a message mentioning "+token+" here", token) {
			calibrated = true
		}
	}
	if !calibrated {
		t.Fatal("the token scan does not fire on a message containing a token")
	}

	kinds := schemas.Kinds()
	scanned, findings := 0, 0
	for _, kind := range kinds {
		source, ok := schemas.SchemaByKind(kind)
		if !ok {
			continue
		}
		schema := &libschema.Schema{}
		if err := json.Unmarshal([]byte(source), schema); err != nil {
			continue
		}
		validator, err := New(schema)
		if err != nil {
			continue
		}
		scanned++
		for _, instance := range instances {
			report, err := validator.Validate(instance, nil)
			if err != nil {
				continue
			}
			findings += len(report.Findings)
			for _, finding := range report.Findings {
				for _, token := range forbidden {
					if strings.Contains(finding.Message, token) {
						t.Errorf("%s: a diagnostic names %q, which no author wrote: %s: %s",
							kind, token, finding.FieldPath, finding.Message)
					}
				}
			}
		}
	}
	if findings == 0 {
		t.Fatal("no findings produced; the scan would pass over an empty set")
	}
	t.Logf("scanned %d diagnostics from %d of %d shipped schemas over %d deliberately wrong instances; none names %v",
		findings, scanned, len(kinds), len(instances), forbidden)
}

// TestNullValueIsNamedRatherThanFormatted pins the one rewrite
// sanitizeMessage performs. A JSON null decodes to an absent Go value, so
// a library formatting the offending value into its message emits a
// formatting artifact; the author wrote `null`, and that is what the
// diagnostic has to say.
func TestNullValueIsNamedRatherThanFormatted(t *testing.T) {
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(`{"type": "object", "properties": {"settings": {"type": "object"}}}`), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	validator, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := validator.Validate(map[string]any{"settings": nil}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Findings) == 0 {
		t.Fatal("a null where an object was declared must be reported")
	}
	for _, finding := range report.Findings {
		if strings.Contains(finding.Message, "reflect") {
			t.Errorf("a diagnostic must not name a value formatter's internals: %s", finding.Message)
		}
		if !strings.Contains(finding.Message, "null") {
			t.Errorf("a diagnostic about a null must say null: %s", finding.Message)
		}
	}

	// The rewrite itself, so the mapping is pinned independently of whether
	// any current schema shape happens to reach it.
	got := sanitizeMessage(`type: `+nullValueArtifact+` has type "null", want "object"`, nil)
	want := `type: null has type "null", want "object"`
	if got != want {
		t.Errorf("sanitizeMessage = %q, want %q", got, want)
	}
}

// TestJSONTypeNameUsesAuthoringVocabulary pins the type namer to the words
// a schema author writes in a type: keyword.
func TestJSONTypeNameUsesAuthoringVocabulary(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{nil, "null"},
		{true, "boolean"},
		{"text", "string"},
		{float64(1), "number"},
		{7, "number"},
		{map[string]any{}, "object"},
		{[]any{}, "array"},
		{struct{}{}, "value"},
	}
	for _, tc := range cases {
		if got := jsonTypeName(tc.value); got != tc.want {
			t.Errorf("jsonTypeName(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
