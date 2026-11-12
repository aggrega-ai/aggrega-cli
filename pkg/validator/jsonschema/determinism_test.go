// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"

	libschema "github.com/google/jsonschema-go/jsonschema"
)

// siblingViolationSchema mirrors the shape that made the diagnostic vary
// between runs: an object whose entries are described by a reusable
// definition, that definition nesting a second object whose two entries
// are described by the same third definition, and an instance violating
// both entries identically.
//
// Both violations are equally true, so a check that hands the whole
// subtree to the schema library reports whichever entry the library's
// iteration over a map reached first — one on some runs, the other on
// others, and never both.
const siblingViolationSchema = `{
	"type": "object",
	"properties": {
		"containers": {
			"type": "object",
			"minProperties": 1,
			"additionalProperties": {"$ref": "#/$defs/container"}
		}
	},
	"$defs": {
		"container": {
			"type": "object",
			"required": ["image"],
			"additionalProperties": false,
			"properties": {
				"image": {"type": "string"},
				"resources": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"limits": {"$ref": "#/$defs/resourceList"},
						"requests": {"$ref": "#/$defs/resourceList"}
					}
				}
			}
		},
		"resourceList": {
			"type": "object",
			"additionalProperties": false,
			"properties": {
				"cpu": {"type": "string"},
				"memory": {"type": "string"}
			}
		}
	}
}`

func siblingViolationInstance() map[string]any {
	return map[string]any{
		"containers": map[string]any{
			"main": map[string]any{
				"image": "example:1",
				"resources": map[string]any{
					"limits":   map[string]any{"cpu": 1},
					"requests": map[string]any{"cpu": 1},
				},
			},
		},
	}
}

// TestEquallyFailingSiblingsAreBothReportedAtTheirOwnPaths is the
// regression test for the diagnostic that varied between runs.
//
// It asserts the property the fix establishes rather than the text the fix
// happens to produce: each violation is reported at the path of the value
// that violates, both of them are reported, and the report is identical
// across many validations of the same instance.
//
// 🔴 Why it is written over repeated validations rather than one. The
// defect is a map-iteration order, which is randomised per iteration
// rather than per process, so a single validation observes one arbitrary
// order and cannot tell a fixed order from a lucky one. A single-pass
// version of this test passed against the defect.
func TestEquallyFailingSiblingsAreBothReportedAtTheirOwnPaths(t *testing.T) {
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(siblingViolationSchema), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	validator, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const passes = 200

	var first string
	for pass := 0; pass < passes; pass++ {
		report, err := validator.Validate(siblingViolationInstance(), nil)
		if err != nil {
			t.Fatalf("pass %d: Validate: %v", pass, err)
		}

		rendered := renderFindings(report)
		if pass == 0 {
			first = rendered
			continue
		}
		if rendered != first {
			t.Fatalf("the report varies between validations of the same instance.\n"+
				"first pass:\n%s\npass %d:\n%s", first, pass, rendered)
		}
	}

	// Both violations present, each at the path of the value that violates.
	for _, wantPath := range []string{
		"containers.main.resources.limits.cpu",
		"containers.main.resources.requests.cpu",
	} {
		if !strings.Contains(first, wantPath+"\t") {
			t.Errorf("no finding at %s; a report naming one of two equally-failing values hides the other.\ngot:\n%s",
				wantPath, first)
		}
	}

	// Paths in ascending order, which is what makes the report reproducible
	// rather than merely stable within one process.
	lines := strings.Split(strings.TrimSuffix(first, "\n"), "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i-1] > lines[i] {
			t.Errorf("findings are not in ascending order at line %d:\n%s", i, first)
		}
	}
}

// TestNoWholeSubtreeCheckReportsAChildViolation pins the mechanism the
// test above depends on: the check performed at a position reports only
// what that position constrains, so the walk's own descent is the single
// reporter of a child's finding. Without this, a child violation is
// reported twice — once at the child and once, non-deterministically
// chosen, at each ancestor.
func TestNoWholeSubtreeCheckReportsAChildViolation(t *testing.T) {
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(siblingViolationSchema), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	validator, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	report, err := validator.Validate(siblingViolationInstance(), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for _, finding := range report.Findings {
		switch finding.FieldPath {
		case "containers.main.resources.limits.cpu", "containers.main.resources.requests.cpu":
			continue
		default:
			t.Errorf("a finding at %s repeats a child's violation at an ancestor position, which is where the arbitrary choice between equally-failing children was made: %s",
				finding.FieldPath, finding.Message)
		}
	}
	if len(report.Findings) != 2 {
		t.Errorf("expected exactly the two child violations; got %d:\n%s", len(report.Findings), renderFindings(report))
	}
}

// TestPositionOwnConstraintsStillReported checks the other side of the
// same mechanism: reducing a position's check to its own constraints must
// not stop those constraints from being reported. Closedness and the
// property-count bound are declared at the position, so they belong to it.
func TestPositionOwnConstraintsStillReported(t *testing.T) {
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(siblingViolationSchema), schema); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	validator, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("an empty entry map violates the count bound", func(t *testing.T) {
		report, err := validator.Validate(map[string]any{"containers": map[string]any{}}, nil)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(report.Findings) == 0 {
			t.Error("minProperties is declared at the containers position and must still be reported")
		}
	})

	t.Run("an undeclared key violates closedness", func(t *testing.T) {
		report, err := validator.Validate(map[string]any{
			"containers": map[string]any{
				"main": map[string]any{"image": "example:1", "unknown": "x"},
			},
		}, nil)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(report.Findings) == 0 {
			t.Error("additionalProperties: false is declared on the container definition and must still be reported")
		}
	})

	t.Run("a missing required key is reported", func(t *testing.T) {
		report, err := validator.Validate(map[string]any{
			"containers": map[string]any{"main": map[string]any{}},
		}, nil)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(report.Findings) == 0 {
			t.Error("required is declared on the container definition and must still be reported")
		}
	})
}

// arrayElementSchema mirrors the maintainer-list shape the entity schemas
// declare: an array whose elements are a closed object with declared
// string properties. An element carrying two wrongly-typed fields makes
// two violations equally true at the same element.
const arrayElementSchema = `{
	"type": "object",
	"properties": {
		"maintainers": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"name":  {"type": "string"},
					"email": {"type": "string"},
					"url":   {"type": "string"}
				},
				"required": ["name"],
				"additionalProperties": false
			}
		}
	}
}`

// TestEveryViolationInsideAnArrayElementIsReportedAtItsOwnPath is the
// regression test for the diagnostic that named one of an array element's
// two failing fields and varied which one between runs.
//
// 🔴 Written over repeated validations rather than one, and the reason is
// the same one that makes a single-pass determinism test worthless: the
// selection being asserted is a map iteration, which this language
// randomises per range rather than per process. A single pass observes one
// arbitrary selection and cannot tell a fixed one from a lucky one.
func TestEveryViolationInsideAnArrayElementIsReportedAtItsOwnPath(t *testing.T) {
	validator := mustValidator(t, arrayElementSchema)
	instance := func() map[string]any {
		return map[string]any{
			"maintainers": []any{
				map[string]any{"name": 111, "email": 222},
			},
		}
	}

	first := requireStableAcrossPasses(t, validator, instance, 200)

	for _, wantPath := range []string{
		"maintainers[0].name",
		"maintainers[0].email",
	} {
		if !strings.Contains(first, wantPath+"\t") {
			t.Errorf("no finding at %s; a report naming one of an element's two equally-failing fields hides the other.\ngot:\n%s",
				wantPath, first)
		}
	}

	// The schema library's own pointer chain describes where a constraint
	// sits in the schema document, which is not something the author of the
	// validated document wrote or can act on.
	if strings.Contains(first, "validating ") || strings.Contains(first, "/items") {
		t.Errorf("a diagnostic carries the schema library's internal pointer chain:\n%s", first)
	}
}

// TestEveryElementOfAnArrayIsReportedNotOnlyTheFirst pins the other half
// of array coverage: a library walks elements by index and stops at the
// first failure, so two failing elements produced one finding naming
// element zero and nothing about element one.
func TestEveryElementOfAnArrayIsReportedNotOnlyTheFirst(t *testing.T) {
	validator := mustValidator(t, arrayElementSchema)
	instance := func() map[string]any {
		return map[string]any{
			"maintainers": []any{
				map[string]any{"name": 1},
				map[string]any{"name": 2},
			},
		}
	}

	first := requireStableAcrossPasses(t, validator, instance, 200)
	for _, wantPath := range []string{"maintainers[0].name", "maintainers[1].name"} {
		if !strings.Contains(first, wantPath+"\t") {
			t.Errorf("no finding at %s; a report stopping at the first failing element hides the rest.\ngot:\n%s",
				wantPath, first)
		}
	}
}

// TestEachUndeclaredKeyIsNamedOnceAtItsOwnPath pins the closed-object
// diagnostic on every property it has to hold at once, because each of them
// was got wrong separately.
//
// A refused key is its own mistake, so two of them are two findings — not
// six, which is what naming each key twice in two wordings produced, and not
// one, which loses the position. The path is the refused key's own, because
// that path is what a surface resolves back into a line and column: pointed
// at the enclosing object, every closedness diagnostic moves off the key the
// author typed and onto the block containing it. And the keys are visited in
// sorted order, so the two findings are produced in the same order on every
// run.
//
// 🔴 Written over repeated validations rather than one: the selection under
// test is a map iteration, which this language randomises per range rather
// than per process, so a single pass cannot tell a fixed order from a lucky
// one.
func TestEachUndeclaredKeyIsNamedOnceAtItsOwnPath(t *testing.T) {
	validator := mustValidator(t, `{
		"type": "object",
		"properties": {
			"spec": {
				"type": "object",
				"additionalProperties": false,
				"properties": {"declared": {"type": "string"}}
			}
		}
	}`)
	instance := func() map[string]any {
		return map[string]any{
			"spec": map[string]any{"declared": "ok", "ZETA": 1, "ALPHA": 2},
		}
	}

	first := requireStableAcrossPasses(t, validator, instance, 200)

	want := "spec.ALPHA\tadditionalProperties\tunexpected property: \"ALPHA\"\n" +
		"spec.ZETA\tadditionalProperties\tunexpected property: \"ZETA\"\n"
	if first != want {
		t.Errorf("the closed-object report is not one finding per refused key at its own path, in order.\nwant:\n%s\ngot:\n%s",
			want, first)
	}

	// The regression this guards in the direction that produced it: a finding
	// at the enclosing object's path resolves to the block rather than to the
	// key, so no surface can point a reader at what they typed.
	for _, line := range strings.Split(strings.TrimSuffix(first, "\n"), "\n") {
		if strings.HasPrefix(line, "spec\t") {
			t.Errorf("a refused key is reported at the enclosing object's path, which no surface can resolve to the key:\n%s", first)
		}
	}
	if strings.Contains(first, "inverse schema") {
		t.Errorf("the diagnostic describes schema machinery instead of the author's mistake:\n%s", first)
	}
}

// TestSelectionIsStableForEveryKeywordTheWalkModels is the breadth half of
// the determinism proof: the cases above pin the shapes that were measured
// varying, and this pins that no keyword the walk now models reintroduces a
// selection that varies.
//
// Determinism here is measured, not reasoned: each case is validated many
// times and the rendered report compared byte-for-byte.
func TestSelectionIsStableForEveryKeywordTheWalkModels(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		instance string
	}{
		{
			name:     "two keys both violating propertyNames",
			schema:   `{"type":"object","properties":{"spec":{"type":"object","propertyNames":{"pattern":"^[a-z]+$"}}}}`,
			instance: `{"spec":{"ZETA":1,"ALPHA":2}}`,
		},
		{
			name:     "two properties of one conditional branch violating together",
			schema:   `{"type":"object","properties":{"spec":{"type":"object","if":{"properties":{"kind":{"const":"x"}},"required":["kind"]},"then":{"properties":{"a":{"type":"string"},"b":{"type":"string"}}}}}}`,
			instance: `{"spec":{"kind":"x","a":1,"b":2}}`,
		},
		{
			name:     "two elements of a positional tuple violating together",
			schema:   `{"type":"object","properties":{"pair":{"type":"array","prefixItems":[{"type":"string"},{"type":"string"}]}}}`,
			instance: `{"pair":[1,2]}`,
		},
		{
			name:     "an array whose elements each carry two violations",
			schema:   `{"type":"object","properties":{"rows":{"type":"array","items":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}}}}`,
			instance: `{"rows":[{"a":1,"b":2},{"a":3,"b":4}]}`,
		},
		{
			name:     "no element satisfies contains",
			schema:   `{"type":"object","properties":{"rows":{"type":"array","contains":{"type":"string"}}}}`,
			instance: `{"rows":[1,2]}`,
		},
		{
			name:     "an object below its property-count bound",
			schema:   `{"type":"object","properties":{"spec":{"type":"object","minProperties":2}}}`,
			instance: `{"spec":{"a":1}}`,
		},
		{
			name:     "keys reaching no declared property under a closed tail",
			schema:   `{"type":"object","properties":{"spec":{"type":"object","properties":{"a":{"type":"string"}},"unevaluatedProperties":false}}}`,
			instance: `{"spec":{"a":"ok","ZETA":1,"ALPHA":2}}`,
		},
		{
			name:     "an object above its key-count ceiling",
			schema:   `{"type":"object","properties":{"spec":{"type":"object","maxProperties":1}}}`,
			instance: `{"spec":{"a":1,"b":2}}`,
		},
		{
			name:     "too few elements match the required shape",
			schema:   `{"type":"object","properties":{"rows":{"type":"array","contains":{"type":"string"},"minContains":2}}}`,
			instance: `{"rows":["a",1]}`,
		},
		{
			name:     "too many elements match the restricted shape",
			schema:   `{"type":"object","properties":{"rows":{"type":"array","contains":{"type":"string"},"maxContains":1}}}`,
			instance: `{"rows":["a","b"]}`,
		},
		{
			name:     "elements past the declared positions",
			schema:   `{"type":"object","properties":{"pair":{"type":"array","prefixItems":[{"type":"string"}],"unevaluatedItems":false}}}`,
			instance: `{"pair":["a","spill","over"]}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			validator := mustValidator(t, c.schema)
			var decoded map[string]any
			if err := json.Unmarshal([]byte(c.instance), &decoded); err != nil {
				t.Fatalf("instance unmarshal: %v", err)
			}
			rendered := requireStableAcrossPasses(t, validator, func() map[string]any { return decoded }, 200)
			if rendered == "" {
				t.Fatalf("the case reports nothing, so it proves nothing about selection stability")
			}
			if strings.Contains(rendered, "validating ") {
				t.Errorf("a diagnostic carries the schema library's internal pointer chain:\n%s", rendered)
			}
			if strings.Contains(rendered, "anonymous schema") || strings.Contains(rendered, "%!s(") {
				t.Errorf("a diagnostic carries an artifact of how the value is held rather than what the author wrote:\n%s", rendered)
			}
		})
	}
}

// TestTheDeclaredOrderingIsTheOrderingProduced pins the order this package
// documents, including the two consequences of comparing a path as text.
//
// Both are pinned because both are the kind of thing a later reader meets as
// a surprise and reads as a defect. An ordering nobody wrote down gets
// "corrected" by whoever notices it, and a golden moves; an ordering written
// down and pinned is a contract someone has to argue with first.
func TestTheDeclaredOrderingIsTheOrderingProduced(t *testing.T) {
	t.Run("an array index orders as text, so ten precedes two", func(t *testing.T) {
		validator := mustValidator(t, `{
			"type": "object",
			"properties": {"rows": {"type": "array", "items": {"type": "string"}}}
		}`)
		elements := make([]any, 11)
		for i := range elements {
			elements[i] = i
		}
		report, err := validator.Validate(map[string]any{"rows": elements}, nil)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		var order []string
		for _, finding := range report.Findings {
			order = append(order, finding.FieldPath)
		}
		if len(order) != 11 {
			t.Fatalf("every failing element must be reported; got %d findings: %v", len(order), order)
		}
		if order[0] != "rows[0]" || order[1] != "rows[10]" || order[2] != "rows[1]" {
			t.Errorf("the documented text ordering puts rows[10] between rows[0] and rows[1]; got %v", order)
		}
	})

	t.Run("a name below the path separator sorts ahead of a deeper path", func(t *testing.T) {
		// "-" is below "." in the character order, so "a-b" precedes "a.c" —
		// which reads as a shallow sibling jumping ahead of a nested value.
		validator := mustValidator(t, `{
			"type": "object",
			"properties": {
				"a":   {"type": "object", "properties": {"c": {"type": "string"}}},
				"a-b": {"type": "string"}
			}
		}`)
		report, err := validator.Validate(map[string]any{
			"a":   map[string]any{"c": 1},
			"a-b": 2,
		}, nil)
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		var order []string
		for _, finding := range report.Findings {
			order = append(order, finding.FieldPath)
		}
		want := []string{"a-b", "a.c"}
		if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
			t.Errorf("documented ordering is %v; got %v", want, order)
		}
	})
}

// TestTheLeftoverKeywordIsNotEnforcedWhereItsSetIsNotLocal pins the stated
// exclusion, in the direction that matters.
//
// The exclusion exists because the reached set spans an applicator the
// position cannot see, and computing it anyway would refuse a value that
// applicator accepted. So the property under test is not "a finding is
// produced" but its opposite: a document the schema in fact permits must not
// be rejected. An enforcement that invents a failure is worse than one that
// misses a real one, and only this direction distinguishes them.
func TestTheLeftoverKeywordIsNotEnforcedWhereItsSetIsNotLocal(t *testing.T) {
	// The combinator branch declares "extra", so "extra" IS reached and the
	// document satisfies the schema as written. A position-local reading of
	// the reached set would not see the branch and would refuse it.
	validator := mustValidator(t, `{
		"type": "object",
		"properties": {"declared": {"type": "string"}},
		"allOf": [{"properties": {"extra": {"type": "string"}}}],
		"unevaluatedProperties": false
	}`)

	report, err := validator.Validate(map[string]any{"declared": "ok", "extra": "ok"}, nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("a document the schema permits was rejected; the leftover keyword was read against a set "+
			"that omits what the combinator branch reached: %s", renderFindings(report))
	}
}

// TestAKeywordFamilyIsModelledWholeOrNotAtAll pins the guard that keeps a
// half-built family from being pruned away.
//
// A branch can fail to build, and marking the family walked on the strength
// of the branches that did build has two silent consequences: a positional
// list assembled from the survivors slides them down and checks one element
// against another's constraint, and the reduced copy of the position drops
// the whole family — leaving whichever branch failed checked by nothing.
//
// Asserted on the index rather than through a validation, because the
// property is about what the index claims: an unresolvable reference
// currently takes its whole enclosing position out of the index, so a
// validation cannot distinguish this guard working from that larger gap.
func TestAKeywordFamilyIsModelledWholeOrNotAtAll(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{
			name:   "a uniform element constraint that does not build",
			schema: `{"type":"array","prefixItems":[{"type":"string"}],"items":{"$ref":"#/nowhere"}}`,
		},
		{
			name:   "one positional element constraint of several that does not build",
			schema: `{"type":"array","prefixItems":[{"type":"string"},{"$ref":"#/nowhere"}]}`,
		},
		{
			name:   "a conditional selector that does not build",
			schema: `{"type":"object","if":{"$ref":"#/nowhere"},"then":{"required":["a"]}}`,
		},
		{
			name:   "a conditional branch that does not build",
			schema: `{"type":"object","if":{"type":"object"},"then":{"$ref":"#/nowhere"}}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := &libschema.Schema{}
			if err := json.Unmarshal([]byte(c.schema), schema); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			indexer := &googleAdapter{expanding: map[string]bool{}}
			indexed := indexer.buildPositionKeywords(schema, nil, "")
			if indexed == nil {
				// Nothing claimed, so the reduced copy keeps every keyword and
				// the library goes on checking them. This is the outcome.
				return
			}
			if len(indexed.prefixItems) > 0 || indexed.items != nil {
				t.Errorf("the element family was claimed with a branch missing, so the reduced copy drops it " +
					"and the missing branch's constraint is checked by nothing")
			}
			if indexed.conditional != nil {
				t.Errorf("the conditional was claimed with a branch missing, so the reduced copy drops it " +
					"and the missing branch's constraint is checked by nothing")
			}
		})
	}
}

// requireStableAcrossPasses validates the same instance many times and
// fails on the first pass whose rendered report differs, returning the
// stable rendering for further assertions.
func requireStableAcrossPasses(t *testing.T, validator Validator, instance func() map[string]any, passes int) string {
	t.Helper()
	var first string
	for pass := 0; pass < passes; pass++ {
		report, err := validator.Validate(instance(), nil)
		if err != nil {
			t.Fatalf("pass %d: Validate: %v", pass, err)
		}
		rendered := renderFindings(report)
		if pass == 0 {
			first = rendered
			continue
		}
		if rendered != first {
			t.Fatalf("the report varies between validations of the same instance.\nfirst pass:\n%s\npass %d:\n%s",
				first, pass, rendered)
		}
	}
	return first
}

func mustValidator(t *testing.T, schemaJSON string) Validator {
	t.Helper()
	schema := &libschema.Schema{}
	if err := json.Unmarshal([]byte(schemaJSON), schema); err != nil {
		t.Fatalf("schema unmarshal: %v", err)
	}
	validator, err := New(schema)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return validator
}

// renderFindings formats a report as one tab-separated line per finding,
// so a comparison between two reports names the position that moved.
func renderFindings(report *Report) string {
	var out strings.Builder
	for _, finding := range report.Findings {
		out.WriteString(finding.FieldPath)
		out.WriteString("\t")
		out.WriteString(finding.Keyword)
		out.WriteString("\t")
		out.WriteString(finding.Message)
		out.WriteString("\n")
	}
	return out.String()
}
