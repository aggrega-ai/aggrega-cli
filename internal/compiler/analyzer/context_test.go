// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// cancellationGraph builds a graph of the given size whose nodes carry no
// attachment, so exactly one validator in the sequence reaches the catalog and
// the count below is attributable to it.
func cancellationGraph(nodeCount int) *types.IRGraph {
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	for i := 0; i < nodeCount; i++ {
		nodes.Set(fmt.Sprintf("c%d", i), types.IRNode{Component: "slow/component", Labels: labels})
	}
	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}
}

// countingCatalog counts the component lookups the validator sequence makes and
// withdraws the caller on the one named by cancelAt, or never when that is zero.
//
// The lookup is the instrument because it is reached once per node, inside the
// per-node loop whose cancellation read is under test. Counting it measures how
// far the walk got, which is the property; a wall-clock reading measures the
// machine.
func countingCatalog(cancel context.CancelFunc, cancelAt int, calls *int) resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) {
			*calls++
			if cancelAt > 0 && *calls == cancelAt {
				cancel()
			}
			return types.Component{}, false
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

// TestAnalyzeContextStopsPartwayOnACancelledCaller pins the per-node
// cancellation read the daemon's shutdown drain relies on.
//
// It asserts how far the walk got rather than how long it took. A walk that
// visited every node and reported the withdrawal at the end returns exactly the
// same error as one that stopped, so the error alone distinguishes nothing —
// and a duration bound distinguishes them only on a machine quiet enough for
// the two to differ by more than the noise. The exact count distinguishes them
// at any machine speed.
func TestAnalyzeContextStopsPartwayOnACancelledCaller(t *testing.T) {
	t.Parallel()

	const (
		nodeCount = 50
		withdrawn = 3
	)

	t.Run("the walk stops on the node after the caller withdraws", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var calls int
		_, err := AnalyzeContext(ctx, cancellationGraph(nodeCount), countingCatalog(cancel, withdrawn, &calls))
		if err == nil {
			t.Fatal("expected the withdrawal to come back as an error; got nil")
		}
		// The exact equality is what makes this a proof. A bound would be
		// satisfied by a walk that visited every node and reported at the end,
		// which is the reading this rules out.
		if calls != withdrawn {
			t.Errorf("the catalog was read %d time(s) after a caller that withdrew on read %d; want exactly %d of the graph's %d node(s)",
				calls, withdrawn, withdrawn, nodeCount)
		}
	})

	t.Run("an uncancelled walk reaches every node", func(t *testing.T) {
		t.Parallel()
		var calls int
		if _, err := AnalyzeContext(context.Background(), cancellationGraph(nodeCount),
			countingCatalog(func() {}, 0, &calls)); err != nil {
			t.Fatalf("analysis over a walk nothing cancelled returned %v", err)
		}
		// The control is what stops the assertion above passing on a sequence
		// that never reached the catalog at all.
		if calls != nodeCount {
			t.Errorf("the catalog was read %d time(s) for %d node(s)", calls, nodeCount)
		}
	})
}
