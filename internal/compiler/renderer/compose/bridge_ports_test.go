// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"reflect"
	"testing"
)

// TestAssignBridgePorts_CollisionFallback locks the port rule: target-port by
// default, and a collision sends the later edge (in stable Key order) to the
// next free port at/above portBase. Caller order is intentionally scrambled to
// prove the function sorts by Key itself (Invariant 8 — no reliance on caller
// order, no map iteration).
func TestAssignBridgePorts_CollisionFallback(t *testing.T) {
	t.Parallel()
	edges := []bridgeEdge{
		{Key: "db\x00backend", TargetPort: 5432},    // collides with cache (later in Key order)
		{Key: "api\x00backend", TargetPort: 8080},   // unique
		{Key: "cache\x00backend", TargetPort: 5432}, // first claimant of 5432 in Key order
	}
	// Key order: api, cache, db.
	//   api   -> 8080 (free)
	//   cache -> 5432 (free)
	//   db    -> 5432 collides -> portBase 10000 (free)
	want := map[string]int{
		"api\x00backend":   8080,
		"cache\x00backend": 5432,
		"db\x00backend":    10000,
	}
	got := assignBridgePorts(edges, 10000)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assignBridgePorts = %v; want %v", got, want)
	}
}

// TestAssignBridgePorts_PortBaseAlsoInUse covers the walk-upward case: when
// portBase itself is a target port already in use, the fallback steps past it.
func TestAssignBridgePorts_PortBaseAlsoInUse(t *testing.T) {
	t.Parallel()
	edges := []bridgeEdge{
		{Key: "a", TargetPort: 10000}, // occupies portBase
		{Key: "b", TargetPort: 7000},
		{Key: "c", TargetPort: 7000}, // collides with b -> 10000 used -> 10001
	}
	want := map[string]int{"a": 10000, "b": 7000, "c": 10001}
	got := assignBridgePorts(edges, 10000)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assignBridgePorts = %v; want %v", got, want)
	}
}

// TestAssignBridgePorts_Deterministic re-runs over a multi-edge input (with a
// collision) and asserts byte-identical maps — locking the no-map-iteration
// discipline against the JSON-round-trip Provides/Inject order loss (Rider 1).
func TestAssignBridgePorts_Deterministic(t *testing.T) {
	t.Parallel()
	edges := []bridgeEdge{
		{Key: "z", TargetPort: 5432},
		{Key: "m", TargetPort: 5432},
		{Key: "a", TargetPort: 6000},
	}
	first := assignBridgePorts(edges, 10000)
	for i := 0; i < 20; i++ {
		if got := assignBridgePorts(edges, 10000); !reflect.DeepEqual(got, first) {
			t.Fatalf("iteration %d diverged: %v vs %v", i, got, first)
		}
	}
}
