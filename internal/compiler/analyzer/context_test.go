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

// TestAnalyzeContextHonorsCancellation builds a graph whose per-edge
// selector pass would run far longer than the cancel window, cancels
// mid-pass, and asserts AnalyzeContext returns near the cancel time
// rather than at full-pass completion. This pins the per-iteration
// ctx.Err() guard the daemon's shutdown drain relies on.
func TestAnalyzeContextHonorsCancellation(t *testing.T) {
	t.Parallel()

	const edges = 300
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	var edgeList []types.IREdge
	for i := 0; i < edges; i++ {
		id := fmt.Sprintf("c%d", i)
		nodes.Set(id, types.IRNode{Component: "slow/component", Labels: labels})
		edgeList = append(edgeList, types.IREdge{Source: id, Target: id, Socket: "s"})
	}
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, Edges: edgeList}}

	// Each Component lookup sleeps, so an uninterrupted pass over 300
	// edges takes ~3s. Cancellation must short-circuit the loop well
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
		t.Fatalf("AnalyzeContext returned after %v; want <200ms — per-edge cancellation not honored", elapsed)
	}
}

// TestValidateSelectorsPopulatesFieldLocator pins the Field locator a
// selector violation carries: the uses-socket key on the source
// component, in the mapping-style dotted form the daemon walks to a
// sub-file position.
func TestValidateSelectorsPopulatesFieldLocator(t *testing.T) {
	t.Parallel()

	serverLabels := types.OrderedMap[string]{}
	serverLabels.Set(types.ComponentTypeLabel, "server")
	// Target labelled "resource" does not satisfy the api-server's
	// databases selector (which requires type=stateful-server).
	wrongLabels := types.OrderedMap[string]{}
	wrongLabels.Set(types.ComponentTypeLabel, "resource")

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/compute/api-server", Labels: serverLabels})
	nodes.Set("cache", types.IRNode{Component: "core/redis", Labels: wrongLabels})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "api", Target: "cache", Socket: "databases"},
			},
		},
	}

	errs, err := validateSelectors(context.Background(), graph, analyzerCatalog())
	if err != nil {
		t.Fatalf("validateSelectors: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 selector violation; got %d: %+v", len(errs), errs)
	}
	const want = ".spec.components.api.uses.databases"
	if errs[0].Field != want {
		t.Errorf("Field = %q; want %q", errs[0].Field, want)
	}
}

// TestValidateSelectorsSubsystemNestedYieldsNoFieldLocator pins the
// degradation contract: a subsystem-nested component is authored under
// .spec.subsystems.*, not .spec.components.*, so the top-level locator
// would name the wrong site. The analyzer emits an empty Field instead,
// routing the daemon to file-level positioning rather than a wrong
// sub-file span.
func TestValidateSelectorsSubsystemNestedYieldsNoFieldLocator(t *testing.T) {
	t.Parallel()

	serverLabels := types.OrderedMap[string]{}
	serverLabels.Set(types.ComponentTypeLabel, "server")
	wrongLabels := types.OrderedMap[string]{}
	wrongLabels.Set(types.ComponentTypeLabel, "resource")

	nodes := types.OrderedMap[types.IRNode]{}
	// "db" is authored inside subsystem "data" — SubsystemPath is set.
	nodes.Set("db", types.IRNode{Component: "core/compute/api-server", Labels: serverLabels, SubsystemPath: "data"})
	nodes.Set("cache", types.IRNode{Component: "core/redis", Labels: wrongLabels})

	graph := &types.IRGraph{
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{
				{Source: "db", Target: "cache", Socket: "databases"},
			},
		},
	}

	errs, err := validateSelectors(context.Background(), graph, analyzerCatalog())
	if err != nil {
		t.Fatalf("validateSelectors: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 selector violation; got %d: %+v", len(errs), errs)
	}
	if errs[0].Field != "" {
		t.Errorf("subsystem-nested violation Field = %q; want empty (file-level fallback)", errs[0].Field)
	}
}
