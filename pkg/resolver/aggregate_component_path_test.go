// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"reflect"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAggregateAnnotationComponentPath_Spelling pins the key composed from the
// dotted subsystem path a flattened node carries, at each nesting depth.
func TestAggregateAnnotationComponentPath_Spelling(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		subsystemPath string
		component     string
		want          string
	}{
		{"top-level subsystem", "data", "api", "subsystems.data.components.api"},
		{"nested one level", "outer.middle", "leaf", "subsystems.outer.subsystems.middle.components.leaf"},
		{"nested two levels", "a.b.c", "x", "subsystems.a.subsystems.b.subsystems.c.components.x"},
		{"synthesized default", "default", "api", "subsystems.default.components.api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AggregateAnnotationComponentPath(tc.subsystemPath, tc.component); got != tc.want {
				t.Errorf("AggregateAnnotationComponentPath(%q, %q) = %q, want %q", tc.subsystemPath, tc.component, got, tc.want)
			}
		})
	}
}

// TestAggregateAnnotationComponentPath_ReachesEveryWalkedComponent pins that the
// key composed from each component's dotted subsystem path is exactly the key
// WireAggregateAnnotations files that component under: the two key sets are
// equal, so no component's cascade is unreachable to a reader holding the dotted
// path, and no composed key names nothing.
func TestAggregateAnnotationComponentPath_ReachesEveryWalkedComponent(t *testing.T) {
	t.Parallel()
	agg := loadAggregate(t, `
apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: test
  version: "1.0.0"
spec:
  subsystems:
    billing:
      annotations:
        team.example/owner: "billing"
      components:
        invoice:
          type: core/server:1.0.0
      subsystems:
        ledger:
          components:
            posting:
              type: core/server:1.0.0
          subsystems:
            archive:
              components:
                cold:
                  type: core/server:1.0.0
    plain:
      components:
        lone:
          type: core/server:1.0.0
`)
	na := normalized(t, agg)
	walked := WireAggregateAnnotations(na)

	var composed []string
	err := types.WalkInstanceTree(na.Aggregate().Spec.Subsystems, types.SubsystemTree(), "", struct{}{},
		types.InstanceTreeVisitor[types.Subsystem, types.ComponentInstance, struct{}]{
			Path: types.JoinSubsystemPath,
			Visit: func(_ types.ComponentInstance, name, path string, _ bool, _ struct{}) error {
				composed = append(composed, AggregateAnnotationComponentPath(path, name))
				return nil
			},
		})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(composed)

	if want := keysOfStringMap(walked); !reflect.DeepEqual(composed, want) {
		t.Errorf("composed keys = %v, want the walk's keys %v", composed, want)
	}
	if len(composed) != 4 {
		t.Errorf("composed %d keys, want 4 (one per component in the fixture): %v", len(composed), composed)
	}
}
