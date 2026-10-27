// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
)

// compilePillar compiles a compact-form pillar body for NewUnderProperty.
func compilePillar(t *testing.T, src string) map[string]any {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	compiled, err := compactform.Compile(&node)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return compiled
}

// cpuValues is a values pillar whose primary cpu is valid and whose secondary
// cpu is the value given.
func cpuValues(secondaryCPU string) map[string]any {
	requests := func(cpu string) map[string]any {
		return map[string]any{"resources": map[string]any{"requests": map[string]any{"cpu": cpu}}}
	}
	return map[string]any{"values": map[string]any{
		"primary":   requests("250m"),
		"secondary": requests(secondaryCPU),
	}}
}

// TestNewUnderProperty_PointerReadsFromThePillar asserts a pointer written in a
// schema carried at a property is read from the root of that schema — the
// pillar it is written in — as a pointer into the definitions already is; that
// a value failing through such a pointer is reported at the field carrying the
// pointer, not at the field the pointer names; and that a pointer naming
// nothing from that root is refused naming the pointer as written.
func TestNewUnderProperty_PointerReadsFromThePillar(t *testing.T) {
	t.Parallel()

	const declared = `primary:
  resources:
    requests:
      cpu: { type: string, pattern: "^[0-9]+(m|\\.[0-9]+)?$" }
secondary:
  resources: { $ref: "%s" }
`
	pillar := func(pointer string) map[string]any {
		return compilePillar(t, strings.Replace(declared, "%s", pointer, 1))
	}

	v, err := NewUnderProperty("values", pillar("#/properties/primary/properties/resources"), nil)
	if err != nil {
		t.Fatalf("a pointer into a property of the pillar was refused: %v", err)
	}

	report, err := v.Validate(cpuValues("500m"), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("a conforming value was refused: %v", report.Findings)
	}

	report, err = v.Validate(cpuValues("lots"), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("expected one finding, got %d: %v", len(report.Findings), report.Findings)
	}
	if got := report.Findings[0].FieldPath; got != "values.secondary.resources" {
		t.Errorf("finding named %q, want the field carrying the pointer, %q", got, "values.secondary.resources")
	}
	if !strings.Contains(report.Findings[0].Message, `"lots"`) {
		t.Errorf("finding does not report the failing value: %q", report.Findings[0].Message)
	}

	// A pointer read from any other root names nothing in the pillar.
	const elsewhere = "#/properties/values/properties/primary/properties/resources"
	_, err = NewUnderProperty("values", pillar(elsewhere), nil)
	if !errors.Is(err, ErrSchemaResolve()) {
		t.Fatalf("a pointer naming nothing in the pillar: error %v, want the resolution condition", err)
	}
	if !strings.Contains(err.Error(), `"`+elsewhere+`"`) {
		t.Errorf("the refusal does not name the pointer as written: %v", err)
	}

	// A pointer into the definitions is unchanged.
	defs := compilePillar(t, `$defs:
  Resources:
    requests:
      cpu: { type: string, pattern: "^[0-9]+(m|\\.[0-9]+)?$" }
primary:
  resources: { $ref: "#/$defs/Resources" }
secondary:
  resources: { $ref: "#/$defs/Resources" }
`)
	v, err = NewUnderProperty("values", defs, nil)
	if err != nil {
		t.Fatalf("a pointer into the definitions was refused: %v", err)
	}
	report, err = v.Validate(cpuValues("lots"), nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].FieldPath != "values.secondary.resources.requests.cpu" {
		t.Errorf("a definition reached by name reports at the value at fault; got %v", report.Findings)
	}
}

// TestWhen_GatesTheRequirementOnAnAbsentValue asserts a requirement on a
// when-gated property holds only while the predicate does: absent with the
// predicate false is accepted, absent with it true is refused at the property,
// and the property's own constraints follow the same predicate.
func TestWhen_GatesTheRequirementOnAnAbsentValue(t *testing.T) {
	t.Parallel()

	for _, form := range []struct {
		name string
		src  string
	}{
		{"beside properties", `architecture: { type: string, enum: [standalone, replication] }
secondary:
  when: '{{ eq $.architecture "replication" }}'
  required: true
  properties:
    replicaCount: { type: integer, min: 2 }
`},
		{"beside a reference", `$defs:
  NodePool:
    replicaCount: { type: integer, min: 1, max: 9 }
architecture: { type: string, enum: [standalone, replication] }
secondary:
  $ref: "#/$defs/NodePool"
  when: '{{ eq $.architecture "replication" }}'
  required: true
  properties:
    replicaCount: { type: integer, min: 2 }
`},
	} {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			v, err := New(mustCompileAndConvert(t, form.src))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			for _, row := range []struct {
				name     string
				instance map[string]any
				wantPath string // "" for accepted
			}{
				{"predicate false, property absent", map[string]any{"architecture": "standalone"}, ""},
				{"predicate true, property absent", map[string]any{"architecture": "replication"}, "secondary"},
				{"predicate false, constraint broken", map[string]any{"architecture": "standalone",
					"secondary": map[string]any{"replicaCount": 1}}, ""},
				{"predicate true, constraint broken", map[string]any{"architecture": "replication",
					"secondary": map[string]any{"replicaCount": 1}}, "secondary.replicaCount"},
				{"predicate true, conforming", map[string]any{"architecture": "replication",
					"secondary": map[string]any{"replicaCount": 3}}, ""},
			} {
				report, err := v.Validate(row.instance, nil)
				if err != nil {
					t.Fatalf("%s: validate: %v", row.name, err)
				}
				if row.wantPath == "" {
					if len(report.Findings) != 0 {
						t.Errorf("%s: refused: %v", row.name, report.Findings)
					}
					continue
				}
				named := false
				for _, f := range report.Findings {
					if !isUnderPath(f.FieldPath, row.wantPath) {
						t.Errorf("%s: a finding outside %q: %v", row.name, row.wantPath, f)
					}
					named = named || f.FieldPath == row.wantPath
				}
				if !named {
					t.Errorf("%s: findings %v, want one at %q", row.name, report.Findings, row.wantPath)
				}
			}
		})
	}
}
