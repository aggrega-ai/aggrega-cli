// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// bridgePlacementRow places the two endpoints of one edge — web uses db on port
// 5432 — and states whether the bridge relays it: only an edge whose endpoints
// are assembled into one file and sit on different networks there.
type bridgePlacementRow struct {
	name                     string
	srcNS, tgtNS             string
	srcBoundary, tgtBoundary string
	srcAssembly, tgtAssembly string
	bridged                  bool
}

// bridgePlacementRows are the placements both bridge halves are read over. The
// endpoints' boundary paths differ in every row, so a half that read them instead
// of the assembly paths would see a crossing in the rows that have none. An edge
// whose endpoints are assembled into two files is never bridged, whatever its
// networks, because a gateway relays only inside the one file it is assembled
// into.
var bridgePlacementRows = []bridgePlacementRow{
	{"unannotated, two boundaries assembled into one file", "", "", "bridge/frontend", "bridge/backend", "bridge/compose", "bridge/compose", false},
	{"unannotated, assembled into two files", "", "", "apart/one", "apart/two", "apart/left", "apart/right", false},
	{"two namespaces, one file", "frontend", "backend", "bridge/frontend", "bridge/backend", "bridge/compose", "bridge/compose", true},
	{"two namespaces, two files", "frontend", "backend", "apart/one", "apart/two", "apart/left", "apart/right", false},
	{"one namespace, two files", "app", "app", "apart/one", "apart/two", "apart/left", "apart/right", false},
}

// placedBridgeEdge is web -> db placed as the row says, in the shape the host
// hands a boundary-scope view.
func placedBridgeEdge(r bridgePlacementRow) types.EnrichedEdge {
	e := bridgeTestEdge("web", "db", "databases", r.srcNS, r.tgtNS, "db", "5432", "DB_HOST")
	e.SourceBoundaryPath, e.TargetBoundaryPath = r.srcBoundary, r.tgtBoundary
	e.SourceAssemblyPath, e.TargetAssemblyPath = r.srcAssembly, r.tgtAssembly
	return e
}

// TestBridgeConsumerRewrite_ReadsEachEndpointsAssemblyPath drives the consumer
// half through its entry point. A consumer is rewritten to the gateway exactly
// when its edge is bridged; otherwise it keeps the target's own name.
func TestBridgeConsumerRewrite_ReadsEachEndpointsAssemblyPath(t *testing.T) {
	t.Parallel()
	for _, r := range bridgePlacementRows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			got, err := ComputeBridgeConsumerEnvOverrides([]types.EnrichedEdge{placedBridgeEdge(r)}, types.OrderedMap[any]{})
			if err != nil {
				t.Fatalf("ComputeBridgeConsumerEnvOverrides: %v", err)
			}
			var want map[string]map[string]string
			if r.bridged {
				want = map[string]map[string]string{"web": {"DB_HOST": "aggrega-gw:5432"}}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("overrides = %v, want %v", got, want)
			}
		})
	}
}

// TestBridgeGateway_ReadsEachEndpointsAssemblyPath drives the gateway half through
// the sourced render the bridge's FeatureRecipe renders by, with the edge
// forwarded the way its values mapping forwards it — the edge's own JSON. Where
// the edge is bridged the gateway is configured and joins the two networks the
// assembler places the endpoints on; where it is not, no gateway configuration
// is written and the gateway joins no network of its own.
func TestBridgeGateway_ReadsEachEndpointsAssemblyPath(t *testing.T) {
	t.Parallel()
	for _, r := range bridgePlacementRows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(placedBridgeEdge(r))
			if err != nil {
				t.Fatalf("encode the edge: %v", err)
			}
			var forwarded map[string]any
			if err := json.Unmarshal(raw, &forwarded); err != nil {
				t.Fatalf("decode the forwarded edge: %v", err)
			}
			values := map[string]any{bridgeMarkerKey: map[string]any{"edges": []any{forwarded}}}
			rendered, err := RenderSourcedCore([]byte(bridgeGatewayFragment), "bridge-fragment.yaml", values, nodeCtxFor("bfr--bridge--gateway"))
			if err != nil {
				t.Fatalf("RenderSourcedCore: %v", err)
			}
			var doc struct {
				Services map[string]struct {
					Networks []string `yaml:"networks"`
				} `yaml:"services"`
				Configs map[string]any `yaml:"configs"`
			}
			for _, body := range rendered.Files {
				if err := yaml.Unmarshal(body, &doc); err != nil {
					t.Fatalf("parse the fragment: %v\n%s", err, body)
				}
			}
			gw := doc.Services[bridgeGatewayService].Networks
			_, configured := doc.Configs[bridgeConfigName]
			if !r.bridged {
				if configured || len(gw) != 0 {
					t.Errorf("the edge is not bridged, yet the gateway is configured (%v) or joins %v", configured, gw)
				}
				return
			}
			want := []string{deriveNodeNetworkName(r.srcNS, r.srcAssembly), deriveNodeNetworkName(r.tgtNS, r.tgtAssembly)}
			sort.Strings(want)
			if !configured || !reflect.DeepEqual(gw, want) {
				t.Errorf("gateway configured %v on %v; want configured on %v", configured, gw, want)
			}
		})
	}
}
