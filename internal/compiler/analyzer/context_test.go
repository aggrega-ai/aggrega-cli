// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// cancellationGraph builds a chain of the given size — every node wired to the
// next — so the enumeration has real work at each start node it enters and a
// walk that stopped early is distinguishable from one that finished.
//
// A chain rather than an isolated node set, and that is what makes the control
// below a measurement rather than a count of an empty loop: an isolated node has
// no outgoing connection, so the enumeration would enter each start node and
// emit nothing, and a signal count over it could not tell a walk that did the
// work from one that skipped it.
func cancellationGraph(nodeCount int) *types.IRGraph {
	labels := types.OrderedMap[string]{}
	labels.Set(types.ComponentTypeLabel, "server")
	nodes := types.OrderedMap[types.IRNode]{}
	var edges []types.IREdge
	for i := 0; i < nodeCount; i++ {
		nodes.Set(fmt.Sprintf("c%d", i), types.IRNode{
			Component: "slow/component",
			Labels:    labels,
		})
		if i+1 < nodeCount {
			edges = append(edges, types.IREdge{
				Source: fmt.Sprintf("c%d", i),
				Target: fmt.Sprintf("c%d", i+1),
				Socket: "next",
			})
		}
	}
	return &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, Edges: edges}}
}

// countingSignal is a caller whose cancellation signal counts the times it is
// read and withdraws after the read named by withdrawAfter, or never when that
// is zero.
//
// The signal read is the instrument because it is the first statement of the
// per-start-node loop under test, so the number of reads is the number of start
// nodes the enumeration entered. Counting it measures how far the walk got,
// which is the property; a wall-clock reading measures the machine.
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

// TestAnalyzeContextStopsPartwayOnACancelledCaller pins the per-start-node
// cancellation read the daemon's shutdown drain relies on.
//
// It asserts how far the walk got rather than how long it took. A walk that
// visited every start node and reported the withdrawal at the end returns
// exactly the same error as one that stopped, so the error alone distinguishes
// nothing — and a duration bound distinguishes them only on a machine quiet
// enough for the two to differ by more than the noise. The exact count
// distinguishes them at any machine speed.
//
// What it counts is the caller's own signal reads. That is the one instrument
// this function's remaining work shares: path enumeration reaches for nothing a
// caller can hand it, so a probe injected as an input would count nothing and
// read as a walk that stopped immediately.
func TestAnalyzeContextStopsPartwayOnACancelledCaller(t *testing.T) {
	t.Parallel()

	const (
		nodeCount = 50
		withdrawn = 3
	)

	t.Run("the walk stops on the start node after the caller withdraws", func(t *testing.T) {
		t.Parallel()

		var reads int
		signal := countingSignal{Context: context.Background(), reads: &reads, withdrawAfter: withdrawn}
		result, err := AnalyzeContext(signal, cancellationGraph(nodeCount))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want the caller's withdrawal", err)
		}
		// The exact equality is what makes this a proof. A bound would be
		// satisfied by a walk that visited every start node and reported at the
		// end, which is the reading this rules out: such a walk reads the signal
		// once per start node, so it would end at nodeCount rather than at the
		// withdrawal.
		if reads != withdrawn+1 {
			t.Errorf("the signal was read %d time(s) by a caller that withdrew after read %d; want exactly %d — the %d start node(s) entered, plus the read that stopped the walk — out of the graph's %d node(s)",
				reads, withdrawn, withdrawn+1, withdrawn, nodeCount)
		}
		// A cancelled derivation hands back no partial result, so a caller cannot
		// mistake a truncated enumeration for a complete one — which matters here
		// because the policy payload is assembled from the whole of it.
		if result != nil {
			t.Errorf("a cancelled derivation returned a result: %+v", result)
		}
	})

	t.Run("an uncancelled walk reaches every start node", func(t *testing.T) {
		t.Parallel()

		var reads int
		signal := countingSignal{Context: context.Background(), reads: &reads}
		result, err := AnalyzeContext(signal, cancellationGraph(nodeCount))
		if err != nil {
			t.Fatalf("derivation over a walk nothing cancelled returned %v", err)
		}
		// The control is what stops the assertion above passing on a sequence
		// that never entered the loop at all.
		if reads != nodeCount {
			t.Errorf("the signal was read %d time(s) for %d node(s)", reads, nodeCount)
		}
		// And the walk did the work at each start node it entered, so the count
		// above measures a walk that ran rather than an empty loop. Every node but
		// the last one starts a path along the chain.
		if len(result.Paths) != nodeCount-1 {
			t.Errorf("the completed walk enumerated %d path(s) over a chain of %d node(s); want %d", len(result.Paths), nodeCount, nodeCount-1)
		}
	})
}
