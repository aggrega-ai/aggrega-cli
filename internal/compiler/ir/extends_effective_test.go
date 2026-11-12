// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ir

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

func compileNode(t *testing.T, y string) compactform.Schema {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(y), &n); err != nil {
		t.Fatalf("schema fixture parse: %v", err)
	}
	c, err := compactform.Compile(&n)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c
}

// TestBuildIR_InheritedDefaultMaterialised proves the FULL-scope routing reaches
// defaults materialisation: an extends-ing Component's inherited value default
// (carried only on the composed effective schema, not the node's authored
// values) is materialised into the rendered values mapping — keeping the
// rendered output consistent with the schema the conformance gate validates
// against.
func TestBuildIR_InheritedDefaultMaterialised(t *testing.T) {
	t.Parallel()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	// The node authors no values; the inherited default must fill in.
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "app/child:1.0.0"})
	topoResult := &topology.TopologyResult{
		Nodes:              nodes,
		FeatureAttachments: map[string][]topology.FeatureAttachment{},
	}
	resolveResult := &resolver.ResolveResult{
		Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
		BoundaryInfra: map[string]*resolver.BoundaryInfra{},
	}
	effectiveSchema := compileNode(t, "values:\n  tier: { type: string, default: gold }\n")
	cl := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) {
			return types.Component{Metadata: types.ComponentMetadata{Name: "app/child", Version: "1.0.0"}}, true
		},
		EffectiveSchema: func(typeURI string) (compactform.Schema, bool) {
			if typeURI == "app/child:1.0.0" {
				return effectiveSchema, true
			}
			return nil, false
		},
	}

	graph, _, _, err := buildIR(BuildIRConfig{Topology: topoResult, ResolveResult: resolveResult, CatalogLookup: cl})
	if err != nil {
		t.Fatalf("BuildIRContext: %v", err)
	}
	raw, ok := graph.Spec.ValuesMapping.Get("api")
	if !ok {
		t.Fatal("expected a values mapping for api carrying the inherited default")
	}
	if !strings.Contains(string(raw), "gold") {
		t.Errorf("inherited default not materialised into rendered values; got %s", raw)
	}
}
