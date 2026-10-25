// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// twoNodeTopology builds a minimal TopologyResult with two labelled nodes for
// the mutator confinement tests: "web" carrying tier=frontend and the values
// webValues builds, and "db" carrying tier=backend and no values, both in
// author order.
func twoNodeTopology() *topology.TopologyResult {
	nodes := types.NewOrderedMap[*topology.TopologyNode]()

	webLabels := types.NewOrderedMap[string]()
	webLabels.Set("tier", "frontend")
	nodes.Set("web", &topology.TopologyNode{InstanceID: "web", ComponentType: "core/web", Labels: *webLabels, Values: webValues()})

	dbLabels := types.NewOrderedMap[string]()
	dbLabels.Set("tier", "backend")
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/db", Labels: *dbLabels})

	return &topology.TopologyResult{Nodes: *nodes}
}

// webValues is the "web" node's declared values, set in an order that is not
// sorted order — replicas before extensions before a nested object whose own
// keys run z before a — so a projection that sorted any level is observable.
// The nested object is held as a pointer and the list as []any, the shapes an
// Aggregate's YAML decodes a nested mapping and a sequence into.
func webValues() types.OrderedMap[any] {
	nested := types.NewOrderedMap[any]()
	nested.Set("z", "last")
	nested.Set("a", "first")
	values := types.NewOrderedMap[any]()
	values.Set("replicas", 2)
	values.Set("extensions", []any{"vector"})
	values.Set("tuning", nested)
	return *values
}

// TestAssembleMutatorInput_CarriesInstanceValues proves the projection hands a
// guest each node's declared values: a node declaring values carries them in
// the author's key order at every level, and a node declaring none carries no
// values key on the wire at all, so an attachment over a graph without values
// sees the payload it saw before values were projected.
func TestAssembleMutatorInput_CarriesInstanceValues(t *testing.T) {
	topo := twoNodeTopology()
	mi, err := topology.AssembleMutatorInput(topo, resolver.CatalogLookup{},
		topology.CompilationContext{ReleaseName: "rel", Environment: "prod"}, nil, nil)
	if err != nil {
		t.Fatalf("AssembleMutatorInput: %v", err)
	}

	web, ok := mi.Graph.Nodes.Get("web")
	if !ok {
		t.Fatal("web node missing from the projection")
	}
	if got, want := web.Values.Keys(), []string{"replicas", "extensions", "tuning"}; !slices.Equal(got, want) {
		t.Errorf("web values key order = %v, want %v", got, want)
	}
	webJSON, err := json.Marshal(web)
	if err != nil {
		t.Fatalf("marshal web node: %v", err)
	}
	const wantWeb = `{"component":"core/web","resolved_version":"","labels":{"tier":"frontend"},` +
		`"values":{"replicas":2,"extensions":["vector"],"tuning":{"z":"last","a":"first"}}}`
	if string(webJSON) != wantWeb {
		t.Errorf("web node on the JSON wire:\n got: %s\nwant: %s", webJSON, wantWeb)
	}

	db, _ := mi.Graph.Nodes.Get("db")
	dbJSON, err := json.Marshal(db)
	if err != nil {
		t.Fatalf("marshal db node: %v", err)
	}
	const wantDB = `{"component":"core/db","resolved_version":"","labels":{"tier":"backend"}}`
	if string(dbJSON) != wantDB {
		t.Errorf("db node on the JSON wire:\n got: %s\nwant: %s", dbJSON, wantDB)
	}

	// Identical input yields identical bytes on the wire the socket carries.
	first, err := mi.EncodeCBOR()
	if err != nil {
		t.Fatalf("EncodeCBOR: %v", err)
	}
	again, err := topology.AssembleMutatorInput(twoNodeTopology(), resolver.CatalogLookup{},
		topology.CompilationContext{ReleaseName: "rel", Environment: "prod"}, nil, nil)
	if err != nil {
		t.Fatalf("AssembleMutatorInput (second): %v", err)
	}
	second, err := again.EncodeCBOR()
	if err != nil {
		t.Fatalf("EncodeCBOR (second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two projections of identical input encode differently:\n first: %x\nsecond: %x", first, second)
	}
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
		// The guest is handed each node's values, and none of these may change
		// them: the patch surface carries labels alone, so a path into the values
		// a guest read either addresses nothing or adds a field beside labels.
		"replace a declared value":     json.RawMessage(`[{"op":"replace","path":"/nodes/web/values/replicas","value":9}]`),
		"add under the values":         json.RawMessage(`[{"op":"add","path":"/nodes/web/values/extensions","value":["none"]}]`),
		"remove a declared value":      json.RawMessage(`[{"op":"remove","path":"/nodes/web/values/tuning"}]`),
		"add a values field":           json.RawMessage(`[{"op":"add","path":"/nodes/db/values","value":{"replicas":9}}]`),
		"replace the values wholesale": json.RawMessage(`[{"op":"replace","path":"/nodes/web/values","value":{}}]`),
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
			// Nor into any node's values.
			web, _ := topo.Nodes.Get("web")
			got, err := json.Marshal(web.Values)
			if err != nil {
				t.Fatalf("marshal web values: %v", err)
			}
			if want := `{"replicas":2,"extensions":["vector"],"tuning":{"z":"last","a":"first"}}`; string(got) != want {
				t.Errorf("web values after a rejected patch = %s, want %s", got, want)
			}
			if db, _ := topo.Nodes.Get("db"); !db.Values.IsZero() {
				t.Errorf("db gained values after a rejected patch: %d keys", db.Values.Len())
			}
		})
	}
}
