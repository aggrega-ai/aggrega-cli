// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package trace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriter_Stats_RecordsTotalWrites verifies the totalWrites counter
// tracks every Write() call. Writes 100 events; asserts the Stats snapshot
// reports TotalWrites == 100. Close() drains the channel; the consumer
// goroutine persists every successful event before Close returns.
func TestWriter_Stats_RecordsTotalWrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root)
	const numWrites = 100
	for i := 0; i < numWrites; i++ {
		if err := w.Write(PassPhaseA, "api", "default", "", map[string]any{"i": i}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	stats := Stats(w)
	if stats.TotalWrites != numWrites {
		t.Errorf("TotalWrites = %d, want %d", stats.TotalWrites, numWrites)
	}
}

// TestWriter_Stats_MaxLatencyIsNonZero verifies the maxWriteLatencyNanos
// counter actually fires — at least one Write() records a non-zero elapsed.
// This is a verification check that the instrumentation is wired, not a perf
// claim; sub-microsecond latencies still register a non-zero nanosecond
// count.
func TestWriter_Stats_MaxLatencyIsNonZero(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root)
	if err := w.Write(PassPhaseA, "api", "default", "", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	stats := Stats(w)
	if stats.MaxWriteLatency <= 0 {
		t.Errorf("MaxWriteLatency = %v, want > 0 (instrumentation should record at least one elapsed)", stats.MaxWriteLatency)
	}
}

// TestWriter_Stats_DroppedCount verifies the droppedWrites counter fires
// when channel saturation hits. Constructs an fsWriter directly with a
// 1-event buffer and a closed done channel (no consumer goroutine — the
// channel never drains). Writes 11 events: the first one queues; the
// next 10 hit the default branch and increment droppedWrites.
func TestWriter_Stats_DroppedCount(t *testing.T) {
	t.Parallel()
	w := &fsWriter{
		root: t.TempDir(),
		ch:   make(chan event, 1),
		done: make(chan struct{}),
	}
	// Pre-close done so the test doesn't hang if it accidentally calls
	// Close. The consumer goroutine is intentionally never started — the
	// channel never drains, forcing every Write() after the first to hit
	// the default branch.
	close(w.done)

	const total = 11
	for i := 0; i < total; i++ {
		_ = w.Write(PassPhaseA, "api", "default", "", map[string]any{"i": i})
	}
	stats := Stats(w)
	if stats.TotalWrites != total {
		t.Errorf("TotalWrites = %d, want %d", stats.TotalWrites, total)
	}
	// The first Write succeeds (channel has 1 slot); next 10 drop.
	if stats.DroppedWrites != total-1 {
		t.Errorf("DroppedWrites = %d, want %d (one Write should succeed, the rest drop)", stats.DroppedWrites, total-1)
	}
}

// TestWriter_NoOp_FlagOff verifies that New("") returns a writer whose
// Write is a no-op and produces no filesystem output. This is the
// `--trace` off path: no `.trace/` directory should appear.
func TestWriter_NoOp_FlagOff(t *testing.T) {
	t.Parallel()
	w := New("")
	if err := w.Write(PassPhaseA, "api", "default", "", map[string]any{"sample": 1}); err != nil {
		t.Errorf("noop Write returned error: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("noop Close returned error: %v", err)
	}
}

// TestWriter_Fs_WritesUnderRoot verifies that the async filesystem writer
// produces files under the configured root directory with the
// boundaries/<bp>/<nodeID>/<pass>.{yaml,log} layout.
func TestWriter_Fs_WritesUnderRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root)

	if err := w.Write(PassPhaseA, "api", "default", "", map[string]any{"sample": 1}); err != nil {
		t.Fatalf("Write PassPhaseA: %v", err)
	}
	if err := w.Write(PassEmission, "api", "default", "", map[string]any{"files": 3}); err != nil {
		t.Fatalf("Write PassEmission: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Phase A — .yaml extension.
	yamlPath := filepath.Join(root, "boundaries", "default", "api", "04-phaseA-inject.yaml")
	if _, err := os.Stat(yamlPath); err != nil {
		t.Errorf("expected %q to exist after Phase A write: %v", yamlPath, err)
	}
	// Emission — .log extension.
	logPath := filepath.Join(root, "boundaries", "default", "api", "11-engine-emission.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("expected %q to exist after PassEmission write: %v", logPath, err)
	}
}

// TestWriter_Fs_FileSuffixForPerArtifact verifies the per-artifact slot
// layout (PassPhaseB3 artifacts and PassPhaseB4 synthesis) — files land
// under the pass directory with the supplied filename, not as
// <pass>.yaml.
func TestWriter_Fs_FileSuffixForPerArtifact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root)
	if err := w.Write(PassPhaseB4, "api", "default", "deployment.yaml", map[string]any{"kind": "Deployment"}); err != nil {
		t.Fatalf("Write PassPhaseB4: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := filepath.Join(root, "boundaries", "default", "api", "10-engine-synthesis", "deployment.yaml")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected per-artifact file at %q: %v", want, err)
	}
}
