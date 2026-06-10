// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// projOM builds an OrderedMap[any] from key/value pairs.
func projOM(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// projGraph builds a consumer→target graph. The api→db edge ("main") carries
// the supplied per-edge values/infra; an api→cache edge ("rel") is stamped
// Release-origin; a db→log edge ("audit") gives the target a non-empty uses
// context so the safety assertion is not trivially empty.
func projGraph(edgeValues, edgeInfra types.OrderedMap[any]) *types.IRGraph {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/server/api-server", ResolvedVersion: "1.0.0"})
	nodes.Set("db", types.IRNode{Component: "core/database/postgres", ResolvedVersion: "2.0.0"})
	nodes.Set("cache", types.IRNode{Component: "core/cache/redis", ResolvedVersion: "1.0.0"})
	nodes.Set("log", types.IRNode{Component: "core/log/sink", ResolvedVersion: "1.0.0"})
	return &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "api", Target: "db", Socket: "databases", Alias: "main", Values: edgeValues, Infra: edgeInfra},
				{Source: "api", Target: "cache", Socket: "caches", Alias: "rel", OriginRelease: true},
				{Source: "db", Target: "log", Socket: "logs", Alias: "audit"},
			},
		},
	}
}

func projContext(t *testing.T, nodeID string, graph *types.IRGraph) *types.TemplateContext {
	t.Helper()
	inject, injectRaw := BuildInjectPipe(nil)
	return AssembleNodeContext(nodeID, graph, NodeProvides{}, inject, injectRaw)
}

// TestAssembleEnrichedUses_ProjectsValuesInfraAndOrigin pins the projection:
// a consumer's outgoing edge carrying per-edge values/infra surfaces at
// .context.uses.<alias>.values/.infra, and a Release-created edge surfaces
// OriginRelease=true — the render-context representation of the ternary origin.
func TestAssembleEnrichedUses_ProjectsValuesInfraAndOrigin(t *testing.T) {
	t.Parallel()
	ctx := projContext(t, "api", projGraph(projOM("pool_size", 20), projOM("storage_class", "fast")))

	main, ok := ctx.Context.Uses.Get("main")
	if !ok {
		t.Fatalf("expected uses alias 'main'; got %v", ctx.Context.Uses.Keys())
	}
	if v, ok := main.Values.Get("pool_size"); !ok || v != 20 {
		t.Errorf("uses.main.values.pool_size = %v (ok=%v), want 20", v, ok)
	}
	if v, ok := main.Infra.Get("storage_class"); !ok || v != "fast" {
		t.Errorf("uses.main.infra.storage_class = %v (ok=%v), want fast", v, ok)
	}
	if main.OriginRelease {
		t.Errorf("Aggregate-authored edge carries OriginRelease=true; want false")
	}

	rel, ok := ctx.Context.Uses.Get("rel")
	if !ok {
		t.Fatalf("expected uses alias 'rel'")
	}
	if !rel.OriginRelease {
		t.Errorf("Release-created edge carries OriginRelease=false; want true")
	}
}

// TestAssembleNodeContext_TargetRenderInvariantWRTConsumerConfig is the
// load-bearing safety property at the render-context layer: a target's
// assembled render context is byte-identical whether or not the consumer
// attaches per-edge
// config, while the consumer's context differs (it carries the config). Because
// rendering is a pure deterministic function of the context, context-identical
// implies output-identical. The full real-output proof lives at the
// end-to-end example.
func TestAssembleNodeContext_TargetRenderInvariantWRTConsumerConfig(t *testing.T) {
	t.Parallel()
	withConfig := projGraph(projOM("pool_size", 20), projOM("storage_class", "fast"))
	without := projGraph(types.OrderedMap[any]{}, types.OrderedMap[any]{})

	marshal := func(c *types.TemplateContext) []byte {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal context: %v", err)
		}
		return b
	}

	// The TARGET (db) must be invariant w.r.t. the api->db edge's per-edge config.
	dbWith := marshal(projContext(t, "db", withConfig))
	dbWithout := marshal(projContext(t, "db", without))
	if string(dbWith) != string(dbWithout) {
		t.Errorf("target render context perturbed by consumer-side per-edge config:\n with:    %s\n without: %s", dbWith, dbWithout)
	}

	// The CONSUMER (api) must differ — it carries the config.
	apiWith := marshal(projContext(t, "api", withConfig))
	apiWithout := marshal(projContext(t, "api", without))
	if string(apiWith) == string(apiWithout) {
		t.Errorf("consumer render context did not change with per-edge config; projection not applied")
	}
}

// TestConsumerRender_PerEdgeValueReachesOutput is the focused consumer-render:
// a consumer recipe's valuesMapping reads .context.uses.<alias>.values via the
// real Phase B template path, and the per-edge value lands in the rendered
// output.
func TestConsumerRender_PerEdgeValueReachesOutput(t *testing.T) {
	t.Parallel()
	ctx := projContext(t, "api", projGraph(projOM("pool_size", 20), projOM("storage_class", "fast")))

	recipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "consumer-recipe"},
		Spec: types.RecipeSpec{
			ValuesMapping: types.ValuesMapping{
				Inline: `{"db_pool": {{ (index .context.uses "main").values.pool_size }}}`,
			},
		},
	}

	values, err := ExecutePhaseB(ctx, recipe, nil)
	if err != nil {
		t.Fatalf("ExecutePhaseB: %v", err)
	}
	got, ok := asNumber(values["db_pool"])
	if !ok || got != 20 {
		t.Errorf("rendered db_pool = %v (%T), want 20", values["db_pool"], values["db_pool"])
	}
}
