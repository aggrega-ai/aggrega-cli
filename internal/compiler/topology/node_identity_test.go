// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestFlattenedNodeIdentity pins which authoring shape qualifies a node ID and
// which does not, because every consumer that addresses a node by name depends
// on the answer — a Release naming a component, and every guard that matches a
// Release entry against the component it consumes.
//
// A module prefix qualifies; a subsystem does not. A component sits at the same
// bare instance name whether the author declared it at the top level or nested
// it two subsystems deep, and its subsystem path travels beside the ID rather
// than inside it. Only a component reached through the `aggregates` surface
// takes a dotted ID, and the dot carries the module's name.
//
// The distinction is easy to invert on a reading, and inverting it would key a
// Release lookup on a string the Release never writes.
func TestFlattenedNodeIdentity(t *testing.T) {
	t.Parallel()
	const rootYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  subsystems:
    data:
      components:
        main-db:
          type: app/db:1.0.0
      subsystems:
        inner:
          components:
            deep:
              type: app/db:1.0.0
  aggregates:
    mod:
      type: ./mod.yaml
`
	const modYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: mod
spec:
  components:
    inner-svc:
      type: app/svc:1.0.0
`
	var root, mod types.Aggregate
	if err := yaml.Unmarshal([]byte(rootYAML), &root); err != nil {
		t.Fatalf("root parse: %v", err)
	}
	if err := yaml.Unmarshal([]byte(modYAML), &mod); err != nil {
		t.Fatalf("mod parse: %v", err)
	}
	loader := func(string, map[string]any) (*types.Aggregate, string, map[string]any, error) {
		return &mod, "mod.yaml", nil, nil
	}
	flat, err := flattenAggregate(types.Normalize(&root), "", "", "root.yaml", loader, map[string]bool{}, 0, nil)
	if err != nil {
		t.Fatalf("flatten: %v", err)
	}

	want := map[string]struct {
		subsystemPath   string
		sourceAggregate string
	}{
		"main-db":       {subsystemPath: "data", sourceAggregate: ""},
		"deep":          {subsystemPath: "data.inner", sourceAggregate: ""},
		"mod.inner-svc": {subsystemPath: "default", sourceAggregate: "mod"},
	}
	if flat.Nodes.Len() != len(want) {
		t.Fatalf("expected %d nodes; got %d (%v)", len(want), flat.Nodes.Len(), flat.Nodes.Keys())
	}
	for id, node := range flat.Nodes.All() {
		w, expected := want[id]
		if !expected {
			t.Errorf("unexpected node ID %q — a subsystem must not qualify an ID", id)
			continue
		}
		if node.SubsystemPath != w.subsystemPath {
			t.Errorf("node %q: subsystem path = %q, want %q", id, node.SubsystemPath, w.subsystemPath)
		}
		if node.SourceAggregate != w.sourceAggregate {
			t.Errorf("node %q: source aggregate = %q, want %q", id, node.SourceAggregate, w.sourceAggregate)
		}
	}
}
