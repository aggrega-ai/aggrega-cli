// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// carrier of a child's finding. Without this, a child violation is
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
