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

	"github.com/cognitio/aggrega-cli/internal/compiler/ir"
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/internal/compiler/trace"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// TestTrace_NonBlockingContract_50Nodes verifies the trace writer's design
// contract: Write() does not block the renderer goroutine on disk I/O. The
// renderer hands events to a buffered channel; a consumer goroutine drains
// them to disk asynchronously. This test runs a 50-node compile against an
// fsWriter and asserts:
//
//  1. Per-Write() latency stays in microseconds (not milliseconds), proving
//     no Write() call blocked on disk I/O. The channel-send path is a
//     memory-only operation; if a Write() takes ~1ms+, it means the writer
//     synchronously hit disk somewhere — design contract violated.
//
//  2. Zero events were dropped due to channel saturation. With ~9 events
//     per node from a 50-node workload-mode-only graph (~450 events) and a
//     1024-event channel buffer, saturation is impossible at this scale;
//     non-zero drops indicate either the consumer goroutine isn't draining
//     or the buffer was reduced without updating this test.
//
//  3. Close() returns within a finite, generous bound (5 seconds). The
//     consumer goroutine must finish processing buffered events when told
//     to stop; an unbounded Close() means a leaked goroutine or a stuck
//     channel.
//
//  4. The trace directory is fully populated after Close() — every node
//     has its expected per-pass files. Proves async writes actually
//     completed before Close() returned.
//
// This is a structural test of the design contract, not a wall-clock perf
// test. Wall-clock-based perf measurements are unsuitable for CI (GC, OS
// scheduling, FS caching, and CPU contention dominate single-sample timing
// at sub-100ms scales). Structural assertions on the writer's internal
// counters are deterministic and runner-independent: per-Write() latency
// captured inside the writer is microseconds for non-blocking sends and
// milliseconds for blocking sends — a 1000x signal-to-noise ratio robust
// against any runner-speed variation.
//
// The test uses helm-workload-mode Recipes (no source:) for test-
// infrastructure reuse via testHelmFactory(); compose-workload-mode would
// exercise the same per-node trace event count (slots 01-04, 06, 08, 10,
// 11, 12) through a different engine. The contract under test is engine-
// independent.
func TestTrace_NonBlockingContract_50Nodes(t *testing.T) {
	t.Parallel()

	const numNodes = 50
	const boundaryPath = "perf-bench"

	// Build a 50-node IRGraph: single boundary, no edges, no FRs. Nodes
	// share Component "core/server/api-server"; Recipe is the same shared
	// stub returned by the closure-based RecipeLookup below.
	nodes := types.OrderedMap[types.IRNode]{}
	for i := 0; i < numNodes; i++ {
		nodeID := fmt.Sprintf("node-%02d", i)
		nodes.Set(nodeID, types.IRNode{
			Component:       "core/server/api-server",
			BoundaryPath:    boundaryPath,
			ResolvedVersion: "1.0.0",
		})
	}

	traceRoot := t.TempDir()
	traceWriter := trace.New(traceRoot)
	// Note: do NOT defer Close() — assertion #3 needs explicit timing.

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
	// pre-loop (01-03), Phase A (04), B0 locals (06), B2 workload (08),
	// B4 synthesis (10), Emission (11), Labels (12) — 9 events per node.
	sharedRecipe := &types.Recipe{
		Metadata: types.RecipeMetadata{Name: "perf-bench-recipe", Version: "1.0.0"},
		Spec: types.RecipeSpec{
			Provider: "helm",
			Workload: `apiVersion: aggrega.ai/v1alpha1
kind: Workload
containers:
  main:
    image: bench:latest
    ports:
      web: 8080
`,
		},
	}
	lookup := renderer.RecipeLookup{
		Recipe: func(string) (*types.Recipe, bool) {
			return sharedRecipe, true
		},
		FeatureRecipe: func(string) (*types.FeatureRecipe, bool) {
			return nil, false
		},
	}

	// Run the compile with tracing on.
	_, err := renderer.RenderAll(context.Background(), input, lookup, testHelmFactory())
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

	// === Assertion 1: per-Write() latency stayed in microsecond range ===
	// A non-blocking channel send + struct copy is hundreds of nanoseconds
	// at most. If a Write() blocked on disk I/O, it would be milliseconds
	// (1ms+). The 1ms threshold is generous: real channel sends on a slow
	// CI runner are still sub-microsecond; 1ms is 1000x larger than expected
	// success and 1/10 of expected disk-I/O failure. Signal-to-noise huge.
	const maxAcceptableWriteLatency = 1 * time.Millisecond
	if stats.MaxWriteLatency > maxAcceptableWriteLatency {
		t.Errorf("Write() max latency = %v, exceeds %v threshold; design contract violated (writer blocking on I/O?)",
			stats.MaxWriteLatency, maxAcceptableWriteLatency)
	}

	// === Assertion 2: zero saturation drops ===
	// 50 nodes × ~9 events per node ≈ 450 events; channel buffer is 1024.
	// Drops at this scale indicate the consumer goroutine isn't draining
	// (deadlock, missing goroutine, or producer-faster-than-consumer in a
	// way the buffer can't absorb).
	if stats.DroppedWrites > 0 {
		t.Errorf("DroppedWrites = %d at 50-node load; channel buffer (1024) and consumer goroutine should absorb ~450-event workload without drops",
			stats.DroppedWrites)
	}

	// === Assertion 3: Close() returns within a generous bound ===
	const maxCloseDuration = 5 * time.Second
	if closeElapsed > maxCloseDuration {
		t.Errorf("Close() took %v, exceeds %v bound; consumer goroutine may have leaked or stalled",
			closeElapsed, maxCloseDuration)
	}

	// === Assertion 4: trace directory is fully populated ===
	// Validates that async writes completed before Close() returned.
	// Slots present for sourceless+workload helm:
	expectedFiles := []string{
		"01-pillars-initial.yaml",
		"02-pass1-provides.yaml",
		"03-pass2-inject.yaml",
		"04-phaseA-inject.yaml",
		"06-phaseB-locals.yaml",
		"08-phaseB-workload.yaml",
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

	// Diagnostic logging on success — recorded in test-report for posterity
	// and future tightening of the bounds (1ms → tighter, 5s → tighter).
	t.Logf("trace stats: total writes=%d, dropped=%d, max Write() latency=%v, Close() duration=%v",
		stats.TotalWrites, stats.DroppedWrites, stats.MaxWriteLatency, closeElapsed)
}
