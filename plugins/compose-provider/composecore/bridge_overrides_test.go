// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func bridgeTestEdge(source, target, socket, srcNS, tgtNS, host, port, inVar string) types.EnrichedEdge {
	provides := types.OrderedMap[string]{}
	provides.Set("host", host)
	provides.Set("port", port)
	env := types.OrderedMap[string]{}
	env.Set(inVar, "host") // inject the named env var FROM the host provides field
	inj := types.OrderedMap[types.OrderedMap[string]]{}
	inj.Set("env", env)
	return types.EnrichedEdge{
		Source:          source,
		Target:          target,
		Socket:          socket,
		SourceNamespace: srcNS,
		TargetNamespace: tgtNS,
		Provides:        provides,
		Inject:          inj,
	}
}

// TestBoundaryConsumerEnvOverrides_CrossNetworkFilterAndPorts proves the engine
// half: only intra-subtree edges whose endpoints resolve to different networks
// are bridged; the host-carrying env var is rewritten to aggrega-gw:<port>; the
// port comes from the shared assignBridgePorts rule (target-port default,
// portBase fallback on collision); a same-network edge is excluded.
func TestBoundaryConsumerEnvOverrides_CrossNetworkFilterAndPorts(t *testing.T) {
	t.Parallel()
	edges := []types.EnrichedEdge{
		// cross-network (frontend->backend), target port 5432
		bridgeTestEdge("web", "db", "backend", "frontend", "backend", "db", "5432", "DB_HOST"),
		// cross-network, also 5432 -> collision; sorts BEFORE db by key
		bridgeTestEdge("web", "cache", "caches", "frontend", "backend", "cache", "5432", "CACHE_HOST"),
		// same network (frontend->frontend) -> NOT bridged
		bridgeTestEdge("web", "sidecar", "side", "frontend", "frontend", "sidecar", "9000", "SIDE_HOST"),
	}
	got, err := ComputeBridgeConsumerEnvOverrides(edges, types.OrderedMap[any]{})
	if err != nil {
		t.Fatalf("ComputeBridgeConsumerEnvOverrides: %v", err)
	}
	// Key order: "web\x00cache\x00caches" < "web\x00db\x00backend" → cache claims
	// 5432, db collides → portBase 10000. sidecar excluded (same network).
	want := map[string]map[string]string{
		"web": {
			"CACHE_HOST": "aggrega-gw:5432",
			"DB_HOST":    "aggrega-gw:10000",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %v; want %v", got, want)
	}
}

// TestBoundaryConsumerEnvOverrides_NoBridgedEdges returns nil when no edge
// crosses a network seam (opt-in: no gateway, no rewrites).
func TestBoundaryConsumerEnvOverrides_NoBridgedEdges(t *testing.T) {
	t.Parallel()
	edges := []types.EnrichedEdge{
		bridgeTestEdge("web", "db", "backend", "app", "app", "db", "5432", "DB_HOST"),
	}
	got, err := ComputeBridgeConsumerEnvOverrides(edges, types.OrderedMap[any]{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil overrides for same-network edges; got %v", got)
	}
}
