// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import "sort"

// bridgeSpec is the normalized per-edge input shared by both halves of the
// compose bridge — the gateway command generation (reading the
// valuesMapping-forwarded edges) and the per-consumer env-override producer
// (reading the structured boundary edges). Both normalize their input into this
// shape and run it through resolveBridge, so the listen port and the consumer
// rewrite agree per edge by construction.
//
// Each endpoint's network inputs are the two the assembler places that endpoint
// by: its aggrega.ai/namespace, else its assembly path — the directory its
// output is assembled into, which the host hands the assembler as the group's
// path and the edge view as the endpoint's assembly path. Never the endpoint's
// boundary path: two boundaries assembled into one file share that file's
// network, so their boundary paths differ where their networks do not.
type bridgeSpec struct {
	Source       string // consumer node ID (override-map key)
	Target       string
	Socket       string
	SrcNS        string // raw aggrega.ai/namespace of the source endpoint
	SrcPath      string // source endpoint assembly path (no-namespace fallback)
	TgtNS        string
	TgtPath      string   // target endpoint assembly path (no-namespace fallback)
	UpstreamHost string   // the target's provided host (the proxy_pass target host)
	TargetPort   int      // the target's own port (default listen port + proxy_pass port)
	TargetVars   []string // consumer env vars to rewrite (host-carrying inject keys)
}

func (s bridgeSpec) key() string { return s.Source + "\x00" + s.Target + "\x00" + s.Socket }

// bridgeResolved pairs a bridged edge with its assigned gateway listen port.
type bridgeResolved struct {
	Spec       bridgeSpec
	ListenPort int
}

// resolveBridge is the single derivation both halves call. It selects the edges
// whose two endpoints are assembled into one compose file and sit on different
// networks there (resolved network names differ — never a raw-namespace
// compare), assigns each a gateway listen port via the shared
// assignBridgePorts rule, and returns them sorted by stable key (Invariant 8).
// One derivation, two consumers (gateway listen + consumer rewrite) ⇒ they agree
// per edge, including portBase-reassigned collisions.
func resolveBridge(specs []bridgeSpec, portBase int) []bridgeResolved {
	var bridged []bridgeSpec
	for _, s := range specs {
		// A gateway relays only inside the one file it is assembled into, so an edge
		// whose endpoints are assembled into two files is not one it can bridge: its
		// consumer keeps the target's own name.
		if s.SrcPath != s.TgtPath {
			continue
		}
		if deriveNodeNetworkName(s.SrcNS, s.SrcPath) == deriveNodeNetworkName(s.TgtNS, s.TgtPath) {
			continue
		}
		bridged = append(bridged, s)
	}
	if len(bridged) == 0 {
		return nil
	}
	bes := make([]bridgeEdge, len(bridged))
	for i, s := range bridged {
		bes[i] = bridgeEdge{Key: s.key(), TargetPort: s.TargetPort}
	}
	ports := assignBridgePorts(bes, portBase)
	out := make([]bridgeResolved, len(bridged))
	for i, s := range bridged {
		out[i] = bridgeResolved{Spec: s, ListenPort: ports[s.key()]}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Spec.key() < out[j].Spec.key() })
	return out
}

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
