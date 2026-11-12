// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package topology

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// annotationPairs reads an OrderedMap[string] as its "key=value" entries in map
// order, so an assertion pins both the values and the author order they arrive in.
func annotationPairs(m types.OrderedMap[string]) []string {
	out := []string{}
	for k, v := range m.All() {
		out = append(out, k+"="+v)
	}
	return out
}

func mustAggregateYAML(t *testing.T, src string) *types.Aggregate {
	t.Helper()
	var a types.Aggregate
	if err := yaml.Unmarshal([]byte(src), &a); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return &a
}

// TestFlattenAggregate_NodesCarryTheAggregateAnnotationCascade pins that every
// flattened node carries the Aggregate-side annotation cascade the resolver
// computes for the document declaring it: a subsystem's annotations reach every
// component under it at every nesting level, a nested subsystem's own annotation
// overrides its parent's, a component's own annotation overrides its subsystem's,
// a structured value is left out while its scalar siblings arrive, a node with no
// annotation on its path carries none, and a sub-aggregate's nodes carry the
// cascade of the sub-aggregate's own document and nothing of the importing one's.
func TestFlattenAggregate_NodesCarryTheAggregateAnnotationCascade(t *testing.T) {
	t.Parallel()
	root := mustAggregateYAML(t, `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: root
  version: "1.0.0"
spec:
  subsystems:
    billing:
      annotations:
        team.example/owner: "billing-squad"
        example.org/limits:
          cpu: "2"
          memory: "4Gi"
        cost.example/center: "CC-3421"
      components:
        invoice:
          type: core/server:1.0.0
        payment:
          type: core/server:1.0.0
          annotations:
            team.example/owner: "payments-squad"
      subsystems:
        ledger:
          annotations:
            cost.example/center: "CC-3422"
          components:
            posting:
              type: core/server:1.0.0
    plain:
      components:
        lone:
          type: core/server:1.0.0
  aggregates:
    data-module:
      type: modules/data
`)
	child := mustAggregateYAML(t, `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: data
  version: "1.0.0"
spec:
  subsystems:
    storage:
      annotations:
        team.example/owner: "data-team"
      components:
        cold:
          type: core/server:1.0.0
`)
	loader := func(typeName string, _ map[string]any) (*types.Aggregate, string, map[string]any, error) {
		if typeName == "modules/data" {
			return child, "", nil, nil
		}
		return nil, "", nil, fmt.Errorf("unknown: %s", typeName)
	}

	result, err := flattenAggregate(types.Normalize(root), "", "", "", loader, map[string]bool{}, 0, nil)
	if err != nil {
		t.Fatalf("flattenAggregate: %v", err)
	}

	cases := []struct {
		name string
		node string
		want []string
	}{
		{
			name: "a subsystem's scalar annotations reach its component; the structured value is left out",
			node: "invoice",
			want: []string{"team.example/owner=billing-squad", "cost.example/center=CC-3421"},
		},
		{
			name: "a component's own annotation overrides its subsystem's, in the subsystem's position",
			node: "payment",
			want: []string{"team.example/owner=payments-squad", "cost.example/center=CC-3421"},
		},
		{
			name: "a nested subsystem inherits its parent's and overrides with its own",
			node: "posting",
			want: []string{"team.example/owner=billing-squad", "cost.example/center=CC-3422"},
		},
		{
			name: "a node with no annotation on its path carries none",
			node: "lone",
			want: []string{},
		},
		{
			name: "a sub-aggregate's node carries its own document's cascade only",
			node: "data-module.cold",
			want: []string{"team.example/owner=data-team"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node, ok := result.Nodes.Get(tc.node)
			if !ok || node == nil {
				t.Fatalf("node %q missing; nodes: %v", tc.node, result.Nodes.Keys())
			}
			if got := annotationPairs(node.AggregateAnnotations); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("node %q AggregateAnnotations = %v, want %v", tc.node, got, tc.want)
			}
		})
	}
}

// TestFlattenSubsystems_AggregateAnnotationsUnderARootPath pins that the lookup
// is made with the node's path inside the document, so a walk hung beneath a
// non-empty root path still finds each node's cascade.
func TestFlattenSubsystems_AggregateAnnotationsUnderARootPath(t *testing.T) {
	t.Parallel()
	agg := mustAggregateYAML(t, `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: root
  version: "1.0.0"
spec:
  subsystems:
    outer:
      annotations:
        team.example/owner: "outer-team"
      subsystems:
        inner:
          components:
            deep:
              type: core/server:1.0.0
`)
	result := &flattenResult{
		Nodes:              types.OrderedMap[*TopologyNode]{},
		FeatureAttachments: map[string][]FeatureAttachment{},
		ExportedComponents: types.OrderedMap[string]{},
		Pruned:             types.OrderedMap[bool]{},
	}
	if err := flattenSubsystems(result, "", "hung", "", types.Normalize(agg), nil); err != nil {
		t.Fatalf("flattenSubsystems: %v", err)
	}
	node, ok := result.Nodes.Get("deep")
	if !ok || node == nil {
		t.Fatalf("node deep missing; nodes: %v", result.Nodes.Keys())
	}
	if node.SubsystemPath != "hung.outer.inner" {
		t.Fatalf("SubsystemPath = %q, want hung.outer.inner (the fixture's premise)", node.SubsystemPath)
	}
	if got, want := annotationPairs(node.AggregateAnnotations), []string{"team.example/owner=outer-team"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AggregateAnnotations = %v, want %v", got, want)
	}
}
