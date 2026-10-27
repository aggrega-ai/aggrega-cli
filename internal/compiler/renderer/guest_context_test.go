// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// orderedStrings builds an ordered string map from key-value pairs, in the order
// given.
func orderedStrings(pairs ...string) types.OrderedMap[string] {
	var m types.OrderedMap[string]
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i], pairs[i+1])
	}
	return m
}

// guestContextFixture builds one node whose every context member is populated
// with a value that names where it came from: each annotation bucket, a nested
// boundary with a sync wave, a directory and a sub-directory, the placement's
// annotation set, and parameters authored in a non-sorted order so the
// constructor's ordering is observable.
func guestContextFixture() (string, types.IRNode, *types.IRGraph, RendererInput) {
	const nodeID = "app"
	node := types.IRNode{
		Component:            "local/compute/app",
		BoundaryPath:         "aws-prod/payments",
		SyncWave:             -2,
		AggregateAnnotations: orderedStrings("team.example/owner", "billing", "aggrega.ai/namespace", "from-aggregate"),
		ReleaseAnnotations:   orderedStrings("aggrega.ai/namespace", "from-release", "example.org/zone", "z1"),
		CatalogAnnotations:   orderedStrings("aggrega.ai/namespace", "from-catalog"),
	}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(nodeID, node)
	graph := &types.IRGraph{Spec: types.IRSpec{
		Nodes:                nodes,
		FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
	}}
	input := RendererInput{
		YAMLIndent: 4,
		ResolveResult: &resolver.ResolveResult{
			BoundaryInfra: map[string]*resolver.BoundaryInfra{nodeID: {
				BoundaryPath: "aws-prod/payments",
				Directory:    "dist/prod",
				SubDirectory: "payments",
				SyncWave:     -2,
				Annotations:  orderedStrings("aggrega.ai/namespace", "from-release", "example.org/zone", "z1"),
			}},
			Parameters: map[string]any{"zeta": "last", "alpha": "first", "mid": "middle"},
		},
	}
	return nodeID, node, graph, input
}

// TestOneGuestContextServesBothSockets pins that a provider export and a pipeline
// step of the same node are handed one context: the one the node render context
// carries for the provider engine, and the one a step's input is built from, are
// the same bytes. A second constructor that dropped or reordered a member on
// either side would fail here even where both shapes stayed individually valid.
func TestOneGuestContextServesBothSockets(t *testing.T) {
	t.Parallel()
	nodeID, node, graph, input := guestContextFixture()
	bi, parameters := nodeBoundaryInputs(input, nodeID)

	provider, err := json.Marshal(buildNodeRenderContext(nodeID, node, graph, input).GuestContext)
	if err != nil {
		t.Fatalf("marshal the provider's context: %v", err)
	}
	step, err := json.Marshal(buildNodeGuestContext(node, bi, parameters))
	if err != nil {
		t.Fatalf("marshal the step's context: %v", err)
	}
	if !bytes.Equal(provider, step) {
		t.Errorf("the two sockets receive different contexts:\n provider: %s\n     step: %s", provider, step)
	}
	if strings.Contains(string(provider), "yaml_indent") {
		t.Errorf("the context the builder produces carries the indentation width, which only the provider engine adds: %s", provider)
	}
}

// TestGuestContextCarriesEveryMember reads the context back off the wire and
// checks each member against the node it was built from: parameters sorted by
// name, each annotation bucket exactly as the IR node carries it — the Aggregate's
// and the catalog's beside the Release's, not folded into it — and every boundary
// member.
func TestGuestContextCarriesEveryMember(t *testing.T) {
	t.Parallel()
	nodeID, node, graph, input := guestContextFixture()

	wire, err := json.Marshal(buildNodeRenderContext(nodeID, node, graph, input).GuestContext)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Parameters  map[string]any               `json:"parameters"`
		Annotations map[string]map[string]string `json:"annotations"`
		Boundary    struct {
			Name         string            `json:"name"`
			Path         []string          `json:"path"`
			SyncWave     int               `json:"sync_wave"`
			Directory    string            `json:"directory"`
			SubDirectory string            `json:"sub_directory"`
			Annotations  map[string]string `json:"annotations"`
		} `json:"boundary"`
	}
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", wire, err)
	}

	wantParams := map[string]any{"alpha": "first", "mid": "middle", "zeta": "last"}
	if !reflect.DeepEqual(got.Parameters, wantParams) {
		t.Errorf("parameters = %v, want %v", got.Parameters, wantParams)
	}
	if a, m, z := strings.Index(string(wire), `"alpha"`), strings.Index(string(wire), `"mid"`), strings.Index(string(wire), `"zeta"`); !(a < m && m < z) {
		t.Errorf("parameters are not sorted by name on the wire: %s", wire)
	}

	wantBuckets := map[string]map[string]string{
		"aggregate": {"team.example/owner": "billing", "aggrega.ai/namespace": "from-aggregate"},
		"release":   {"aggrega.ai/namespace": "from-release", "example.org/zone": "z1"},
		"catalog":   {"aggrega.ai/namespace": "from-catalog"},
	}
	if !reflect.DeepEqual(got.Annotations, wantBuckets) {
		t.Errorf("annotations = %v, want %v", got.Annotations, wantBuckets)
	}

	if got.Boundary.Name != "payments" {
		t.Errorf("boundary name = %q, want %q", got.Boundary.Name, "payments")
	}
	if !reflect.DeepEqual(got.Boundary.Path, []string{"aws-prod", "payments"}) {
		t.Errorf("boundary path = %v, want [aws-prod payments]", got.Boundary.Path)
	}
	if got.Boundary.SyncWave != -2 {
		t.Errorf("boundary sync_wave = %d, want -2", got.Boundary.SyncWave)
	}
	if got.Boundary.Directory != "dist/prod" || got.Boundary.SubDirectory != "payments" {
		t.Errorf("boundary directory/sub_directory = %q/%q, want dist/prod/payments", got.Boundary.Directory, got.Boundary.SubDirectory)
	}
	if !reflect.DeepEqual(got.Boundary.Annotations, map[string]string{"aggrega.ai/namespace": "from-release", "example.org/zone": "z1"}) {
		t.Errorf("boundary annotations = %v", got.Boundary.Annotations)
	}
}

// TestGuestContextIsDeterministic builds the context many times from inputs
// whose parameter map Go iterates in a different order each time, and requires
// one byte sequence: the wire payload a guest reads must not depend on map
// iteration order.
func TestGuestContextIsDeterministic(t *testing.T) {
	t.Parallel()
	nodeID, node, graph, input := guestContextFixture()
	first, err := json.Marshal(buildNodeRenderContext(nodeID, node, graph, input).GuestContext)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 50; i++ {
		again, err := json.Marshal(buildNodeRenderContext(nodeID, node, graph, input).GuestContext)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("build %d produced different bytes:\n first: %s\n again: %s", i, first, again)
		}
	}
}

// TestGuestContextWithoutAResolveResult pins the zero end: a render with no
// resolve result still hands each socket the node's buckets and boundary
// identity, with no parameters and no placement members.
func TestGuestContextWithoutAResolveResult(t *testing.T) {
	t.Parallel()
	nodeID, node, graph, _ := guestContextFixture()
	gc := buildNodeRenderContext(nodeID, node, graph, RendererInput{}).GuestContext
	if gc.Parameters.Len() != 0 {
		t.Errorf("parameters carried without a resolve result: %v", gc.Parameters.Keys())
	}
	if gc.Boundary.Directory != "" || gc.Boundary.SubDirectory != "" || gc.Boundary.Annotations.Len() != 0 {
		t.Errorf("placement members carried without a resolve result: %+v", gc.Boundary)
	}
	if gc.Boundary.Name != "payments" || gc.Annotations.Release.Len() != 2 {
		t.Errorf("the node's own members were dropped: %+v", gc)
	}
}
