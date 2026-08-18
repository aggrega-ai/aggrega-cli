// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// cancellationGraph builds a graph of the given size, every node carrying a
// presence mandate nothing satisfies, so the surviving validator has work to do
// at each node and a walk that stopped early is distinguishable from one that
// finished.
func cancellationGraph(nodeCount int) *types.IRGraph {
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	annotations := types.OrderedMap[string]{}
	annotations.Set("features.aggrega.ai/requiredType.audit", "example/absent-feature")
	nodes := types.OrderedMap[types.IRNode]{}
	for i := 0; i < nodeCount; i++ {
		nodes.Set(fmt.Sprintf("c%d", i), types.IRNode{
			Component:          "slow/component",
			Labels:             labels,
			CatalogAnnotations: annotations,
		})
	}
	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes}}
}

// countingSignal is a caller whose cancellation signal counts the times it is
// read and withdraws after the read named by withdrawAfter, or never when that
// is zero.
//
// The signal read is the instrument because it is the first statement of the
// per-node loop under test, so the number of reads is the number of node
// iterations the walk entered. Counting it measures how far the walk got, which
// is the property; a wall-clock reading measures the machine.
type countingSignal struct {
	context.Context
	reads         *int
	withdrawAfter int
}

func (c countingSignal) Err() error {
	*c.reads++
	if c.withdrawAfter > 0 && *c.reads > c.withdrawAfter {
		return context.Canceled
	}
	return c.Context.Err()
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
//
// What it counts is the caller's own signal reads rather than anything the walk
// consults afterwards. That is the one instrument every remaining reading here
// shares: the reading in this package answers a node's presence mandates from
// the node's own record and reaches for nothing a caller can hand it, so a probe
// injected as an input would count nothing and read as a walk that stopped
// immediately.
func TestAnalyzeContextStopsPartwayOnACancelledCaller(t *testing.T) {
	t.Parallel()

	const (
		nodeCount = 50
		withdrawn = 3
	)

	t.Run("the walk stops on the node after the caller withdraws", func(t *testing.T) {
		t.Parallel()

		var reads int
		signal := countingSignal{Context: context.Background(), reads: &reads, withdrawAfter: withdrawn}
		result, err := AnalyzeContext(signal, cancellationGraph(nodeCount), resolver.CatalogLookup{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want the caller's withdrawal", err)
		}
		// The exact equality is what makes this a proof. A bound would be
		// satisfied by a walk that visited every node and reported at the end,
		// which is the reading this rules out: such a walk reads the signal once
		// per node, so it would end at nodeCount rather than at the withdrawal.
		if reads != withdrawn+1 {
			t.Errorf("the signal was read %d time(s) by a caller that withdrew after read %d; want exactly %d — the %d node iteration(s) entered, plus the read that stopped the walk — out of the graph's %d node(s)",
				reads, withdrawn, withdrawn+1, withdrawn, nodeCount)
		}
		// A cancelled analysis hands back no partial result, so a caller cannot
		// mistake a truncated set for a complete one.
		if result != nil {
			t.Errorf("a cancelled analysis returned a result: %+v", result)
		}
	})

	t.Run("an uncancelled walk reaches every node", func(t *testing.T) {
		t.Parallel()

		var reads int
		signal := countingSignal{Context: context.Background(), reads: &reads}
		result, err := AnalyzeContext(signal, cancellationGraph(nodeCount), resolver.CatalogLookup{})
		if err != nil {
			t.Fatalf("analysis over a walk nothing cancelled returned %v", err)
		}
		// The control is what stops the assertion above passing on a sequence
		// that never entered the loop at all.
		if reads != nodeCount {
			t.Errorf("the signal was read %d time(s) for %d node(s)", reads, nodeCount)
		}
		// And the walk did the work at each node it entered, so the count above
		// is a measure of a walk that ran rather than of an empty loop.
		if len(result.Errors) != nodeCount {
			t.Errorf("the completed walk reported %d violation(s) for %d node(s) each declaring an unsatisfied mandate", len(result.Errors), nodeCount)
		}
	})
}
