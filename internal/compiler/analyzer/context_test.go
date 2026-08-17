// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAnalyzeContextHonorsCancellation builds a graph whose per-node
// validator passes would run far longer than the cancel window, cancels
// mid-pass, and asserts AnalyzeContext returns near the cancel time
// rather than at full-pass completion. This pins the per-iteration
// ctx.Err() guard the daemon's shutdown drain relies on.
func TestAnalyzeContextHonorsCancellation(t *testing.T) {
	t.Parallel()

	const nodeCount = 300
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	var edgeList []types.IREdge
	for i := 0; i < nodeCount; i++ {
		id := fmt.Sprintf("c%d", i)
		nodes.Set(id, types.IRNode{Component: "slow/component", Labels: labels})
		edgeList = append(edgeList, types.IREdge{Source: id, Target: id, Socket: "s"})
	}
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, Edges: edgeList}}

	// Each Component lookup sleeps, so an uninterrupted pass over 300
	// nodes takes ~3s. Cancellation must short-circuit the loop well
	// before that.
	lookup := resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) {
			time.Sleep(10 * time.Millisecond)
			return types.Component{}, false
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := AnalyzeContext(ctx, graph, lookup)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a cancellation error; got nil")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("AnalyzeContext returned after %v; want <200ms — per-iteration cancellation not honored", elapsed)
	}
}
