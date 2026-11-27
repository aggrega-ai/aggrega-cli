// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestPillarRequiredListsTheRequiredFieldsWithNoDefault compiles a schema whose
// values pillar requires four fields — one with a default, one its properties
// do not declare — and returns the three with no default, in the order the
// required list names them, each with its compiled schema, nil for the one not
// declared; a pillar the schema does not declare, and a schema requiring
// nothing, return none.
func TestPillarRequiredListsTheRequiredFieldsWithNoDefault(t *testing.T) {
	var node yaml.Node
	src := "values:\n  properties:\n    runtime: { type: string }\n    memory: { type: integer, default: 128 }\n" +
		"    tags: { type: array }\n  required: [tags, memory, runtime, undeclared]\n" +
		"infra:\n  image: { type: string }\n"
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(&node)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var names []string
	for _, f := range PillarRequired(compiled, "values") {
		names = append(names, f.Name)
		if typ, _ := f.Schema["type"].(string); f.Name == "runtime" && typ != "string" {
			t.Errorf("runtime carries the schema %v, want its compiled schema of type string", f.Schema)
		}
		if f.Name == "undeclared" && f.Schema != nil {
			t.Errorf("the undeclared field carries the schema %v, want none", f.Schema)
		}
	}
	if want := []string{"tags", "runtime", "undeclared"}; !reflect.DeepEqual(names, want) {
		t.Errorf("the values pillar requires %v, want %v", names, want)
	}
	if got := PillarRequired(compiled, "infra"); got != nil {
		t.Errorf("the infra pillar, requiring nothing, requires %v", got)
	}
	if got := PillarRequired(compiled, "provides"); got != nil {
		t.Errorf("a pillar the schema does not declare requires %v", got)
	}
}
