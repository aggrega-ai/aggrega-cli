// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// withholdingCatalogDir is the catalog whose gateway Component declares one of
// its values sensitive and leaves a sibling readable — the same catalog the
// governance socket's verdict-over-the-graph withholding is proved against, so
// the label-transform input is held to the same declarations.
const withholdingCatalogDir = "../testdata/governance-withholding/catalog"

// plantedSensitiveValue is what the gateway node authors at its
// declared-sensitive value. It must never cross into the guest's input.
const plantedSensitiveValue = "planted-node-own-value"

// TestAssembleMutatorInput_WithholdsDeclaredSensitiveValues proves the
// label-transform input carries a value the node's Component declares sensitive
// only as the declared withheld marker, while a sibling value no declaration
// reaches arrives verbatim — and that the topology node keeps its authored value,
// because the withholding is a copy made for the guest rather than an edit to
// the graph the build goes on to render from.
//
// The marker is built from the key the withholding package exports, so this file
// never spells it.
func TestAssembleMutatorInput_WithholdsDeclaredSensitiveValues(t *testing.T) {
	t.Parallel()

	cat, err := catalog.LoadAll("", withholdingCatalogDir)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	gatewayValues := types.NewOrderedMap[any]()
	gatewayValues.Set("region", "eu")
	gatewayValues.Set("admin_token", plantedSensitiveValue)
	nodes := types.NewOrderedMap[*topology.TopologyNode]()
	nodes.Set("gateway", &topology.TopologyNode{InstanceID: "gateway", ComponentType: "withhold/gateway", Values: *gatewayValues})
	nodes.Set("model", &topology.TopologyNode{InstanceID: "model", ComponentType: "withhold/model"})
	topo := &topology.TopologyResult{Nodes: *nodes}

	mi, err := topology.AssembleMutatorInput(topo, cat.CatalogLookup(),
		topology.CompilationContext{ReleaseName: "rel", Environment: "local"}, nil, nil)
	if err != nil {
		t.Fatalf("AssembleMutatorInput: %v", err)
	}

	gateway, ok := mi.Graph.Nodes.Get("gateway")
	if !ok {
		t.Fatal("gateway node missing from the projection")
	}
	got, err := json.Marshal(gateway.Values)
	if err != nil {
		t.Fatalf("marshal gateway values: %v", err)
	}
	marker, err := json.Marshal(redact.WithheldMarker(redact.WithheldDeclared))
	if err != nil {
		t.Fatalf("marshal the withheld marker: %v", err)
	}
	if want := `{"region":"eu","admin_token":` + string(marker) + `}`; string(got) != want {
		t.Errorf("gateway values handed to the guest:\n got: %s\nwant: %s", got, want)
	}

	// Nowhere on the wire the socket carries.
	wire, err := mi.EncodeCBOR()
	if err != nil {
		t.Fatalf("EncodeCBOR: %v", err)
	}
	if bytes.Contains(wire, []byte(plantedSensitiveValue)) {
		t.Errorf("the declared-sensitive value crossed into the guest's input in the clear")
	}

	// A node declaring no values carries none.
	if model, _ := mi.Graph.Nodes.Get("model"); !model.Values.IsZero() {
		t.Errorf("model node gained values: %d keys", model.Values.Len())
	}

	// The graph the build renders from still holds the authored value.
	node, _ := topo.Nodes.Get("gateway")
	if v, _ := node.Values.Get("admin_token"); v != plantedSensitiveValue {
		t.Errorf("the topology node's own value changed to %v; the withholding must be a copy", v)
	}
}
