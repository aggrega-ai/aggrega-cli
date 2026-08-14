// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestTrace_NonBlockingContract_50Nodes verifies the trace writer's design
// contract: Write() does not block the renderer goroutine on disk I/O. The
// renderer hands events to a buffered channel; a consumer goroutine drains
// them to disk asynchronously. This test runs a 50-node compile against an
// fsWriter and asserts:
//
//  1. Zero events were dropped due to channel saturation. With ~9 events
//     per node from a 50-node workload-mode-only graph (~450 events) and a
//     1024-event channel buffer, saturation is impossible at this scale;
//     non-zero drops indicate either the consumer goroutine isn't draining
//     or the buffer was reduced without updating this test.
//
//  2. Close() returns within a finite, generous bound (5 seconds). The
//     consumer goroutine must finish processing buffered events when told
//     to stop; an unbounded Close() means a leaked goroutine or a stuck
//     channel. This is a liveness ceiling, not a performance measurement:
//     it separates "returns" from "hangs forever", which no amount of
//     machine contention can blur.
//
//  3. The trace directory is fully populated after Close() — every node
//     has its expected per-pass files. Proves async writes actually
//     completed before Close() returned.
//
// The non-blocking half of the contract is asserted where it can be asserted
// without a clock, in the trace package's own saturation test: that test
// drives a writer whose channel has one slot and no consumer, and requires
// the subsequent Write() calls to return reporting a drop. A blocking send
// could not do that — it would deadlock the test rather than fail it — so the
// assertion discriminates the two designs structurally, at any machine speed.
// The same package confirms the other half by construction: the only call
// site that touches the filesystem is reached from the consumer goroutine, so
// no Write() can reach disk on the caller's goroutine.
//
// Peak Write() latency is logged at the end of this test and deliberately not
// asserted on. A wall-clock bound here would measure the machine's spare CPU
// rather than the writer, because Write() serialises the pillar snapshot on
// the calling goroutine before the send, and that serialisation is
// CPU-bound — so under a fully parallel battery the number reflects
// contention from unrelated packages. Widening such a bound to absorb the
// contention only makes it measure less; it was already widened once.
//
// The test uses helm-workload-mode Recipes (no source:) for test-
// infrastructure reuse via testHelmFactory(t); compose-workload-mode would
// exercise the same per-node trace event count (slots 01-04, 06, 08, 10,
// 11, 12) through a different engine. The contract under test is engine-
// independent.
func TestTrace_NonBlockingContract_50Nodes(t *testing.T) {
	t.Parallel()

	const numNodes = 50
	const boundaryPath = "perf-bench"

	// Build a 50-node IRGraph: single boundary, no edges, no FRs. Nodes
	// share Component "core/compute/api-server"; Recipe is the same shared
	// stub returned by the closure-based RecipeLookup below.
	nodes := types.OrderedMap[types.IRNode]{}
	for i := 0; i < numNodes; i++ {
		nodeID := fmt.Sprintf("node-%02d", i)
		nodes.Set(nodeID, types.IRNode{
			Component:       "core/compute/api-server",
			BoundaryPath:    boundaryPath,
			ResolvedVersion: "1.0.0",
		})
	}

	traceRoot := t.TempDir()
	traceWriter := trace.New(traceRoot, trace.Mode{})
	// Note: do NOT defer Close() — assertion #2 needs explicit timing.

	input := renderer.RendererInput{
		Graph: &types.IRGraph{
			Metadata: types.IRMetadata{AggregateName: "perf-bench"},
			Spec: types.IRSpec{
				Nodes:      nodes,
				Provenance: types.IRProvenance{CompilerVersion: "0.1.0", AggregateVersion: "1.0.0"},
			},
		},
		FeatureOrder: ir.FeatureOrder{},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "perf-bench"}},
			Release:       &types.Release{Metadata: types.ReleaseMetadata{Name: "perf-release", Environment: "test"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{},
		},
		Trace: traceWriter,
	}

	// Stub RecipeLookup: same workload-mode helm Recipe for every node.
	// No source:, no valuesMapping:, no artifacts:. Slot coverage:
	// pre-loop (01-03), inject mapping (04), source-resolution locals (06),
	// workload synthesis (08), provider render (10), emission (11), labels
	// (12) — 9 events per node.
	sharedRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "perf-bench-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Capability: "helm",
			Workload: types.WorkloadTemplate{Inline: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: bench:latest
    ports:
      web: 8080
`},
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(_, _ string) (*types.Recipe, bool) {
			return sharedRecipe, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) {
			return nil, false
		},
	}

	// Run the compile with tracing on.
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll failed: %v", err)
	}

	// Time Close() — it drains the channel and waits on the consumer
	// goroutine. Bounded drain is a contract; unbounded means a leak.
	closeStart := time.Now()
	if err := traceWriter.Close(); err != nil {
		t.Fatalf("traceWriter.Close failed: %v", err)
	}
	closeElapsed := time.Since(closeStart)

	stats := trace.Stats(traceWriter)

	// === Assertion 1: zero saturation drops ===
	// 50 nodes × ~9 events per node ≈ 450 events; channel buffer is 1024.
	// Drops at this scale indicate the consumer goroutine isn't draining
	// (deadlock, missing goroutine, or producer-faster-than-consumer in a
	// way the buffer can't absorb).
	if stats.DroppedWrites > 0 {
		t.Errorf("DroppedWrites = %d at 50-node load; channel buffer (1024) and consumer goroutine should absorb ~450-event workload without drops",
			stats.DroppedWrites)
	}

	// === Assertion 2: Close() returns within a generous bound ===
	const maxCloseDuration = 5 * time.Second
	if closeElapsed > maxCloseDuration {
		t.Errorf("Close() took %v, exceeds %v bound; consumer goroutine may have leaked or stalled",
			closeElapsed, maxCloseDuration)
	}

	// === Assertion 3: trace directory is fully populated ===
	// Validates that async writes completed before Close() returned.
	// Slots present for sourceless+workload helm:
	expectedFiles := []string{
		"01-pillars-initial.yaml",
		"02-provides-evaluation.yaml",
		"03-inject-hydration.yaml",
		"04-inject-mapping.yaml",
		"06-source-locals.yaml",
		"08-workload-synthesis.yaml",
		"11-engine-emission.log",
		"12-labels-injected.log",
	}
	for i := 0; i < numNodes; i++ {
		nodeID := fmt.Sprintf("node-%02d", i)
		nodeDir := filepath.Join(traceRoot, "boundaries", boundaryPath, nodeID)
		for _, fname := range expectedFiles {
			path := filepath.Join(nodeDir, fname)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("expected trace file %s not found: %v", path, err)
			}
		}
		// Slot 10: per-emitted-file directory; helm sourceless mode emits
		// at least one synthesised file (placeholder Deployment for
		// workload-only Recipes), so the directory should be non-empty.
		synthDir := filepath.Join(nodeDir, "10-engine-synthesis")
		entries, err := os.ReadDir(synthDir)
		if err != nil {
			t.Errorf("expected slot 10 directory %s not found: %v", synthDir, err)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("slot 10 directory %s is empty; expected at least one synthesised file", synthDir)
		}
	}

	// Diagnostic logging on success. The peak Write() latency here is
	// observation only — nothing asserts on it, so a slow number on a loaded
	// machine is information rather than a verdict.
	t.Logf("trace stats: total writes=%d, dropped=%d, max Write() latency=%v, Close() duration=%v",
		stats.TotalWrites, stats.DroppedWrites, stats.MaxWriteLatency, closeElapsed)
}
