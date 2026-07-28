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
	w := New(root, Mode{})
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
	w := New(root, Mode{})
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

// TestWriter_NoOp_FlagOff verifies that New("", Mode{}) returns a writer whose
// Write is a no-op and produces no filesystem output. This is the
// `--trace` off path: no `.trace/` directory should appear.
func TestWriter_NoOp_FlagOff(t *testing.T) {
	t.Parallel()
	w := New("", Mode{})
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
	w := New(root, Mode{})

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
	w := New(root, Mode{})
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

// TestWriter_Stats_NoopReturnsZero pins the noopWriter branch of Stats.
// A no-op writer carries no instrumentation; Stats returns the zero
// value rather than panic on the type assertion miss.
func TestWriter_Stats_NoopReturnsZero(t *testing.T) {
	t.Parallel()
	w := New("", Mode{})
	if err := w.Write(PassPhaseA, "x", "y", "", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("noop Write: %v", err)
	}
	stats := Stats(w)
	if stats.TotalWrites != 0 || stats.DroppedWrites != 0 || stats.MaxWriteLatency != 0 {
		t.Errorf("noop writer should report zero stats; got %+v", stats)
	}
}

// TestWriter_Write_AfterCloseReturnsNil pins the closed-writer branch of
// Write. A Write after Close drops silently rather than blocking on the
// closed channel — the contract is that producers don't need to
// coordinate close ordering with the trace consumer.
func TestWriter_Write_AfterCloseReturnsNil(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root, Mode{})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Write(PassPhaseA, "x", "y", "", map[string]any{"k": "v"}); err != nil {
		t.Errorf("Write after Close should return nil; got %v", err)
	}
}

// TestWriter_Close_IsIdempotent pins the re-Close branch. The first
// Close drains the consumer goroutine; subsequent Closes are no-ops
// returning nil rather than panic on the already-closed channel.
func TestWriter_Close_IsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := New(root, Mode{})
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close should be idempotent; got %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("third Close should be idempotent; got %v", err)
	}
}

// TestWriter_Close_SurfacesConsumerError pins the consume()/persist
// error-propagation path. When persist fails (e.g., the root directory
// is unwritable), the consumer goroutine stores the first error and
// Close surfaces it.
func TestWriter_Close_SurfacesConsumerError(t *testing.T) {
	t.Parallel()
	// Root with a path component that's a file (not a directory) makes
	// MkdirAll inside persist fail.
	root := t.TempDir()
	blockerFile := filepath.Join(root, "boundaries")
	if err := os.WriteFile(blockerFile, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("setup blocker file: %v", err)
	}
	w := New(root, Mode{})
	if err := w.Write(PassPhaseA, "x", "y", "", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	closeErr := w.Close()
	if closeErr == nil {
		t.Errorf("Close should surface the persist error; got nil")
	}
}

// TestIsPhaseAOrLater_DiscriminatesPasses pins the false-branch coverage
// of the phase discriminator. Passes before Phase A return false; Phase
// A and later return true.
func TestIsPhaseAOrLater_DiscriminatesPasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pass Pass
		want bool
	}{
		{PassPhaseA, true},
		{PassPhaseB6, true},
		{PassEmission, true},
		{PassProvides, false},
		{PassPillarsInitial, false},
	}
	for _, c := range cases {
		if got := isPhaseAOrLater(c.pass); got != c.want {
			t.Errorf("isPhaseAOrLater(%v) = %v; want %v", c.pass, got, c.want)
		}
	}
}

// TestIsPhaseB0OrLater_DiscriminatesPasses pins the false-branch
// coverage of the Phase-B0-and-later discriminator. Phase A returns
// false (predates Phase B0); Phase B0 and later return true.
func TestIsPhaseB0OrLater_DiscriminatesPasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pass Pass
		want bool
	}{
		{PassPhaseB0, true},
		{PassPhaseB6, true},
		{PassEmission, true},
		{PassPhaseA, false},
		{PassProvides, false},
	}
	for _, c := range cases {
		if got := isPhaseB0OrLater(c.pass); got != c.want {
			t.Errorf("isPhaseB0OrLater(%v) = %v; want %v", c.pass, got, c.want)
		}
	}
}

// TestMemoryWriter_CollectsEntriesUnderRelativePaths verifies that the
// memory writer stores per-event payloads keyed by the same relative path
// the filesystem writer would produce. The relative-path identity is the
// load-bearing guarantee the pipeline package relies on when writing the
// collected map to .trace/ from the CLI wrapper.
func TestMemoryWriter_CollectsEntriesUnderRelativePaths(t *testing.T) {
	t.Parallel()
	w := NewMemory(Mode{})
	if err := w.Write(PassPhaseA, "api", "default", "", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("Write PassPhaseA: %v", err)
	}
	if err := w.Write(PassEmission, "api", "default", "", "log content"); err != nil {
		t.Fatalf("Write PassEmission: %v", err)
	}
	if err := w.Write(PassPhaseB3, "api", "default", "deployment.yaml", "stuff"); err != nil {
		t.Fatalf("Write PassPhaseB3 with suffix: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := w.Entries()
	wantPaths := []string{
		filepath.Join("boundaries", "default", "api", string(PassPhaseA)+".yaml"),
		filepath.Join("boundaries", "default", "api", string(PassEmission)+".log"),
		filepath.Join("boundaries", "default", "api", string(PassPhaseB3), "deployment.yaml"),
	}
	for _, p := range wantPaths {
		if _, ok := got[p]; !ok {
			t.Errorf("missing key %q in memory writer entries; got keys: %v", p, keys(got))
		}
	}
	if len(got) != len(wantPaths) {
		t.Errorf("entries count = %d, want %d", len(got), len(wantPaths))
	}
}

// TestMemoryWriter_EntriesReturnsIndependentCopy verifies the returned
// map is independent of the writer's internal state: subsequent Write
// calls should NOT mutate a previously-returned Entries() snapshot.
func TestMemoryWriter_EntriesReturnsIndependentCopy(t *testing.T) {
	t.Parallel()
	w := NewMemory(Mode{})
	if err := w.Write(PassPhaseA, "api", "default", "", "a"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	snap := w.Entries()
	if err := w.Write(PassPhaseA, "api2", "default", "", "b"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(snap) != 1 {
		t.Errorf("first snapshot mutated after subsequent Write; len = %d, want 1", len(snap))
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
