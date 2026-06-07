// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import "sort"

// bridgeEdge is the normalized per-edge input to the compose-bridge port rule:
// a stable ordering key plus the target's own port. Both halves of the bridge
// normalize their input into this shape before assigning ports — the gateway
// command generation (reading the valuesMapping-forwarded edges) and the
// per-consumer env-override producer (reading the structured boundary edges) —
// so assignBridgePorts yields an identical map for both. Keeping the shape
// minimal (key + port) is deliberate: the rule must never depend on the
// round-tripped Provides/Inject map order (which JSON forwarding does not
// preserve); upstream host/port are read by field NAME elsewhere, never by
// iterating those maps.
type bridgeEdge struct {
	// Key is the stable ordering + identity key for the edge (e.g. target +
	// socket). The gateway listen port and the consumer's rewrite are both keyed
	// by it, so the two halves agree per edge.
	Key string
	// TargetPort is the target component's own port — the default listen port
	// for this edge's gateway server block.
	TargetPort int
}

// assignBridgePorts computes the gateway listen port each bridged edge gets:
// the target's own port by default; when two edges would collide on a listen
// port, the later one (in stable Key order) takes the next free port at or
// above portBase, walking upward past any in-use port. It is a pure function of
// (edges, portBase): the edges are sorted by Key here (no reliance on caller
// order, no map iteration), so the result is byte-deterministic and identical
// for both halves of the bridge — gateway `listen` and the per-consumer rewrite
// to `aggrega-gw:<port>` agree per edge, including the reassigned collisions.
func assignBridgePorts(edges []bridgeEdge, portBase int) map[string]int {
	sorted := make([]bridgeEdge, len(edges))
	copy(sorted, edges)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	assigned := make(map[string]int, len(sorted))
	used := make(map[int]bool, len(sorted))
	for _, e := range sorted {
		p := e.TargetPort
		if used[p] {
			p = portBase
			for used[p] {
				p++
			}
		}
		used[p] = true
		assigned[e.Key] = p
	}
	return assigned
}
