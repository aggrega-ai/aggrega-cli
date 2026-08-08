// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// twoNodeTopology builds a minimal TopologyResult with two labelled nodes for
// the mutator confinement tests: "web" carrying tier=frontend and "db"
// carrying tier=backend, both in author order.
func twoNodeTopology() *topology.TopologyResult {
	nodes := types.NewOrderedMap[*topology.TopologyNode]()

	webLabels := types.NewOrderedMap[string]()
	webLabels.Set("tier", "frontend")
	nodes.Set("web", &topology.TopologyNode{InstanceID: "web", ComponentType: "core/web", Labels: *webLabels})

	dbLabels := types.NewOrderedMap[string]()
	dbLabels.Set("tier", "backend")
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/db", Labels: *dbLabels})

	return &topology.TopologyResult{Nodes: *nodes}
}

func labelOf(t *testing.T, topo *topology.TopologyResult, nodeID, key string) (string, bool) {
	t.Helper()
	node, ok := topo.Nodes.Get(nodeID)
	if !ok {
		t.Fatalf("node %q not present", nodeID)
	}
	return node.Labels.Get(key)
}

// TestApplyMutatorPatches_AddsLabelWithinConfinement proves the happy path: a
// well-formed patch adds a label to one node, the original labels survive in
// author order, and the untouched node is unchanged.
func TestApplyMutatorPatches_AddsLabelWithinConfinement(t *testing.T) {
	topo := twoNodeTopology()
	out := topology.MutatorOutput{Patches: []json.RawMessage{
		json.RawMessage(`[{"op":"add","path":"/nodes/web/labels/compliance.aggrega~1pci","value":"true"}]`),
	}}

	if err := topology.ApplyMutatorPatches(topo, out); err != nil {
		t.Fatalf("ApplyMutatorPatches: unexpected error: %v", err)
	}

	if v, ok := labelOf(t, topo, "web", "compliance.aggrega/pci"); !ok || v != "true" {
		t.Errorf("web injected label: got (%q,%v), want (\"true\",true)", v, ok)
	}
	// Original label survives, still first (author order preserved).
	web, _ := topo.Nodes.Get("web")
	keys := web.Labels.Keys()
	if len(keys) != 2 || keys[0] != "tier" || keys[1] != "compliance.aggrega/pci" {
		t.Errorf("web label order: got %v, want [tier compliance.aggrega/pci]", keys)
	}
	// Untouched node is unchanged.
	if v, ok := labelOf(t, topo, "db", "tier"); !ok || v != "backend" {
		t.Errorf("db tier changed: got (%q,%v)", v, ok)
	}
	if db, _ := topo.Nodes.Get("db"); db.Labels.Len() != 1 {
		t.Errorf("db gained/lost labels: len=%d, want 1", db.Labels.Len())
	}
}

// TestApplyMutatorPatches_EmptyIsNoOp proves an empty patch list leaves the
// graph untouched.
func TestApplyMutatorPatches_EmptyIsNoOp(t *testing.T) {
	topo := twoNodeTopology()
	if err := topology.ApplyMutatorPatches(topo, topology.MutatorOutput{}); err != nil {
		t.Fatalf("ApplyMutatorPatches empty: %v", err)
	}
	if v, _ := labelOf(t, topo, "web", "tier"); v != "frontend" {
		t.Errorf("web tier mutated by empty patch set: %q", v)
	}
}

// TestApplyMutatorPatches_RejectsOutOfBounds proves every confinement breach
// halts with ErrMutatorPatchRejected: a patch that decodes badly, that targets
// a non-label node field, that adds a ghost node, that adds a top-level key, or
// that sets a non-string label value.
func TestApplyMutatorPatches_RejectsOutOfBounds(t *testing.T) {
	cases := map[string]json.RawMessage{
		"malformed patch document":     json.RawMessage(`{"op":"add"}`),
		"replace non-label node field": json.RawMessage(`[{"op":"replace","path":"/nodes/web/component","value":"HACKED"}]`),
		"add non-label node field":     json.RawMessage(`[{"op":"add","path":"/nodes/web/component","value":"HACKED"}]`),
		"add ghost node":               json.RawMessage(`[{"op":"add","path":"/nodes/ghost","value":{"labels":{}}}]`),
		"add top-level key":            json.RawMessage(`[{"op":"add","path":"/injected","value":"ghost"}]`),
		"non-string label value":       json.RawMessage(`[{"op":"add","path":"/nodes/web/labels/nested","value":{"deep":"x"}}]`),
		"remove a node":                json.RawMessage(`[{"op":"remove","path":"/nodes/db"}]`),
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			topo := twoNodeTopology()
			err := topology.ApplyMutatorPatches(topo, topology.MutatorOutput{Patches: []json.RawMessage{patch}})
			if err == nil {
				t.Fatalf("expected ErrMutatorPatchRejected, got nil")
			}
			if !errors.Is(err, topology.ErrMutatorPatchRejected()) {
				t.Fatalf("expected ErrMutatorPatchRejected, got %v", err)
			}
			// The rejected mutation must not have leaked into the graph.
			if _, hacked := labelOf(t, topo, "web", "component"); hacked {
				t.Errorf("rejected patch leaked a component label onto the graph")
			}
		})
	}
}
